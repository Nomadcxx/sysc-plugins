package wallpaperdepth

import (
	"strconv"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarTree renders the bar pill with the helper's coarse readiness.
func BarTree(ready bool) *v1.Node {
	label := "depth"
	row := &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindText, Text: label},
	}}
	if ready {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: "ready"})
	}
	return row
}

// TooltipText summarizes readiness for the bar tooltip.
func TooltipText(ready, runtimeReady, modelReady bool) string {
	switch {
	case ready:
		return "Wallpaper depth: ready"
	case runtimeReady && !modelReady:
		return "Wallpaper depth: model missing, run setup"
	default:
		return "Wallpaper depth: not set up"
	}
}

// PanelTree shows helper status and the stub's operations. The real feature —
// compositing widgets behind the wallpaper — needs a shell surface API that
// does not exist yet; see the plugin README.
func PanelTree(status HelperStatus, errMsg string, busy bool, lastMask string, wallpaper string, threshold, feather int) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Wallpaper Depth (stub)"},
		{Kind: v1.KindText, Text: "Model: Depth Anything V2 Small (onnx)"},
	}}
	if busy {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "Working…"})
	}
	col.Children = append(col.Children,
		&v1.Node{Kind: v1.KindText, Text: boolLine("Runtime ready", status.RuntimeReady)},
		&v1.Node{Kind: v1.KindText, Text: boolLine("Model ready", status.ModelReady)},
	)
	if wallpaper != "" {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "Wallpaper: " + wallpaper})
	} else {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: "Wallpaper: not set", Tone: v1.ToneError})
	}
	col.Children = append(col.Children,
		&v1.Node{Kind: v1.KindText, Text: "Threshold " + strconv.Itoa(threshold) + " · Feather " + strconv.Itoa(feather)},
	)
	if lastMask != "" {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "Last mask: " + lastMask})
	}
	if errMsg != "" {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: errMsg, Tone: v1.ToneError})
	}
	col.Children = append(col.Children, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindButton, ID: "check", Text: "Check", Name: "Check helper status", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
		{Kind: v1.KindButton, ID: "setup", Text: "Run setup", Name: "Run helper setup", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
		{Kind: v1.KindButton, ID: "generate", Text: "Generate mask", Name: "Generate depth mask", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
		{Kind: v1.KindButton, ID: "clear-cache", Text: "Clear cache", Name: "Clear mask cache", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
	}})
	return col
}

func boolLine(label string, ok bool) string {
	if ok {
		return label + ": yes"
	}
	return label + ": no"
}
