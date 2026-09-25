package calendar

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func fixedNow() time.Time {
	// 2026-09-15 is a Tuesday.
	return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
}

func TestGridMondayStart(t *testing.T) {
	weeks := grid(fixedNow(), "monday", true)
	// September 2026 starts on a Tuesday; with Monday first one leading day.
	if len(weeks) != 5 {
		t.Fatalf("weeks = %d, want 5", len(weeks))
	}
	first := weeks[0]
	if first[0].Day != 31 || first[0].InMonth {
		t.Fatalf("first cell = %+v, want August 31 out of month", first[0])
	}
	if first[1].Day != 1 || !first[1].InMonth {
		t.Fatalf("second cell = %+v, want September 1", first[1])
	}
	// September 15 (Tuesday) is in week 3, second cell, and is today.
	if got := weeks[2][1]; got.Day != 15 || !got.Today || !got.InMonth {
		t.Fatalf("today cell = %+v", got)
	}
	last := weeks[len(weeks)-1]
	if last[6].Day != 4 || last[6].InMonth {
		t.Fatalf("trailing cell = %+v, want October 4 out of month", last[6])
	}
}

func TestGridSundayStart(t *testing.T) {
	weeks := grid(fixedNow(), "sunday", true)
	// With Sunday first, September 2026 (Tuesday start) has two leading days.
	if weeks[0][2].Day != 1 || !weeks[0][2].InMonth {
		t.Fatalf("first in-month cell = %+v, want September 1 third", weeks[0][2])
	}
	if weeks[0][0].Day != 30 || weeks[0][0].InMonth {
		t.Fatalf("first cell = %+v, want August 30", weeks[0][0])
	}
}

func TestGridFebruaryLeapYear(t *testing.T) {
	view := time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC)
	weeks := grid(view, "monday", true)
	days := 0
	for _, w := range weeks {
		for _, c := range w {
			if c.InMonth {
				days++
			}
		}
	}
	if days != 29 {
		t.Fatalf("leap February had %d days", days)
	}
	for _, w := range weeks {
		if len(w) != 7 {
			t.Fatalf("week length %d", len(w))
		}
	}
}

func TestModelNavigationAndWeekStart(t *testing.T) {
	m := New(fixedNow)
	if got := m.WeekStart(); got != "monday" {
		t.Fatalf("default week start = %q", got)
	}
	m.SetWeekStart("sunday")
	if got := m.WeekStart(); got != "sunday" {
		t.Fatalf("week start = %q", got)
	}
	m.SetWeekStart("bogus") // ignored
	if got := rangeTitle(m.Panel("a"), m.WeekStart()); got != "September 2026" {
		t.Fatalf("initial panel header = %q", got)
	}
	m.Step("a", -1)
	if got := rangeTitle(m.Panel("a"), m.WeekStart()); got != "August 2026" {
		t.Fatalf("previous panel header = %q", got)
	}
	m.Step("a", 2)
	if got := rangeTitle(m.Panel("a"), m.WeekStart()); got != "October 2026" {
		t.Fatalf("next panel header = %q", got)
	}
	m.TodayFor("a")
	if got := rangeTitle(m.Panel("a"), m.WeekStart()); got != "September 2026" {
		t.Fatalf("today header = %q", got)
	}
	if got := m.Date(); got != "Tue 15 Sep 2026" {
		t.Fatalf("date = %q", got)
	}
	if got := m.DayOfMonth(); got != "15" {
		t.Fatalf("day of month = %q", got)
	}
	if wd := m.WeekdayHeader(); wd[0] != "S" || wd[6] != "S" {
		t.Fatalf("sunday-start header = %v", wd)
	}
	m.SetWeekStart("monday")
	if wd := m.WeekdayHeader(); wd[0] != "M" {
		t.Fatalf("monday-start header = %v", wd)
	}
}

func TestTreesValidate(t *testing.T) {
	m := New(fixedNow)
	now := fixedNow()
	if err := v1.Validate(BarTree(nil, now), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(TooltipTree(nil, now), v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree(m.Panel("test"), nil, m.WeekStart(), LoadStatus{Available: true, CalendarCount: 1}, now, nil, nil, false), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
}
