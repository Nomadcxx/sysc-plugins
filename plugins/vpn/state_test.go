package vpn

import (
	"context"
	"errors"
	"testing"
	"time"
)

func readyMachine(t *testing.T) (*Machine, time.Time) {
	t.Helper()
	m := NewMachine(true, true)
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for _, p := range []Provider{Mullvad, Obscura} {
		gen, ok := m.BeginRead(p)
		if !ok {
			t.Fatal("read refused")
		}
		m.ApplyRead(p, gen, Observation{Phase: Disconnected, Traffic: TrafficNotBlocked}, nil, now)
	}
	return m, now
}

func TestStateRejectsOldReadAfterMutation(t *testing.T) {
	m, now := readyMachine(t)
	old, _ := m.BeginRead(Mullvad)
	gen, err := m.BeginMutation(Mullvad, Action{Kind: "connect"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.BeginMutation(Obscura, Action{Kind: "connect"}, now); err == nil {
		t.Fatal("overlapping mutation")
	}
	m.CompleteMutation(Mullvad, gen, nil, now)
	if m.ApplyRead(Mullvad, old, Observation{Phase: Disconnected}, nil, now) {
		t.Fatal("old read accepted")
	}
	fresh, ok := m.BeginRead(Mullvad)
	if !ok {
		t.Fatal("stale read kept slot")
	}
	m.ApplyRead(Mullvad, fresh, Observation{Phase: Connected}, nil, now)
	if m.State(Mullvad).Observed.Phase != Connected || m.Pending != "" {
		t.Fatalf("%+v", m)
	}
}

func TestStateSelectionIsNotAMutation(t *testing.T) {
	m, now := readyMachine(t)
	gen, _ := m.BeginRead(Mullvad)
	if err := m.Select(Obscura); err != nil {
		t.Fatal(err)
	}
	m.ApplyRead(Mullvad, gen, Observation{Phase: Connected}, nil, now)
	if m.Selected != Obscura || m.Busy || m.State(Obscura).Observed.Phase != Disconnected {
		t.Fatal("selection changed controller")
	}
	if err := m.Select("bad"); err == nil {
		t.Fatal("bad selection")
	}
}

func TestStateErrorsDoNotInventTunnelState(t *testing.T) {
	m, now := readyMachine(t)
	gen, _ := m.BeginRead(Mullvad)
	m.ApplyRead(Mullvad, gen, Observation{Phase: Connected}, nil, now)
	op, err := m.BeginMutation(Mullvad, Action{Kind: "setting", Key: "lan", Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	m.CompleteMutation(Mullvad, op, context.DeadlineExceeded, now)
	if m.State(Mullvad).Observed.Phase != Connected || m.ActionError == "" || m.Busy {
		t.Fatal("lost connected observation")
	}
	if _, err := m.BeginMutation(Mullvad, Action{Kind: "disconnect"}, now); err == nil {
		t.Fatal("mutation from stale observation")
	}
	read, _ := m.BeginRead(Mullvad)
	m.ApplyRead(Mullvad, read, Observation{}, errors.New("offline"), now)
	if m.State(Mullvad).Observed.Phase != Connected || m.State(Mullvad).Fresh(now) {
		t.Fatal("read failure fabricated state")
	}
}

func TestStatePendingExpiresWithoutDisconnecting(t *testing.T) {
	m, now := readyMachine(t)
	gen, err := m.BeginMutation(Mullvad, Action{Kind: "connect"}, now)
	if err != nil {
		t.Fatal(err)
	}
	m.CompleteMutation(Mullvad, gen, nil, now)
	if m.Pending != Connected {
		t.Fatal("no pending target")
	}
	if !m.Expire(now.Add(time.Minute)) || m.State(Mullvad).Observed.Phase != Disconnected || m.ActionError == "" {
		t.Fatal("expiry invented state")
	}
}

func TestConflictPolicy(t *testing.T) {
	for _, o := range []Observation{
		{Phase: Connected}, {Phase: Connecting}, {Phase: Disconnecting}, {Phase: TunnelError},
		{Phase: Disconnected, Traffic: TrafficBlocked},
	} {
		if err := checkOther(Mullvad, o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	if err := checkOther(Obscura, Observation{Phase: Disconnected, Traffic: TrafficUnknown}); err != nil {
		t.Fatal(err)
	}
}

func TestLocationConnectWaitsForConnected(t *testing.T) {
	m, now := readyMachine(t)
	gen, err := m.BeginMutation(Mullvad, Action{Kind: "location", Location: "se::", Connect: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	m.CompleteMutation(Mullvad, gen, nil, now)
	if m.Pending != Connected {
		t.Fatalf("pending %q", m.Pending)
	}
	st := m.State(Mullvad)
	st.Observed.Phase, st.ReadError = Connected, ""
	m.Providers[Mullvad], m.Pending = st, ""
	if _, err := m.BeginMutation(Mullvad, Action{Kind: "location", Location: "se::", Connect: true}, now); err == nil {
		t.Fatal("connect-on-select accepted while already connected")
	}
}

func TestTransitionAndPreferInstalled(t *testing.T) {
	m, now := readyMachine(t)
	gen, err := m.BeginMutation(Mullvad, Action{Kind: "connect"}, now)
	if err != nil || m.Transition(Mullvad) != Connecting || m.Transition(Obscura) != "" {
		t.Fatalf("busy connect: %q %v", m.Transition(Mullvad), err)
	}
	m.CompleteMutation(Mullvad, gen, nil, now)
	if m.Transition(Mullvad) != Connecting {
		t.Fatalf("pending connect: %q", m.Transition(Mullvad))
	}
	m.Pending = ""
	gen, _ = m.BeginMutation(Mullvad, Action{Kind: "setting", Key: "lan", Enabled: true}, now)
	if m.Transition(Mullvad) != "" {
		t.Fatal("a setting change is not a tunnel transition")
	}
	m.CompleteMutation(Mullvad, gen, nil, now)
	m = NewMachine(false, true)
	_ = m.Select(Mullvad)
	m.PreferInstalled()
	if m.Selected != Obscura {
		t.Fatalf("selected %s with only Obscura installed", m.Selected)
	}
}
