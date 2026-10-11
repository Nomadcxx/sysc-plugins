package main

import (
	"context"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/panel"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/store"
)

type fakeSource struct{ running map[string]time.Time }

func (f fakeSource) Name() string                                { return "fake" }
func (f fakeSource) List(context.Context) ([]source.Game, error) { return nil, nil }
func (f fakeSource) Launch(context.Context, source.Game) error   { return nil }
func (f fakeSource) Stop(context.Context, source.Game) error     { return nil }
func (f fakeSource) Running(context.Context, []source.Game) (map[string]time.Time, error) {
	return f.running, nil
}

// Restart with a game no longer running: the persisted open session must be
// closed at startup instead of inflating playtime forever (#92).
func TestScanClosesStaleOpenSessionOnce(t *testing.T) {
	now := time.Now()
	s := &session{
		env:      environment{now: func() time.Time { return now }},
		src:      fakeSource{running: map[string]time.Time{}},
		machine:  panel.NewMachine(),
		sessions: store.Log{"g1": {{Start: now.Add(-72 * time.Hour)}}},
	}
	s.scan(context.Background(), map[string]time.Time{})

	sessions := s.sessions["g1"]
	if got := sessions[len(sessions)-1].End; got.IsZero() {
		t.Fatal("stale open session was not closed at startup")
	} else if !got.Equal(now) {
		t.Fatalf("stale session closed at %v, want restore time %v", got, now)
	}

	// A fresh launch then opens a NEW session, not nothing.
	s.src = fakeSource{running: map[string]time.Time{"g1": now}}
	s.scan(context.Background(), map[string]time.Time{"g1": now})
	if got := s.sessions["g1"]; len(got) != 2 || !got[1].End.IsZero() {
		t.Fatalf("sessions = %+v, want a fresh open session after the stale one", got)
	}
}

// With no bar widget and the panel closed, polling currently stops while a
// game runs, so its quit is noticed late or never (#92).
func TestDesiredPollKeepsPollingWhileRunning(t *testing.T) {
	now := time.Now()
	s := &session{
		running: map[string]time.Time{"g1": now},
		machine: panel.NewMachine(),
	}
	if got := s.desiredPoll(); got == 0 {
		t.Fatal("desiredPoll = 0 while a game is running; session end would never be seen")
	}
}

func TestStaleRefreshCannotReplaceLibrary(t *testing.T) {
	cached := []source.Game{{ID: "old", Name: "Cached"}}
	s := &session{refreshGeneration: 2, games: cached}
	if s.applyRefresh(context.Background(), refreshResult{
		generation: 1,
		requestID:  1,
		stage:      refreshLibrary,
		done:       true,
		games:      []source.Game{{ID: "new", Name: "Stale"}},
	}) {
		t.Fatal("stale refresh was applied")
	}
	if len(s.games) != 1 || s.games[0].ID != "old" {
		t.Fatalf("games = %+v, want cached generation", s.games)
	}
}

func TestRefreshRequestsKeepOnlyNewestQueuedWork(t *testing.T) {
	s := &session{
		src:         fakeSource{},
		settings:    defaultSettings(),
		refreshJobs: make(chan refreshJob, 1),
	}
	s.requestRefresh(false)
	s.requestRefresh(false)
	if len(s.refreshJobs) != 1 {
		t.Fatalf("queued refreshes = %d, want one", len(s.refreshJobs))
	}
	if got := (<-s.refreshJobs).requestID; got != 2 {
		t.Fatalf("queued request ID = %d, want latest ID 2", got)
	}
}
