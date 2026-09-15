package timer

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"90", 90 * time.Second},
		{"90s", 90 * time.Second},
		{"5m", 5 * time.Minute},
		{"1h", time.Hour},
		{"1:30", 90 * time.Second},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseDuration(%q) = %v %v, want %v", c.in, got, err, c.want)
		}
	}
	if _, err := ParseDuration("-1"); err == nil {
		t.Fatal("negative must fail")
	}
}

func TestStartPauseResetAndNoNegative(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	tm := New(func() time.Time { return now })
	tm.SetDuration(10 * time.Second)
	tm.Start()
	now = now.Add(4 * time.Second)
	if got := tm.Remaining(); got != 6*time.Second {
		t.Fatalf("remaining = %v", got)
	}
	tm.Pause()
	now = now.Add(time.Minute)
	if got := tm.Remaining(); got != 6*time.Second {
		t.Fatalf("paused remaining moved: %v", got)
	}
	tm.Reset()
	if got := tm.Remaining(); got != 10*time.Second {
		t.Fatalf("reset = %v", got)
	}
	tm.Start()
	now = now.Add(time.Hour)
	if got := tm.Remaining(); got != 0 {
		t.Fatalf("negative remaining %v", got)
	}
}

func TestTickFiresOnceAtZero(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	tm := New(func() time.Time { return now })
	tm.SetDuration(time.Second)
	tm.Start()
	now = now.Add(time.Second)
	_, done := tm.Tick()
	if !done {
		t.Fatal("completion not reported")
	}
	_, done = tm.Tick()
	if done {
		t.Fatal("completion reported twice")
	}
}

func TestRestoreDeadline(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	tm := New(func() time.Time { return now })
	deadline := now.Add(30 * time.Second)
	tm.Restore(deadline)
	if !tm.Running() || tm.Remaining() != 30*time.Second {
		t.Fatalf("restore remaining=%v running=%v", tm.Remaining(), tm.Running())
	}
}

func TestFormatMMSS(t *testing.T) {
	t.Parallel()
	if got := FormatMMSS(-time.Second); got != "00:00" {
		t.Fatalf("negative = %q", got)
	}
	if got := FormatMMSS(5*time.Minute + 7*time.Second); got != "05:07" {
		t.Fatalf("got %q", got)
	}
}
