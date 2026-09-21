package aiusage

import (
	"testing"
	"time"
)

var base = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func TestLabelForMinutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		minutes int
		label   string
		short   string
	}{
		{300, "Session", "5h"},
		{10080, "Weekly", "Wk"},
		{43200, "Monthly", "Mo"},
		{720, "12 hour", "12h"},
		{45, "45 min", "45m"},
		{0, "", ""},
	}
	for _, tc := range cases {
		label, short := LabelForMinutes(tc.minutes)
		if label != tc.label || short != tc.short {
			t.Errorf("LabelForMinutes(%d) = %q/%q, want %q/%q", tc.minutes, label, short, tc.label, tc.short)
		}
	}
}

func TestFormatCountdown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{25 * time.Hour, "1d 1h"},
		{3*time.Hour + 12*time.Minute, "3h 12m"},
		{8 * time.Minute, "8m"},
		{-time.Minute, "now"},
	}
	for _, tc := range cases {
		got := FormatCountdown(base.Add(tc.offset), base)
		if got != tc.want {
			t.Errorf("FormatCountdown(+%v) = %q, want %q", tc.offset, got, tc.want)
		}
	}
	if got := FormatCountdown(time.Time{}, base); got != "" {
		t.Errorf("zero reset = %q, want empty", got)
	}
}

func TestElapsedPercent(t *testing.T) {
	t.Parallel()
	w := Window{WindowMinutes: 300, ResetsAt: base.Add(150 * time.Minute)}
	pct, ok := ElapsedPercent(w, base)
	if !ok || pct != 50 {
		t.Fatalf("mid-window = %v, %v; want 50, true", pct, ok)
	}
	early := Window{WindowMinutes: 300, ResetsAt: base.Add(400 * time.Minute)}
	if pct, _ := ElapsedPercent(early, base); pct != 0 {
		t.Errorf("before start = %v, want clamped 0", pct)
	}
	spent := Window{WindowMinutes: 300, ResetsAt: base.Add(-time.Minute)}
	if pct, _ := ElapsedPercent(spent, base); pct != 100 {
		t.Errorf("past end = %v, want clamped 100", pct)
	}
	if _, ok := ElapsedPercent(Window{WindowMinutes: 0, ResetsAt: base.Add(time.Hour)}, base); ok {
		t.Error("unbounded window returned an elapsed fraction")
	}
	if _, ok := ElapsedPercent(Window{WindowMinutes: 300}, base); ok {
		t.Error("zero reset returned an elapsed fraction")
	}
}

func TestPace(t *testing.T) {
	t.Parallel()
	w := Window{WindowMinutes: 300, ResetsAt: base.Add(180 * time.Minute), HasPercent: true, UsedPercent: 60}
	pace, ok := Pace(w, base)
	if !ok || pace != 20 {
		t.Fatalf("pace = %d, %v; want 20 ahead", pace, ok)
	}
	if _, ok := Pace(Window{WindowMinutes: 0, HasPercent: true, UsedPercent: 60}, base); ok {
		t.Error("unbounded window produced a pace")
	}
	if _, ok := Pace(Window{WindowMinutes: 300, ResetsAt: base.Add(time.Hour)}, base); ok {
		t.Error("window without a percent produced a pace")
	}
}

func TestSeverity(t *testing.T) {
	t.Parallel()
	cases := map[float64]int{84: 0, 85: 1, 94: 1, 95: 2, 98: 2, 99: 3, 100: 3}
	for pct, want := range cases {
		if got := Severity(pct, 85, 95); got != want {
			t.Errorf("Severity(%v) = %d, want %d", pct, got, want)
		}
	}
}

func TestHeadline(t *testing.T) {
	t.Parallel()
	exhaustedEarly := Window{Key: "primary", HasPercent: true, UsedPercent: 100, ResetsAt: base.Add(2 * time.Hour)}
	exhaustedLate := Window{Key: "secondary", HasPercent: true, UsedPercent: 100, ResetsAt: base.Add(5 * time.Hour)}
	high := Window{Key: "primary", HasPercent: true, UsedPercent: 90}
	info := Window{Key: "primary", HasPercent: false, DisplayValue: "$12 / $50"}

	// Among exhausted windows the latest reset is the blocker.
	if got := Headline([]Window{exhaustedEarly, exhaustedLate}); got.Key != "secondary" {
		t.Errorf("blocking headline = %s, want the latest reset", got.Key)
	}
	// An exhausted window beats a merely high one.
	if got := Headline([]Window{high, exhaustedEarly}); got.Key != "primary" {
		t.Errorf("exhausted headline = %s, want the blocking window", got.Key)
	}
	// Without exhaustion the highest percent leads.
	if got := Headline([]Window{info, Window{Key: "secondary", HasPercent: true, UsedPercent: 80}, high}); got.Key != "primary" {
		t.Errorf("highest headline = %s, want the 90%% window", got.Key)
	}
	// Percent-less windows keep the source's order.
	if got := Headline([]Window{info}); got == nil || got.DisplayValue != "$12 / $50" {
		t.Errorf("informational headline = %+v", got)
	}
	if got := Headline(nil); got != nil {
		t.Errorf("empty headline = %+v, want nil", got)
	}
}

func TestScrub(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"token sk-abc123def456hi leaked", "token [redacted] leaked"},
		{"Authorization: Bearer abc.def", "Authorization: [redacted]"},
		{"key gz1Ax9+/EE0fF2gHh5iJ8kK7lL6mM5nN4oO3pP2qQ1rR==", "key [redacted]"},
		{"Session usage", "Session usage"},
		{"62% of monthly limit consumed", "62% of monthly limit consumed"},
	}
	for _, tc := range cases {
		if got := Scrub(tc.in); got != tc.want {
			t.Errorf("Scrub(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
