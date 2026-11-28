package subs

import "testing"

func sub(name string, minor int64, cur Currency, c Cycle, anchor string, intent Intent) Subscription {
	return Subscription{ID: name, Name: name, Price: Money{minor, cur}, Cycle: c, Interval: 1,
		Anchor: MustDate(anchor), Intent: intent, Status: Active}
}

func TestComputeTotals(t *testing.T) {
	today := MustDate("2026-11-29")
	rates := Rates{Date: today, PerAUD: map[Currency]float64{"USD": 0.645}}
	list := []Subscription{
		sub("Netflix", 1899, AUD, Month, "2026-01-14", Keep),
		sub("Synthetic", 2000, "USD", Month, "2026-03-05", Undecided),
		sub("Speechify", 13900, "USD", Year, "2025-12-10", Cancel),
		sub("Hetzner", 549, "EUR", Month, "2026-02-20", Keep),
	}
	got := ComputeTotals(list, rates, today)
	if got.MonthlyAUD != 5000 {
		t.Errorf("MonthlyAUD = %d want 5000", got.MonthlyAUD)
	}
	if got.YearlyAUD != 60000 {
		t.Errorf("YearlyAUD = %d", got.YearlyAUD)
	}
	if !got.Approx {
		t.Error("Approx false with a missing EUR rate")
	}
	// Next 30 days (2026-11-29..2026-12-29): Netflix 12-14 (1899), Synthetic 12-05 (3101),
	// Speechify 12-10 (21550, cancelling still counts), Hetzner 12-20 (no rate: counted, not summed).
	if got.Next30AUD != 1899+3101+21550 || got.Next30Count != 4 {
		t.Errorf("Next30 = %d / %d", got.Next30AUD, got.Next30Count)
	}
}

func TestComputeTotalsExcludesRunningTrialsAndCancelled(t *testing.T) {
	today := MustDate("2026-11-29")
	end := MustDate("2026-12-02")
	trial := sub("Grok", 3000, AUD, Month, "2026-12-02", Undecided)
	trial.TrialEnd = &end
	gone := sub("Stan", 1700, AUD, Month, "2026-01-10", Keep)
	gone.Status = Cancelled
	got := ComputeTotals([]Subscription{trial, gone, sub("Spotify", 1399, AUD, Month, "2026-01-01", Keep)}, Rates{Date: today}, today)
	if got.MonthlyAUD != 1399 {
		t.Errorf("MonthlyAUD = %d", got.MonthlyAUD)
	}
	if got.Next30AUD != 3000+1399 || got.Next30Count != 2 {
		t.Errorf("Next30 = %d / %d (trial end counts, cancelled doesn't)", got.Next30AUD, got.Next30Count)
	}
	if got.Approx {
		t.Error("all-AUD totals marked approximate")
	}
}

func TestComputeTotalsStaleRates(t *testing.T) {
	today := MustDate("2026-11-29")
	list := []Subscription{sub("Synthetic", 2000, "USD", Month, "2026-03-05", Keep)}
	fresh := Rates{Date: today.AddDays(-7), PerAUD: map[Currency]float64{"USD": 0.645}}
	if ComputeTotals(list, fresh, today).Approx {
		t.Error("7-day-old rates marked stale")
	}
	stale := Rates{Date: today.AddDays(-8), PerAUD: fresh.PerAUD}
	if got := ComputeTotals(list, stale, today); !got.Approx || got.MonthlyAUD != 3101 {
		t.Errorf("stale: %+v", got)
	}
	// No rates at all: non-AUD prices are left out, never shown as A$0 exact.
	if got := ComputeTotals(list, Rates{}, today); !got.Approx || got.MonthlyAUD != 0 {
		t.Errorf("no rates: %+v", got)
	}
}

func TestMonthlySeriesIsTwelveZeroFilled(t *testing.T) {
	charges := []Charge{
		{Date: MustDate("2026-08-03"), AUDMinor: 1500},
		{Date: MustDate("2026-08-20"), AUDMinor: 500},
		{Date: MustDate("2025-11-30"), AUDMinor: 9999}, // 12 months back: outside the window
		{Date: MustDate("2026-11-01"), AUDMinor: 100},
	}
	got := MonthlySeries(charges, MustDate("2026-11-29"))
	if len(got) != 12 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].Month != "2025-12" || got[11].Month != "2026-11" {
		t.Fatalf("range %s..%s", got[0].Month, got[11].Month)
	}
	for _, m := range got {
		want := map[string]int64{"2026-08": 2000, "2026-11": 100}[m.Month]
		if m.AUDMinor != want {
			t.Errorf("%s = %d want %d", m.Month, m.AUDMinor, want)
		}
	}
}

func TestItemOf(t *testing.T) {
	today := MustDate("2026-11-29")
	s := sub("Synthetic", 2000, "USD", Month, "2026-03-05", Cancel)
	s.NoticeDays = 2
	it := ItemOf(s, Rates{Date: today, PerAUD: map[Currency]float64{"USD": 0.645}}, today)
	if it.PriceAUD != 3101 || it.PriceApprox || it.Target.String() != "2026-12-05" ||
		it.TargetKind != TargetRenewal || it.CancelBy.String() != "2026-12-03" || it.Intent != Cancel {
		t.Fatalf("%+v", it)
	}
	if it := ItemOf(s, Rates{}, today); !it.PriceApprox || it.PriceAUD != 0 {
		t.Fatalf("no rate: %+v", it)
	}
}
