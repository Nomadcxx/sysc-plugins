package wallpaperdepth

import (
	"fmt"
	"strconv"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarTree keeps the bar affordance to one glyph; its tone carries readiness.
func BarTree(snapshot ControllerSnapshot) *v1.Node {
	tone := v1.ToneNormal
	switch {
	case snapshot.Error != "":
		tone = v1.ToneError
	case snapshot.Busy:
		tone = v1.ToneAccent
	case !snapshot.Checked || !snapshot.Helper.Ready:
		tone = v1.ToneSubtle
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Icon: "wallpaper",
		Name: "Open Wallpaper Depth panel", Role: "button",
		Tone: tone, Events: []v1.EventKind{v1.EventActivate},
	}}}
}

func TooltipTree(snapshot ControllerSnapshot) *v1.Node {
	state, tone := helperState(snapshot)
	outputs := "No wallpaper outputs"
	if len(snapshot.Rows) != 0 {
		outputs = fmt.Sprintf("%d wallpaper outputs", len(snapshot.Rows))
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Wallpaper Depth", Size: "title"},
		{Kind: v1.KindText, Text: state, Tone: tone},
		{Kind: v1.KindText, Text: outputs, Tone: v1.ToneSubtle},
	}}
}

func PanelTree(snapshot ControllerSnapshot) *v1.Node {
	state, tone := helperState(snapshot)
	automatic := "off"
	if snapshot.Settings.AutoGenerate {
		automatic = "on"
	}
	setupChildren := []*v1.Node{
		{Kind: v1.KindText, Text: "Depth Anything V2 Small", Bold: true},
		{Kind: v1.KindText, Text: state, Tone: tone},
	}
	if snapshot.Error != "" {
		setupChildren = append(setupChildren, &v1.Node{Kind: v1.KindText, Text: snapshot.Error, Tone: v1.ToneError})
	}
	setupCard := &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: 10, Gap: 4, Children: setupChildren}

	return &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Wallpaper Depth", Size: "title"},
		{Kind: v1.KindText, Text: "Local depth masks reveal foreground scenery over the centred clock."},
		setupCard,
		{Kind: v1.KindRow, Gap: 12, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Automatic " + automatic},
			{Kind: v1.KindText, Text: "Threshold " + strconv.Itoa(snapshot.Settings.Threshold)},
			{Kind: v1.KindText, Text: "Feather " + strconv.Itoa(snapshot.Settings.Feather)},
		}},
		outputList(snapshot),
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			actionButton("check", "Check", "Check helper status", snapshot.Busy),
			actionButton("setup", "Run setup", "Set up the local runtime and model", snapshot.Busy),
			actionButton("clear-cache", "Clear cache", "Clear generated depth masks", snapshot.Busy),
		}},
	}}
}

func helperState(snapshot ControllerSnapshot) (string, v1.Tone) {
	switch {
	case snapshot.Operation == string(operationSetup):
		return "Setting up helper…", v1.ToneAccent
	case snapshot.Operation == string(operationCheck) || !snapshot.Checked:
		return "Checking helper status…", v1.ToneSubtle
	case snapshot.Error != "":
		return "Helper error", v1.ToneError
	case !snapshot.Helper.Ready:
		return "Setup required", v1.ToneSubtle
	case snapshot.Busy:
		return "Generating depth masks…", v1.ToneAccent
	default:
		return "Ready", v1.ToneAccent
	}
}

func outputList(snapshot ControllerSnapshot) *v1.Node {
	children := make([]*v1.Node, 0, len(snapshot.Rows))
	if len(snapshot.Rows) == 0 {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "No outputs detected"})
	} else {
		for _, row := range snapshot.Rows {
			children = append(children, outputRow(row, snapshot.Busy || !snapshot.Helper.Ready))
		}
	}
	return &v1.Node{Kind: v1.KindList, Height: 250, Gap: 6, Events: []v1.EventKind{v1.EventScroll}, Children: children}
}

func outputRow(row OutputRow, disabled bool) *v1.Node {
	status, tone := outputStatus(row)
	children := []*v1.Node{
		{Kind: v1.KindText, Text: row.Output},
		{Kind: v1.KindText, Text: status, Tone: tone},
	}
	if row.State == "image" {
		children = append(children, &v1.Node{
			Kind: v1.KindButton, ID: "generate-" + row.Output,
			Text: "Generate", Name: "Generate depth mask for " + row.Output, Role: "button",
			Disabled: disabled, Events: []v1.EventKind{v1.EventActivate},
		})
	}
	return &v1.Node{Kind: v1.KindRow, Height: 40, Padding: 6, Gap: 8, Children: children}
}

func outputStatus(row OutputRow) (string, v1.Tone) {
	if row.Error != "" {
		return "Error: " + row.Error, v1.ToneError
	}
	switch row.Status {
	case "processing":
		return "Processing", v1.ToneAccent
	case "ready":
		if row.CacheHit {
			return "Ready · cached", v1.ToneAccent
		}
		return "Ready", v1.ToneAccent
	case "waiting":
		return "Waiting", v1.ToneSubtle
	case "error":
		return "Error", v1.ToneError
	case "unsupported":
		switch row.State {
		case "video":
			return "Video wallpaper", v1.ToneSubtle
		case "covered":
			return "Covered by another wallpaper", v1.ToneSubtle
		case "none":
			return "No wallpaper", v1.ToneSubtle
		case "transitioning":
			return "Wallpaper changing", v1.ToneSubtle
		default:
			return "Unsupported wallpaper", v1.ToneSubtle
		}
	default:
		return "Waiting", v1.ToneSubtle
	}
}

func actionButton(id, text, name string, disabled bool) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Text: text, Name: name, Role: "button",
		Disabled: disabled, Events: []v1.EventKind{v1.EventActivate},
	}
}
