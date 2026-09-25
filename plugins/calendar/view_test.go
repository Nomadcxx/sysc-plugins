package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestCalendarPanelBuildsAllFiveViewsAndSelectedDayAgenda(t *testing.T) {
	zone, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, zone)
	events := []Event{
		{ID: "morning", Summary: "Product review", Calendar: "Work", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour), Marker: "#0a7bde"},
		{ID: "leave", Summary: "Leave", Calendar: "Personal", AllDay: true, StartDate: "2026-09-15", EndDate: "2026-09-17", Marker: "secondary"},
	}
	status := LoadStatus{Available: true, CalendarCount: 2}
	sources := []CalendarSource{{ID: "work", Name: "Work", Color: "#0a7bde"}, {ID: "personal", Name: "Personal"}}
	for _, mode := range []ViewMode{ViewMonth, ViewWeek, ViewFourDays, ViewDay, ViewAgenda} {
		state := PanelState{Date: now, View: mode}
		root := PanelTree(state, events, "monday", status, now, sources, nil, false)
		if err := v1.Validate(root, v1.ViewPanel); err != nil {
			t.Fatalf("%s tree validation: %v", mode, err)
		}
		findKind(t, root, v1.KindSegmented)
		if mode == ViewWeek || mode == ViewFourDays || mode == ViewDay {
			grid := findKind(t, root, v1.KindScheduleGrid)
			if grid.Schedule.Days != viewDays(mode) || len(grid.Schedule.Events) != 2 {
				t.Fatalf("%s schedule = %+v", mode, grid.Schedule)
			}
		}
		if mode == ViewMonth {
			if findNode(root, "event-morning") == nil || findNode(root, "event-leave") == nil {
				t.Fatal("selected day's agenda omitted an event")
			}
			if findNode(root, "cal-date-20260915") == nil {
				t.Fatal("month grid omitted the selected date")
			}
		}
		if findings := lint.Tree(root, v1.ViewPanel, 1040, 760); len(findings) != 0 {
			t.Errorf("%s panel does not fit: %+v", mode, findings)
		}
	}
}

func TestCalendarPanelDetailsAndProviderStates(t *testing.T) {
	date := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	event := Event{ID: "meeting", Summary: "Design review", Description: "Review the first release", Location: "Room 2", URL: "https://meet.example/room", Calendar: "Work", Start: date.Add(time.Hour), End: date.Add(2 * time.Hour)}
	root := PanelTree(PanelState{Date: date, View: ViewMonth, Details: true, SelectedEventID: event.ID}, []Event{event}, "monday", LoadStatus{Available: true, CalendarCount: 1}, date, nil, nil, false)
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cal-back", "action-join", "action-copy"} {
		if findNode(root, id) == nil {
			t.Errorf("details omitted %q", id)
		}
	}
	for name, status := range map[string]LoadStatus{
		"loading":        {Loading: true},
		"unavailable":    {Error: "EDS is not running"},
		"no calendars":   {Available: true},
		"read error":     {Available: true, CalendarCount: 1, Error: "connection timed out"},
		"stale snapshot": {Available: true, CalendarCount: 1, Error: "connection timed out", Stale: true},
	} {
		stateRoot := PanelTree(PanelState{Date: date, View: ViewAgenda}, nil, "monday", status, date, nil, nil, false)
		if err := v1.Validate(stateRoot, v1.ViewPanel); err != nil {
			t.Errorf("%s tree validation: %v", name, err)
		}
		if findNode(stateRoot, "cal-retry") == nil && name != "loading" {
			t.Errorf("%s state omitted retry", name)
		}
	}
}

func TestExpandedCalendarFilterFitsEveryPanelView(t *testing.T) {
	date := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	sources := make([]CalendarSource, 12)
	for i := range sources {
		sources[i] = CalendarSource{ID: "source-" + string(rune('a'+i)), Name: "Calendar " + string(rune('A'+i))}
	}
	for _, mode := range []ViewMode{ViewMonth, ViewWeek, ViewFourDays, ViewDay, ViewAgenda} {
		root := PanelTree(PanelState{Date: date, View: mode}, nil, "monday", LoadStatus{Available: true, CalendarCount: len(sources)}, date, sources, nil, true)
		if err := v1.Validate(root, v1.ViewPanel); err != nil {
			t.Fatalf("%s expanded tree validation: %v", mode, err)
		}
		if findNode(root, CalendarToggleNodeID(sources[len(sources)-1].ID)) == nil {
			t.Errorf("%s expanded calendar list omitted its final source", mode)
		}
		if findings := lint.Tree(root, v1.ViewPanel, 1040, 760); len(findings) != 0 {
			t.Errorf("%s expanded panel does not fit: %+v", mode, findings)
		}
	}
}

func TestBarCountdownAndTooltipHaveAccessibleEventContext(t *testing.T) {
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	event := Event{ID: "standup", Summary: "Daily standup with the whole team", Location: "Room 4", URL: "https://meet.example/standup", Start: now.Add(25 * time.Minute), End: now.Add(time.Hour)}
	bar := BarTree([]Event{event}, now)
	if err := v1.Validate(bar, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(barTooltip(bar), "Room 4") || !strings.Contains(barTooltip(bar), "Meeting link available") {
		t.Fatalf("bar tooltip lacks full details: %q", barTooltip(bar))
	}
	tooltip := TooltipTree([]Event{event}, now)
	if err := v1.Validate(tooltip, v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if !treeText(tooltip, "25m") {
		t.Fatal("countdown is missing from the bar and tooltip")
	}
}

func findKind(t *testing.T, root *v1.Node, kind v1.NodeKind) *v1.Node {
	t.Helper()
	if root.Kind == kind {
		return root
	}
	for _, child := range root.Children {
		if found := findKindMaybe(child, kind); found != nil {
			return found
		}
	}
	t.Fatalf("tree omitted node kind %q", kind)
	return nil
}

func findKindMaybe(root *v1.Node, kind v1.NodeKind) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Kind == kind {
		return root
	}
	for _, child := range root.Children {
		if found := findKindMaybe(child, kind); found != nil {
			return found
		}
	}
	return nil
}

func findNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

func viewDays(mode ViewMode) int {
	switch mode {
	case ViewWeek:
		return 7
	case ViewFourDays:
		return 4
	default:
		return 1
	}
}

func barTooltip(root *v1.Node) string {
	if root == nil {
		return ""
	}
	if root.Tooltip != "" {
		return root.Tooltip
	}
	for _, child := range root.Children {
		if value := barTooltip(child); value != "" {
			return value
		}
	}
	return ""
}

func treeText(root *v1.Node, value string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, value) {
		return true
	}
	for _, child := range root.Children {
		if treeText(child, value) {
			return true
		}
	}
	return false
}
