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

	// The test body and the auto-reply goroutine below both write to host;
	// io.Pipe allows only one writer at a time.
	hostOut := &lockedWriter{w: host}
	finished := make(chan error, 1)
	go func() {
		finished <- runPlugin(input, output, func() time.Time { return now }, func(_ context.Context, start, end time.Time) (calendar.EDSResult, error) {
			if !start.Before(end) {
				return calendar.EDSResult{}, io.ErrUnexpectedEOF
			}
			return calendar.EDSResult{Available: true, Calendars: []calendar.CalendarSource{{ID: "work", Name: "Work", Color: "#0a7bde"}}, Events: []calendar.Event{fixture}}, nil
		})
	}()

	stamped := make(chan []byte, 16)
	go func() {
		scanner := bufio.NewScanner(plugin)
		for scanner.Scan() {
			stamped <- append([]byte(nil), scanner.Bytes()...)
		}
		close(stamped)
	}()
	// Snapshot publishing adds state.set host calls; answer them in-line so
	// the strict message script below only sees what it expects.
	lines := make(chan []byte, 16)
	go func() {
		for line := range stamped {
			var call v1.HostCall
			if json.Unmarshal(line, &call) == nil && call.Type == v1.TypeHostCall && call.Call == v1.CallStateSet {
				reply, _ := json.Marshal(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{}`)})
				_, _ = host.Write(append(reply, '\n'))
				continue
			}
			lines <- line
		}
		close(lines)
	}()
	sendHostJSON(t, hostOut, map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 8}}, "capabilities": []string{"panels", "state", "clipboard-write", "open-url"}})
	if got := nextPluginLine(t, lines); messageType(got) != "plugin.hello" {
		t.Fatalf("first plugin message = %s", got)
	}
	stateCall := nextPluginLine(t, lines)
	var call v1.HostCall
	if err := json.Unmarshal(stateCall, &call); err != nil || call.Call != v1.CallStateGet {
		t.Fatalf("preference call = %s err=%v", stateCall, err)
	}
	result, _ := json.Marshal(v1.StateGetResult{Found: false})
	sendHostJSON(t, hostOut, v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: result})
	sendHostJSON(t, hostOut, v1.ViewOpen{Type: "view.open", ViewID: "calendar-panel", View: v1.ViewPanel, Entry: "panel", Width: calendar.PanelWidth, Height: calendar.PanelHeight})

	// Snapshot publishes can land while the test reads; keep the LAST
	// event-bearing snapshot so the input revision is never stale.
	var populated v1.ViewSnapshot
	for {
		line, ok := nextPluginLineIdle(t, lines, 300*time.Millisecond)
		if !ok {
			break
		}
		var snapshot v1.ViewSnapshot
		if err := json.Unmarshal(line, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Type == v1.TypeViewSnapshot && findWireNode(snapshot.Root, "event-"+fixture.ID) != nil {
			populated = snapshot
		}
	}
	if populated.Revision == 0 {
		t.Fatal("no populated month snapshot")
	}
	if err := v1.Validate(populated.Root, v1.ViewPanel); err != nil {
		t.Fatalf("populated month tree: %v", err)
	}
	sendHostJSON(t, hostOut, v1.InputEvent{Type: "input.event", ViewID: populated.ViewID, Revision: populated.Revision, Node: "cal-view-week", Event: v1.EventActivate})
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
	sendHostJSON(t, hostOut, v1.InputEvent{Type: "input.event", ViewID: schedule.ViewID, Revision: schedule.Revision, Node: fixture.ID, Event: v1.EventActivate})
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

	call = activateForHostCall(t, hostOut, lines, details, "action-copy", v1.EventShortcut, v1.CallClipboardWrite)
	var clipboard v1.ClipboardWriteParams
	if err := json.Unmarshal(call.Params, &clipboard); err != nil || !strings.Contains(clipboard.Text, "Product review") || !strings.Contains(clipboard.Text, "Room 2") {
		t.Fatalf("clipboard summary = %+v err=%v", clipboard, err)
	}
	sendHostJSON(t, hostOut, v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{}`)})
	details = waitProcessSnapshot(t, lines, details.ViewID, "Event details copied")

	call = activateForHostCall(t, hostOut, lines, details, "action-join", v1.EventActivate, v1.CallOpenURL)
	var meeting v1.OpenURLParams
	if err := json.Unmarshal(call.Params, &meeting); err != nil || meeting.URL != fixture.URL {
		t.Fatalf("meeting URL = %+v err=%v", meeting, err)
	}
	sendHostJSON(t, hostOut, v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{}`)})
	_ = waitProcessSnapshot(t, lines, details.ViewID, "Opening meeting link")

	sendHostJSON(t, hostOut, v1.HostShutdown{Type: "host.shutdown"})
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

func TestCalendarHostCallDoesNotBlockLaterInputOrShutdown(t *testing.T) {
	const responsivenessBudget = 100 * time.Millisecond
	const startupBudget = 2 * time.Second
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	hostOut := &lockedWriter{w: host}
	finished := make(chan error, 1)
	go func() {
		finished <- runPlugin(input, output, func() time.Time { return now }, func(ctx context.Context, _, _ time.Time) (calendar.EDSResult, error) {
			<-ctx.Done()
			return calendar.EDSResult{}, ctx.Err()
		})
	}()
	var heldCallID string
	ended := false
	t.Cleanup(func() {
		if !ended {
			if heldCallID != "" {
				data, _ := json.Marshal(v1.HostReply{Type: "host.reply", ID: heldCallID, OK: true, Result: json.RawMessage(`{}`)})
				_, _ = hostOut.Write(append(data, '\n'))
			}
			_ = sendHostJSONNoFail(hostOut, v1.HostShutdown{Type: "host.shutdown"})
		}
		_ = host.Close()
		_ = input.Close()
		_ = output.Close()
		if !ended {
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Error("calendar process did not stop during cleanup")
			}
		}
	})

	stamped := make(chan []byte, 32)
	go func() {
		for scanner := bufio.NewScanner(plugin); scanner.Scan(); {
			stamped <- append([]byte(nil), scanner.Bytes()...)
		}
		close(stamped)
	}()
	lines := make(chan []byte, 32)
	go func() {
		for line := range stamped {
			var call v1.HostCall
			if json.Unmarshal(line, &call) == nil && call.Type == v1.TypeHostCall && (call.Call == v1.CallStateGet || call.Call == v1.CallStateSet) {
				var result json.RawMessage = json.RawMessage(`{}`)
				if call.Call == v1.CallStateGet {
					result, _ = json.Marshal(v1.StateGetResult{Found: false})
				}
				data, _ := json.Marshal(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: result})
				_, _ = hostOut.Write(append(data, '\n'))
				continue
			}
			lines <- line
		}
		close(lines)
	}()

	sendHostJSON(t, hostOut, map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 8}}, "capabilities": []string{"panels", "state"}})
	if got := nextPluginLineWithin(t, lines, startupBudget); messageType(got) != "plugin.hello" {
		t.Fatalf("first plugin message = %s", got)
	}
	sendHostJSON(t, hostOut, v1.ViewOpen{Type: "view.open", ViewID: "calendar-bar", View: v1.ViewBar, Width: 240, Instance: "bar-instance", Output: "DP-1", Generation: 7})
	bar := nextViewSnapshotWithin(t, lines, "calendar-bar", startupBudget)
	sendHostJSON(t, hostOut, v1.InputEvent{Type: "input.event", ViewID: bar.ViewID, Revision: bar.Revision, Node: "calendar-open", Event: v1.EventActivate})
	line := nextPluginLineWithin(t, lines, responsivenessBudget)
	var call v1.HostCall
	if err := json.Unmarshal(line, &call); err != nil || call.Call != v1.CallPanelOpen {
		t.Fatalf("calendar-open host call = %s, err=%v", line, err)
	}
	heldCallID = call.ID
	var panelParams v1.PanelParams
	if err := json.Unmarshal(call.Params, &panelParams); err != nil || panelParams.Instance != "bar-instance" || panelParams.Output != "DP-1" || panelParams.Generation != 7 {
		t.Fatalf("panel-open params = %+v, err=%v", panelParams, err)
	}

	sendHostJSON(t, hostOut, v1.ViewOpen{Type: "view.open", ViewID: "calendar-panel", View: v1.ViewPanel, Entry: "panel", Width: calendar.PanelWidth, Height: calendar.PanelHeight})
	panel := nextViewSnapshotWithin(t, lines, "calendar-panel", responsivenessBudget)
	title := findWireNode(panel.Root, "cal-title")
	if title == nil {
		t.Fatal("calendar panel omitted its title")
	}
	sendHostJSON(t, hostOut, v1.InputEvent{Type: "input.event", ViewID: panel.ViewID, Revision: panel.Revision, Node: "cal-next", Event: v1.EventActivate})
	updated := nextViewSnapshotWithin(t, lines, "calendar-panel", responsivenessBudget)
	updatedTitle := findWireNode(updated.Root, "cal-title")
	if updated.Revision <= panel.Revision || updatedTitle == nil || updatedTitle.Text == title.Text {
		t.Fatalf("later input did not update the panel: before=%q after=%v", title.Text, updatedTitle)
	}

	sendHostJSON(t, hostOut, v1.HostShutdown{Type: "host.shutdown"})
	select {
	case err := <-finished:
		ended = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(responsivenessBudget):
		t.Fatal("shutdown waited for the held panel.open reply")
	}
}

func nextPluginLineWithin(t *testing.T, lines <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatal("plugin output closed unexpectedly")
		}
		return line
	case <-timer.C:
		t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

func nextViewSnapshotWithin(t *testing.T, lines <-chan []byte, viewID string, timeout time.Duration) v1.ViewSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Until(deadline) > 0 {
		line := nextPluginLineWithin(t, lines, time.Until(deadline))
		var snapshot v1.ViewSnapshot
		if json.Unmarshal(line, &snapshot) == nil && snapshot.Type == v1.TypeViewSnapshot && snapshot.ViewID == viewID {
			return snapshot
		}
	}
	t.Fatalf("timed out waiting for snapshot %s", viewID)
	return v1.ViewSnapshot{}
}

func sendHostJSONNoFail(writer io.Writer, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(data, '\n'))
	return err
}

func activateForHostCall(t *testing.T, host io.Writer, lines <-chan []byte, current v1.ViewSnapshot, node string, event v1.EventKind, want v1.CallKind) v1.HostCall {
	t.Helper()
	input := v1.InputEvent{Type: "input.event", ViewID: current.ViewID, Revision: current.Revision, Node: node, Event: event}
	if event == v1.EventShortcut {
		input.Key = "c"
	}
	sendHostJSON(t, host, input)
	for {
		line := nextPluginLine(t, lines)
		var call v1.HostCall
		if err := json.Unmarshal(line, &call); err == nil && call.Type == v1.TypeHostCall {
			if call.Call != want {
				t.Fatalf("%s produced host call %q, want %q", node, call.Call, want)
			}
			return call
		}
	}
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

func nextPluginLineIdle(t *testing.T, lines <-chan []byte, idle time.Duration) ([]byte, bool) {
	t.Helper()
	timer := time.NewTimer(idle)
	defer timer.Stop()
	select {
	case line, open := <-lines:
		if !open {
			return nil, false
		}
		return line, true
	case <-timer.C:
		return nil, false
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
