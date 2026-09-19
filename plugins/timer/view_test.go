package timer

import (
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestBarTreeIsOneControlWithGlyphAndLabel(t *testing.T) {
	t.Parallel()
	root := BarTree("04:12", StateIdle, true)
	if root.Kind != "row" || len(root.Children) != 1 {
		t.Fatalf("bar = %+v", root)
	}
	open := root.Children[0]
	if open.ID != "open" || open.Name == "" || open.Role == "" || open.Icon != "schedule" {
		t.Fatalf("open control = %+v", open)
	}
	if open.Text != "04:12" {
		t.Fatalf("label = %q", open.Text)
	}
	if open.Tone != v1.ToneNormal {
		t.Fatalf("idle tone = %v", open.Tone)
	}
}

func TestBarTreeHidesLabelWhenIdleAndConfigured(t *testing.T) {
	t.Parallel()
	root := BarTree("05:00", StateIdle, false)
	if root.Children[0].Text != "" {
		t.Fatalf("idle label = %q, want icon only", root.Children[0].Text)
	}
	running := BarTree("04:12", StateRunning, false)
	if running.Children[0].Text != "04:12" {
		t.Fatalf("running label = %q", running.Children[0].Text)
	}
}

func TestBarTreeToneFollowsState(t *testing.T) {
	t.Parallel()
	if got := BarTree("04:12", StateRunning, true).Children[0].Tone; got != v1.ToneAccent {
		t.Fatalf("running tone = %v", got)
	}
	if got := BarTree("00:00", StateNotify, true).Children[0].Tone; got != v1.ToneError {
		t.Fatalf("notify tone = %v", got)
	}
	if got := BarTree("04:12", StatePaused, true).Children[0].Tone; got != v1.ToneAccent {
		t.Fatalf("paused tone = %v", got)
	}
}

func TestPanelTreeIsThePomodoroLayout(t *testing.T) {
	t.Parallel()
	idle := PanelTree("25:00", StateIdle, 1.0, ModeWork, 0, 4)
	if idle.Padding != 16 {
		t.Fatalf("root padding = %d, want 16", idle.Padding)
	}
	var gauge, header, subtitle, modeText, footer *v1.Node
	var toggle, reset *v1.Node
	pills := map[string]*v1.Node{}
	for _, c := range idle.Children {
		switch {
		case c.Kind == v1.KindGauge:
			gauge = c
		case c.Kind == v1.KindColumn && c.Fill == "card":
			footer = c
		case c.Kind == v1.KindRow && c.PinEnd:
			header = c
		case c.Text == "Work":
			modeText = c
		case strings.HasPrefix(c.Text, "Focus session"):
			subtitle = c
		}
		for _, b := range c.Children {
			switch {
			case b.ID == "start" || b.ID == "pause":
				toggle = b
			case b.ID == "reset":
				reset = b
			case strings.HasPrefix(b.ID, "mode-"):
				pills[b.ID] = b
			}
		}
	}
	if header == nil {
		t.Fatal("panel has no header row")
	}
	var title *v1.Node
	var close *v1.Node
	for _, c := range header.Children {
		if c.Text == "Pomodoro Timer" {
			title = c
		}
		if c.ID == "close" {
			close = c
		}
	}
	if title == nil || title.Size != "title" || !title.Bold {
		t.Fatalf("header title = %+v, want bold title size", title)
	}
	if close == nil {
		t.Fatal("header has no close control")
	}
	if subtitle == nil || subtitle.Tone != v1.ToneSubtle || subtitle.Size != "caption" || !subtitle.CenterX {
		t.Fatalf("subtitle = %+v", subtitle)
	}
	if !strings.Contains(subtitle.Text, "0 completed") {
		t.Fatalf("subtitle = %q, want the session tally", subtitle.Text)
	}
	if gauge == nil || gauge.Height != 160 || gauge.ValueText != "25:00" || gauge.Value != 0 {
		t.Fatalf("gauge = %+v", gauge)
	}
	if modeText == nil || modeText.Text != "Work" || !modeText.CenterX {
		t.Fatalf("mode label = %+v", modeText)
	}
	if toggle == nil || toggle.Text != "Start" || toggle.Fill != "accent" {
		t.Fatalf("toggle = %+v", toggle)
	}
	if reset == nil || reset.Fill != "soft" {
		t.Fatalf("reset = %+v", reset)
	}
	if pills["mode-work"] == nil || pills["mode-work"].Fill != "accent" {
		t.Fatalf("work pill = %+v", pills["mode-work"])
	}
	if pills["mode-short"] == nil || pills["mode-short"].Fill != "chip" {
		t.Fatalf("short pill = %+v", pills["mode-short"])
	}
	if pills["mode-long"] == nil || pills["mode-long"].Fill != "chip" {
		t.Fatalf("long pill = %+v", pills["mode-long"])
	}
	if footer == nil || footer.Radius != 10 {
		t.Fatalf("footer = %+v", footer)
	}
	footerText := treeText(footer)
	if !strings.Contains(footerText, "0 pomodoros completed") ||
		!strings.Contains(footerText, "Next long break after 4 more") {
		t.Fatalf("footer text = %q", footerText)
	}

	running := PanelTree("04:12", StateRunning, 0.5, ModeWork, 1, 4)
	var runningToggle, runningGauge *v1.Node
	for _, c := range running.Children {
		if c.Kind == v1.KindGauge {
			runningGauge = c
		}
		for _, b := range c.Children {
			if b.ID == "pause" {
				runningToggle = b
			}
		}
	}
	if runningToggle == nil || runningToggle.Text != "Pause" || runningToggle.Fill != "soft" {
		t.Fatalf("running toggle = %+v", runningToggle)
	}
	if runningGauge == nil || runningGauge.Value != 0.5 || runningGauge.ValueText != "04:12" {
		t.Fatalf("running gauge = %+v", runningGauge)
	}

	paused := PanelTree("04:12", StatePaused, 0.5, ModeShort, 1, 4)
	for _, c := range paused.Children {
		for _, b := range c.Children {
			if b.ID == "start" && (b.Text != "Resume" || b.Fill != "soft") {
				t.Fatalf("paused toggle = %+v", b)
			}
		}
	}
}

func TestTreesValidate(t *testing.T) {
	t.Parallel()
	if err := v1.Validate(BarTree("04:12", StateRunning, true), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree("05:00", StateIdle, false), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(TooltipTree("04:12", StatePaused), v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{StateIdle, StateRunning, StatePaused, StateNotify} {
		for _, mode := range []Mode{ModeWork, ModeShort, ModeLong} {
			panel := PanelTree("04:12", state, 0.5, mode, 1, 4)
			if err := v1.Validate(panel, v1.ViewPanel); err != nil {
				t.Fatalf("panel %s/%s: %v", state, mode, err)
			}
		}
	}
}

func TestModelStateTransitions(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tm := New(func() time.Time { return now })
	if got := tm.State(); got != StateIdle {
		t.Fatalf("fresh state = %q", got)
	}
	tm.SetDuration(2 * time.Minute)
	tm.Start()
	if got := tm.State(); got != StateRunning {
		t.Fatalf("running state = %q", got)
	}
	now = now.Add(30 * time.Second)
	tm.Pause()
	if got := tm.State(); got != StatePaused {
		t.Fatalf("paused state = %q", got)
	}
	tm.Start()
	now = now.Add(2 * time.Minute)
	if _, done := tm.Tick(); !done {
		t.Fatal("countdown did not fire")
	}
	if got := tm.State(); got != StateNotify {
		t.Fatalf("notify state = %q", got)
	}
	if !tm.Fired() {
		t.Fatal("Fired = false after completion")
	}
	tm.Reset()
	if got := tm.State(); got != StateIdle {
		t.Fatalf("state after reset = %q", got)
	}
}

func TestFormatClockSwitchesToHours(t *testing.T) {
	t.Parallel()
	if got := FormatClock(59 * time.Second); got != "00:59" {
		t.Fatalf("59s = %q", got)
	}
	if got := FormatClock(90 * time.Minute); got != "1:30:00" {
		t.Fatalf("90m = %q", got)
	}
	if got := FormatClock(-time.Second); got != "00:00" {
		t.Fatalf("negative = %q", got)
	}
}

func treeText(n *v1.Node) string {
	if n == nil {
		return ""
	}
	out := n.Text
	for _, c := range n.Children {
		out += " " + treeText(c)
	}
	return out
}
