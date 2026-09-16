package recorder

import (
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestPanelTreeIdle(t *testing.T) {
	t.Parallel()
	root := PanelTree(Snapshot{Mode: Idle}, Config{}, time.Time{})
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	body := flatten(root)
	if !strings.Contains(body, "Screen Recorder") || !strings.Contains(body, "Record") {
		t.Fatalf("panel = %q", body)
	}
	if strings.Contains(body, "Start replay") {
		t.Fatal("replay controls shown while disabled")
	}
	rec := childByID(root, nodeRecord)
	if rec == nil || rec.Text != "Record" {
		t.Fatalf("idle Record = %#v", rec)
	}
	if rec.Tone == v1.ToneError {
		t.Fatal("idle panel Record must not use error tone")
	}
}

func TestPanelTreeReplayControls(t *testing.T) {
	t.Parallel()
	root := PanelTree(Snapshot{Mode: Idle}, Config{ReplayEnabled: true}, time.Time{})
	if !strings.Contains(flatten(root), "Start replay") {
		t.Fatal("missing replay start")
	}
}

func TestPanelTreeElapsed(t *testing.T) {
	t.Parallel()
	root := PanelTree(Snapshot{Mode: Recording, Elapsed: 72 * time.Second}, Config{}, time.Time{})
	if !strings.Contains(flatten(root), "01:12") {
		t.Fatalf("elapsed missing: %q", flatten(root))
	}
	var recElapsed, replayElapsed *v1.Node
	walk(root, func(n *v1.Node) {
		if n.Key == "elapsed" {
			recElapsed = n
		}
	})
	if recElapsed == nil || recElapsed.Tone != v1.ToneError {
		t.Fatalf("elapsed = %+v, want error while recording", recElapsed)
	}
	walk(PanelTree(Snapshot{Mode: ReplayActive, Elapsed: time.Second}, Config{}, time.Time{}), func(n *v1.Node) {
		if n.Key == "elapsed" {
			replayElapsed = n
		}
	})
	if replayElapsed == nil || replayElapsed.Tone != v1.ToneAccent {
		t.Fatalf("replay elapsed = %+v, want accent", replayElapsed)
	}
}

func TestPanelTreeStopIsDestructiveWhileRecording(t *testing.T) {
	t.Parallel()
	live := childByID(PanelTree(Snapshot{Mode: Recording}, Config{}, time.Time{}), nodeStop)
	if live == nil || live.Tone != v1.ToneError {
		t.Fatalf("live stop = %#v, want the destructive chip", live)
	}
	idle := childByID(PanelTree(Snapshot{Mode: Idle}, Config{}, time.Time{}), nodeStop)
	if idle == nil || idle.Tone == v1.ToneError {
		t.Fatalf("idle stop = %#v, want no destructive chrome", idle)
	}
}

func TestBarTreeStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode     Mode
		icon     string
		tone     v1.Tone
		wantText string
		save     bool
	}{
		{Idle, "record", v1.ToneNormal, "", false},
		{Recording, "stop", v1.ToneError, "00:00", false},
		{Adopted, "stop", v1.ToneError, "00:00", false},
		{Stopping, "stop", v1.ToneNormal, "", false},
		{ReplayActive, "record", v1.ToneNormal, "", true},
		{Unavailable, "camera-off", v1.ToneError, "", false},
		{Failed, "camera-off", v1.ToneError, "", false},
	}
	for _, tc := range cases {
		root := BarTree(Snapshot{Mode: tc.mode}, Config{})
		if err := v1.Validate(root, v1.ViewBar); err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		if root.Kind != v1.KindRow {
			t.Fatalf("%s root = kind %q", tc.mode, root.Kind)
		}
		toggle := childByID(root, nodeToggle)
		if toggle == nil {
			t.Fatalf("%s missing toggle", tc.mode)
		}
		if toggle.Icon != tc.icon {
			t.Fatalf("%s toggle icon = %q, want %q", tc.mode, toggle.Icon, tc.icon)
		}
		if toggle.Tone != tc.tone {
			t.Fatalf("%s toggle tone = %q, want %q", tc.mode, toggle.Tone, tc.tone)
		}
		if toggle.Text != tc.wantText {
			t.Fatalf("%s toggle text = %q, want %q", tc.mode, toggle.Text, tc.wantText)
		}
		if toggle.Name != "Toggle recording" {
			t.Fatalf("%s toggle name = %q", tc.mode, toggle.Name)
		}
		save := childByID(root, nodeSave)
		if tc.save && save == nil {
			t.Fatalf("%s missing save control", tc.mode)
		}
		if !tc.save && save != nil {
			t.Fatalf("%s has save control: %+v", tc.mode, save)
		}
	}
}

func TestBarTreeCarriesElapsedWhileRecording(t *testing.T) {
	t.Parallel()
	root := BarTree(Snapshot{Mode: Recording, Elapsed: 72 * time.Second}, Config{})
	toggle := childByID(root, nodeToggle)
	if toggle.Text != "01:12" || !toggle.Tabular {
		t.Fatalf("toggle = %+v", toggle)
	}
}

func TestBarTreeHidesWhenIdleAndConfigured(t *testing.T) {
	t.Parallel()
	hidden := BarTree(Snapshot{Mode: Idle}, Config{HideInactive: true})
	if len(hidden.Children) != 0 {
		t.Fatalf("hide_inactive idle children = %d, want none", len(hidden.Children))
	}
	if err := v1.Validate(hidden, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	shown := BarTree(Snapshot{Mode: Recording}, Config{HideInactive: true})
	if childByID(shown, nodeToggle) == nil {
		t.Fatalf("hide_inactive hid the control while recording: %q", flatten(shown))
	}
}

func TestTooltipTreeIncludesFailureLog(t *testing.T) {
	t.Parallel()
	root := TooltipTree(Snapshot{Mode: Failed, Err: "zero-byte artifact", Logs: "gpu: encoder failed"})
	if err := v1.Validate(root, v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	body := flatten(root)
	if !strings.Contains(body, "failed") || !strings.Contains(body, "encoder failed") {
		t.Fatalf("tooltip = %q", body)
	}
}

func TestHandleInputButtons(t *testing.T) {
	t.Parallel()
	open, record, stop, replay, save := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventActivate}, Idle)
	if open || !record || stop || replay || save {
		t.Fatalf("toggle idle = %v %v %v %v %v", open, record, stop, replay, save)
	}

	for _, mode := range []Mode{Recording, Adopted, Stopping} {
		open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventActivate}, mode)
		if open || record || !stop || replay || save {
			t.Fatalf("toggle live in %s = %v %v %v %v %v", mode, open, record, stop, replay, save)
		}
	}
	for _, mode := range []Mode{Unavailable, Failed, ReplayActive} {
		_, record, _, _, _ = HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventActivate}, mode)
		if record {
			t.Fatalf("toggle recorded in %s", mode)
		}
	}

	// The panel keeps explicit controls.
	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeRecord, Event: v1.EventActivate}, Idle)
	if open || !record || stop || replay || save {
		t.Fatalf("record idle = %v %v %v %v %v", open, record, stop, replay, save)
	}
	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeRecord, Event: v1.EventActivate}, Recording)
	if open || record || stop || replay || save {
		t.Fatalf("record while recording = %v %v %v %v %v", open, record, stop, replay, save)
	}
	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeStop, Event: v1.EventActivate}, Recording)
	if open || record || !stop || replay || save {
		t.Fatalf("stop recording = %v %v %v %v %v", open, record, stop, replay, save)
	}
	for _, mode := range []Mode{Idle, Unavailable, Failed, ReplayActive} {
		_, _, stop, _, _ = HandleInput(&v1.InputEvent{Node: nodeStop, Event: v1.EventActivate}, mode)
		if stop {
			t.Fatalf("stop live in %s", mode)
		}
	}

	for _, mode := range []Mode{Idle, Unavailable, Failed, Recording} {
		open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventPointer, Button: v1.ButtonSecondary}, mode)
		if !open || record || stop || replay || save {
			t.Fatalf("toggle secondary in %s = %v %v %v %v %v", mode, open, record, stop, replay, save)
		}
	}
	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventPointer, Button: v1.ButtonMiddle}, Idle)
	if open || record || stop || replay || save {
		t.Fatalf("middle = %v %v %v %v %v", open, record, stop, replay, save)
	}
}

func TestPanelTreeShowsFailureReason(t *testing.T) {
	t.Parallel()
	root := PanelTree(Snapshot{Mode: Failed, Err: "gpu-screen-recorder: process exited"}, Config{}, time.Time{})
	body := flatten(root)
	if !strings.Contains(body, "Recorder failed") || !strings.Contains(body, "process exited") {
		t.Fatalf("panel = %q", body)
	}
	unavail := PanelTree(Snapshot{Mode: Unavailable, Err: "gpu-screen-recorder is not installed or not on PATH"}, Config{}, time.Time{})
	if !strings.Contains(flatten(unavail), "not installed") {
		t.Fatalf("unavailable panel = %q", flatten(unavail))
	}
}

func flatten(n *v1.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(n.Text)
	for _, c := range n.Children {
		b.WriteString(flatten(c))
	}
	return b.String()
}

func childByID(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if found := childByID(c, id); found != nil {
			return found
		}
	}
	return nil
}

func walk(n *v1.Node, fn func(*v1.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		walk(c, fn)
	}
}
