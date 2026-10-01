package screenshot

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// CloseDelay gives the compositor time to unmap the panel before the shell
// reads the screen. The live gate re-measures it.
const CloseDelay = 150 * time.Millisecond

// Caller is the host-call surface; *v1.Client satisfies it.
type Caller interface {
	Call(ctx context.Context, kind v1.CallKind, params any) (v1.HostReply, error)
}

// Launcher turns panel events into host calls and owns the panel's model.
type Launcher struct {
	c       Caller
	changed func()

	// Sleep and OpenFolder are seams for tests.
	Sleep      func(time.Duration)
	OpenFolder func(dir string) error

	mu    sync.Mutex
	model Model
}

// NewLauncher returns a launcher. changed is called, from the calling
// goroutine, whenever the model changed and the views should repaint.
func NewLauncher(c Caller, changed func()) *Launcher {
	return &Launcher{c: c, changed: changed, Sleep: time.Sleep, OpenFolder: openFolder}
}

// Model returns a copy of what the panel should show.
func (l *Launcher) Model() Model {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.model
}

func (l *Launcher) set(f func(*Model)) {
	l.mu.Lock()
	f(&l.model)
	l.mu.Unlock()
	l.changed()
}

// Refresh reads the save directory. A failure leaves none, so no caption is
// drawn rather than a stale one.
func (l *Launcher) Refresh(ctx context.Context) {
	dir := ""
	if reply, err := l.c.Call(ctx, v1.CallScreenshotDirectory, nil); err == nil && reply.OK {
		var r v1.ScreenshotDirectoryResult
		if json.Unmarshal(reply.Result, &r) == nil {
			dir = r.Directory
		}
	}
	l.set(func(m *Model) { m.Directory = dir })
}

// Act handles one activated node. It blocks on host calls, so callers run it
// in its own goroutine.
func (l *Launcher) Act(ctx context.Context, node, output string, generation uint32, instance string) {
	reopen := v1.PanelParams{Entry: "panel", Output: output, Generation: generation, Instance: instance}
	switch node {
	case NodeOpen:
		_, _ = l.c.Call(ctx, v1.CallPanelOpen, reopen)
	case NodeClose:
		_, _ = l.c.Call(ctx, v1.CallPanelClose, v1.PanelParams{Entry: "panel"})
	case NodeRegion, NodeWindow, NodeScreen:
		l.capture(ctx, node, reopen)
	case NodeFolder:
		l.folder(ctx)
	}
}

func (l *Launcher) capture(ctx context.Context, mode string, reopen v1.PanelParams) {
	l.set(func(m *Model) { m.Error = "" })
	_, _ = l.c.Call(ctx, v1.CallPanelClose, v1.PanelParams{Entry: "panel"})
	l.Sleep(CloseDelay)
	reply, err := l.c.Call(ctx, v1.CallScreenshotStart, v1.ScreenshotStartParams{Mode: mode})
	msg := ""
	switch {
	case err != nil:
		msg = err.Error()
	case !reply.OK:
		msg = reply.Error
	}
	if msg == "" {
		return
	}
	l.set(func(m *Model) { m.Error = msg })
	_, _ = l.c.Call(ctx, v1.CallPanelOpen, reopen)
}

func (l *Launcher) folder(ctx context.Context) {
	dir := l.Model().Directory
	if dir == "" {
		l.set(func(m *Model) { m.Error = "The save folder is unknown" })
		return
	}
	if err := l.OpenFolder(dir); err != nil {
		l.set(func(m *Model) { m.Error = "Could not open the folder" })
		return
	}
	_, _ = l.c.Call(ctx, v1.CallPanelClose, v1.PanelParams{Entry: "panel"})
}

// openFolder creates the directory first: a fresh install has saved nothing
// yet, and opening a missing folder would fail for no reason.
func openFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("xdg-open", dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
