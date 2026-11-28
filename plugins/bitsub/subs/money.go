package subs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Currency is an ISO 4217 code.
type Currency string

const AUD Currency = "AUD"

// ParseCurrency trims and upper-cases s and requires three letters.
func ParseCurrency(s string) (Currency, error) {
	c := strings.ToUpper(strings.TrimSpace(s))
	if len(c) != 3 {
		return "", fmt.Errorf("currency %q: want a 3-letter ISO 4217 code", s)
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return "", fmt.Errorf("currency %q: want a 3-letter ISO 4217 code", s)
		}
	}
	return Currency(c), nil
}

// Decimals is the number of minor-unit digits: 0 for JPY, 2 otherwise.
func (c Currency) Decimals() int {
	if c == "JPY" {
		return 0
	}
	return 2
}

var symbols = map[Currency]string{
	"AUD": "A$", "USD": "US$", "EUR": "€", "GBP": "£", "JPY": "¥", "NZD": "NZ$", "CAD": "C$",
}

// Money is an amount in minor units of a currency. Stored money is never a float.
type Money struct {
	Minor    int64
	Currency Currency
}

// maxIntegerDigits keeps parsed amounts far from int64 overflow.
const maxIntegerDigits = 13

// ParseAmount reads a plain decimal string such as "20" or "139.00",
// refusing more precision than the currency's minor unit.
func ParseAmount(s string, c Currency) (Money, error) {
	s = strings.TrimSpace(s)
	whole, frac, hasDot := strings.Cut(s, ".")
	dec := c.Decimals()
	if whole == "" || !digits(whole) || len(whole) > maxIntegerDigits ||
		(hasDot && (frac == "" || !digits(frac) || len(frac) > dec)) {
		return Money{}, fmt.Errorf("amount %q: want a positive decimal with at most %d decimals", s, dec)
	}
	frac += strings.Repeat("0", dec-len(frac))
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("amount %q: %w", s, err)
	}
	return Money{n, c}, nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// String formats m with its symbol, e.g. US$20.00, ¥500 or CHF 5.00.
func (m Money) String() string {
	sym, ok := symbols[m.Currency]
	if !ok {
		sym = string(m.Currency) + " "
	}
	return sym + formatMinor(m.Minor, m.Currency.Decimals())
}

// Decimal is the bare amount ParseAmount reads back, e.g. "20.00" or "500".
func (m Money) Decimal() string {
	return strings.ReplaceAll(formatMinor(m.Minor, m.Currency.Decimals()), ",", "")
}

func formatMinor(minor int64, dec int) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	scale := int64(math.Pow10(dec))
	whole := strconv.FormatInt(minor/scale, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if dec > 0 {
		fmt.Fprintf(&b, ".%0*d", dec, minor%scale)
	}
	return sign + b.String()
}

// Rates holds units of each currency per 1 AUD, as Frankfurter's base=AUD returns.
type Rates struct {
	Date   Date
	PerAUD map[Currency]float64
}

// ToAUD converts m to AUD cents, rounding half away from zero. It reports
// false when the currency has no rate.
func (r Rates) ToAUD(m Money) (int64, bool) {
	if m.Currency == AUD {
		return m.Minor, true
	}
	rate, ok := r.PerAUD[m.Currency]
	if !ok || rate <= 0 {
		return 0, false
	}
	major := float64(m.Minor) / math.Pow10(m.Currency.Decimals())
	return int64(math.Round(major / rate * 100)), true
}

// FormatAUD renders AUD first with the original in brackets when it isn't
// AUD, e.g. "A$31.00 (US$20.00)"; approx prefixes "≈".
func FormatAUD(aud int64, orig Money, approx bool) string {
	return FormatPrice(aud, orig, approx, "")
}

// FormatPrice is FormatAUD with a cycle suffix after the AUD figure, e.g.
// "A$215.50/yr (US$139.00)". A non-AUD price with no AUD figure (no rate
// yet) shows only the original rather than a misleading A$0.00.
func FormatPrice(aud int64, orig Money, approx bool, suffix string) string {
	if aud == 0 && orig.Currency != AUD && orig.Currency != "" && orig.Minor != 0 {
		return orig.String() + suffix
	}
	s := Money{aud, AUD}.String() + suffix
	if approx {
		s = "≈" + s
	}
	if orig.Currency != AUD && orig.Currency != "" {
		s += " (" + orig.String() + ")"
	}
	return s
}
