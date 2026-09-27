package bar

import (
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

// geomFit clones the pill row with a whitelisted icon. The pinned SDK
// 7b5d65f predates the sports_esports glyph, so lint's icon whitelist
// rejects it; this keeps the lint focused on geometry. Drop the swap when
// the pin catches up (ponytail).
func geomFit(n *v1.Node) *v1.Node {
	btn := *n.Children[0]
	btn.Icon = "schedule"
	root := *n
	root.Children = []*v1.Node{&btn}
	return &root
}

func TestPillValidatesAsBar(t *testing.T) {
	running := map[string]Run{"7": {Name: "Hades", Start: now.Add(-5 * time.Minute)}}
	long := map[string]Run{"7": {Name: "Baldurs Gate 3 Divinity Original Sin", Start: now.Add(-125 * time.Minute)}}
	multi := map[string]Run{"7": {Name: "Baldurs Gate 3 Divinity Original Sin", Start: now.Add(-5 * time.Minute)}, "3": {Name: "Doom", Start: now.Add(-9 * time.Minute)}}
	for _, n := range []*v1.Node{Pill(nil, false, now), Pill(running, false, now), Pill(nil, true, now), Pill(long, false, now), Pill(multi, false, now)} {
		if err := v1.Validate(n, v1.ViewBar); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if n.Kind != v1.KindRow || len(n.Children) != 1 {
			t.Fatalf("root must be a one-child row, got %s", n.Kind)
		}
		if n.Children[0].Fill != "card" {
			t.Fatalf("pill fill=%q, want card (host capsule double-pill bug)", n.Children[0].Fill)
		}
		if len(n.Children[0].Text) > 24 {
			t.Fatalf("pill text %q exceeds 24-byte bar budget", n.Children[0].Text)
		}
		for _, finding := range shelllint.Tree(geomFit(n), v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
			t.Fatalf("bar lint: %v", finding)
		}
	}
}

func TestPillIdle(t *testing.T) {
	n := Pill(nil, false, now).Children[0]
	if n.Kind != v1.KindButton || n.Key != "bar" {
		t.Fatalf("kind/key: %s/%s", n.Kind, n.Key)
	}
	if n.Icon != "sports_esports" {
		t.Fatalf("idle pill icon=%q", n.Icon)
	}
}

func TestPillLibraryMissing(t *testing.T) {
	n := Pill(nil, true, now).Children[0]
	if n.Tone != v1.ToneSubtle {
		t.Fatalf("missing library tone=%q, want subtle", n.Tone)
	}
	if n.Kind != v1.KindButton {
		t.Fatalf("missing library must stay a button, got %s", n.Kind)
	}
}

func TestPillOneRunning(t *testing.T) {
	running := map[string]Run{"7": {Name: "Hades", Start: now.Add(-42 * time.Minute)}}
	n := Pill(running, false, now).Children[0]
	if n.Text != "Hades · 42m" {
		t.Fatalf("text=%q", n.Text)
	}
	if !n.Tabular {
		t.Fatal("elapsed should be tabular")
	}
}

func TestPillHoursFormat(t *testing.T) {
	running := map[string]Run{"7": {Name: "Hades", Start: now.Add(-125 * time.Minute)}}
	n := Pill(running, false, now).Children[0]
	if n.Text != "Hades · 2h 05m" {
		t.Fatalf("text=%q", n.Text)
	}
}

func TestPillLongNameFits(t *testing.T) {
	running := map[string]Run{"7": {Name: "Baldurs Gate 3 Divinity Original Sin", Start: now.Add(-125 * time.Minute)}}
	text := Pill(running, false, now).Children[0].Text
	if len(text) > 24 {
		t.Fatalf("text=%q (%d bytes) overflows bar budget", text, len(text))
	}
	if !strings.Contains(text, "…") || !strings.HasSuffix(text, " · 2h 05m") {
		t.Fatalf("text=%q, want truncated name + full elapsed", text)
	}
}

func TestPillMultiple(t *testing.T) {
	running := map[string]Run{
		"7": {Name: "Hades", Start: now.Add(-10 * time.Minute)},
		"3": {Name: "Doom", Start: now.Add(-30 * time.Minute)},
		"9": {Name: "Tunic", Start: now.Add(-20 * time.Minute)},
	}
	n := Pill(running, false, now).Children[0]
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
