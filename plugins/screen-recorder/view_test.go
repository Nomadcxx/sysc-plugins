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
}

func TestBarTreeStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode     Mode
		icon     string
		tone     v1.Tone
		wantText string
	}{
		{Idle, "camera", v1.ToneNormal, "RecordStop"},
		{Recording, "camera", v1.ToneError, "RecordStop"},
		{Adopted, "camera", v1.ToneError, "RecordStop"},
		{Stopping, "camera", v1.ToneNormal, "RecordStop"},
		{ReplayActive, "replay", v1.ToneNormal, "RecordStop"},
		{Unavailable, "camera-off", v1.ToneError, "RecordStop"},
		{Failed, "camera-off", v1.ToneError, "RecordStop"},
	}
	for _, tc := range cases {
		root := BarTree(Snapshot{Mode: tc.mode}, Config{})
		if err := v1.Validate(root, v1.ViewBar); err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		if root.Kind != v1.KindRow || root.Gap != 8 {
			t.Fatalf("%s root = kind %q gap %d", tc.mode, root.Kind, root.Gap)
		}
		cam := childByID(root, nodeCamera)
		if cam == nil {
			t.Fatalf("%s missing camera", tc.mode)
		}
		if cam.Icon != tc.icon {
			t.Fatalf("%s camera icon = %q, want %q", tc.mode, cam.Icon, tc.icon)
		}
		if cam.Tone != tc.tone {
			t.Fatalf("%s camera tone = %q, want %q", tc.mode, cam.Tone, tc.tone)
		}
		if cam.Name != "Open screen recorder" {
			t.Fatalf("%s camera name = %q", tc.mode, cam.Name)
		}
		rec := childByID(root, nodeRecord)
		if rec == nil || rec.Text != "Record" || rec.Name != "Record" || rec.Tone != v1.ToneError {
			t.Fatalf("%s record button missing or wrong: %#v", tc.mode, rec)
		}
		stop := childByID(root, nodeStop)
		if stop == nil || stop.Text != "Stop" || stop.Name != "Stop" {
			t.Fatalf("%s stop button missing or wrong: %#v", tc.mode, stop)
		}
		if flatten(root) != tc.wantText {
			t.Fatalf("%s flatten = %q, want %q", tc.mode, flatten(root), tc.wantText)
		}
	}
}

func TestBarTreeHidesWhenIdleAndConfigured(t *testing.T) {
	t.Parallel()
	root := BarTree(Snapshot{Mode: Idle}, Config{HideInactive: true})
	if err := v1.Validate(root, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if childByID(root, nodeCamera) == nil {
		t.Fatal("hide_inactive idle omitted camera")
	}
	if childByID(root, nodeRecord) != nil || childByID(root, nodeStop) != nil {
		t.Fatalf("hide_inactive idle still has record/stop: %q", flatten(root))
	}
	shown := BarTree(Snapshot{Mode: Recording}, Config{HideInactive: true})
	if childByID(shown, nodeRecord) == nil || childByID(shown, nodeStop) == nil {
		t.Fatalf("hide_inactive hid transport while recording: %q", flatten(shown))
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
	open, record, stop, replay, save := HandleInput(&v1.InputEvent{Node: nodeCamera, Event: v1.EventActivate}, Idle)
	if !open || record || stop || replay || save {
		t.Fatalf("camera activate = %v %v %v %v %v", open, record, stop, replay, save)
	}

	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeRecord, Event: v1.EventActivate}, Idle)
	if open || !record || stop || replay || save {
		t.Fatalf("record idle = %v %v %v %v %v", open, record, stop, replay, save)
	}
	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeRecord, Event: v1.EventActivate}, Recording)
	if open || record || stop || replay || save {
		t.Fatalf("record while recording = %v %v %v %v %v", open, record, stop, replay, save)
	}
	for _, mode := range []Mode{Unavailable, Failed, Stopping, Adopted, ReplayActive} {
		_, record, _, _, _ = HandleInput(&v1.InputEvent{Node: nodeRecord, Event: v1.EventActivate}, mode)
		if record {
			t.Fatalf("record live in %s", mode)
		}
	}

	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeStop, Event: v1.EventActivate}, Recording)
	if open || record || !stop || replay || save {
		t.Fatalf("stop recording = %v %v %v %v %v", open, record, stop, replay, save)
	}
	for _, mode := range []Mode{Adopted, Stopping} {
		_, _, stop, _, _ = HandleInput(&v1.InputEvent{Node: nodeStop, Event: v1.EventActivate}, mode)
		if !stop {
			t.Fatalf("stop inert in %s", mode)
		}
	}
	for _, mode := range []Mode{Idle, Unavailable, Failed, ReplayActive} {
		_, _, stop, _, _ = HandleInput(&v1.InputEvent{Node: nodeStop, Event: v1.EventActivate}, mode)
		if stop {
			t.Fatalf("stop live in %s", mode)
		}
	}

	for _, mode := range []Mode{Idle, Unavailable, Failed, Recording} {
		open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeCamera, Event: v1.EventPointer, Button: v1.ButtonSecondary}, mode)
		if !open || record || stop || replay || save {
			t.Fatalf("camera secondary in %s = %v %v %v %v %v", mode, open, record, stop, replay, save)
		}
		open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeRecord, Event: v1.EventPointer, Button: v1.ButtonSecondary}, mode)
		if !open || record || stop || replay || save {
			t.Fatalf("record secondary in %s = %v %v %v %v %v", mode, open, record, stop, replay, save)
		}
	}
	open, record, stop, replay, save = HandleInput(&v1.InputEvent{Node: nodeRecord, Event: v1.EventPointer, Button: v1.ButtonMiddle}, Idle)
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
