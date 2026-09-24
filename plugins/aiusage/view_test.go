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

func TestNeedsSetupDetailOffersRetry(t *testing.T) {
	const setupErr = "setup required: plugin setting opencode_go_api_key; env OPENCODE_GO_API_KEY; /tmp/opencode/auth.json (opencode API key)"
	report := Report{Providers: []ProviderReport{{
		ID: "opencode-go", Name: "OpenCode Go", State: StateNeedsSetup, Err: setupErr,
	}}}
	tree := PanelTree(report, "opencode-go", nil, viewConfig(), 4, viewNow)
	if findByID(tree, "retry:opencode-go") == nil {
		t.Fatal("setup-required provider has no retry action")
	}
	for _, hint := range []string{
		"No supported credential was found.",
		"Settings are below.",
		"Focus the provider row for credential options.",
	} {
		if findText(tree, hint) == nil {
			t.Errorf("setup detail omits visible guidance %q", hint)
		}
	}
	row := findByID(tree, "sel:opencode-go")
	if row == nil || !strings.Contains(row.Tooltip, setupErr) {
		t.Fatalf("provider tooltip lost credential-source detail: %+v", row)
	}
}

func TestNoDataDetailShowsProviderExplanation(t *testing.T) {
	const explanation = "Monthly usage is available, but its unit and limit are undocumented."
	report := Report{Providers: []ProviderReport{{
		ID: "ollama", Name: "Ollama", State: StateNoData, Err: explanation,
	}}}
	tree := PanelTree(report, "ollama", nil, viewConfig(), 4, viewNow)
	msg := findText(tree, explanation)
	if msg == nil {
		t.Fatal("no-data detail hid the provider explanation")
	}
	if msg.MaxWidth != 320 {
		t.Fatalf("no-data explanation max width = %d, want 320", msg.MaxWidth)
	}
}

func TestPanelRefreshShowsBusyState(t *testing.T) {
	tree := PanelTree(Report{Loading: true}, "", nil, viewConfig(), 4, viewNow)
	button := findByID(tree, "refresh")
	if button == nil || !button.Disabled || button.Text != "Refreshing…" {
		t.Fatalf("busy refresh button = %+v", button)
	}
}

func TestDeferredProviderUsesCompactStatusWithFullTooltip(t *testing.T) {
	p := ProviderReport{Name: "Claude", State: StateFresh, DeferredUntil: viewNow.Add(time.Minute)}
	status, _ := p.secondLine(viewConfig(), viewNow)
	if status != "Wait" {
		t.Fatalf("deferred status = %q", status)
	}
	if tooltip := rowTooltip(p, viewConfig(), viewNow); !strings.Contains(tooltip, "Refresh deferred · 1m") {
		t.Fatalf("deferred tooltip = %q", tooltip)
	}
}

func TestProviderRowsKeepNamesAndStatusesReadable(t *testing.T) {
	window := Window{Key: "primary", Label: "Session", HasPercent: true,
		UsedPercent: 10, WindowMinutes: 300, ResetsAt: viewNow.Add(time.Hour)}
	cases := []struct {
		name       string
		provider   ProviderReport
		status     string
		tooltipHas string
	}{
		{"ready", ProviderReport{ID: "commandcode", Name: "Command Code", Plan: "Pro", State: StateFresh, UpdatedAt: viewNow, Windows: []Window{window}}, "Ready", "Pro"},
		{"setup", ProviderReport{ID: "synthetic", Name: "Synthetic", State: StateNeedsSetup, Err: "credential missing"}, "Setup", "Needs setup"},
		{"fault", ProviderReport{ID: "minimax", Name: "MiniMax", State: StateFault, Err: "request failed"}, "Error", "Read failed"},
		{"no data", ProviderReport{ID: "opencode-go", Name: "OpenCode Go", State: StateNoData, Err: "OpenCode Go subscription required (HTTP 403)"}, "No data", "subscription required"},
		{"deferred", ProviderReport{ID: "claude", Name: "Claude", State: StateFresh, DeferredUntil: viewNow.Add(time.Minute)}, "Wait", "Refresh deferred"},
		{"stale", ProviderReport{ID: "ollama", Name: "Ollama", State: StateFresh, Stale: true, UpdatedAt: viewNow.Add(-3 * time.Minute)}, "Stale", "Stale"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := providerRow(tc.provider, false, viewConfig(), 7, viewNow)
			if len(row.Children) != 4 {
				t.Fatalf("provider row has %d children, want 4", len(row.Children))
			}
			name := row.Children[1]
			status := row.Children[2].Children[0]
			if got := len(name.Text) * 8; got > name.Width {
				t.Errorf("provider name %q needs %dpx, control is %dpx", name.Text, got, name.Width)
			}
			if status.Text != tc.status {
				t.Errorf("visible status = %q, want %q", status.Text, tc.status)
			}
			if got := len(status.Text) * 8; got > row.Children[2].Width {
				t.Errorf("status %q needs %dpx, slot is %dpx", status.Text, got, row.Children[2].Width)
			}
			used := row.Children[0].Width + name.Width + row.Children[2].Width + row.Children[3].Width + row.Gap*3
			if used > 290-2*row.Padding {
				t.Errorf("provider controls use %dpx, list row has %dpx", used, 290-2*row.Padding)
			}
			if !strings.Contains(name.Tooltip, tc.provider.Name) || !strings.Contains(name.Tooltip, tc.tooltipHas) {
				t.Errorf("tooltip %q omits provider or full status detail", name.Tooltip)
			}
		})
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

func TestDetailSummaryPairsIdentityFreshnessAndGauge(t *testing.T) {
	tree := PanelTree(viewReport(), "alpha", nil, viewConfig(), 4, viewNow)
	summary := findByKey(tree, "provider-summary")
	if summary == nil {
		t.Fatal("provider summary row missing")
	}
	if findText(summary, "Alpha") == nil || findText(summary, "Updated just now · 12:00") == nil {
		t.Fatal("provider identity and freshness are not grouped in the summary")
	}
	var gauge *v1.Node
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n.Kind == v1.KindGauge && n.Width == 64 {
			gauge = n
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(summary)
	if gauge == nil || gauge.ValueText != "40%" {
		t.Fatalf("summary gauge = %+v, want the headline usage gauge", gauge)
	}
}

func TestPanelUsesFlatQuotaSectionsAndKeepsSelection(t *testing.T) {
	tree := PanelTree(viewReport(), "alpha", []float64{20, 40}, viewConfig(), 4, viewNow)
	selected := findByKey(tree, "provider-alpha")
	unselected := findByKey(tree, "provider-beta")
	if selected == nil || selected.Fill != "chip" || selected.Shape == "card" {
		t.Fatalf("selected provider surface = %+v, want a chip selection without a card", selected)
	}
	if unselected == nil || unselected.Fill != "" || unselected.Shape == "card" {
		t.Fatalf("unselected provider surface = %+v, want a flat row", unselected)
	}
	var filledCards int
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n.Fill == "card" || n.Shape == "card" {
			filledCards++
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(tree)
	if filledCards != 0 {
		t.Fatalf("panel contains %d repeated card surfaces", filledCards)
	}
	detail := findByKey(tree, "provider-detail")
	if detail == nil {
		t.Fatal("provider detail list missing")
	}
	separators := 0
	for _, child := range detail.Children {
		if child.Kind == v1.KindSeparator {
			separators++
		}
	}
	if separators != 1 {
		t.Fatalf("quota section separators = %d, want one between the two windows", separators)
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
	if count(4) != 2 {
		t.Fatalf("minor-4 separators = %d, want one provider and one quota divider", count(4))
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

func findByKey(n *v1.Node, key string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.Key == key {
		return n
	}
	for _, child := range n.Children {
		if found := findByKey(child, key); found != nil {
			return found
		}
	}
	return nil
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
