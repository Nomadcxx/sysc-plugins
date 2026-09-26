package worldclock

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func visit(n *v1.Node, fn func(*v1.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		visit(c, fn)
	}
}

func findID(root *v1.Node, id string) *v1.Node {
	var hit *v1.Node
	visit(root, func(n *v1.Node) {
		if hit == nil && n.ID == id {
			hit = n
		}
	})
	return hit
}

func hasText(root *v1.Node, text string, tone v1.Tone) bool {
	found := false
	visit(root, func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text == text && n.Tone == tone {
			found = true
		}
	})
	return found
}

func sampleReadings() []Reading {
	return []Reading{
		{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "21:04", Offset: "UTC+9", Relative: "+9h", DayShift: 1, Daytime: false, OnBar: true},
		{Zone: "UTC", Label: "UTC", Clock: "12:04", Offset: "UTC+0", Relative: "Same time", Daytime: true, OnBar: false},
	}
}

func lintPanel(t *testing.T, name string, s PanelState) *v1.Node {
	t.Helper()
	root := Panel(s)
	for _, f := range shelllint.Tree(root, v1.ViewPanel, PanelWidth, PanelHeight) {
		t.Errorf("%s: %s", name, f)
	}
	return root
}

func TestPanelLintEveryState(t *testing.T) {
	t.Parallel()
	many := make([]Reading, 8)
	for i := range many {
		many[i] = Reading{Zone: "America/Argentina/Buenos_Aires_" + string(rune('a'+i)), Label: strings.Repeat("東", MaxLabelRunes), Clock: "12:04 PM", Offset: "UTC-3:30", Relative: "−13h30", DayShift: -1, OnBar: true}
	}
	states := map[string]PanelState{
		"empty":       {},
		"zones":       {Readings: sampleReadings()},
		"eight":       {Readings: many},
		"error":       {Readings: sampleReadings(), Errors: []string{"Couldn't save zones"}, Notice: "Limited search: tz tables not found"},
		"suggestions": {Query: "to", Suggestions: []Suggestion{{ID: "Asia/Tokyo", Title: "Tokyo · Japan · +9h"}, {ID: "America/Toronto", Title: "Toronto · Canada · −14h"}}},
		"no-match":    {Query: "zzz", NoMatches: "No matching zone"},
		"renaming":    {Readings: sampleReadings(), Renaming: "Asia/Tokyo", RenameDraft: "Office"},
		"deleting":    {Readings: sampleReadings(), PendingDelete: "UTC"},
	}
	for name, s := range states {
		lintPanel(t, name, s)
	}
}

func TestPanelZoneCard(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "zones", PanelState{Readings: sampleReadings()})
	for _, id := range []string{"drag:Asia/Tokyo", "bar:Asia/Tokyo", "edit:Asia/Tokyo", "del:Asia/Tokyo", "drop:0", "drop:1", "drop:2"} {
		if findID(root, id) == nil {
			t.Fatalf("missing %s", id)
		}
	}
	if !hasText(root, "Asia · UTC+9", v1.ToneSubtle) || !hasText(root, "+9h", v1.ToneSubtle) || !hasText(root, "+1", v1.ToneAccent) {
		t.Fatal("card lines missing")
	}
	if !hasText(root, "Tokyo", v1.ToneNormal) || !hasText(root, "UTC", v1.ToneSubtle) {
		t.Fatal("on-bar label should be normal tone, hidden label subtle")
	}
	if findID(root, "bar:Asia/Tokyo").Icon != "visibility" || findID(root, "bar:UTC").Icon != "visibility_off" {
		t.Fatal("bar toggle icons")
	}
	if findID(root, "search").Placeholder != "Search a city or country" {
		t.Fatal("search placeholder")
	}
	if !findID(root, "add").Disabled {
		t.Fatal("add enabled with an empty query")
	}
}

func TestPanelEmptyState(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "empty", PanelState{})
	if !hasText(root, "No zones yet. Search for a city above.", v1.ToneSubtle) {
		t.Fatal("empty state missing")
	}
	if findID(root, "drop:0") != nil {
		t.Fatal("drop zone in an empty list")
	}
}

func TestPanelSuggestionsReplaceTheList(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "suggestions", PanelState{Readings: sampleReadings(), Query: "to", QueryReseed: 3,
		Suggestions: []Suggestion{{ID: "America/Toronto", Title: "Toronto · Canada · −14h"}}})
	if findID(root, "pick:America/Toronto") == nil || findID(root, "drag:Asia/Tokyo") != nil {
		t.Fatal("suggestions should occupy the list slot")
	}
	if s := findID(root, "search"); s.Text != "to" || s.Reseed != 3 || findID(root, "add").Disabled {
		t.Fatalf("search = %+v", s)
	}
	none := lintPanel(t, "no-match", PanelState{Query: "zzz"})
	if !hasText(none, "No matching zone", v1.ToneSubtle) {
		t.Fatal("no-match line missing")
	}
}

func TestPanelInlineEdits(t *testing.T) {
	t.Parallel()
	ren := lintPanel(t, "renaming", PanelState{Readings: sampleReadings(), Renaming: "Asia/Tokyo", RenameDraft: "Office", RenameReseed: 2})
	in := findID(ren, "label:Asia/Tokyo")
	if in == nil || in.Kind != v1.KindTextInput || in.Text != "Office" || in.Reseed != 2 || in.Placeholder != "Tokyo" {
		t.Fatalf("rename input = %+v", in)
	}
	if findID(ren, "rename-ok") == nil || findID(ren, "rename-cancel") == nil || findID(ren, "edit:Asia/Tokyo") != nil {
		t.Fatal("rename actions")
	}
	del := lintPanel(t, "deleting", PanelState{Readings: sampleReadings(), PendingDelete: "UTC"})
	if ok := findID(del, "del-ok"); ok == nil || ok.Fill != "error" || findID(del, "del-cancel") == nil || findID(del, "del:UTC") != nil {
		t.Fatal("delete confirm must replace the row's actions")
	}
	if findID(del, "del:Asia/Tokyo") == nil {
		t.Fatal("other rows keep their actions")
	}
}

func TestPanelErrorAndNotice(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "error", PanelState{Errors: []string{"Couldn't save zones"}, Notice: "Limited search: tz tables not found"})
	if !hasText(root, "Couldn't save zones", v1.ToneError) || !hasText(root, "Limited search: tz tables not found", v1.ToneSubtle) {
		t.Fatal("error or notice missing")
	}
}

func TestPanelPatchKeysExistInPanel(t *testing.T) {
	t.Parallel()
	for _, renaming := range []string{"", "Asia/Tokyo"} {
		root := Panel(PanelState{Readings: sampleReadings(), Renaming: renaming})
		keys := map[string]bool{}
		visit(root, func(n *v1.Node) {
			if n.Key != "" {
				keys[n.Key] = true
			}
		})
		patch := PanelPatch(sampleReadings(), renaming)
		if len(patch) == 0 {
			t.Fatal("empty patch")
		}
		for _, r := range patch {
			if !keys[r.Key] || r.Node.Key != r.Key {
				t.Fatalf("renaming=%q: patch key %s not in panel", renaming, r.Key)
			}
			if err := v1.Validate(&v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{r.Node}}, v1.ViewPanel); err != nil {
				t.Fatalf("patch node %s: %v", r.Key, err)
			}
		}
	}
}

func TestSuggestionTitle(t *testing.T) {
	t.Parallel()
	if got := SuggestionTitle(Match{City: "Tokyo", Country: "Japan"}, "+9h"); got != "Tokyo · Japan · +9h" {
		t.Fatalf("title = %q", got)
	}
	if got := SuggestionTitle(Match{City: "UTC"}, "Same time"); got != "UTC · Same time" {
		t.Fatalf("title = %q", got)
	}
}

func TestManifestPanelMatchesPanelBox(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Panels []struct{ Width, Height int } `json:"panels"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Panels) != 1 {
		t.Fatalf("manifest panels: %v %+v", err, m)
	}
	if m.Panels[0].Width != PanelWidth || m.Panels[0].Height != PanelHeight {
		t.Fatalf("manifest %dx%d, code %dx%d", m.Panels[0].Width, m.Panels[0].Height, PanelWidth, PanelHeight)
	}
	if PanelWidth < 480 || PanelHeight < 440 {
		t.Fatalf("panel %dx%d is the size that clipped zone lines on hardware", PanelWidth, PanelHeight)
	}
}

// On hardware the title and search box sat on the panel border, the search
// text overlapped the pill's cap, and the scrollbar covered the card actions.
func TestPanelContentClearsEdgesAndScrollbar(t *testing.T) {
	t.Parallel()
	root := Panel(PanelState{Readings: sampleReadings()})
	if root.Padding < 12 {
		t.Fatalf("root padding %d leaves content on the panel border", root.Padding)
	}
	search := findID(root, "search")
	if search.Padding < 8 || search.Height-2*search.Padding < 16 {
		t.Fatalf("search padding %d at height %d: text must clear the cap and keep a 16px line", search.Padding, search.Height)
	}
	var list, header *v1.Node
	visit(root, func(n *v1.Node) {
		if n.Kind == v1.KindList && list == nil {
			list = n
		}
	})
	header = root.Children[0]
	if list == nil || list.Padding < 10 {
		t.Fatalf("list padding must hold the 4px scrollbar clear of the cards: %+v", list)
	}
	if header.Padding != list.Padding {
		t.Fatalf("header inset %d and card inset %d disagree", header.Padding, list.Padding)
	}
	inner := PanelWidth - 2*root.Padding - 2*header.Padding
	if got := search.Width + searchGap + searchAddWidth; got != inner {
		t.Fatalf("search row is %d wide, header content is %d", got, inner)
	}
}

func TestPanelListFillsHeightWithoutOverflow(t *testing.T) {
	t.Parallel()
	for _, errs := range [][]string{nil, {"Couldn't save zones", "Tokyo is already in the list"}} {
		s := PanelState{Readings: sampleReadings(), Errors: errs}
		if len(errs) > 0 {
			s.Notice = "Limited search: tz tables not found"
		}
		root := Panel(s)
		var list *v1.Node
		visit(root, func(n *v1.Node) {
			if n.Kind == v1.KindList && list == nil {
				list = n
			}
		})
		used := 2*root.Padding + panelHeaderHeight(s) + root.Gap + list.Height
		if used > PanelHeight {
			t.Fatalf("errors=%d: content needs %d px of a %d px panel", len(errs), used, PanelHeight)
		}
		if len(errs) == 0 && list.Height < 300 {
			t.Fatalf("list is %d px: the taller panel should show five cards", list.Height)
		}
	}
}

func TestMetaLineOmitsRedundantZoneForUTC(t *testing.T) {
	t.Parallel()
	if got := metaText(Reading{Zone: "UTC", Offset: "UTC+0"}).Text; got != "UTC+0" {
		t.Fatalf("utc meta = %q", got)
	}
	if got := metaText(Reading{Zone: "Asia/Tokyo", Offset: "UTC+9"}).Text; got != "Asia/Tokyo · UTC+9" {
		t.Fatalf("tokyo meta = %q", got)
	}
}

func TestMetaLineDropsTheCityTheLabelRepeats(t *testing.T) {
	t.Parallel()
	cases := map[Reading]string{
		{Zone: "America/New_York", Label: "New York", Offset: "UTC-4"}:                   "America · UTC-4",
		{Zone: "America/Argentina/Buenos_Aires", Label: "Buenos Aires", Offset: "UTC-3"}: "America/Argentina · UTC-3",
		{Zone: "America/New_York", Label: "HQ", Offset: "UTC-4"}:                         "America/New_York · UTC-4",
		{Zone: "UTC", Label: "UTC", Offset: "UTC+0"}:                                     "UTC+0",
	}
	for r, want := range cases {
		if got := metaText(r).Text; got != want {
			t.Errorf("%s/%s meta = %q, want %q", r.Zone, r.Label, got, want)
		}
	}
}

func TestHeaderToListSpacingIsOneInset(t *testing.T) {
	t.Parallel()
	root := Panel(PanelState{Readings: sampleReadings()})
	header, list := root.Children[0], root.Children[1]
	if got := header.Padding + root.Gap + list.Padding; got > 2*contentInset {
		t.Fatalf("search box to first card is %d px; hardware showed 30 px reads as a hole", got)
	}
}
