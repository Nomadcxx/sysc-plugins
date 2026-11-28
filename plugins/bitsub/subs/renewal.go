package subs

import "time"

// NthRenewal is the k-th charge date from anchor (k=0 is the anchor). Month
// and year cycles count months from the anchor and clamp the day to the
// month's length, so a 31st anchor never sticks on an earlier clamp.
func NthRenewal(anchor Date, c Cycle, interval, k int) Date {
	switch c {
	case Day:
		return anchor.AddDays(k * interval)
	case Week:
		return anchor.AddDays(7 * k * interval)
	case Year:
		return addMonths(anchor, 12*k*interval)
	default:
		return addMonths(anchor, k*interval)
	}
}

func addMonths(d Date, n int) Date {
	m := int(d.Month) - 1 + n
	y := d.Year + m/12
	m %= 12
	if m < 0 {
		m += 12
		y--
	}
	month := time.Month(m + 1)
	return Date{y, month, min(d.Day, daysIn(y, month))}
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 12, 0, 0, 0, time.UTC).Day()
}

// indexOnOrAfter is the smallest k ≥ 0 with NthRenewal(k) ≥ day.
func indexOnOrAfter(anchor Date, c Cycle, interval int, day Date) int {
	if !anchor.Before(day) {
		return 0
	}
	var k int
	switch c {
	case Day, Week:
		step := interval
		if c == Week {
			step *= 7
		}
		k = (DaysBetween(anchor, day) + step - 1) / step
	default:
		months := (day.Year-anchor.Year)*12 + int(day.Month) - int(anchor.Month)
		if c == Year {
			months /= 12
		}
		k = months / interval
	}
	for NthRenewal(anchor, c, interval, k).Before(day) {
		k++
	}
	return k
}

// NextOnOrAfter is the first charge date on or after day.
func NextOnOrAfter(anchor Date, c Cycle, interval int, day Date) Date {
	return NthRenewal(anchor, c, interval, indexOnOrAfter(anchor, c, interval, day))
}

// RenewalsBetween lists charge dates in (after, through].
func RenewalsBetween(anchor Date, c Cycle, interval int, after, through Date) []Date {
	var out []Date
	for k := indexOnOrAfter(anchor, c, interval, after.AddDays(1)); ; k++ {
		d := NthRenewal(anchor, c, interval, k)
		if d.After(through) {
			return out
		}
		out = append(out, d)
	}
}

type TargetKind string

const (
	TargetTrialEnd TargetKind = "trial_end"
	TargetRenewal  TargetKind = "renewal"
)

// Target is the next event that needs a decision: the trial end while the
// trial runs, otherwise the next renewal.
func (s Subscription) Target(today Date) (Date, TargetKind) {
	if s.TrialRunning(today) {
		return *s.TrialEnd, TargetTrialEnd
	}
	return NextOnOrAfter(s.Anchor, s.Cycle, s.Interval, today), TargetRenewal
}

// CancelBy is the last day the vendor accepts a cancellation before the target.
func (s Subscription) CancelBy(today Date) Date {
	d, _ := s.Target(today)
	return d.AddDays(-s.NoticeDays)
}

// MonthlyMinor normalises a per-cycle amount to a month.
func MonthlyMinor(amount int64, c Cycle, interval int) int64 {
	num, den := amount, int64(interval)
	switch c {
	case Year:
		den *= 12
	case Week:
		num *= 52
		den *= 12
	case Day:
		num *= 365
		den *= 12
	}
	return divRound(num, den)
}

// divRound divides rounding half away from zero; den > 0.
func divRound(num, den int64) int64 {
	if num < 0 {
		return -divRound(-num, den)
	}
	return (2*num + den) / (2 * den)
}
