package worldclock

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestReadTokyoFromMelbourne(t *testing.T) {
	t.Parallel()
	mel := mustLoc(t, "Australia/Melbourne")
	now := time.Date(2026, 6, 1, 23, 30, 0, 0, mel) // AEST, UTC+10
	r, err := Read(Zone{ID: "Asia/Tokyo", OnBar: true}, now, mel, true)
	if err != nil {
		t.Fatal(err)
	}
	want := Reading{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "22:30", Offset: "UTC+9", Relative: "−1h", DayShift: 0, Daytime: false, OnBar: true}
	if r != want {
		t.Fatalf("reading = %+v", r)
	}
}

func TestReadDayShiftAcrossMidnight(t *testing.T) {
	t.Parallel()
	mel := mustLoc(t, "Australia/Melbourne")
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, mel)
	ny, _ := Read(Zone{ID: "America/New_York"}, now, mel, true)
	if ny.DayShift != -1 || ny.Relative != "−14h" || ny.Clock != "19:00" {
		t.Fatalf("new york = %+v", ny)
	}
	utc := time.UTC
	late := time.Date(2026, 6, 1, 20, 0, 0, 0, utc)
	tk, _ := Read(Zone{ID: "Asia/Tokyo"}, late, utc, false)
	if tk.DayShift != 1 || tk.Relative != "+9h" || tk.Clock != "5:00 AM" || tk.Daytime {
		t.Fatalf("tokyo = %+v", tk)
	}
}

func TestReadQuarterHourAndDST(t *testing.T) {
	t.Parallel()
	utc := time.UTC
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, utc)
	ktm, _ := Read(Zone{ID: "Asia/Kathmandu"}, now, utc, true)
	if ktm.Offset != "UTC+5:45" || ktm.Relative != "+5h45" {
		t.Fatalf("kathmandu = %+v", ktm)
	}
	cht, _ := Read(Zone{ID: "Pacific/Chatham"}, now, utc, true)
	if cht.Offset != "UTC+13:45" || cht.Relative != "+13h45" || cht.DayShift != 1 {
		t.Fatalf("chatham = %+v", cht)
	}
	nfl, _ := Read(Zone{ID: "America/St_Johns"}, now, utc, true)
	if nfl.Offset != "UTC-3:30" || nfl.Relative != "−3h30" {
		t.Fatalf("st johns = %+v", nfl)
	}
	// 2026-03-08 07:00 UTC is 03:00 EDT, just after the spring-forward.
	ny, _ := Read(Zone{ID: "America/New_York"}, time.Date(2026, 3, 8, 7, 0, 0, 0, utc), utc, true)
	if ny.Offset != "UTC-4" || ny.Clock != "03:00" {
		t.Fatalf("new york dst = %+v", ny)
	}
	ist, _ := Read(Zone{ID: "Asia/Kolkata"}, now, mustLoc(t, "Asia/Kathmandu"), true)
	if ist.Relative != "−15m" {
		t.Fatalf("kolkata from kathmandu = %+v", ist)
	}
}

func TestReadSameZoneAsLocal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	r, err := Read(Zone{ID: "UTC"}, now, time.UTC, true)
	if err != nil || r.Relative != "Same time" || r.DayShift != 0 || !r.Daytime {
		t.Fatalf("utc = %+v err = %v", r, err)
	}
}

func TestReadLabelAndInvalidZone(t *testing.T) {
	t.Parallel()
	now := time.Now()
	r, _ := Read(Zone{ID: "America/New_York", Label: "HQ"}, now, time.UTC, true)
	if r.Label != "HQ" {
		t.Fatalf("label = %q", r.Label)
	}
	if _, err := Read(Zone{ID: "Not/AZone"}, now, time.UTC, true); err == nil {
		t.Fatal("invalid zone read")
	}
}

func TestDaytimeBounds(t *testing.T) {
	t.Parallel()
	for hour, want := range map[int]bool{5: false, 6: true, 17: true, 18: false} {
		r, _ := Read(Zone{ID: "UTC"}, time.Date(2026, 6, 1, hour, 59, 0, 0, time.UTC), time.UTC, true)
		if r.Daytime != want {
			t.Fatalf("%d:59 daytime = %v", hour, r.Daytime)
		}
	}
}

func TestDayMarker(t *testing.T) {
	t.Parallel()
	if DayMarker(1) != "+1" || DayMarker(-1) != "−1" || DayMarker(0) != "" {
		t.Fatal("day markers")
	}
}

func TestStoreReadingsSkipNothingValid(t *testing.T) {
	t.Parallel()
	got := NewStore().Readings(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), time.UTC, true)
	if len(got) != 4 || got[1].Label != "New York" || got[0].Clock != "12:00" {
		t.Fatalf("readings = %+v", got)
	}
}
