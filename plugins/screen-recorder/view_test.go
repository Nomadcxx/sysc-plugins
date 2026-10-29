package recorder

import (
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
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
		stopName string
	}{
		{Idle, "camera", v1.ToneNormal, "Stop"},
		{Recording, "camera", v1.ToneError, "Stop"},
		{Adopted, "camera", v1.ToneError, "Stop"},
		{Stopping, "camera", v1.ToneNormal, "Stop"},
		{ReplayActive, "replay", v1.ToneNormal, "Stop replay"},
		{Unavailable, "camera-off", v1.ToneError, "Stop"},
		{Failed, "camera-off", v1.ToneError, "Stop"},
	}
	for _, tc := range cases {
		root := BarTree(Snapshot{Mode: tc.mode}, Config{})
		if err := v1.Validate(root, v1.ViewBar); err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		if root.Kind != v1.KindRow {
			t.Fatalf("%s root = kind %q", tc.mode, root.Kind)
		}
		camera := childByID(root, "camera")
		if camera == nil {
			t.Fatalf("%s missing camera control", tc.mode)
		}
		if camera.Icon != tc.icon {
			t.Fatalf("%s camera icon = %q, want %q", tc.mode, camera.Icon, tc.icon)
		}
		if camera.Tone != tc.tone {
			t.Fatalf("%s camera tone = %q, want %q", tc.mode, camera.Tone, tc.tone)
		}
		if camera.Name != "Open screen recorder" || camera.Role != "button" || len(camera.Events) != 1 || camera.Events[0] != v1.EventActivate {
			t.Fatalf("%s camera interaction = %+v", tc.mode, camera)
		}
		record := childByID(root, nodeRecord)
		if record == nil || record.Text != "Record" || record.Name != "Record" {
			t.Fatalf("%s Record = %+v", tc.mode, record)
		}
		stop := childByID(root, nodeStop)
		if stop == nil || stop.Text != "Stop" || stop.Name != tc.stopName {
			t.Fatalf("%s Stop = %+v", tc.mode, stop)
		}
		if childByID(root, nodeSave) != nil {
			t.Fatalf("%s exposes replay save on the bar", tc.mode)
		}
	}
}

func TestBarTreeFitsSideWidths(t *testing.T) {
	for _, state := range []Snapshot{
		{Mode: Idle}, {Mode: Recording, Elapsed: 72 * time.Second}, {Mode: ReplayActive}, {Mode: Failed},
	} {
		for _, width := range []int{shelllint.BarWidth, 28, 32, 64, 120} {
			bar := BarTreeAtWidth(state, Config{}, width)
			for _, finding := range shelllint.Tree(bar, v1.ViewBar, width, shelllint.BarHeight) {
				t.Errorf("mode %s width %d: %s", state.Mode, width, finding)
			}
			camera := childByID(bar, "camera")
			if camera == nil {
				t.Fatalf("mode %s width %d lost camera", state.Mode, width)
			}
			if width < 120 {
				if childByID(bar, nodeRecord) != nil || childByID(bar, nodeStop) != nil {
					t.Fatalf("mode %s width %d shows actions beside the camera", state.Mode, width)
				}
			} else if childByID(bar, nodeRecord) == nil || childByID(bar, nodeStop) == nil {
				t.Fatalf("mode %s width %d missing transport controls", state.Mode, width)
			}
			if childByID(bar, nodeSave) != nil {
				t.Fatalf("mode %s width %d exposes save on the bar", state.Mode, width)
			}
		}
	}
	panelSave := childByID(PanelTree(Snapshot{Mode: ReplayActive}, Config{ReplayEnabled: true}, time.Time{}), nodeSave)
	if panelSave == nil || panelSave.Name != "Save replay" {
		t.Fatalf("replay save missing from panel: %+v", panelSave)
	}
}

func TestBarTreeCarriesElapsedWhileRecording(t *testing.T) {
	t.Parallel()
	root := BarTree(Snapshot{Mode: Recording, Elapsed: 72 * time.Second}, Config{})
	if strings.Contains(flatten(root), "01:12") {
		t.Fatalf("elapsed leaked onto the bar: %q", flatten(root))
	}
	if !strings.Contains(flatten(PanelTree(Snapshot{Mode: Recording, Elapsed: 72 * time.Second}, Config{}, time.Time{})), "01:12") {
		t.Fatal("panel lost elapsed time")
	}
}

func TestBarTreeHidesWhenIdleAndConfigured(t *testing.T) {
	t.Parallel()
	hidden := BarTree(Snapshot{Mode: Idle}, Config{HideInactive: true})
	if childByID(hidden, "camera") == nil || childByID(hidden, nodeRecord) != nil || childByID(hidden, nodeStop) != nil {
		t.Fatalf("hide_inactive idle tree = %q", flatten(hidden))
	}
	if err := v1.Validate(hidden, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	shown := BarTree(Snapshot{Mode: Recording}, Config{HideInactive: true})
	if childByID(shown, "camera") == nil || childByID(shown, nodeRecord) == nil || childByID(shown, nodeStop) == nil {
		t.Fatalf("hide_inactive hid live controls: %q", flatten(shown))
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
	assert := func(node string, mode Mode, wantOpen, wantRecord, wantStop, wantReplay, wantSave bool) {
		t.Helper()
		open, record, stop, replay, save := HandleInput(&v1.InputEvent{Node: node, Event: v1.EventActivate}, mode)
		if open != wantOpen || record != wantRecord || stop != wantStop || replay != wantReplay || save != wantSave {
			t.Fatalf("%s in %s = %v %v %v %v %v", node, mode, open, record, stop, replay, save)
		}
	}

	for _, mode := range []Mode{Idle, Recording, Adopted, Stopping, ReplayActive, Unavailable, Failed} {
		assert("camera", mode, true, false, false, false, false)
	}
	assert(nodeRecord, Idle, false, true, false, false, false)
	assert(nodeRecord, Recording, false, false, false, false, false)
	assert(nodeRecord, Failed, false, true, false, false, false)
	assert(nodeRecord, Unavailable, false, false, false, false, false)
	for _, mode := range []Mode{Recording, Adopted, Stopping} {
		assert(nodeStop, mode, false, false, true, false, false)
	}
	assert(nodeStop, Idle, false, false, false, false, false)
	assert(nodeStop, Unavailable, false, false, false, false, false)
	assert(nodeStop, Failed, false, false, false, false, false)
	assert(nodeStop, ReplayActive, false, false, false, true, false)
	assert(nodeReplay, Failed, false, false, false, true, false)
	assert(nodeSave, ReplayActive, false, false, false, false, true)

	for _, button := range []v1.PointerButton{v1.ButtonSecondary, v1.ButtonMiddle} {
		open, record, stop, replay, save := HandleInput(&v1.InputEvent{Node: "camera", Event: v1.EventPointer, Button: button}, Idle)
		if open || record || stop || replay || save {
			t.Fatalf("pointer button %v = %v %v %v %v %v", button, open, record, stop, replay, save)
		}
	}
}

// A replay that cannot be stopped keeps capturing the screen, so the bar
// Stop control and panel controls have to reach ToggleReplay while one runs.
func TestRunningReplayOffersAStopControl(t *testing.T) {
	t.Parallel()
	for _, node := range []string{nodeReplay, nodeStop} {
		open, record, stop, replay, save := HandleInput(&v1.InputEvent{Node: node, Event: v1.EventActivate}, ReplayActive)
		if open || record || stop || !replay || save {
			t.Fatalf("%s during replay = %v %v %v %v %v", node, open, record, stop, replay, save)
		}
	}
	running := childByID(PanelTree(Snapshot{Mode: ReplayActive}, Config{ReplayEnabled: true}, time.Time{}), nodeReplay)
	if running == nil || running.Text != "Stop replay" {
		t.Fatalf("running replay button = %+v", running)
	}
	idle := childByID(PanelTree(Snapshot{Mode: Idle}, Config{ReplayEnabled: true}, time.Time{}), nodeReplay)
	if idle == nil || idle.Text != "Start replay" {
		t.Fatalf("idle replay button = %+v", idle)
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
