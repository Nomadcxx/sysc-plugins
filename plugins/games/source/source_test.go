package source

import (
	"context"
	"testing"
	"time"
)

type fakeSource struct{ games []Game }

func (f *fakeSource) Name() string                         { return "fake" }
func (f *fakeSource) List(context.Context) ([]Game, error) { return f.games, nil }
func (f *fakeSource) Launch(context.Context, Game) error   { return nil }
func (f *fakeSource) Stop(context.Context, Game) error     { return nil }
func (f *fakeSource) Running(context.Context) (map[string]time.Time, error) {
	return nil, nil
}

func TestFakeSatisfiesInterface(t *testing.T) {
	var s Source = &fakeSource{games: []Game{{ID: "7", Name: "Hades", Runner: "wine"}}}
	games, err := s.List(context.Background())
	if err != nil || len(games) != 1 || games[0].Name != "Hades" {
		t.Fatalf("List = %v, %v", games, err)
	}
}
