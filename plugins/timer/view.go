package timer

import v1 "github.com/Nomadcxx/sysc-plugins/internal/wire"

func BarTree(remaining string, running bool) *v1.Node {
	label := "Start"
	id := "start"
	if running {
		label = "Pause"
		id = "pause"
	}
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: remaining, Tabular: true},
		{Kind: v1.KindButton, ID: id, Text: label, Name: label, Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
		{Kind: v1.KindButton, ID: "reset", Text: "Reset", Name: "Reset", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func TooltipTree(remaining string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Timer " + remaining},
	}}
}

func PanelTree(remaining, duration string, running bool, progress float64) *v1.Node {
	bar := BarTree(remaining, running)
	return &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: remaining, Tabular: true},
		{Kind: v1.KindProgress, Value: progress},
		{Kind: v1.KindTextInput, ID: "duration", Text: duration, Name: "Duration", Role: "textbox",
			Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
		bar,
	}}
}
