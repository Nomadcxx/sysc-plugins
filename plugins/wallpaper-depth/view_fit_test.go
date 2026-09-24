package wallpaperdepth

import (
	"slices"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestViewsFitAtHostSizes(t *testing.T) {
	ready := HelperStatus{Ready: true, RuntimeReady: true, ModelReady: true}
	settings := Settings{AutoGenerate: true, Threshold: 30, Feather: 8}
	states := map[string]ControllerSnapshot{
		"checking": {
			Busy: true, Operation: string(operationCheck), Settings: settings,
		},
		"setup missing": {
			Checked: true, Settings: settings,
		},
		"setup running": {
			Checked: true, Busy: true, Operation: string(operationSetup), Settings: settings,
		},
		"ready with no outputs": {
			Checked: true, Helper: ready, Settings: settings,
		},
		"output states": {
			Checked: true, Helper: ready, Settings: settings,
			Rows: []OutputRow{
				{Output: "DP-1", State: "image", WallpaperPath: "/wall-a.jpg", Status: "processing"},
				{Output: "DP-2", State: "image", WallpaperPath: "/wall-b.jpg", Status: "ready", MaskPath: "/mask/b.png"},
				{Output: "DP-3", State: "video", WallpaperPath: "/movie.mp4", Status: "unsupported"},
				{Output: "DP-4", State: "covered", Status: "unsupported"},
				{Output: "DP-5", State: "image", WallpaperPath: "/wall-c.jpg", Status: "waiting"},
			},
		},
		"helper error": {
			Checked: true, Error: "helper status failed", Settings: settings,
		},
		"cache hit": {
			Checked: true, Helper: ready, Settings: settings,
			Rows: []OutputRow{{Output: "DP-1", State: "image", WallpaperPath: "/wall.jpg", Status: "ready", MaskPath: "/mask.png", CacheHit: true, ElapsedMs: 1280}},
		},
		"output error": {
			Checked: true, Helper: ready, Settings: settings,
			Rows: []OutputRow{{Output: "DP-1", State: "image", WallpaperPath: "/wall.jpg", Status: "error", Error: "inference failed: model output was invalid"}},
		},
		"busy buttons": {
			Checked: true, Helper: ready, Busy: true, Operation: string(operationGenerate), Settings: settings,
			Rows: []OutputRow{{Output: "DP-1", State: "image", WallpaperPath: "/wall.jpg", Status: "processing"}},
		},
	}

	for name, snapshot := range states {
		t.Run(name, func(t *testing.T) {
			checkView(t, BarTree(snapshot), v1.ViewBar, lint.BarWidth, lint.BarHeight)
			checkView(t, TooltipTree(snapshot), v1.ViewTooltip, lint.TooltipWidth, lint.TooltipHeight)
			checkView(t, PanelTree(snapshot), v1.ViewPanel, 500, 560)
		})
	}
}

func TestBarIsGlyphOnlyAcrossStates(t *testing.T) {
	states := []struct {
		name string
		snap ControllerSnapshot
		tone v1.Tone
	}{
		{"ready", ControllerSnapshot{Checked: true, Helper: HelperStatus{Ready: true}}, v1.ToneNormal},
		{"processing", ControllerSnapshot{Busy: true, Operation: string(operationGenerate)}, v1.ToneAccent},
		{"setup required", ControllerSnapshot{Checked: true}, v1.ToneSubtle},
		{"error", ControllerSnapshot{Error: "helper failed"}, v1.ToneError},
	}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			root := BarTree(state.snap)
			var button *v1.Node
			walkView(root, func(node *v1.Node) {
				if node.Kind == v1.KindText || node.Text != "" {
					t.Errorf("bar includes text in state %s: %+v", state.name, node)
				}
				if node.Kind == v1.KindButton {
					button = node
				}
			})
			if button == nil || button.Icon != "wallpaper" || button.Tone != state.tone {
				t.Fatalf("bar control = %+v, want wallpaper glyph with tone %q", button, state.tone)
			}
		})
	}
}

func TestPanelRendersRequiredStatesAndOutputRows(t *testing.T) {
	ready := HelperStatus{Ready: true, RuntimeReady: true, ModelReady: true}
	settings := Settings{AutoGenerate: true, Threshold: 30, Feather: 8}
	cases := []struct {
		name string
		snap ControllerSnapshot
		want []string
	}{
		{"checking", ControllerSnapshot{Busy: true, Operation: string(operationCheck)}, []string{"Checking"}},
		{"setup missing", ControllerSnapshot{Checked: true}, []string{"Setup required"}},
		{"setup running", ControllerSnapshot{Checked: true, Busy: true, Operation: string(operationSetup)}, []string{"Setting up"}},
		{"ready empty", ControllerSnapshot{Checked: true, Helper: ready, Settings: settings}, []string{"No outputs"}},
		{"helper error", ControllerSnapshot{Checked: true, Error: "helper status failed"}, []string{"helper status failed"}},
		{"cache hit", ControllerSnapshot{Checked: true, Helper: ready, Settings: settings,
			Rows: []OutputRow{{Output: "DP-1", State: "image", Status: "ready", CacheHit: true, ElapsedMs: 1280}}}, []string{"cached", "1.3 s"}},
		{"bulk action", ControllerSnapshot{Checked: true, Helper: ready, Settings: settings,
			Rows: []OutputRow{{Output: "DP-1", State: "image", WallpaperPath: "/wall.jpg", Status: "waiting"}}}, []string{"Generate masks"}},
		{"setup disclosure", ControllerSnapshot{Checked: true, Settings: settings}, []string{"Python runtime setup needed", "Depth model download needed", "99 MB", "internet is needed once", "stay local"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := viewText(PanelTree(tc.snap))
			for _, text := range tc.want {
				if !strings.Contains(body, text) {
					t.Errorf("panel %q missing %q", body, text)
				}
			}
		})
	}

	rows := ControllerSnapshot{Checked: true, Helper: ready, Settings: settings, Rows: []OutputRow{
		{Output: "DP-1", State: "image", Status: "processing"},
		{Output: "DP-2", State: "image", Status: "ready"},
		{Output: "DP-3", State: "video", Status: "unsupported"},
		{Output: "DP-4", State: "covered", Status: "unsupported"},
		{Output: "DP-5", State: "image", Status: "waiting"},
	}}
	body := viewText(PanelTree(rows))
	for _, want := range []string{"Outputs", "Settings below", "Automatic on", "DP-1", "Processing", "DP-2", "Ready", "DP-3", "Video", "DP-4", "Covered", "DP-5", "Waiting"} {
		if !strings.Contains(body, want) {
			t.Errorf("output panel %q missing %q", body, want)
		}
	}
}

func TestOutputRowOffersGenerateOnlyForCurrentImage(t *testing.T) {
	tests := []struct {
		name string
		row  OutputRow
		want bool
	}{
		{name: "current image", row: OutputRow{Output: "DP-1", State: "image", WallpaperPath: "/wall.jpg"}, want: true},
		{name: "missing wallpaper path", row: OutputRow{Output: "DP-1", State: "image"}},
		{name: "video", row: OutputRow{Output: "DP-1", State: "video", WallpaperPath: "/movie.mp4"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := false
			walkView(outputRow(tc.row, false), func(node *v1.Node) {
				got = got || node.ID == "generate-"+tc.row.Output
			})
			if got != tc.want {
				t.Fatalf("Generate action present = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestPanelActionIdentityStaysStableAndBusyActionsDisable(t *testing.T) {
	ready := HelperStatus{Ready: true, RuntimeReady: true, ModelReady: true}
	base := ControllerSnapshot{Checked: true, Helper: ready, Settings: Settings{AutoGenerate: true, Threshold: 30, Feather: 8},
		Rows: []OutputRow{{Output: "DP-1", State: "image", WallpaperPath: "/wall.jpg", Status: "waiting"}}}
	busy := base
	busy.Busy, busy.Operation = true, string(operationGenerate)
	baseIDs, busyIDs := interactiveIDs(PanelTree(base)), interactiveIDs(PanelTree(busy))
	if !slices.Contains(baseIDs, "generate-all") {
		t.Fatalf("panel actions %v missing the bulk Generate action", baseIDs)
	}
	var bulk *v1.Node
	walkView(PanelTree(base), func(node *v1.Node) {
		if node.ID == "generate-all" {
			bulk = node
		}
	})
	if bulk == nil || bulk.Fill != "accent" {
		t.Fatalf("bulk action = %+v, want the primary accent action", bulk)
	}
	if !slices.Equal(baseIDs, busyIDs) {
		t.Fatalf("panel action IDs changed while busy: %v vs %v", baseIDs, busyIDs)
	}
	walkView(PanelTree(busy), func(node *v1.Node) {
		if node.Kind == v1.KindButton && !node.Disabled {
			t.Errorf("busy button %q is enabled", node.ID)
		}
	})
}

func checkView(t *testing.T, root *v1.Node, kind v1.ViewKind, width, height int) {
	t.Helper()
	if err := v1.Validate(root, kind); err != nil {
		t.Fatalf("%s validation: %v", kind, err)
	}
	for _, finding := range lint.Tree(root, kind, width, height) {
		t.Errorf("%s fit: %s", kind, finding)
	}
	walkView(root, func(node *v1.Node) {
		if isInteractiveViewNode(node.Kind) && (node.ID == "" || node.Name == "" || node.Role == "" || len(node.Events) == 0) {
			t.Errorf("interactive node has incomplete accessible identity: %+v", node)
		}
	})
}

func interactiveIDs(root *v1.Node) []string {
	var ids []string
	walkView(root, func(node *v1.Node) {
		if isInteractiveViewNode(node.Kind) {
			ids = append(ids, node.ID)
		}
	})
	slices.Sort(ids)
	return ids
}

func isInteractiveViewNode(kind v1.NodeKind) bool {
	return kind == v1.KindButton || kind == v1.KindTextInput || kind == v1.KindDragSource
}

func walkView(root *v1.Node, visit func(*v1.Node)) {
	if root == nil {
		return
	}
	visit(root)
	for _, child := range root.Children {
		walkView(child, visit)
	}
}

func viewText(root *v1.Node) string {
	var text []string
	walkView(root, func(node *v1.Node) {
		if node.Text != "" {
			text = append(text, node.Text)
		}
	})
	return strings.Join(text, " ")
}
