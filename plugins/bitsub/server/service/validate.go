package service

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// ValidationError names the input field that was rejected so an MCP client
// can correct itself.
type ValidationError struct {
	Field, Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// DuplicateError refuses a name that normalises equal to an existing
// active subscription.
type DuplicateError struct {
	Existing subs.Subscription
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("name: %q already exists (id %s); update that entry, or pass allow_duplicate to add another",
		e.Existing.Name, e.Existing.ID)
}

const (
	maxNameRunes     = 80
	maxAmountMajor   = 100000
	maxInterval      = 36
	maxNoticeDays    = 90
	maxReminderSteps = 5
	maxReminderDay   = 60
	maxLabelRunes    = 40
	maxCategoryRunes = 40
	maxNotesBytes    = 2048
	dateRangeYears   = 10
	cardDigitRun     = 8
)

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes {
		return "", invalid("name", "must be 1–%d characters", maxNameRunes)
	}
	return name, nil
}

// validCurrency checks the code's syntax and, once rates are known, that
// Frankfurter publishes it.
func validCurrency(s string, rates subs.Rates, known bool) (subs.Currency, error) {
	c, err := subs.ParseCurrency(s)
	if err != nil {
		return "", invalid("currency", "want a 3-letter ISO 4217 code such as AUD or USD")
	}
	if known && c != subs.AUD {
		if _, ok := rates.PerAUD[c]; !ok {
			return "", invalid("currency", "%s is not supported (no exchange rate)", c)
		}
	}
	return c, nil
}

func validAmount(s string, c subs.Currency) (subs.Money, error) {
	m, err := subs.ParseAmount(s, c)
	if err != nil {
		return m, invalid("amount", "want a decimal such as 18.99 (at most %d decimals for %s)", c.Decimals(), c)
	}
	limit := int64(maxAmountMajor)
	for i := 0; i < c.Decimals(); i++ {
		limit *= 10
	}
	if m.Minor <= 0 || m.Minor > limit {
		return m, invalid("amount", "must be more than 0 and at most %d", maxAmountMajor)
	}
	return m, nil
}

func validInterval(n int) error {
	if n < 1 || n > maxInterval {
		return invalid("interval", "must be 1–%d", maxInterval)
	}
	return nil
}

func validCycle(c subs.Cycle) error {
	if !c.Valid() {
		return invalid("cycle", "must be day, week, month or year")
	}
	return nil
}

func validIntent(i subs.Intent) error {
	if !i.Valid() {
		return invalid("intent", "must be keep, cancel or undecided")
	}
	return nil
}

// validDate parses YYYY-MM-DD within ±10 years of today.
func validDate(field, s string, today subs.Date) (subs.Date, error) {
	d, err := subs.ParseDate(strings.TrimSpace(s))
	if err != nil {
		return d, invalid(field, "want YYYY-MM-DD")
	}
	lo := subs.NthRenewal(today, subs.Year, dateRangeYears, -1)
	hi := subs.NthRenewal(today, subs.Year, dateRangeYears, 1)
	if d.Before(lo) || d.After(hi) {
		return d, invalid(field, "must be within %d years of today", dateRangeYears)
	}
	return d, nil
}

func validNoticeDays(n int) error {
	if n < 0 || n > maxNoticeDays {
		return invalid("notice_days", "must be 0–%d", maxNoticeDays)
	}
	return nil
}

func validReminderDays(days []int) ([]int, error) {
	if len(days) == 0 {
		return nil, nil
	}
	if len(days) > maxReminderSteps {
		return nil, invalid("reminder_days", "at most %d values", maxReminderSteps)
	}
	seen := map[int]bool{}
	for _, d := range days {
		if d < 1 || d > maxReminderDay || seen[d] {
			return nil, invalid("reminder_days", "distinct values in 1–%d", maxReminderDay)
		}
		seen[d] = true
	}
	return append([]int(nil), days...), nil
}

func validURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", invalid("url", "must be an http(s) URL")
	}
	return s, nil
}

// validPaymentLabel allows "Visa ··21" or "PayPal" but never a card number.
func validPaymentLabel(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxLabelRunes {
		return "", invalid("payment_label", "at most %d characters", maxLabelRunes)
	}
	run := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			run++
			if run >= cardDigitRun {
				return "", invalid("payment_label", "looks like a card number; use a label such as \"Visa ··21\"")
			}
		case r == ' ' || r == '-':
		default:
			run = 0
		}
	}
	return s, nil
}

func validCategory(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxCategoryRunes {
		return "", invalid("category", "at most %d characters", maxCategoryRunes)
	}
	return s, nil
}

func validNotes(s string) (string, error) {
	if len(s) > maxNotesBytes {
		return "", invalid("notes", "at most %d bytes", maxNotesBytes)
	}
	return s, nil
}
