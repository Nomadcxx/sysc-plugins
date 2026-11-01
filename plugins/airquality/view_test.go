package airquality

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// sampleReport is a realistic AirHealth-shaped report: grass is the worst
// measure, air quality is a numeric PM2.5 reading, and thunderstorm asthma is
// present because Melbourne is inside the Oct–Dec season.
func sampleReport() Report {
	measures := []Measure{
		{Name: MeasureGrass, Title: Title(MeasureGrass), Level: Level{Name: "High", Index: 2, Severity: SeverityHigh}, Metric: &Metric{Subcat: "Grass", Value: 86, Units: "grains/m³"}, Source: "airhealth"},
		{Name: MeasureAllergens, Title: Title(MeasureAllergens), Level: Level{Name: "Moderate", Index: 1, Severity: SeverityModerate}, Metric: &Metric{Subcat: "Alternaria", Value: 34, Units: "grains/m³"}, Source: "airhealth"},
		{Name: MeasureAirQuality, Title: Title(MeasureAirQuality), Level: Level{Name: "Fair", Index: 2, Severity: SeverityModerate}, Metric: &Metric{Subcat: "PM2.5", Value: 19.9, Units: "ug/m3"}, Source: "airhealth"},
		{Name: MeasureThunderstorm, Title: Title(MeasureThunderstorm), Level: Level{Name: "Moderate", Index: 1, Severity: SeverityModerate}, Source: "airhealth"},
	}
	// Production attaches the published zones when it normalises each measure;
	// the fixture goes through the same path so a dial test exercises real
	// bands rather than hand-written ones.
	for i := range measures {
		WireScale.AttachDial(&measures[i])
	}
	return Report{
		UpdatedAt: time.Now().Add(-7 * time.Minute),
		Snapshot: Snapshot{
			Location: Location{Suburb: "Melbourne", State: "VIC", SAL: "21640", SA3: "20604"},
			Date:     "2026-10-31",
			Measures: measures,
			Alert:    &Alert{Title: "Thunderstorm Asthma", Text: "Moderate.", Level: Level{Name: "Moderate", Index: 1, Severity: SeverityModerate}},
			Forecast: []Day{
				{Date: "2026-11-01", Title: "Sunday", Measures: map[string]Measure{
					MeasureGrass: {Name: MeasureGrass, Level: Level{Name: "Moderate", Index: 1, Severity: SeverityModerate}},
				}},
				{Date: "2026-11-02", Title: "Monday", Measures: map[string]Measure{
					MeasureGrass:      {Name: MeasureGrass, Level: Level{Name: "High", Index: 2, Severity: SeverityHigh}},
					MeasureAirQuality: {Name: MeasureAirQuality, Level: Level{Name: "Very Good", Index: 0, Severity: SeverityGood}},
				}},
			},
			Attribution: AirHealthAttribution,
		},
	}
}

// staleReport is the empty-because-offline case: no snapshot at all.
func offlineReport() Report {
	return Report{Errors: []string{"airhealth unavailable"}, Stale: true}
}

func lintView(t *testing.T, root *v1.Node, view v1.ViewKind, width, height int) {
	t.Helper()
	if root == nil {
		t.Fatalf("%s view is nil", view)
	}
	for _, finding := range shelllint.Tree(root, view, width, height) {
		t.Errorf("%s at %dx%d: %s", view, width, height, finding)
	}
}

func TestBarFitsEveryWidth(t *testing.T) {
	t.Parallel()
	for _, width := range []int{shelllint.BarWidth, 240, 180, 120, 119, 96, 64, 40, 32} {
		bar := Bar(sampleReport(), width)
		if bar.Kind != v1.KindRow {
			t.Fatalf("width %d: bar root = %q, want row", width, bar.Kind)
		}
		if len(bar.Children) != 1 {
			t.Fatalf("width %d: bar has %d children, want the one control", width, len(bar.Children))
		}
		lintView(t, bar, v1.ViewBar, width, shelllint.BarHeight)
	}
}

func TestBarControlIsInteractiveAndNamed(t *testing.T) {
	t.Parallel()
	control := Bar(sampleReport(), shelllint.BarWidth).Children[0]
	if control.ID != "open" || control.Name == "" || control.Role != "button" {
		t.Fatalf("control = %+v", control)
	}
	if len(control.Events) != 1 || control.Events[0] != v1.EventActivate {
		t.Fatalf("control events = %v", control.Events)
	}
}

// A narrow side-bar slot cannot fit the label, so the bar drops to the icon
// and its accessible name, never truncating mid-word at the host's edge.
func TestBarDropsLabelWhenNarrow(t *testing.T) {
	t.Parallel()
	narrow := Bar(sampleReport(), 40).Children[0]
	if len(narrow.Children) != 1 || narrow.Children[0].Kind != v1.KindIcon {
		t.Fatalf("narrow bar children = %+v", narrow.Children)
	}
	if narrow.Name == "" {
		t.Fatal("narrow bar control lost its accessible name")
	}
	wide := Bar(sampleReport(), shelllint.BarWidth).Children[0]
	if len(wide.Children) != 2 || wide.Children[1].Kind != v1.KindText {
		t.Fatalf("wide bar children = %+v", wide.Children)
	}
}

func TestBarWithNoReadingStillRenders(t *testing.T) {
	t.Parallel()
	for _, width := range []int{shelllint.BarWidth, 64} {
		bar := Bar(offlineReport(), width)
		lintView(t, bar, v1.ViewBar, width, shelllint.BarHeight)
		control := bar.Children[0]
		if control.Name == "" {
			t.Fatalf("width %d: offline control has no name", width)
		}
		if len(control.Children) == 0 || control.Children[0].Icon == "" {
			t.Fatalf("width %d: offline control has no glyph", width)
		}
	}
}

func TestBarTextNamesWorstMeasure(t *testing.T) {
	t.Parallel()
	text := BarText(sampleReport())
	if !strings.Contains(text, "Grass") {
		t.Fatalf("bar text = %q, want the worst measure", text)
	}
	if !strings.Contains(text, "high") {
		t.Fatalf("bar text = %q, want the category", text)
	}
	if len(text) > barTextBytes {
		t.Fatalf("bar text = %q is %d bytes, over the %d budget", text, len(text), barTextBytes)
	}
}

// A UV reading is quoted as a bare number nationally, so the bar leads with the
// figure and follows with the band. It must also use the short measure name:
// the full "UV Index" pushed the label past the narrow bar's budget.
func TestBarTextLeadsWithTheUVFigure(t *testing.T) {
	t.Parallel()
	report := Report{Snapshot: Snapshot{Measures: []Measure{{
		Name:   MeasureUV,
		Title:  Title(MeasureUV),
		Level:  Level{Name: uvHigh, Index: 2, Severity: SeverityHigh},
		Metric: &Metric{Subcat: "today's peak", Value: 6.4, Units: ""},
	}}}}
	text := BarText(report)
	if want := "UV 6.4 high"; text != want {
		t.Fatalf("bar text = %q, want %q", text, want)
	}
}

func TestTooltipFitsEveryGeometry(t *testing.T) {
	t.Parallel()
	for _, width := range []int{shelllint.TooltipWidth, 280, 200} {
		root := TooltipFor(sampleReport(), width)
		lintView(t, root, v1.ViewTooltip, width, shelllint.TooltipHeight)
		budget := (width - 2*tooltipPadding) / 8
		var inspect func(*v1.Node)
		inspect = func(node *v1.Node) {
			if node.Kind == v1.KindText && len(node.Text) > budget {
				t.Errorf("width %d: tooltip line %q exceeds %d bytes", width, node.Text, budget)
			}
			for _, child := range node.Children {
				inspect(child)
			}
		}
		inspect(root)
	}
	lintView(t, TooltipFor(offlineReport(), shelllint.TooltipWidth), v1.ViewTooltip,
		shelllint.TooltipWidth, shelllint.TooltipHeight)
}

func TestTooltipShowsUVPeakAndDominantAllergen(t *testing.T) {
	report := sampleReport()
	report.Snapshot.Alert = nil
	report.Snapshot.Measures = []Measure{{
		Name: MeasureAllergens, Title: Title(MeasureAllergens),
		Level:  Level{Name: "Moderate", Severity: SeverityModerate},
		Metric: &Metric{Subcat: "Alternaria", Value: 34, Units: "grains/m³"},
	}, {
		Name: MeasureUV, Title: Title(MeasureUV),
		Level:     Level{Name: "High", Severity: SeverityHigh},
		Metric:    &Metric{Subcat: "today's peak", Value: 6.4},
		Breakdown: []Breakdown{{Name: "now", Metric: &Metric{Value: 0}}},
	}}
	report.Snapshot.Attribution = AirHealthAttribution + " · " + OpenMeteoAttribution
	root := TooltipFor(report, shelllint.TooltipWidth)
	lintView(t, root, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight)
	text := nodeText(root)
	for _, want := range []string{"peak today", "6.4", "Alternaria", "34", AirHealthAttribution, OpenMeteoAttribution} {
		if !strings.Contains(text, want) {
			t.Errorf("tooltip text %q is missing %q", text, want)
		}
	}
}

func TestNarrowTooltipFitsStormSummaryAndAllSourceCredits(t *testing.T) {
	report := sampleReport()
	report.Snapshot.Attribution = AirHealthAttribution + " · " + EPAAttribution + " · " + OpenMeteoAttribution
	root := TooltipFor(report, 200)
	lintView(t, root, v1.ViewTooltip, 200, shelllint.TooltipHeight)
	text := nodeText(root)
	for _, want := range []string{
		"Storm: Moderate", "AirHealth", "University of Tasmania", "University of Melbourne",
		"EPA Victoria", "CC BY 4.0", "CAMS ENSEMBLE", "Open-Meteo",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("narrow tooltip text %q is missing %q", text, want)
		}
	}
}

// The panel is the one view that can genuinely overflow, because it grows with
// the number of measures. Lint at the manifest's declared box.
func TestPanelFitsDeclaredBox(t *testing.T) {
	t.Parallel()
	lintView(t, Panel(sampleReport()), v1.ViewPanel, PanelWidth, PanelHeight)
}

func TestPanelFitsWithLongAttributionAndErrors(t *testing.T) {
	t.Parallel()
	report := sampleReport()
	report.Snapshot.Attribution = AirHealthAttribution + " · " + EPAAttribution
	report.Errors = []string{"epa unavailable"}
	report.Stale = true
	lintView(t, Panel(report), v1.ViewPanel, PanelWidth, PanelHeight)
}

func TestPanelFitsWithAllMeasures(t *testing.T) {
	t.Parallel()
	report := sampleReport()
	report.Snapshot.Measures = append(report.Snapshot.Measures,
		Measure{Name: MeasureWoodsmoke, Level: Level{Name: "Low", Index: 0, Severity: SeverityGood}})
	lintView(t, Panel(report), v1.ViewPanel, PanelWidth, PanelHeight)
}

func TestPanelWithoutDataFits(t *testing.T) {
	t.Parallel()
	lintView(t, Panel(offlineReport()), v1.ViewPanel, PanelWidth, PanelHeight)
}

func TestPanelOffersRetryWhenSourcesFail(t *testing.T) {
	root := Panel(offlineReport())
	card := nodeWithFill(root, "error-container")
	if card == nil {
		t.Fatal("failed report has no error card")
	}
	if !strings.Contains(nodeText(card), "airhealth unavailable") {
		t.Fatalf("error card omits the source failure: %q", nodeText(card))
	}
	heading := firstKind(card, v1.KindText)
	if heading == nil || heading.Tone != v1.ToneNormal {
		t.Fatalf("error card heading = %+v, want normal foreground on the error surface", heading)
	}
	button := nodeWithID(card, "refresh")
	if button == nil || button.Name != "Retry air quality and pollen sources" ||
		button.Text != "Retry" || button.Icon != iconRefresh || button.Role != "button" ||
		button.Height != 36 || button.Padding != 14 || len(button.Events) != 1 || button.Events[0] != v1.EventActivate {
		t.Fatalf("retry control = %+v", button)
	}
}

func TestPanelRootKindIsAllowed(t *testing.T) {
	t.Parallel()
	root := Panel(sampleReport())
	if root.Kind != v1.KindList {
		t.Fatalf("panel root = %q, want scrollable list", root.Kind)
	}
	if root.Padding != panelPad {
		t.Fatalf("panel padding = %d, want %d", root.Padding, panelPad)
	}
	if root.Height != PanelHeight || len(root.Events) != 1 || root.Events[0] != v1.EventScroll {
		t.Fatalf("panel scroll = height %d, events %v; want %d and scroll", root.Height, root.Events, PanelHeight)
	}
}

// Every icon must exist in one of the shell's two icon catalogues, or the host
// refuses the whole view. The catalogues live in an internal package, so the
// only way to ask is to build a view with the glyph and lint it: the converter
// fails with "no icon named ..." before layout.
func TestAllIconsExist(t *testing.T) {
	t.Parallel()
	used := []string{
		iconPollen, iconAir, iconStorm, iconSmoke,
		iconGood, iconModerate, iconHigh, iconAlert, iconRefresh,
	}
	for _, s := range []Severity{SeverityGood, SeverityModerate, SeverityHigh} {
		used = append(used, SeverityIcon(s))
	}
	for _, m := range []string{MeasureGrass, MeasureAllergens, MeasureAirQuality, MeasureThunderstorm, MeasureWoodsmoke} {
		used = append(used, MeasureIcon(m))
	}
	for _, name := range used {
		bar := &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
			Kind: v1.KindButton, ID: "open", Name: name, Role: "button",
			Events:   []v1.EventKind{v1.EventActivate},
			Children: []*v1.Node{{Kind: v1.KindIcon, Icon: name}},
		}}}
		for _, finding := range shelllint.Tree(bar, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
			t.Errorf("icon %q: %s", name, finding)
		}
	}
}

func TestFormatValue(t *testing.T) {
	t.Parallel()
	cases := map[float64]string{0: "0", 1: "1", 12.5: "12.5", 19.94: "19.9", 150: "150"}
	for in, want := range cases {
		if got := formatValue(in); got != want {
			t.Errorf("formatValue(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestUnitsLabel(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ug/m3": "µg/m³", "µg/m³": "µg/m³", "ppb": "ppb", "pphm": "pphm", "": "",
	}
	for in, want := range cases {
		if got := unitsLabel(in); got != want {
			t.Errorf("unitsLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgeTextNeverClaimsFreshness(t *testing.T) {
	t.Parallel()
	stale := sampleReport()
	stale.Stale = true
	if text := ageText(stale); !strings.HasPrefix(text, "stale") {
		t.Fatalf("stale age text = %q", text)
	}
	if text := ageText(Report{}); text != "never updated" {
		t.Fatalf("zero age text = %q", text)
	}
	// A clock that jumped backwards must not print a negative age.
	future := Report{UpdatedAt: time.Now().Add(time.Hour)}
	if text := ageText(future); strings.Contains(text, "-") {
		t.Fatalf("future age text = %q", text)
	}
}

// Forecast rows skip days with no data rather than printing blank categories,
// so a forecast of nothing must collapse to no section at all.
func TestForecastSectionSkipsEmptyDays(t *testing.T) {
	t.Parallel()
	if section := forecastSection(nil); section != nil {
		t.Fatalf("empty forecast = %+v, want nil", section)
	}
	blank := []Day{{Date: "2026-11-01", Title: "Sunday"}}
	if section := forecastSection(blank); section != nil {
		t.Fatalf("day without measures = %+v, want nil", section)
	}
}

// TestPanelFitsWithBreakdowns is the geometry test that matters most: the
// breakdown strip is added under the worst measure at render time, so a panel
// that fits without it can still be refused once a real reading arrives with
// constituents attached.
func TestPanelFitsWithBreakdowns(t *testing.T) {
	report := sampleReport()
	for i := range report.Snapshot.Measures {
		if report.Snapshot.Measures[i].Name != MeasureGrass {
			continue
		}
		report.Snapshot.Measures[i].Breakdown = []Breakdown{
			{Name: "Birch", Level: Level{Name: "High", Severity: SeverityHigh}},
			{Name: "Cypress", Level: Level{Name: "Moderate", Severity: SeverityModerate}},
			{Name: "Eucalypts", Level: Level{Name: "High", Severity: SeverityHigh}},
			{Name: "Alternaria", Level: Level{Name: "Extreme", Severity: SeverityHigh},
				Metric: &Metric{Value: 128, Units: "grains/m³"}},
		}
	}
	lintView(t, Panel(report), v1.ViewPanel, PanelWidth, PanelHeight)
}

// TestPanelFitsWithEveryMetric makes the widest realistic row: every measure
// carrying both a category and a number, plus UV, plus a breakdown.
func TestPanelFitsWithEveryMetric(t *testing.T) {
	report := sampleReport()
	report.Snapshot.Measures = append(report.Snapshot.Measures,
		Measure{Name: MeasureUV, Title: Title(MeasureUV),
			Level:  Level{Name: uvVeryHigh, Severity: SeverityHigh},
			Metric: &Metric{Subcat: "today's peak", Value: 10.4},
			Source: "open-meteo"},
		Measure{Name: MeasureWoodsmoke, Title: Title(MeasureWoodsmoke),
			Level: Level{Name: "Moderate", Severity: SeverityModerate}},
	)
	for i := range report.Snapshot.Measures {
		if report.Snapshot.Measures[i].Metric == nil {
			report.Snapshot.Measures[i].Metric = &Metric{Value: 33.5, Units: "ug/m3"}
		}
	}
	report.Snapshot.Measures[1].Breakdown = []Breakdown{
		{Name: "PM2.5", Metric: &Metric{Value: 19.9, Units: "µg/m³"}},
		{Name: "O3", Metric: &Metric{Value: 57, Units: "µg/m³"}},
		{Name: "NO2", Metric: &Metric{Value: 3.1, Units: "µg/m³"}},
	}
	report.Snapshot.Attribution = AirHealthAttribution + " · " + EPAAttribution + " · " + OpenMeteoAttribution
	lintView(t, Panel(report), v1.ViewPanel, PanelWidth, PanelHeight)
}

func TestMeasureValueTextKeepsCategoryAndNumber(t *testing.T) {
	cases := []struct {
		m    Measure
		want string
	}{
		{Measure{Name: MeasureGrass, Level: Level{Name: "High"}}, "High"},
		{Measure{Name: MeasureGrass}, "—"},
		{Measure{Name: MeasureAirQuality, Level: Level{Name: "Fair"},
			Metric: &Metric{Value: 19.9, Units: "ug/m3"}}, "Fair · 19.9 µg/m³"},
		{Measure{Name: MeasureUV, Level: Level{Name: "Moderate"},
			Metric: &Metric{Value: 4.9}}, "Today's peak 4.9 Moderate"},
		{Measure{Name: MeasureUV, Metric: &Metric{Value: 4.9}}, "Today's peak 4.9"},
	}
	for _, c := range cases {
		if got := measureValueText(c.m); got != c.want {
			t.Errorf("measureValueText(%s) = %q, want %q", c.m.Name, got, c.want)
		}
	}
}

func TestUVValueAndDialNameIdentifyTodaysPeak(t *testing.T) {
	uv := Measure{
		Name: MeasureUV, Level: Level{Name: "High", Severity: SeverityHigh},
		Metric: &Metric{Subcat: "today's peak", Value: 6.4}, Max: uvDialMax, Bands: UVBands(),
	}
	if got, want := measureValueText(uv), "Today's peak 6.4 High"; got != want {
		t.Fatalf("UV measure value = %q, want %q", got, want)
	}
	report := Report{Snapshot: Snapshot{Measures: []Measure{uv}}}
	if got, want := DialLabel(report, 25), "UV index · today's peak"; got != want {
		t.Fatalf("UV dial label = %q, want %q", got, want)
	}
}

func TestCurrentUVFallbackIsNotLabelledAsTodaysPeak(t *testing.T) {
	uv := Measure{
		Name: MeasureUV, Level: Level{Name: "Low", Severity: SeverityGood},
		Metric: &Metric{Subcat: "now", Value: 1.2}, Max: uvDialMax, Bands: UVBands(),
	}
	if got, want := measureValueText(uv), "Now 1.2 Low"; got != want {
		t.Fatalf("current UV row = %q, want %q", got, want)
	}
	if got, want := tooltipLines(uv)[0], "UV 1.2 · now · Low"; got != want {
		t.Fatalf("current UV tooltip = %q, want %q", got, want)
	}
	if got, want := measureText(uv, Report{}), "UV now 1.2 low"; got != want {
		t.Fatalf("current UV bar label = %q, want %q", got, want)
	}
	report := Report{Snapshot: Snapshot{Measures: []Measure{uv}}}
	if got, want := DialLabel(report, 25), "UV index · now"; got != want {
		t.Fatalf("current UV dial caption = %q, want %q", got, want)
	}
	if got, want := Dial(report, 25).Tooltip, "UV index: now 1.2 Low"; got != want {
		t.Fatalf("current UV dial tooltip = %q, want %q", got, want)
	}
}

func TestAlertCardShowsSourceExplanation(t *testing.T) {
	const explanation = "There is more than one of the conditions necessary for an epidemic thunderstorm asthma event."
	card := alertCard(Alert{
		Title: "Thunderstorm Asthma", Text: explanation,
		Level: Level{Name: "High", Severity: SeverityHigh},
	}, false)
	if card.Fill != "error-container" || card.Radius != 12 || card.Padding != 8 {
		t.Fatalf("alert card surface = fill %q, radius %d, padding %d", card.Fill, card.Radius, card.Padding)
	}
	if text := nodeText(card); !strings.Contains(text, explanation) {
		t.Fatalf("alert card text = %q, want source explanation", text)
	}
	heading := firstKind(card, v1.KindText)
	if heading == nil || heading.Tone != v1.ToneNormal || card.Children[1].Tone != v1.ToneNormal {
		t.Fatalf("high alert tones = heading %+v, detail %+v; want normal foreground on the error surface", heading, card.Children[1])
	}
}

func TestAlertCardDistinguishesModerateAndStaleWarnings(t *testing.T) {
	alert := Alert{Title: "Thunderstorm Asthma", Text: "Elevated risk forecast.",
		Level: Level{Name: "Moderate", Severity: SeverityModerate}}
	moderate := alertCard(alert, false)
	heading := firstKind(moderate, v1.KindText)
	if moderate.Fill != "card" || heading == nil || heading.Tone != v1.ToneWarning {
		t.Fatalf("moderate alert = fill %q, heading %+v; want a caution card", moderate.Fill, heading)
	}

	alert.Level = Level{Name: "High", Severity: SeverityHigh}
	stale := alertCard(alert, true)
	if stale.Fill != "card" || !strings.Contains(nodeText(stale), "Last alert · Thunderstorm Asthma · High") {
		t.Fatalf("stale alert = fill %q, text %q; want a neutral, historical alert", stale.Fill, nodeText(stale))
	}
}

func TestBreakdownRowCapsAtFour(t *testing.T) {
	m := Measure{Name: MeasureAllergens, Breakdown: []Breakdown{
		{Name: "Birch"}, {Name: "Cypress"}, {Name: "Eucalypts"}, {Name: "Olive"}, {Name: "Plane"},
	}}
	row := breakdownRow(m)
	// Four chips plus three separators; the fifth is dropped rather than
	// overflowing the row.
	chips := 0
	for _, c := range row.Children {
		if c.Text != "·" {
			chips++
		}
	}
	if chips != 4 {
		t.Errorf("chips = %d, want 4", chips)
	}
}

func TestDialAppearsForHostThatSupportsIt(t *testing.T) {
	t.Parallel()

	root := PanelFor(sampleReport(), 24)
	if !hasKind(root, v1.KindGauge) {
		t.Fatal("panel has no dial on a minor-twenty-four host")
	}
}

func TestDialIsOmittedForOlderHost(t *testing.T) {
	t.Parallel()

	root := PanelFor(sampleReport(), 23)
	if hasKind(root, v1.KindGauge) {
		t.Fatal("panel offered a dial to a host below minor twenty-four")
	}
	// The information is not lost, only the shape.
	if !hasKind(root, v1.KindRow) {
		t.Fatal("panel dropped the rows as well as the dial")
	}
}

func TestDialCarriesTextAsWellAsColour(t *testing.T) {
	t.Parallel()

	root := PanelFor(sampleReport(), 24)
	gauge := firstKind(root, v1.KindGauge)
	if gauge == nil {
		t.Fatal("no dial")
	}
	// A colour ramp alone cannot be read, so the label must name both the
	// figure and the category.
	if !strings.ContainsAny(gauge.ValueText, "0123456789") {
		t.Fatalf("dial label %q has no figure", gauge.ValueText)
	}
	if !strings.Contains(gauge.ValueText, "High") {
		t.Fatalf("dial label %q has no category", gauge.ValueText)
	}
	if len(gauge.Bands) == 0 {
		t.Fatal("dial has no zones")
	}
	if gauge.Form != v1.GaugeHalf {
		t.Fatalf("dial form = %q, want half", gauge.Form)
	}
	if gauge.Name != "Grass pollen" || gauge.Role != "img" {
		t.Fatalf("dial accessible identity = %q/%q", gauge.Name, gauge.Role)
	}
	if !strings.Contains(gauge.Tooltip, "Grass pollen") || !strings.Contains(gauge.Tooltip, "High") {
		t.Fatalf("dial tooltip = %q, want measure and category", gauge.Tooltip)
	}
}

func TestMinorTwentyFiveDialsGrassAtItsCategoryMidpoint(t *testing.T) {
	t.Parallel()

	report := sampleReport()
	grass, ok := report.Snapshot.Measure(MeasureGrass)
	if !ok {
		t.Fatal("grass measure missing")
	}
	for i := range report.Snapshot.Measures {
		if report.Snapshot.Measures[i].Name != MeasureGrass {
			continue
		}
		grass = report.Snapshot.Measures[i]
		grass.Metric = nil
		grass.Max = 0
		grass.Bands = nil
		WireScale.AttachDial(&grass)
		report.Snapshot.Measures[i] = grass
	}

	root := PanelFor(report, 25)
	gauge := firstKind(root, v1.KindGauge)
	if gauge == nil {
		t.Fatal("no category-only grass dial")
	}
	if gauge.Value < 0.624 || gauge.Value > 0.626 {
		t.Fatalf("High category midpoint = %v, want 0.625", gauge.Value)
	}
	if gauge.ValueText != "High" {
		t.Fatalf("category-only centre label = %q, want High without a fabricated count", gauge.ValueText)
	}
	if got := DialLabel(report, 25); got != "Grass pollen · category position" {
		t.Fatalf("category-only dial caption = %q", got)
	}
	raw, err := json.Marshal(gauge)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Low", "Moderate", "High", "Extreme"} {
		if !strings.Contains(string(raw), `"label":"`+label+`"`) {
			t.Errorf("gauge omitted the %q threshold label: %s", label, raw)
		}
	}

	legacy := PanelFor(report, 24)
	legacyGauge := firstKind(legacy, v1.KindGauge)
	legacyMeasure, legacyOK := dialMeasureForProtocol(report, 24)
	if legacyGauge == nil || !legacyOK || legacyMeasure.Metric == nil || legacyMeasure.Name == MeasureGrass {
		t.Fatal("minor 24 did not keep the numeric dial fallback")
	}
	if strings.Contains(DialLabel(report, 24), "category position") {
		t.Fatalf("minor 24 received category-only caption %q", DialLabel(report, 24))
	}
	legacyRaw, err := json.Marshal(legacyGauge)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyRaw), `"label"`) {
		t.Fatalf("minor 24 received minor 25 band labels: %s", legacyRaw)
	}
}

func TestDialPicksWorstMeasureThatCanBeDrawn(t *testing.T) {
	t.Parallel()

	report := sampleReport()
	// Make thunderstorm asthma the worst reading. It is ordinal, so it gets no
	// dial; the next worst measure should take the arc instead.
	for i := range report.Snapshot.Measures {
		switch report.Snapshot.Measures[i].Name {
		case MeasureThunderstorm:
			report.Snapshot.Measures[i].Level = Level{Name: "High", Severity: SeverityHigh}
		case MeasureGrass:
			report.Snapshot.Measures[i].Level = Level{Name: "Moderate", Severity: SeverityModerate}
		}
	}
	m, ok := dialMeasure(report)
	if !ok {
		t.Fatal("no dialable measure")
	}
	if m.Name == MeasureThunderstorm {
		t.Fatal("thunderstorm asthma was put on a dial")
	}
}

func TestDialFitsThePanel(t *testing.T) {
	t.Parallel()

	lintView(t, PanelFor(sampleReport(), 24), v1.ViewPanel, PanelWidth, PanelHeight)
}

// hasKind and firstKind walk the tree so tests can assert on shape without
// depending on child indices.
func hasKind(root *v1.Node, kind v1.NodeKind) bool {
	return firstKind(root, kind) != nil
}

func firstKind(root *v1.Node, kind v1.NodeKind) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Kind == kind {
		return root
	}
	for _, child := range root.Children {
		if found := firstKind(child, kind); found != nil {
			return found
		}
	}
	return nil
}

// The bar's label budget has to follow the host's width. A label that overruns
// refuses the entire row, and the old fixed 34-byte cap assumed a 240px slot.
func TestMeasureRowPinsTheReadingToTheRowEnd(t *testing.T) {
	// A real render showed "Air qualityFair · 19.9 µg/m³" flowed together
	// because PinEnd sat on the trailing text instead of the row. The lint
	// suite passed regardless: a pin that does nothing still fits.
	row := measureRow(Measure{
		Name:   MeasureAirQuality,
		Level:  Level{Name: "Fair", Severity: SeverityModerate},
		Metric: &Metric{Subcat: "PM2.5", Value: 19.9, Units: "µg/m³"},
	})
	if !row.PinEnd {
		t.Fatal("measure row does not pin its trailing reading")
	}
	if len(row.Children) != 2 {
		t.Fatalf("measure row has %d children, want 2 so PinEnd reserves the trailing group", len(row.Children))
	}
	if row.Children[1].PinEnd {
		t.Error("PinEnd is set on the child; layout only reads it from the row")
	}
	if got := row.Children[1].Text; got != "Fair · 19.9 µg/m³" {
		t.Errorf("trailing text = %q", got)
	}
	if got := row.Children[1].Tone; got != v1.ToneNormal {
		t.Errorf("reading tone = %q, want normal foreground for contrast", got)
	}
	if got := row.Children[0].Children[0].Tone; got != v1.ToneAccent {
		t.Errorf("severity icon tone = %q, want accent", got)
	}
}

func TestTooltipReadingsUseNormalForeground(t *testing.T) {
	root := Tooltip(sampleReport())
	text := nodeText(root)
	for _, want := range []string{"Storm: Moderate", "Grass: High", "Air: Fair 19.9 µg/m³"} {
		if !strings.Contains(text, want) {
			t.Errorf("tooltip text %q does not include %q", text, want)
		}
	}
	var visit func(*v1.Node)
	visit = func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Tone != v1.ToneNormal && n.Tone != v1.ToneSubtle {
			t.Errorf("tooltip text %q tone = %q, want normal foreground", n.Text, n.Tone)
		}
		for _, child := range n.Children {
			visit(child)
		}
	}
	visit(root)
}

func TestForecastRowUsesTheDayItNames(t *testing.T) {
	day := Day{
		Date:  "2026-11-01",
		Title: "Sunday",
		Measures: map[string]Measure{
			MeasureGrass: {Name: MeasureGrass, Level: Level{Name: "Moderate", Severity: SeverityModerate}},
		},
	}
	row := forecastRow(day)
	if row == nil {
		t.Fatal("forecast row is nil for a day with one known measure")
	}
	if got := row.Children[0].Text; got != "Sunday" {
		t.Errorf("label = %q, want the day title", got)
	}
	if got := row.Children[1].Text; got != "Grass moderate" {
		t.Errorf("readings = %q", got)
	}
}

func TestBreakdownSkipsATimeOfDayPlaceholder(t *testing.T) {
	// The UV breakdown carries "now" beside today's peak. Rendered beneath the
	// dial at night it read "now 0", which is a clock statement rather than a
	// reading, so a zero-valued time-of-day entry is noise.
	m := Measure{
		Name:   MeasureUV,
		Level:  Level{Name: "Moderate", Severity: SeverityModerate},
		Metric: &Metric{Subcat: "today's peak", Value: 4.9},
		Breakdown: []Breakdown{
			{Name: "now", Metric: &Metric{Subcat: "now", Value: 0}},
		},
	}
	row := breakdownRow(m)
	if row != nil {
		for _, child := range row.Children {
			if child.Text == "now 0" {
				t.Fatal("breakdown still shows a zero time-of-day entry")
			}
		}
	}
}

func TestBreakdownKeepsRealConstituents(t *testing.T) {
	m := Measure{
		Name:   MeasureAirQuality,
		Level:  Level{Name: "Fair", Severity: SeverityModerate},
		Metric: &Metric{Subcat: "PM2.5", Value: 19.9},
		Breakdown: []Breakdown{
			{Name: "PM2.5", Metric: &Metric{Value: 19.9}},
			{Name: "PM10", Metric: &Metric{Value: 7.6}},
		},
	}
	row := breakdownRow(m)
	if row == nil {
		t.Fatal("a pollutant breakdown produced no row")
	}
	var texts []string
	for _, child := range row.Children {
		texts = append(texts, child.Text)
	}
	want := "PM2.5 19.9 · PM10 7.6"
	if got := strings.Join(texts, " "); got != want {
		t.Errorf("strip = %q, want %q", got, want)
	}
	if row.Children[0].Tone != v1.ToneNormal || row.Children[2].Tone != v1.ToneNormal {
		t.Errorf("breakdown tones = %q and %q, want normal foreground", row.Children[0].Tone, row.Children[2].Tone)
	}
}

func TestPanelStaysInsideItsDeclaredBox(t *testing.T) {
	// A real render of the busiest honest report -- alert, five measures, a
	// dial, a breakdown, three forecast days and a long attribution -- needed
	// 665px in the old 560px panel, so the forecast and attribution were cut off.
	// The lint helper measures text, so it can catch this where a snapshot
	// comparison cannot.
	report := sampleReport()
	report.Snapshot.Alert = &Alert{
		Title: "Thunderstorm Asthma",
		Text:  "The Thunderstorm Asthma forecast for this location is currently: Moderate.",
		Level: Level{Name: "Moderate", Severity: SeverityModerate},
	}
	report.Snapshot.Measures = append(report.Snapshot.Measures,
		Measure{Name: MeasureUV, Title: Title(MeasureUV), Level: Level{Name: "High", Severity: SeverityHigh},
			Metric: &Metric{Subcat: "today's peak", Value: 6}, Max: uvDialMax, Bands: UVBands()},
		Measure{Name: MeasureWoodsmoke, Title: Title(MeasureWoodsmoke), Level: Level{Name: "Low", Severity: SeverityGood},
			Metric: &Metric{Subcat: "PM2.5", Value: 12, Units: "µg/m³"}},
	)
	report.Snapshot.Attribution = "Data provided in collaboration with the University of Tasmania and " +
		"University of Melbourne · Air quality data: EPA Victoria (CC BY 4.0) · " +
		"Air quality and UV: CAMS ENSEMBLE via Open-Meteo"
	report.Snapshot.Forecast = []Day{
		{Title: "Sunday", Measures: map[string]Measure{MeasureGrass: {Name: MeasureGrass, Level: Level{Name: "High", Severity: SeverityHigh}}}},
		{Title: "Monday", Measures: map[string]Measure{MeasureGrass: {Name: MeasureGrass, Level: Level{Name: "Moderate", Severity: SeverityModerate}}}},
		{Title: "Tuesday", Measures: map[string]Measure{MeasureAirQuality: {Name: MeasureAirQuality, Level: Level{Name: "Good", Severity: SeverityGood}}}},
	}
	lintView(t, PanelFor(report, 24), v1.ViewPanel, PanelWidth, PanelHeight)
}

func TestAlertCardReplacesTheMeasureItNames(t *testing.T) {
	// The alert restates one measure verbatim. Drawing both repeated the
	// thunderstorm reading four pixels apart and pushed the forecast off the
	// panel.
	report := sampleReport()
	report.Snapshot.Alert = &Alert{
		Title: "Thunderstorm Asthma",
		Text:  "The Thunderstorm Asthma forecast for this location is currently: Moderate.",
		Level: Level{Name: "Moderate", Severity: SeverityModerate},
	}
	seen := 0
	for _, m := range panelMeasureRows(PanelFor(report, 24)) {
		if m == "Thunderstorm asthma" {
			seen++
		}
	}
	if seen != 0 {
		t.Errorf("panel still draws a thunderstorm asthma row beside its alert card")
	}
}

// panelMeasureRows lists the measure labels a panel actually draws.
func panelMeasureRows(root *v1.Node) []string {
	var out []string
	for _, child := range root.Children {
		if child.Kind != v1.KindRow || len(child.Children) < 2 {
			continue
		}
		lead := child.Children[0]
		if lead.Kind != v1.KindRow || len(lead.Children) < 2 {
			continue
		}
		if label := lead.Children[1]; label.Kind == v1.KindText && label.Text != "" {
			out = append(out, label.Text)
		}
	}
	return out
}

func TestBarLabelFitsItsWidth(t *testing.T) {
	t.Parallel()

	// A long reading, so the budget is what limits it rather than the data.
	report := sampleReport()
	report.Snapshot.Measures[0].Metric = &Metric{Subcat: "Grass", Value: 1234.5, Units: "grains/m³"}
	for _, width := range []int{240, 200, 160, 120, 119, 96, 64, 40, 32} {
		bar := Bar(report, width)
		lintView(t, bar, v1.ViewBar, width, shelllint.BarHeight)
		control := bar.Children[0]
		var label string
		for _, child := range control.Children {
			if child.Kind == v1.KindText {
				label = child.Text
			}
		}
		if label == "" {
			continue
		}
		if budget := barLabelBytes(width); len(label) > budget {
			t.Fatalf("width %d: label %q is %d bytes, budget %d", width, label, len(label), budget)
		}
	}
}

func TestTruncateBytesNeverSplitsARune(t *testing.T) {
	t.Parallel()

	// "µg/m³" is multi-byte; cutting inside one would refuse the text node.
	for budget := 0; budget < len("Grass 24 µg/m³"); budget++ {
		got := truncateBytes("Grass 24 µg/m³", budget)
		if len(got) > budget {
			t.Fatalf("budget %d returned %d bytes", budget, len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("budget %d split a rune: %q", budget, got)
		}
	}
}

func TestDialReplacesTheRowItDraws(t *testing.T) {
	// sampleReport's worst measure is grass, which is dialable. Drawing the
	// dial and then a row for the same measure showed "UV index" twice: once as
	// the dial caption and again as a card directly under the arc.
	report := sampleReport()
	dialled := DialLabel(report, 24)
	if dialled == "" {
		t.Fatal("fixture has no dial")
	}
	rows := panelMeasureRows(PanelFor(report, 24))
	if len(rows) == 0 {
		t.Fatal("panel draws no measure rows")
	}
	for _, label := range rows {
		if label == dialled {
			t.Fatalf("panel draws %q both as the dial and as a row", dialled)
		}
	}
}

func TestWrapTextKeepsTheAttributionReadable(t *testing.T) {
	// Attribution is a licence condition. A single text node is ellipsised past
	// the panel width, so a long dual-source attribution lost its tail.
	const long = "Air quality data: EPA Victoria (CC BY 4.0) · Air quality and UV: CAMS ENSEMBLE via Open-Meteo"
	lines := wrapText(long, attributionBytes)
	if len(lines) < 2 {
		t.Fatalf("wrapText(%q) = %v, want it split", long, lines)
	}
	for _, line := range lines {
		if len(line) > attributionBytes {
			t.Fatalf("line %q is %d bytes, over the %d budget", line, len(line), attributionBytes)
		}
	}
	if joined := strings.Join(lines, " "); joined != long {
		t.Fatalf("wrapping lost text:\n got %q\nwant %q", joined, long)
	}
}

func TestWrapTextKeepsAShortAttributionOnOneLine(t *testing.T) {
	const short = "Air quality data: EPA Victoria (CC BY 4.0)"
	if lines := wrapText(short, attributionBytes); len(lines) != 1 || lines[0] != short {
		t.Fatalf("wrapText(%q) = %v, want one unchanged line", short, lines)
	}
}

func TestPanelGroupsAndShowsEveryAttributionSource(t *testing.T) {
	report := sampleReport()
	report.Snapshot.Attribution = "Air quality data: EPA Victoria (CC BY 4.0) · Air quality and UV: CAMS ENSEMBLE via Open-Meteo"
	root := PanelFor(report, 24)
	credits := root.Children[len(root.Children)-1]
	if credits.Kind != v1.KindColumn || len(credits.Children) != 2 {
		t.Fatalf("source credits = %+v, want two grouped sources", credits)
	}
	if got := credits.Children[0].Children[0].Text; got != "Air quality data: EPA Victoria (CC BY 4.0)" {
		t.Errorf("first source = %q", got)
	}
	if got := credits.Children[1].Children[0].Text; got != "Air quality and UV: CAMS ENSEMBLE via Open-Meteo" {
		t.Errorf("second source = %q", got)
	}
	joined := nodeText(credits)
	for _, want := range []string{"EPA Victoria", "CAMS ENSEMBLE", "Open-Meteo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("panel attribution is missing %q: %q", want, joined)
		}
	}
}

func nodeText(root *v1.Node) string {
	var texts []string
	var walk func(*v1.Node)
	walk = func(node *v1.Node) {
		if node == nil {
			return
		}
		if node.Text != "" {
			texts = append(texts, node.Text)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return strings.Join(texts, " ")
}

func nodeWithID(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := nodeWithID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func nodeWithFill(root *v1.Node, fill string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Fill == fill {
		return root
	}
	for _, child := range root.Children {
		if found := nodeWithFill(child, fill); found != nil {
			return found
		}
	}
	return nil
}
