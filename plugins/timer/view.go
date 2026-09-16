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
// progress meter, the duration field while idle, and reset plus start or
// pause controls. The reset paints as the destructive chip.
func PanelTree(remaining string, state State, progress float64, duration string) *v1.Node {
	tone := v1.ToneNormal
	switch state {
	case StateRunning, StatePaused:
		tone = v1.ToneAccent
	case StateNotify:
		tone = v1.ToneError
	}

	col := &v1.Node{Kind: v1.KindColumn, Gap: 10, Children: []*v1.Node{
		{Kind: v1.KindText, Text: remaining, Tabular: true, Tone: tone, Height: 36},
		{Kind: v1.KindProgress, Value: progress},
	}}

	if state == StateIdle {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindTextInput, ID: "duration", Text: duration, Name: "Duration", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}})
	}

	controls := []*v1.Node{
		{Kind: v1.KindButton, ID: "reset", Text: "Reset", Name: "Reset timer", Role: "button",
			Tone: v1.ToneError, Events: []v1.EventKind{v1.EventActivate}},
	}
	switch state {
	case StateRunning:
		controls = append(controls,
			&v1.Node{Kind: v1.KindButton, ID: "pause", Text: "Pause", Name: "Pause timer", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}})
	case StatePaused:
		controls = append(controls,
			&v1.Node{Kind: v1.KindButton, ID: "start", Text: "Resume", Name: "Resume timer", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}})
	case StateNotify:
		controls = append(controls,
			&v1.Node{Kind: v1.KindButton, ID: "start", Text: "Clear", Name: "Clear fired timer", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}})
	default:
		controls = append(controls,
			&v1.Node{Kind: v1.KindButton, ID: "start", Text: "Start", Name: "Start timer", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}})
	}
	col.Children = append(col.Children, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: controls})

	if state == StateIdle {
		col.Children = append(col.Children, &v1.Node{
			Kind: v1.KindText, Text: "90 · 5m · m:ss · 1h30m", Tone: v1.ToneSubtle})
	}
	return col
}
