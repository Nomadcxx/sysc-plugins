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
