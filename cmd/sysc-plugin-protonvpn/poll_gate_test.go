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
	s.settings = defaultSettings() // refreshSeconds = 5
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

func TestStatusPollKeepsCadenceWhenTunnelPresent(t *testing.T) {
	s := gatedSession(true)
	base := time.Now()
	prime(s, base)

	s.tick(context.Background(), base.Add(6*time.Second))
	if len(s.polls) != 1 {
		t.Fatal("tunnel present must honour refresh_seconds exactly")
	}
}
