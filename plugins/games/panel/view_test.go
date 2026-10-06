package panel

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/store"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func games() []source.Game {
	return []source.Game{
		{ID: "1", Name: "Hades", Slug: "hades", Runner: "wine", Platform: "Windows", Installed: true, Year: "2020", PlaytimeSec: 3.5 * 3600},
		{ID: "2", Name: "Doom", Slug: "doom", Runner: "linux", Platform: "Linux", Installed: true, PlaytimeSec: 60},
		{ID: "3", Name: "Neverinstall", Slug: "ni", Runner: "wine", Installed: false},
	}
}

func baseState() State {
	return State{Now: now, All: games(), CacheDir: "/tmp/does-not-exist-ok",
		Prefs: store.Prefs{View: "library"}}
}

func find(t *testing.T, n *v1.Node, id string) *v1.Node {
	t.Helper()
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if f := find(t, c, id); f != nil {
			return f
		}
	}
	return nil
}

func collect(t *testing.T, n *v1.Node, pred func(*v1.Node) bool, acc *[]*v1.Node) {
	t.Helper()
	if pred(n) {
		*acc = append(*acc, n)
	}
	for _, c := range n.Children {
		collect(t, c, pred, acc)
	}
}

func TestTreeValidates(t *testing.T) {
	s := baseState()
	s.Selected = "1"
	if err := v1.Validate(BuildTree(s), v1.ViewPanel); err != nil {
		t.Fatalf("validate: %v", err)
	}
	s.Running = map[string]time.Time{"1": now.Add(-time.Hour)}
	if err := v1.Validate(BuildTree(s), v1.ViewPanel); err != nil {
		t.Fatalf("validate running: %v", err)
	}
}

func TestSegmentedOneSelected(t *testing.T) {
	root := BuildTree(baseState())
	if find(t, root, "section-library") == nil {
		t.Fatal("no section-library")
	}
	var sel []*v1.Node
	collect(t, root, func(n *v1.Node) bool { return n.Selected }, &sel)
	if len(sel) != 1 || sel[0].ID != "section-library" {
		t.Fatalf("want exactly one selected segmented button, got %d", len(sel))
	}
}

func TestSectionAndQueryFiltering(t *testing.T) {
	s := baseState()
	if got := Visible(s); len(got) != 3 {
		t.Fatalf("library shows all: %d", len(got))
	}
	s.HideUnavailable = true
	if got := Visible(s); len(got) != 2 {
		t.Fatalf("hide_unavailable: %d", len(got))
	}
	s.Prefs.Favorites = map[string]bool{"2": true}
	s.Prefs.View = "favorites"
	if got := Visible(s); len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("favorites: %+v", got)
	}
	s.Prefs.View = "hidden"
	s.Prefs.Hidden = map[string]bool{"1": true}
	if got := Visible(s); len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("hidden: %+v", got)
	}
	s.Prefs.View = "playing"
	s.Prefs.Hidden = nil
	s.Running = map[string]time.Time{"2": now}
	if got := Visible(s); len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("playing: %+v", got)
	}
	s = baseState()
	s.Query = "HAD"
	if got := Visible(s); len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("query case-insensitive: %+v", got)
	}
}

// The grid caps at maxCards so a huge library cannot build a huge tree, but
// the cap used to be invisible: the panel looked like the whole library while
// the bar tooltip counted every game. Say how many the grid left out.
func TestTruncatedLibrarySaysHowManyAreHidden(t *testing.T) {
	s := baseState()
	s.All = manyGames(maxCards + 15)

	if got := len(Visible(s)); got != maxCards {
		t.Fatalf("grid shows %d cards, want the %d cap", got, maxCards)
	}
	n := find(t, BuildTree(s), "game-overflow")
	if n == nil {
		t.Fatalf("panel hides %d games without saying so", 15)
	}
	if !strings.Contains(n.Text, "15") {
		t.Fatalf("overflow row %q does not name the hidden count", n.Text)
	}

	s.All = manyGames(maxCards)
	if n := find(t, BuildTree(s), "game-overflow"); n != nil {
		t.Fatalf("nothing is hidden at the cap, got overflow row %q", n.Text)
	}
}

func manyGames(n int) []source.Game {
	out := make([]source.Game, n)
	for i := range out {
		out[i] = source.Game{
			ID:        strconv.Itoa(i),
			Name:      fmt.Sprintf("Game %03d", i),
			Runner:    "linux",
			Installed: true,
		}
	}
	return out
}

func TestSortModes(t *testing.T) {
	s := baseState()
	s.All[0].LastPlayed = now.Add(-48 * time.Hour)
	s.All[1].LastPlayed = now.Add(-time.Hour)
	s.Prefs.Sort = "recent"
	if got := Visible(s); got[0].ID != "2" {
		t.Fatalf("recent sort: %s first", got[0].ID)
	}
	s.Prefs.Sort = "playtime"
	if got := Visible(s); got[0].ID != "1" {
		t.Fatalf("playtime sort: %s first", got[0].ID)
	}
}

func TestCardsFivePerRow(t *testing.T) {
	s := baseState()
	for i := 4; i <= 7; i++ {
		id := strconv.Itoa(i)
		s.All = append(s.All, source.Game{ID: id, Name: "Game " + id, Installed: true})
	}
	rows := find(t, BuildTree(s), "game-list").Children
	if len(rows) != 2 || len(rows[0].Children) != cardsPerRow || len(rows[1].Children) != 2 {
		t.Fatalf("7 games -> rows of 5 and 2, got %d rows", len(rows))
	}
	root := BuildTree(baseState())
	list := find(t, root, "game-list")
	if list == nil || list.Kind != v1.KindList {
		t.Fatal("no list")
	}
	card := find(t, root, "card-1")
	if card == nil || card.Kind != v1.KindButton {
		t.Fatal("card-1 missing/not button")
	}
	if len(card.Events) != 2 {
		t.Fatal("card needs activate+pointer")
	}
}

func TestDetailLaunchAndActions(t *testing.T) {
	s := baseState()
	s.Selected = "1"
	root := BuildTree(s)
	launch := find(t, root, "launch-1")
	if launch == nil || launch.Disabled || launch.Fill != "accent" {
		t.Fatalf("launch should be the enabled accent action: %+v", launch)
	}
	for _, id := range []string{"config-1", "folder-1", "favtoggle-1", "hidetoggle-1", "remove-1"} {
		if find(t, root, id) == nil {
			t.Fatalf("action %s missing: actions are always visible", id)
		}
	}
	s.Running = map[string]time.Time{"1": now.Add(-2 * time.Hour)}
	root = BuildTree(s)
	if find(t, root, "launch-1") != nil || find(t, root, "stop-1") == nil {
		t.Fatal("a running game's primary action is Stop")
	}
	s = baseState()
	s.Selected = "3" // not installed
	if l := find(t, BuildTree(s), "launch-3"); l == nil || !l.Disabled {
		t.Fatalf("an uninstalled game cannot launch: %+v", l)
	}
}

func TestFmtLastPlayed(t *testing.T) {
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Minute, "played just now"},
		{5 * time.Hour, "played 5h ago"},
		{3 * 24 * time.Hour, "played 3d ago"},
		{95 * 24 * time.Hour, "played 3mo ago"},
		{536 * 24 * time.Hour, "played 1y ago"},
	} {
		if got := fmtLastPlayed(now.Add(-tc.ago), now); got != tc.want {
			t.Errorf("%v ago = %q, want %q", tc.ago, got, tc.want)
		}
	}
	if got := fmtLastPlayed(time.Time{}, now); got != "never launched" {
		t.Errorf("zero time = %q", got)
	}
}

func TestEllipsisKeepsRunesWhole(t *testing.T) {
	if got := ellipsis("Pokémon Légendes Arceus", 9); got != "Pokémon …" {
		t.Fatalf("got %q", got)
	}
}

// An absent graph paints nothing, which read as a blank gap in the detail
// pane; without sessions the pane says so in words instead.
func TestGraphOnlyWithSessions(t *testing.T) {
	s := baseState()
	s.Selected = "1"
	root := BuildTree(s)
	if find(t, root, "session-graph") != nil || find(t, root, "session-empty") == nil {
		t.Fatal("no sessions: want the session-empty line and no graph")
	}
	s.Sessions = store.Log{"1": {{Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)}}}
	root = BuildTree(s)
	g := find(t, root, "session-graph")
	if g == nil || g.Absent || len(g.Values) == 0 {
		t.Fatalf("want values graph, got %+v", g)
	}
	for _, v := range g.Values {
		if v < 0 || v > 1 {
			t.Fatalf("values must be normalized 0..1, got %v", g.Values)
		}
	}
}

func TestEmptyLibraryNeverErrorTone(t *testing.T) {
	s := baseState()
	s.All = nil
	root := BuildTree(s)
	var errs []*v1.Node
	collect(t, root, func(n *v1.Node) bool { return n.Tone == v1.ToneError }, &errs)
	if len(errs) != 0 {
		t.Fatalf("empty library must be subtle, got %d error nodes", len(errs))
	}
	s.LibraryMissing = true
	root = BuildTree(s)
	collect(t, root, func(n *v1.Node) bool { return n.Tone == v1.ToneError }, &errs)
	if len(errs) != 0 {
		t.Fatal("missing library must be subtle too")
	}
}

func TestFailedLaunchMarksCard(t *testing.T) {
	s := baseState()
	s.Failed = map[string]bool{"2": true}
	root := BuildTree(s)
	var errs []*v1.Node
	collect(t, root, func(n *v1.Node) bool { return n.Tone == v1.ToneError }, &errs)
	if len(errs) != 1 {
		t.Fatalf("failed game should mark exactly one text node error, got %d", len(errs))
	}
}

func TestFavoriteStarOnCard(t *testing.T) {
	s := baseState()
	s.Prefs.Favorites = map[string]bool{"1": true}
	root := BuildTree(s)
	var stars []*v1.Node
	collect(t, root, func(n *v1.Node) bool { return n.Kind == v1.KindIcon && n.Icon == "star" }, &stars)
	if len(stars) != 1 {
		t.Fatalf("one star expected, got %d", len(stars))
	}
}

func TestPanelFits(t *testing.T) {
	s := baseState()
	if f := shelllint.Tree(BuildTree(s), v1.ViewPanel, 1200, 800); len(f) != 0 {
		t.Fatalf("library view does not fit 1200x800: %v", f)
	}
	s.Selected = baseState().All[0].ID
	s.Running = map[string]time.Time{s.Selected: now.Add(-time.Hour)}
	if f := shelllint.Tree(BuildTree(s), v1.ViewPanel, 1200, 800); len(f) != 0 {
		t.Fatalf("detail view does not fit 1200x800: %v", f)
	}
}

// TestPanelGeometry pins the host layout rules shelllint does not model: an
// unsized list in a row takes every remaining pixel (rejecting the detail
// pane), and a button card in a row measures zero tall unless sized. Both
// made the live shell refuse every revision of this panel.
func TestPanelGeometry(t *testing.T) {
	s := baseState()
	s.Selected = "1"
	root := BuildTree(s)
	list, detail := find(t, root, "game-list"), find(t, root, "detail")
	if list.Width <= 0 || list.Height <= 0 {
		t.Fatalf("game-list must be sized, got %dx%d", list.Width, list.Height)
	}
	if got := list.Width + gap + detail.Width; got != 1200-2*pad {
		t.Fatalf("list+gap+detail = %d, want the %d content width", got, 1200-2*pad)
	}
	if got := headerH + gap + list.Height; got != 800-2*pad {
		t.Fatalf("header+gap+body = %d, want the %d content height", got, 800-2*pad)
	}
	if detail.Height != list.Height {
		t.Fatalf("detail height %d != list height %d", detail.Height, list.Height)
	}
	if row := cardsPerRow*cardWidth + (cardsPerRow-1)*cardGap; row > list.Width {
		t.Fatalf("a card row is %d wide, list is %d", row, list.Width)
	}
	var cards []*v1.Node
	collect(t, root, func(n *v1.Node) bool { return n.Kind == v1.KindButton && n.Shape == "card" }, &cards)
	for _, c := range cards {
		if c.Height <= 0 {
			t.Fatalf("card %s has no height; a row lays it out zero tall", c.ID)
		}
	}
}

// While Lutris starts a game the card and the detail pane say so with a
// spinner the host turns, and Launch cannot be pressed again.
func TestLaunchingShowsASpinner(t *testing.T) {
	s := baseState()
	s.Selected = "1"
	s.Launching = map[string]bool{"1": true}
	root := BuildTree(s)
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatalf("validate: %v", err)
	}
	var spinners []*v1.Node
	collect(t, root, func(n *v1.Node) bool { return n.Kind == v1.KindSpinner }, &spinners)
	if len(spinners) != 2 || spinners[0].Key == spinners[1].Key {
		t.Fatalf("want a card and a detail spinner with distinct keys, got %d", len(spinners))
	}
	launch := find(t, root, "launch-1")
	if launch == nil || !launch.Disabled || launch.Text != "Launching…" {
		t.Fatalf("launch button while launching = %+v", launch)
	}
	if f := shelllint.Tree(root, v1.ViewPanel, 1200, 800); len(f) != 0 {
		t.Fatalf("launching view does not fit: %v", f)
	}
	s.Launching = nil
	root = BuildTree(s)
	spinners = nil
	collect(t, root, func(n *v1.Node) bool { return n.Kind == v1.KindSpinner }, &spinners)
	if len(spinners) != 0 {
		t.Fatal("no spinner once the game is up")
	}
}
