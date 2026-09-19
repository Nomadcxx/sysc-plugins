package timer

import (
	"testing"
	"time"
)

func TestSessionWorkRollsIntoShortBreak(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewSession(func() time.Time { return now })
	s.SetDurations(2*time.Second, time.Minute, 2*time.Minute)
	s.Start()
	now = now.Add(2 * time.Second)
	if _, done := s.Tick(); !done {
		t.Fatal("work phase did not complete")
	}
	if s.Mode() != ModeShort {
		t.Fatalf("mode = %q, want short", s.Mode())
	}
	if s.Completed() != 1 {
		t.Fatalf("completed = %d, want 1", s.Completed())
	}
	if s.Remaining() != time.Minute {
		t.Fatalf("remaining = %v, want the short break length", s.Remaining())
	}
	if s.Running() {
		t.Fatal("break auto-started with auto-start off")
	}
}

func TestSessionLongBreakAfterCadence(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewSession(func() time.Time { return now })
	s.SetDurations(time.Second, time.Second, 3*time.Second)
	s.SetSessions(2)
	for i := 0; i < 2; i++ {
		s.SetMode(ModeWork)
		s.Start()
		now = now.Add(time.Second)
		if _, done := s.Tick(); !done {
			t.Fatal("work phase did not complete")
		}
		if i == 0 && s.Mode() != ModeShort {
			t.Fatalf("mode after first work phase = %q, want short", s.Mode())
		}
	}
	if s.Mode() != ModeLong {
		t.Fatalf("mode = %q, want long", s.Mode())
	}
	if s.Completed() != 2 {
		t.Fatalf("completed = %d, want 2", s.Completed())
	}
}

func TestSessionAutoStart(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewSession(func() time.Time { return now })
	s.SetDurations(time.Second, time.Minute, time.Minute)
	s.SetAutoBreak(true)
	s.Start()
	now = now.Add(time.Second)
	s.Tick()
	if s.Mode() != ModeShort {
		t.Fatalf("mode = %q, want short", s.Mode())
	}
	if !s.Running() {
		t.Fatal("break did not auto-start")
	}
	now = now.Add(time.Minute)
	s.Tick()
	if s.Mode() != ModeWork {
		t.Fatalf("mode = %q, want work", s.Mode())
	}
	if s.Running() {
		t.Fatal("work auto-started with auto-start off")
	}
}

func TestSessionSetModeResets(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewSession(func() time.Time { return now })
	s.SetDurations(time.Minute, 5*time.Minute, 15*time.Minute)
	s.Start()
	now = now.Add(30 * time.Second)
	s.SetMode(ModeShort)
	if s.Mode() != ModeShort {
		t.Fatalf("mode = %q, want short", s.Mode())
	}
	if s.Running() {
		t.Fatal("mode switch left the clock running")
	}
	if s.Remaining() != 5*time.Minute {
		t.Fatalf("remaining = %v, want the short break length", s.Remaining())
	}
}

func TestSessionSessionsClampToOne(t *testing.T) {
	s := NewSession(func() time.Time { return time.Unix(0, 0) })
	s.SetSessions(0)
	if s.SessionsBeforeLong() != 1 {
		t.Fatalf("sessions = %d, want 1", s.SessionsBeforeLong())
	}
}
