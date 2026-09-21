package aiusage

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// View builders turn a Report snapshot into plugin/v1 view trees. They are
// pure functions: every data-dependent value is an argument, every
// settings-dependent value comes from Instance/Config.
//
// The bar tree is node-count stable across state transitions — a failure
// recolors and refills in place, never adds or removes nodes (the
// ai-usagebar invariant). The panel tree is stateful by nature; its list
// keeps setup and no-data rows so the instruction is never lost.

// Instance is one bar widget instance's settings slice.
type Instance struct {
	Vendor        string // "auto" or a provider id
	Visualization string // "radial" | "meter" | "none"
	GlyphPosition string // "before" | "after"
	Extras        string // "none" | "countdown" | "pace" | "both"
	ShowValue     bool
	ShowGlyph     bool
	ShowName      bool
}

// DefaultInstance is the manifest's instance-setting defaults.
func DefaultInstance() Instance {
	return Instance{
		Vendor: "auto", Visualization: "radial", GlyphPosition: "before",
		Extras: "none", ShowValue: true, ShowGlyph: true,
	}
}

// selectProvider resolves the capsule's provider: a pinned id or the
// severity-ranked auto pick. A pinned vendor with no live read returns nil —
// the capsule goes empty rather than holding a number nothing is refreshing.
func selectProvider(r Report, vendor string, warn, crit int) *ProviderReport {
	for i := range r.Providers {
		if r.Providers[i].ID == vendor {
			return &r.Providers[i]
		}
	}
	if vendor != "" && vendor != "auto" {
		return nil
	}
	type ranked struct {
		idx int
		sev int
		pct float64
		id  string
	}
	var order []ranked
	for i := range r.Providers {
		p := &r.Providers[i]
		h := Headline(p.Windows)
		sev, pct := 0, -1.0
		if h != nil && h.HasPercent {
			sev = Severity(h.UsedPercent, warn, crit)
			pct = h.UsedPercent
		}
		order = append(order, ranked{i, sev, pct, p.ID})
	}
	sort.SliceStable(order, func(a, b int) bool {
		if order[a].sev != order[b].sev {
			return order[a].sev > order[b].sev
		}
		if order[a].pct != order[b].pct {
			return order[a].pct > order[b].pct
		}
		return order[a].id < order[b].id
	})
	if len(order) == 0 {
		return nil
	}
	return &r.Providers[order[0].idx]
}

// severityTone maps the alert ladder onto the wire's tone vocabulary:
// calm text, warm accent at warn, error at critical and depleted.
func severityTone(pct float64, hasPercent bool, cfg Config) v1.Tone {
	if !hasPercent {
		return v1.ToneSubtle
	}
	switch Severity(pct, cfg.Warn, cfg.Crit) {
	case 3, 2:
		return v1.ToneError
	case 1:
		return v1.ToneAccent
	default:
		return v1.ToneNormal
	}
}

// BarTree builds the bar pill. Fixed slots keep the node count identical
// across states; missing data renders as "--", a dimmed gauge, and muted
// tones instead of vanishing nodes.
func BarTree(r Report, inst Instance, cfg Config, hostMinor int, now time.Time) *v1.Node {
	p := selectProvider(r, inst.Vendor, cfg.Warn, cfg.Crit)

	tone := v1.ToneSubtle
	pctText := "--"
	value := 0.0
	absent := true
	countdown, pace := "", ""
	name := ""
	tooltip := "no readings yet"

	if p != nil {
		name = p.Name
		if h := Headline(p.Windows); h != nil {
			if h.HasPercent {
				pctText = fmt.Sprintf("%.0f%%", h.UsedPercent)
				value = h.UsedPercent / 100
				absent = false
				tone = severityTone(h.UsedPercent, true, cfg)
			}
			countdown = FormatCountdown(h.ResetsAt, now)
			if pacePts, ok := Pace(*h, now); ok {
				if pacePts >= 0 {
					pace = fmt.Sprintf("↑%d", pacePts)
				} else {
					pace = fmt.Sprintf("↓%d", -pacePts)
				}
			}
			tooltip = fmt.Sprintf("%s · %s %v%%", p.Name, h.Label, h.UsedPercent)
			if cd := FormatCountdown(h.ResetsAt, now); cd != "" {
				tooltip += " · resets in " + cd
			}
		} else if p.Err != "" {
			tooltip = p.Err
		}
	}

	row := &v1.Node{Kind: v1.KindRow, Gap: 6}
	// The activate button is what opens the panel — the host owns no pill
	// click, so the pill carries its own control.
	button := &v1.Node{
		Kind: v1.KindButton, ID: "open", Icon: "ai-usage",
		Name: "Open AI usage", Role: "button", Tooltip: tooltip,
		Events: []v1.EventKind{v1.EventActivate},
	}
	if inst.ShowGlyph && inst.GlyphPosition == "after" {
		row.Children = append(row.Children, gaugeOrMeter(inst, value, absent, pctText, tone, cfg, hostMinor)...)
		row.Children = append(row.Children, button)
	} else {
		row.Children = append(row.Children, button)
		row.Children = append(row.Children, gaugeOrMeter(inst, value, absent, pctText, tone, cfg, hostMinor)...)
	}
	if inst.ShowName {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: name, Tone: tone})
	}
	if inst.ShowValue {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: pctText, Tabular: true, Tone: tone})
	}
	if inst.Extras == "countdown" || inst.Extras == "both" {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: countdown, Tabular: true, Tone: v1.ToneSubtle})
	}
	if inst.Extras == "pace" || inst.Extras == "both" {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: pace, Tone: v1.ToneSubtle})
	}

	// The quota-over-elapsed pairing: a thin strip beneath the pill showing
	// how far through the headline window the clock is. Zero when unknown —
	// a fixed slot, so the node count never changes.
	col := &v1.Node{Kind: v1.KindColumn, Gap: 2}
	col.Children = append(col.Children, row)
	if inst.Visualization != "none" {
		elapsed := 0.0
		if p != nil {
			if h := Headline(p.Windows); h != nil {
				if e, ok := ElapsedPercent(*h, now); ok {
					elapsed = e / 100
				}
			}
		}
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindProgress, Value: elapsed, Height: 3, MaxWidth: 120})
	}
	return col
}

// gaugeOrMeter is the visualization slot: a radial gauge on minor-3-plus
// hosts (absent reserves the box with no reading), a meter below that, or
// nothing when the visualization is "none".
func gaugeOrMeter(inst Instance, value float64, absent bool, pctText string, tone v1.Tone, cfg Config, hostMinor int) []*v1.Node {
	if inst.Visualization == "none" {
		return nil
	}
	if inst.Visualization == "radial" && hostMinor >= 3 {
		g := &v1.Node{
			Kind: v1.KindGauge, Width: 22, Height: 22,
			Value: value, ValueText: strings.TrimSuffix(pctText, "%"),
			Absent: absent, Tone: tone,
		}
		if absent {
			g.Value = 0
		}
		return []*v1.Node{g}
	}
	// Meter fallback: the percent text carries "--" when there is no reading.
	pct := value
	if absent {
		pct = 0
	}
	return []*v1.Node{{Kind: v1.KindProgress, Value: pct, Height: 5, MaxWidth: 60, Tone: tone}}
}

// monogram is the provider identity disc: a circle with the id's first
// letter. Catalogue glyphs can replace it per provider later.
func monogram(id string) *v1.Node {
	letter := "?"
	if id != "" {
		letter = strings.ToUpper(id[:1])
	}
	return &v1.Node{
		Kind: v1.KindColumn, Width: 26, Height: 26,
		Shape: "circle", Fill: "accent",
		Name: "provider " + id, Role: "img",
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: letter, CenterX: true, Height: 26},
		},
	}
}

// fleetRollup summarizes the tracked fleet the way AIOC's header does:
// average load across timed quota windows (balance and informational
// placeholders are excluded so they cannot dilute the average), the peak
// provider as a jump link, the at-risk count, and the next reset anywhere.
func fleetRollup(r Report, cfg Config, now time.Time) *v1.Node {
	var total float64
	timed := 0
	atRisk := 0
	var peak *ProviderReport
	peakPct := -1.0
	var nextReset time.Time
	for i := range r.Providers {
		p := &r.Providers[i]
		if p.State != StateFresh || p.Stale {
			continue
		}
		h := Headline(p.Windows)
		if h == nil || !h.HasPercent || h.WindowMinutes <= 0 {
			continue
		}
		total += h.UsedPercent
		timed++
		if h.UsedPercent >= float64(cfg.Warn) {
			atRisk++
		}
		if peak == nil || h.UsedPercent > peakPct {
			peak, peakPct = p, h.UsedPercent
		}
		if !h.ResetsAt.IsZero() && (nextReset.IsZero() || h.ResetsAt.Before(nextReset)) {
			nextReset = h.ResetsAt
		}
	}
	if timed == 0 {
		return nil
	}
	row := &v1.Node{Kind: v1.KindRow, Gap: 8, Fill: "card", Shape: "card", Padding: 8, Key: "fleet-rollup"}
	row.Children = append(row.Children,
		&v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("Avg %v%%", math.Round(total/float64(timed))), Bold: true})
	if peak != nil {
		row.Children = append(row.Children, &v1.Node{
			Kind: v1.KindButton, ID: "peak:" + peak.ID,
			Text: fmt.Sprintf("Peak %s %v%%", peak.Name, peakPct),
			Name: "Jump to " + peak.Name + ", the most loaded provider", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}})
	}
	row.Children = append(row.Children,
		&v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d at risk", atRisk), Tone: v1.ToneSubtle, Size: "caption"})
	if !nextReset.IsZero() {
		if cd := FormatCountdown(nextReset, now); cd != "" {
			row.Children = append(row.Children,
				&v1.Node{Kind: v1.KindText, Text: "next reset " + cd, Tone: v1.ToneSubtle, Size: "caption", Tabular: true})
		}
	}
	return row
}

// PanelTree builds the master/detail panel: provider rows on the left,
// the selected provider's detail on the right.
func PanelTree(r Report, selected string, hist []float64, cfg Config, hostMinor int, now time.Time) *v1.Node {
	list := &v1.Node{Kind: v1.KindColumn, Width: 290, Gap: 2}
	list.Children = append(list.Children,
		&v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "ai-usage"},
			{Kind: v1.KindText, Text: "AI Usage", Bold: true, Size: "title"},
			&v1.Node{Kind: v1.KindButton, ID: "refresh", Text: "Refresh",
				Name: "Refresh all providers", Role: "button",
				Disabled: r.Loading, Tooltip: "Fetch every tracked provider now",
				Events: []v1.EventKind{v1.EventActivate}},
		}})
	// The fleet rollup: average load across timed quota windows, the peak
	// provider as a jump link, at-risk count, and the next reset anywhere.
	if rollup := fleetRollup(r, cfg, now); rollup != nil {
		list.Children = append(list.Children, rollup)
	}

	providers := make([]ProviderReport, len(r.Providers))
	copy(providers, r.Providers)
	sortProviderRows(providers, cfg)

	for i, p := range providers {
		if i > 0 && hostMinor >= 4 {
			list.Children = append(list.Children, &v1.Node{Kind: v1.KindSeparator})
		}
		list.Children = append(list.Children, providerRow(p, selected == p.ID, cfg, hostMinor, now))
	}

	detail := &v1.Node{Kind: v1.KindColumn, Gap: 10}
	detail.Children = append(detail.Children, detailPane(r, providers, selected, hist, cfg, hostMinor, now))

	return &v1.Node{Kind: v1.KindRow, Gap: 12, Padding: 12, Children: []*v1.Node{list, detail}}
}

// sortProviderRows orders the list severity-first, percent-second; setup and
// no-data rows stay (their instruction is the point).
func sortProviderRows(providers []ProviderReport, cfg Config) {
	sort.SliceStable(providers, func(a, b int) bool {
		pa, pb := &providers[a], &providers[b]
		ha, hb := Headline(pa.Windows), Headline(pb.Windows)
		sa, sb := 0, 0
		var pca, pcb float64
		if ha != nil && ha.HasPercent {
			sa = Severity(ha.UsedPercent, cfg.Warn, cfg.Crit)
			pca = ha.UsedPercent
		}
		if hb != nil && hb.HasPercent {
			sb = Severity(hb.UsedPercent, cfg.Warn, cfg.Crit)
			pcb = hb.UsedPercent
		}
		if sa != sb {
			return sa > sb
		}
		if pca != pcb {
			return pca > pcb
		}
		return pa.ID < pb.ID
	})
}

func providerRow(p ProviderReport, selected bool, cfg Config, hostMinor int, now time.Time) *v1.Node {
	fill := "card"
	if selected {
		fill = "chip" // the selection tint
	}
	secondLine, tone := p.secondLine(cfg, now)
	h := Headline(p.Windows)

	pctText, pctTone := "--", v1.ToneSubtle
	meterH, meterV := 5, 0.0
	size := "caption"
	if h != nil && h.HasPercent {
		pctText = fmt.Sprintf("%.0f%%", h.UsedPercent)
		pctTone = severityTone(h.UsedPercent, true, cfg)
		meterV = h.UsedPercent / 100
		switch Severity(h.UsedPercent, cfg.Warn, cfg.Crit) {
		case 3, 2:
			size, meterH = "title", 7
		case 1:
			size = "body"
		}
	}

	row := &v1.Node{
		Kind: v1.KindRow, Key: "provider-" + p.ID,
		Fill: fill, Shape: "card", Padding: 8, Gap: 8,
	}
	row.Children = append(row.Children, monogram(p.ID))
	row.Children = append(row.Children, &v1.Node{
		Kind: v1.KindButton, ID: "sel:" + p.ID, Text: p.Name,
		Name: "Show " + p.Name, Role: "button",
		Tooltip: rowTooltip(p, now),
		Events:  []v1.EventKind{v1.EventActivate},
	})
	row.Children = append(row.Children, &v1.Node{
		Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
			{Kind: v1.KindText, Text: secondLine, Tone: tone, Size: "caption"},
		},
	})
	pin := &v1.Node{
		Kind: v1.KindColumn, Gap: 2, PinEnd: true,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: pctText, Tabular: true, Tone: pctTone, Bold: size == "title", Size: size, Width: 48},
			{Kind: v1.KindProgress, Value: meterV, Height: meterH, Width: 60},
		},
	}
	row.Children = append(row.Children, pin)
	return row
}

// staleFor flags a report the loop marked, or one simply too old: past
// twice the refresh interval the numbers are stale however fresh the state
// claims (the AIOC 2× rule).
func (p ProviderReport) staleFor(cfg Config, now time.Time) bool {
	if p.Stale {
		return true
	}
	if p.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(p.UpdatedAt) > 2*cfg.Refresh
}

// secondLine is the row's status line: plan, or why there is no number.
func (p ProviderReport) secondLine(cfg Config, now time.Time) (string, v1.Tone) {
	switch p.State {
	case StateNeedsSetup:
		return "Needs setup", v1.ToneAccent
	case StateFault:
		return "Read failed", v1.ToneError
	case StateNoData:
		return "No data yet", v1.ToneSubtle
	default:
		if p.staleFor(cfg, now) {
			age := "just now"
			if !p.UpdatedAt.IsZero() {
				age = "Stale · " + humanizeAge(now.Sub(p.UpdatedAt)) + " ago"
			}
			return age, v1.ToneError
		}
		if p.Plan != "" {
			return p.Plan, v1.ToneSubtle
		}
		return "", v1.ToneSubtle
	}
}

func rowTooltip(p ProviderReport, now time.Time) string {
	h := Headline(p.Windows)
	if h == nil {
		if p.Err != "" {
			return p.Err
		}
		return "no readings yet"
	}
	t := fmt.Sprintf("%s %v%%", h.Label, h.UsedPercent)
	if cd := FormatCountdown(h.ResetsAt, now); cd != "" {
		t += " · resets in " + cd
	}
	return t
}

// detailPane renders the selected provider. An unknown or absent selection
// falls back to the first row so the pane is never empty while rows exist.
func detailPane(r Report, providers []ProviderReport, selected string, hist []float64, cfg Config, hostMinor int, now time.Time) *v1.Node {
	var p *ProviderReport
	for i := range providers {
		if providers[i].ID == selected {
			p = &providers[i]
			break
		}
	}
	if p == nil && len(providers) > 0 {
		p = &providers[0]
	}
	pane := &v1.Node{Kind: v1.KindColumn, Gap: 8}
	if p == nil {
		pane.Children = append(pane.Children,
			&v1.Node{Kind: v1.KindText, Text: "No providers tracked — enable one in settings.", Tone: v1.ToneSubtle})
		return pane
	}

	// Header: identity and plan.
	header := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		monogram(p.ID),
		{Kind: v1.KindText, Text: p.Name, Bold: true, Size: "headline"},
	}}
	if p.Plan != "" {
		header.Children = append(header.Children,
			&v1.Node{Kind: v1.KindText, Text: p.Plan, Tone: v1.ToneSubtle, Size: "caption"})
	}
	pane.Children = append(pane.Children, header)

	// Freshness line: age against the capture, stale in error tone.
	freshness := "no readings yet"
	tone := v1.ToneSubtle
	if !p.UpdatedAt.IsZero() {
		freshness = fmt.Sprintf("Updated %s · %s", humanizeAge(now.Sub(p.UpdatedAt)), p.UpdatedAt.Format("15:04"))
		if p.staleFor(cfg, now) {
			freshness = fmt.Sprintf("Stale (%s) — is the tool signed in?", humanizeAge(now.Sub(p.UpdatedAt)))
			tone = v1.ToneError
		}
	}
	pane.Children = append(pane.Children,
		&v1.Node{Kind: v1.KindText, Text: freshness, Tone: tone, Size: "caption"})

	switch p.State {
	case StateNeedsSetup:
		msg := p.Err
		if msg == "" {
			msg = "no credential found"
		}
		pane.Children = append(pane.Children, &v1.Node{
			Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: 10, Gap: 4,
			Children: []*v1.Node{
				{Kind: v1.KindText, Text: "Needs setup", Bold: true},
				{Kind: v1.KindText, Text: msg, Tone: v1.ToneSubtle, Size: "caption"},
			},
		})
		return pane
	case StateFault:
		row := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: p.Err, Tone: v1.ToneError, Size: "caption", MaxWidth: 320},
		}}
		if p.Stale && len(p.Windows) > 0 {
			// A record card shows the last numbers as dated text: a bar
			// reads as a live reading, and nothing is reading.
			row.Children = append(row.Children, &v1.Node{Kind: v1.KindText,
				Text: "Last reading, " + humanizeAge(now.Sub(p.UpdatedAt)) + " ago",
				Tone: v1.ToneSubtle, Size: "caption"})
		}
		row.Children = append(row.Children, &v1.Node{
			Kind: v1.KindButton, ID: "retry:" + p.ID, Text: "Retry",
			Name: "Retry " + p.Name, Role: "button",
			Events: []v1.EventKind{v1.EventActivate}})
		pane.Children = append(pane.Children, row)
	case StateNoData:
		pane.Children = append(pane.Children,
			&v1.Node{Kind: v1.KindText, Text: "No data yet — use the tool to record a snapshot.", Tone: v1.ToneSubtle, Size: "caption"})
	}

	h := Headline(p.Windows)
	if p.State == StateFresh {
		// Hero dial for the headline window. A stale fault gets a record
		// card instead: dated text, not gauges — nothing is reading.
		gauge := &v1.Node{
			Kind: v1.KindGauge, Width: 64, Height: 64,
			Value: 0, ValueText: "--", Absent: h == nil || !h.HasPercent,
			Name: "headline usage", Role: "img",
		}
		if h != nil && h.HasPercent {
			gauge.Value = h.UsedPercent / 100
			gauge.ValueText = fmt.Sprintf("%.0f%%", h.UsedPercent)
			gauge.Absent = false
			gauge.Tone = severityTone(h.UsedPercent, true, cfg)
		}
		pane.Children = append(pane.Children, gauge)

		// Exhausted-quota notice: a window at or past one hundred blocks
		// use, and the panel says so with the renew instant.
		for _, w := range p.Windows {
			if !w.HasPercent || w.UsedPercent < 100 {
				continue
			}
			line := fmt.Sprintf("%s · Quota exhausted", w.Label)
			if cd := FormatCountdown(w.ResetsAt, now); cd != "" {
				line += " · renews in " + cd
			} else if !w.ResetsAt.IsZero() {
				line += " · renews " + w.ResetsAt.Format("Mon 15:04")
			}
			pane.Children = append(pane.Children, &v1.Node{
				Kind: v1.KindColumn, Fill: "error-container", Shape: "card", Padding: 10,
				Children: []*v1.Node{
					{Kind: v1.KindText, Text: line, Tone: v1.ToneError, Bold: true},
				},
			})
			break // the blocking window is the story; one notice covers it
		}
	}

	// History card: the sparkline over recorded percents, with the trend.
	if hostMinor >= 4 && p.State == StateFresh && len(hist) >= 2 {
		card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: 10, Gap: 4}
		values := make([]float64, 0, len(hist))
		for _, pct := range hist {
			values = append(values, pct/100)
		}
		card.Children = append(card.Children, &v1.Node{Kind: v1.KindGraph, Values: values, Height: 40})
		card.Children = append(card.Children,
			&v1.Node{Kind: v1.KindText, Text: trendCaption(hist), Tone: v1.ToneSubtle, Size: "caption"})
		pane.Children = append(pane.Children, card)
	}

	// One card per window — fresh reads only. A stale fault rendered its
	// numbers as the record card above; drawing meters over them would read
	// as a live reading, and nothing is reading.
	if p.State == StateFresh {
		for _, w := range p.Windows {
			pane.Children = append(pane.Children, windowCard(w, cfg, now))
		}
	}

	// Credits row when the provider reports a balance worth showing.
	if p.Credits != nil && *p.Credits > 0 {
		pane.Children = append(pane.Children,
			&v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("Credits: $%s", formatAmount(*p.Credits)),
				Tone: v1.ToneSubtle, Size: "caption"})
	}

	// The honesty footer, with the history export beside it.
	footer := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Tone: v1.ToneSubtle, Size: "caption",
			Text: "Quota windows · last local snapshot per provider · not billing figures"},
		{Kind: v1.KindButton, ID: "export", Text: "Export CSV",
			Name: "Export usage history as CSV", Role: "button",
			Tooltip: "Write the recorded percents to a CSV file in your downloads folder",
			Events:  []v1.EventKind{v1.EventActivate}},
	}}
	pane.Children = append(pane.Children, footer)
	return pane
}

func windowCard(w Window, cfg Config, now time.Time) *v1.Node {
	tone := severityTone(w.UsedPercent, w.HasPercent, cfg)
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: 10, Gap: 4}
	pct := "--"
	if w.HasPercent {
		pct = fmt.Sprintf("%.0f%%", w.UsedPercent)
	}
	card.Children = append(card.Children, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: w.Label, Bold: true},
		{Kind: v1.KindColumn, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: pct, Tabular: true, Tone: tone, Bold: true},
		}},
	}})
	if w.HasPercent {
		card.Children = append(card.Children, &v1.Node{Kind: v1.KindProgress, Value: w.UsedPercent / 100, Height: 5})
	}
	if e, ok := ElapsedPercent(w, now); ok {
		card.Children = append(card.Children, &v1.Node{Kind: v1.KindProgress, Value: e / 100, Height: 3})
	}
	clockRow := &v1.Node{Kind: v1.KindRow, Gap: 8}
	cd := FormatCountdown(w.ResetsAt, now)
	switch {
	case w.HasPercent && !w.ResetsAt.IsZero() && w.ResetsAt.Before(now):
		// The window rolled over but no fresh snapshot has landed: keep the
		// last percents — never show 0% on a rolled-over window.
		clockRow.Children = append(clockRow.Children,
			&v1.Node{Kind: v1.KindText, Text: "Waiting for fresh data", Tone: v1.ToneSubtle, Size: "caption"})
	case cd != "":
		clockRow.Children = append(clockRow.Children,
			&v1.Node{Kind: v1.KindText, Text: "resets in " + cd, Tabular: true})
		if !w.ResetsAt.IsZero() {
			clockRow.Children = append(clockRow.Children,
				&v1.Node{Kind: v1.KindText, Text: w.ResetsAt.Format("Mon 15:04"), Tone: v1.ToneSubtle, Size: "caption"})
		}
	default:
		clockRow.Children = append(clockRow.Children,
			&v1.Node{Kind: v1.KindText, Text: w.resetLine(), Tone: v1.ToneSubtle, Size: "caption"})
	}
	card.Children = append(card.Children, clockRow)
	if pacePts, ok := Pace(w, now); ok {
		sign, word := "↑", "ahead"
		if pacePts < 0 {
			sign, word = "↓", "behind"
		}
		card.Children = append(card.Children, &v1.Node{Kind: v1.KindText,
			Text: fmt.Sprintf("%s%d pace — %d points %s of the clock", sign, abs(pacePts), abs(pacePts), word),
			Tone: v1.ToneSubtle, Size: "caption"})
	}
	if w.DisplayValue != "" {
		card.Children = append(card.Children,
			&v1.Node{Kind: v1.KindText, Text: w.DisplayValue, Tone: v1.ToneSubtle, Size: "caption"})
	}
	return card
}

func (w Window) resetLine() string {
	if !w.ResetsAt.IsZero() {
		return "resets " + w.ResetsAt.Format("Mon 15:04")
	}
	if w.ResetDescription != "" {
		return w.ResetDescription
	}
	return "reset time unknown"
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// trendCaption is the sparkline's one-line read: last-two-snapshot delta.
func trendCaption(hist []float64) string {
	if len(hist) < 2 {
		return "collecting history…"
	}
	delta := hist[len(hist)-1] - hist[len(hist)-2]
	switch {
	case delta >= 1:
		return fmt.Sprintf("↑ %.0fpts over the last reading", delta)
	case delta <= -1:
		return fmt.Sprintf("↓ %.0fpts over the last reading", -delta)
	default:
		return "flat over the last reading"
	}
}

// humanizeAge renders a duration the freshness line uses.
func humanizeAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if m := int(d.Minutes()); m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	if h := int(d.Hours()); h < 24 {
		return fmt.Sprintf("%dh %dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

// TooltipTree is the hover content the shell opens for the bar widget: a
// text-only view (tooltips reject interactive nodes) naming the displayed
// provider, its headline window, and the reset.
func TooltipTree(r Report, inst Instance, cfg Config, hostMinor int, now time.Time) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 2}
	p := selectProvider(r, inst.Vendor, cfg.Warn, cfg.Crit)
	if p == nil {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: "No readings yet", Tone: v1.ToneSubtle})
		return col
	}
	h := Headline(p.Windows)
	if h == nil {
		line := p.Name + ": no readings yet"
		if p.Err != "" {
			line = p.Name + ": " + p.Err
		}
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: line, Tone: v1.ToneSubtle})
		return col
	}
	line := fmt.Sprintf("%s · %s %v%%", p.Name, h.Label, h.UsedPercent)
	if cd := FormatCountdown(h.ResetsAt, now); cd != "" {
		line += " · resets in " + cd
	}
	col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: line})
	if p.staleFor(cfg, now) {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: "stale — last read kept", Tone: v1.ToneError, Size: "caption"})
	}
	return col
}
