package timer

import "testing"

func TestBarTreeHasAccessibleControls(t *testing.T) {
	t.Parallel()
	root := BarTree("04:12", false)
	if root.Kind != "row" || len(root.Children) != 3 {
		t.Fatalf("%+v", root)
	}
	start := root.Children[1]
	if start.Name == "" || start.Role == "" || start.ID != "start" {
		t.Fatalf("start = %+v", start)
	}
}

func TestPanelTreeIncludesDurationField(t *testing.T) {
	t.Parallel()
	root := PanelTree("05:00", "5m", false)
	if root.Kind != "column" {
		t.Fatal(root.Kind)
	}
	found := false
	for _, c := range root.Children {
		if c.ID == "duration" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing duration field")
	}
}
