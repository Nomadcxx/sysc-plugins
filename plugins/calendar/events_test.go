package calendar

import (
	"testing"
	"time"
)

func TestPanelsKeepIndependentDateAndViewState(t *testing.T) {
	zone, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, zone) }
	m := New(now)
	m.SelectDate("one", time.Date(2026, 1, 31, 12, 0, 0, 0, zone))
	if !m.SetPanelView("one", ViewMonth) {
		t.Fatal("month view rejected")
	}
	m.Step("one", 1)
	if got := m.Panel("one").Date; got.Year() != 2026 || got.Month() != time.February || got.Day() != 28 {
		t.Fatalf("month step = %v, want 2026-02-28", got)
	}
	m.SetPanelView("one", ViewFourDays)
	m.SelectDate("one", time.Date(2026, 10, 3, 12, 0, 0, 0, zone))
	m.Step("one", 1)
	if got := m.Panel("one").Date; got.Format("2006-01-02") != "2026-10-07" || got.Location() != zone {
		t.Fatalf("four-day DST step = %v, want local 2026-10-07", got)
	}
	if got := m.Panel("two"); got.Date.Format("2006-01-02") != "2026-09-15" || got.View != ViewMonth {
		t.Fatalf("second panel inherited state: %+v", got)
	}
}

func TestViewRangeHonoursWeekStartAndAgendaWidth(t *testing.T) {
	date := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	state := PanelState{Date: date, View: ViewWeek}
	start, end := ViewRange(state, "monday")
	if start.Format("2006-01-02") != "2026-09-14" || end.Format("2006-01-02") != "2026-09-21" {
		t.Fatalf("Monday week = %v .. %v", start, end)
	}
	start, end = ViewRange(state, "sunday")
	if start.Format("2006-01-02") != "2026-09-13" || end.Format("2006-01-02") != "2026-09-20" {
		t.Fatalf("Sunday week = %v .. %v", start, end)
	}
	state.View = ViewAgenda
	start, end = ViewRange(state, "monday")
	if end.Sub(start) != 7*24*time.Hour {
		t.Fatalf("agenda range = %v, want seven days", end.Sub(start))
	}
}

func TestEventSnapshotAndNextTimedEvent(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	events := []Event{
		{ID: "all-day", Summary: "Holiday", AllDay: true, StartDate: "2026-09-15", EndDate: "2026-09-16"},
		{ID: "later", Summary: "Later", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)},
		{ID: "active", Summary: "Active", Start: now.Add(-time.Minute), End: now.Add(time.Hour)},
	}
	m := New(func() time.Time { return now })
	if err := m.SetEvents(events); err != nil {
		t.Fatal(err)
	}
	got := m.Events()
	got[0].Summary = "mutated"
	if m.Events()[0].Summary == "mutated" {
		t.Fatal("event snapshot aliases caller data")
	}
	event, active, ok := NextTimedEvent(m.Events(), now)
	if !ok || !active || event.ID != "active" {
		t.Fatalf("active event = %+v active=%v ok=%v", event, active, ok)
	}
	filtered := []Event{events[0], events[1]}
	event, active, ok = NextTimedEvent(filtered, now)
	if !ok || active || event.ID != "later" {
		t.Fatalf("next event = %+v active=%v ok=%v", event, active, ok)
	}
	if !events[0].OccursOn(now) || events[0].OccursOn(now.AddDate(0, 0, 1)) {
		t.Fatal("all-day end date was not treated as exclusive")
	}
}

func TestSafeHTTPURL(t *testing.T) {
	for _, raw := range []string{"https://meet.example/room", "http://localhost:8080/call"} {
		if !SafeHTTPURL(raw) {
			t.Errorf("SafeHTTPURL(%q) = false", raw)
		}
	}
	for _, raw := range []string{"javascript:alert(1)", "file:///etc/passwd", "//example.test/path", "https://user:pass@example.test/", "https://x.test/\n"} {
		if SafeHTTPURL(raw) {
			t.Errorf("SafeHTTPURL(%q) = true", raw)
		}
	}
}
