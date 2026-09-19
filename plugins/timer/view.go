package timer

import (
	"fmt"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// barTone follows the noctalia bar: the glyph paints in the accent while the
// countdown runs, in the error colour once it fires, and plain otherwise.
func barTone(state State) v1.Tone {
	switch state {
	case StateRunning, StatePaused:
		return v1.ToneAccent
	case StateNotify:
		return v1.ToneError
	}
	return v1.ToneNormal
}

// BarTree is one control carrying the glyph beside the countdown, the
// noctalia bar-widget shape. Clicking it opens the panel -- or, in the
// notify state, clears the fired timer. When showWhenIdle is false the idle
// timer collapses to the glyph alone.
func BarTree(remaining string, state State, showWhenIdle bool) *v1.Node {
	text := remaining
	if state == StateIdle && !showWhenIdle {
		text = ""
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Icon: "schedule", Text: text,
		Name: "Open timer", Role: "button", Tone: barTone(state), Tabular: true,
		Events: []v1.EventKind{v1.EventActivate},
	}}}
}

func TooltipTree(remaining string, state State) *v1.Node {
	line := "Timer " + remaining
	switch state {
	case StateRunning:
		line += " · running"
	case StatePaused:
		line += " · paused"
	case StateNotify:
		line = "Timer done"
	}
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: line},
	}}
}

// PanelTree is the pomodoro panel: the header with its close control, the
// session tally, the radial gauge carrying the countdown, the run controls,
// the phase pills, and the completed footer. The root carries padding so the
// cards never touch the panel chrome.
func PanelTree(remaining string, state State, progress float64, mode Mode, completed, sessions int) *v1.Node {
	toggle := v1.Node{Kind: v1.KindButton, ID: "start", Text: "Start", Name: "Start timer", Role: "button",
		Fill: "accent", Events: []v1.EventKind{v1.EventActivate}}
	switch state {
	case StateRunning:
		toggle.ID, toggle.Text, toggle.Name, toggle.Fill = "pause", "Pause", "Pause timer", "soft"
	case StatePaused:
		toggle.Text, toggle.Name, toggle.Fill = "Resume", "Resume timer", "soft"
	}
	reset := v1.Node{Kind: v1.KindButton, ID: "reset", Text: "Reset", Name: "Reset timer", Role: "button",
		Fill: "soft", Events: []v1.EventKind{v1.EventActivate}}

	pill := func(id, text string, active bool) *v1.Node {
		fill := "chip"
		if active {
			fill = "accent"
		}
		return &v1.Node{Kind: v1.KindButton, ID: id, Text: text, Name: text, Role: "button",
			Fill: fill, Events: []v1.EventKind{v1.EventActivate}}
	}

	footer := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 10, Padding: 10, Gap: 2,
		Children: []*v1.Node{{
			Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindText, Text: "✓", Tone: v1.ToneSubtle},
				{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
					{Kind: v1.KindText, Text: fmt.Sprintf("%d pomodoros completed", completed), Bold: true},
					{Kind: v1.KindText, Text: nextLongText(completed, sessions), Tone: v1.ToneSubtle, Size: "caption"},
				}},
			},
		}}}

	return &v1.Node{Kind: v1.KindColumn, Gap: 12, Padding: 16, Children: []*v1.Node{
		{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Pomodoro Timer", Size: "title", Bold: true},
			{Kind: v1.KindButton, ID: "close", Text: "✕", Name: "Close", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
		{Kind: v1.KindText, Text: fmt.Sprintf("Focus session • %d completed", completed),
			Tone: v1.ToneSubtle, Size: "caption", CenterX: true},
		{Kind: v1.KindGauge, Height: 160, Value: 1 - progress, ValueText: remaining},
		{Kind: v1.KindText, Text: modeLabel(mode), Tone: v1.ToneSubtle, CenterX: true},
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{&toggle, &reset}},
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			pill("mode-work", "Work", mode == ModeWork),
			pill("mode-short", "Short Break", mode == ModeShort),
			pill("mode-long", "Long Break", mode == ModeLong),
		}},
		footer,
	}}
}

func modeLabel(m Mode) string {
	switch m {
	case ModeShort:
		return "Short Break"
	case ModeLong:
		return "Long Break"
	}
	return "Work"
}

func nextLongText(completed, sessions int) string {
	if sessions < 1 {
		sessions = 1
	}
	more := sessions - completed%sessions
	return fmt.Sprintf("Next long break after %d more", more)
}
