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

// The panel is 360 wide with 16 of padding, so 328 is the usable track.
// Three pills and their two 8px gaps divide it exactly.
const (
	transportSize = 44
	pillHeight    = 36
	pillWidth     = (360 - 2*16 - 2*8) / 3
)

// PanelTree is the pomodoro panel: the header with its close control, the
// session tally, the radial gauge carrying the countdown, the run controls,
// the phase pills, and the completed footer. The root carries padding so the
// cards never touch the panel chrome.
func PanelTree(remaining string, state State, progress float64, mode Mode, completed, sessions int) *v1.Node {
	// The transport is icon-only and circular, so the three controls read as
	// one group under the ring rather than as three competing labels.
	transport := func(id, icon, name, fill string) *v1.Node {
		return &v1.Node{Kind: v1.KindButton, ID: id, Icon: icon, Name: name, Role: "button",
			Fill: fill, Width: transportSize, Height: transportSize, Radius: transportSize / 2,
			Events: []v1.EventKind{v1.EventActivate}}
	}
	toggle := transport("start", "play_arrow", "Start timer", "accent")
	switch state {
	case StateRunning:
		toggle.ID, toggle.Icon, toggle.Name = "pause", "pause", "Pause timer"
	case StatePaused:
		toggle.Name = "Resume timer"
	}

	// A fixed width divides the row evenly, which the vocabulary has no
	// grow for; the labels stay short so the glyph and text always fit.
	pill := func(id, icon, text, name string, active bool) *v1.Node {
		fill := "chip"
		if active {
			fill = "accent"
		}
		return &v1.Node{Kind: v1.KindButton, ID: id, Icon: icon, Text: text, Name: name, Role: "button",
			Fill: fill, Width: pillWidth, Height: pillHeight, Radius: pillHeight / 2, Gap: 6,
			Events: []v1.EventKind{v1.EventActivate}}
	}

	footer := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 12, Gap: 2,
		Children: []*v1.Node{{
			Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: "check", Tone: v1.ToneAccent},
				{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
					{Kind: v1.KindText, Text: fmt.Sprintf("%d pomodoros completed", completed), Bold: true, Size: "caption"},
					{Kind: v1.KindText, Text: nextLongText(completed, sessions), Tone: v1.ToneSubtle, Size: "caption"},
				}},
			},
		}}}

	return &v1.Node{Kind: v1.KindColumn, Gap: 14, Padding: 16, Children: []*v1.Node{
		{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Pomodoro Timer", Size: "title", Bold: true},
			{Kind: v1.KindButton, ID: "close", Icon: "close", Name: "Close", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
		{Kind: v1.KindText, Text: fmt.Sprintf("Focus session • %d completed", completed),
			Tone: v1.ToneSubtle, Size: "caption", CenterX: true},
		{Kind: v1.KindGauge, Height: 184, Value: 1 - progress, ValueText: remaining},
		{Kind: v1.KindText, Text: modeLabel(mode), Tone: v1.ToneSubtle, Size: "caption", CenterX: true},
		{Kind: v1.KindRow, Gap: 14, CenterX: true, Children: []*v1.Node{
			toggle,
			transport("reset", "restart_alt", "Reset timer", "soft"),
			transport("skip", "skip_next", "Skip to the next phase", "soft"),
		}},
		{Kind: v1.KindRow, Gap: 8, CenterX: true, Children: []*v1.Node{
			pill("mode-work", "desktop_windows", "Work", "Work", mode == ModeWork),
			pill("mode-short", "coffee", "Short", "Short break", mode == ModeShort),
			pill("mode-long", "bedtime", "Long", "Long break", mode == ModeLong),
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
