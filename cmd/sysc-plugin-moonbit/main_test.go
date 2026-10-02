package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/moonbit"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func drainUntil(t *testing.T, s *session, done func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !done() {
		select {
		case fn := <-s.async:
			fn()
		case <-deadline:
			t.Fatalf("phase stuck at %v (err %q)", s.state.Phase, s.state.Err)
		}
	}
}

func TestStreamEndWithoutTerminalLeavesPhaseRecoverable(t *testing.T) {
	s := &session{
		async:  make(chan func(), 8),
		runner: moonbit.Runner{Path: filepath.Join(t.TempDir(), "absent.sock")},
	}
	s.state.StartScan()
	s.state.ScanCat, s.state.ScanIdx = "apt", 1
	events := make(chan moonbit.Event, 1)
	events <- moonbit.Event{T: "category", Name: "apt", I: 1, Total: 3}
	op := &moonbit.Op{Events: events}
	s.streamOp(op)
	close(events)

	drainUntil(t, s, func() bool {
		return s.state.Phase == moonbit.PhaseError && s.op == nil
	})
	if s.state.Err != "moonbit connection lost" {
		t.Fatalf("err = %q", s.state.Err)
	}
	if s.state.ScanCat != "" || s.state.ScanIdx != 0 {
		t.Fatalf("progress left set: cat=%q idx=%d", s.state.ScanCat, s.state.ScanIdx)
	}

	// Cancel with no live op is the escape hatch for a phase that is still
	// scanning or cleaning. After the reset above it must not move a review.
	s.state.Phase = moonbit.PhaseReview
	s.handle(&v1.InputEvent{Node: "cancel"})
	if s.state.Phase != moonbit.PhaseReview {
		t.Fatalf("cancel moved review to %v", s.state.Phase)
	}
}

func TestStreamEndAfterDoneDoesNotClobberReview(t *testing.T) {
	s := &session{
		async:  make(chan func(), 8),
		runner: moonbit.Runner{Path: filepath.Join(t.TempDir(), "absent.sock")},
	}
	s.state.StartScan()
	events := make(chan moonbit.Event, 1)
	events <- moonbit.Event{T: "done"}
	op := &moonbit.Op{Events: events}
	s.streamOp(op)
	close(events)

	drainUntil(t, s, func() bool { return s.op == nil && s.state.Phase == moonbit.PhaseReview })
	// The status refresh dials a missing socket and posts one more callback.
	select {
	case fn := <-s.async:
		fn()
	case <-time.After(2 * time.Second):
	}
	if s.state.Phase != moonbit.PhaseReview {
		t.Fatalf("phase = %v, want review", s.state.Phase)
	}
}

func TestCancelWithNoOpResetsStuckScan(t *testing.T) {
	s := &session{async: make(chan func(), 1)}
	s.state.StartClean()
	s.handle(&v1.InputEvent{Node: "cancel"})
	if s.state.Phase != moonbit.PhaseError || s.state.Err != "moonbit connection lost" {
		t.Fatalf("phase=%v err=%q", s.state.Phase, s.state.Err)
	}
}
