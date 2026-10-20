package main

import (
	"context"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/protonvpn"
)

func gatedSession(tunnelUp bool) *session {
	s := &session{
		machine:  &protonvpn.Machine{},
		polls:    make(chan pollRequest, 4),
		tunnelUp: func() bool { return tunnelUp },
	}
	s.settings = defaultSettings() // refreshSeconds = 60
	s.hasCLI = true
	return s
}

// tick once and drain the poll it is always allowed to make.
func prime(s *session, at time.Time) {
	s.tick(context.Background(), at)
	<-s.polls
}

func TestStatusPollBacksOffWhileDisconnected(t *testing.T) {
	s := gatedSession(false)
	base := time.Now()
	prime(s, base)

	s.tick(context.Background(), base.Add(6*time.Second))
	if len(s.polls) != 0 {
		t.Fatal("status polled 6s after the last one while the tunnel was absent; the 30s floor was not applied")
	}

	s.tick(context.Background(), base.Add(36*time.Second))
	if len(s.polls) != 1 {
		t.Fatal("status never polled again after the 30s floor elapsed")
	}
}

func TestStatusPollBacksOffWhileConnected(t *testing.T) {
	s := gatedSession(true)
	s.machine.SetStatus(protonvpn.Status{Phase: protonvpn.PhaseConnected})
	s.settings.trafficMonitoring = false
	base := time.Now()
	s.lastLink = base
	s.cliJobs = make(chan cliJob, 1)

	polls := 0
	for second := 0; second < 3600; second++ {
		s.tick(context.Background(), base.Add(time.Duration(second)*time.Second))
		for len(s.polls) > 0 {
			<-s.polls
			polls++
		}
		for len(s.cliJobs) > 0 {
			<-s.cliJobs // Don't run nmcli in this scheduler test.
		}
	}
	if polls != 60 {
		t.Fatalf("connected status polls in one hour = %d; want 60 at the default", polls)
	}
}

func TestStatusPollUsesConfiguredConnectedInterval(t *testing.T) {
	s := gatedSession(true)
	s.machine.SetStatus(protonvpn.Status{Phase: protonvpn.PhaseConnected})
	s.settings.refreshSeconds = 15
	s.settings.trafficMonitoring = false
	base := time.Now()
	s.lastLink = base
	s.cliJobs = make(chan cliJob, 1)
	prime(s, base)

	s.tick(context.Background(), base.Add(14*time.Second))
	if len(s.polls) != 0 {
		t.Fatal("status polled before the configured 15s connected interval")
	}
	s.tick(context.Background(), base.Add(15*time.Second))
	if len(s.polls) != 1 {
		t.Fatal("status did not poll at the configured 15s connected interval")
	}
}

func TestTunnelLossForcesImmediateStatusPoll(t *testing.T) {
	up := true
	s := gatedSession(true)
	s.tunnelUp = func() bool { return up }
	s.machine.SetStatus(protonvpn.Status{Phase: protonvpn.PhaseConnected})
	s.settings.trafficMonitoring = false
	base := time.Now()
	s.lastLink = base
	s.cliJobs = make(chan cliJob, 1)
	prime(s, base)

	up = false
	s.tick(context.Background(), base.Add(time.Second))
	if len(s.polls) != 1 {
		t.Fatal("status poll was not forced immediately after the tunnel disappeared")
	}
}

func TestTunnelAppearanceForcesImmediateStatusPoll(t *testing.T) {
	up := false
	s := gatedSession(false)
	s.tunnelUp = func() bool { return up }
	base := time.Now()
	prime(s, base)

	up = true
	s.tick(context.Background(), base.Add(time.Second))
	if len(s.polls) != 1 {
		t.Fatal("status poll was not forced immediately after the tunnel appeared")
	}
}

func TestTransitionStatusPollRemainsEverySecond(t *testing.T) {
	for _, tc := range []struct {
		name     string
		phase    protonvpn.Phase
		tunnelUp bool
	}{
		{name: "connecting with tunnel", phase: protonvpn.PhaseConnecting, tunnelUp: true},
		{name: "connecting without tunnel", phase: protonvpn.PhaseConnecting, tunnelUp: false},
		{name: "disconnecting with tunnel", phase: protonvpn.PhaseDisconnecting, tunnelUp: true},
		{name: "disconnecting without tunnel", phase: protonvpn.PhaseDisconnecting, tunnelUp: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := gatedSession(tc.tunnelUp)
			s.machine.SetStatus(protonvpn.Status{Phase: tc.phase})
			base := time.Now()
			prime(s, base)

			s.tick(context.Background(), base.Add(time.Second))
			if len(s.polls) != 1 {
				t.Fatal("transition status poll did not retain its 1s cadence")
			}
		})
	}
}

func TestRefreshSecondsDefaultAndClamp(t *testing.T) {
	s := defaultSettings()
	if s.refreshSeconds != 60 {
		t.Fatalf("default refresh_seconds = %d; want 60", s.refreshSeconds)
	}
	s.apply(map[string]any{"refresh_seconds": float64(2)})
	if s.refreshSeconds != 15 {
		t.Fatalf("minimum refresh_seconds = %d; want 15", s.refreshSeconds)
	}
	s.apply(map[string]any{"refresh_seconds": float64(90)})
	if s.refreshSeconds != 60 {
		t.Fatalf("maximum refresh_seconds = %d; want 60", s.refreshSeconds)
	}
}
