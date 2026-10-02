package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	// pending is the root run waiting on the password prompt; password is
	// the field's live value, zeroed once handed to sudo.
	pending  *moonbit.Request
	password []byte

	lastFlush time.Time
	flushAt   <-chan time.Time // armed while a coalesced flush is pending
}

func runPlugin(in io.Reader, out io.Writer) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.moonbit", Name: "Moonbit", Version: "1.2.0"})); err != nil {
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

// refreshStatus reads the last scan and the schedule off the loop; neither
// needs root. The result lands back on the loop through async.
func (s *session) refreshStatus() {
	go func() {
		cache, err := moonbit.LastScan()
		sched := moonbit.ReadSchedule()
		s.async <- func() {
			if err == nil {
				s.state.Cache = cache
			}
			s.state.Schedule = sched
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
				if e.T == "done" || e.T == "clean_done" || e.T == "cancelled" || e.T == "error" || e.T == "docker_done" || e.T == "schedule_done" {
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

// requestAuth puts a root run behind the password prompt, as `sudo moonbit`
// asks before anything runs.
func (s *session) requestAuth(req moonbit.Request) {
	if s.op != nil {
		return
	}
	s.pending = &req
	s.state.Back = s.state.Phase
	if s.state.Back == moonbit.PhaseAuth || s.state.Back == moonbit.PhaseError {
		s.state.Back = moonbit.PhaseIdle
	}
	s.state.Phase, s.state.Err = moonbit.PhaseAuth, ""
	s.state.AuthFor, s.state.AuthErr, s.state.Authorizing = moonbit.RequestLabel(req), "", false
}

// authorize hands the password to sudo off the loop and starts the pending
// run once sudo accepts it.
func (s *session) authorize() {
	if s.pending == nil || s.state.Authorizing || s.op != nil {
		return
	}
	req, pw := *s.pending, s.password
	s.password = nil
	s.state.Authorizing, s.state.AuthErr = true, ""
	s.state.AuthReseed++ // clear the field
	go func() {
		op, err := s.runner.Start(pw, req)
		s.async <- func() {
			s.state.Authorizing = false
			if err != nil {
				if errors.Is(err, moonbit.ErrWrongPassword) {
					s.state.AuthErr = "Sorry, that password wasn't accepted. Try again."
					return
				}
				s.pending = nil
				s.state.Phase, s.state.Err = moonbit.PhaseError, err.Error()
				return
			}
			s.pending = nil
			s.begin(req)
			s.streamOp(op)
		}
	}()
}

// begin flips the view to the run's progress screen before its first event.
func (s *session) begin(req moonbit.Request) {
	switch req.Cmd {
	case "scan":
		s.state.Review, s.state.Selected = nil, nil
		s.state.StartScan()
	case "clean":
		s.state.StartClean()
	case "docker":
		s.state.Phase, s.state.Back = moonbit.PhaseWorking, moonbit.PhaseDocker
		s.state.Working = "Cleaning Docker"
	case "schedule":
		s.state.Phase, s.state.Back = moonbit.PhaseWorking, moonbit.PhaseSchedule
		s.state.Working = map[string]string{"enable": "Enabling ", "disable": "Disabling "}[req.Action] +
			map[string]string{"daemon": "daemon mode", "timers": "the timers"}[req.Target]
	}
}

func (s *session) cleanRequest() *moonbit.Request {
	var cats []string
	for _, c := range s.state.SelectedStats() {
		cats = append(cats, c.Name)
	}
	if len(cats) == 0 {
		return nil
	}
	return &moonbit.Request{Cmd: "clean", Force: true, Categories: cats, ScannedAt: s.state.ScannedAt}
}

func (s *session) handle(m *v1.InputEvent) {
	node := m.Node
	// current is true when the event comes from the tree the user is
	// looking at; destructive actions only count then.
	current := m.Revision == s.views[m.ViewID].rev
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
	case node == "password":
		if s.state.Phase != moonbit.PhaseAuth {
			return
		}
		clear(s.password)
		s.password = []byte(m.Text)
		if m.Event == v1.EventSubmit {
			s.authorize()
		}
	case node == "auth_run":
		if s.state.Phase == moonbit.PhaseAuth {
			s.authorize()
		}
	case node == "auth_cancel":
		if s.state.Phase == moonbit.PhaseAuth && !s.state.Authorizing {
			clear(s.password)
			s.password, s.pending = nil, nil
			s.state.AuthReseed++
			s.state.Phase = s.state.Back
		}
	case node == "scan_quick":
		s.requestAuth(moonbit.Request{Cmd: "scan", Mode: "quick"})
	case node == "scan_deep":
		s.requestAuth(moonbit.Request{Cmd: "scan", Mode: "deep"})
	case node == "review_last":
		s.reviewLast()
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
		if s.state.Phase == moonbit.PhaseConfirm && current {
			if req := s.cleanRequest(); req != nil {
				s.requestAuth(*req)
			}
		}
	case node == "cancel_op":
		s.state.Phase = moonbit.PhaseReview
	case node == "back":
		s.state.Phase, s.state.Err, s.state.Notice = moonbit.PhaseIdle, "", ""
		s.state.Review, s.state.Selected = nil, nil
		s.refreshStatus()
	case node == "docker":
		s.state.Phase, s.state.Err, s.state.Notice = moonbit.PhaseDocker, "", ""
	case strings.HasPrefix(node, "docker:"):
		if s.state.Phase == moonbit.PhaseDocker {
			s.state.DockerOp = strings.TrimPrefix(node, "docker:")
			s.state.Phase = moonbit.PhaseDockerConfirm
		}
	case node == "docker_run":
		if s.state.Phase == moonbit.PhaseDockerConfirm && current {
			s.requestAuth(moonbit.Request{Cmd: "docker", Op: s.state.DockerOp})
		}
	case node == "schedule":
		s.state.Phase, s.state.Err, s.state.Notice = moonbit.PhaseSchedule, "", ""
		s.refreshStatus()
	case strings.HasPrefix(node, "sched:"):
		parts := strings.Split(node, ":") // sched:<target>:<action>
		if s.state.Phase == moonbit.PhaseSchedule && len(parts) == 3 {
			s.requestAuth(moonbit.Request{Cmd: "schedule", Target: parts[1], Action: parts[2]})
		}
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

// reviewLast opens the review list on the last saved scan, the TUI's Review
// Results. The clean is bound to that scan's stamp.
func (s *session) reviewLast() {
	c := s.state.Cache
	if c == nil || c.Files == 0 {
		return
	}
	s.state.Review, s.state.Selected = nil, map[string]bool{}
	for _, cat := range c.Categories {
		if cat.Files > 0 {
			s.state.Review = append(s.state.Review, cat)
			s.state.Selected[cat.Name] = true
		}
	}
	s.state.ScannedAt = c.ScannedAt
	s.state.ScanErrs, s.state.ScanSkipped = nil, nil
	s.state.Phase, s.state.Err = moonbit.PhaseReview, ""
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
