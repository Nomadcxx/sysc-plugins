package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

// The refresh worker publishes a listing and then asks every source what is
// running with that same listing. The session adopts the listing, so it has
// to take a copy of it: a cover path written into the session's games would
// land in the memory the worker is reading.

func TestTheSessionKeepsItsOwnCopyOfThePublishedListing(t *testing.T) {
	published := []source.Game{{ID: "lutris:hades", Name: "Hades"}}
	s := &session{}
	s.applyRefresh(context.Background(), refreshResult{
		generation: s.refreshGeneration,
		stage:      refreshLibrary,
		games:      published,
	})
	s.applyCover(coverDone{gameID: "lutris:hades", path: "/tmp/cover.png"})
	if published[0].CoverPath != "" {
		t.Fatalf("the session wrote %q into the listing the refresh worker published", published[0].CoverPath)
	}
	got := s.game("lutris:hades")
	if got == nil {
		t.Fatal("the session lost the game it published")
	}
	if got.CoverPath != "/tmp/cover.png" {
		t.Fatalf("cover path = %q, want %q", got.CoverPath, "/tmp/cover.png")
	}
}

// The race detector is the judge here: the worker reads the published
// listing while the session writes a cover path into its games. With the
// copy in place the two touch different memory and the run stays clean.
func TestTheSessionAndTheRefreshWorkerNeverTouchTheSameGame(t *testing.T) {
	published := []source.Game{{ID: "lutris:hades", Name: "Hades"}}
	stop := make(chan struct{})
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = published[0].CoverPath
		}
	}()
	s := &session{}
	s.applyRefresh(context.Background(), refreshResult{
		generation: s.refreshGeneration,
		stage:      refreshLibrary,
		games:      published,
	})
	for i := 0; i < 2000; i++ {
		s.applyCover(coverDone{gameID: "lutris:hades", path: fmt.Sprintf("/tmp/cover-%d.png", i)})
	}
	close(stop)
	<-reading
}
