package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	worldclock "github.com/Nomadcxx/sysc-plugins/plugins/world-clock"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const zoneTab = "JP\t+353916+1394441\tAsia/Tokyo\nAU\t-3352+15113\tAustralia/Sydney\n"
const isoTab = "AU\tAustralia\nJP\tJapan\n"

type harness struct {
	t       *testing.T
	host    io.WriteCloser
	lines   chan []byte
	done    chan error
	stopped bool
}

func start(t *testing.T, stored *v1.StateGetResult) *harness {
	t.Helper()
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &harness{t: t, host: host, lines: make(chan []byte, 64), done: make(chan error, 1)}
	now := time.Date(2026, 9, 15, 12, 0, 30, 0, time.UTC)
	go func() {
		err := runPlugin(input, output, environment{
			now: func() time.Time { return now }, local: time.UTC,
			index: worldclock.NewIndex(strings.NewReader(zoneTab), strings.NewReader(isoTab), nil),
		})
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
	call := h.nextCall(v1.CallStateGet)
	result := v1.StateGetResult{Found: false}
	if stored != nil {
		result = *stored
	}
	raw, _ := json.Marshal(result)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: raw})
	t.Cleanup(h.stop)
	return h
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
	h.t.Helper()
	select {
	case line, ok := <-h.lines:
		if !ok {
			h.t.Fatal("plugin output closed")
		}
		return line
	case <-time.After(3 * time.Second):
		h.t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

func (h *harness) nextCall(kind v1.CallKind) v1.HostCall {
	h.t.Helper()
	for {
		line := h.next()
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

func TestSubmitDuplicateShowsError(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventSubmit, Text: "tokyo"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Tokyo is already in the list") })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventSubmit, Text: "qqqq"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, `No zone matches "qqqq"`) })
}

func TestFailedSaveShowsError(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "bar:UTC", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: false, Error: "disk full"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Couldn't save zones") })
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

func TestNextMinute(t *testing.T) {
	if got := nextMinute(time.Date(2026, 1, 1, 10, 0, 45, 0, time.UTC)); got != 15*time.Second {
		t.Fatalf("next minute = %v", got)
	}
	if got := nextMinute(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)); got != time.Minute {
		t.Fatalf("on the boundary = %v", got)
	}
}
