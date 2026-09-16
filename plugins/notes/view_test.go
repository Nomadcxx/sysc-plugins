package notes

import (
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestBarTreeOpensPanel(t *testing.T) {
	t.Parallel()
	root := BarTree()
	if err := v1.Validate(root, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if root.Kind != v1.KindRow || len(root.Children) != 1 || root.Children[0].ID != "open" {
		t.Fatalf("%+v", root)
	}
}

func TestPanelListHasStableKeysAndActions(t *testing.T) {
	t.Parallel()
	root := PanelTree(Snapshot{
		Page:  "list",
		Notes: []Note{{Name: "a.md", Pinned: true}, {Name: "b.md"}},
	})
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	var list, key, pin, rm, neu bool
	walk(root, func(n *v1.Node) {
		if n.Kind == v1.KindList {
			list = true
		}
		if n.Key == "row:a.md" {
			key = true
		}
		if n.ID == "pin:a.md" {
			pin = true
		}
		if n.ID == "rm:b.md" {
			rm = true
		}
		if n.ID == "new" {
			neu = true
		}
	})
	if !list || !key || !pin || !rm || !neu {
		t.Fatalf("list=%v key=%v pin=%v rm=%v new=%v", list, key, pin, rm, neu)
	}
}

func TestPanelEditorIsMultilineWithCounts(t *testing.T) {
	t.Parallel()
	root := PanelTree(Snapshot{
		Page: "editor", Current: "a.md", Buffer: "hi there", Title: "a",
		Reseed: 4, Status: "dirty", Words: 2, Chars: 8,
	})
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	var body, multi, status, back bool
	walk(root, func(n *v1.Node) {
		if n.ID == "body" {
			body = true
			multi = n.Multiline && n.Key == "body" && n.Reseed == 4
		}
		if n.Key == "status" && n.Text != "" {
			status = true
		}
		if n.ID == "back" {
			back = true
		}
	})
	if !body || !multi || !status || !back {
		t.Fatalf("body=%v multi=%v status=%v back=%v", body, multi, status, back)
	}
}

func TestPanelErrorShowsSaveFailure(t *testing.T) {
	t.Parallel()
	root := PanelTree(Snapshot{Page: "editor", Current: "a.md", Buffer: "x", SaveErr: "read-only"})
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	found := false
	walk(root, func(n *v1.Node) {
		if n.Tone == v1.ToneError && n.Text == "read-only" {
			found = true
		}
	})
	if !found {
		t.Fatal("missing error text")
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
