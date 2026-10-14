package main

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// waitStep is one poll's world: the instant, the pacman.log mtime, and
// whether the pacman database lock is held. waitForTerminalWith samples this
// world exactly once per wake-up, so serving steps in order from a channel
// keeps the test deterministic: no sleeps, no wall clock.
type waitStep struct {
	at     time.Time
	mtime  time.Time
	locked bool
}

type waitScript struct {
	t          *testing.T
	steps      chan waitStep
	ticks      chan time.Time
	done       chan struct{}
	finished   chan struct{}
	clock      atomic.Int64
	locked     atomic.Bool
	updates    atomic.Int32
	lastUpdate atomic.Int64
	releases   atomic.Int32
}

func startWaitScript(t *testing.T, base, mtime time.Time) *waitScript {
	t.Helper()
	s := &waitScript{
		t:        t,
		steps:    make(chan waitStep),
		ticks:    make(chan time.Time),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	s.clock.Store(base.UnixNano())
	go func() {
		waitForTerminalWith(s.done, s.mtimeAt, s.lockHeld, s.ticks, s.now, s.onUpdate, func() { s.releases.Add(1) })
		close(s.finished)
	}()
	s.serve(waitStep{at: base, mtime: mtime})
	return s
}

func (s *waitScript) now() time.Time { return time.Unix(0, s.clock.Load()) }

// mtimeAt serves the next step. The waiter calls it once per poll, so the
// step's instant becomes the time the rest of that poll reasons about.
func (s *waitScript) mtimeAt() time.Time {
	step := <-s.steps
	s.clock.Store(step.at.UnixNano())
	s.locked.Store(step.locked)
	return step.mtime
}

func (s *waitScript) lockHeld() bool { return s.locked.Load() }

func (s *waitScript) onUpdate() {
	s.updates.Add(1)
	s.lastUpdate.Store(s.now().UnixNano())
}

// poll wakes the waiter for one tick and hands it the world to observe. The
// sends are bounded so a waiter that stops polling fails the test instead of
// hanging it.
func (s *waitScript) poll(step waitStep) {
	select {
	case s.ticks <- step.at:
	case <-time.After(2 * time.Second):
		s.t.Fatal("waiter stopped polling before the script ended")
	}
	s.serve(step)
}

// exit closes the launcher and hands the waiter one more poll at the same
// instant, so the exit is taken at the clock the last poll left behind.
func (s *waitScript) exit(at, mtime time.Time) {
	close(s.done)
	s.serve(waitStep{at: at, mtime: mtime})
}

// driveToBudget polls in ten-minute strides to the two-hour budget, so every
// script ends with the watcher actually stopping.
func (s *waitScript) driveToBudget(base, from, mtime time.Time, locked bool) {
	for at := from; !at.After(base.Add(updateWaitBudget)); at = at.Add(10 * time.Minute) {
		s.poll(waitStep{at: at, mtime: mtime, locked: locked})
	}
}

func (s *waitScript) serve(step waitStep) {
	select {
	case s.steps <- step:
	case <-time.After(2 * time.Second):
		s.t.Fatal("waiter stopped polling before the script ended")
	}
}

// TestWaitForTerminalRefreshesOncePerSettledEpisode walks the cases the
// post-update refresh has to survive: a hand-off launcher that exits before
// pacman starts, a transaction that starts only after the launcher grace, a
// run that is cancelled or ignores the log, and one that never finishes. It
// also pins that the run guard is released exactly once, however the run ends.
func TestWaitForTerminalRefreshesOncePerSettledEpisode(t *testing.T) {
	const step = 5 * time.Second
	base := time.Unix(1_700_000_000, 0)
	quiet := base.Add(-time.Hour)

	cases := []struct {
		name          string
		script        func(*waitScript, time.Time)
		wantRefreshes int32
		wantAfter     time.Duration
		wantAtMost    time.Duration
	}{
		{
			name: "the lock clears long after the log moved",
			script: func(s *waitScript, base time.Time) {
				logged := base.Add(step)
				s.poll(waitStep{at: base.Add(step), mtime: logged, locked: true})
				s.poll(waitStep{at: base.Add(2 * step), mtime: logged, locked: true})
				s.poll(waitStep{at: base.Add(3 * step), mtime: logged, locked: false})
				s.driveToBudget(base, base.Add(10*time.Minute), logged, false)
			},
			wantRefreshes: 1, wantAfter: 3 * step, wantAtMost: 3 * step,
		},
		{
			name: "the launcher exits before pacman starts",
			script: func(s *waitScript, base time.Time) {
				s.exit(base, quiet)
				s.poll(waitStep{at: base.Add(step), mtime: quiet})
				logged := base.Add(2 * step)
				s.poll(waitStep{at: base.Add(2 * step), mtime: logged, locked: true})
				s.poll(waitStep{at: base.Add(3 * step), mtime: logged, locked: true})
				s.poll(waitStep{at: base.Add(4 * step), mtime: logged, locked: false})
				s.driveToBudget(base, base.Add(10*time.Minute), logged, false)
			},
			wantRefreshes: 1, wantAfter: 4 * step, wantAtMost: 4 * step,
		},
		{
			name: "nothing ever moves",
			script: func(s *waitScript, base time.Time) {
				s.exit(base, quiet)
				for i := 1; i <= int(launcherGrace/step); i++ {
					s.poll(waitStep{at: base.Add(time.Duration(i) * step), mtime: quiet})
				}
				s.driveToBudget(base, base.Add(10*time.Minute), quiet, false)
			},
			wantRefreshes: 1, wantAfter: launcherGrace, wantAtMost: launcherGrace,
		},
		{
			name: "the transaction starts after the grace",
			script: func(s *waitScript, base time.Time) {
				s.exit(base, quiet)
				for i := 1; i <= int(launcherGrace/step); i++ {
					s.poll(waitStep{at: base.Add(time.Duration(i) * step), mtime: quiet})
				}
				logged := base.Add(2 * time.Minute)
				s.poll(waitStep{at: base.Add(2 * time.Minute), mtime: logged, locked: true})
				s.poll(waitStep{at: base.Add(150 * time.Second), mtime: logged, locked: true})
				s.poll(waitStep{at: base.Add(3 * time.Minute), mtime: logged, locked: false})
				s.driveToBudget(base, base.Add(10*time.Minute), logged, false)
			},
			wantRefreshes: 2, wantAfter: 3 * time.Minute, wantAtMost: 3 * time.Minute,
		},
		{
			name: "the lock is never released",
			script: func(s *waitScript, base time.Time) {
				const stride = 10 * time.Minute
				logged := base
				for i := 1; i <= int(updateWaitBudget/stride); i++ {
					s.poll(waitStep{at: base.Add(time.Duration(i) * stride), mtime: logged, locked: true})
				}
			},
			wantRefreshes: 1, wantAfter: updateWaitBudget, wantAtMost: updateWaitBudget,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := startWaitScript(t, base, quiet)
			c.script(s, base)
			select {
			case <-s.finished:
			case <-time.After(2 * time.Second):
				t.Fatal("waiter did not finish")
			}
			if got := s.updates.Load(); got != c.wantRefreshes {
				t.Fatalf("refreshes = %d, want %d", got, c.wantRefreshes)
			}
			if got := s.releases.Load(); got != 1 {
				t.Fatalf("run guard released %d times, want exactly one", got)
			}
			got := time.Unix(0, s.lastUpdate.Load()).Sub(base)
			if got < c.wantAfter || got > c.wantAtMost {
				t.Fatalf("last refresh at +%s, want between +%s and +%s", got, c.wantAfter, c.wantAtMost)
			}
		})
	}
}

func TestQueueOneCheckRunsOneFollowUpAfterAnActiveCheck(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	followUp := make(chan struct{})
	var calls atomic.Int32
	request := queueOneCheck(func() {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
		case 2:
			close(followUp)
		}
	})
	request()
	<-started
	request()
	close(release)
	select {
	case <-followUp:
	case <-time.After(time.Second):
		t.Fatal("a refresh requested during an active check was lost")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("check calls = %d, want 2", got)
	}
}

func TestUpdateCommandChoice(t *testing.T) {
	orig := lookPath
	defer func() { lookPath = orig }()

	both := func(name string) (string, error) {
		if name == "paru" || name == "yay" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	none := func(string) (string, error) { return "", errors.New("not found") }

	cases := []struct {
		name string
		cfg  settings
		look func(string) (string, error)
		want string
	}{
		{"override wins", settings{updateCommand: "topgrade"}, both, "topgrade"},
		{"helper preferred", settings{aurHelper: "auto"}, both, "paru -Syu"},
		{"fallback to pacman", settings{aurHelper: "off"}, both, "sudo pacman -Syu"},
		{"no helper installed", settings{aurHelper: "auto"}, none, "sudo pacman -Syu"},
		{"explicit missing helper", settings{aurHelper: "yay"}, none, "sudo pacman -Syu"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lookPath = c.look
			if got := updateCommand(c.cfg); got != c.want {
				t.Fatalf("updateCommand = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDefaultSettings(t *testing.T) {
	cfg := defaultSettings()
	if cfg.intervalHours != 3 || cfg.aurHelper != "auto" || !cfg.includeFlatpak || !cfg.hideWhenZero || cfg.notify {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.updateMethod != "terminal" {
		t.Fatalf("updateMethod = %q, want terminal", cfg.updateMethod)
	}
}
