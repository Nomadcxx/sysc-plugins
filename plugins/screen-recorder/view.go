package recorder

import (
	"fmt"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	nodeToggle = "toggle"
	nodeRecord = "record"
	nodeStop   = "stop"
	nodeReplay = "replay"
	nodeSave   = "save"
)

// barPresentation mirrors the noctalia widget: a glyph that follows the
// recorder state, the elapsed time while a recording runs, and nothing
// otherwise. Recording paints in the error colour -- the universal "this is
// live" signal -- and a failed recorder paints its broken glyph.
func barPresentation(snap Snapshot) (icon string, tone v1.Tone, text string) {
	switch snap.Mode {
	case Unavailable, Failed:
		return "camera-off", v1.ToneError, ""
	case Recording, Adopted:
		return "stop", v1.ToneError, FormatElapsed(snap.Elapsed)
	case Stopping:
		return "stop", v1.ToneNormal, ""
	case ReplayActive:
		return "record", v1.ToneNormal, ""
	default:
		return "record", v1.ToneNormal, ""
	}
}

// BarTree is one control: the glyph beside the elapsed time, the whole thing
// toggling the recording. A running replay grows a save control beside it.
func BarTree(snap Snapshot, cfg Config) *v1.Node {
	icon, tone, text := barPresentation(snap)
	children := []*v1.Node{{
		Kind: v1.KindButton, ID: nodeToggle, Key: nodeToggle,
		Icon: icon, Text: text, Name: "Toggle recording", Role: "button",
		Tone: tone, Tabular: true,
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}}
	if snap.Mode == ReplayActive {
		children = append(children, &v1.Node{
			Kind: v1.KindButton, ID: nodeSave, Key: nodeSave,
			Text: "Save", Name: "Save replay", Role: "button",
			Events: []v1.EventKind{v1.EventActivate},
		})
	}
	// Hide when idle collapses the widget to nothing rather than a dead glyph.
	if snap.Mode == Idle && cfg.HideInactive {
		children = nil
	}
	return &v1.Node{Kind: v1.KindRow, Children: children}
}

func TooltipTree(snap Snapshot) *v1.Node {
	label, _ := presentation(snap)
	children := []*v1.Node{{Kind: v1.KindText, Text: label}}
	if snap.Artifact != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: snap.Artifact, Tone: v1.ToneSubtle})
	}
	if snap.Err != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: snap.Err, Tone: v1.ToneError})
	}
	if snap.Logs != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: snap.Logs, Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: children}
}

func PanelTree(snap Snapshot, cfg Config, now time.Time) *v1.Node {
	_ = now
	icon, _ := cameraPresentation(snap)
	label, tone := presentation(snap)
	children := []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon, Name: "Screen Recorder"},
			{Kind: v1.KindText, Text: "Screen Recorder"},
		}},
	}
	status := []*v1.Node{{Kind: v1.KindText, Text: label, Tone: tone}}
	if snap.Mode == Recording || snap.Mode == Adopted || snap.Mode == ReplayActive {
		elapsed := v1.ToneAccent
		if snap.Mode != ReplayActive {
			elapsed = v1.ToneError
		}
		status = append(status, &v1.Node{
			Kind: v1.KindText, Key: "elapsed", Tabular: true, Tone: elapsed,
			Text: FormatElapsed(snap.Elapsed),
		})
	}
	children = append(children, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: status})
	if snap.Err != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: snap.Err, Tone: v1.ToneError})
	}

	stop := &v1.Node{Kind: v1.KindButton, ID: nodeStop, Key: nodeStop,
		Text: "Stop", Name: "Stop", Role: "button",
		Events: []v1.EventKind{v1.EventActivate}}
	if snap.Mode == Recording || snap.Mode == Adopted {
		stop.Tone = v1.ToneError
	}
	transport := []*v1.Node{
		{Kind: v1.KindButton, ID: nodeRecord, Key: nodeRecord,
			Text: "Record", Name: "Record", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
		stop,
	}
	if cfg.ReplayEnabled {
		transport = append(transport,
			&v1.Node{Kind: v1.KindButton, ID: nodeReplay, Key: nodeReplay,
				Text: "Start replay", Name: "Start replay", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			&v1.Node{Kind: v1.KindButton, ID: nodeSave, Key: nodeSave,
				Text: "Save replay", Name: "Save replay", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		)
	}
	children = append(children, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: transport})
	if snap.Artifact != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: snap.Artifact, Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: children}
}

func FormatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d / time.Second)
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

func presentation(snap Snapshot) (string, v1.Tone) {
	switch snap.Mode {
	case Unavailable:
		return "Recorder unavailable", v1.ToneError
	case Recording, Adopted:
		return "Recording", v1.ToneError
	case ReplayActive:
		return "Replay", v1.ToneAccent
	case Stopping:
		return "Stopping", v1.ToneNormal
	case Failed:
		return "Recorder failed", v1.ToneError
	default:
		return "Idle", v1.ToneNormal
	}
}

// HandleInput routes one input event against the recorder mode. The bar
// control toggles: a live recording stops, an idle one starts. The panel
// keeps explicit record, stop, replay, and save controls; a secondary click
// anywhere opens the panel.
func HandleInput(ev *v1.InputEvent, mode Mode) (open, record, stop, replay, save bool) {
	if ev == nil {
		return false, false, false, false, false
	}
	if ev.Event == v1.EventPointer && ev.Button == v1.ButtonSecondary {
		return true, false, false, false, false
	}
	if ev.Event != v1.EventActivate {
		return false, false, false, false, false
	}
	switch ev.Node {
	case nodeToggle:
		switch mode {
		case Recording, Adopted, Stopping:
			return false, false, true, false, false
		case Idle:
			return false, true, false, false, false
		}
		return false, false, false, false, false
	case nodeRecord:
		if mode == Idle {
			return false, true, false, false, false
		}
	case nodeStop:
		switch mode {
		case Recording, Adopted, Stopping:
			return false, false, true, false, false
		}
	case nodeReplay:
		if mode == Idle {
			return false, false, false, true, false
		}
	case nodeSave:
		if mode == ReplayActive {
			return false, false, false, false, true
		}
	}
	return false, false, false, false, false
}

func cameraPresentation(snap Snapshot) (string, v1.Tone) {
	switch snap.Mode {
	case Unavailable, Failed:
		return "camera-off", v1.ToneError
	case Recording, Adopted:
		return "camera", v1.ToneError
	case ReplayActive:
		return "replay", v1.ToneNormal
	default:
		return "camera", v1.ToneNormal
	}
}
