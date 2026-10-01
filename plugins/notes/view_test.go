package notes

import (
	"fmt"
	"strings"
	"testing"
	"time"

	lint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestManagerAndEditorFitTheNotesPanel(t *testing.T) {
	longTitle := strings.Repeat("A very long Obsidian note title ", 8)
	summary := Snapshot{
		Notes: []Summary{{Name: longTitle + ".md", Title: longTitle, Preview: strings.Repeat("Useful preview text ", 12), Modified: time.Now(), Favorite: true}},
	}
	states := []Snapshot{
		{},
		{ScanError: "The configured notes folder cannot be read"},
		{Query: "no match"},
		summary,
		{Selected: "long.md", Title: "A long note", Body: strings.Repeat("Markdown line\n", 120), Dirty: true},
		{Selected: "conflict.md", Title: "Conflict", Conflict: true, ConflictBody: "external body", PendingDelete: "conflict.md", SaveError: "The file is read-only", ScanError: "Sticky notes need on-demand layer-shell focus"},
	}
	for i, state := range states {
		if findings := lint.Tree(PanelTree(state, true), v1.ViewPanel, 420, 800); len(findings) != 0 {
			t.Errorf("state %d does not fit: %v", i, findings)
		}
	}
}

func TestStickyTreeUsesSelectedColorAndPinStateAndFits(t *testing.T) {
	doc := Document{Name: "Planning.md", Body: "Write the first draft"}
	root := StickyTree(doc, "mint", true)
	if root.Fill != "note-mint" {
		t.Fatalf("sticky fill = %q", root.Fill)
	}
	if findings := lint.Tree(root, v1.ViewFloating, 380, 500); len(findings) != 0 {
		t.Fatalf("sticky does not fit: %v", findings)
	}
	if got := root.Children[2].Children[0].Text; got != "Always on top" {
		t.Fatalf("pin status = %q", got)
	}
	if got := root.Children[0].Children[1].Name; got != "Mint note color (selected)" {
		t.Errorf("selected color name = %q", got)
	}
}

func TestManagerOnlyShowsClipboardImportWhenGranted(t *testing.T) {
	for _, tc := range []struct {
		granted bool
		want    bool
	}{{false, false}, {true, true}} {
		root := PanelTree(Snapshot{}, tc.granted)
		found := false
		var walk func(*v1.Node)
		walk = func(n *v1.Node) {
			if n == nil {
				return
			}
			if n.ID == "clipboard-import" {
				found = true
			}
			for _, child := range n.Children {
				walk(child)
			}
		}
		walk(root)
		if found != tc.want {
			t.Errorf("clipboard import found=%v with grant=%v", found, tc.granted)
		}
		if findings := lint.Tree(root, v1.ViewPanel, 420, 800); len(findings) != 0 {
			t.Errorf("clipboard grant=%v layout: %v", tc.granted, findings)
		}
	}
}

func TestManagerLargeLibraryStaysWithinPluginTreeLimits(t *testing.T) {
	items := make([]Summary, 120)
	for i := range items {
		items[i] = Summary{Name: fmt.Sprintf("note-%03d.md", i), Title: fmt.Sprintf("Note %03d", i), Favorite: i < 60}
	}
	root := PanelTree(Snapshot{Notes: items}, false)
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatalf("large manager tree exceeds protocol limits: %v", err)
	}
	var cards, notices int
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if strings.HasPrefix(n.Key, "note:") {
			cards++
		}
		if n.ID == "favorites-more" || n.ID == "recent-more" {
			notices++
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(root)
	if cards != 120 || notices != 0 {
		t.Fatalf("large manager showed %d cards and %d limit notices", cards, notices)
	}
	items = append(items, Summary{Name: "overflow.md", Title: "Overflow"})
	root = PanelTree(Snapshot{Notes: items}, false)
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatalf("manager with a truncated section exceeds protocol limits: %v", err)
	}
	cards, notices = 0, 0
	walk(root)
	if cards != 120 || notices != 1 {
		t.Fatalf("truncated manager showed %d cards and %d limit notices", cards, notices)
	}
}
