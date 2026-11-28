package subs

import (
	"slices"
	"testing"
)

func TestMonthEndAnchorDoesNotDrift(t *testing.T) {
	a := MustDate("2026-01-31")
	want := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31"}
	for k, w := range want {
		if got := NthRenewal(a, Month, 1, k).String(); got != w {
			t.Errorf("k=%d %s want %s", k, got, w)
		}
	}
	checks := []struct {
		anchor string
		c      Cycle
		iv, k  int
		want   string
	}{
		{"2027-01-31", Month, 1, 1, "2027-02-28"},
		{"2028-01-31", Month, 1, 1, "2028-02-29"},
		{"2028-02-29", Year, 1, 1, "2029-02-28"},
		{"2028-02-29", Year, 1, 4, "2032-02-29"},
		{"2026-01-31", Month, 3, 1, "2026-04-30"},
		{"2026-11-30", Month, 1, 2, "2027-01-30"},
		{"2026-12-01", Week, 2, 1, "2026-12-15"},
		{"2026-12-30", Day, 3, 1, "2027-01-02"},
	}
	for _, c := range checks {
		if got := NthRenewal(MustDate(c.anchor), c.c, c.iv, c.k).String(); got != c.want {
			t.Errorf("NthRenewal(%s,%s,%d,%d) = %s want %s", c.anchor, c.c, c.iv, c.k, got, c.want)
		}
	}
}

func TestNextOnOrAfter(t *testing.T) {
	a := MustDate("2026-01-31")
	cases := map[string]string{
		"2025-06-01": "2026-01-31", // before the anchor
		"2026-01-31": "2026-01-31",
		"2026-02-01": "2026-02-28",
		"2026-02-28": "2026-02-28",
		"2026-03-01": "2026-03-31",
		"2026-11-29": "2026-11-30",
	}
	for day, want := range cases {
		if got := NextOnOrAfter(a, Month, 1, MustDate(day)).String(); got != want {
			t.Errorf("NextOnOrAfter(%s) = %s want %s", day, got, want)
		}
	}
	if got := NextOnOrAfter(MustDate("2026-12-01"), Week, 1, MustDate("2026-12-09")).String(); got != "2026-12-15" {
		t.Errorf("weekly = %s", got)
	}
	if got := NextOnOrAfter(MustDate("2025-12-02"), Year, 1, MustDate("2026-12-03")).String(); got != "2027-12-02" {
		t.Errorf("yearly = %s", got)
	}
}

func TestRenewalsBetween(t *testing.T) {
	got := RenewalsBetween(MustDate("2026-09-15"), Month, 1, MustDate("2026-09-14"), MustDate("2026-12-19"))
	var s []string
	for _, d := range got {
		s = append(s, d.String())
	}
	want := []string{"2026-09-15", "2026-10-15", "2026-11-15", "2026-12-15"}
	if !slices.Equal(s, want) {
		t.Fatalf("got %v want %v", s, want)
	}
	// after is exclusive, through inclusive
	got = RenewalsBetween(MustDate("2026-09-15"), Month, 1, MustDate("2026-09-15"), MustDate("2026-10-15"))
	if len(got) != 1 || got[0].String() != "2026-10-15" {
		t.Fatalf("got %v", got)
	}
	if got := RenewalsBetween(MustDate("2026-09-15"), Month, 1, MustDate("2026-12-01"), MustDate("2026-11-01")); len(got) != 0 {
		t.Fatalf("empty range gave %v", got)
	}
}

func TestTargetPrefersRunningTrial(t *testing.T) {
	end := MustDate("2026-12-02")
	s := Subscription{Anchor: end, TrialEnd: &end, Cycle: Year, Interval: 1}
	if d, k := s.Target(MustDate("2026-11-29")); d != end || k != TargetTrialEnd {
		t.Fatal(d, k)
	}
	if d, k := s.Target(end); d != end || k != TargetTrialEnd {
		t.Fatal("trial end day itself", d, k)
	}
	if d, k := s.Target(MustDate("2026-12-03")); d.String() != "2027-12-02" || k != TargetRenewal {
		t.Fatal(d, k)
	}
}

func TestCancelByHonoursNoticeDays(t *testing.T) {
	s := Subscription{Anchor: MustDate("2026-12-10"), Cycle: Month, Interval: 1, NoticeDays: 2}
	if got := s.CancelBy(MustDate("2026-11-29")).String(); got != "2026-12-08" {
		t.Fatal(got)
	}
	s.NoticeDays = 0
	if got := s.CancelBy(MustDate("2026-11-29")).String(); got != "2026-12-10" {
		t.Fatal(got)
	}
}

func TestMonthlyMinor(t *testing.T) {
	cases := []struct {
		amount int64
		c      Cycle
		iv     int
		want   int64
	}{
		{1899, Month, 1, 1899},
		{12000, Year, 1, 1000},
		{1000, Week, 1, 4333},  // 1000×52/12 = 4333.3
		{100, Day, 1, 3042},    // 100×365/12 = 3041.67
		{3000, Month, 3, 1000}, // quarterly
		{6, Year, 1, 1},        // 0.5 rounds away from zero
	}
	for _, c := range cases {
		if got := MonthlyMinor(c.amount, c.c, c.iv); got != c.want {
			t.Errorf("MonthlyMinor(%d,%s,%d) = %d want %d", c.amount, c.c, c.iv, got, c.want)
		}
	}
}

func TestParseActionKey(t *testing.T) {
	if v, id, ok := ParseActionKey("cancel:abc"); !ok || v != "cancel" || id != "abc" {
		t.Fatal(v, id, ok)
	}
	for _, k := range []string{"keep:x", "done:x"} {
		if _, _, ok := ParseActionKey(k); !ok {
			t.Errorf("%q rejected", k)
		}
	}
	for _, bad := range []string{"nuke:abc", "keep", "keep:", "", ":abc"} {
		if _, _, ok := ParseActionKey(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPriceSuffix(t *testing.T) {
	cases := map[[2]any]string{
		{Month, 1}: "/mo", {Year, 1}: "/yr", {Week, 1}: "/wk", {Day, 1}: "/day",
		{Month, 3}: "/3mo", {Year, 2}: "/2yr", {Week, 2}: "/2wk", {Day, 10}: "/10day",
	}
	for in, want := range cases {
		if got := PriceSuffix(in[0].(Cycle), in[1].(int)); got != want {
			t.Errorf("PriceSuffix(%v) = %q want %q", in, got, want)
		}
	}
}
