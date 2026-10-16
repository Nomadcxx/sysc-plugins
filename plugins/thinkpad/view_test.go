package thinkpad

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// snapshots covers the same branches as ExampleStates, which the
// screenshot tool paints to PNG so the layout can be reviewed by eye too.
func snapshots() map[string]Snapshot { return ExampleStates() }

func eachSnapshot(t *testing.T, fn func(t *testing.T, name string, snap Snapshot)) {
	t.Helper()
	for name, snap := range snapshots() {
		fn(t, name, snap)
	}
}

func TestPanelLintsInEveryBranch(t *testing.T) {
	cfg := DefaultConfig()
	eachSnapshot(t, func(t *testing.T, name string, snap Snapshot) {
		tree := PanelTree(snap, DefaultInstance(), cfg, "", "")
		for _, finding := range lint.Tree(tree, v1.ViewPanel, panelWidth, PanelPreferredHeight(snap, "")) {
			t.Errorf("panel %s: %s", name, finding)
		}
	})
}

func TestPanelWithLogoLints(t *testing.T) {
	cfg := DefaultConfig()
	logo := filepath.Join(t.TempDir(), "thinkpad.png")
	if err := os.WriteFile(logo, []byte("not really a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	eachSnapshot(t, func(t *testing.T, name string, snap Snapshot) {
		tree := PanelTree(snap, DefaultInstance(), cfg, logo, "night")
		for _, finding := range lint.Tree(tree, v1.ViewPanel, panelWidth, PanelPreferredHeight(snap, "night")) {
			t.Errorf("panel %s with logo: %s", name, finding)
		}
	})
}

func TestSettingsPanelLints(t *testing.T) {
	for _, finding := range lint.Tree(SettingsPanelTree(), v1.ViewPanel, panelWidth, 720) {
		t.Errorf("settings panel: %s", finding)
	}
}

func TestBarLintsAtEveryWidth(t *testing.T) {
	cfg := DefaultConfig()
	inst := DefaultInstance()
	eachSnapshot(t, func(t *testing.T, name string, snap Snapshot) {
		for _, width := range []int{80, 119, 120, 160, 200, 240, 320, 400} {
			tree := BarTree(snap, inst, cfg, width)
			for _, finding := range lint.Tree(tree, v1.ViewBar, width, 32) {
				t.Errorf("bar %s at %d: %s", name, width, finding)
			}
		}
	})
}

func TestBarNeverOverrunsItsWidth(t *testing.T) {
	cfg := DefaultConfig()
	inst := DefaultInstance()
	eachSnapshot(t, func(t *testing.T, name string, snap Snapshot) {
		for _, width := range []int{80, 119, 120, 150, 200, 240, 320, 480} {
			tree := BarTree(snap, inst, cfg, width)
			got := barWidth(tree.Children) + 2*tree.Padding
			if got > width {
				t.Errorf("bar %s at %d: children need %d", name, width, got)
			}
		}
	})
}

func TestBarFanGaugeAndRPMVisibilityAreIndependent(t *testing.T) {
	snap := snapshots()["full"]
	cfg := DefaultConfig()
	for _, tc := range []struct {
		name    string
		showFan bool
		showRPM bool
		width   int
		wantFan bool
		wantRPM bool
	}{
		{name: "both", showFan: true, showRPM: true, width: 240, wantFan: true, wantRPM: true},
		{name: "fan gauge only", showFan: true, width: 240, wantFan: true},
		{name: "RPM only", showRPM: true, width: 240, wantRPM: true},
		{name: "compact RPM hidden", width: 120},
		{name: "compact RPM shown", showRPM: true, width: 120, wantRPM: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := Instance{ShowFan: tc.showFan, ShowRPM: tc.showRPM}
			root := BarTree(snap, inst, cfg, tc.width)
			gotFan, gotRPM := false, false
			walk(root, func(node *v1.Node) {
				gotFan = gotFan || node.Kind == v1.KindGauge
				gotRPM = gotRPM || node.Key == "bar-rpm"
			})
			if gotFan != tc.wantFan || gotRPM != tc.wantRPM {
				t.Errorf("bar fan visibility = (%v, %v), want (%v, %v)",
					gotFan, gotRPM, tc.wantFan, tc.wantRPM)
			}
		})
	}
}

func TestFanNoteDoesNotReportInvalidRPMAsZero(t *testing.T) {
	snap := snapshots()["full"]
	snap.State.FanValid = false
	snap.State.FanRPM = 0
	if got := fanNote(snap, true); got != "Fan speed unavailable." {
		t.Fatalf("fan note = %q, want an unavailable reading", got)
	}
}

func TestKeyboardHardwareChangeDoesNotClaimLidLightChanged(t *testing.T) {
	snap := snapshots()["full"]
	snap.ExternalKeyboard = true
	if got := keyboardNote(snap, true); got != "A hardware key changed the backlight." {
		t.Fatalf("keyboard note = %q, want the external keyboard-light change", got)
	}
	if got := lidNote(snap, true); got != "Lit while the lid logo is on." {
		t.Fatalf("lid note = %q, want the unchanged lid-light note", got)
	}
}

func TestTooltipLints(t *testing.T) {
	cfg := DefaultConfig()
	eachSnapshot(t, func(t *testing.T, name string, snap Snapshot) {
		tree := TooltipTree(snap, cfg)
		for _, finding := range lint.Tree(tree, v1.ViewTooltip, 280, 200) {
			t.Errorf("tooltip %s: %s", name, finding)
		}
	})
}

// The host drops a control row outright when one child overruns it, so keep
// the fan slider and its two explicit non-numeric modes inside the card.
func TestFanSliderAndModesFitTheCard(t *testing.T) {
	snap := snapshots()["full"]
	snap.State.FanLevel = "4"
	card := fanCard(snap)
	slider := findByID(card, "fan-slider")
	if slider == nil {
		t.Fatal("fan card has no level slider")
	}
	if slider.Role != "slider" || slider.Name != "Fan level" || slider.Min != 0 ||
		slider.Max != MaxNumericFanLevel || slider.Step != 1 || slider.Value != 4 || slider.Disabled {
		t.Errorf("fan slider = %+v", slider)
	}
	if len(slider.Events) != 1 || slider.Events[0] != v1.EventChange {
		t.Errorf("fan slider events = %v, want change", slider.Events)
	}
	for _, id := range []string{"fan:auto", "fan:disengaged"} {
		if findByID(card, id) == nil {
			t.Errorf("fan mode button %q is missing", id)
		}
	}
	for _, id := range []string{"fan:0", "fan:1", "fan:2", "fan:3", "fan:4", "fan:5", "fan:6", "fan:7"} {
		if findByID(card, id) != nil {
			t.Errorf("numeric fan level still has a separate button: %q", id)
		}
	}

	// The fan needs its own write permission; another writable light must not
	// make a read-only fan slider look active.
	snap.State.FanWritable = false
	snap.State.Keyboard.Writable = true
	if got := findByID(fanCard(snap), "fan-slider"); got == nil || !got.Disabled {
		t.Errorf("read-only fan slider = %+v, want disabled", got)
	}
}

func TestDisabledFanStatusIsVisibleWithoutBlockingLevelSelection(t *testing.T) {
	snap := snapshots()["full"]
	snap.State.FanStatus = "disabled"
	card := fanCard(snap)

	if got := fanLevelCaption(snap); got != "disabled" {
		t.Errorf("fan status caption = %q, want disabled", got)
	}
	note := fanNote(snap, true)
	if !strings.Contains(strings.ToLower(note), "fan is disabled") || !strings.Contains(note, "re-enabled") {
		t.Errorf("fan note = %q, want disabled state and what level changes mean", note)
	}
	if got := findByID(card, "fan-slider"); got == nil || got.Disabled {
		t.Errorf("fan slider = %+v, want usable for setting the level after re-enable", got)
	}
}

func TestKeyboardSliderUsesTheLEDRange(t *testing.T) {
	for _, maxBrightness := range []int{1, 2, 3, 4, 8} {
		snap := snapshots()["full"]
		snap.State.Keyboard = LEDState{Name: LEDKeyboardBacklight, Present: true,
			Brightness: 1, MaxBrightness: maxBrightness, Writable: true}
		card := keyboardCard(snap)
		slider := findByID(card, "kbd-slider")
		wantMax := min(maxBrightness, MaxKeyboardLevel)
		if slider == nil {
			t.Fatalf("max_brightness %d: keyboard slider is missing", maxBrightness)
		}
		if slider.Role != "slider" || slider.Min != 0 || slider.Max != float64(wantMax) ||
			slider.Step != 1 || slider.Value != 1 || slider.Disabled {
			t.Errorf("max_brightness %d: keyboard slider = %+v", maxBrightness, slider)
		}
		if toggle := findByID(card, "kbd:toggle"); toggle == nil {
			t.Errorf("max_brightness %d: keyboard on/off toggle is missing", maxBrightness)
		}
	}

	snap := snapshots()["full"]
	snap.State.FanWritable = true
	snap.State.Keyboard.Writable = false
	if got := findByID(keyboardCard(snap), "kbd-slider"); got == nil || !got.Disabled {
		t.Errorf("read-only keyboard slider = %+v, want disabled", got)
	}
}

func TestPartialSetupIsReportedWithoutDisablingWritableControls(t *testing.T) {
	snap := snapshots()["full"]
	snap.State.Keyboard.Writable = false
	if got := accessLabel(snap); got == "Controls ready" {
		t.Fatal("access label says Controls ready while the keyboard is read-only")
	}
	lid := findByID(lidCard(snap), nodeLidToggle)
	if lid == nil || lid.Disabled {
		t.Fatalf("writable lid toggle = %+v, want enabled while another control needs setup", lid)
	}
	setup := setupCard(snap)
	var title, body *v1.Node
	walk(setup, func(node *v1.Node) {
		switch node.Key {
		case "setup-title":
			title = node
		case "setup-why":
			body = node
		}
	})
	if title == nil || title.Text != "Some controls need setup" {
		t.Fatalf("setup title = %+v, want partial-setup label", title)
	}
	if body == nil || !strings.Contains(body.Text, "fan option or write permission") {
		t.Fatalf("setup body = %+v, want the missing fan option or permission explained", body)
	}
}

func TestBarUsesTheThinkPadWordmarkAndDeclaresPointerInput(t *testing.T) {
	logo := filepath.Join(t.TempDir(), "thinkpad.png")
	if err := os.WriteFile(logo, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LogoPath = logo
	root := BarTree(snapshots()["full"], DefaultInstance(), cfg, 240)
	open := findByID(root, "open")
	if open == nil || open.Name != "Open the ThinkPad panel" || open.Role != "button" {
		t.Fatalf("bar opener = %+v", open)
	}
	hasActivate, hasPointer := false, false
	for _, event := range open.Events {
		hasActivate = hasActivate || event == v1.EventActivate
		hasPointer = hasPointer || event == v1.EventPointer
	}
	if !hasActivate || !hasPointer {
		t.Errorf("bar opener events = %v, want activate and pointer", open.Events)
	}
	if len(open.Children) != 1 || open.Children[0].Kind != v1.KindImage || open.Children[0].Path != logo {
		t.Errorf("bar opener children = %+v, want the ThinkPad wordmark image", open.Children)
	} else if open.Children[0].ImageW != 44 || open.Children[0].ImageH != 16 {
		t.Errorf("bar wordmark dimensions = %dx%d, want 44x16 to preserve its proportions",
			open.Children[0].ImageW, open.Children[0].ImageH)
	}

	var heroMark *v1.Node
	walk(hero(snapshots()["full"], cfg, logo), func(node *v1.Node) {
		if heroMark == nil && node.Kind == v1.KindImage {
			heroMark = node
		}
	})
	if heroMark == nil || heroMark.ImageW != 122 || heroMark.ImageH != 44 {
		t.Errorf("hero wordmark = %+v, want a 122x44 image with the official aspect ratio", heroMark)
	}
}

func TestBundledThinkPadWordmarkHasTheOfficialRasterShape(t *testing.T) {
	file, err := os.Open("assets/thinkpad.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	logo, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode bundled ThinkPad mark: %v", err)
	}
	if got := logo.Bounds().Size(); got.X != 94 || got.Y != 34 {
		t.Fatalf("bundled mark size = %v, want the official 94x34 source size", got)
	}
	_, _, _, alpha := logo.At(0, 0).RGBA()
	if alpha == 0xffff {
		t.Fatal("bundled mark has an opaque background")
	}
}

// Every Height in these builders already includes the node's own padding; the
// panel request must equal the arithmetic the tree actually occupies, or the
// host keeps resizing to a height that does not fit.
func TestPanelPreferredHeightMatchesTheTree(t *testing.T) {
	cfg := DefaultConfig()
	eachSnapshot(t, func(t *testing.T, name string, snap Snapshot) {
		tree := PanelTree(snap, DefaultInstance(), cfg, "", "")
		preferred := PanelPreferredHeight(snap, "")
		if got := measure(t, tree); got > preferred {
			t.Errorf("panel %s: tree measures %d but only %d was requested",
				name, got, preferred)
		}
		// A panel with one profile is legitimately shorter than the manifest's
		// declared height; the manifest is only the first paint.
		if preferred < 460 {
			t.Errorf("panel %s: requested height %d is implausibly short", name, preferred)
		}
	})
}

// measure walks the tree the way the host does: a container's Height includes
// its padding, and a column without an explicit Height is as tall as its
// children plus their gaps.
func measure(t *testing.T, node *v1.Node) int {
	t.Helper()
	if node.Height > 0 {
		return node.Height
	}
	switch node.Kind {
	case v1.KindColumn, v1.KindRow:
		total := 0
		for i, child := range node.Children {
			if i > 0 {
				total += node.Gap
			}
			total += measure(t, child)
		}
		return total
	default:
		return 16
	}
}

func TestNodeCountIsStableAcrossValueChanges(t *testing.T) {
	// Poll-to-poll transitions must not reshape the tree: a revision that
	// changes the node count makes the host rebuild retained state, so a fan
	// reading ticking once a second would thrash it forever.
	cfg := DefaultConfig()
	base := snapshots()["full"]

	count := func(snap Snapshot) int {
		return countNodes(PanelTree(snap, DefaultInstance(), cfg, "", ""))
	}
	reference := count(base)

	mutations := map[string]func(*Snapshot){
		"rpm zero":    func(s *Snapshot) { s.State.FanRPM = 0 },
		"rpm high":    func(s *Snapshot) { s.State.FanRPM = 9000 },
		"fan invalid": func(s *Snapshot) { s.State.FanValid = false },
		"fan level 5": func(s *Snapshot) { s.State.FanLevel = "5" },
		"fan full":    func(s *Snapshot) { s.State.FanLevel = FanLevelDisengaged },
		"kbd off": func(s *Snapshot) {
			s.State.Keyboard = LEDState{Name: LEDKeyboardBacklight, Present: true, MaxBrightness: 3, Writable: true}
		},
		"lid off":    func(s *Snapshot) { s.State.Lid = LEDState{Name: LEDLidLogoDot, Present: true, Writable: true} },
		"temp gone":  func(s *Snapshot) { s.State.CPUTempValid = false },
		"temp hot":   func(s *Snapshot) { s.State.CPUTempC = 99 },
		"busy":       func(s *Snapshot) { s.Busy = true },
		"external":   func(s *Snapshot) { s.External = true },
		"no active":  func(s *Snapshot) { s.Active = "" },
		"want moved": func(s *Snapshot) { s.Want.KeyboardLevel = 3 },
	}
	for name, mutate := range mutations {
		snap := base
		mutate(&snap)
		if got := count(snap); got != reference {
			t.Errorf("%s: node count %d, reference %d", name, got, reference)
		}
	}
}

func TestPanelTreeIsDeterministic(t *testing.T) {
	cfg := DefaultConfig()
	first := PanelTree(snapshots()["full"], DefaultInstance(), cfg, "", "night")
	second := PanelTree(snapshots()["full"], DefaultInstance(), cfg, "", "night")
	if countNodes(first) != countNodes(second) {
		t.Errorf("panel is not deterministic: %d then %d nodes",
			countNodes(first), countNodes(second))
	}
}

// Capability changes may reshape the tree -- a backlight grid that grows with
// max_brightness is telling the truth -- but the result must still be a valid
// panel, which the lint tests above cover for every branch.

// The manifest declares the panel height the host paints before the plugin can
// ask for anything. Tying it to the seeded three-profile layout keeps the two
// from drifting apart silently.
func TestManifestHeightMatchesTheSeededLayout(t *testing.T) {
	snap := snapshots()["full"]
	if got := PanelPreferredHeight(snap, ""); got != panelHeight {
		t.Errorf("seeded layout measures %d but the manifest declares %d",
			got, panelHeight)
	}
}

func countNodes(node *v1.Node) int {
	if node == nil {
		return 0
	}
	total := 1
	for _, child := range node.Children {
		total += countNodes(child)
	}
	return total
}

func TestInteractiveNodesAreNamed(t *testing.T) {
	cfg := DefaultConfig()
	eachSnapshot(t, func(t *testing.T, name string, snap Snapshot) {
		trees := map[string]*v1.Node{
			"panel":    PanelTree(snap, DefaultInstance(), cfg, "", ""),
			"settings": SettingsPanelTree(),
			"bar":      BarTree(snap, DefaultInstance(), cfg, 240),
		}
		for surface, tree := range trees {
			walk(tree, func(node *v1.Node) {
				if !interactive(node) {
					return
				}
				if node.ID == "" {
					t.Errorf("%s %s: interactive node has no id", surface, name)
				}
				if node.Name == "" || node.Role == "" || len(node.Events) == 0 {
					t.Errorf("%s %s: node %q is interactive but not fully described",
						surface, name, node.ID)
				}
			})
		}
	})
}

func interactive(node *v1.Node) bool {
	switch node.Kind {
	case v1.KindButton, v1.KindTextInput, v1.KindDragSource, v1.KindSlider:
		return true
	default:
		return false
	}
}

func walk(node *v1.Node, fn func(*v1.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for _, child := range node.Children {
		walk(child, fn)
	}
}

// findByID returns the first node carrying id, or nil. Node ids are unique per
// revision, so the first match is the only match.
func findByID(node *v1.Node, id string) *v1.Node {
	var found *v1.Node
	walk(node, func(n *v1.Node) {
		if found == nil && n.ID == id {
			found = n
		}
	})
	return found
}

func TestEveryInteractiveIdIsHandled(t *testing.T) {
	// An id the process loop does not dispatch is a dead control: it looks live
	// and silently does nothing.
	handled := []string{"open", "settings", "back", "close", "lid", "recheck",
		"setup:copy", "profile:save", "profile:rename",
		"fan:", "kbd:", "fan-slider", "kbd-slider",
		"profile:apply:", "profile:delete:", "profile:pick:"}
	cfg := DefaultConfig()
	snap := snapshots()["full"]

	tree := PanelTree(snap, DefaultInstance(), cfg, "", "")
	bar := BarTree(snap, DefaultInstance(), cfg, 240)
	for _, root := range []*v1.Node{tree, bar} {
		walk(root, func(node *v1.Node) {
			if !interactive(node) || node.ID == "" {
				return
			}
			if node.Kind == v1.KindTextInput {
				return // handled through its change and submit events
			}
			found := false
			for _, prefix := range handled {
				if node.ID == prefix || strings.HasPrefix(node.ID, prefix) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("node id %q is never dispatched", node.ID)
			}
		})
	}
}

func TestLogoPathPrefersTheOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "custom.png")
	if err := os.WriteFile(override, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LogoPath = override
	if got := LogoPath(cfg, ""); got != override {
		t.Errorf("LogoPath = %q, want the override %q", got, override)
	}

	// A directory is not an image; falling through matters more than trying.
	cfg.LogoPath = dir
	if got := LogoPath(cfg, ""); got != "" {
		t.Errorf("LogoPath = %q for a directory, want empty", got)
	}
}

func TestHeroFallsBackToTheGlyphWithoutAnAsset(t *testing.T) {
	cfg := DefaultConfig()
	tree := hero(snapshots()["full"], cfg, "")
	found := false
	walk(tree, func(node *v1.Node) {
		if node.Kind == v1.KindIcon && node.Icon == "laptop" {
			found = true
		}
		if node.Kind == v1.KindImage {
			t.Errorf("an image node was emitted with no path: %q", node.Path)
		}
	})
	if !found {
		t.Error("no laptop glyph in the hero fallback")
	}
}

// A machine with no tpacpi backlight used to draw a full-width "1" button on a
// light that does not exist, and the header toggle read "Off" as if it were
// switchable. Both were found by rendering absent.png and looking at it.
func TestAbsentBacklightOffersNoLevels(t *testing.T) {
	snap := ExampleStates()["absent"]
	if snap.State.Keyboard.Present {
		t.Fatalf("fixture is not the absent branch: %+v", snap.State.Keyboard)
	}
	if levels := keyboardSliderMax(snap.State.Keyboard); levels != 0 {
		t.Errorf("an absent backlight offers slider maximum %d, want none", levels)
	}

	tree := PanelTree(snap, DefaultInstance(), DefaultConfig(), "", "")
	if findByID(tree, "kbd-slider") != nil {
		t.Error("an absent backlight offers a slider")
	}
	for _, id := range []string{prefixKeyboard + "off", prefixKeyboard + "1", prefixKeyboard + "2"} {
		if node := findByID(tree, id); node != nil {
			t.Errorf("absent backlight rendered a level button %q", id)
		}
	}
	header := findByID(tree, prefixKeyboard+"toggle")
	if header == nil {
		t.Fatal("keyboard toggle disappeared entirely; the card must stay readable")
	}
	if header.Text != "None" {
		t.Errorf("absent backlight toggle reads %q, want %q", header.Text, "None")
	}
}

// The absent card must lose the grid row's height, or the panel reserves a
// band of empty space where the controls would have been.
func TestAbsentBacklightShortensItsCard(t *testing.T) {
	snap := ExampleStates()["absent"]
	present := ExampleStates()["full"]
	if got, want := keyboardCardHeightFor(snap), keyboardCardHeightFor(present); got >= want {
		t.Errorf("absent keyboard card height %d, want less than the present card's %d", got, want)
	}
	if got, want := keyboardCardHeightFor(snap), keyboardCardHeight-keyboardRowH-cardGap; got != want {
		t.Errorf("absent keyboard card height = %d, want %d", got, want)
	}
}
