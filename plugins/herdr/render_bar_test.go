package herdr

import (
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// checkLint fails the test for every reason the host would refuse the view.
func checkLint(t *testing.T, view v1.ViewKind, root *v1.Node, w, h int) {
	t.Helper()
	for _, f := range shelllint.Tree(root, view, w, h) {
		t.Errorf("%s lint: %s", view, f)
	}
}

func barStates() map[string]*Model {
	return map[string]*Model{
		"missing": {HerdrMissing: true},
		"empty":   {},
		"idle": modelWith(runningSess("alpha",
			Counts{Sessions: 1, Agents: 1},
			onePaneWS("work", pane("p1", "shell", StatusIdle)))),
		"attention": modelWith(runningSess("alpha",
			Counts{Sessions: 1, Agents: 3, Blocked: 1, Done: 1, Working: 1},
			onePaneWS("work", pane("p1", "shell", StatusBlocked)))),
		"manyattention": modelWith(runningSess("alpha",
			Counts{Sessions: 1, Agents: 110, Blocked: 60, Done: 50},
			onePaneWS("work", pane("p1", "shell", StatusBlocked)))),
		"working": modelWith(runningSess("alpha",
			Counts{Sessions: 1, Agents: 2, Working: 2},
			onePaneWS("work", pane("p1", "shell", StatusWorking)))),
		"stopped": modelWith(stoppedSess("old")),
	}
}

func TestBarLintsInEveryState(t *testing.T) {
	t.Parallel()
	settings := map[string]WidgetSettings{
		"standard": DefaultWidgetSettings(),
		"icon":     {DisplayMode: "icon"},
		"count0":   {DisplayMode: "icon_and_count", HideCountWhenZero: false},
	}
	for sname, m := range barStates() {
		for wname, ws := range settings {
			for _, width := range []int{240, 120, 64, 32} {
				bar := BarTree(m, ws, shelllint.BarHeight)
				if width != 240 {
					bar = BarTreeAtWidth(m, ws, shelllint.BarHeight, width)
				}
				checkLint(t, v1.ViewBar, bar, width, shelllint.BarHeight)
				if len(bar.Children) != 1 || bar.Children[0].ID != "open" {
					t.Fatalf("%s/%s: bar root = %+v", sname, wname, bar)
				}
			}
			checkLint(t, v1.ViewTooltip, TooltipTree(m, ws), shelllint.TooltipWidth, shelllint.TooltipHeight)
		}
	}
}

func TestBarButtonCarriesTheHerdrIcon(t *testing.T) {
	t.Parallel()
	tone := worstTone(barStates()["attention"])
	bar := BarTree(barStates()["attention"], DefaultWidgetSettings(), shelllint.BarHeight)
	open := bar.Children[0]
	if open.Kind != v1.KindButton || open.Name != "Herdr" || open.Role != "button" ||
		len(open.Events) != 1 || open.Events[0] != v1.EventActivate {
		t.Fatalf("bar interaction = %+v", open)
	}
	icon := open.Children[0]
	if icon.Kind != v1.KindIcon || icon.Icon != "ai-usage" || icon.IconSize != 16 ||
		icon.Key != "herdr-bar" || icon.Tone != tone {
		t.Fatalf("bar icon = %+v, want worst tone %q", icon, tone)
	}
	if tone != v1.ToneError {
		t.Fatalf("attention tone = %q, want error", tone)
	}
}

func TestBarCountTextRules(t *testing.T) {
	t.Parallel()
	m := barStates()["attention"] // 1 blocked + 1 done = 2
	if got := needsYouCount(m); got != 2 {
		t.Fatalf("needsYouCount = %d, want 2", got)
	}
	// Standard shows the count whenever it is non-zero.
	open := BarTree(m, DefaultWidgetSettings(), 32).Children[0]
	if len(open.Children) != 2 {
		t.Fatalf("standard children = %d, want icon + count", len(open.Children))
	}
	// Icon display mode never shows the number.
	open = BarTree(m, WidgetSettings{DisplayMode: "icon"}, 32).Children[0]
	if len(open.Children) != 1 {
		t.Fatalf("icon mode children = %d, want icon only", len(open.Children))
	}
	// HideCountWhenZero with no attention hides the zero.
	open = BarTree(barStates()["idle"], DefaultWidgetSettings(), 32).Children[0]
	if len(open.Children) != 1 {
		t.Fatalf("idle standard children = %d, want icon only", len(open.Children))
	}
}

// At 32px only the icon fits: a three-digit count cannot join it, so the
// button has exactly one child.
func TestCompact32ShowsIconOnly(t *testing.T) {
	t.Parallel()
	m := barStates()["manyattention"] // 110 needs you
	if got := needsYouCount(m); got != 110 {
		t.Fatalf("needsYouCount = %d, want 110", got)
	}
	open := BarTreeAtWidth(m, DefaultWidgetSettings(), 32, 32).Children[0]
	if open.Padding != 0 {
		t.Fatalf("compact padding = %d, want 0", open.Padding)
	}
	if len(open.Children) != 1 {
		t.Fatalf("compact@32 children = %d, want the icon button only", len(open.Children))
	}
	// At 64px a single digit fits alongside the icon.
	one := barStates()["attention"]
	open = BarTreeAtWidth(one, DefaultWidgetSettings(), 32, 64).Children[0]
	if len(open.Children) != 2 {
		t.Fatalf("compact@64 children = %d, want icon + count", len(open.Children))
	}
}

func TestTooltipLines(t *testing.T) {
	t.Parallel()
	tree := TooltipTree(barStates()["attention"], DefaultWidgetSettings())
	got := []string{}
	for _, c := range tree.Children {
		got = append(got, c.Text)
	}
	want := []string{"1 sessions · 3 agents", "1 blocked", "1 done", "1 working"}
	if len(got) != len(want) {
		t.Fatalf("tooltip = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tooltip[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if miss := TooltipTree(barStates()["missing"], DefaultWidgetSettings()); len(miss.Children) != 1 ||
		miss.Children[0].Text != "herdr not found" {
		t.Fatalf("missing tooltip = %+v", miss)
	}
}

func TestWorstToneLadder(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]v1.Tone{
		"missing":       v1.ToneError,
		"empty":         v1.ToneSubtle,
		"idle":          v1.ToneSubtle,
		"attention":     v1.ToneError,
		"working":       v1.ToneNormal,
		"stopped":       v1.ToneSubtle,
		"manyattention": v1.ToneError,
	} {
		if got := worstTone(barStates()[name]); got != want {
			t.Errorf("worstTone(%s) = %q, want %q", name, got, want)
		}
	}
	if got := worstTone(nil); got != v1.ToneSubtle {
		t.Errorf("worstTone(nil) = %q", got)
	}
}
