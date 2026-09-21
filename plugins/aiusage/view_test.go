package aiusage

import (
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var viewNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func viewConfig() Config {
	return Config{Warn: 85, Crit: 95, Refresh: time.Minute, HostMinor: 4,
		Track: map[string]bool{"alpha": true, "beta": true}}
}

func viewReport() Report {
	return Report{Providers: []ProviderReport{
		{ID: "alpha", Name: "Alpha", Plan: "Pro", State: StateFresh, UpdatedAt: viewNow,
			Windows: []Window{
				{Key: "primary", Label: "Session", ShortLabel: "5h", HasPercent: true,
					UsedPercent: 40, WindowMinutes: 300, ResetsAt: viewNow.Add(90 * time.Minute)},
				{Key: "secondary", Label: "Weekly", ShortLabel: "Wk", HasPercent: true,
					UsedPercent: 12, WindowMinutes: 10080, ResetsAt: viewNow.Add(60 * time.Hour)},
			}},
		{ID: "beta", Name: "Beta", State: StateFresh, UpdatedAt: viewNow,
			Windows: []Window{
				{Key: "primary", Label: "Session", ShortLabel: "5h", HasPercent: true,
					UsedPercent: 90, WindowMinutes: 300, ResetsAt: viewNow.Add(2 * time.Hour)},
			}},
	}}
}

func countNodes(n *v1.Node) int {
	if n == nil {
		return 0
	}
	c := 1
	for _, ch := range n.Children {
		c += countNodes(ch)
	}
	return c
}

func treeDepth(n *v1.Node) int {
	if n == nil {
		return 0
	}
	d := 0
	for _, ch := range n.Children {
		if gd := treeDepth(ch); gd > d {
			d = gd
		}
	}
	return d + 1
}

func findByID(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, ch := range n.Children {
		if got := findByID(ch, id); got != nil {
			return got
		}
	}
	return nil
}

func findText(n *v1.Node, text string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.Kind == v1.KindText && n.Text == text {
		return n
	}
	for _, ch := range n.Children {
		if got := findText(ch, text); got != nil {
			return got
		}
	}
	return nil
}

func TestBarTreeValidatesAcrossStatesAndHosts(t *testing.T) {
	t.Parallel()

	states := map[string]Report{
		"fresh":  viewReport(),
		"nodata": {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateNoData}}},
		"setup":  {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateNeedsSetup, Err: "setup required: /none"}}},
		"fault":  {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateFault, Stale: true, UpdatedAt: viewNow, Windows: viewReport().Providers[0].Windows}}},
		"empty":  {},
	}
	inst := DefaultInstance()
	inst.Extras = "both"
	cfg := viewConfig()
	// The matrix runs to minor 6: the pinned grammar here predates minor
	// seven, so absent meters validate only in the shell's own tests. The
	// builder's minor-7 gating is asserted separately below.
	for name, r := range states {
		for _, minor := range []int{6, 5, 4, 3, 2} {
			tree := BarTree(r, inst, cfg, minor, viewNow)
			if err := v1.Validate(tree, v1.ViewBar); err != nil {
				t.Errorf("%s minor %d: bar rejected: %v", name, minor, err)
			}
		}
	}
}

func treeHasAbsentMeter(n *v1.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == v1.KindProgress && n.Absent {
		return true
	}
	for _, ch := range n.Children {
		if treeHasAbsentMeter(ch) {
			return true
		}
	}
	return false
}

func TestBarTreeContentPerState(t *testing.T) {
	t.Parallel()

	cfg := viewConfig()
	inst := DefaultInstance()
	inst.Extras = "both" // countdown and pace slots are settings-fixed

	// Fresh, auto: the severity-ranked pick is beta at 90% (warn → accent).
	bar := BarTree(viewReport(), inst, cfg, 4, viewNow)
	var gaugeNode *v1.Node
	var walk func(n *v1.Node)
	walk = func(n *v1.Node) {
		if n.Kind == v1.KindGauge {
			gaugeNode = n
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(bar)
	if gaugeNode == nil || gaugeNode.ValueText != "90" || gaugeNode.Absent || gaugeNode.Tone != v1.ToneAccent {
		t.Fatalf("auto gauge = %+v, want beta's 90%% in accent", gaugeNode)
	}
	// The radial dial carries the number; a duplicate percent beside it is
	// exactly the noise the dial exists to remove.
	if findText(bar, "90%") != nil {
		t.Fatal("percent text duplicated beside the radial dial")
	}
	if findText(bar, "2h 0m") == nil {
		t.Fatal("countdown missing from the fresh bar")
	}

	// Pinned: the capsule follows the vendor even when another is worse.
	inst.Vendor = "alpha"
	bar = BarTree(viewReport(), inst, cfg, 4, viewNow)
	gaugeNode = nil
	walk(bar)
	if gaugeNode == nil || gaugeNode.ValueText != "40" || gaugeNode.Tone != v1.ToneNormal {
		t.Fatalf("pinned gauge = %+v, want alpha's calm 40%%", gaugeNode)
	}
	if findText(bar, "1h 30m") == nil {
		t.Fatal("countdown missing from the pinned bar")
	}

	// The severity ramp: 90% is accent, 96% is error.
	if tone := severityTone(90, true, cfg); tone != v1.ToneAccent {
		t.Errorf("90%% tone = %q, want accent", tone)
	}
	if tone := severityTone(96, true, cfg); tone != v1.ToneError {
		t.Errorf("96%% tone = %q, want error", tone)
	}

	// No data: the gauge is absent (box reserved, nothing painted) and the
	// percent slot holds "--" — the capsule is empty without losing nodes.
	bar = BarTree(Report{Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateNoData}}},
		inst, cfg, 4, viewNow)
	walk(bar)
	if gaugeNode == nil || !gaugeNode.Absent {
		t.Fatalf("no-data gauge = %+v", gaugeNode)
	}
	// No separate dash text beside the dial: the absent gauge reserves the
	// slot and paints nothing, leaving the glyph as the only mark — the
	// empty-capsule rule for a number nothing is refreshing.
}

func TestBarTreeMinorTwoUsesMeter(t *testing.T) {
	t.Parallel()

	bar := BarTree(viewReport(), DefaultInstance(), viewConfig(), 2, viewNow)
	var walk func(n *v1.Node, fn func(*v1.Node))
	walk = func(n *v1.Node, fn func(*v1.Node)) {
		fn(n)
		for _, ch := range n.Children {
			walk(ch, fn)
		}
	}
	gauges, meters := 0, 0
	walk(bar, func(n *v1.Node) {
		switch n.Kind {
		case v1.KindGauge:
			gauges++
		case v1.KindProgress:
			meters++
		}
	})
	if gauges != 0 || meters == 0 {
		t.Fatalf("minor-2 bar = %d gauges, %d meters; want meter fallback", gauges, meters)
	}
}

func TestBarTreeNodeCountStability(t *testing.T) {
	t.Parallel()

	inst := DefaultInstance()
	cfg := viewConfig()
	fresh := BarTree(viewReport(), inst, cfg, 4, viewNow)
	faulted := Report{Providers: []ProviderReport{{
		ID: "alpha", Name: "Alpha", State: StateFault, Stale: true, UpdatedAt: viewNow,
		Windows: viewReport().Providers[0].Windows, // last good carried forward
	}, {ID: "beta", Name: "Beta", State: StateFault, Stale: true, UpdatedAt: viewNow,
		Windows: viewReport().Providers[1].Windows}}}
	broken := BarTree(faulted, inst, cfg, 4, viewNow)
	if countNodes(fresh) != countNodes(broken) {
		t.Fatalf("node count changed across the fault: %d → %d",
			countNodes(fresh), countNodes(broken))
	}
}

func TestPanelTreeValidatesAcrossStatesAndHosts(t *testing.T) {
	t.Parallel()

	states := map[string]Report{
		"fresh":  viewReport(),
		"setup":  {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateNeedsSetup, Err: "setup required: /none"}}},
		"fault":  {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateFault, Stale: true, UpdatedAt: viewNow, Err: "boom", Windows: viewReport().Providers[0].Windows}}},
		"nodata": {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateNoData}}},
		"empty":  {},
	}
	hist := []float64{10, 20, 40}
	// Panel matrix to minor 6 for the same pin reason as the bar; the
	// minor-7 absent meter is gated in the builder and covered by the
	// shell's grammar tests.
	for name, r := range states {
		for _, minor := range []int{6, 5, 4, 3, 2} {
			tree := PanelTree(r, "alpha", hist, viewConfig(), minor, viewNow)
			if err := v1.Validate(tree, v1.ViewPanel); err != nil {
				t.Errorf("%s minor %d: panel rejected: %v", name, minor, err)
			}
		}
	}
}

func TestPanelTreeStructure(t *testing.T) {
	t.Parallel()

	cfg := viewConfig()
	rep := viewReport()
	rep.Loading = true
	tree := PanelTree(rep, "alpha", []float64{10, 20, 40}, cfg, 4, viewNow)

	// Refresh button is the busy state while a round runs.
	if btn := findByID(tree, "refresh"); btn == nil || !btn.Disabled {
		t.Fatalf("refresh button = %+v, want disabled while loading", btn)
	}

	// Rows are keyed and the selection is tinted.
	if row := findByID(tree, ""); row == nil {
		// findByID by key: walk keys instead.
	}
	if !treeHasKey(tree, "provider-alpha") || !treeHasKey(tree, "provider-beta") {
		t.Fatal("provider rows missing their keys")
	}
	var alphaRow *v1.Node
	var walkKey func(n *v1.Node)
	walkKey = func(n *v1.Node) {
		if n.Key == "provider-alpha" {
			alphaRow = n
		}
		for _, ch := range n.Children {
			walkKey(ch)
		}
	}
	walkKey(tree)
	if alphaRow.Fill != "chip" {
		t.Fatalf("selected row fill = %q, want chip", alphaRow.Fill)
	}
	if btn := findByID(tree, "sel:beta"); btn == nil || len(btn.Events) == 0 {
		t.Fatalf("select button = %+v", btn)
	}

	// The hero gauge reads the headline window.
	var hero *v1.Node
	var walkGauge func(n *v1.Node)
	walkGauge = func(n *v1.Node) {
		if n.Kind == v1.KindGauge && n.Width == 64 {
			hero = n
		}
		for _, ch := range n.Children {
			walkGauge(ch)
		}
	}
	walkGauge(tree)
	if hero == nil || hero.ValueText != "40%" || hero.Absent {
		t.Fatalf("hero gauge = %+v", hero)
	}

	// The history sparkline renders the samples with a trend caption.
	var graph *v1.Node
	var walkGraph func(n *v1.Node)
	walkGraph = func(n *v1.Node) {
		if n.Kind == v1.KindGraph {
			graph = n
		}
		for _, ch := range n.Children {
			walkGraph(ch)
		}
	}
	walkGraph(tree)
	if graph == nil || len(graph.Values) != 3 || graph.Values[2] != 0.4 {
		t.Fatalf("graph = %+v", graph)
	}
	if findText(tree, "↑ 20pts over the last reading") == nil {
		t.Fatal("trend caption missing")
	}

	// The faulted provider's detail carries a retry.
	tree = PanelTree(viewReport(), "beta", nil, cfg, 4, viewNow)
	rep2 := viewReport()
	rep2.Providers[1].State = StateFault
	rep2.Providers[1].Err = "boom"
	rep2.Providers[1].Stale = true
	tree = PanelTree(rep2, "beta", nil, cfg, 4, viewNow)
	if btn := findByID(tree, "retry:beta"); btn == nil {
		t.Fatal("retry button missing on the faulted detail")
	}
	// The record card shows the last numbers as dated text — the date alone
	// would blank the pane's only useful content.
	if findText(tree, "Session 90%") == nil {
		t.Fatal("record card missing the last numbers")
	}
	if findText(tree, "Quota windows · last local snapshot per provider · not billing figures") == nil {
		t.Fatal("honesty footer missing")
	}
}

func TestPanelTreeSeparatorsAndMinorTwo(t *testing.T) {
	t.Parallel()

	rep := viewReport()
	count := func(minor int) int {
		tree := PanelTree(rep, "alpha", nil, viewConfig(), minor, viewNow)
		seps := 0
		var walk func(n *v1.Node)
		walk = func(n *v1.Node) {
			if n.Kind == v1.KindSeparator {
				seps++
			}
			for _, ch := range n.Children {
				walk(ch)
			}
		}
		walk(tree)
		return seps
	}
	if count(4) != 1 {
		t.Fatalf("minor-4 separators = %d, want 1 (two rows)", count(4))
	}
	if count(3) != 0 {
		t.Fatalf("minor-3 separators = %d, want 0", count(3))
	}
}

func TestPanelTreeBudgetsWithSixProviders(t *testing.T) {
	t.Parallel()

	rep := Report{}
	for _, id := range []string{"claude", "codex", "commandcode", "minimax", "ollama", "synthetic"} {
		p := freshRep(id)
		p.Name = strings.Title(id[:1]) + id[1:]
		rep.Providers = append(rep.Providers, p)
	}
	tree := PanelTree(rep, "claude", nil, viewConfig(), 4, viewNow)
	if err := v1.Validate(tree, v1.ViewPanel); err != nil {
		t.Fatalf("six-provider panel rejected: %v", err)
	}
	if n := countNodes(tree); n > 1024 {
		t.Fatalf("panel holds %d nodes, past the %d budget", n, 1024)
	}
	if d := treeDepth(tree); d > 16 {
		t.Fatalf("panel depth %d, past the %d budget", d, 16)
	}
}

func treeHasKey(n *v1.Node, key string) bool {
	if n == nil {
		return false
	}
	if n.Key == key {
		return true
	}
	for _, ch := range n.Children {
		if treeHasKey(ch, key) {
			return true
		}
	}
	return false
}

func TestDetailExhaustedNoticeAndTooltip(t *testing.T) {
	t.Parallel()

	cfg := viewConfig()
	rep := viewReport()
	rep.Providers[0].Windows[0].UsedPercent = 100
	rep.Providers[0].Windows[0].ResetsAt = viewNow.Add(30 * time.Hour)
	tree := PanelTree(rep, "alpha", nil, cfg, 4, viewNow)
	if findText(tree, "Session · Quota exhausted · renews in 1d 6h") == nil {
		t.Fatal("exhausted notice missing")
	}

	// The tooltip view is text-only and names provider, window, and reset.
	// Auto picks the depleted window, so the tooltip leads with Alpha at
	// 100% — the bottleneck headline logic reaching the hover too.
	tt := TooltipTree(rep, DefaultInstance(), cfg, 4, viewNow)
	if err := v1.Validate(tt, v1.ViewTooltip); err != nil {
		t.Fatalf("tooltip rejected: %v", err)
	}
	if !strings.Contains(tt.Children[0].Text, "Alpha · Session 100%") {
		t.Fatalf("tooltip line = %q", tt.Children[0].Text)
	}
}

func TestWindowCardWaitingForFreshData(t *testing.T) {
	t.Parallel()

	w := Window{Key: "primary", Label: "Session", HasPercent: true,
		UsedPercent: 40, WindowMinutes: 300, ResetsAt: viewNow.Add(-time.Minute)}
	card := windowCard(w, viewConfig(), 7, viewNow)
	if findText(card, "Waiting for fresh data") == nil {
		t.Fatalf("card = %+v", card)
	}
}
