package subs

import "fmt"

// StaleAfterDays is how old rates may be before totals are approximate.
const StaleAfterDays = 7

type Totals struct {
	MonthlyAUD  int64 `json:"monthly_aud"`
	YearlyAUD   int64 `json:"yearly_aud"`
	Next30AUD   int64 `json:"next30_aud"`
	Next30Count int   `json:"next30_count"`
	// Approx is set when a non-AUD price had no rate (and was left out of
	// the sums) or the rates are stale.
	Approx bool `json:"approx"`
}

// RatesStale reports whether r is older than StaleAfterDays on today.
func RatesStale(r Rates, today Date) bool {
	return r.Date.IsZero() || DaysBetween(r.Date, today) > StaleAfterDays
}

// convert gives m in AUD and whether that figure is approximate.
func convert(m Money, r Rates, today Date) (aud int64, ok, approx bool) {
	aud, ok = r.ToAUD(m)
	if m.Currency == AUD {
		return aud, true, false
	}
	return aud, ok, !ok || RatesStale(r, today)
}

// ComputeTotals sums active subscriptions. Monthly leaves out anything being
// cancelled and trials still running; next-30 counts every charge date in
// [today, today+30], cancelling ones included because the charge still
// comes if the owner forgets.
func ComputeTotals(list []Subscription, r Rates, today Date) Totals {
	var t Totals
	through := today.AddDays(30)
	for _, s := range list {
		if s.Status != Active || s.DeletedAt != nil {
			continue
		}
		aud, ok, approx := convert(s.Price, r, today)
		t.Approx = t.Approx || approx
		if ok && s.Intent != Cancel && !s.TrialRunning(today) {
			t.MonthlyAUD += MonthlyMinor(aud, s.Cycle, s.Interval)
		}
		dates := RenewalsBetween(s.Anchor, s.Cycle, s.Interval, today.AddDays(-1), through)
		if s.TrialRunning(today) && !s.TrialEnd.After(through) && !containsDate(dates, *s.TrialEnd) {
			dates = append(dates, *s.TrialEnd)
		}
		t.Next30Count += len(dates)
		if ok {
			t.Next30AUD += aud * int64(len(dates))
		}
	}
	t.YearlyAUD = t.MonthlyAUD * 12
	return t
}

func containsDate(ds []Date, d Date) bool {
	for _, x := range ds {
		if x == d {
			return true
		}
	}
	return false
}

type MonthTotal struct {
	Month    string `json:"month"` // "2026-11"
	AUDMinor int64  `json:"aud_minor"`
}

// MonthlySeries is charged AUD per calendar month: 12 entries, oldest
// first, ending with today's month, zero-filled.
func MonthlySeries(charges []Charge, today Date) []MonthTotal {
	out := make([]MonthTotal, 12)
	index := map[string]int{}
	first := addMonths(Date{today.Year, today.Month, 1}, -11)
	for i := range out {
		d := addMonths(first, i)
		out[i].Month = monthKey(d)
		index[out[i].Month] = i
	}
	for _, c := range charges {
		if i, ok := index[monthKey(c.Date)]; ok {
			out[i].AUDMinor += c.AUDMinor
		}
	}
	return out
}

func monthKey(d Date) string { return fmt.Sprintf("%04d-%02d", d.Year, d.Month) }
