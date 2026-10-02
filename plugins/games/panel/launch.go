package panel

import (
	"time"
)

type LaunchPhase int

const (
	PhaseIdle LaunchPhase = iota
	PhaseLaunching
	PhaseRunning
	PhaseFailed
)

// LaunchTimeout bounds how long a launched game may stay invisible before the
// card reports failure. Lutris can spend tens of seconds preparing a runtime
// or a Proton prefix before the game's process exists, and 15s reported
// working launches as failed. A game that shows up later still starts.
const LaunchTimeout = 60 * time.Second

// Machine tracks per-game launch state. It is pure: the caller feeds it
// observations (Requested, Seen) with explicit timestamps and applies the
// returned Events.
type Machine struct {
	phases map[string]LaunchPhase
	t0     map[string]time.Time
}

func NewMachine() *Machine {
	return &Machine{phases: map[string]LaunchPhase{}, t0: map[string]time.Time{}}
}

type LaunchEvent struct {
	GameID string
	Kind   LaunchEventKind
	At     time.Time // session start (Running) or start of the failed attempt
}

type LaunchEventKind int

const (
	EventStarted LaunchEventKind = iota // proc seen: open a session
	EventFailed                         // launch never appeared
	EventStopped                        // proc gone: close the session
)

func (m *Machine) Phase(id string) LaunchPhase { return m.phases[id] }

// Request marks intent to launch (xdg-open already fired by the caller).
func (m *Machine) Request(id string, now time.Time) {
	if m.phases[id] == PhaseIdle || m.phases[id] == PhaseFailed {
		m.phases[id] = PhaseLaunching
		m.t0[id] = now
	}
}

// Observe feeds one poll's running-detection result and returns transitions.
func (m *Machine) Observe(running map[string]bool, now time.Time) []LaunchEvent {
	var out []LaunchEvent
	for id, ph := range m.phases {
		switch ph {
		case PhaseLaunching, PhaseFailed:
			if running[id] {
				m.phases[id] = PhaseRunning
				out = append(out, LaunchEvent{GameID: id, Kind: EventStarted, At: m.t0[id]})
			} else if ph == PhaseLaunching && now.Sub(m.t0[id]) > LaunchTimeout {
				m.phases[id] = PhaseFailed
				out = append(out, LaunchEvent{GameID: id, Kind: EventFailed, At: m.t0[id]})
			}
		case PhaseRunning:
			if !running[id] {
				m.phases[id] = PhaseIdle
				out = append(out, LaunchEvent{GameID: id, Kind: EventStopped, At: now})
			}
		}
	}
	return out
}

// MarkRunning adopts games already running when the plugin (re)starts, so the
// first scan does not fake a launch.
func (m *Machine) MarkRunning(id string) {
	m.phases[id] = PhaseRunning
}
