// Package vpn implements the combined Mullvad and Obscura controller.
package vpn

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

type Provider string

const (
	Mullvad Provider = "mullvad"
	Obscura Provider = "obscura"
)

func (p Provider) Valid() bool { return p == Mullvad || p == Obscura }
func (p Provider) Name() string {
	if p == Obscura {
		return "Obscura"
	}
	if p == Mullvad {
		return "Mullvad"
	}
	return "VPN"
}
func (p Provider) Other() Provider {
	if p == Mullvad {
		return Obscura
	}
	return Mullvad
}

type Phase string

const (
	Disconnected  Phase = "disconnected"
	Connecting    Phase = "connecting"
	Connected     Phase = "connected"
	Disconnecting Phase = "disconnecting"
	TunnelError   Phase = "error"
)

type TrafficState string

const (
	TrafficUnknown    TrafficState = "unknown"
	TrafficBlocked    TrafficState = "blocked"
	TrafficNotBlocked TrafficState = "not-blocked"
)

type Observation struct {
	Phase                                               Phase
	Traffic                                             TrafficState
	City, CityCode, Country, Exit, Entry, IP, Transport string
	Detail                                              string
}

// waitingForStatus is a provider's ReadError until its first read returns.
const waitingForStatus = "Waiting for status"

type ProviderState struct {
	Installed      bool
	Observed       Observation
	HasObservation bool
	LastSuccess    time.Time
	ReadError      string
}

func (s ProviderState) Fresh(now time.Time) bool {
	return s.Installed && s.HasObservation && s.ReadError == "" && now.Sub(s.LastSuccess) <= 10*time.Second
}

type Action struct {
	Kind          string // connect | disconnect | location | setting
	Location, Key string
	Enabled       bool
	Connect       bool // location: connect after saving it (from Disconnected)
}

// Machine is owned by the wire event loop. It never starts a goroutine or a
// command; workers return tagged results for this owner to apply.
type Machine struct {
	Selected         Provider
	Providers        map[Provider]ProviderState
	Busy             bool
	Pending          Phase
	ActionError      string
	epochs           map[Provider]uint64
	reads            map[Provider]uint64
	activeProvider   Provider
	activeGeneration uint64
	activeAction     Action
	pendingProvider  Provider
	pendingUntil     time.Time
}

func NewMachine(mullvad, obscura bool) *Machine {
	m := &Machine{Selected: Mullvad, Providers: map[Provider]ProviderState{}, epochs: map[Provider]uint64{}, reads: map[Provider]uint64{}}
	m.Providers[Mullvad] = ProviderState{Installed: mullvad, ReadError: waitingForStatus}
	m.Providers[Obscura] = ProviderState{Installed: obscura, ReadError: waitingForStatus}
	if !mullvad && obscura {
		m.Selected = Obscura
	}
	return m
}

func (m *Machine) State(p Provider) ProviderState { return m.Providers[p] }

func (m *Machine) SetInstalled(p Provider, on bool) {
	s := m.State(p)
	if s.Installed != on {
		m.epochs[p]++
		s = ProviderState{Installed: on, ReadError: waitingForStatus}
		m.Providers[p] = s
	}
}

func (m *Machine) Select(p Provider) error {
	if !p.Valid() {
		return errors.New("unknown VPN provider")
	}
	m.Selected = p
	m.ActionError = ""
	return nil
}

func (m *Machine) BeginRead(p Provider) (uint64, bool) {
	if !m.State(p).Installed {
		return 0, false
	}
	if _, active := m.reads[p]; active {
		return 0, false
	}
	gen := m.epochs[p]
	m.reads[p] = gen
	return gen, true
}

func (m *Machine) ApplyRead(p Provider, gen uint64, o Observation, err error, now time.Time) bool {
	active, exists := m.reads[p]
	if !exists || active != gen {
		return false
	}
	delete(m.reads, p)
	if m.epochs[p] != gen {
		return false
	}
	s := m.State(p)
	if err != nil {
		s.ReadError = err.Error()
	} else {
		s.Observed, s.HasObservation, s.LastSuccess, s.ReadError = o, true, now, ""
		if m.pendingProvider == p && m.Pending == o.Phase {
			m.Pending = ""
		}
		if m.ActionError != "" && p == Obscura && p == m.activeProvider &&
			m.activeAction.Kind == "location" && o.matchesObscuraLocation(m.activeAction.Location) {
			m.ActionError = ""
		}
	}
	m.Providers[p] = s
	return true
}

func (m *Machine) BeginMutation(p Provider, a Action, now time.Time) (uint64, error) {
	if m.Busy || m.Pending != "" {
		return 0, errors.New("Wait for the current VPN action")
	}
	if p != m.Selected || !m.State(p).Fresh(now) {
		return 0, errors.New("Refresh this provider's status first")
	}
	phase := m.State(p).Observed.Phase
	switch a.Kind {
	case "connect":
		if phase != Disconnected {
			return 0, errors.New("VPN is not disconnected")
		}
	case "disconnect":
		if phase != Connected && phase != Connecting && phase != TunnelError {
			return 0, errors.New("VPN is not ready to disconnect")
		}
	case "location", "setting":
		if phase != Connected && phase != Disconnected {
			return 0, errors.New("Wait for the VPN transition")
		}
		if a.Connect && phase != Disconnected {
			return 0, errors.New("VPN is not disconnected")
		}
	default:
		return 0, errors.New("unknown VPN action")
	}
	m.epochs[p]++
	m.Busy, m.ActionError = true, ""
	m.activeProvider, m.activeGeneration, m.activeAction = p, m.epochs[p], a
	return m.activeGeneration, nil
}

func (m *Machine) CompleteMutation(p Provider, gen uint64, err error, now time.Time) bool {
	if !m.Busy || p != m.activeProvider || gen != m.activeGeneration {
		return false
	}
	m.Busy = false
	// A read started while the command was running also predates its result.
	m.epochs[p]++
	s := m.State(p)
	s.ReadError = "Refreshing status after action"
	m.Providers[p] = s
	if err != nil {
		m.ActionError = err.Error()
		return true
	}
	var target Phase
	switch m.activeAction.Kind {
	case "connect":
		target = Connected
	case "disconnect":
		target = Disconnected
	case "location":
		if s.Observed.Phase == Connected || m.activeAction.Connect {
			target = Connected
		}
	}
	if target != "" {
		m.Pending, m.pendingProvider, m.pendingUntil = target, p, now.Add(20*time.Second)
	}
	return true
}

// Transition is the move under way for p, or "" when it is at rest: a
// connection command still running, or a target the poll has not yet seen.
// Setting changes are not tunnel transitions.
func (m *Machine) Transition(p Provider) Phase {
	if m.Busy && m.activeProvider == p {
		switch a := m.activeAction; {
		case a.Kind == "connect", a.Kind == "location" && (a.Connect || m.State(p).Observed.Phase == Connected):
			return Connecting
		case a.Kind == "disconnect":
			return Disconnecting
		}
	}
	if m.Pending != "" && m.pendingProvider == p {
		if m.Pending == Connected {
			return Connecting
		}
		return Disconnecting
	}
	return ""
}

// ActiveAction is the action whose command is still running, if any.
func (m *Machine) ActiveAction() (Action, bool) {
	if !m.Busy {
		return Action{}, false
	}
	return m.activeAction, true
}

// PreferInstalled moves the selection off a provider that is not installed
// when the other one is, without saving it: the saved choice still wins once
// that provider is installed again.
func (m *Machine) PreferInstalled() {
	if !m.State(m.Selected).Installed && m.State(m.Selected.Other()).Installed {
		m.Selected = m.Selected.Other()
	}
}

func (m *Machine) Expire(now time.Time) bool {
	if m.Pending == "" || now.Before(m.pendingUntil) {
		return false
	}
	m.Pending = ""
	m.ActionError = "Requested state not confirmed. Refresh before trying again."
	return true
}

func display(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	r := []rune(s)
	if len(r) > 128 {
		return string(r[:127]) + "…"
	}
	return s
}
