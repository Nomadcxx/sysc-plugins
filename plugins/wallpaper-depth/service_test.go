package wallpaperdepth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type fakeRunner struct {
	resp   string
	err    error
	called [][]string
}

func (f *fakeRunner) Run(ctx context.Context, args ...string) (json.RawMessage, error) {
	f.called = append(f.called, args)
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(f.resp), nil
}

func TestSessionCheckParsesStatus(t *testing.T) {
	r := &fakeRunner{resp: `{"ready":true,"runtimeReady":true,"modelReady":true,"modelSize":99060839}`}
	s := NewSession(r)
	s.Check(context.Background())
	status, errMsg, busy, _ := s.Snapshot()
	if !status.Ready || !status.ModelReady {
		t.Fatalf("status = %+v", status)
	}
	if errMsg != "" || busy {
		t.Fatalf("errMsg=%q busy=%v", errMsg, busy)
	}
	if len(r.called) != 1 || r.called[0][0] != "status" {
		t.Fatalf("calls = %v", r.called)
	}
}

func TestSessionGenerateBuildsArgsAndReportsMask(t *testing.T) {
	r := &fakeRunner{resp: `{"ready":true,"maskPath":"/tmp/mask.png","cacheHit":false,"elapsedMs":42}`}
	s := NewSession(r)
	s.Generate(context.Background(), "/home/x/wall.jpg", 30, 8)
	status, errMsg, _, lastMask := s.Snapshot()
	if errMsg != "" {
		t.Fatalf("errMsg = %q", errMsg)
	}
	if lastMask != "/tmp/mask.png" {
		t.Fatalf("lastMask = %q", lastMask)
	}
	if !status.Ready {
		t.Fatal("status not updated")
	}
	args := r.called[0]
	if args[0] != "generate" || args[1] != "--wallpaper" || args[2] != "/home/x/wall.jpg" {
		t.Fatalf("args = %v", args)
	}
	if args[3] != "--threshold" || args[4] != "0.30" || args[5] != "--feather" || args[6] != "0.16" {
		t.Fatalf("numeric args = %v", args)
	}
}

func TestSessionGenerateRequiresWallpaper(t *testing.T) {
	r := &fakeRunner{}
	s := NewSession(r)
	s.Generate(context.Background(), "", 30, 8)
	if len(r.called) != 0 {
		t.Fatal("helper ran without a wallpaper")
	}
	_, errMsg, _, _ := s.Snapshot()
	if errMsg == "" {
		t.Fatal("no error recorded for missing wallpaper")
	}
}

func TestSessionBusyIsSerialized(t *testing.T) {
	r := &fakeRunner{err: errors.New("slow")}
	s := NewSession(r)
	s.Check(context.Background())
	// A failing run is not busy anymore; a second call still runs once at a
	// time. This asserts the busy flag clears after each operation.
	s.Check(context.Background())
	if len(r.called) != 2 {
		t.Fatalf("calls = %d", len(r.called))
	}
}

func TestSessionErrorSurfaces(t *testing.T) {
	r := &fakeRunner{err: errors.New("python3: module not found")}
	s := NewSession(r)
	s.Check(context.Background())
	_, errMsg, _, _ := s.Snapshot()
	if errMsg == "" {
		t.Fatal("error not surfaced")
	}
}

func TestTreesValidate(t *testing.T) {
	status := HelperStatus{Ready: true, RuntimeReady: true, ModelReady: true}
	if err := v1.Validate(BarTree(true), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree(false), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	panel := PanelTree(status, "", false, "/tmp/mask.png", "/home/x/wall.jpg", 30, 8)
	if err := v1.Validate(panel, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree(HelperStatus{}, "boom", true, "", "", 30, 8), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
}
