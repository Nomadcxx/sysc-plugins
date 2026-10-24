package main

import (
	"context"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

// TestTheSessionOwnsTheListingItAdopts guards the race between the session
// and the refresh worker. The worker publishes a listing and keeps reading it
// while it asks every source what is running; the session then writes a cover
// path into a game of that listing. When the session adopts the worker's
// slice, that write lands in memory the worker reads, and the race detector
// reports it.
func TestTheSessionOwnsTheListingItAdopts(t *testing.T) {
	s := &session{}
	published := []source.Game{{ID: "a1", Name: "Alpha"}, {ID: "b1", Name: "Beta"}}

	s.applyRefresh(context.Background(), refreshResult{
		generation: s.refreshGeneration,
		stage:      refreshLibrary,
		games:      published,
	})
	if len(s.games) != len(published) {
		t.Fatalf("the session holds %d games, want %d", len(s.games), len(published))
	}

	game := s.game("a1")
	if game == nil {
		t.Fatal("the session did not adopt the published game")
	}
	game.CoverPath = "/tmp/cover.png"
	if published[0].CoverPath != "" {
		t.Fatalf("the session wrote a cover path into the worker's listing: %q", published[0].CoverPath)
	}
}
