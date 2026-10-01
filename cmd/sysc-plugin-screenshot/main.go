// Command sysc-plugin-screenshot is the Screenshot plugin: a bar camera and a
// small panel that start the shell's own capture engine through host calls.
// The plugin captures nothing itself; the shell owns the selector, the frozen
// frame, the clipboard hand-off and the result toast.
package main

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/screenshot"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

// lockedWriter serialises whole messages: the encoder writes one message per
// Write, and host calls go out from their own goroutines so a reply the event
// loop is not waiting on can never stall it.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// callTimeout bounds one host call. A capture start acknowledges at once, but
// the close-and-wait before it is part of the same action.
const callTimeout = 10 * time.Second

type view struct {
	kind     v1.ViewKind
	rev      uint64
	instance string
}

func run(in io.Reader, out io.Writer) error {
	c := v1.NewClient(in, &lockedWriter{w: out})
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.screenshot", Name: "Screenshot", Version: "1.0.0"})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repaint := make(chan struct{}, 1)
	launcher := screenshot.NewLauncher(c, func() {
		select {
		case repaint <- struct{}{}:
		default:
		}
	})
	views := map[string]*view{}

	snapshot := func(id string, v *view) {
		var root *v1.Node
		switch v.kind {
		case v1.ViewBar:
			root = screenshot.BarTree()
		case v1.ViewTooltip:
			root = screenshot.TooltipTree()
		default:
			root = screenshot.PanelTree(launcher.Model())
		}
		if err := c.Snapshot(id, v.rev+1, root); err == nil {
			v.rev++
		}
	}

	incoming := make(chan v1.Message, 16)
	go func() {
		defer cancel()
		for {
			msg, err := c.Recv()
			if err != nil {
				return
			}
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-repaint:
			for id, v := range views {
				snapshot(id, v)
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				v := &view{kind: m.View, instance: m.Instance}
				views[m.ViewID] = v
				snapshot(m.ViewID, v)
				if m.View == v1.ViewPanel {
					go func() {
						cctx, cancelCall := context.WithTimeout(ctx, callTimeout)
						defer cancelCall()
						launcher.Refresh(cctx)
					}()
				}
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					snapshot(m.ViewID, v)
				}
			case *v1.InputEvent:
				if m.Event != v1.EventActivate {
					continue
				}
				instance := ""
				if v, ok := views[m.ViewID]; ok {
					instance = v.instance
				}
				go func() {
					cctx, cancelCall := context.WithTimeout(ctx, callTimeout)
					defer cancelCall()
					launcher.Act(cctx, m.Node, m.Output, m.Generation, instance)
				}()
			}
		}
	}
}
