// Package subs holds bitsub's pure domain code: civil dates, money, renewal
// maths, totals and the wire types shared by bitsubd and the plugin.
package subs

import (
	"encoding/json"
	"fmt"
	"time"
)

// Date is a calendar day with no time zone. Renewals, trial ends and
// access-until dates are civil dates read in the system time zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate accepts only YYYY-MM-DD naming a real day.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Format(time.DateOnly) != s {
		return Date{}, fmt.Errorf("date %q: want YYYY-MM-DD", s)
	}
	return Date{t.Year(), t.Month(), t.Day()}, nil
}

// MustDate is ParseDate for constants; it panics on a bad date.
func MustDate(s string) Date {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Today is the civil date of now in loc.
func Today(now time.Time, loc *time.Location) Date {
	y, m, d := now.In(loc).Date()
	return Date{y, m, d}
}

// noon anchors arithmetic at midday UTC so no DST edge can move the day.
func (d Date) noon() time.Time { return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, time.UTC) }

func fromTime(t time.Time) Date { return Date{t.Year(), t.Month(), t.Day()} }

func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

func (d Date) IsZero() bool { return d == Date{} }

func (d Date) AddDays(n int) Date { return fromTime(d.noon().AddDate(0, 0, n)) }

func (d Date) Compare(o Date) int {
	switch {
	case d.Year != o.Year:
		return cmp(d.Year, o.Year)
	case d.Month != o.Month:
		return cmp(int(d.Month), int(o.Month))
	default:
		return cmp(d.Day, o.Day)
	}
}

func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

func (d Date) After(o Date) bool { return d.Compare(o) > 0 }

func (d Date) Weekday() time.Weekday { return d.noon().Weekday() }

// At is the instant hour:00 on d in loc.
func (d Date) At(hour int, loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, hour, 0, 0, 0, loc)
}

// Short formats d as "Tue 2 Dec".
func (d Date) Short() string { return d.noon().Format("Mon 2 Jan") }

// DaysBetween is b − a in days.
func DaysBetween(a, b Date) int {
	return int(b.noon().Sub(a.noon()).Hours() / 24)
}

func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte(`""`), nil
	}
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*d = Date{}
		return nil
	}
	v, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
