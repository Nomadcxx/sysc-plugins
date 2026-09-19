package timer

import (
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

func TestPanelTreeTracksState(t *testing.T) {
	t.Parallel()
	idle := PanelTree("05:00", StateIdle, 1.0, "5m")
	var hasInput, hasProgress, hasHint bool
	var reset, start *v1.Node
	for _, c := range idle.Children {
		switch {
		case c.ID == "duration":
			hasInput = true
			if c.Disabled {
				t.Fatal("idle duration field is disabled")
			}
		case c.Kind == "progress":
			hasProgress = true
		case c.Tone == v1.ToneSubtle:
			hasHint = true
		}
		for _, b := range c.Children {
			if b.ID == "reset" {
				reset = b
			}
			if b.ID == "start" {
				start = b
			}
		}
	}
	if !hasInput || !hasProgress || !hasHint {
		t.Fatalf("idle panel missing parts: input=%v progress=%v hint=%v", hasInput, hasProgress, hasHint)
	}
	if big := idle.Children[0]; big.Size != "display" || !big.Bold || !big.CenterX {
		t.Fatalf("idle time = %+v, want display-size bold centred", big)
	}
	if reset == nil || reset.Tone != v1.ToneError || reset.Fill != "error" {
		t.Fatalf("reset = %+v, want the destructive chip", reset)
	}
	if start == nil || start.Text != "Start" || start.Fill != "accent" {
		t.Fatalf("start = %+v", start)
	}

	running := PanelTree("04:12", StateRunning, 0.5, "5m")
	var runningInput, runningToggle *v1.Node
	for _, c := range running.Children {
		if c.ID == "duration" {
			runningInput = c
		}
		for _, b := range c.Children {
			if b.ID == "pause" {
				runningToggle = b
			}
		}
	}
	if runningInput == nil || !runningInput.Disabled {
		t.Fatalf("running duration field = %+v, want disabled", runningInput)
	}
	if runningToggle == nil || runningToggle.Text != "Pause" || runningToggle.Fill != "soft" {
		t.Fatalf("running toggle = %+v", runningToggle)
	}
	if big := running.Children[0]; big.Tone != v1.ToneAccent {
		t.Fatalf("running time tone = %v", big.Tone)
	}

	notify := PanelTree("00:00", StateNotify, 0, "5m")
	if notify.Children[0].Tone != v1.ToneError {
		t.Fatalf("notify time tone = %v", notify.Children[0].Tone)
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
		if err := v1.Validate(PanelTree("04:12", state, 0.5, "5m"), v1.ViewPanel); err != nil {
			t.Fatalf("panel %s: %v", state, err)
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
