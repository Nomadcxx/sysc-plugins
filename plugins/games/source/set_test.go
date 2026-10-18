package source

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubSource struct {
	name     string
	games    []Game
	running  map[string]time.Time
	err      error
	launched []Game
	stopped  []Game
	ranGames []Game
}

func (s *stubSource) Name() string { return s.name }
func (s *stubSource) List(context.Context) ([]Game, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.games, nil
}
func (s *stubSource) Launch(_ context.Context, g Game) error {
	s.launched = append(s.launched, g)
	return nil
}
func (s *stubSource) Stop(_ context.Context, g Game) error {
	s.stopped = append(s.stopped, g)
	return nil
}
func (s *stubSource) Running(_ context.Context, games []Game) (map[string]time.Time, error) {
	s.ranGames = append(s.ranGames, games...)
	return s.running, nil
}

func TestKeyEscapesAndSplits(t *testing.T) {
	cases := []struct{ src, id string }{
		{"lutris", "7"},
		{"heroic", "My Game (v2).exe"},
		{"faugus", "3f2b-c0ffee"},
		{"steam", "730"},
		{"lutris", "a:b/c\\d"},
	}
	for _, c := range cases {
		key := Key(c.src, c.id)
		gotSrc, gotID := Split(key)
		if gotSrc != c.src || gotID != c.id {
			t.Errorf("Split(Key(%q, %q)) = %q, %q; want round trip", c.src, c.id, gotSrc, gotID)
		}
		for i := 0; i < len(key); i++ {
			ch := key[i]
			if ch == ':' || ch == '%' { // ':' separates, '%' is the escape marker
				continue
			}
			if !isSafe(ch) {
				t.Fatalf("Key(%q, %q) = %q; unsafe byte %q", c.src, c.id, key, ch)
			}
		}
	}
	if got, want := Key("heroic", "My Game (v2)"), "heroic:My%20Game%20%28v2%29"; got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
}

func TestSetDedupesByIdentity(t *testing.T) {
	steam := &stubSource{name: "steam", games: []Game{
		{ID: "730", Name: "Counter-Strike 2", Identity: "steam:730"},
	}}
	lutris := &stubSource{name: "lutris", games: []Game{
		{ID: "7", Name: "Counter-Strike 2 (imported)", Identity: "steam:730"},
	}}
	set := NewSet(steam, lutris)
	games, err := set.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 {
		t.Fatalf("games = %d, want one merged card: %+v", len(games), games)
	}
	if games[0].Source != "steam" || games[0].ID != "steam:730" {
		t.Fatalf("winner = %s %s, want the preferred source's copy", games[0].Source, games[0].ID)
	}
	if len(games[0].OtherSources) != 1 || games[0].OtherSources[0] != "lutris" {
		t.Fatalf("OtherSources = %v, want [lutris]", games[0].OtherSources)
	}
}

func TestSetPreferenceOrderDecides(t *testing.T) {
	lutris := &stubSource{name: "lutris", games: []Game{
		{ID: "7", Name: "Counter-Strike 2", Identity: "steam:730"},
	}}
	steam := &stubSource{name: "steam", games: []Game{
		{ID: "730", Name: "Counter-Strike 2", Identity: "steam:730"},
	}}
	games, err := NewSet(lutris, steam).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].Source != "lutris" {
		t.Fatalf("games = %+v, want lutris first because it is first in the set", games)
	}
}

func TestSetTitleFallbackMergesIdentitylessCopies(t *testing.T) {
	lutris := &stubSource{name: "lutris", games: []Game{
		{ID: "9", Name: "Hades   II", Identity: "steam:1145360"},
	}}
	heroic := &stubSource{name: "heroic", games: []Game{
		// No identity: a sideloaded copy of the same title.
		{ID: "hades2", Name: "hades ii"},
	}}
	games, err := NewSet(lutris, heroic).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 {
		t.Fatalf("games = %d, want the identity-less copy folded in: %+v", len(games), games)
	}
	if games[0].Source != "lutris" || len(games[0].OtherSources) != 1 || games[0].OtherSources[0] != "heroic" {
		t.Fatalf("merged card = %+v", games[0])
	}
}

func TestSetDoesNotMergeConflictingIdentities(t *testing.T) {
	steam := &stubSource{name: "steam", games: []Game{
		{ID: "1", Name: "Doom", Identity: "steam:1"},
	}}
	lutris := &stubSource{name: "lutris", games: []Game{
		{ID: "2", Name: "Doom", Identity: "steam:2"},
	}}
	games, err := NewSet(steam, lutris).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %d, want both kept (provably different store ids): %+v", len(games), games)
	}
}

func TestSetDoesNotMergeConflictingIdentitiesAfterTitleFallback(t *testing.T) {
	heroic := &stubSource{name: "heroic", games: []Game{{ID: "side", Name: "Portal", Source: "heroic"}}}
	steam := &stubSource{name: "steam", games: []Game{{ID: "620", Name: "Portal", Identity: "steam:620"}}}
	lutris := &stubSource{name: "lutris", games: []Game{{ID: "7", Name: "Portal", Identity: "epic:portal"}}}

	games, err := NewSet(heroic, steam, lutris).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %+v, want the identity-less copy merged with one store copy and the conflicting store copy kept separate", games)
	}
	if games[0].Identity != "steam:620" || len(games[0].OtherSources) != 1 || games[0].OtherSources[0] != "steam" {
		t.Fatalf("identity-less card did not retain the first strong identity and source: %+v", games[0])
	}
	if games[1].Identity != "epic:portal" || games[1].Source != "lutris" {
		t.Fatalf("conflicting store copy was not kept: %+v", games[1])
	}
}

func TestSetKeepsSameTitleWithinOneSource(t *testing.T) {
	lutris := &stubSource{name: "lutris", games: []Game{
		{ID: "1", Name: "Portal"},
		{ID: "2", Name: "Portal"},
	}}
	games, err := NewSet(lutris).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %d, want both same-named installs kept: %+v", len(games), games)
	}
}

func TestSetLaunchAndStopRouteByName(t *testing.T) {
	steam := &stubSource{name: "steam"}
	lutris := &stubSource{name: "lutris"}
	set := NewSet(steam, lutris)
	g := Game{ID: Key("lutris", "42"), Name: "Hades", Source: "lutris"}
	if err := set.Launch(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if len(lutris.launched) != 1 || lutris.launched[0].ID != "42" {
		t.Fatalf("lutris launched = %+v, want the un-namespaced id", lutris.launched)
	}
	if len(steam.launched) != 0 {
		t.Fatalf("steam must not see the launch")
	}
	if err := set.Stop(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if len(lutris.stopped) != 1 || lutris.stopped[0].ID != "42" {
		t.Fatalf("lutris stopped = %+v", lutris.stopped)
	}
}

func TestSetUnknownSourceFails(t *testing.T) {
	set := NewSet(&stubSource{name: "lutris"})
	if err := set.Launch(context.Background(), Game{ID: "heroic:x", Source: "heroic"}); err == nil {
		t.Fatal("launching through a library that is not enabled must fail")
	}
	if err := set.Launch(context.Background(), Game{ID: "orphan"}); err == nil {
		t.Fatal("launching a game with no owning library must fail")
	}
}

func TestSetRunningNamespacesKeys(t *testing.T) {
	now := time.Now()
	steam := &stubSource{name: "steam", running: map[string]time.Time{"730": now}}
	lutris := &stubSource{name: "lutris", running: map[string]time.Time{"7": now.Add(-time.Hour)}}
	set := NewSet(steam, lutris)
	got, err := set.Running(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["steam:730"].Equal(now) || !got["lutris:7"].Equal(now.Add(-time.Hour)) {
		t.Fatalf("running = %+v", got)
	}
}

// Each library sees only its own games, with the namespace stripped, so its
// Running works on the local ids it listed.
func TestSetRunningHandsEachLibraryItsOwnGames(t *testing.T) {
	steam := &stubSource{name: "steam"}
	lutris := &stubSource{name: "lutris"}
	set := NewSet(steam, lutris)
	games := []Game{
		{ID: Key("steam", "730"), Source: "steam"},
		{ID: Key("lutris", "7"), Source: "lutris"},
		{ID: Key("lutris", "My Game (v2)"), Source: "lutris"},
	}
	if _, err := set.Running(context.Background(), games); err != nil {
		t.Fatal(err)
	}
	if len(steam.ranGames) != 1 || steam.ranGames[0].ID != "730" {
		t.Fatalf("steam saw %+v", steam.ranGames)
	}
	if len(lutris.ranGames) != 2 || lutris.ranGames[0].ID != "7" || lutris.ranGames[1].ID != "My Game (v2)" {
		t.Fatalf("lutris saw %+v", lutris.ranGames)
	}
}

func TestSetRunningMapsMergedCopyToCanonicalGame(t *testing.T) {
	start := time.Now()
	steam := &stubSource{name: "steam", games: []Game{{ID: "620", Name: "Portal 2", Identity: "steam:620"}}}
	heroic := &stubSource{
		name:    "heroic",
		games:   []Game{{ID: "portal-2", Name: "Portal 2", Identity: "steam:620"}},
		running: map[string]time.Time{"portal-2": start},
	}
	set := NewSet(steam, heroic)
	games, err := set.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	running, err := set.Running(context.Background(), games)
	if err != nil {
		t.Fatal(err)
	}
	if got := running["steam:620"]; !got.Equal(start) {
		t.Fatalf("canonical running state = %v, want %v; running=%v", got, start, running)
	}
	if len(heroic.ranGames) != 1 || heroic.ranGames[0].ID != "portal-2" {
		t.Fatalf("Heroic received games %+v, want its source-local alias", heroic.ranGames)
	}
}

func TestSetToleratesOneFailingLibrary(t *testing.T) {
	broken := &stubSource{name: "heroic", err: errors.New("no config root")}
	lutris := &stubSource{name: "lutris", games: []Game{{ID: "1", Name: "Hades"}}}
	games, err := NewSet(broken, lutris).List(context.Background())
	if err != nil || len(games) != 1 {
		t.Fatalf("List = %v, %v; want the healthy library's games", games, err)
	}
	if _, err := NewSet(broken).List(context.Background()); err == nil {
		t.Fatal("every library failing must surface an error")
	}
}

func TestSetEmptyIsAnError(t *testing.T) {
	if _, err := NewSet().List(context.Background()); err == nil {
		t.Fatal("a set with no enabled libraries lists nothing and says why")
	}
}
