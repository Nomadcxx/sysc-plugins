package timer

import v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"

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

// PanelTree is the noctalia timer panel: the big remaining time over a
// progress meter, the duration field, and reset plus start or pause controls.
// The field stays visible while the countdown runs, greyed out, so the panel
// does not reflow; the reset paints as the destructive chip.
func PanelTree(remaining string, state State, progress float64, duration string) *v1.Node {
	tone := v1.ToneNormal
	switch state {
	case StateRunning, StatePaused:
		tone = v1.ToneAccent
	case StateNotify:
		tone = v1.ToneError
	}

	col := &v1.Node{Kind: v1.KindColumn, Gap: 10, Children: []*v1.Node{
		{Kind: v1.KindText, Text: remaining, Tabular: true, Tone: tone,
			Size: "display", Bold: true, CenterX: true, Height: 48},
		{Kind: v1.KindProgress, Value: progress},
	}}

	toggle := v1.Node{Kind: v1.KindButton, ID: "start", Name: "Start timer", Role: "button",
		Fill: "accent", Events: []v1.EventKind{v1.EventActivate}}
	reset := v1.Node{Kind: v1.KindButton, ID: "reset", Text: "Reset", Name: "Reset timer", Role: "button",
		Tone: v1.ToneError, Fill: "error", Events: []v1.EventKind{v1.EventActivate}}
	input := v1.Node{Kind: v1.KindTextInput, ID: "duration", Text: duration, Name: "Duration", Role: "textbox",
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}}

	switch state {
	case StateRunning:
		toggle.ID, toggle.Text, toggle.Name, toggle.Fill = "pause", "Pause", "Pause timer", "soft"
		input.Disabled = true
		col.Children = append(col.Children, &input, &v1.Node{Kind: v1.KindRow, Gap: 8,
			Children: []*v1.Node{&reset, &toggle}})
	case StatePaused:
		toggle.Text, toggle.Name, toggle.Fill = "Resume", "Resume timer", "soft"
		input.Disabled = true
		col.Children = append(col.Children, &input, &v1.Node{Kind: v1.KindRow, Gap: 8,
			Children: []*v1.Node{&reset, &toggle}})
	default:
		toggle.Text, toggle.Name = "Start", "Start timer"
		col.Children = append(col.Children, &input, &v1.Node{Kind: v1.KindRow, Gap: 8,
			Children: []*v1.Node{&reset, &toggle}})
	}

	if state == StateIdle {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText,
			Text: "90 · 5m · 1030 = 10:30 · 1h30m", Tone: v1.ToneSubtle, Size: "caption", CenterX: true})
	}
	return col
}
