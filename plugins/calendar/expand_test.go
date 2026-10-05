package calendar

import (
	"testing"
	"time"
)

func octWeek(t *testing.T) (time.Time, time.Time) {
	t.Helper()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return start, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
}

func TestExpandInstancesExpandsWeeklyMaster(t *testing.T) {
	start, end := octWeek(t)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\n" +
		"UID:standup@x\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T093000Z\r\n" +
		"RRULE:FREQ=WEEKLY;BYDAY=MO\r\nSUMMARY:Standup\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	instances, truncated, err := expandInstances(ics, start.Unix(), end.Unix())
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(instances) != 4 {
		t.Fatalf("expanded %d occurrences, want 4 Mondays in October", len(instances))
	}
	events, errs := parseICalendar("work", "Work", "", instances[0])
	if len(errs) != 0 || len(events) != 1 {
		t.Fatalf("parse instance: %v, %v", errs, events)
	}
	want := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	if !events[0].Start.Equal(want) {
		t.Fatalf("first occurrence starts %v, want %v", events[0].Start, want)
	}
	// Occurrences of one series must have distinct IDs or merge-by-ID collapses them.
	ids := map[string]bool{}
	for _, ical := range instances {
		evs, errs := parseICalendar("work", "Work", "", ical)
		if len(errs) != 0 || len(evs) != 1 {
			t.Fatalf("parse instance: %v, %v", errs, evs)
		}
		if ids[evs[0].ID] {
			t.Fatalf("occurrence %v shares ID with a sibling occurrence", evs[0].Start)
		}
		ids[evs[0].ID] = true
	}
}

func TestExpandInstancesHonorsExdateUntilAndRdate(t *testing.T) {
	start, end := octWeek(t)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\n" +
		"UID:h\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T093000Z\r\n" +
		"RRULE:FREQ=WEEKLY;BYDAY=MO;UNTIL=20260210T090000Z\r\n" +
		"RDATE:20261020T090000Z\r\nEXDATE:20261012T090000Z\r\nSUMMARY:Hybrid\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	instances, _, err := expandInstances(ics, start.Unix(), end.Unix())
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expanded %d, want 1 (only RDATE in range)", len(instances))
	}
	evs, errs := parseICalendar("work", "Work", "", instances[0])
	if len(errs) != 0 || len(evs) != 1 {
		t.Fatalf("parse: %v, %v", errs, evs)
	}
	want := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	if !evs[0].Start.Equal(want) {
		t.Fatalf("RDATE occurrence starts %v, want %v", evs[0].Start, want)
	}
}

func TestExpandInstancesKeepsAllDayAndDetachedOverride(t *testing.T) {
	start, end := octWeek(t)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:f\r\nDTSTART;VALUE=DATE:20260105\r\nDTEND;VALUE=DATE:20260106\r\n" +
		"RRULE:FREQ=WEEKLY;BYDAY=MO\r\nSUMMARY:AllDay\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	instances, _, err := expandInstances(ics, start.Unix(), end.Unix())
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(instances) != 4 {
		t.Fatalf("expanded %d all-day, want 4", len(instances))
	}
	evs, errs := parseICalendar("work", "Work", "", instances[0])
	if len(errs) != 0 || len(evs) != 1 {
		t.Fatalf("parse: %v, %v", errs, evs)
	}
	if !evs[0].AllDay || evs[0].StartDate != "2026-10-05" || evs[0].EndDate != "2026-10-06" {
		t.Fatalf("all-day occurrence = %+v", evs[0])
	}

	// Master plus a detached override for 20261019: the overridden slot must
	// produce one entry (the moved one), not the master time as well.
	moved := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:m\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T093000Z\r\n" +
		"RRULE:FREQ=WEEKLY;BYDAY=MO\r\nSUMMARY:Standup\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:m\r\nRECURRENCE-ID:20261019T090000Z\r\n" +
		"DTSTART:20261019T110000Z\r\nDTEND:20261019T113000Z\r\nSUMMARY:Moved\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	instances, _, err = expandInstances(moved, start.Unix(), end.Unix())
	if err != nil {
		t.Fatalf("expand with override: %v", err)
	}
	seen := map[string]bool{}
	for _, ical := range instances {
		evs, errs := parseICalendar("work", "Work", "", ical)
		if len(errs) != 0 || len(evs) != 1 {
			t.Fatalf("parse: %v, %v", errs, evs)
		}
		seen[evs[0].Start.Format(time.RFC3339)] = true
	}
	if seen["2026-10-19T09:00:00Z"] {
		t.Fatal("overridden occurrence still present at the master time")
	}
	if !seen["2026-10-19T11:00:00Z"] {
		t.Fatal("detached override occurrence missing")
	}
	if len(instances) != 4 {
		t.Fatalf("expanded %d with override, want 4", len(instances))
	}
}

func TestExpandInstancesKeepsTimezone(t *testing.T) {
	start, end := octWeek(t)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\n" +
		"UID:g\r\nDTSTART;TZID=Europe/London:20260105T090000\r\nDTEND;TZID=Europe/London:20260105T093000\r\n" +
		"RRULE:FREQ=WEEKLY;BYDAY=MO\r\nSUMMARY:London\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	instances, _, err := expandInstances(ics, start.Unix(), end.Unix())
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(instances) != 4 {
		t.Fatalf("expanded %d, want 4", len(instances))
	}
	evs, errs := parseICalendar("work", "Work", "", instances[len(instances)-1])
	if len(errs) != 0 || len(evs) != 1 {
		t.Fatalf("parse: %v, %v", errs, evs)
	}
	london, _ := time.LoadLocation("Europe/London")
	want := time.Date(2026, 10, 26, 9, 0, 0, 0, london)
	if !evs[0].Start.Equal(want) || evs[0].Start.Location().String() != "Europe/London" {
		t.Fatalf("zoned occurrence = %v, want %v", evs[0].Start, want)
	}
}

func TestExpandInstancesKeepsEventSpanningIntoRange(t *testing.T) {
	// A multi-day event that starts before the window but ends inside it
	// matched the EDS occur-in-time-range? sexp before expansion existed;
	// dropping it here would be a regression.
	ical := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:trip@x\r\nDTSTART;VALUE=DATE:20260928\r\nDTEND;VALUE=DATE:20261010\r\nSUMMARY:Trip\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC).Unix()
	instances, _, err := expandInstances(ical, start, end)
	if err != nil {
		t.Fatalf("expandInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("spanning event lost: got %d instances, want 1", len(instances))
	}
}
