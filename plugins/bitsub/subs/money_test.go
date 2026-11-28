package subs

import "testing"

func TestFormatAUD(t *testing.T) {
	cases := []struct {
		aud    int64
		orig   Money
		approx bool
		want   string
	}{
		{3100, Money{2000, "USD"}, false, "A$31.00 (US$20.00)"},
		{970, Money{549, "EUR"}, false, "A$9.70 (€5.49)"},
		{1700, Money{1700, "AUD"}, false, "A$17.00"},
		{411360, Money{411360, "AUD"}, false, "A$4,113.60"},
		{3100, Money{2000, "USD"}, true, "≈A$31.00 (US$20.00)"},
		{780, Money{500, "CHF"}, false, "A$7.80 (CHF 5.00)"},
		{500, Money{500, "JPY"}, false, "A$5.00 (¥500)"},
		{123456789, Money{123456789, "AUD"}, false, "A$1,234,567.89"},
		{5, Money{5, "AUD"}, false, "A$0.05"},
	}
	for _, c := range cases {
		if got := FormatAUD(c.aud, c.orig, c.approx); got != c.want {
			t.Errorf("FormatAUD(%d, %v, %v) = %q, want %q", c.aud, c.orig, c.approx, got, c.want)
		}
	}
}

func TestMoneyString(t *testing.T) {
	cases := map[Money]string{
		{2000, "USD"}: "US$20.00",
		{500, "JPY"}:  "¥500",
		{500, "CHF"}:  "CHF 5.00",
		{1299, "GBP"}: "£12.99",
		{1000, "NZD"}: "NZ$10.00",
		{1000, "CAD"}: "C$10.00",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", m, got, want)
		}
	}
}

func TestRatesToAUD(t *testing.T) {
	r := Rates{PerAUD: map[Currency]float64{"USD": 0.645}}
	if got, ok := r.ToAUD(Money{2000, "USD"}); !ok || got != 3101 { // 20/0.645 = 31.007…
		t.Fatal(got, ok)
	}
	if _, ok := r.ToAUD(Money{100, "GBP"}); ok {
		t.Fatal("missing currency converted")
	}
	if got, ok := r.ToAUD(Money{1700, "AUD"}); !ok || got != 1700 {
		t.Fatal(got, ok)
	}
	// ¥500 at 100 JPY per AUD is A$5.00: JPY has no minor unit.
	r.PerAUD["JPY"] = 100
	if got, ok := r.ToAUD(Money{500, "JPY"}); !ok || got != 500 {
		t.Fatal(got, ok)
	}
	// Half away from zero: 1 US cent at 0.4 per AUD is 2.5 AUD cents.
	r.PerAUD["USD"] = 0.4
	if got, _ := r.ToAUD(Money{1, "USD"}); got != 3 {
		t.Fatal(got)
	}
}

func TestParseAmountPrecision(t *testing.T) {
	ok := []struct {
		s    string
		c    Currency
		want int64
	}{
		{"20", "USD", 2000},
		{"139.00", "USD", 13900},
		{"5.5", "EUR", 550},
		{"500", "JPY", 500},
		{" 18.99 ", "AUD", 1899},
		{"100000.00", "AUD", 10000000},
	}
	for _, c := range ok {
		m, err := ParseAmount(c.s, c.c)
		if err != nil || m.Minor != c.want || m.Currency != c.c {
			t.Errorf("ParseAmount(%q, %s) = %v, %v; want %d", c.s, c.c, m, err, c.want)
		}
	}
	bad := []struct {
		s string
		c Currency
	}{
		{"5.499", "USD"}, {"5.5", "JPY"}, {"-1", "USD"}, {"", "USD"}, {"1e3", "USD"},
		{"1,000", "USD"}, {".", "USD"}, {"abc", "USD"}, {"12.", "USD"}, {"99999999999999999999", "USD"},
	}
	for _, c := range bad {
		if m, err := ParseAmount(c.s, c.c); err == nil {
			t.Errorf("ParseAmount(%q, %s) = %v, want error", c.s, c.c, m)
		}
	}
}

func TestParseCurrency(t *testing.T) {
	for in, want := range map[string]Currency{"usd": "USD", " Eur ": "EUR", "AUD": "AUD"} {
		if got, err := ParseCurrency(in); err != nil || got != want {
			t.Errorf("ParseCurrency(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"usd$", "US", "USDT", "", "U5D"} {
		if _, err := ParseCurrency(bad); err == nil {
			t.Errorf("ParseCurrency(%q) accepted", bad)
		}
	}
}

func TestMoneyDecimal(t *testing.T) {
	for m, want := range map[Money]string{{2000, "USD"}: "20.00", {123456789, "AUD"}: "1234567.89", {500, "JPY"}: "500", {5, "EUR"}: "0.05"} {
		if got := m.Decimal(); got != want {
			t.Errorf("%v.Decimal() = %q want %q", m, got, want)
		}
		if back, err := ParseAmount(m.Decimal(), m.Currency); err != nil || back != m {
			t.Errorf("round trip %v: %v %v", m, back, err)
		}
	}
}
