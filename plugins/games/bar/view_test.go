package bar

import (
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func TestPillValidatesAsBar(t *testing.T) {
	running := map[string]Run{"7": {Name: "Hades", Start: now.Add(-5 * time.Minute)}}
	for _, n := range []*v1.Node{Pill(nil, false, now), Pill(running, false, now), Pill(nil, true, now)} {
		if err := v1.Validate(n, v1.ViewBar); err != nil {
			t.Fatalf("validate: %v", err)
		}
	}
}

func TestPillIdle(t *testing.T) {
	n := Pill(nil, false, now)
	if n.Kind != v1.KindButton || n.Key != "bar" {
		t.Fatalf("kind/key: %s/%s", n.Kind, n.Key)
	}
	if n.Icon != "sports_esports" {
		t.Fatalf("idle pill icon=%q", n.Icon)
	}
}

func TestPillLibraryMissing(t *testing.T) {
	n := Pill(nil, true, now)
	if n.Tone != v1.ToneSubtle {
		t.Fatalf("missing library tone=%q, want subtle", n.Tone)
	}
	if n.Kind != v1.KindButton {
		t.Fatalf("missing library must stay a button, got %s", n.Kind)
	}
}

func TestPillOneRunning(t *testing.T) {
	running := map[string]Run{"7": {Name: "Hades", Start: now.Add(-42 * time.Minute)}}
	n := Pill(running, false, now)
	if n.Text != "Hades · 42m" {
		t.Fatalf("text=%q", n.Text)
	}
	if !n.Tabular {
		t.Fatal("elapsed should be tabular")
	}
}

func TestPillHoursFormat(t *testing.T) {
	running := map[string]Run{"7": {Name: "Hades", Start: now.Add(-125 * time.Minute)}}
	n := Pill(running, false, now)
	if n.Text != "Hades · 2h 05m" {
		t.Fatalf("text=%q", n.Text)
	}
}

func TestPillMultiple(t *testing.T) {
	running := map[string]Run{
		"7": {Name: "Hades", Start: now.Add(-10 * time.Minute)},
		"3": {Name: "Doom", Start: now.Add(-30 * time.Minute)},
		"9": {Name: "Tunic", Start: now.Add(-20 * time.Minute)},
	}
	n := Pill(running, false, now)
	// newest start first; +N others
	if n.Text != "Hades +2" {
		t.Fatalf("text=%q", n.Text)
	}
}

func TestElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0m"},
		{59 * time.Second, "0m"},
		{42 * time.Minute, "42m"},
		{60 * time.Minute, "1h 00m"},
		{125 * time.Minute, "2h 05m"},
	}
	for _, c := range cases {
		if got := Elapsed(now.Add(-c.d), now); got != c.want {
			t.Errorf("Elapsed(-%v)=%q want %q", c.d, got, c.want)
		}
	}
}
