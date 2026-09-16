package main

import (
	"context"
	"os"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/notes"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.notes", Name: "Notes", Version: "1.0.0"})); err != nil {
		return err
	}
	var sess *notes.Session
	ensure := func() *notes.Session {
		if sess != nil {
			return sess
		}
		st, err := notes.Open("~/Documents/Notes", "md")
		if err != nil {
			return nil
		}
		sess = notes.NewSession(st, time.Now)
		return sess
	}
	type view struct {
		kind v1.ViewKind
		rev  uint64
	}
	views := map[string]view{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			incoming <- msg
		}
	}()
	ticks := time.NewTicker(time.Second)
	defer ticks.Stop()

	snapshot := func() {
		s := sess
		if s == nil {
			return
		}
		snap := s.Snap()
		for id, v := range views {
			v.rev++
			views[id] = v
			switch v.kind {
			case v1.ViewBar:
				_ = c.Snapshot(id, v.rev, notes.BarTree())
			case v1.ViewTooltip:
				_ = c.Snapshot(id, v.rev, notes.TooltipTree())
			default:
				_ = c.Snapshot(id, v.rev, notes.PanelTree(snap))
			}
		}
	}

	panelOpen := func() bool {
		for _, v := range views {
			if v.kind == v1.ViewPanel {
				return true
			}
		}
		return false
	}

	for {
		select {
		case <-ctx.Done():
			if sess != nil {
				_ = sess.Close()
			}
			return nil
		case <-ticks.C:
			if sess != nil && panelOpen() {
				sess.Tick()
				snapshot()
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				if sess != nil {
					_ = sess.Close()
				}
				return nil
			case *v1.ViewOpen:
				if ensure() == nil {
					return nil
				}
				views[m.ViewID] = view{kind: m.View}
				snapshot()
			case *v1.ViewClose:
				if v, ok := views[m.ViewID]; ok && v.kind == v1.ViewPanel && sess != nil {
					_ = sess.Close()
				}
				delete(views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					v.rev = 0
					views[m.ViewID] = v
				}
				snapshot()
			case *v1.InputEvent:
				if ensure() == nil {
					continue
				}
				handle(ctx, c, sess, m)
				snapshot()
			case *v1.SettingsChanged:
				sess = applySettings(sess, m.Values)
				snapshot()
			}
		}
	}
}

func applySettings(sess *notes.Session, values map[string]any) *notes.Session {
	dir := "~/Documents/Notes"
	ext := "md"
	if values != nil {
		if v, ok := values["notes_dir"].(string); ok && v != "" {
			dir = v
		}
		if v, ok := values["extension"].(string); ok && v != "" {
			ext = v
		}
	}
	st, err := notes.Open(dir, ext)
	if err != nil {
		return sess
	}
	if sess == nil {
		return notes.NewSession(st, time.Now)
	}
	sess.SetStore(st)
	return sess
}

func handle(ctx context.Context, c *v1.Client, sess *notes.Session, m *v1.InputEvent) {
	switch {
	case m.Node == "open":
		_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
	case m.Node == "new":
		_ = sess.Create()
	case m.Node == "scratch":
		_ = sess.OpenScratch()
	case m.Node == "back":
		sess.Back()
	case m.Node == "cancel":
		sess.CancelPending()
	case m.Node == "confirm-delete":
		_ = sess.ConfirmDelete()
	case m.Node == "reload":
		sess.Reload()
	case m.Node == "keep":
		sess.KeepLocal()
	case m.Node == "body":
		sess.Type(m.Text)
	case m.Node == "title":
		renameTitle(sess, m.Text)
	case strings.HasPrefix(m.Node, "open:"):
		_ = sess.Open(strings.TrimPrefix(m.Node, "open:"))
	case strings.HasPrefix(m.Node, "pin:"):
		name := strings.TrimPrefix(m.Node, "pin:")
		on := true
		for _, n := range sess.Snap().Notes {
			if n.Name == name && n.Pinned {
				on = false
				break
			}
		}
		_ = sess.Pin(name, on)
	case strings.HasPrefix(m.Node, "rm:"):
		sess.ProposeDelete(strings.TrimPrefix(m.Node, "rm:"))
	}
}

func renameTitle(sess *notes.Session, title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	cur := sess.Snap().Current
	suffix := ".md"
	if dot := strings.LastIndex(cur, "."); dot >= 0 {
		suffix = cur[dot:]
	}
	if !strings.HasSuffix(title, suffix) {
		title += suffix
	}
	_ = sess.Rename(title)
}
