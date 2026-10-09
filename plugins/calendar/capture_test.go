package calendar

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/calendar/screenshot.png when CAPTURE=1:
// October 2026 in month view with the 9th selected and a dozen invented
// events across two calendars.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	sources := []CalendarSource{
		{ID: "work", Name: "Work", Color: "#4f8cff"},
		{ID: "home", Name: "Home", Color: "#e0719a"},
	}
	event := func(id, cal, summary string, day, hour, minutes int) Event {
		start := time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC)
		return Event{ID: id, CalendarID: cal, Calendar: cal, Summary: summary,
			Start: start, End: start.Add(time.Duration(minutes) * time.Minute)}
	}
	events := []Event{
		event("1", "work", "Sprint planning", 5, 10, 60),
		event("2", "home", "Dentist", 7, 8, 45),
		event("3", "work", "Design review", 9, 9, 30),
		event("4", "work", "Team lunch", 9, 12, 60),
		event("5", "home", "Piano lesson", 9, 17, 45),
		event("6", "work", "Release freeze", 12, 14, 60),
		event("7", "home", "Farmers market", 17, 9, 120),
		event("8", "work", "Quarterly review", 20, 13, 90),
		event("9", "home", "Dinner with friends", 24, 19, 120),
		event("10", "work", "Demo day", 28, 15, 60),
	}
	state := PanelState{Date: now, View: ViewMonth, SelectedEventID: "3"}
	capture.Panel(t, "calendar", PanelTree(state, events, "monday", LoadStatus{Available: true, CalendarCount: 2}, now, sources, map[string]bool{}, false))
}
