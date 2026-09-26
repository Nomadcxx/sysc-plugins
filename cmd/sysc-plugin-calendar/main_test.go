package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/calendar"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestRefreshRangesIncludeLookaheadAndFarPanel(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	near := calendar.PanelState{Date: now, View: calendar.ViewMonth}
	far := calendar.PanelState{Date: now.AddDate(3, 0, 0), View: calendar.ViewMonth}
	ranges := queryRanges(now, []calendar.PanelState{near, far}, "monday", nil, true)
	if len(ranges) != 2 {
		t.Fatalf("query ranges = %+v, want lookahead and far view", ranges)
	}
	if ranges[0].end.Sub(ranges[0].start) != 370*24*time.Hour {
		t.Fatalf("lookahead range = %v", ranges[0].end.Sub(ranges[0].start))
	}
	farStart, farEnd := calendar.ViewRange(far, "monday")
	if !rangeCovered(ranges[1], calendarRange{start: farStart, end: farEnd}) {
		t.Fatalf("far range does not cover the selected month: %+v", ranges[1])
	}
	if got := queryRanges(now, []calendar.PanelState{near}, "monday", ranges, false); len(got) != 0 {
		t.Fatalf("covered view queried again: %+v", got)
	}
}

func TestRefreshReplacesQueriedRangeAndKeepsPriorEventsOnOtherDates(t *testing.T) {
	zone := time.UTC
	start := time.Date(2026, 9, 15, 0, 0, 0, 0, zone)
	old := []calendar.Event{
		{ID: "stale", Summary: "Removed", Start: start.Add(time.Hour), End: start.Add(2 * time.Hour)},
		{ID: "far", Summary: "Far event", Start: start.AddDate(1, 0, 0), End: start.AddDate(1, 0, 0).Add(time.Hour)},
	}
	ranges := []calendarRange{{start: start, end: start.AddDate(0, 0, 1)}}
	newEvent := calendar.Event{ID: "new", Summary: "Added", Start: start.Add(3 * time.Hour), End: start.Add(4 * time.Hour)}
	got := replaceEventsInRanges(old, []calendar.Event{newEvent}, ranges)
	if len(got) != 2 || got[0].ID != "far" || got[1].ID != "new" {
		t.Fatalf("refreshed snapshot = %+v", got)
	}
}

func TestCalendarProcessOpensPopulatedMonthAndShowsEventDetails(t *testing.T) {
	zone, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, zone)
	fixture := calendar.Event{ID: "0123456789abcdef0123456789abcdef", CalendarID: "work", Calendar: "Work", Summary: "Product review", Location: "Room 2", URL: "https://meet.example/room", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)}
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	finished := make(chan error, 1)
	go func() {
		finished <- runPlugin(input, output, func() time.Time { return now }, func(_ context.Context, start, end time.Time) (calendar.EDSResult, error) {
			if !start.Before(end) {
				return calendar.EDSResult{}, io.ErrUnexpectedEOF
			}
			return calendar.EDSResult{Available: true, Calendars: []calendar.CalendarSource{{ID: "work", Name: "Work", Color: "#0a7bde"}}, Events: []calendar.Event{fixture}}, nil
		})
	}()

	lines := make(chan []byte, 16)
	go func() {
		scanner := bufio.NewScanner(plugin)
		for scanner.Scan() {
			lines <- append([]byte(nil), scanner.Bytes()...)
		}
		close(lines)
	}()
	sendHostJSON(t, host, map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 8}}, "capabilities": []string{"panels", "state", "clipboard-write", "open-url"}})
	if got := nextPluginLine(t, lines); messageType(got) != "plugin.hello" {
		t.Fatalf("first plugin message = %s", got)
	}
	stateCall := nextPluginLine(t, lines)
	var call v1.HostCall
	if err := json.Unmarshal(stateCall, &call); err != nil || call.Call != v1.CallStateGet {
		t.Fatalf("preference call = %s err=%v", stateCall, err)
	}
	result, _ := json.Marshal(v1.StateGetResult{Found: false})
	sendHostJSON(t, host, v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: result})
	sendHostJSON(t, host, v1.ViewOpen{Type: "view.open", ViewID: "calendar-panel", View: v1.ViewPanel, Entry: "panel", Width: calendar.PanelWidth, Height: calendar.PanelHeight})

	var populated v1.ViewSnapshot
	for {
		line := nextPluginLine(t, lines)
		if err := json.Unmarshal(line, &populated); err != nil {
			t.Fatal(err)
		}
		if populated.Type == v1.TypeViewSnapshot && findWireNode(populated.Root, "event-"+fixture.ID) != nil {
			break
		}
	}
	if err := v1.Validate(populated.Root, v1.ViewPanel); err != nil {
		t.Fatalf("populated month tree: %v", err)
	}
	sendHostJSON(t, host, v1.InputEvent{Type: "input.event", ViewID: populated.ViewID, Revision: populated.Revision, Node: "cal-view-week", Event: v1.EventActivate})
	var schedule v1.ViewSnapshot
	for {
		line := nextPluginLine(t, lines)
		if err := json.Unmarshal(line, &schedule); err != nil {
			t.Fatal(err)
		}
		if schedule.Type == v1.TypeViewSnapshot && findKind(schedule.Root, v1.KindScheduleGrid) != nil {
			break
		}
	}
	if err := v1.Validate(schedule.Root, v1.ViewPanel); err != nil {
		t.Fatalf("week schedule tree: %v", err)
	}
	sendHostJSON(t, host, v1.InputEvent{Type: "input.event", ViewID: schedule.ViewID, Revision: schedule.Revision, Node: fixture.ID, Event: v1.EventActivate})
	var details v1.ViewSnapshot
	for {
		line := nextPluginLine(t, lines)
		var snapshot v1.ViewSnapshot
		if err := json.Unmarshal(line, &snapshot); err == nil && snapshot.Type == v1.TypeViewSnapshot && findWireNode(snapshot.Root, "action-join") != nil {
			if findWireNode(snapshot.Root, "action-copy") == nil || !treeContainsText(snapshot.Root, "Product review") {
				t.Fatal("selected event detail omitted actions or event title")
			}
			details = snapshot
			break
		}
	}

	call = activateForHostCall(t, host, lines, details, "action-copy", v1.EventShortcut, v1.CallClipboardWrite)
	var clipboard v1.ClipboardWriteParams
	if err := json.Unmarshal(call.Params, &clipboard); err != nil || !strings.Contains(clipboard.Text, "Product review") || !strings.Contains(clipboard.Text, "Room 2") {
		t.Fatalf("clipboard summary = %+v err=%v", clipboard, err)
	}
	sendHostJSON(t, host, v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{}`)})
	details = waitProcessSnapshot(t, lines, details.ViewID, "Event details copied")

	call = activateForHostCall(t, host, lines, details, "action-join", v1.EventActivate, v1.CallOpenURL)
	var meeting v1.OpenURLParams
	if err := json.Unmarshal(call.Params, &meeting); err != nil || meeting.URL != fixture.URL {
		t.Fatalf("meeting URL = %+v err=%v", meeting, err)
	}
	sendHostJSON(t, host, v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{}`)})
	_ = waitProcessSnapshot(t, lines, details.ViewID, "Opening meeting link")

	sendHostJSON(t, host, v1.HostShutdown{Type: "host.shutdown"})
	_ = host.Close()
	_ = input.Close()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("calendar process did not stop")
	}
	_ = output.Close()
}

func activateForHostCall(t *testing.T, host io.Writer, lines <-chan []byte, current v1.ViewSnapshot, node string, event v1.EventKind, want v1.CallKind) v1.HostCall {
	t.Helper()
	for attempts := 0; attempts < 10; attempts++ {
		input := v1.InputEvent{Type: "input.event", ViewID: current.ViewID, Revision: current.Revision, Node: node, Event: event}
		if event == v1.EventShortcut {
			input.Key = "c"
		}
		sendHostJSON(t, host, input)
		line := nextPluginLine(t, lines)
		var call v1.HostCall
		if err := json.Unmarshal(line, &call); err == nil && call.Type == v1.TypeHostCall {
			if call.Call != want {
				t.Fatalf("%s produced host call %q, want %q", node, call.Call, want)
			}
			return call
		}
		if err := json.Unmarshal(line, &current); err != nil || current.Type != v1.TypeViewSnapshot || findWireNode(current.Root, node) == nil {
			t.Fatalf("%s produced neither its host call nor a current view: %s", node, line)
		}
	}
	t.Fatalf("%s did not produce host call %q", node, want)
	return v1.HostCall{}
}

func waitProcessSnapshot(t *testing.T, lines <-chan []byte, viewID, text string) v1.ViewSnapshot {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case line := <-lines:
			var snapshot v1.ViewSnapshot
			if json.Unmarshal(line, &snapshot) == nil && snapshot.Type == v1.TypeViewSnapshot && snapshot.ViewID == viewID && treeContainsText(snapshot.Root, text) {
				return snapshot
			}
		case <-deadline:
			t.Fatalf("view %s never showed %q", viewID, text)
		}
	}
}

func sendHostJSON(t *testing.T, writer io.Writer, message any) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func nextPluginLine(t *testing.T, lines <-chan []byte) []byte {
	t.Helper()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatal("plugin output closed unexpectedly")
		}
		return line
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

func messageType(line []byte) string {
	var message struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &message)
	return message.Type
}

func findWireNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if node := findWireNode(child, id); node != nil {
			return node
		}
	}
	return nil
}

func findKind(root *v1.Node, kind v1.NodeKind) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Kind == kind {
		return root
	}
	for _, child := range root.Children {
		if found := findKind(child, kind); found != nil {
			return found
		}
	}
	return nil
}

func treeContainsText(root *v1.Node, text string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, text) {
		return true
	}
	for _, child := range root.Children {
		if treeContainsText(child, text) {
			return true
		}
	}
	return false
}

func TestEventCardIDStripsAgendaDaySuffix(t *testing.T) {
	if got := eventIDFromNode("event-abc123-on-20260915"); got != "abc123" {
		t.Fatalf("event ID = %q", got)
	}
	if got := eventIDFromNode("event-abc123"); got != "abc123" {
		t.Fatalf("month event ID = %q", got)
	}
}
