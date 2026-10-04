package screenshot

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	panelW = 360
	panelH = 260
)

func buttonIDs(n *v1.Node, out *[]string) {
	if n.Kind == v1.KindButton && n.ID != "" {
		*out = append(*out, n.ID)
	}
	for _, c := range n.Children {
		buttonIDs(c, out)
	}
}

func TestPanelListsItsRowsInOrder(t *testing.T) {
	var ids []string
	buttonIDs(PanelTree(Model{Directory: "/home/u/Pictures/Screenshots"}), &ids)
	want := []string{NodeClose, NodeRegion, NodeWindow, NodeScreen, NodeFolder}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("buttons = %v, want %v", ids, want)
	}
}

func TestBarIsOneCameraButton(t *testing.T) {
	var ids []string
	root := BarTree()
	buttonIDs(root, &ids)
	if !reflect.DeepEqual(ids, []string{NodeOpen}) {
		t.Fatalf("bar buttons = %v", ids)
	}
	if root.Children[0].Icon != "camera" || root.Children[0].Name != "Screenshot" {
		t.Fatalf("bar button = %+v", root.Children[0])
	}
}

func TestBarFitsSideWidths(t *testing.T) {
	bar := BarTree()
	for _, width := range []int{shelllint.BarWidth, 28, 32, 64} {
		for _, finding := range shelllint.Tree(bar, v1.ViewBar, width, shelllint.BarHeight) {
			t.Errorf("width %d: %s", width, finding)
		}
	}
	button := bar.Children[0]
	if button == nil || button.Name != "Screenshot" || button.Role != "button" || len(button.Events) != 1 || button.Events[0] != v1.EventActivate {
		t.Fatalf("bar interaction = %+v", button)
	}
}

func TestViewsPassTheHostLint(t *testing.T) {
	for name, m := range map[string]Model{
		"idle":       {Directory: "/home/u/Pictures/Screenshots"},
		"no caption": {},
		"error":      {Directory: "/home/u/Pictures", Error: "a region selector is already open"},
		"long path":  {Directory: "/home/someone/Pictures/Screenshots/with/a/very/deep/tree/of/folders"},
		"cjk path":   {Directory: "/home/ユーザー/ピクチャ/スクリーンショット/とても/深い/フォルダ/の/階層/です"},
	} {
		for _, f := range shelllint.Tree(PanelTree(m), v1.ViewPanel, panelW, panelH) {
			t.Errorf("%s panel: %s", name, f)
		}
	}
	for _, f := range shelllint.Tree(BarTree(), v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
		t.Errorf("bar: %s", f)
	}
	for _, f := range shelllint.Tree(TooltipTree(), v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
		t.Errorf("tooltip: %s", f)
	}
}

func TestTooltipIsReadOnlyText(t *testing.T) {
	var ids []string
	root := TooltipTree()
	buttonIDs(root, &ids)
	if len(ids) != 0 {
		t.Fatalf("a tooltip holds buttons %v; the host rejects that", ids)
	}
	if !hasTextNode(root, "Screenshot") {
		t.Fatalf("tooltip does not say what the button is: %+v", root)
	}
}

func hasTextNode(n *v1.Node, text string) bool {
	if n.Kind == v1.KindText && n.Text == text {
		return true
	}
	for _, c := range n.Children {
		if hasTextNode(c, text) {
			return true
		}
	}
	return false
}

func TestShortenPath(t *testing.T) {
	tests := []struct {
		name, in string
		max      int
		want     string
	}{
		{"fits", "/home/u/Pictures", 40, "/home/u/Pictures"},
		{"exact", "abcdef", 6, "abcdef"},
		{"middle", "/home/u/Pictures/Screenshots", 16, "/home/u…eenshots"},
		{"zero", "abcdef", 0, ""},
		{"one", "abcdef", 1, "…"},
		{"two", "abcdef", 2, "…f"},
	}
	for _, tc := range tests {
		if got := ShortenPath(tc.in, tc.max); got != tc.want {
			t.Errorf("%s: ShortenPath(%q,%d) = %q, want %q", tc.name, tc.in, tc.max, got, tc.want)
		}
	}
	got := ShortenPath("/home/ユーザー/Pictures/Screenshots/with/a/very/deep/tree/of/folders", 24)
	if len([]rune(got)) != 24 || !strings.Contains(got, "…") || !utf8.ValidString(got) {
		t.Errorf("non-ASCII path shortened to %q, want 24 valid runes with an ellipsis", got)
	}
}

func TestCaptionIsNotDrawnWithoutADirectory(t *testing.T) {
	var found bool
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n.Kind == v1.KindText && strings.HasPrefix(n.Text, "/") {
			found = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(PanelTree(Model{}))
	if found {
		t.Fatal("a caption was drawn with no directory")
	}
}
