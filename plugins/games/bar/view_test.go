package bar

import (
	"fmt"
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

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
		for _, finding := range shelllint.Tree(n, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
			t.Fatalf("bar lint: %v", finding)
		}
	}
}

func TestPillFitsSideWidths(t *testing.T) {
	running := map[string]Run{"7": {Name: "Baldurs Gate 3 Divinity Original Sin", Start: now.Add(-125 * time.Minute)}}
	for _, width := range []int{shelllint.BarWidth, 28, 32, 64} {
		bar := Pill(running, false, now)
		if width != shelllint.BarWidth {
			bar = PillAtWidth(running, false, now, width)
		}
		for _, finding := range shelllint.Tree(bar, v1.ViewBar, width, shelllint.BarHeight) {
			t.Errorf("width %d: %s", width, finding)
		}
		if width <= 64 && bar.Children[0].Text != "" {
			t.Errorf("width %d label %q should remain in the tooltip", width, bar.Children[0].Text)
		}
		button := bar.Children[0]
		if button.ID != "bar" || button.Name != "games" || button.Role != "button" || len(button.Events) != 2 || button.Events[0] != v1.EventActivate || button.Events[1] != v1.EventPointer {
			t.Fatalf("width %d bar interaction = %+v", width, button)
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

func TestTooltipValidatesAndFits(t *testing.T) {
	running := map[string]Run{
		"7": {Name: "Hades", Start: now.Add(-5 * time.Minute)},
		"3": {Name: "Baldurs Gate 3 Divinity Original Sin", Start: now.Add(-125 * time.Minute)},
	}
	many := map[string]Run{}
	for i := 0; i < 9; i++ {
		many[fmt.Sprintf("%d", i)] = Run{Name: fmt.Sprintf("Game %d", i), Start: now.Add(-time.Duration(i) * time.Minute)}
	}
	for _, n := range []*v1.Node{
		TooltipTree(nil, false, 53, now),
		TooltipTree(nil, true, 0, now),
		TooltipTree(running, false, 53, now),
		TooltipTree(many, false, 53, now),
	} {
		if err := v1.Validate(n, v1.ViewTooltip); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if n.Kind != v1.KindColumn {
			t.Fatalf("tooltip root must be a column, got %s", n.Kind)
		}
		for _, finding := range shelllint.Tree(n, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
			t.Fatalf("tooltip lint: %v", finding)
		}
	}
}

func TestTooltipContent(t *testing.T) {
	running := map[string]Run{
		"7": {Name: "Hades", Start: now.Add(-5 * time.Minute)},
		"3": {Name: "Doom", Start: now.Add(-30 * time.Minute)},
	}
	n := TooltipTree(running, false, 53, now)
	if n.Children[0].Text != "Games" {
		t.Fatalf("title=%q", n.Children[0].Text)
	}
	first := n.Children[1]
	if first.Kind != v1.KindRow || first.Children[1].Text != "Hades" || first.Children[2].Text != "5m" {
		t.Fatalf("first row=%+v, want Hades 5m newest-first", first)
	}
	if got := TooltipTree(nil, false, 53, now).Children[1].Text; got != "No games running" {
		t.Fatalf("idle text=%q", got)
	}
	if got := TooltipTree(nil, true, 0, now).Children[1].Text; got != "Lutris library not found" {
		t.Fatalf("missing text=%q", got)
	}
	many := map[string]Run{}
	for i := 0; i < 9; i++ {
		many[fmt.Sprintf("%d", i)] = Run{Name: fmt.Sprintf("Game %d", i), Start: now.Add(-time.Duration(i) * time.Minute)}
	}
	last := TooltipTree(many, false, 53, now).Children
	if got := last[len(last)-1].Text; got != "+3 more" {
		t.Fatalf("overflow line=%q, want +3 more", got)
	}
}
