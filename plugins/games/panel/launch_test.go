package panel

import (
	"testing"
	"time"
)

func mkt() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }

func TestLaunchHappyPath(t *testing.T) {
	now := mkt()
	m := NewMachine()
	if m.Phase("7") != PhaseIdle {
		t.Fatal("new machine not idle")
	}
	m.Request("7", now)
	if m.Phase("7") != PhaseLaunching {
		t.Fatal("request did not launch")
	}
	// too early to fail
	if evs := m.Observe(map[string]bool{}, now.Add(time.Second)); len(evs) != 0 {
		t.Fatalf("premature events: %v", evs)
	}
	evs := m.Observe(map[string]bool{"7": true}, now.Add(3*time.Second))
	if len(evs) != 1 || evs[0].Kind != EventStarted || evs[0].GameID != "7" {
		t.Fatalf("start events: %+v", evs)
	}
	if evs[0].At != now {
		t.Fatalf("session should start at request time, got %v", evs[0].At)
	}
	if m.Phase("7") != PhaseRunning {
		t.Fatal("not running")
	}
	// still running → silent
	if evs := m.Observe(map[string]bool{"7": true}, now.Add(time.Minute)); len(evs) != 0 {
		t.Fatalf("running poll emitted %v", evs)
	}
	evs = m.Observe(map[string]bool{}, now.Add(10*time.Minute))
	if len(evs) != 1 || evs[0].Kind != EventStopped || !evs[0].At.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("stop events: %+v", evs)
	}
	if m.Phase("7") != PhaseIdle {
		t.Fatal("not back to idle")
	}
}

func TestLaunchTimeoutFailsOnce(t *testing.T) {
	now := mkt()
	m := NewMachine()
	m.Request("7", now)
	evs := m.Observe(map[string]bool{}, now.Add(LaunchTimeout+time.Second))
	if len(evs) != 1 || evs[0].Kind != EventFailed {
		t.Fatalf("want one failed event, got %+v", evs)
	}
	// failed is terminal until re-requested — no repeat notifications
	if evs := m.Observe(map[string]bool{}, now.Add(time.Minute)); len(evs) != 0 {
		t.Fatalf("failed repeated: %+v", evs)
	}
	m.Request("7", now) // retry allowed
	if m.Phase("7") != PhaseLaunching {
		t.Fatal("cannot relaunch after failure")
	}
}

func TestAdoptExternalRunning(t *testing.T) {
	now := mkt()
	m := NewMachine()
	m.MarkRunning("9")
	if evs := m.Observe(map[string]bool{"9": true}, now.Add(time.Minute)); len(evs) != 0 {
		t.Fatalf("adopted game emitted %v", evs)
	}
	evs := m.Observe(map[string]bool{}, now.Add(2*time.Minute))
	if len(evs) != 1 || evs[0].Kind != EventStopped {
		t.Fatalf("adopted stop missed: %+v", evs)
	}
}

// A slow start can outlast the timeout: the game shows up after the card said
// it failed. It is running then, and its session starts when it was asked for.
func TestLateAppearanceAfterFailureStarts(t *testing.T) {
	now := mkt()
	m := NewMachine()
	m.Request("7", now)
	m.Observe(map[string]bool{}, now.Add(LaunchTimeout+time.Second))
	evs := m.Observe(map[string]bool{"7": true}, now.Add(LaunchTimeout+5*time.Second))
	if len(evs) != 1 || evs[0].Kind != EventStarted || !evs[0].At.Equal(now) {
		t.Fatalf("want one started event at the request, got %+v", evs)
	}
	if m.Phase("7") != PhaseRunning {
		t.Fatalf("phase = %v, want running", m.Phase("7"))
	}
}

// Lutris may spend tens of seconds on a runtime or Proton before the game's
// process exists; the timeout must leave room for that.
func TestLaunchTimeoutAllowsASlowRunner(t *testing.T) {
	if LaunchTimeout < 45*time.Second {
		t.Fatalf("LaunchTimeout = %v; a Proton prefix start needs at least 45s", LaunchTimeout)
	}
}
