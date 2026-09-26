package protonvpn

import (
	"testing"
	"time"
)

func TestConnectDeadline(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &Machine{Now: func() time.Time { return now }}
	m.StartConnect()
	if m.Snapshot().Phase != PhaseConnecting {
		t.Fatal("want connecting")
	}
	now = now.Add(19 * time.Second)
	if m.TransitionExpired() {
		t.Fatal("expired early")
	}
	now = now.Add(2 * time.Second)
	if !m.TransitionExpired() {
		t.Fatal("want expired after 20s")
	}
	m.SetStatus(Status{Phase: PhaseError})
	if m.Snapshot().Phase != PhaseError {
		t.Fatal("want error")
	}
}

func TestTrafficRates(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &Machine{Now: func() time.Time { return now }}
	m.SampleTraffic(1000, 500, true) // baseline
	now = now.Add(time.Second)
	m.SampleTraffic(2000, 1500, true)
	s := m.Snapshot()
	if s.RxRate != 1000 || s.TxRate != 1000 {
		t.Fatalf("got %v/%v", s.RxRate, s.TxRate)
	}
	if s.RxTotal != 2000 || s.TxTotal != 1500 {
		t.Fatalf("totals %v/%v", s.RxTotal, s.TxTotal)
	}
}

func TestTrafficStopsWhenInterfaceVanishes(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &Machine{Now: func() time.Time { return now }}
	m.SampleTraffic(1000, 500, true)
	now = now.Add(time.Second)
	m.SampleTraffic(0, 0, false) // tunnel gone: rates zero, totals frozen
	s := m.Snapshot()
	if s.RxRate != 0 || s.TxRate != 0 {
		t.Fatal("want zero rates")
	}
	if s.RxTotal != 1000 {
		t.Fatalf("totals frozen at %v", s.RxTotal)
	}
}

func TestErrorClearsOnNextTransition(t *testing.T) {
	m := &Machine{Now: func() time.Time { return time.Unix(1000, 0) }}
	m.Fail("Tunnel setup failed")
	if m.Snapshot().Err == "" {
		t.Fatal("want detail")
	}
	m.StartConnect()
	if m.Snapshot().Err != "" {
		t.Fatal("want detail cleared")
	}
}
