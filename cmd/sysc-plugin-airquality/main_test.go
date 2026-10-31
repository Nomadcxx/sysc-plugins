package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	airquality "github.com/Nomadcxx/sysc-plugins/plugins/airquality"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// epaBody is a trimmed ArcGIS response with one Standard station near the CBD.
const epaBody = `{
  "features": [
    {"attributes": {"siteID":"1001","siteName":"Melbourne CBD","siteType":"Standard",
      "healthParameter":"PM2.5","healthAdvice":"Fair","averageValue":19.9,
      "since":1764907200000,"until":1764914400000},
     "geometry": {"x":144.9671,"y":-37.8141}}
  ]
}`

const dashboardBody = `{
  "sals": ["21640"],
  "locations": [{
    "location": {"id": 27593, "suburb": "Melbourne", "state": "VIC", "sal": "21640"},
    "current_conditions": {"date": "2026-10-31", "day_title": "Later today"},
    "measures": [
      {"name": "Grass", "text": "High", "value": 2, "background_colour": "#fe9a1f"},
      {"name": "Air Quality", "text": "Good", "value": 1,
       "show_metric": {"subcat": "PM2.5", "value": 4.2, "units": "ug/m3"}}
    ],
    "forecast": [
      {"date": "2026-11-01", "day_title": "Sunday",
       "measures": [{"name": "Grass", "text": "Moderate", "value": 1}]}
    ]
  }],
  "attribution": "Data provided in collaboration with the University of Tasmania and University of Melbourne"
}`

// recorder answers HTTP requests from a table of path prefixes, so a test can
// drive the real clients without a network.
type recorder struct {
	mu     sync.Mutex
	routes []route
	paths  []string
}

type route struct {
	suffix string
	body   string
	status int
}

func (r *recorder) Do(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, req.URL.String())
	for _, route := range r.routes {
		if strings.Contains(req.URL.Path, route.suffix) {
			status := route.status
			if status == 0 {
				status = 200
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(route.body)),
				Header:     http.Header{},
				Request:    req,
			}, nil
		}
	}
	return &http.Response{
		StatusCode: 404,
		Body:       io.NopCloser(strings.NewReader("no route")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func (r *recorder) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func (r *recorder) hits(fragment string) int {
	n := 0
	for _, p := range r.requested() {
		if strings.Contains(p, fragment) {
			n++
		}
	}
	return n
}

type harness struct {
	t             *testing.T
	host          io.WriteCloser
	lines         chan []byte
	notifications chan v1.NotifyParams
	done          chan error
	stopped       bool
	nowMu         sync.Mutex
	now           time.Time
	closed        chan struct{}
	once          sync.Once
	viewMu        sync.Mutex
	views         int
	stateMu       sync.Mutex
	saved         []byte
}

// start runs the plugin with settings and a recorder. The harness answers every
// host call itself: state reads return stored (nil means "nothing saved") and
// everything else succeeds, so a test only has to watch the view traffic.
func start(t *testing.T, rec *recorder, stored *v1.StateGetResult, tweak func(*settings)) *harness {
	t.Helper()
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &harness{
		t: t, host: host, lines: make(chan []byte, 256), notifications: make(chan v1.NotifyParams, 8), done: make(chan error, 1),
		closed: make(chan struct{}),
	}
	h.now = time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)

	env := environment{now: h.currentTime, do: rec}
	base := defaultSettings()
	if tweak != nil {
		tweak(&base)
	}
	stateResult := v1.StateGetResult{Found: false}
	if stored != nil {
		stateResult = *stored
	}

	go func() {
		err := runPluginWith(input, output, env, base)
		_ = output.Close()
		h.done <- err
	}()

	// One goroutine owns the plugin's output: it answers calls inline (the
	// plugin blocks on each one) and forwards everything else to the test.
	go func() {
		sc := bufio.NewScanner(plugin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			var call v1.HostCall
			if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil {
				reply := v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{}`)}
				switch call.Call {
				case v1.CallStateGet:
					raw, _ := json.Marshal(stateResult)
					reply.Result = raw
				case v1.CallStateSet:
					// Capture the last write so a restart test can restore
					// exactly what the plugin saved.
					var params v1.StateSetParams
					if json.Unmarshal(call.Params, &params) == nil {
						h.stateMu.Lock()
						h.saved = append([]byte(nil), params.Value...)
						h.stateMu.Unlock()
					}
				case v1.CallNotify:
					var params v1.NotifyParams
					if json.Unmarshal(call.Params, &params) == nil {
						h.notifications <- params
					}
				}
				data, _ := json.Marshal(reply)
				if _, err := h.host.Write(append(data, '\n')); err != nil {
					return
				}
				continue
			}
			select {
			case h.lines <- line:
			case <-h.closed:
				return
			}
		}
		close(h.lines)
	}()

	h.send(map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 10}}, "capabilities": []string{"notifications", "panels", "settings", "state"}})
	if got := h.next(); messageType(got) != "plugin.hello" {
		t.Fatalf("first message = %s", got)
	}
	t.Cleanup(h.stop)
	return h
}

func (h *harness) currentTime() time.Time {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	return h.now
}

func (h *harness) send(message any) {
	h.t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.host.Write(append(data, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) nextWithin(timeout time.Duration) []byte {
	h.t.Helper()
	select {
	case line, ok := <-h.lines:
		if !ok {
			h.t.Fatal("plugin output closed")
		}
		return line
	case <-time.After(timeout):
		h.t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

func (h *harness) next() []byte { return h.nextWithin(5 * time.Second) }

// snapshotWhere waits for a snapshot whose root satisfies ok.
func (h *harness) snapshotWhere(ok func(*v1.Node) bool) v1.ViewSnapshot {
	h.t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		line := h.nextWithin(6 * time.Second)
		var s v1.ViewSnapshot
		if messageType(line) == v1.TypeViewSnapshot && json.Unmarshal(line, &s) == nil && ok(s.Root) {
			return s
		}
	}
	h.t.Fatal("no matching snapshot")
	return v1.ViewSnapshot{}
}

// openPanel opens a panel and waits for the first snapshot carrying a reading,
// so a test never races the initial refresh.
func (h *harness) openPanel() v1.ViewSnapshot {
	id := fmt.Sprintf("p%d", h.nextView())
	h.send(v1.ViewOpen{Type: "view.open", ViewID: id, View: v1.ViewPanel, Entry: "panel",
		Width: airquality.PanelWidth, Height: airquality.PanelHeight})
	// Wait for any real reading rather than one specific measure: a test may
	// enable only Open-Meteo, whose panel never mentions grass or µg/m³.
	return h.snapshotFor(id, func(root *v1.Node) bool {
		text := allText(root)
		return !strings.Contains(text, "No reading")
	})
}

// openBar opens a bar and waits for its snapshot at that width.
func (h *harness) openBar(width int) v1.ViewSnapshot {
	id := fmt.Sprintf("b%d", h.nextView())
	h.send(v1.ViewOpen{Type: "view.open", ViewID: id, View: v1.ViewBar, Entry: "bar",
		Width: width, Height: shelllint.BarHeight})
	return h.snapshotFor(id, func(*v1.Node) bool { return true })
}

func (h *harness) nextView() int {
	h.viewMu.Lock()
	defer h.viewMu.Unlock()
	h.views++
	return h.views
}

// snapshotFor waits for a snapshot of one view satisfying ok.
func (h *harness) snapshotFor(id string, ok func(*v1.Node) bool) v1.ViewSnapshot {
	h.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		line := h.nextWithin(8 * time.Second)
		var s v1.ViewSnapshot
		if messageType(line) != v1.TypeViewSnapshot || json.Unmarshal(line, &s) != nil {
			continue
		}
		if s.ViewID != id || !ok(s.Root) {
			continue
		}
		return s
	}
	h.t.Fatalf("no matching snapshot for view %s", id)
	return v1.ViewSnapshot{}
}

func (h *harness) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	h.once.Do(func() { close(h.closed) })
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		h.t.Fatal("plugin did not stop")
	}
}

func messageType(line []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &m)
	return m.Type
}

func find(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if n := find(c, id); n != nil {
			return n
		}
	}
	return nil
}

func allText(root *v1.Node) string {
	if root == nil {
		return ""
	}
	out := root.Text + "\n"
	for _, c := range root.Children {
		out += allText(c)
	}
	return out
}

// EPA still works alone when the user turns off AirHealth and Open-Meteo.
func TestEPAOnlyRendersReading(t *testing.T) {
	rec := &recorder{routes: []route{{suffix: "FeatureServer/0/query", body: epaBody}}}
	h := start(t, rec, nil, func(s *settings) {
		s.airHealth = false
		s.openMeteo = false
	})

	panel := h.openPanel()
	if text := allText(panel.Root); !strings.Contains(text, "Melbourne CBD") {
		t.Fatalf("panel does not name the station: %s", text)
	}
	if got := rec.hits("airhealthservices"); got != 0 {
		t.Fatalf("opted-out source was called %d times", got)
	}
	if rec.hits("FeatureServer") == 0 {
		t.Fatal("EPA was never queried")
	}
}

// Turning AirHealth on must reach it and display its readings and attribution.
func TestAirHealthReadingsWhenEnabled(t *testing.T) {
	rec := &recorder{routes: []route{
		{suffix: "/locations/", body: `{"locations":[{"id":863,"suburb":"Melbourne","state":"VIC","SAL":"21640","SA3":"20604","use_sal":false}]}`},
		{suffix: "/dashboard/metricdash/SAL/21640/", body: dashboardBody},
	}}
	h := start(t, rec, nil, func(s *settings) { s.airHealth = true; s.epa = false; s.openMeteo = false })

	panel := h.openPanel()
	text := allText(panel.Root)
	for _, want := range []string{"Grass pollen", "High", "University of Melbourne"} {
		if !strings.Contains(text, want) {
			t.Errorf("panel missing %q: %s", want, text)
		}
	}
	if rec.hits("airhealthservices") == 0 {
		t.Fatal("AirHealth was enabled but its host was not recorded")
	}
}

func TestPartialSourceFailureKeepsPreviousReport(t *testing.T) {
	previous := airquality.Report{
		UpdatedAt: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC),
		Snapshot: airquality.Snapshot{
			Location: airquality.Location{Suburb: "Melbourne", State: "VIC", SAL: "21640"},
			Measures: []airquality.Measure{{
				Name: airquality.MeasureGrass, Title: "Grass pollen",
				Level:  airquality.Level{Name: "Low", Severity: airquality.SeverityGood},
				Source: "airhealth",
			}},
		},
	}
	raw, err := airquality.EncodeReport(previous)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{routes: []route{
		{suffix: "/locations/", status: http.StatusServiceUnavailable, body: "down"},
		{suffix: "FeatureServer/0/query", body: strings.Replace(epaBody, `"healthAdvice":"Fair"`, `"healthAdvice":"Very Poor"`, 1)},
	}}
	h := start(t, rec, &v1.StateGetResult{Found: true, Value: raw}, func(s *settings) {
		s.airHealth = true
		s.epa = true
		s.openMeteo = false
	})
	id := fmt.Sprintf("partial%d", h.nextView())
	h.send(v1.ViewOpen{Type: "view.open", ViewID: id, View: v1.ViewPanel, Entry: "panel",
		Width: airquality.PanelWidth, Height: airquality.PanelHeight})
	panel := h.snapshotFor(id, func(root *v1.Node) bool {
		return strings.Contains(allText(root), "AirHealth pollen")
	})
	text := allText(panel.Root)
	if !strings.Contains(text, "Grass pollen") || !strings.Contains(text, "stale") {
		t.Fatalf("partial refresh dropped the previous pollen reading or its stale mark: %s", text)
	}
	select {
	case notification := <-h.notifications:
		if notification.Summary != "Air quality very poor" {
			t.Fatalf("partial refresh notification = %q, want the fresh EPA warning", notification.Summary)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh high reading did not notify during a partial source failure")
	}
}

// A stored report from a previous run must be drawn before any refresh
// completes, and it must be labelled stale.
func TestRestoredReportIsStale(t *testing.T) {
	stored := airquality.Report{
		UpdatedAt: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC),
		Snapshot: airquality.Snapshot{
			Location: airquality.Location{Suburb: "Melbourne"},
			Measures: []airquality.Measure{
				{Name: airquality.MeasureGrass, Level: airquality.Level{Name: "High", Severity: airquality.SeverityHigh}},
			},
		},
	}
	raw, err := airquality.EncodeReport(stored)
	if err != nil {
		t.Fatal(err)
	}
	// Fail every refresh so the restored copy is what the panel shows.
	rec := &recorder{routes: []route{{suffix: "FeatureServer/0/query", status: 503, body: "down"}}}
	h := start(t, rec, &v1.StateGetResult{Found: true, Value: raw}, nil)

	id := fmt.Sprintf("r%d", h.nextView())
	h.send(v1.ViewOpen{Type: "view.open", ViewID: id, View: v1.ViewPanel, Entry: "panel",
		Width: airquality.PanelWidth, Height: airquality.PanelHeight})
	panel := h.snapshotFor(id, func(root *v1.Node) bool {
		return strings.Contains(allText(root), "High")
	})
	if !strings.Contains(allText(panel.Root), "stale") {
		t.Fatalf("restored reading is not marked stale: %s", allText(panel.Root))
	}
}

// The bar must lint at every width the host can open it, including the side
// bar widths a vertical panel uses.
func TestBarFitsHostWidths(t *testing.T) {
	rec := &recorder{routes: []route{{suffix: "FeatureServer/0/query", body: epaBody}}}
	h := start(t, rec, nil, nil)
	for _, width := range []int{shelllint.BarWidth, 96, 64, 40, 32} {
		snap := h.openBar(width)
		for _, finding := range shelllint.Tree(snap.Root, v1.ViewBar, width, shelllint.BarHeight) {
			t.Errorf("width %d: %s", width, finding)
		}
	}
}

// The settings message must rebuild the views rather than wait for a refresh,
// and an instance-scoped bar setting must change the label.
func TestBarShowSettingChangesLabel(t *testing.T) {
	rec := &recorder{routes: []route{{suffix: "FeatureServer/0/query", body: epaBody}}}
	h := start(t, rec, nil, nil)
	h.openBar(shelllint.BarWidth)

	h.send(v1.SettingsChanged{Type: "settings.changed", Scope: v1.ScopeInstance, Values: map[string]any{"show": "icon"}})
	snap := h.snapshotWhere(func(root *v1.Node) bool {
		open := find(root, "open")
		if open == nil {
			return false
		}
		for _, c := range open.Children {
			if c.Kind == v1.KindText {
				return false
			}
		}
		return len(open.Children) == 1
	})
	for _, finding := range shelllint.Tree(snap.Root, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
		t.Errorf("icon-only bar: %s", finding)
	}
}

// A failed refresh with no prior reading must not crash or draw a fake zero.
func TestTotalFailureStillAnswers(t *testing.T) {
	rec := &recorder{routes: []route{{suffix: "FeatureServer/0/query", status: 500, body: "boom"}}}
	h := start(t, rec, nil, nil)
	id := fmt.Sprintf("f%d", h.nextView())
	h.send(v1.ViewOpen{Type: "view.open", ViewID: id, View: v1.ViewPanel, Entry: "panel",
		Width: airquality.PanelWidth, Height: airquality.PanelHeight})
	panel := h.snapshotFor(id, func(root *v1.Node) bool {
		return strings.Contains(allText(root), "EPA Victoria")
	})
	for _, finding := range shelllint.Tree(panel.Root, v1.ViewPanel, airquality.PanelWidth, airquality.PanelHeight) {
		t.Errorf("failure panel: %s", finding)
	}
	if text := allText(panel.Root); strings.Contains(text, "0 µg") {
		t.Fatalf("failure panel invented a reading: %s", text)
	}
}

func TestSettingsApplyAllowsWorldwideCoordinates(t *testing.T) {
	s := defaultSettings()
	s.apply(map[string]any{
		"refresh_minutes": float64(1), "latitude": float64(51.5072), "longitude": float64(-0.1276),
		"radius_km": float64(500), "site": "  Melbourne CBD  ",
	})
	if s.refresh != 5*time.Minute {
		t.Errorf("refresh = %v, want the 5m floor", s.refresh)
	}
	if s.latitude != 51.5072 || s.longitude != -0.1276 {
		t.Errorf("location = %v,%v, want London coordinates", s.latitude, s.longitude)
	}
	s.apply(map[string]any{"latitude": float64(91), "longitude": float64(-181)})
	if s.latitude != 51.5072 || s.longitude != -0.1276 {
		t.Errorf("invalid coordinates changed location to %v,%v", s.latitude, s.longitude)
	}
	if s.radiusKM != 100 {
		t.Errorf("radius = %v, want the 100km ceiling", s.radiusKM)
	}
	if s.site != "Melbourne CBD" {
		t.Errorf("site = %q, want it trimmed", s.site)
	}
}

func TestSourcesRouteByProviderCoverage(t *testing.T) {
	cases := []struct {
		name           string
		lat, lon       float64
		airHealth, epa bool
	}{
		{"Melbourne", -37.8136, 144.9631, true, true},
		{"Canberra", -35.2809, 149.1300, true, true},
		{"Sydney", -33.8688, 151.2093, true, true},
		{"Perth", -31.9505, 115.8605, true, true},
		{"London", 51.5072, -0.1276, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := defaultSettings()
			s.latitude, s.longitude = tc.lat, tc.lon
			sources := s.sources()
			if sources.AirHealth != tc.airHealth || sources.EPA != tc.epa || !sources.OpenMeteo {
				t.Fatalf("sources = %+v, want AirHealth=%v EPA=%v OpenMeteo=true", sources, tc.airHealth, tc.epa)
			}
		})
	}
}

func TestAirHealthIsEnabledByDefaultAndCanBeTurnedOff(t *testing.T) {
	s := defaultSettings()
	if !s.airHealth {
		t.Fatal("AirHealth is disabled by default")
	}
	s.apply(map[string]any{"source_airhealth": false})
	if s.airHealth {
		t.Fatal("explicit AirHealth opt-out was ignored")
	}
}

// A settings message that omits a key must not reset it: the host sends the
// scope's whole map, and a widget setting legitimately absent from it.
func TestSettingsApplyKeepsAbsentKeys(t *testing.T) {
	s := defaultSettings()
	s.epa = false
	s.apply(map[string]any{"refresh_minutes": float64(30)})
	if s.epa {
		t.Error("absent source_epa reset the value")
	}
	if s.refresh != 30*time.Minute {
		t.Errorf("refresh = %v", s.refresh)
	}
}

func TestBarChoiceRejectsUnknown(t *testing.T) {
	if got := barChoice("nonsense"); got != "worst" {
		t.Errorf("barChoice(nonsense) = %q", got)
	}
	if got := barChoice("storm"); got != "storm" {
		t.Errorf("barChoice(storm) = %q", got)
	}
}

// planAlerts must fire once on the transition into risk, not on every refresh,
// and thunderstorm asthma must alert even when another measure ranks worse.
func TestPlanAlertsNotifiesOnTransitionOnly(t *testing.T) {
	cfg := defaultSettings()
	seen := map[string]airquality.Severity{}
	risky := airquality.Report{Snapshot: airquality.Snapshot{Measures: []airquality.Measure{
		{Name: airquality.MeasureGrass, Level: airquality.Level{Name: "High", Index: 2, Severity: airquality.SeverityHigh}},
		{Name: airquality.MeasureThunderstorm, Level: airquality.Level{Name: "Moderate", Index: 1, Severity: airquality.SeverityModerate}},
	}}}

	// Grass outranks the storm on severity, so a worst-measure-only check
	// would miss the storm entirely.
	got, seen := planAlerts(risky, cfg, seen)
	if len(got) != 2 {
		t.Fatalf("first pass raised %d alerts, want 2: %+v", len(got), got)
	}
	var storm bool
	for _, a := range got {
		if a.measure == airquality.MeasureThunderstorm && a.urgency == v1.UrgencyCritical {
			storm = true
		}
	}
	if !storm {
		t.Fatalf("no critical storm alert: %+v", got)
	}

	// An identical second pass must be silent.
	if got, _ := planAlerts(risky, cfg, seen); len(got) != 0 {
		t.Fatalf("repeat pass raised %+v", got)
	}

	// Dropping to Low re-arms; coming back up alerts again.
	quiet := airquality.Report{Snapshot: airquality.Snapshot{Measures: []airquality.Measure{
		{Name: airquality.MeasureGrass, Level: airquality.Level{Name: "Low", Index: 0, Severity: airquality.SeverityGood}},
		{Name: airquality.MeasureThunderstorm, Level: airquality.Level{Name: "Low", Index: 0, Severity: airquality.SeverityGood}},
	}}}
	_, seen = planAlerts(quiet, cfg, seen)
	again, _ := planAlerts(risky, cfg, seen)
	if len(again) != 2 {
		t.Fatalf("re-armed pass raised %d alerts, want 2", len(again))
	}
}

// Notifications must respect the settings toggles.
func TestPlanAlertsHonoursToggles(t *testing.T) {
	cfg := defaultSettings()
	cfg.notifyHigh = false
	cfg.notifyStorm = false
	report := airquality.Report{Snapshot: airquality.Snapshot{Measures: []airquality.Measure{
		{Name: airquality.MeasureGrass, Level: airquality.Level{Name: "Extreme", Index: 3, Severity: airquality.SeverityHigh}},
		{Name: airquality.MeasureThunderstorm, Level: airquality.Level{Name: "High", Index: 2, Severity: airquality.SeverityHigh}},
	}}}
	if got, _ := planAlerts(report, cfg, map[string]airquality.Severity{}); len(got) != 0 {
		t.Fatalf("raised %+v with both toggles off", got)
	}
}

// An unknown or absent measure must never be treated as a low reading, or an
// out-of-season forecast would clear a real alert.
func TestPlanAlertsIgnoresUnknownLevels(t *testing.T) {
	cfg := defaultSettings()
	seen := map[string]airquality.Severity{airquality.MeasureGrass: airquality.SeverityHigh}
	report := airquality.Report{Snapshot: airquality.Snapshot{Measures: []airquality.Measure{
		{Name: airquality.MeasureGrass, Level: airquality.Level{Name: "Unknown"}},
	}}}
	got, next := planAlerts(report, cfg, seen)
	if len(got) != 0 {
		t.Fatalf("unexpected alert %+v", got)
	}
	if next[airquality.MeasureGrass] != airquality.SeverityHigh {
		t.Fatalf("unknown level overwrote the known state: %v", next)
	}
}

// Open-Meteo is the only source of a UV index, so enabling it alone must put
// UV on the panel even with no other source available.
func TestOpenMeteoSuppliesTooltipAndPanel(t *testing.T) {
	rec := &recorder{routes: []route{{suffix: "/v1/air-quality", body: openMeteoBody}}}
	h := start(t, rec, nil, func(s *settings) {
		s.epa = false
		s.airHealth = false
		s.openMeteo = true
	})

	panel := h.openPanel()
	text := allText(panel.Root)
	for _, want := range []string{"Air quality", "UV index", "Moderate", "CAMS"} {
		if !strings.Contains(text, want) {
			t.Errorf("panel missing %q: %s", want, text)
		}
	}
	if rec.hits("/v1/air-quality") == 0 {
		t.Fatal("Open-Meteo was never queried")
	}
}

func TestRequestRefreshKeepsNewestSettingsSnapshot(t *testing.T) {
	s := &session{settings: defaultSettings(), wake: make(chan refreshRequest, 1)}
	s.requestRefresh()
	s.settings.latitude = -33.8688
	s.settings.longitude = 151.2093
	s.generation = 1
	s.requestRefresh()

	request := <-s.wake
	if request.sources.Latitude != -33.8688 || request.sources.Longitude != 151.2093 {
		t.Fatalf("queued refresh uses coordinates %v,%v, want Sydney", request.sources.Latitude, request.sources.Longitude)
	}
	if request.generation != 1 {
		t.Fatalf("queued generation = %d, want 1", request.generation)
	}
	if len(s.wake) != 0 {
		t.Fatalf("queued %d refreshes, want one coalesced request", len(s.wake))
	}
}

func TestRetryActivationQueuesRefresh(t *testing.T) {
	s := &session{settings: defaultSettings(), wake: make(chan refreshRequest, 1)}
	s.handle(context.Background(), &v1.InputEvent{Node: "refresh", Event: v1.EventActivate})
	if len(s.wake) != 1 {
		t.Fatal("Retry activation did not queue a refresh")
	}
	<-s.wake
	s.handle(context.Background(), &v1.InputEvent{Node: "refresh", Event: v1.EventChange})
	if len(s.wake) != 0 {
		t.Fatal("a non-activation event queued a refresh")
	}
}

func TestHandleRefreshIgnoresSupersededGeneration(t *testing.T) {
	s := &session{generation: 2}
	s.cache.Put(airquality.Report{Snapshot: airquality.Snapshot{Measures: []airquality.Measure{{
		Name: airquality.MeasureGrass, Level: airquality.Level{Name: "High", Severity: airquality.SeverityHigh},
	}}}})
	s.handleRefresh(context.Background(), refreshResult{
		generation: 1,
		report: airquality.Report{Snapshot: airquality.Snapshot{Measures: []airquality.Measure{{
			Name: airquality.MeasureUV, Level: airquality.Level{Name: "Low", Severity: airquality.SeverityGood},
		}}}},
	})
	current, _ := s.cache.Get()
	if _, ok := current.Snapshot.Measure(airquality.MeasureGrass); !ok {
		t.Fatal("superseded refresh replaced the current report")
	}
	if _, ok := current.Snapshot.Measure(airquality.MeasureUV); ok {
		t.Fatal("superseded refresh was applied")
	}
}

// The UV row must survive the state round-trip, or a restart loses it.
func TestOpenMeteoReadingPersistsAcrossRestart(t *testing.T) {
	rec := &recorder{routes: []route{{suffix: "/v1/air-quality", body: openMeteoBody}}}
	h := start(t, rec, nil, func(s *settings) {
		s.epa = false
		s.airHealth = false
		s.openMeteo = true
	})
	panel := h.openPanel()
	if text := allText(panel.Root); !strings.Contains(text, "UV index") {
		t.Fatalf("panel has no UV row: %s", text)
	}
	h.stop()

	// A second run with no network at all, restoring the saved state.
	offline := &recorder{routes: []route{{suffix: "/v1/air-quality", status: 503, body: "down"}}}
	h2 := start(t, offline, h.storedState(), func(s *settings) {
		s.epa = false
		s.airHealth = false
		s.openMeteo = true
	})
	restored := h2.openPanel()
	text := allText(restored.Root)
	if !strings.Contains(text, "UV index") {
		t.Fatalf("restored panel lost the UV row: %s", text)
	}
}

const openMeteoBody = `{
 "current_units":{"pm2_5":"μg/m³","ozone":"μg/m³","uv_index":"","us_aqi":"USAQI"},
 "current":{"time":"2026-10-31T19:00","pm2_5":4.3,"ozone":57.0,"nitrogen_dioxide":3.1,"uv_index":0.1,"us_aqi":28},
 "daily_units":{"time":"iso8601","uv_index_max":""},
 "daily":{"time":["2026-10-31","2026-11-01"],"uv_index_max":[4.9,4.55]}
}`

// storedState returns what the plugin last persisted, shaped for a restart.
func (h *harness) storedState() *v1.StateGetResult {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if len(h.saved) == 0 {
		h.t.Fatal("plugin saved no state")
	}
	return &v1.StateGetResult{Found: true, Value: append([]byte(nil), h.saved...)}
}
