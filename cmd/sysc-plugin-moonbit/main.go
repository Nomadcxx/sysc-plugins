package main

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/moonbit"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := runPlugin(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

type view struct {
	kind v1.ViewKind
	rev  uint64
}

type session struct {
	client *v1.Client
	runner moonbit.Runner
	state  moonbit.State
	op     *moonbit.Op
	views  map[string]view
	async  chan func()
}

func runPlugin(in io.Reader, out io.Writer) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.moonbit", Name: "Moonbit", Version: "1.0.0"})); err != nil {
		return err
	}
	s := &session{client: c, views: map[string]view{}, async: make(chan func(), 16)}
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
	s.refreshStatus()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View}
				s.snapshot(m.ViewID)
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := s.views[m.ViewID]; ok {
					v.rev = 0
					s.views[m.ViewID] = v
					s.snapshot(m.ViewID)
				}
			case *v1.InputEvent:
				s.handle(m)
				s.snapshotAll()
			}
		case f := <-s.async:
			f()
			s.snapshotAll()
		case <-ticker.C:
			s.refreshStatus()
		}
	}
}

// refreshStatus asks the daemon for its state without blocking the loop; the
// reply lands back on the loop through async.
func (s *session) refreshStatus() {
	go func() {
		st, err := s.runner.Status()
		s.async <- func() {
			if err != nil {
				// A dead socket is the idle-with-no-data case, not a fault:
				// the panel explains it, the bar stays quiet.
				if s.state.Phase == moonbit.PhaseIdle {
					s.state.Status = nil
				}
				return
			}
			s.state.Fold(*st)
		}
	}()
}

// streamOp starts a runner operation and forwards every event onto the loop.
func (s *session) streamOp(op *moonbit.Op) {
	s.op = op
	go func() {
		for ev := range op.Events {
			e := ev
			s.async <- func() {
				if e.T == "pong" {
					return
				}
				changed := s.state.Fold(e)
				if e.T == "done" || e.T == "clean_done" || e.T == "cancelled" || e.T == "error" {
					s.op = nil
					s.refreshStatus()
				}
				if changed {
					s.snapshotAll()
				}
			}
		}
		s.async <- func() {
			if s.op == op {
				s.op = nil
			}
		}
	}()
}

func (s *session) startScan(mode string) {
	if s.op != nil {
		return
	}
	s.state.StartScan()
	op, err := s.runner.Scan(mode, nil)
	if err != nil {
		s.state.Phase, s.state.Err = moonbit.PhaseError, "moonbit daemon unreachable: "+err.Error()
		return
	}
	s.streamOp(op)
}

func (s *session) startClean() {
	if s.op != nil {
		return
	}
	var cats []string
	for _, c := range s.state.SelectedStats() {
		cats = append(cats, c.Name)
	}
	if len(cats) == 0 {
		return
	}
	s.state.StartClean()
	op, err := s.runner.Clean(true, cats)
	if err != nil {
		s.state.Phase, s.state.Err = moonbit.PhaseError, "moonbit daemon unreachable: "+err.Error()
		return
	}
	s.streamOp(op)
}

func (s *session) handle(m *v1.InputEvent) {
	node := m.Node
	switch {
	case node == "bar":
		if m.Event == v1.EventActivate {
			_, _ = s.client.Call(context.Background(), v1.CallPanelOpen, v1.PanelParams{
				Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID,
			})
		}
	case node == "scan_quick":
		s.startScan("quick")
	case node == "scan_deep":
		s.startScan("deep")
	case node == "cancel":
		if s.op != nil {
			s.op.Cancel()
		}
	case node == "to_confirm":
		if len(s.state.SelectedStats()) > 0 {
			s.state.Phase = moonbit.PhaseConfirm
		}
	case node == "confirm_clean":
		s.startClean()
	case node == "cancel_op":
		s.state.Phase = moonbit.PhaseReview
	case node == "back":
		s.state.Phase = moonbit.PhaseIdle
		s.refreshStatus()
	case node == "select_all":
		all := true
		for _, c := range s.state.Review {
			if !s.state.Selected[c.Name] {
				all = false
			}
		}
		for _, c := range s.state.Review {
			s.state.Selected[c.Name] = !all
		}
	case strings.HasPrefix(node, "toggle:"):
		name := strings.TrimPrefix(node, "toggle:")
		if s.state.Selected == nil {
			s.state.Selected = map[string]bool{}
		}
		s.state.Selected[name] = !s.state.Selected[name]
	}
}

func (s *session) tree(kind v1.ViewKind) *v1.Node {
	switch kind {
	case v1.ViewBar:
		return moonbit.Bar(&s.state)
	case v1.ViewTooltip:
		return moonbit.Tooltip(&s.state)
	}
	return moonbit.Panel(&s.state)
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	v.rev++
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, s.tree(v.kind))
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}
