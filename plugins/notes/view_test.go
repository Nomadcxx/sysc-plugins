package notes

import (
	"fmt"
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var viewNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func findByID(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if f := findByID(c, id); f != nil {
			return f
		}
	}
	return nil
}

func findText(n *v1.Node, text string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.Text == text {
		return n
	}
	for _, c := range n.Children {
		if f := findText(c, text); f != nil {
			return f
		}
	}
	return nil
}

func panelFixtures() map[string]Snapshot {
	notes := []Summary{
		{Name: "Weekly review.md", Title: "Weekly review", Preview: "Ship notes redesign", Modified: viewNow.Add(-20 * time.Minute), Favorite: true},
		{Name: "x.md", Title: strings.Repeat("Unbroken", 30), Preview: strings.Repeat("y", 400), Modified: viewNow.Add(-50 * time.Hour)},
		{Name: "scratchpad.md", Title: "scratchpad", Preview: "loose ends", Modified: viewNow},
	}
	sel := Snapshot{Notes: notes, Selected: "Weekly review.md", Title: "Weekly review", Body: "# Weekly review", Words: 3, Modified: viewNow, Favorite: true, Now: viewNow}
	conflict, del, dirty, failed := sel, sel, sel, sel
	conflict.Conflict, conflict.SaveError = true, "This note changed outside Notes"
	del.PendingDelete = "Weekly review.md"
	dirty.Dirty = true
	failed.SaveError = "Save failed: " + strings.Repeat("permission denied ", 10)
	long := sel
	long.Title = strings.Repeat("A very long note title ", 12)
	return map[string]Snapshot{
		"empty":    {Now: viewNow},
		"library":  {Notes: notes, Now: viewNow},
		"selected": sel, "conflict": conflict, "delete": del, "dirty": dirty, "failed": failed, "long-title": long,
		"notice":  {Notes: notes, Notice: "The clipboard has no plain text", Now: viewNow},
		"scan":    {ScanError: "notes: scan folder: permission denied", Now: viewNow},
		"search":  {Query: "nothing", Now: viewNow},
		"sort-az": {Notes: notes, SortByName: true, Now: viewNow},
	}
}

func TestPanelFitsEveryState(t *testing.T) {
	for name, snap := range panelFixtures() {
		for _, clip := range []bool{true, false} {
			for _, f := range shelllint.Tree(PanelTree(snap, clip), v1.ViewPanel, PanelWidth, PanelHeight) {
				t.Errorf("%s clip=%v: %s", name, clip, f)
			}
		}
	}
}

func TestBarAndTooltipFit(t *testing.T) {
	for _, f := range shelllint.Tree(BarTree(), v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
		t.Errorf("bar: %s", f)
	}
	for _, tip := range []*v1.Node{TooltipTree(12, viewNow.Add(-time.Hour), viewNow), TooltipTree(0, time.Time{}, viewNow)} {
		for _, f := range shelllint.Tree(tip, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
			t.Errorf("tooltip: %s", f)
		}
	}
	if findText(TooltipTree(12, viewNow.Add(-time.Hour), viewNow), "12 notes · last edited 1h ago") == nil {
		t.Error("tooltip must say how many notes and when one last changed")
	}
}

func TestNoticeDoesNotHideLibrary(t *testing.T) {
	tree := PanelTree(panelFixtures()["notice"], false)
	if findByID(tree, "notice-dismiss") == nil || findByID(tree, "open:"+Token("Weekly review.md")) == nil {
		t.Fatal("notice must sit above a visible library")
	}
}

func TestSelectedRowIsMarkedAndEditorShown(t *testing.T) {
	tree := PanelTree(panelFixtures()["selected"], false)
	row := findByID(tree, "open:"+Token("Weekly review.md"))
	if row.Fill != "chip" || row.Stroke != 1 {
		t.Errorf("selected row fill %q stroke %d", row.Fill, row.Stroke)
	}
	if findByID(tree, "body") == nil || findByID(tree, "favorite").Icon != "star" {
		t.Error("editor or favourite action missing")
	}
	if findText(tree, "3 words · edited Just now") == nil {
		t.Error("footer must show word count and age")
	}
}

func TestEmptySelectionShowsGuidance(t *testing.T) {
	tree := PanelTree(panelFixtures()["library"], false)
	if findByID(tree, "body") != nil || findText(tree, "No note open") == nil {
		t.Fatal("unselected panel must show the empty editor state")
	}
}

func TestScratchpadIsFirstAndNotRepeated(t *testing.T) {
	tree := PanelTree(panelFixtures()["library"], false)
	list := findByID(tree, "library")
	if list.Children[0].ID != "scratch" {
		t.Fatalf("first library row = %q, want scratch", list.Children[0].ID)
	}
	if findByID(tree, "open:"+Token("scratchpad.md")) != nil {
		t.Fatal("the scratchpad must not appear again among the notes")
	}
}

func TestClipboardButtonOnlyWhenGranted(t *testing.T) {
	if findByID(PanelTree(Snapshot{}, false), "clipboard-import") != nil {
		t.Fatal("paste offered without clipboard access")
	}
	if findByID(PanelTree(Snapshot{}, true), "clipboard-import") == nil {
		t.Fatal("paste missing with clipboard access")
	}
}

func TestOmniboxIsTheHostSearchField(t *testing.T) {
	box := findByID(PanelTree(Snapshot{Query: "lease"}, false), "omnibox")
	if box == nil || box.Name != "Search" || box.Text != "lease" || box.Padding == 0 {
		t.Fatalf("omnibox = %+v", box)
	}
}

func TestEmptySearchOffersToCreate(t *testing.T) {
	if findText(PanelTree(panelFixtures()["search"], false), "No matches · Enter creates “nothing”") == nil {
		t.Fatal("a search with no matches must say Enter creates it")
	}
}

func TestLargeLibraryStaysWithinTreeLimits(t *testing.T) {
	items := make([]Summary, 300)
	for i := range items {
		items[i] = Summary{Name: fmt.Sprintf("note-%03d.md", i), Title: fmt.Sprintf("Note %03d", i), Favorite: i < 40}
	}
	root := PanelTree(Snapshot{Notes: items, Now: viewNow}, false)
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatalf("large library exceeds protocol limits: %v", err)
	}
	if findText(root, fmt.Sprintf("Showing %d of 300 · search to narrow", maxRows)) == nil {
		t.Fatal("a truncated library must say so")
	}
}

// realTextHeight is what the system faces measure a label at; shelllint's
// fixed 16px metric passes rows the live shell refuses.
const realTextHeight = 20

func rowsLeaveRoomForRealText(t *testing.T, name string, n *v1.Node) {
	t.Helper()
	if n == nil {
		return
	}
	if n.Kind == v1.KindRow && n.Height > 0 {
		for _, c := range n.Children {
			if c.Kind == v1.KindText && n.Height-2*n.Padding < realTextHeight {
				t.Errorf("%s: row %q holds text %q in %dpx", name, n.ID, c.Text, n.Height-2*n.Padding)
			}
		}
	}
	for _, c := range n.Children {
		rowsLeaveRoomForRealText(t, name, c)
	}
}

func TestPanelRowsLeaveRoomForRealText(t *testing.T) {
	for name, snap := range panelFixtures() {
		rowsLeaveRoomForRealText(t, name, PanelTree(snap, true))
	}
}

// The shell frames a sticky with a 44px title bar and a 16px grip at default
// density; at its 200x180 minimum the content gets what is left.
const stickyMinW, stickyMinContentH = 200, 180 - 44 - 16

func TestStickyFitsAndIsBodyFirst(t *testing.T) {
	doc := Document{Name: "Weekly review.md", Body: strings.Repeat("line\n", 80)}
	for _, c := range []string{"sun", "mint", "sky", "rose", "lilac"} {
		tree := StickyTree(doc, c)
		for _, f := range shelllint.Tree(tree, v1.ViewFloating, stickyMinW, stickyMinContentH) {
			t.Errorf("%s: %s", c, f)
		}
		if tree.Children[0].Kind != v1.KindTextInput || tree.Fill != "note-"+c {
			t.Errorf("%s: body must come first on note paper", c)
		}
		rowsLeaveRoomForRealText(t, c, tree)
	}
}

func TestStickyShowsSelectedColourWithACheck(t *testing.T) {
	tree := StickyTree(Document{Name: "a.md"}, "mint")
	dot := findByID(tree, "color:"+Token("a.md")+":mint")
	if dot.Icon != "check" || dot.Fill != "note-mint" {
		t.Fatalf("selected dot = %+v", dot)
	}
	if other := findByID(tree, "color:"+Token("a.md")+":sun"); other.Icon != "" || other.Fill != "note-sun" {
		t.Fatalf("unselected dot = %+v", other)
	}
}

func TestStickyStatusOnlyWhenNotSaved(t *testing.T) {
	if findText(StickyTree(Document{Name: "a.md"}, "sun"), "Saved") != nil {
		t.Error("a saved sticky must not print its save state")
	}
	if findText(StickyTree(Document{Name: "a.md", Dirty: true}, "sun"), "Saving…") == nil {
		t.Error("a dirty sticky must say Saving…")
	}
	if n := findText(StickyTree(Document{Name: "a.md", Error: "Save failed: disk full"}, "sun"), "Save failed: disk full"); n == nil || n.Tone != v1.ToneError {
		t.Error("a failed save must show in the error tone")
	}
}

func TestStickyDotsAreRingedSoTheyShowOnTheirOwnPaper(t *testing.T) {
	tree := StickyTree(Document{Name: "a.md"}, "sun")
	for _, c := range stickyColors {
		dot := findByID(tree, "color:"+Token("a.md")+":"+c.id)
		want := 1
		if c.id == "sun" {
			want = 2
		}
		if dot.Stroke != want || dot.StrokeFill != "outline" {
			t.Errorf("%s dot stroke %d %q, want %d outline", c.id, dot.Stroke, dot.StrokeFill, want)
		}
	}
}
