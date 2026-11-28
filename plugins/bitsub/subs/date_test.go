package subs

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDateMonthArithmeticAndParse(t *testing.T) {
	d, err := ParseDate("2026-01-31")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.AddDays(29).String(); got != "2026-03-01" {
		t.Fatalf("AddDays(29) = %s", got)
	}
	for _, bad := range []string{"2026-1-31", "2026-02-30", "31/01/2026", "", "2026-01-31T00:00"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if n := DaysBetween(MustDate("2026-11-29"), MustDate("2026-12-02")); n != 3 {
		t.Fatalf("DaysBetween = %d", n)
	}
	if n := DaysBetween(MustDate("2026-12-02"), MustDate("2026-11-29")); n != -3 {
		t.Fatalf("DaysBetween reversed = %d", n)
	}
}

func TestDateCompareAndWeekday(t *testing.T) {
	a, b := MustDate("2026-11-29"), MustDate("2026-12-01")
	if !a.Before(b) || b.Before(a) || a.Before(a) {
		t.Fatal("Before")
	}
	if a.Compare(b) != -1 || b.Compare(a) != 1 || a.Compare(a) != 0 {
		t.Fatal("Compare")
	}
	if a.Weekday() != time.Sunday {
		t.Fatalf("weekday %v", a.Weekday())
	}
}

func TestTodayAndAtUseLocation(t *testing.T) {
	mel, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Skip(err)
	}
	// 2026-11-29 14:30 UTC is 2026-11-30 01:30 in Melbourne (AEDT, +11).
	now := time.Date(2026, 11, 29, 14, 30, 0, 0, time.UTC)
	if got := Today(now, mel).String(); got != "2026-11-30" {
		t.Fatalf("Today = %s", got)
	}
	at := MustDate("2026-11-30").At(9, mel)
	if at.UTC().Format(time.RFC3339) != "2026-11-29T22:00:00Z" {
		t.Fatalf("At = %s", at.UTC())
	}
}

func TestDateJSON(t *testing.T) {
	b, err := json.Marshal(struct{ D Date }{MustDate("2026-12-02")})
	if err != nil || string(b) != `{"D":"2026-12-02"}` {
		t.Fatalf("%s %v", b, err)
	}
	var v struct{ D Date }
	if err := json.Unmarshal([]byte(`{"D":"2027-01-31"}`), &v); err != nil || v.D.String() != "2027-01-31" {
		t.Fatalf("%v %v", v, err)
	}
	if err := json.Unmarshal([]byte(`{"D":"2027-02-31"}`), &v); err == nil {
		t.Fatal("bad date unmarshalled")
	}
}
