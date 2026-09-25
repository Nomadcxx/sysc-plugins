package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	worldclock "github.com/Nomadcxx/sysc-plugins/plugins/world-clock"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const zoneTab = "JP\t+353916+1394441\tAsia/Tokyo\nAU\t-3352+15113\tAustralia/Sydney\nDE\t+5230+01322\tEurope/Berlin\nBM\t+3217-06446\tAtlantic/Bermuda\n"
const isoTab = "AU\tAustralia\nJP\tJapan\nDE\tGermany\nBM\tBermuda\n"

type harness struct {
	t       *testing.T
	host    io.WriteCloser
	lines   chan []byte
	done    chan error
	stopped bool
	nowMu   sync.Mutex
	now     time.Time
}

func start(t *testing.T, stored *v1.StateGetResult) *harness {
	t.Helper()
	result := v1.StateGetResult{Found: false}
	if stored != nil {
		result = *stored
	}
	raw, _ := json.Marshal(result)
	return startWith(t, nil, func(call v1.HostCall) v1.HostReply {
		return v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: raw}
	})
}

// startWith runs the plugin with env adjusted by tweak and answers its first
// state.get with getReply.
func startWith(t *testing.T, tweak func(*environment), getReply func(v1.HostCall) v1.HostReply) *harness {
	t.Helper()
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &harness{t: t, host: host, lines: make(chan []byte, 64), done: make(chan error, 1)}
	h.now = time.Date(2026, 9, 15, 12, 0, 30, 0, time.UTC)
	env := environment{
		now: h.currentTime, local: time.UTC,
		index:       worldclock.NewIndex(strings.NewReader(zoneTab), strings.NewReader(isoTab), nil),
		callTimeout: 2 * time.Second,
	}
	if tweak != nil {
		tweak(&env)
	}
	go func() {
		err := runPlugin(input, output, env)
		_ = output.Close() // ends the line reader so a test can drain everything
		h.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(plugin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			h.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(h.lines)
	}()
	h.send(map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 7}}, "capabilities": []string{"panels", "settings", "state"}})
	if got := h.next(); messageType(got) != "plugin.hello" {
		t.Fatalf("first message = %s", got)
	}
	h.send(getReply(h.nextCall(v1.CallStateGet)))
	t.Cleanup(h.stop)
	return h
}

func (h *harness) currentTime() time.Time {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	return h.now
}

func (h *harness) advanceTime(d time.Duration) {
	h.nowMu.Lock()
	h.now = h.now.Add(d)
	h.nowMu.Unlock()
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

func (h *harness) next() []byte {
	return h.nextWithin(3 * time.Second)
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

func (h *harness) nextCall(kind v1.CallKind) v1.HostCall {
	h.t.Helper()
	for {
		line := h.nextWithin(5 * time.Second)
		var call v1.HostCall
		if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil && call.Call == kind {
			return call
		}
	}
}

// snapshotWhere returns the first snapshot whose root satisfies ok.
func (h *harness) snapshotWhere(ok func(*v1.Node) bool) v1.ViewSnapshot {
	h.t.Helper()
	for {
		line := h.next()
		var s v1.ViewSnapshot
		if messageType(line) == v1.TypeViewSnapshot && json.Unmarshal(line, &s) == nil && ok(s.Root) {
			return s
		}
	}
}

func (h *harness) patchWhere(ok func(v1.ViewPatch) bool) v1.ViewPatch {
	h.t.Helper()
	for {
		line := h.nextWithin(5 * time.Second)
		var p v1.ViewPatch
		if messageType(line) == v1.TypeViewPatch && json.Unmarshal(line, &p) == nil && ok(p) {
			return p
		}
	}
}

func (h *harness) openPanel() v1.ViewSnapshot {
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel", Width: 420, Height: 360})
	return h.snapshotWhere(func(*v1.Node) bool { return true })
}

func (h *harness) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
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

func contains(root *v1.Node, text string) bool {
	if root == nil {
		return false
	}
	if root.Text == text {
		return true
	}
	for _, c := range root.Children {
		if contains(c, text) {
			return true
		}
	}
	return false
}

func TestPickSuggestionAddsZoneAndClearsSearch(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventChange, Text: "syd"})
	s := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "pick:Australia/Sydney") != nil })
	if !contains(s.Root, "Sydney · Australia · +10h") {
		t.Fatal("suggestion title")
	}
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: s.Revision, Node: "pick:Australia/Sydney", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	var params v1.StateSetParams
	_ = json.Unmarshal(call.Params, &params)
	if params.Key != "zones" || !strings.Contains(string(params.Value), `"id":"Australia/Sydney"`) {
		t.Fatalf("saved %s", call.Params)
	}
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
	after := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "drag:Australia/Sydney") != nil })
	if in := find(after.Root, "search"); in.Text != "" || in.Reseed == 0 {
		t.Fatalf("search not cleared: %+v", in)
	}
}

func TestBarOpenOnlyActivatesOnRelease(t *testing.T) {
	h := start(t, nil)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar"})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "b", Node: "open", Event: v1.EventPointer, Button: v1.ButtonPrimary})

	select {
	case line := <-h.lines:
		var call v1.HostCall
		if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil && call.Call == v1.CallPanelOpen {
			h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
			t.Fatal("bar press opened the panel before release")
		}
	case <-time.After(100 * time.Millisecond):
	}

	h.send(v1.InputEvent{Type: "input.event", ViewID: "b", Node: "open", Event: v1.EventActivate})
	call := h.nextCall(v1.CallPanelOpen)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
}

func TestSubmitDuplicateShowsError(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventSubmit, Text: "tokyo"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Tokyo is already in the list") })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventSubmit, Text: "qqqq"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, `No zone matches "qqqq"`) })
}

func TestAddUsesFirstVisibleSuggestion(t *testing.T) {
	for _, submit := range []bool{true, false} {
		name := "add button"
		if submit {
			name = "Enter"
		}
		t.Run(name, func(t *testing.T) {
			h := start(t, nil)
			p := h.openPanel()
			h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventChange, Text: "ber"})
			s := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "pick:Atlantic/Bermuda") != nil })
			if !strings.Contains(find(s.Root, "pick:Atlantic/Bermuda").Text, "Bermuda") {
				t.Fatal("Bermuda is not the first visible suggestion")
			}
			if submit {
				h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: s.Revision, Node: "search", Event: v1.EventSubmit, Text: "ber"})
			} else {
				h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: s.Revision, Node: "add", Event: v1.EventActivate})
			}
			call := h.nextCall(v1.CallStateSet)
			var params v1.StateSetParams
			_ = json.Unmarshal(call.Params, &params)
			h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
			if strings.Count(string(params.Value), `"id":"Atlantic/Bermuda"`) != 1 || strings.Count(string(params.Value), `"id":"Europe/Berlin"`) != 1 {
				t.Fatalf("added a hidden search result instead of the first visible suggestion: %s", params.Value)
			}
		})
	}
}

func TestAllAddedSearchShowsCityInsteadOfNoMatches(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventChange, Text: "tokyo"})
	s := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "search") != nil && find(n, "search").Text == "tokyo" })
	if !contains(s.Root, "Tokyo is already in the list") || contains(s.Root, "No matching zone") {
		t.Fatalf("all-added state should explain the matching city: %+v", s.Root)
	}
}

func TestFailedSaveShowsError(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "bar:UTC", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: false, Error: "disk full"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Couldn't save zones") })
}

func TestSaveAndAddErrorsBothRenderInOrderAndFit(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "bar:UTC", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: false, Error: "disk full"})
	saved := h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Couldn't save zones") })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: saved.Revision, Node: "search", Event: v1.EventSubmit, Text: "tokyo"})
	panel := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "search") != nil && find(n, "search").Text == "tokyo" })

	saveIndex, addIndex := -1, -1
	for i, child := range panel.Root.Children {
		if child.Kind != v1.KindText {
			continue
		}
		switch child.Text {
		case "Couldn't save zones":
			saveIndex = i
		case "Tokyo is already in the list":
			addIndex = i
		}
	}
	if saveIndex < 0 || addIndex < 0 || saveIndex >= addIndex {
		t.Fatalf("save and add errors missing or out of order: %+v", panel.Root.Children)
	}
	if findings := shelllint.Tree(panel.Root, v1.ViewPanel, worldclock.PanelWidth, worldclock.PanelHeight); len(findings) != 0 {
		t.Fatalf("two-error panel does not fit: %v", findings)
	}
}

func TestStoredEmptyListStaysEmpty(t *testing.T) {
	h := start(t, &v1.StateGetResult{Found: true, Value: json.RawMessage(`[]`)})
	p := h.openPanel()
	if !contains(p.Root, "No zones yet. Search for a city above.") {
		t.Fatal("empty list was reseeded")
	}
}

func TestUndecodableStateShowsDefaultsAndIsNotWritten(t *testing.T) {
	h := start(t, &v1.StateGetResult{Found: true, Value: json.RawMessage(`{"not":"a list"}`)})
	p := h.openPanel()
	if find(p.Root, "drag:Asia/Tokyo") == nil {
		t.Fatal("defaults not shown")
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	for line := range h.lines {
		var call v1.HostCall
		if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil && call.Call == v1.CallStateSet {
			t.Fatal("undecodable state was overwritten without a user change")
		}
	}
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
}

func TestSettingsSwitchBarMode(t *testing.T) {
	h := start(t, nil)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar"})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil && find(n, "open").Text == "UTC 12:00" })
	h.send(v1.SettingsChanged{Type: "settings.changed", Scope: v1.ScopePlugin, Values: map[string]any{"bar_mode": "icon"}})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil && find(n, "open").Icon == "public" })
}

func startCyclingHarness(t *testing.T) *harness {
	t.Helper()
	h := start(t, nil)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar"})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil })
	h.send(v1.SettingsChanged{Type: "settings.changed", Scope: v1.ScopePlugin, Values: map[string]any{"bar_mode": "cycle", "cycle_seconds": 3.0}})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil && find(n, "open").Text == "UTC 12:00" })
	h.patchWhere(func(p v1.ViewPatch) bool {
		return p.ViewID == "b" && len(p.Replacements) == 1 && p.Replacements[0].Key == "bar" && p.Replacements[0].Node.Text == "New York 08:00"
	})
	return h
}

func TestCycleResetsAfterAddingOnBarZone(t *testing.T) {
	h := startCyclingHarness(t)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventChange, Text: "syd"})
	s := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "pick:Australia/Sydney") != nil })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: s.Revision, Node: "pick:Australia/Sydney", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
	bar := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil })
	if got := find(bar.Root, "open").Text; got != "UTC 12:00" {
		t.Fatalf("bar after adding a zone = %q, want cycle to reset to UTC", got)
	}
}

func TestCycleResetsAfterDeletingOnBarZone(t *testing.T) {
	h := startCyclingHarness(t)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "del:America/New_York", Event: v1.EventActivate})
	d := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "del-ok") != nil })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: d.Revision, Node: "del-ok", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
	bar := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil })
	if got := find(bar.Root, "open").Text; got != "UTC 12:00" {
		t.Fatalf("bar after deleting a zone = %q, want cycle to reset to UTC", got)
	}
}

func TestCycleResetsAfterReorderingOnBarZones(t *testing.T) {
	h := startCyclingHarness(t)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "drop:0", Event: v1.EventDrop, Text: "Asia/Tokyo"})
	call := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
	bar := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil })
	if got := find(bar.Root, "open").Text; got != "Tokyo 21:00" {
		t.Fatalf("bar after reordering zones = %q, want cycle to reset to Tokyo", got)
	}
}

func TestSettingsApplyIgnoresBadValues(t *testing.T) {
	s := defaultSettings()
	s.apply(map[string]any{"hour24": "yes", "bar_mode": "sideways", "cycle_seconds": "fast"})
	if s != defaultSettings() {
		t.Fatalf("bad values changed settings: %+v", s)
	}
	s.apply(map[string]any{"cycle_seconds": 500.0})
	if s.cycle != 120*time.Second {
		t.Fatalf("cycle not clamped: %v", s.cycle)
	}
	s.apply(map[string]any{"cycle_seconds": 1.0, "hour24": false, "bar_mode": "all"})
	if s.cycle != 3*time.Second || s.hour24 || s.mode != worldclock.BarAll {
		t.Fatalf("settings = %+v", s)
	}
}

func TestTickFollowsTheWallClockMinute(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 45, 0, time.UTC)
	s := &session{env: environment{now: func() time.Time { return now }, local: time.UTC}, store: worldclock.NewStore(), views: map[string]view{}}
	if !s.maybeTick() {
		t.Fatal("first check must tick")
	}
	if s.maybeTick() {
		t.Fatal("ticked twice in one minute")
	}
	// A suspend stops monotonic timers; the wall clock jumps ahead.
	now = now.Add(47 * time.Minute)
	if !s.maybeTick() {
		t.Fatal("did not tick after the wall clock moved on")
	}
}

func TestMinuteTickPatchesStableViewsAndSnapshotsTooltip(t *testing.T) {
	h := start(t, nil)
	for _, v := range []struct {
		id   string
		kind v1.ViewKind
	}{{"b", v1.ViewBar}, {"p", v1.ViewPanel}, {"t", v1.ViewTooltip}} {
		h.send(v1.ViewOpen{Type: "view.open", ViewID: v.id, View: v.kind, Entry: "test"})
		h.snapshotWhere(func(s *v1.Node) bool { return true })
	}
	h.advanceTime(time.Minute)

	patches := map[string]v1.ViewPatch{}
	snapshots := map[string]v1.ViewSnapshot{}
	for range 3 {
		line := h.nextWithin(8 * time.Second)
		switch messageType(line) {
		case v1.TypeViewPatch:
			var p v1.ViewPatch
			if err := json.Unmarshal(line, &p); err != nil {
				t.Fatal(err)
			}
			patches[p.ViewID] = p
		case v1.TypeViewSnapshot:
			var s v1.ViewSnapshot
			if err := json.Unmarshal(line, &s); err != nil {
				t.Fatal(err)
			}
			snapshots[s.ViewID] = s
		default:
			t.Fatalf("unexpected tick message: %s", line)
		}
	}
	bar := patches["b"]
	if len(bar.Replacements) != 1 || bar.Replacements[0].Key != "bar" {
		t.Fatalf("bar replacements = %+v", bar.Replacements)
	}
	panel := patches["p"]
	keys := map[string]bool{}
	for _, replacement := range panel.Replacements {
		keys[replacement.Key] = true
	}
	for _, id := range worldclock.DefaultZones {
		for _, prefix := range []string{"clock:", "meta:", "sky:"} {
			if !keys[prefix+id] {
				t.Errorf("stable panel patch missing %s%s", prefix, id)
			}
		}
	}
	tooltip, ok := snapshots["t"]
	if !ok || !contains(tooltip.Root, "UTC 12:01 · Same time") {
		t.Fatalf("tooltip did not receive the new snapshot: %+v", tooltip)
	}
}

func TestMinuteTickSnapshotsPanelWithQuery(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventChange, Text: "syd"})
	searching := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "pick:Australia/Sydney") != nil })
	h.advanceTime(time.Minute)
	line := h.nextWithin(8 * time.Second)
	if messageType(line) != v1.TypeViewSnapshot {
		t.Fatalf("query panel update = %s, want full snapshot", line)
	}
	var snapshot v1.ViewSnapshot
	if err := json.Unmarshal(line, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ViewID != "p" || snapshot.Revision <= searching.Revision || find(snapshot.Root, "pick:Australia/Sydney") == nil {
		t.Fatalf("query panel snapshot = %+v", snapshot)
	}
}

func failGet(call v1.HostCall) v1.HostReply {
	return v1.HostReply{Type: "host.reply", ID: call.ID, OK: false, Error: "busy"}
}

func TestFailedLoadRetriesBeforeSaving(t *testing.T) {
	h := startWith(t, nil, failGet)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "bar:Asia/Tokyo", Event: v1.EventActivate})
	get := h.nextCall(v1.CallStateGet)
	raw, _ := json.Marshal(v1.StateGetResult{Found: true, Value: json.RawMessage(`["Asia/Tokyo","Europe/Oslo"]`)})
	h.send(v1.HostReply{Type: "host.reply", ID: get.ID, OK: true, Result: raw})
	set := h.nextCall(v1.CallStateSet)
	var params v1.StateSetParams
	_ = json.Unmarshal(set.Params, &params)
	if strings.Contains(string(params.Value), "UTC") || !strings.Contains(string(params.Value), "Europe/Oslo") {
		t.Fatalf("saved defaults over the stored list: %s", params.Value)
	}
	h.send(v1.HostReply{Type: "host.reply", ID: set.ID, OK: true})
}

func TestUnloadedListIsNeverSaved(t *testing.T) {
	h := startWith(t, nil, failGet)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "bar:UTC", Event: v1.EventActivate})
	h.send(failGet(h.nextCall(v1.CallStateGet)))
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Couldn't load zones") })
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	for line := range h.lines {
		var call v1.HostCall
		if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil && call.Call == v1.CallStateSet {
			t.Fatal("saved a list that was never loaded")
		}
	}
	<-h.done
}

func TestUnansweredSaveTimesOut(t *testing.T) {
	h := startWith(t, func(e *environment) { e.callTimeout = 100 * time.Millisecond }, func(call v1.HostCall) v1.HostReply {
		return v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{"found":false}`)}
	})
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "bar:UTC", Event: v1.EventActivate})
	h.nextCall(v1.CallStateSet) // never answered
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Couldn't save zones") })
}

func TestStaleSearchChangeAfterAddIsIgnored(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventChange, Text: "syd"})
	s := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "pick:Australia/Sydney") != nil })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: s.Revision, Node: "pick:Australia/Sydney", Event: v1.EventActivate})
	set := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: set.ID, OK: true})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "drag:Australia/Sydney") != nil })
	// Typed before the cleared input reached the host: generated against the
	// pre-reseed revision.
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: s.Revision, Node: "search", Event: v1.EventChange, Text: "sydx"})
	after := h.snapshotWhere(func(*v1.Node) bool { return true })
	if find(after.Root, "search").Text != "" || find(after.Root, "drag:Australia/Sydney") == nil {
		t.Fatal("a stale keystroke revived the cleared query")
	}
}
