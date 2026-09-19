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

// A session nobody has configured still opens on the work length: the
// settings message is optional, and the bar is read before it arrives.
func TestNewSessionOpensOnTheWorkLength(t *testing.T) {
	s := NewSession(func() time.Time { return time.Unix(0, 0) })
	if s.Mode() != ModeWork {
		t.Fatalf("mode = %v, want work", s.Mode())
	}
	if s.Remaining() != 25*time.Minute {
		t.Fatalf("remaining = %v, want 25m", s.Remaining())
	}
	if s.Duration() != 25*time.Minute {
		t.Fatalf("duration = %v, want 25m", s.Duration())
	}
}

func TestSnapshotRoundTripsPhaseAndTally(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := NewSession(func() time.Time { return now })
	s.SetDurations(25*time.Minute, 5*time.Minute, 15*time.Minute)
	s.SetMode(ModeLong)
	s.Start()

	snap := s.Snapshot()
	if snap.Mode != ModeLong {
		t.Fatalf("snapshot mode = %v, want long", snap.Mode)
	}
	if snap.Deadline == 0 || snap.Duration != int64((15*time.Minute).Seconds()) {
		t.Fatalf("snapshot = %+v, want the long-break deadline and length", snap)
	}

	// A fresh process, as after a shell restart.
	later := now.Add(5 * time.Minute)
	restored := NewSession(func() time.Time { return later })
	restored.SetDurations(25*time.Minute, 5*time.Minute, 15*time.Minute)
	restored.RestoreSnapshot(snap)

	if restored.Mode() != ModeLong {
		t.Fatalf("restored mode = %v, want long; a break must not come back as work", restored.Mode())
	}
	if restored.Remaining() != 10*time.Minute {
		t.Fatalf("restored remaining = %v, want 10m", restored.Remaining())
	}
	if restored.Duration() != 15*time.Minute {
		t.Fatalf("restored duration = %v, want the long-break length", restored.Duration())
	}
}

// The tally is what a restart used to corrupt: a break restored as work
// completes, increments completed, and drifts the long-break cadence.
func TestRestoreKeepsTheTallyAndCadence(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := NewSession(func() time.Time { return now })
	s.SetDurations(time.Second, time.Second, time.Second)
	for i := 0; i < 3; i++ {
		s.SetMode(ModeWork)
		s.Start()
		now = now.Add(2 * time.Second)
		s.Tick()
	}
	if s.Completed() != 3 {
		t.Fatalf("completed = %d, want 3", s.Completed())
	}

	restored := NewSession(func() time.Time { return now })
	restored.SetDurations(time.Second, time.Second, time.Second)
	restored.RestoreSnapshot(s.Snapshot())
	if restored.Completed() != 3 {
		t.Fatalf("restored completed = %d, want 3", restored.Completed())
	}

	// The fourth work phase is the one that earns the long break.
	restored.SetMode(ModeWork)
	restored.Start()
	now = now.Add(2 * time.Second)
	restored.Tick()
	if restored.Mode() != ModeLong {
		t.Fatalf("mode after the 4th pomodoro = %v, want long", restored.Mode())
	}
}

func TestViewIsOneConsistentRead(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := NewSession(func() time.Time { return now })
	s.SetDurations(2*time.Minute, time.Minute, 3*time.Minute)
	s.SetMode(ModeShort)
	v := s.View()
	if v.Mode != ModeShort || v.Remaining != time.Minute || v.Sessions != 4 {
		t.Fatalf("view = %+v, want the short break at a minute", v)
	}
	if v.State != StateIdle {
		t.Fatalf("view state = %v, want idle", v.State)
	}
}
