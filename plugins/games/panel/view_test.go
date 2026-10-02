package panel

import (
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	"testing"
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
	s.Actions = true
	s.Running = map[string]time.Time{"1": now.Add(-time.Hour)}
	if err := v1.Validate(BuildTree(s), v1.ViewPanel); err != nil {
		t.Fatalf("validate actions: %v", err)
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

func TestCardsTwoPerRow(t *testing.T) {
	root := BuildTree(baseState())
	list := find(t, root, "game-list")
	if list == nil || list.Kind != v1.KindList {
		t.Fatal("no list")
	}
	rows := list.Children
	if len(rows) != 2 {
		t.Fatalf("3 games -> 2 rows, got %d", len(rows))
	}
	if len(rows[0].Children) != 2 || len(rows[1].Children) != 1 {
		t.Fatal("card pairing wrong")
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
	if launch == nil || launch.Disabled {
		t.Fatal("launch button missing or disabled")
	}
	s.Running = map[string]time.Time{"1": now.Add(-2 * time.Hour)}
	root = BuildTree(s)
	launch = find(t, root, "launch-1")
	if launch == nil || !launch.Disabled || launch.Text != "Playing" {
		t.Fatalf("running launch should be disabled 'Playing': %+v", launch)
	}
	if find(t, root, "stop-1") != nil {
		t.Fatal("stop button should not show before More")
	}
	s.Actions = true
	root = BuildTree(s)
	if find(t, root, "stop-1") == nil || find(t, root, "detail-back") == nil {
		t.Fatal("action column missing stop/back")
	}
	if find(t, root, "favtoggle-1") == nil || find(t, root, "hidetoggle-1") == nil ||
		find(t, root, "remove-1") == nil || find(t, root, "config-1") == nil ||
		find(t, root, "folder-1") == nil {
		t.Fatal("action column missing buttons")
	}
}

func TestGraphAbsentWithoutSessions(t *testing.T) {
	s := baseState()
	s.Selected = "1"
	root := BuildTree(s)
	g := find(t, root, "session-graph")
	if g == nil || !g.Absent || len(g.Values) != 0 {
		t.Fatalf("want absent graph, got %+v", g)
	}
	s.Sessions = store.Log{"1": {{Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)}}}
	root = BuildTree(s)
	g = find(t, root, "session-graph")
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
	collect(t, root, func(n *v1.Node) bool { return n.Kind == v1.KindText && n.Text == "\u2605" }, &stars)
	if len(stars) != 1 {
		t.Fatalf("one star expected, got %d", len(stars))
	}
}

func TestPanelFits(t *testing.T) {
	s := baseState()
	if f := shelllint.Tree(BuildTree(s), v1.ViewPanel, 720, 560); len(f) != 0 {
		t.Fatalf("library view does not fit 720x560: %v", f)
	}
	s.Selected = baseState().All[0].ID
	s.Actions = true
	if f := shelllint.Tree(BuildTree(s), v1.ViewPanel, 720, 560); len(f) != 0 {
		t.Fatalf("action view does not fit 720x560: %v", f)
	}
}

// TestPanelGeometry pins the host layout rules shelllint does not model: an
// unsized list in a row takes every remaining pixel (rejecting the detail
// pane), and a button card in a row measures zero tall unless sized. Both
// made the live shell refuse every revision of this panel.
func TestPanelGeometry(t *testing.T) {
	for _, actions := range []bool{false, true} {
		s := baseState()
		s.Selected, s.Actions = "1", actions
		root := BuildTree(s)
		list, detail := find(t, root, "game-list"), find(t, root, "detail")
		if list.Width <= 0 || list.Height <= 0 {
			t.Fatalf("game-list must be sized, got %dx%d", list.Width, list.Height)
		}
		if got := list.Width + 12 + detail.Width; got != 720-2*12 {
			t.Fatalf("list+gap+detail = %d, want the 696 content width", got)
		}
		if detail.Height != list.Height {
			t.Fatalf("detail height %d != list height %d", detail.Height, list.Height)
		}
		var cards []*v1.Node
		collect(t, root, func(n *v1.Node) bool { return n.Kind == v1.KindButton && n.Shape == "card" }, &cards)
		for _, c := range cards {
			if c.Height <= 0 {
				t.Fatalf("card %s has no height; a row lays it out zero tall", c.ID)
			}
		}
	}
}
