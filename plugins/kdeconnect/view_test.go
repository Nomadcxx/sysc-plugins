package kdeconnect

import (
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestBarTreeShowsOfflineState(t *testing.T) {
	t.Parallel()
	open := BarTree(Snapshot{}).Children[0]
	if open.ID != "open" || open.Name == "" || open.Role == "" {
		t.Fatalf("open control = %+v", open)
	}
	if open.Icon != "phonelink-off" || open.Text != "N/A" {
		t.Fatalf("offline pill = %q %q", open.Icon, open.Text)
	}
}

func TestBarTreeDropsLabelWhenAvailable(t *testing.T) {
	t.Parallel()
	open := BarTree(Snapshot{Available: true, BackendName: "KDE Connect"}).Children[0]
	if open.Icon != "smartphone" {
		t.Fatalf("available glyph = %q", open.Icon)
	}
	if open.Text != "" {
		t.Fatalf("available label = %q, want icon only", open.Text)
	}
}

func TestTooltipTreeTracksState(t *testing.T) {
	t.Parallel()
	if got := TooltipTree(Snapshot{}).Children[0].Text; got != "Phone Connect · unavailable" {
		t.Fatalf("unavailable tooltip = %q", got)
	}
	if got := TooltipTree(Snapshot{Available: true}).Children[0].Text; got != "Phone Connect · no devices" {
		t.Fatalf("empty tooltip = %q", got)
	}
}

func TestPanelTreeHeaderCounts(t *testing.T) {
	t.Parallel()
	snap := Snapshot{Available: true, BackendName: "KDE Connect", Devices: []Device{
		{ID: "a", Name: "Pixel", Reachable: true, Paired: true},
		{ID: "b", Name: "Tablet", Paired: true},
	}}
	header := PanelTree(snap).Children[0]
	texts := headerTexts(header)
	if len(texts) != 2 || texts[0] != "KDE Connect" || texts[1] != "1 connected • 2 paired" {
		t.Fatalf("header texts = %v", texts)
	}
}

func TestPanelTreeUnavailableAndEmptyStates(t *testing.T) {
	t.Parallel()
	down := PanelTree(Snapshot{})
	if len(down.Children) != 2 || down.Children[1].Fill != "card" {
		t.Fatalf("unavailable panel = %+v", down)
	}
	if text := down.Children[1].Children[0].Text; text != "KDE Connect daemon unreachable" {
		t.Fatalf("unavailable headline = %q", text)
	}

	empty := PanelTree(Snapshot{Available: true})
	if len(empty.Children) != 2 || empty.Children[1].Fill != "card" {
		t.Fatalf("empty panel = %+v", empty)
	}
	if text := empty.Children[1].Children[0].Text; text != "No devices" {
		t.Fatalf("empty headline = %q", text)
	}

	populated := PanelTree(Snapshot{Available: true, Devices: []Device{{ID: "a", Paired: true}}})
	if len(populated.Children) != 1 {
		t.Fatal("populated panel grew a state card")
	}
}

func TestHeaderRefreshControlPinsRight(t *testing.T) {
	t.Parallel()
	header := PanelTree(Snapshot{Available: true}).Children[0]
	if !header.PinEnd {
		t.Fatal("header row is not a pin-end row")
	}
	refresh := header.Children[1]
	if refresh.ID != "refresh" || refresh.Icon != "refresh" || refresh.Name == "" || refresh.Role == "" {
		t.Fatalf("refresh control = %+v", refresh)
	}
}

func TestTreesValidate(t *testing.T) {
	t.Parallel()
	if err := v1.Validate(BarTree(Snapshot{}), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree(Snapshot{Available: true}), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(TooltipTree(Snapshot{}), v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	states := []Snapshot{
		{},
		{Available: true},
		{Available: true, Devices: []Device{{ID: "a", Name: "Pixel", Paired: true}}},
	}
	for _, snap := range states {
		if err := v1.Validate(PanelTree(snap), v1.ViewPanel); err != nil {
			t.Fatalf("panel %+v: %v", snap, err)
		}
	}
}

// headerTexts collects the text of the title and detail runs inside the
// header's nested row.
func headerTexts(header *v1.Node) []string {
	var texts []string
	var walk func(n *v1.Node)
	walk = func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text != "" {
			texts = append(texts, n.Text)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, c := range header.Children {
		walk(c)
	}
	return texts
}
