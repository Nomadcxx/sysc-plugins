package main

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/moonbit"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// clickSession is a session with one panel view at revision 1, so every event
// below counts as `current` the way a real click on the rendered tree does.
func clickSession() *session {
	return &session{views: map[string]view{"panel": {kind: v1.ViewPanel, rev: 1}}}
}

func (s *session) click(node string) {
	s.handle(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: 1,
		Node: node, Event: v1.EventActivate})
}

// A node id is host input, not a value the TUI chose. `docker:<op>` copied
// whatever came after the colon straight into a sudo request.
func TestDockerRunOnlyReachesRootForAKnownOp(t *testing.T) {
	for _, tc := range []struct {
		op    string
		valid bool
	}{{"images", true}, {"all", true}, {"prune;rm -rf /", false}, {"", false}, {"IMAGES", false}} {
		t.Run(tc.op, func(t *testing.T) {
			s := clickSession()
			s.state.Phase = moonbit.PhaseDocker
			s.click("docker:" + tc.op)
			s.click("docker_run")
			if got := s.state.Phase == moonbit.PhaseAuth; got != tc.valid {
				t.Fatalf("op %q reached the root prompt: %v", tc.op, got)
			}
			if s.pending != nil && s.pending.Op != tc.op {
				t.Errorf("pending op %q, want %q", s.pending.Op, tc.op)
			}
			if !tc.valid && s.pending != nil {
				t.Errorf("op %q queued a sudo request %+v", tc.op, *s.pending)
			}
		})
	}
}

// `sched:<target>:<action>` only counted its colons, so any three-part id
// became a sudo systemctl run.
func TestScheduleOnlyReachesRootForAKnownTargetAndAction(t *testing.T) {
	for _, tc := range []struct {
		node  string
		valid bool
	}{
		{"sched:daemon:enable", true},
		{"sched:daemon:disable", true},
		{"sched:timers:enable", true},
		{"sched:timers:disable", true},
		{"sched:daemon:start", false},
		{"sched:anything:enable", false},
		{"sched:daemon:", false},
		{"sched:daemon:enable:now", false},
	} {
		t.Run(tc.node, func(t *testing.T) {
			s := clickSession()
			s.state.Phase = moonbit.PhaseSchedule
			s.click(tc.node)
			if got := s.state.Phase == moonbit.PhaseAuth; got != tc.valid {
				t.Fatalf("%s reached the root prompt: %v", tc.node, got)
			}
			if !tc.valid && s.pending != nil {
				t.Errorf("%s queued a sudo request %+v", tc.node, *s.pending)
			}
		})
	}
}

// An unknown op must not reach the confirm screen at all: dockerCard filters
// to the chosen op, so a bogus one renders an empty card above a live
// "Clean Now" button.
func TestUnknownDockerOpStaysOffTheConfirmScreen(t *testing.T) {
	s := clickSession()
	s.state.Phase = moonbit.PhaseDocker
	s.click("docker:not-a-real-op")
	if s.state.Phase != moonbit.PhaseDocker {
		t.Errorf("phase %d, want %d", s.state.Phase, moonbit.PhaseDocker)
	}
	if s.state.DockerOp != "" {
		t.Errorf("DockerOp %q kept an unvalidated id", s.state.DockerOp)
	}
}
