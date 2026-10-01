package screenshot

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type fakeCall struct {
	kind   v1.CallKind
	params any
}

type fakeHost struct {
	mu        sync.Mutex
	calls     []fakeCall
	directory string
	startErr  string
	dirErr    bool
}

func (f *fakeHost) Call(_ context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{kind, params})
	switch kind {
	case v1.CallScreenshotStart:
		if f.startErr != "" {
			return v1.HostReply{OK: false, Error: f.startErr}, nil
		}
	case v1.CallScreenshotDirectory:
		if f.dirErr {
			return v1.HostReply{OK: false, Error: "no directory"}, nil
		}
		raw, _ := json.Marshal(v1.ScreenshotDirectoryResult{Directory: f.directory})
		return v1.HostReply{OK: true, Result: raw}, nil
	}
	return v1.HostReply{OK: true}, nil
}

func (f *fakeHost) kinds() []v1.CallKind {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []v1.CallKind
	for _, c := range f.calls {
		out = append(out, c.kind)
	}
	return out
}

func newTestLauncher(f *fakeHost) (*Launcher, *[]time.Duration, *int) {
	var slept []time.Duration
	changed := 0
	l := NewLauncher(f, func() { changed++ })
	l.Sleep = func(d time.Duration) { slept = append(slept, d) }
	return l, &slept, &changed
}

func TestModeRowClosesThenWaitsThenStarts(t *testing.T) {
	for _, tc := range []struct{ node, mode string }{
		{NodeRegion, "region"}, {NodeWindow, "window"}, {NodeScreen, "screen"},
	} {
		f := &fakeHost{}
		l, slept, _ := newTestLauncher(f)
		l.Act(context.Background(), tc.node, "eDP-1", 3, "bar-1")
		want := []v1.CallKind{v1.CallPanelClose, v1.CallScreenshotStart}
		if got := f.kinds(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: calls = %v, want %v", tc.node, got, want)
		}
		if !reflect.DeepEqual(*slept, []time.Duration{CloseDelay}) {
			t.Fatalf("%s: slept %v, want one %v wait between close and start", tc.node, *slept, CloseDelay)
		}
		if p, ok := f.calls[1].params.(v1.ScreenshotStartParams); !ok || p.Mode != tc.mode {
			t.Fatalf("%s: start params = %#v", tc.node, f.calls[1].params)
		}
	}
}

func TestRefusedStartReopensThePanelWithTheMessage(t *testing.T) {
	f := &fakeHost{startErr: "a region selector is already open"}
	l, _, changed := newTestLauncher(f)
	l.Act(context.Background(), NodeRegion, "eDP-1", 3, "bar-1")
	want := []v1.CallKind{v1.CallPanelClose, v1.CallScreenshotStart, v1.CallPanelOpen}
	if got := f.kinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	open, ok := f.calls[2].params.(v1.PanelParams)
	if !ok || open.Entry != "panel" || open.Output != "eDP-1" || open.Generation != 3 || open.Instance != "bar-1" {
		t.Fatalf("reopen params = %#v", f.calls[2].params)
	}
	if got := l.Model().Error; got != "a region selector is already open" {
		t.Fatalf("model error = %q", got)
	}
	if *changed == 0 {
		t.Fatal("the view was never told to repaint")
	}
	f.startErr = ""
	l.Act(context.Background(), NodeWindow, "eDP-1", 3, "bar-1")
	if got := l.Model().Error; got != "" {
		t.Fatalf("the error survived a new attempt: %q", got)
	}
}

func TestRefreshReadsTheDirectoryAndSurvivesFailure(t *testing.T) {
	f := &fakeHost{directory: "/home/u/Pictures/Screenshots"}
	l, _, changed := newTestLauncher(f)
	l.Refresh(context.Background())
	if got := l.Model().Directory; got != "/home/u/Pictures/Screenshots" {
		t.Fatalf("directory = %q", got)
	}
	if *changed != 1 {
		t.Fatalf("changed %d times, want 1", *changed)
	}
	f.dirErr = true
	l.Refresh(context.Background())
	if got := l.Model().Directory; got != "" {
		t.Fatalf("a failed read left directory %q, want none so no caption is drawn", got)
	}
}

func TestOpenFolderUsesTheReportedDirectory(t *testing.T) {
	f := &fakeHost{directory: "/home/u/Pictures/Screenshots"}
	l, _, _ := newTestLauncher(f)
	var opened []string
	l.OpenFolder = func(dir string) error { opened = append(opened, dir); return nil }
	l.Refresh(context.Background())
	l.Act(context.Background(), NodeFolder, "eDP-1", 1, "")
	if !reflect.DeepEqual(opened, []string{"/home/u/Pictures/Screenshots"}) {
		t.Fatalf("opened %v", opened)
	}
	if got := f.kinds(); got[len(got)-1] != v1.CallPanelClose {
		t.Fatalf("calls = %v, want the panel closed after a successful open", got)
	}
}

func TestOpenFolderWithNoDirectoryOrAFailingOpenShowsAnErrorAndStays(t *testing.T) {
	f := &fakeHost{dirErr: true}
	l, _, _ := newTestLauncher(f)
	l.OpenFolder = func(string) error { t.Fatal("opened an empty directory"); return nil }
	l.Refresh(context.Background())
	l.Act(context.Background(), NodeFolder, "eDP-1", 1, "")
	if l.Model().Error == "" {
		t.Fatal("no error shown for a missing directory")
	}
	for _, k := range f.kinds() {
		if k == v1.CallPanelClose {
			t.Fatal("the panel closed on a failed folder open")
		}
	}

	g := &fakeHost{directory: "/d"}
	l2, _, _ := newTestLauncher(g)
	l2.OpenFolder = func(string) error { return errors.New("no handler") }
	l2.Refresh(context.Background())
	l2.Act(context.Background(), NodeFolder, "eDP-1", 1, "")
	if l2.Model().Error == "" {
		t.Fatal("no error shown for a failing open")
	}
}

func TestOpenAndCloseNodesToggleThePanel(t *testing.T) {
	f := &fakeHost{}
	l, _, _ := newTestLauncher(f)
	l.Act(context.Background(), NodeOpen, "eDP-1", 2, "bar-1")
	l.Act(context.Background(), NodeClose, "eDP-1", 2, "bar-1")
	if got, want := f.kinds(), []v1.CallKind{v1.CallPanelOpen, v1.CallPanelClose}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
