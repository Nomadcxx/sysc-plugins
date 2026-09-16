package worldclock

import (
	"strconv"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func BarTree(first Reading) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Key: "time", Text: first.Clock, Tabular: true},
		{Kind: v1.KindButton, ID: "open", Text: first.Zone, Name: "Open world clock", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func TimePatch(readings []Reading) []v1.Replacement {
	out := make([]v1.Replacement, 0, len(readings)+1)
	if len(readings) > 0 {
		out = append(out, v1.Replacement{
			Key:  "time",
			Node: &v1.Node{Kind: v1.KindText, Key: "time", Text: readings[0].Clock, Tabular: true},
		})
	}
	for _, r := range readings {
		key := "time:" + r.Zone
		out = append(out, v1.Replacement{
			Key:  key,
			Node: &v1.Node{Kind: v1.KindText, Key: key, Text: r.Clock, Tabular: true},
		})
	}
	return out
}

func PanelTree(readings []Reading, pendingAdd, pendingRemove, draft string) *v1.Node {
	rows := make([]*v1.Node, 0, len(readings)+4)
	for i, r := range readings {
		rows = append(rows, &v1.Node{Kind: v1.KindRow, Gap: 8, Key: "row:" + r.Zone, Children: []*v1.Node{
			{
				Kind: v1.KindDragSource, ID: "drag:" + r.Zone, Key: "drag:" + r.Zone, Text: "=",
				Name: "Reorder " + r.Zone, Role: "button", DragType: "zone", Payload: r.Zone,
				Events: []v1.EventKind{v1.EventPointer},
			},
			{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
				{Kind: v1.KindText, Text: r.Label},
				{Kind: v1.KindText, Text: r.Zone, Tone: v1.ToneSubtle},
			}},
			{Kind: v1.KindText, Key: "time:" + r.Zone, Text: r.Clock, Tabular: true},
			{Kind: v1.KindText, Text: r.Offset},
			{
				Kind: v1.KindButton, ID: "rm:" + r.Zone, Text: "Remove", Name: "Remove " + r.Zone, Role: "button",
				Events: []v1.EventKind{v1.EventActivate},
			},
			{
				Kind: v1.KindDropZone, ID: "drop:" + strconv.Itoa(i), Accept: []string{"zone"},
				Events: []v1.EventKind{v1.EventDrop},
			},
		}})
	}
	rows = append(rows, &v1.Node{
		Kind: v1.KindDropZone, ID: "drop:" + strconv.Itoa(len(readings)), Accept: []string{"zone"},
		Events: []v1.EventKind{v1.EventDrop},
	})
	switch {
	case pendingAdd != "":
		rows = append(rows, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Add " + pendingAdd + "?"},
			{Kind: v1.KindButton, ID: "confirm-add", Text: "Add", Name: "Confirm add", Role: "button", Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "cancel", Text: "Cancel", Name: "Cancel", Role: "button", Events: []v1.EventKind{v1.EventActivate}},
		}})
	case pendingRemove != "":
		rows = append(rows, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Remove " + pendingRemove + "?"},
			{Kind: v1.KindButton, ID: "confirm-remove", Text: "Remove", Name: "Confirm remove", Role: "button", Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "cancel", Text: "Cancel", Name: "Cancel", Role: "button", Events: []v1.EventKind{v1.EventActivate}},
		}})
	default:
		rows = append(rows, &v1.Node{Kind: v1.KindTextInput, ID: "zone", Text: draft, Name: "Add a city", Role: "textbox",
			Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindList, Height: 240, Children: rows},
	}}
}
