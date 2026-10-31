package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	airquality "github.com/Nomadcxx/sysc-plugins/plugins/airquality"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The plugin's identity, matching manifest.json. identity.FromManifest reads
// the installed manifest and falls back to these values, so a renamed build
// still handshakes as itself.
const (
	pluginID      = "org.sysc.airquality"
	pluginName    = "Air Quality & Pollen"
	pluginVersion = "0.1.0"
)

const (
	// requestTimeout bounds one refresh. Both sources are fast; a slow one is
	// treated as down rather than allowed to starve the next tick.
	requestTimeout = 20 * time.Second
	// callTimeout bounds every host call. Calls run on the loop, and a reply
	// queued behind a burst of input would otherwise never be read.
	callTimeout = 5 * time.Second
	// stateKey is the host-state slot holding the last good report.
	stateKey = "report"
	// immediateDelay is how long after startup the first refresh runs, so the
	// handshake and the initial snapshots are not blocked on the network.
	immediateDelay = 250 * time.Millisecond
)

func main() {
	env := environment{now: time.Now, do: http.DefaultClient}
	if err := runPlugin(os.Stdin, os.Stdout, env); err != nil {
		os.Exit(1)
	}
}

type environment struct {
	now func() time.Time
	// do performs HTTP requests; tests substitute a recorder.
	do airquality.Doer
}

// settings is the plugin's configuration, mirroring manifest.json. Fields are
// plain values with the manifest's defaults so a missing key is harmless.
type settings struct {
	airHealth   bool
	epa         bool
	openMeteo   bool
	latitude    float64
	longitude   float64
	site        string
	refresh     time.Duration
	radiusKM    float64
	notifyHigh  bool
	notifyStorm bool
	// barShow picks which measure the bar widget displays. It is a widget
	// setting, so it arrives in an instance-scoped message.
	barShow string
}

func defaultSettings() settings {
	return settings{
		airHealth:   true,
		epa:         true,
		openMeteo:   true,
		latitude:    airquality.DefaultLatitude,
		longitude:   airquality.DefaultLongitude,
		refresh:     15 * time.Minute,
		radiusKM:    airquality.DefaultRadiusKM,
		notifyHigh:  true,
		notifyStorm: true,
		barShow:     "worst",
	}
}

// apply takes the values the host sent. A missing or mistyped key keeps its
// current value rather than resetting it, because the host sends the whole
// scope's map and a plugin setting may legitimately be absent from it.
func (s *settings) apply(values map[string]any) {
	if v, ok := values["source_airhealth"].(bool); ok {
		s.airHealth = v
	}
	if v, ok := values["source_epa"].(bool); ok {
		s.epa = v
	}
	if v, ok := values["source_openmeteo"].(bool); ok {
		s.openMeteo = v
	}
	if v, ok := values["latitude"].(float64); ok {
		if v >= -90 && v <= 90 {
			s.latitude = v
		}
	}
	if v, ok := values["longitude"].(float64); ok {
		if v >= -180 && v <= 180 {
			s.longitude = v
		}
	}
	if v, ok := values["site"].(string); ok {
		s.site = strings.TrimSpace(v)
	}
	if v, ok := values["refresh_minutes"].(float64); ok {
		s.refresh = time.Duration(clamp(v, 5, 180)) * time.Minute
	}
	if v, ok := values["radius_km"].(float64); ok {
		s.radiusKM = clamp(v, 1, 100)
	}
	if v, ok := values["notify_high"].(bool); ok {
		s.notifyHigh = v
	}
	if v, ok := values["notify_storm"].(bool); ok {
		s.notifyStorm = v
	}
	if v, ok := values["show"].(string); ok {
		s.barShow = v
	}
}

func clamp(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}

func (s settings) sources() airquality.Sources {
	return airquality.Sources{
		AirHealth: s.airHealth && airquality.AirHealthSupportsCoordinates(s.latitude, s.longitude),
		EPA:       s.epa && airquality.EPAVictoriaMayCoverCoordinates(s.latitude, s.longitude),
		OpenMeteo: s.openMeteo,
		Latitude:  s.latitude,
		Longitude: s.longitude,
		Site:      s.site,
		RadiusKM:  s.radiusKM,
	}
}

type view struct {
	kind  v1.ViewKind
	rev   uint64
	width int
}

type session struct {
	env      environment
	client   *v1.Client
	settings settings
	views    map[string]view
	cache    airquality.Cache
	// minor is the host's protocol minor from the handshake. The half gauge
	// arrived in minor twenty-four; labels and category-only grass require 25.
	minor int
	// generation invalidates refreshes started before source settings changed.
	generation uint64
	// wake holds one pending immutable request. The main loop is its only sender.
	wake chan refreshRequest
	// loaded is false until state.get has answered. Saving before then would
	// overwrite the stored report with an empty one.
	loaded bool
	// notified tracks the last severity announced per measure so a refresh
	// that finds the same High grass count does not notify twice.
	notified map[string]airquality.Severity
	// lastErr is the most recent refresh error, shown in the panel and
	// cleared by a success.
	lastErr string
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	return runPluginWith(in, out, env, defaultSettings())
}

// runPluginWith is runPlugin with settings injected, so a test can start the
// loop already configured instead of racing a settings message against the
// first refresh.
func runPluginWith(in io.Reader, out io.Writer, env environment, initial settings) error {
	c := v1.NewClient(in, out)
	hello, err := c.Handshake(identity.FromManifest(v1.Identity{ID: pluginID, Name: pluginName, Version: pluginVersion}))
	if err != nil {
		return err
	}
	// The version the client selected is not exposed after the handshake, so
	// negotiate the same way it did: highest single-digit major the host
	// offers that this build understands.
	protocol, _ := v1.NegotiateProtocol(hello.Supported)
	s := &session{
		env: env, client: c, settings: initial, minor: protocol.Minor,
		views: map[string]view{}, wake: make(chan refreshRequest, 1),
		notified: map[string]airquality.Severity{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			incoming <- msg
		}
	}()

	s.restore(ctx)

	// The refresher owns the network. Keeping it off the loop means a slow
	// source delays a reading, never the handshake or a keypress.
	results := make(chan refreshResult, 1)
	go s.refreshLoop(ctx, results)

	var timer *time.Timer
	var timerC <-chan time.Time
	armTimer := func(d time.Duration) {
		if timer != nil {
			timer.Stop()
		}
		timer = time.NewTimer(d)
		timerC = timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	// The first reading should not wait a whole refresh interval. The timer is
	// the trigger; asking the refresher directly as well would fire twice.
	armTimer(immediateDelay)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timerC:
			s.requestRefresh()
			armTimer(s.settings.refresh)
		case result := <-results:
			s.handleRefresh(ctx, result)
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View, width: m.Width}
				s.snapshot(m.ViewID)
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := s.views[m.ViewID]; ok {
					v.rev = 0
					s.views[m.ViewID] = v
					s.snapshot(m.ViewID)
				}
			case *v1.InputEvent:
				s.handle(ctx, m)
			case *v1.SettingsChanged:
				before := s.settings.sources()
				s.settings.apply(m.Values)
				armTimer(s.settings.refresh)
				// A location or source change invalidates the reading; a bar
				// label change only needs a redraw.
				after := s.settings.sources()
				if before != after {
					s.generation++
					s.lastErr = "refreshing with updated settings"
					// Do not leave the old-location result looking current while
					// the new request is in flight.
					s.cache.Keep([]string{s.lastErr})
					s.requestRefresh()
				}
				s.snapshotAll()
			}
		}
	}
}

// refreshResult carries one finished refresh back to the loop.
type refreshResult struct {
	report     airquality.Report
	err        error
	generation uint64
}

type refreshRequest struct {
	sources    airquality.Sources
	generation uint64
}

// refreshLoop serialises refreshes. It coalesces: at most one refresh is
// queued behind the running one, so a burst of refresh triggers costs two
// requests, not one per trigger.
func (s *session) refreshLoop(ctx context.Context, results chan<- refreshResult) {
	for {
		var request refreshRequest
		select {
		case <-ctx.Done():
			return
		case request = <-s.wake:
		}
		// The fetch gets its own deadline so a hung source cannot stop the
		// loop forever; the parent context still cancels the whole plugin.
		fetchCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		fetcher := airquality.Fetcher{
			AirHealth: airquality.AirHealth{Do: s.env.do},
			EPA:       airquality.EPAArcGIS{Do: s.env.do},
			OpenMeteo: airquality.OpenMeteo{Do: s.env.do},
			Now:       s.env.now,
		}
		report, err := fetcher.Fetch(fetchCtx, request.sources)
		cancel()
		select {
		case results <- refreshResult{report: report, err: err, generation: request.generation}:
		case <-ctx.Done():
			return
		}
	}
}

// requestRefresh asks the refresher for a reading without blocking. A trigger
// already pending is the same request, so a full slot is success.
func (s *session) requestRefresh() {
	request := refreshRequest{sources: s.settings.sources(), generation: s.generation}
	select {
	case s.wake <- request:
	default:
		// Keep only the newest settings snapshot while one refresh is running.
		select {
		case <-s.wake:
		default:
		}
		s.wake <- request
	}
}

func (s *session) handleRefresh(ctx context.Context, result refreshResult) {
	if result.generation != s.generation {
		return
	}
	if result.err != nil || len(result.report.Snapshot.Measures) == 0 {
		problem := "no reading"
		if result.err != nil {
			problem = result.err.Error()
		}
		// Keep the last good reading rather than replacing it with nothing;
		// the panel marks it stale and names what failed.
		if kept, ok := s.cache.Keep([]string{problem}); ok {
			s.lastErr = problem
			s.save(ctx, kept)
			s.snapshotAll()
			return
		}
		s.lastErr = problem
		s.snapshotAll()
		return
	}
	if len(result.report.Errors) > 0 {
		// Keep the last complete report so a partial refresh cannot replace and
		// permanently discard a provider's last reading.
		if kept, ok := s.cache.Keep(result.report.Errors); ok {
			// ponytail: alert from measures this refresh actually returned while
			// retaining the last complete panel snapshot; unavailable sources do
			// not suppress a fresh warning from another provider.
			s.announce(ctx, result.report)
			s.lastErr = strings.Join(result.report.Errors, "; ")
			s.save(ctx, kept)
			s.snapshotAll()
			return
		}
	}
	s.lastErr = ""
	s.cache.Put(result.report)
	s.save(ctx, result.report)
	s.announce(ctx, result.report)
	s.snapshotAll()
}

// alert is one notification worth raising, decided from a report without
// sending anything.
type alert struct {
	measure string
	summary string
	body    string
	urgency v1.Urgency
	level   airquality.Severity
}

// planAlerts decides what to notify about. It is pure: it returns the alerts to
// raise and the new "last announced" state, so the transition rules can be
// tested without a host connection.
//
// A measure notifies when it reaches its threshold and did not last time, which
// makes a high reading held all afternoon one interruption rather than eight,
// and re-arms the alert once the reading drops back.
func planAlerts(report airquality.Report, cfg settings, seen map[string]airquality.Severity) ([]alert, map[string]airquality.Severity) {
	next := make(map[string]airquality.Severity, len(seen)+len(report.Snapshot.Measures))
	for k, v := range seen {
		next[k] = v
	}
	var out []alert
	for _, m := range report.Snapshot.Measures {
		if !m.Level.Known() {
			continue
		}
		next[m.Name] = m.Level.Severity
		switch {
		case m.Name == airquality.MeasureThunderstorm:
			// Thunderstorm asthma is checked on its own rather than through the
			// worst measure: it matters even when pollen ranks higher, and it is
			// the one reading that changes what a sufferer does with their day.
			if cfg.notifyStorm && m.Level.Severity >= airquality.SeverityModerate && seen[m.Name] < airquality.SeverityModerate {
				out = append(out, alert{
					measure: m.Name,
					summary: "Thunderstorm asthma risk",
					body:    stormBody(m),
					urgency: v1.UrgencyCritical,
					level:   m.Level.Severity,
				})
			}
		default:
			if cfg.notifyHigh && m.Level.Severity >= airquality.SeverityHigh && seen[m.Name] < airquality.SeverityHigh {
				out = append(out, alert{
					measure: m.Name,
					summary: airquality.Title(m.Name) + " " + strings.ToLower(m.Level.Name),
					body:    measureBody(m),
					urgency: v1.UrgencyNormal,
					level:   m.Level.Severity,
				})
			}
		}
	}
	return out, next
}

// announce raises the notifications planAlerts decided on and records what was
// said, so the next refresh compares against it.
func (s *session) announce(ctx context.Context, report airquality.Report) {
	alerts, next := planAlerts(report, s.settings, s.notified)
	s.notified = next
	for _, a := range alerts {
		s.notify(ctx, a.summary, a.body, a.urgency)
	}
}

func stormBody(m airquality.Measure) string {
	return "The epidemic thunderstorm asthma forecast for your area is " +
		strings.ToLower(m.Level.Name) + ". Avoid exposure before storms and keep reliever medication nearby."
}

func measureBody(m airquality.Measure) string {
	body := airquality.Title(m.Name) + " is " + strings.ToLower(m.Level.Name)
	if m.Metric != nil {
		body += fmt.Sprintf(" (%.1f %s)", m.Metric.Value, m.Metric.Units)
	}
	return body + "."
}

func (s *session) notify(ctx context.Context, summary, body string, urgency v1.Urgency) {
	_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{Summary: summary, Body: body, Urgency: urgency, TimeoutMS: 8000})
}

func (s *session) call(ctx context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
	if callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}
	return s.client.Call(ctx, kind, params)
}

// restore loads the last good report so the panel has something to draw before
// the first refresh returns. A restored report is stale by definition.
func (s *session) restore(ctx context.Context) {
	reply, err := s.call(ctx, v1.CallStateGet, v1.StateGetParams{Key: stateKey})
	if err != nil || !reply.OK {
		return
	}
	s.loaded = true
	var result v1.StateGetResult
	if json.Unmarshal(reply.Result, &result) != nil || !result.Found {
		return
	}
	report, ok := airquality.DecodeReport(result.Value)
	if !ok {
		return
	}
	s.cache.Restore(report)
}

// save writes the report for the next start. A failed save is not worth
// surfacing: the reading on screen is still correct, and the next refresh
// retries.
func (s *session) save(ctx context.Context, report airquality.Report) {
	if !s.loaded {
		return
	}
	raw, err := airquality.EncodeReport(report)
	if err != nil {
		return
	}
	_, _ = s.call(ctx, v1.CallStateSet, v1.StateSetParams{Key: stateKey, Value: raw})
}

// current returns the report to draw, with the panel's error line folded in.
func (s *session) current() airquality.Report {
	report, ok := s.cache.Get()
	if !ok {
		report = airquality.Report{Stale: true}
	}
	if s.lastErr != "" && len(report.Errors) == 0 {
		report.Errors = []string{s.lastErr}
	}
	return report
}

func (s *session) tree(v view) *v1.Node {
	report := s.current()
	switch v.kind {
	case v1.ViewBar:
		return airquality.BarFor(report, barChoice(s.settings.barShow), v.width)
	case v1.ViewTooltip:
		return airquality.TooltipFor(report, v.width)
	default:
		return airquality.PanelFor(report, s.minor)
	}
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	v.rev++
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, s.tree(v))
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}

func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	switch {
	case m.Node == "open" && m.Event == v1.EventActivate:
		_, _ = s.call(ctx, v1.CallPanelOpen, v1.PanelParams{
			Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID,
		})
	case m.Node == "refresh" && m.Event == v1.EventActivate:
		s.requestRefresh()
	}
}

// barChoice validates a widget setting. An unknown value falls back to the
// worst measure rather than drawing nothing.
func barChoice(v string) string {
	switch v {
	case "worst", "grass", "air", "storm", "icon":
		return v
	}
	return "worst"
}
