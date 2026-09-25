package calendar

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestQueryEDSSmoke(t *testing.T) {
	if os.Getenv("SYSC_CALENDAR_EDS_SMOKE") != "1" {
		t.Skip("set SYSC_CALENDAR_EDS_SMOKE=1 to query the configured EDS calendars read-only")
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := QueryEDS(ctx, now.Add(-24*time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("read configured EDS calendars: %v", err)
	}
	t.Logf("EDS available=%t calendars=%d events=%d errors=%d truncated=%t", result.Available, len(result.Calendars), len(result.Events), len(result.Errors), result.Truncated)
	if !result.Available && len(result.Errors) == 0 {
		t.Fatal("unavailable EDS returned no actionable error")
	}
}

func TestICalendarParsesFoldedTextAndExpandedOccurrences(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:weekly\r\n" +
		"RECURRENCE-ID;TZID=Australia/Melbourne:20260915T100000\r\n" +
		"DTSTART;TZID=Australia/Melbourne:20260915T100000\r\n" +
		"DTEND;TZID=Australia/Melbourne:20260915T110000\r\n" +
		"SUMMARY:Planning\\, product \r\n review\r\nLOCATION:Room\\; 2\r\n" +
		"URL:https://meet.example/room\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:holiday\r\nDTSTART;VALUE=DATE:20260916\r\n" +
		"DTEND;VALUE=DATE:20260918\r\nSUMMARY:Leave\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := parseICalendar("work", "Work", "#0a7bde", ics)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d events, want 2", len(got))
	}
	if got[0].Summary != "Planning, product review" || got[0].Location != "Room; 2" {
		t.Fatalf("decoded text = %q at %q", got[0].Summary, got[0].Location)
	}
	if got[0].Start.Location().String() != "Australia/Melbourne" || got[0].End.Sub(got[0].Start) != time.Hour {
		t.Fatalf("TZID event = %v .. %v", got[0].Start, got[0].End)
	}
	if got[0].URL != "https://meet.example/room" || got[0].Marker != "#0a7bde" {
		t.Fatalf("meeting or calendar decoration lost: %+v", got[0])
	}
	if !got[1].AllDay || got[1].StartDate != "2026-09-16" || got[1].EndDate != "2026-09-18" {
		t.Fatalf("all-day exclusive range = %+v", got[1])
	}
	if got[0].ID == got[1].ID {
		t.Fatal("different occurrences received the same ID")
	}
}

func TestICalendarDurationUsesCalendarDaysAcrossDST(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:overnight\r\n" +
		"DTSTART;TZID=Australia/Melbourne:20261003T120000\r\nDURATION:P1DT2H\r\n" +
		"SUMMARY:On call\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := parseICalendar("work", "Work", "", ics)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("parsed %d events, want 1", len(got))
	}
	want := time.Date(2026, 10, 4, 14, 0, 0, 0, got[0].Start.Location())
	if !got[0].End.Equal(want) {
		t.Fatalf("end = %v, want %v", got[0].End, want)
	}
}

func TestICalendarRejectsUnknownZoneAndMalformedComponents(t *testing.T) {
	base := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTART%s\r\nSUMMARY:Test\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	for _, tc := range []struct {
		name string
		ics  string
	}{
		{"unknown time zone", sprintfICalendar(base, ";TZID=Mars/Olympus:20260915T100000")},
		{"unterminated event", "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTART:20260915T100000Z\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseICalendar("work", "Work", "", tc.ics); err == nil {
				t.Fatal("malformed calendar was accepted")
			}
		})
	}
}

func sprintfICalendar(format, value string) string {
	return strings.Replace(format, "%s", value, 1)
}
