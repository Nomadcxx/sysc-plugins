package calendar

import (
	"testing"
	"time"
)

func TestBuildControlCenterSnapshotBoundsAndCounts(t *testing.T) {
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.Local)
	mk := func(id string, start, end time.Time) Event {
		return Event{ID: id, Calendar: "Work", Summary: id, Start: start, End: end}
	}
	events := []Event{
		mk("past", now.Add(-48*time.Hour), now.Add(-47*time.Hour)),
		mk("now", now.Add(time.Hour), now.Add(2*time.Hour)),
		{ID: "allday", Calendar: "Leave", Summary: "Holiday", AllDay: true, StartDate: "2026-09-29", EndDate: "2026-10-01"},
	}
	for i := 0; i < maxSnapshotUpcoming+4; i++ {
		events = append(events, mk("future", now.Add(time.Duration(i)*time.Hour), now.Add(time.Duration(i)*time.Hour+time.Hour)))
	}
	snapshot := BuildControlCenterSnapshot(events, 2, now)
	if snapshot.Sources != 2 || snapshot.Generated == "" {
		t.Fatalf("header = %+v", snapshot)
	}
	if snapshot.Days["2026-09-26"] != 1 {
		t.Fatalf("past day not counted for grid markers: %v", snapshot.Days)
	}
	if snapshot.Days["2026-09-29"] != 1 || snapshot.Days["2026-09-30"] != 1 {
		t.Fatalf("all-day span counts = %v", snapshot.Days)
	}
	if len(snapshot.Upcoming) != maxSnapshotUpcoming {
		t.Fatalf("upcoming %d, want cap %d", len(snapshot.Upcoming), maxSnapshotUpcoming)
	}
	if snapshot.Upcoming[0].Summary != "now" {
		t.Fatalf("upcoming not start-ordered: %+v", snapshot.Upcoming)
	}
	if _, err := time.Parse(time.RFC3339, snapshot.Upcoming[0].Start); err != nil {
		t.Fatalf("upcoming start %q: %v", snapshot.Upcoming[0].Start, err)
	}
}
