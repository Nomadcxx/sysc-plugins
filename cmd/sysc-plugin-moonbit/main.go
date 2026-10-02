package main

import (
	"bytes"
	"context"
	"encoding/json"
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

// frameGap spaces stream-driven frames. A deep scan finishes absent-path
// categories in milliseconds, and the host ends a plugin that outruns its
// update budget (60/s), so daemon events coalesce to at most ten flushes a
// second; input still flushes at once.
const frameGap = 100 * time.Millisecond

type view struct {
	kind v1.ViewKind
	rev  uint64
	sent []byte // last tree sent, so an unchanged view is not resent
}

type session struct {
	client *v1.Client
	runner moonbit.Runner
	state  moonbit.State
	op     *moonbit.Op
	views  map[string]view
	async  chan func()

	lastFlush time.Time
	flushAt   <-chan time.Time // armed while a coalesced flush is pending
}

func runPlugin(in io.Reader, out io.Writer) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.moonbit", Name: "Moonbit", Version: "1.1.0"})); err != nil {
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
				s.snapshot(m.ViewID, true)
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := s.views[m.ViewID]; ok {
					v.rev = 0
					s.views[m.ViewID] = v
					s.snapshot(m.ViewID, true)
				}
			case *v1.InputEvent:
				s.handle(m)
				s.flush()
			}
		case f := <-s.async:
			f()
			s.schedule()
		case <-s.flushAt:
			s.flush()
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
				s.state.Fold(e)
				if e.T == "done" || e.T == "clean_done" || e.T == "cancelled" || e.T == "error" {
					s.op = nil
					s.refreshStatus()
				}
			}
		}
		s.async <- func() {
			if s.op == op {
				s.op = nil
				if s.state.StreamEnded() {
					s.refreshStatus()
				}
			}
		}
	}()
}

func (s *session) startScan(mode string) {
	if s.op != nil {
		return
	}
	s.state.Review, s.state.Selected = nil, nil
	s.state.StartScan()
	op, err := s.runner.Scan(mode, nil)
	if err != nil {
		s.state.Phase, s.state.Err = moonbit.PhaseError, moonbit.UnreachableMessage
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
	op, err := s.runner.Clean(true, cats, s.state.ScannedAt)
	if err != nil {
		s.state.Phase, s.state.Err = moonbit.PhaseError, moonbit.UnreachableMessage
		return
	}
	s.streamOp(op)
}

func (s *session) handle(m *v1.InputEvent) {
	node := m.Node
	switch {
	case node == "bar":
		if m.Event == v1.EventActivate {
			// Off the loop: the reply arrives through Recv, which blocks
			// while the loop is not draining incoming.
			params := v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = s.client.Call(ctx, v1.CallPanelOpen, params)
			}()
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
		// The one destructive action only counts from the confirm screen the
		// user is looking at: an event aimed at an older revision may come
		// from a tree that showed another selection.
		if s.state.Phase == moonbit.PhaseConfirm && m.Revision == s.views[m.ViewID].rev {
			s.startClean()
		}
	case node == "cancel_op":
		s.state.Phase = moonbit.PhaseReview
	case node == "back":
		s.state.Phase = moonbit.PhaseIdle
		s.state.Review, s.state.Selected = nil, nil
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

// snapshot sends a view's tree at a new revision, unless it matches what the
// host already has and force is false.
func (s *session) snapshot(id string, force bool) {
	v := s.views[id]
	tree := s.tree(v.kind)
	b, err := json.Marshal(tree)
	if err != nil {
		return
	}
	if !force && bytes.Equal(b, v.sent) {
		return
	}
	v.rev++
	v.sent = b
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, tree)
}

// flush publishes every changed view now.
func (s *session) flush() {
	s.flushAt = nil
	s.lastFlush = time.Now()
	for id := range s.views {
		s.snapshot(id, false)
	}
}

// schedule flushes now if the last frame is at least frameGap old, otherwise
// once that gap has passed; changes landing meanwhile share the frame.
func (s *session) schedule() {
	if s.flushAt != nil {
		return
	}
	wait := frameGap - time.Since(s.lastFlush)
	if wait <= 0 {
		s.flush()
		return
	}
	s.flushAt = time.After(wait)
}
