package switcher

import (
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var testNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func TestBuildEmpty(t *testing.T) {
	root := Build(nil, testNow)
	if err := v1.Validate(root, v1.ViewFloating); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, c := range root.Children {
		if c.Kind == v1.KindList {
			t.Fatal("empty switcher should not render a list")
		}
	}
}

func TestBuildRowsNewestFirst(t *testing.T) {
	runs := []Run{
		{ID: "1", Name: "Old", Start: testNow.Add(-2 * time.Hour), CoverPath: "/tmp/old.png"},
		{ID: "2", Name: "New", Start: testNow.Add(-10 * time.Minute)},
	}
	root := Build(runs, testNow)
	if err := v1.Validate(root, v1.ViewFloating); err != nil {
		t.Fatalf("validate: %v", err)
	}
	list := find(t, root, "switcher-list")
	if len(list.Children) != 2 {
		t.Fatalf("rows = %d, want 2", len(list.Children))
	}
	first := list.Children[0].Children[0]
	if first.ID != "sw-open-2" {
		t.Fatalf("newest first: got %s", first.ID)
	}
	if first.Children[0].Children[0].Kind != v1.KindColumn {
		t.Fatal("run without cover should start at the info column")
	}
	second := list.Children[1].Children[0]
	if second.ID != "sw-open-1" {
		t.Fatalf("second row = %s", second.ID)
	}
	if second.Children[0].Children[0].Kind != v1.KindImage {
		t.Fatal("cover image missing for run with CoverPath")
	}
	var stops int
	collect(root, func(n *v1.Node) {
		if n.ID == "sw-stop-1" || n.ID == "sw-stop-2" {
			stops++
		}
	})
	if stops != 2 {
		t.Fatalf("stop buttons = %d, want 2", stops)
	}
}

func TestSwitcherFitsFloatingSurface(t *testing.T) {
	runs := []Run{{ID: "1", Name: "A Rather Long Game Name Here", Start: testNow, CoverPath: "/x.png"}}
	if findings := shelllint.Tree(Build(runs, testNow), v1.ViewFloating, 320, 400); len(findings) != 0 {
		for _, f := range findings {
			t.Errorf("lint: %v", f)
		}
	}
}

func find(t *testing.T, n *v1.Node, id string) *v1.Node {
	t.Helper()
	var out *v1.Node
	collect(n, func(c *v1.Node) {
		if c.ID == id {
			out = c
		}
	})
	if out == nil {
		t.Fatalf("node %q not found", id)
	}
	return out
}

func collect(n *v1.Node, fn func(*v1.Node)) {
	fn(n)
	for _, c := range n.Children {
		collect(c, fn)
	}
}
