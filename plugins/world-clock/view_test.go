package worldclock

import (
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestPanelTreeHasDragHandlesAndTimeKeys(t *testing.T) {
	t.Parallel()
	root := PanelTree([]Reading{{Zone: "UTC", Clock: "15:04", Offset: "UTC+0"}}, "", "", "")
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	var drag, timeKey, drop bool
	walk(root, func(n *v1.Node) {
		if n.Kind == v1.KindDragSource && n.ID == "drag:UTC" {
			drag = true
		}
		if n.Key == "time:UTC" {
			timeKey = true
		}
		if n.Kind == v1.KindDropZone {
			drop = true
		}
	})
	if !drag || !timeKey || !drop {
		t.Fatalf("drag=%v time=%v drop=%v", drag, timeKey, drop)
	}
}

func TestPanelTreeZoneCards(t *testing.T) {
	t.Parallel()
	root := PanelTree([]Reading{{Zone: "UTC", Label: "London", Clock: "15:04", Offset: "UTC+0"}}, "", "", "")
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	var card, label, zoneID, clock, offset, title, add bool
	walk(root, func(n *v1.Node) {
		switch {
		case n.Key == "row:UTC":
			card = n.Fill == "card" && n.Radius == 10
		case n.Kind == v1.KindText && n.Text == "London":
			label = n.Bold
		case n.Kind == v1.KindText && n.Text == "UTC":
			zoneID = n.Tone == v1.ToneSubtle
		case n.Key == "time:UTC":
			clock = n.Bold && n.Tabular
		case n.Kind == v1.KindText && n.Text == "UTC+0":
			offset = n.Tone == v1.ToneSubtle
		case n.Kind == v1.KindText && n.Size == "title":
			title = n.Bold
		case n.Kind == v1.KindButton && n.ID == "add":
			add = n.Fill == "accent"
		}
	})
	if !card || !label || !zoneID || !clock || !offset || !title || !add {
		t.Fatalf("card=%v label=%v zone=%v clock=%v offset=%v title=%v add=%v",
			card, label, zoneID, clock, offset, title, add)
	}
}

func TestPanelTreeEmptyState(t *testing.T) {
	t.Parallel()
	root := PanelTree(nil, "", "", "")
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	var empty bool
	walk(root, func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text == "No zones — add a city" && n.Tone == v1.ToneSubtle {
			empty = true
		}
	})
	if !empty {
		t.Fatal("empty state missing")
	}
}

func TestTimePatchTouchesOnlyTimeNodes(t *testing.T) {
	t.Parallel()
	p := TimePatch([]Reading{{Zone: "UTC", Clock: "16:00", Offset: "UTC+0"}})
	if len(p) != 2 {
		t.Fatalf("replacements = %d", len(p))
	}
	for _, r := range p {
		// The bar patch replaces a button (the whole control is clickable);
		// the panel patches replace plain text.
		want := v1.KindButton
		if r.Key != "time" {
			want = v1.KindText
		}
		if r.Node.Kind != want {
			t.Fatalf("patched %s for %s, want %s", r.Node.Kind, r.Key, want)
		}
		if r.Key == "time" && r.Node.ID != "open" {
			t.Fatalf("bar patch lost the open action: %+v", r.Node)
		}
	}
}

func TestBarTreeIsARow(t *testing.T) {
	t.Parallel()
	root := BarTree(Reading{Zone: "UTC", Clock: "15:04"})
	if err := v1.Validate(root, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
}

func walk(n *v1.Node, fn func(*v1.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		walk(c, fn)
	}
}
