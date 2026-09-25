package faith

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func find(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if f := find(c, id); f != nil {
			return f
		}
	}
	return nil
}

func texts(n *v1.Node) []string {
	var out []string
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n.Text != "" {
			out = append(out, n.Text)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

func TestBarIsTheCross(t *testing.T) {
	ref := Ref{Book: 42, Chapter: 3, Verse: 16}
	bar := BarTree(ref, false)
	if len(bar.Children) != 1 {
		t.Fatalf("bar has %d children", len(bar.Children))
	}
	cross := find(bar, NodeCross)
	if cross == nil || cross.Icon != "cross" || cross.Name != "Faith: click for a prayer" ||
		len(cross.Events) != 2 || cross.Events[0] != v1.EventActivate || cross.Events[1] != v1.EventPointer {
		t.Fatalf("cross = %+v", cross)
	}
	withRef := BarTree(ref, true)
	if len(withRef.Children) != 2 || withRef.Children[1].Text != "John 3:16" || withRef.Children[1].Tone != v1.ToneSubtle {
		t.Fatalf("bar with reference = %+v", withRef.Children)
	}
}

func TestTooltipShowsTwoLinesAndTheHint(t *testing.T) {
	b := mustBible(t, "KJV")
	ref := mustRef(t, "JHN 3:16")
	verse, _ := b.Text(ref)
	got := texts(TooltipTree(ref, verse))
	if len(got) != 4 || got[0] != "John 3:16" || !strings.HasSuffix(got[2], "…") || got[3] != "Middle-click to read" {
		t.Fatalf("tooltip = %q", got)
	}
	for _, l := range got[1:3] {
		if len(l) > tooltipBytes {
			t.Fatalf("tooltip line %q is %d bytes", l, len(l))
		}
	}
}

func TestPanelControlsAndSections(t *testing.T) {
	m := PanelModel{
		Ref: mustRef(t, "GEN 1:1"), Translation: "BSB", Verse: "In the beginning God created the heavens and the earth.",
		CanPrev: false, CanNext: true, CanRead: true,
		Commentary: CommentaryReady, Note: "A note.",
		Xrefs: []Ref{mustRef(t, "JHN 1:1"), mustRef(t, "HEB 11:3")},
	}
	p := PanelTree(m)
	for id, icon := range map[string]string{NodePrev: "chevron_left", NodeNext: "chevron_right", NodeNew: "refresh", NodeRead: "menu_book"} {
		if n := find(p, id); n == nil || n.Icon != icon {
			t.Fatalf("%s = %+v", id, n)
		}
	}
	if !find(p, NodePrev).Disabled || find(p, NodeNext).Disabled {
		t.Fatal("prev/next disabled states are wrong at Genesis 1:1")
	}
	if n := find(p, XrefNode(1)); n == nil || n.Text != "Hebrews 11:3" {
		t.Fatalf("xref:1 = %+v", n)
	}
	all := strings.Join(texts(p), "|")
	for _, want := range []string{"Genesis 1:1", "Commentary", "A note.", "See also", "OpenBible.info"} {
		if !strings.Contains(all, want) {
			t.Fatalf("panel lacks %q: %s", want, all)
		}
	}
	m.CanRead = false
	m.Commentary = CommentaryOff
	p = PanelTree(m)
	if find(p, NodeRead) != nil || strings.Contains(strings.Join(texts(p), "|"), "Commentary") {
		t.Fatal("read button or commentary shown when off")
	}
}

func TestLongCommentaryIsShortened(t *testing.T) {
	m := PanelModel{Ref: mustRef(t, "GEN 1:1"), Translation: "KJV", Verse: "v", Commentary: CommentaryReady,
		Note: strings.Repeat("word ", 2000)}
	all := texts(PanelTree(m))
	n := 0
	for _, s := range all {
		if strings.HasPrefix(s, "word") {
			n++
		}
	}
	if n != MaxCommentaryLines || !strings.Contains(strings.Join(all, "|"), "Shortened.") {
		t.Fatalf("%d commentary lines, shortened note present=%v", n, strings.Contains(strings.Join(all, "|"), "Shortened."))
	}
}

func panelBox(t *testing.T) (int, int) {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Panels []struct{ Width, Height int } `json:"panels"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Panels) != 1 {
		t.Fatalf("manifest panels: %v", err)
	}
	return m.Panels[0].Width, m.Panels[0].Height
}

// TestViewsFitTheirHostSlots lays every view out with the host's own rules,
// at the host's sizes, over the longest content the plugin can show.
func TestViewsFitTheirHostSlots(t *testing.T) {
	w, h := panelBox(t)
	pool, err := LoadPool()
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, n *v1.Node, kind v1.ViewKind, width, height int) {
		t.Helper()
		if err := v1.Validate(n, kind); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, f := range shelllint.Tree(n, kind, width, height) {
			t.Errorf("%s: %s", name, f)
		}
	}
	longestRef, longest := pool[0], ""
	for _, id := range Translations {
		b := mustBible(t, id)
		for _, r := range pool {
			if len(r.String()) > len(longestRef.String()) {
				longestRef = r
			}
			if v, _ := b.Text(r); len(v) > len(longest) {
				longest = v
			}
		}
	}
	for _, show := range []bool{false, true} {
		check("bar", BarTree(longestRef, show), v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight)
	}
	check("tooltip", TooltipTree(longestRef, longest), v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight)
	five := []Ref{longestRef, longestRef, longestRef, longestRef, longestRef}
	for _, status := range []CommentaryStatus{CommentaryOff, CommentaryLoading, CommentaryReady, CommentaryNone, CommentaryUnavailable} {
		for _, xrefs := range [][]Ref{nil, five} {
			m := PanelModel{Ref: longestRef, Translation: "WEB", Verse: longest, CanPrev: true, CanNext: true, CanRead: true,
				Commentary: status, Note: strings.Repeat("A very long commentary sentence. ", 400), Xrefs: xrefs}
			check("panel", PanelTree(m), v1.ViewPanel, w, h)
		}
	}
}
