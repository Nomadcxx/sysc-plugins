package timer

import (
	"sync"
	"time"
)

// Mode selects which pomodoro phase the countdown is running.
type Mode string

const (
	ModeWork  Mode = "work"
	ModeShort Mode = "short"
	ModeLong  Mode = "long"
)

// Session layers pomodoro bookkeeping over a plain countdown: work and
// break phases, the completed tally, and the long-break cadence.
type Session struct {
	mu        sync.Mutex
	mode      Mode
	completed int
	work      time.Duration
	short     time.Duration
	long      time.Duration
	sessions  int
	autoWork  bool
	autoBreak bool
	timer     *Timer
}

func NewSession(now func() time.Time) *Session {
	s := &Session{
		mode:     ModeWork,
		work:     25 * time.Minute,
		short:    5 * time.Minute,
		long:     15 * time.Minute,
		sessions: 4,
		timer:    New(now),
	}
	// The countdown carries its own default length, so the opening work
	// phase has to be pushed into it; otherwise a session that is never
	// configured counts the bare timer's five minutes under a Work label.
	s.timer.SetDuration(s.work)
	return s
}

func (s *Session) Mode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *Session) Completed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completed
}

func (s *Session) SessionsBeforeLong() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions
}

// SetDurations updates the phase lengths; an idle countdown picks the
// change up immediately, a running one on its next phase switch.
func (s *Session) SetDurations(work, short, long time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if work > 0 {
		s.work = work
	}
	if short > 0 {
		s.short = short
	}
	if long > 0 {
		s.long = long
	}
	if s.timer.State() == StateIdle {
		s.timer.SetDuration(s.duration(s.mode))
	}
}

func (s *Session) SetSessions(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 1 {
		n = 1
	}
	s.sessions = n
}

func (s *Session) SetAutoWork(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoWork = v
}

func (s *Session) SetAutoBreak(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoBreak = v
}

// SetMode switches phase, cancelling whatever is on the clock.
func (s *Session) SetMode(m Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch m {
	case ModeWork, ModeShort, ModeLong:
	default:
		return
	}
	s.mode = m
	s.timer.SetDuration(s.duration(m))
	s.timer.Reset()
}

// Tick advances the countdown; when a phase completes it rolls the
// bookkeeping forward and may auto-start the next phase.
func (s *Session) Tick() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining, done := s.timer.Tick()
	if !done {
		return remaining, false
	}
	var next Mode
	var auto bool
	if s.mode == ModeWork {
		s.completed++
		next = ModeShort
		if s.completed%s.sessions == 0 {
			next = ModeLong
		}
		auto = s.autoBreak
	} else {
		next = ModeWork
		auto = s.autoWork
	}
	s.mode = next
	s.timer.SetDuration(s.duration(next))
	s.timer.Reset()
	if auto {
		s.timer.Start()
	}
	return remaining, true
}

func (s *Session) Remaining() time.Duration { return s.timer.Remaining() }
func (s *Session) Running() bool            { return s.timer.Running() }
func (s *Session) Duration() time.Duration  { return s.timer.Duration() }
func (s *Session) Progress() float64        { return s.timer.Progress() }
func (s *Session) State() State             { return s.timer.State() }
func (s *Session) Fired() bool              { return s.timer.Fired() }
func (s *Session) Start()                   { s.timer.Start() }
func (s *Session) Pause()                   { s.timer.Pause() }
func (s *Session) Reset()                   { s.timer.Reset() }
func (s *Session) Deadline() (time.Time, bool) {
	return s.timer.Deadline()
}

// Snapshot is the part of a session that has to outlive the process. The
// deadline alone is not enough: without the phase and the tally a restart
// resumes a break under a Work label and counts it as a finished pomodoro.
type Snapshot struct {
	Mode      Mode  `json:"mode"`
	Completed int   `json:"completed"`
	Duration  int64 `json:"duration_seconds,omitempty"`
	Deadline  int64 `json:"deadline,omitempty"`
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Mode: s.mode, Completed: s.completed}
	if deadline, ok := s.timer.Deadline(); ok {
		snap.Deadline = deadline.Unix()
		snap.Duration = int64(s.timer.Duration() / time.Second)
	}
	return snap
}

// RestoreSnapshot puts a saved session back. A countdown that was running
// resumes against its own phase length; anything else opens on the phase
// the settings ask for, which arrive after this call.
func (s *Session) RestoreSnapshot(snap Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch snap.Mode {
	case ModeWork, ModeShort, ModeLong:
		s.mode = snap.Mode
	}
	if snap.Completed > 0 {
		s.completed = snap.Completed
	}
	if snap.Deadline > 0 {
		s.timer.Restore(time.Unix(snap.Deadline, 0), time.Duration(snap.Duration)*time.Second)
		return
	}
	s.timer.SetDuration(s.duration(s.mode))
}

// View is everything one frame needs, read under a single lock so a
// publish racing a phase change cannot mix the two sides of it.
type View struct {
	Remaining time.Duration
	State     State
	Progress  float64
	Mode      Mode
	Completed int
	Sessions  int
}

func (s *Session) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return View{
		Remaining: s.timer.Remaining(),
		State:     s.timer.State(),
		Progress:  s.timer.Progress(),
		Mode:      s.mode,
		Completed: s.completed,
		Sessions:  s.sessions,
	}
}

func (s *Session) duration(m Mode) time.Duration {
	switch m {
	case ModeShort:
		return s.short
	case ModeLong:
		return s.long
	default:
		return s.work
	}
}
