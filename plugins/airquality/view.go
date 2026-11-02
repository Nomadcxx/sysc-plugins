package airquality

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The panel geometry. The host adds no inner padding, so the root column
// provides it, and the height budget below subtracts it before handing the
// remainder to the scrolling list.
const (
	PanelWidth  = 460
	PanelHeight = 600

	panelPad         = 12
	panelScrollInset = 4
	panelCardPadding = 8
	headerGap        = 8
	titleLine        = 24 // the title token renders taller than body text
	textLine         = 18
	sectionGap       = 12

	tooltipDefaultWidth  = 280
	tooltipDefaultHeight = 200
	tooltipPadding       = 6
	tooltipGap           = 1
	tooltipSafetySpace   = 20
	cardTextBytes        = (PanelWidth - 2*panelPad - 2*panelCardPadding) / 8
	errorDetailBytes     = (PanelWidth - 2*panelPad - 2*panelCardPadding - 24 - 8) / 8
)

// truncateBytes cuts a label to a byte budget without splitting a rune, since
// a half UTF-8 sequence would refuse the text node outright.
func truncateBytes(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

const barTextBytes = 34

// measureRowGap separates the glyph from its label, and the label group from
// the pinned reading.
const measureRowGap = 8

// measureRowHeight is the card height. It is 38 rather than the 44 a touch
// target wants because these rows are read, not tapped, and five of them plus
// a dial, an alert, a forecast and an attribution have to fit 600px.
const measureRowHeight = 38

// Icons come from the shell's catalogue, which has weather glyphs but no
// pollen or leaf symbol, so grass pollen borrows the clearest available
// weather metaphor rather than showing nothing.
const (
	iconPollen  = "wind"
	iconAir     = "fog"
	iconStorm   = "thunderstorm"
	iconSun     = "clear-day"
	iconSmoke   = "cloud"
	iconAlert   = "notifications-active"
	iconRefresh = "refresh"
)

var activate = []v1.EventKind{v1.EventActivate}

// MeasureIcon picks a glyph for a measure, used where the measure's identity
// matters more than its severity.
func MeasureIcon(measure string) string {
	switch measure {
	case MeasureGrass, MeasureAllergens:
		return iconPollen
	case MeasureThunderstorm:
		return iconStorm
	case MeasureWoodsmoke:
		return iconSmoke
	case MeasureUV:
		return iconSun
	default:
		return iconAir
	}
}

// The bar answers one question from across a room: does the air need
// thinking about today. It shows dials, not words: a dial for the air and,
// where the location has pollen data, one for pollen. Each needle sits on a
// shared good, moderate and high scale, so the two read alike although their
// sources measure in different units. Readings and names live in the tooltip
// and the panel.

// Bar choices, the values of the bar widget's "show" setting.
const (
	BarBoth   = "both"
	BarAir    = "air"
	BarPollen = "pollen"
)

// BarChoice resolves a stored "show" value. Earlier releases offered single
// measures; each maps to the dial that now carries it.
func BarChoice(v string) string {
	switch v {
	case BarAir:
		return BarAir
	case BarPollen, "grass", "storm":
		return BarPollen
	}
	return BarBoth
}

var (
	airDialMeasures    = []string{MeasureAirQuality, MeasureWoodsmoke}
	pollenDialMeasures = []string{MeasureGrass, MeasureAllergens, MeasureThunderstorm}
)

const (
	// barPairDialWidth fits two dials and their captions in a standard bar.
	barPairDialWidth = 30
	// barSoloDialWidth is the lone dial, a size up because it has the pill to
	// itself.
	barSoloDialWidth = 40
	// barDialHeight is the dial's box. The shell centres the arc and hub in
	// it, so it sits on the bar's text line.
	barDialHeight = 24
	// barPairMinWidth is the narrowest slot that fits both dials and their
	// captions; a side bar below it gets the one dial that matters most.
	barPairMinWidth = 120
	// barControlInset is the button's own padding, which a lone dial in a
	// narrow side bar has to leave room for.
	barControlInset = 16
)

// soloDialWidth is the lone dial's width: its full size unless the slot is
// narrower than that plus the button around it.
func soloDialWidth(width int) int {
	if width <= 0 {
		return barSoloDialWidth
	}
	return max(min(barSoloDialWidth, width-barControlInset), 1)
}

// Bar renders the bar widget with both dials.
func Bar(report Report, width int) *v1.Node {
	return BarFor(report, BarBoth, width)
}

// BarFor renders the bar widget for a "show" choice.
//
// The root is a row because the host refuses any other kind of bar root, and
// the row holds exactly one control so the whole slot is one click target.
// A width of zero is the protocol's legacy unspecified size, treated as wide.
func BarFor(report Report, show string, width int) *v1.Node {
	air, haveAir := worstOf(report, airDialMeasures)
	pollen, havePollen := worstOf(report, pollenDialMeasures)

	var dials []*v1.Node
	switch {
	case show == BarBoth && haveAir && havePollen && (width == 0 || width >= barPairMinWidth):
		dials = []*v1.Node{
			barCaption("AIR"), barDial("air", air, true, barPairDialWidth),
			barCaption("POL"), barDial("pollen", pollen, true, barPairDialWidth),
		}
	case show == BarBoth && haveAir && havePollen:
		// Too narrow for two: the worse of the two keeps the slot.
		m := air
		if concern(pollen) > concern(air) {
			m = pollen
		}
		dials = []*v1.Node{barDial("worst", m, true, soloDialWidth(width))}
	case show == BarAir:
		dials = []*v1.Node{barDial("air", air, haveAir, soloDialWidth(width))}
	case havePollen && show != BarAir && (show == BarPollen || !haveAir):
		dials = []*v1.Node{barDial("pollen", pollen, true, soloDialWidth(width))}
	default:
		// No pollen here, or air only: the air dial alone. With no reading
		// at all it is an empty dial rather than a vanished widget.
		dials = []*v1.Node{barDial("air", air, haveAir, soloDialWidth(width))}
	}

	control := &v1.Node{
		Kind: v1.KindButton, ID: "open", Key: "bar", Name: barName(report, air, haveAir, pollen, havePollen),
		Role: "button", Events: activate, Children: dials,
	}
	// A thunderstorm asthma warning is the one reading that means act now,
	// so it tints the whole pill, not just a needle.
	if storm, ok := report.Snapshot.Measure(MeasureThunderstorm); ok && reportHasData(report) &&
		storm.Level.Known() && storm.Level.Severity >= SeverityHigh {
		control.Fill = "error-container"
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{control}}
}

// worstOf is the worst known reading among the named measures. A tie goes to
// the reading further along its scale.
func worstOf(report Report, names []string) (Measure, bool) {
	if !reportHasData(report) {
		return Measure{}, false
	}
	best, found := Measure{}, false
	for _, name := range names {
		m, ok := report.Snapshot.Measure(name)
		if !ok || !m.Level.Known() {
			continue
		}
		if !found || m.Level.Severity > best.Level.Severity ||
			(m.Level.Severity == best.Level.Severity && concern(m) > concern(best)) {
			best, found = m, true
		}
	}
	return best, found
}

// concernBands are the dial's zones: good, moderate and high, a third each,
// unlabelled because the dial is read by colour and angle.
func concernBands() []v1.GaugeBand {
	return []v1.GaugeBand{
		{Until: 1.0 / 3, Severity: v1.SeverityGood},
		{Until: 2.0 / 3, Severity: v1.SeverityModerate},
		{Until: 1, Severity: v1.SeverityHigh},
	}
}

// concern places a reading on the dial's shared scale. Its severity picks the
// third, and its position among its own scale's bands of that severity places
// it inside the third. A reading with no position sits mid-third.
func concern(m Measure) float64 {
	third := float64(min(max(m.Level.Severity, SeverityGood), SeverityHigh))
	within := 0.5
	if len(m.Bands) > 0 {
		at := dialFraction(m)
		lo, hi, found := 0.0, 0.0, false
		previous := 0.0
		for _, band := range m.Bands {
			if band.Severity == m.Level.Severity {
				if !found {
					lo, found = previous, true
				}
				hi = band.Until
			} else if found {
				break
			}
			previous = band.Until
		}
		if found && hi > lo && at >= lo && at <= hi {
			within = (at - lo) / (hi - lo)
		}
	}
	return min(max((third+within)/3, 0), 1)
}

// barDial is one dial. Without a reading it is drawn absent: the empty arc
// says "nothing yet" where a needle at zero would say "all clear".
func barDial(key string, m Measure, has bool, width int) *v1.Node {
	n := &v1.Node{
		Kind: v1.KindGauge, Form: v1.GaugeHalf, Bands: concernBands(),
		Width: width, Height: barDialHeight, Key: "dial-" + key, Role: "img",
	}
	if !has {
		n.Absent = true
		n.Name = "No reading"
		return n
	}
	n.Value = concern(m)
	n.Animate = true
	n.Name = measureText(m, Report{})
	return n
}

// barCaption names the dial that follows it. It is small: position does the
// work from a distance, and the caption is for the first look.
func barCaption(text string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: text, Size: "caption", Tone: v1.ToneSubtle}
}

// barName is the control's accessible name: what each dial shows, in words.
func barName(report Report, air Measure, haveAir bool, pollen Measure, havePollen bool) string {
	switch {
	case !reportHasData(report) && report.Stale && len(report.Errors) > 0:
		return "Air quality: offline"
	case !haveAir && !havePollen:
		return "Air quality: no reading"
	}
	var parts []string
	if haveAir {
		parts = append(parts, measureText(air, report))
	}
	if havePollen {
		parts = append(parts, measureText(pollen, report))
	}
	return strings.Join(parts, "; ")
}

// measureText is one measure as a bar label: its short name, its category and
// the numeric reading when the source supplies one.
//
// The figure comes before the category, so a narrow slot or a long category
// such as "Unhealthy for Sensitive Groups" loses words of the category rather
// than the number. The cut falls on a word boundary.
func measureText(m Measure, report Report) string {
	text := measureShort(m.Name)
	if m.Metric != nil && m.Metric.Value > 0 {
		if uvIsCurrent(m) {
			text += " now"
		}
		switch metricScaleLabel(m) {
		case "US AQI":
			text += " AQI"
		case "UPI":
			text += " UPI"
		}
		text += " " + formatValue(m.Metric.Value)
	}
	category := strings.ToLower(m.Level.Name)
	if category == "unhealthy for sensitive groups" {
		// The one category too long for a bar slot; cut at a word boundary
		// it would end on "for".
		category = "unhealthy (sensitive)"
	}
	text += " " + category
	if report.Stale {
		text += " ·"
	}
	return cutWords(text, barTextBytes)
}

// cutWords shortens text to budget bytes at a word boundary, falling back to
// a rune boundary for a single word longer than the budget.
func cutWords(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	cut := truncateBytes(text, budget)
	if i := strings.LastIndexByte(cut, ' '); i > 0 && text[len(cut)] != ' ' {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ·")
}

// Tooltip renders a short reading summary, freshness and the full source
// credit within the host's standard tooltip geometry.
func Tooltip(report Report) *v1.Node {
	return TooltipFor(report, tooltipDefaultWidth)
}

// TooltipFor fits its text to the width the host opened for this view.
func TooltipFor(report Report, width int) *v1.Node {
	if width <= 0 {
		width = tooltipDefaultWidth
	}
	lineBytes := max((width-2*tooltipPadding)/8, 1)
	title := "Air quality"
	if loc := report.Snapshot.Location.Suburb; loc != "" {
		title = loc
	}
	children := []*v1.Node{
		{Kind: v1.KindRow, Height: titleLine, Gap: 8, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: title, Size: "title", Bold: true},
			{Kind: v1.KindText, Text: ageText(report), Tone: v1.ToneSubtle, Size: "caption"},
		}},
	}
	if !reportHasData(report) {
		status := "No reading yet"
		if len(report.Errors) > 0 {
			status = "Sources unavailable"
		}
		children = append(children, &v1.Node{Kind: v1.KindText, Text: status, Tone: v1.ToneSubtle})
		return &v1.Node{Kind: v1.KindColumn, Padding: tooltipPadding, Gap: tooltipGap, Children: children}
	}
	var credits []string
	if attribution := strings.TrimSpace(report.Snapshot.Attribution); attribution != "" {
		attribution = tooltipAttribution(attribution, width)
		credits = wrapText(attribution, lineBytes)
	}
	remaining := tooltipReadingBudget(len(credits))
	appendSummary := func(text string) {
		parts := wrapText(text, lineBytes)
		if len(parts) > remaining {
			return
		}
		for _, part := range parts {
			children = append(children, &v1.Node{Kind: v1.KindText, Text: part})
		}
		remaining -= len(parts)
	}
	if report.Snapshot.Alert != nil {
		alert := report.Snapshot.Alert
		label := "Storm: " + alert.Level.Name
		if report.Stale {
			label = "Last storm: " + alert.Level.Name
		}
		appendSummary(label)
	}
	pollen := MeasureGrass
	if _, ok := report.Snapshot.Measure(pollen); !ok {
		pollen = MeasureAllergens
	}
	for _, name := range []string{pollen, MeasureAirQuality, MeasureUV, MeasureWoodsmoke, MeasureThunderstorm} {
		if remaining <= 0 {
			break
		}
		if name == MeasureThunderstorm && report.Snapshot.Alert != nil {
			continue
		}
		m, ok := report.Snapshot.Measure(name)
		if !ok || !m.Level.Known() {
			continue
		}
		lines := tooltipLines(m)
		if m.Name == MeasureAllergens && len(lines) > 1 {
			lines = []string{strings.Join(lines, " · ")}
		}
		for _, line := range lines {
			appendSummary(line)
		}
	}
	for _, line := range credits {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: line, Tone: v1.ToneSubtle, Size: "caption"})
	}
	return &v1.Node{Kind: v1.KindColumn, Padding: tooltipPadding, Gap: tooltipGap, Children: children}
}

func tooltipAttribution(attribution string, width int) string {
	if width >= tooltipDefaultWidth {
		return attribution
	}
	// Keep the required names and licence at narrow widths, while avoiding
	// repeating labels that wrap several times in the tooltip. The panel keeps
	// the full source attribution.
	return strings.NewReplacer(
		AirHealthAttribution, "AirHealth: University of Tasmania & University of Melbourne",
		OpenMeteoAttribution, "CAMS ENSEMBLE via Open-Meteo",
	).Replace(attribution)
}

func tooltipReadingBudget(creditLines int) int {
	available := tooltipDefaultHeight - 2*tooltipPadding - titleLine - (14+tooltipGap)*creditLines - tooltipSafetySpace
	return min(3, max(0, available/(textLine+tooltipGap)))
}

func tooltipLines(m Measure) []string {
	switch m.Name {
	case MeasureGrass:
		return []string{"Grass: " + m.Level.Name}
	case MeasureAllergens:
		lines := []string{"Allergens: " + m.Level.Name}
		if m.Metric != nil {
			name := strings.TrimSpace(m.Metric.Subcat)
			if name == "" {
				name = "Allergen"
			}
			detail := name + " " + formatValue(m.Metric.Value)
			if m.Metric.Units != "" {
				detail += " " + unitsLabel(m.Metric.Units)
			}
			lines = append(lines, detail)
		}
		return lines
	case MeasureAirQuality:
		text := "Air: " + m.Level.Name
		if scale := metricScaleLabel(m); scale != "" {
			text += " · " + scale
		}
		if m.Metric != nil {
			text += " " + formatValue(m.Metric.Value)
			if m.Metric.Units != "" {
				text += " " + unitsLabel(m.Metric.Units)
			}
		}
		return []string{text}
	case MeasureUV:
		text := "UV"
		if m.Metric != nil {
			text += " " + formatValue(m.Metric.Value)
			if uvIsCurrent(m) {
				text += " · now"
			} else {
				text += " · peak today"
			}
		}
		if m.Level.Name != "" {
			text += " · " + m.Level.Name
		}
		return []string{text}
	case MeasureThunderstorm:
		return []string{"Storm: " + m.Level.Name}
	default:
		return []string{Title(m.Name) + ": " + m.Level.Name}
	}
}

// Panel renders the full report: a header with the place and freshness, one
// card per measure, the forecast, and the attribution the sources require.
func Panel(report Report) *v1.Node {
	return PanelFor(report, dialProtocolMinor)
}

// PanelFor renders the panel for a host at the given protocol minor. A host
// below minor twenty-four cannot paint gauge bands or a half arc, so it gets the plain
// row list instead of a dial whose zones would silently vanish.
func PanelFor(report Report, protocolMinor int) *v1.Node {
	children := []*v1.Node{panelHeader(report)}
	if report.Snapshot.Alert != nil {
		children = append(children, alertCard(*report.Snapshot.Alert, report.Stale))
	}
	if len(report.Errors) > 0 {
		children = append(children, sourceErrorCard(report.Errors, reportHasData(report)))
	}
	if !reportHasData(report) {
		children = append(children, &v1.Node{
			Kind: v1.KindText, Text: "No reading yet", Tone: v1.ToneSubtle,
		})
		return panelRoot(children)
	}
	// Only the worst measure gets its constituents expanded. Expanding all of
	// them would push the forecast and the attribution off a 600px panel, and
	// the breakdown is only actionable for the thing the reader is reacting
	// to -- the full pollen species list is reference material, not a warning.
	worst, hasWorst := report.Snapshot.Worst()
	dialled := ""
	if dial := Dial(report, protocolMinor); dial != nil {
		if m, ok := dialMeasureForProtocol(report, protocolMinor); ok {
			dialled = m.Name
		}
		// The gauge centre holds the number, so the measure it belongs to
		// goes directly beneath it. Without this the panel shows a red arc
		// and "6 High" with nothing saying what is at six.
		// The label sits above the arc rather than under it: the arc is
		// horizontally centred in its box, so a caption below it reads as
		// misaligned whenever the box is wider than the text.
		children = append(children, &v1.Node{Kind: v1.KindColumn, Gap: 0, Children: []*v1.Node{
			{Kind: v1.KindText, Text: DialLabel(report, protocolMinor), Tone: v1.ToneSubtle, Size: "label"},
			dial,
		}})
		// The dialled measure has no row, so its constituents go under the
		// dial; otherwise a Very Poor reading never says which pollutant.
		if hasWorst && worst.Name == dialled {
			if strip := breakdownRow(worst); strip != nil {
				children = append(children, strip)
			}
		}
	}
	// The alert card restates one measure in full. Drawing both made the panel
	// carry "Thunderstorm Asthma: Moderate" twice, four pixels apart, and the
	// second copy is what pushed the forecast off the bottom.
	alerted := ""
	if report.Snapshot.Alert != nil {
		alerted = CanonicalMeasure(report.Snapshot.Alert.Title)
	}
	for _, m := range report.Snapshot.Measures {
		// The dial already names and quantifies its own measure, so a row for
		// the same measure repeats it directly under the arc.
		if m.Name == alerted || m.Name == dialled {
			continue
		}
		children = append(children, measureRow(m))
		if hasWorst && m.Name == worst.Name && len(m.Breakdown) > 0 {
			if strip := breakdownRow(m); strip != nil {
				children = append(children, strip)
			}
		}
	}
	if note := coverageNote(report); note != nil {
		children = append(children, note)
	}
	if section := forecastSection(report.Snapshot.Forecast); section != nil {
		children = append(children, section)
	}
	if report.Snapshot.Attribution != "" {
		children = append(children, attributionNode(report.Snapshot.Attribution))
	}
	return panelRoot(children)
}

// coverageNote explains an empty pollen section, such as a location that
// needs a Google Pollen API key; without it the gap reads as a fault.
func coverageNote(report Report) *v1.Node {
	if report.PollenNote == "" {
		return nil
	}
	lines := wrapText(report.PollenNote, attributionBytes)
	column := &v1.Node{Kind: v1.KindColumn, Children: make([]*v1.Node, 0, len(lines))}
	for _, line := range lines {
		column.Children = append(column.Children, &v1.Node{Kind: v1.KindText, Text: line, Tone: v1.ToneSubtle})
	}
	return column
}

// attributionBytes is the byte budget for one attribution line. Text measures
// eight bytes wide per byte of content regardless of size token, so the budget
// is the panel's content width in bytes, not in pixels.
const attributionBytes = (PanelWidth - 2*panelPad) / 8

// attributionNode wraps each provider credit independently, so a source's
// label stays with its name. The full text remains visible, with compact line
// spacing because legal credits belong below the forecast rather than beside
// the current readings.
func attributionNode(attribution string) *v1.Node {
	var sources []*v1.Node
	for _, source := range strings.Split(attribution, " · ") {
		var lines []*v1.Node
		for _, line := range wrapText(source, attributionBytes) {
			lines = append(lines, &v1.Node{
				Kind: v1.KindText, Text: line, Tone: v1.ToneSubtle, Size: "caption",
			})
		}
		if len(lines) > 0 {
			sources = append(sources, &v1.Node{Kind: v1.KindColumn, Children: lines})
		}
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: sources}
}

// wrapText breaks s on spaces into lines of at most budget bytes. A word longer
// than the budget is emitted on its own line rather than split, because a URL or
// a name cut mid-word is worse than one line that overruns slightly.
func wrapText(s string, budget int) []string {
	if budget <= 0 || len(s) <= budget {
		return []string{s}
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= budget:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func panelRoot(children []*v1.Node) *v1.Node {
	return &v1.Node{
		Kind: v1.KindList, Padding: panelPad, Gap: 6, Height: PanelHeight,
		Events: []v1.EventKind{v1.EventScroll}, Children: children,
	}
}

func panelHeader(report Report) *v1.Node {
	title := "Air quality & pollen"
	subtitle := ""
	if loc := report.Snapshot.Location.Suburb; loc != "" {
		subtitle = loc
		if state := report.Snapshot.Location.State; state != "" {
			subtitle += ", " + state
		}
	}
	if subtitle != "" {
		subtitle += " · "
	}
	subtitle += ageText(report)
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: title, Size: "title", Bold: true},
		{Kind: v1.KindText, Text: subtitle, Tone: v1.ToneSubtle},
	}}
}

// measureRow is one measure card: label on the left, category and reading
// pinned right. The trailing group has an explicit width so the card's row
// cannot overrun the panel.
// measureRow is one measure line: glyph, name, then the reading pinned right.
//
// The right-hand text carries both the category and the number when both
// exist. They answer different questions -- "High" is what to do, "4.9" is how
// far into High it is -- and showing only the number leaves a reader
// translating a bare figure against a scale they cannot see.
func measureRow(m Measure) *v1.Node {
	value := measureValueText(m)
	// PinEnd belongs to the row, not the trailing node: the layout pins the
	// row's last child and reserves its measured width. Setting it on the
	// child instead leaves the reading flowed right after the label, which is
	// what a panel showed before this was fixed. Two children only -- the
	// layout ignores PinEnd on any other count -- so the glyph and label share
	// the leading group.
	return &v1.Node{
		Kind: v1.KindRow, Fill: "card", Radius: 8, Padding: 8, Height: measureRowHeight, PinEnd: true,
		Gap: measureRowGap,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: measureRowGap, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: MeasureIcon(m.Name), Tone: iconToneFor(m.Level.Severity)},
				{Kind: v1.KindText, Text: Title(m.Name)},
			}},
			{Kind: v1.KindText, Text: value},
		},
	}
}

// measureValueText is the right-hand reading for a measure row.
func measureValueText(m Measure) string {
	if m.Metric == nil {
		if txt := m.Level.Name; txt != "" {
			return txt
		}
		return "—"
	}
	num := formatValue(m.Metric.Value)
	if scale := metricScaleLabel(m); scale != "" {
		num = scale + " " + num
	} else if m.Name == MeasureAllergens && m.Metric.Subcat != "" {
		// The allergen row reports its worst species; say which.
		num = m.Metric.Subcat + " " + num
	}
	if m.Metric.Units != "" {
		num += " " + unitsLabel(m.Metric.Units)
	}
	switch {
	case m.Name == MeasureUV:
		period := "Today's peak"
		if uvIsCurrent(m) {
			period = "Now"
		}
		if m.Level.Name == "" {
			return period + " " + num
		}
		return period + " " + num + " " + m.Level.Name
	case m.Level.Name == "":
		return num
	}
	return m.Level.Name + " · " + num
}

// metricScaleLabel names an index the reading is quoted on: the US AQI for
// model air quality, the Universal Pollen Index for Google pollen.
func metricScaleLabel(m Measure) string {
	if m.Metric == nil {
		return ""
	}
	switch subcat := strings.TrimSpace(m.Metric.Subcat); {
	case m.Name == MeasureAirQuality && strings.EqualFold(subcat, "US AQI"):
		return "US AQI"
	case strings.EqualFold(subcat, "UPI"):
		return "UPI"
	}
	return ""
}

func uvIsCurrent(m Measure) bool {
	return m.Name == MeasureUV && m.Metric != nil && strings.EqualFold(strings.TrimSpace(m.Metric.Subcat), "now")
}

func alertCard(a Alert, stale bool) *v1.Node {
	// Subtle text on a card is not a pair the theme validates, so a stale
	// alert says it is stale in its title and keeps its detail at full contrast.
	fill, tone, detailTone := "card", v1.ToneWarning, v1.ToneNormal
	title := a.Title + ": " + a.Level.Name
	if stale {
		title = "Last alert · " + a.Title + " · " + a.Level.Name
	} else if a.Level.Severity >= SeverityHigh {
		// The error container already carries the warning colour. Normal
		// foreground text keeps the copy legible against that surface.
		fill, tone, detailTone = "error-container", v1.ToneNormal, v1.ToneNormal
	}
	// The title sits in a column so the row does not pin it to the far edge,
	// away from its icon.
	children := []*v1.Node{{
		Kind: v1.KindRow, Gap: 8, Height: 24, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: iconAlert, Tone: tone},
			{Kind: v1.KindColumn, Children: []*v1.Node{{Kind: v1.KindText, Text: title, Tone: tone, Bold: true}}},
		},
	}}
	if explanation := strings.TrimSpace(a.Text); explanation != "" {
		for _, line := range wrapText(explanation, cardTextBytes) {
			children = append(children, &v1.Node{Kind: v1.KindText, Text: line, Tone: detailTone})
		}
	}
	return &v1.Node{Kind: v1.KindColumn, Fill: fill, Radius: 12, Padding: panelCardPadding, Gap: 4, Children: children}
}

// sourceErrorCard names the sources that failed. With no reading at all it is
// an error; when readings are still shown it is a warning on a plain card, so
// a routine outage of one source does not outrank the readings themselves.
func sourceErrorCard(problems []string, haveData bool) *v1.Node {
	heading, fill, tone := "Sources unavailable", "error-container", v1.ToneNormal
	if haveData {
		heading, fill, tone = "Some sources didn't update", "card", v1.ToneWarning
	}
	details := []*v1.Node{{Kind: v1.KindText, Text: heading, Bold: true}}
	for _, problem := range problems {
		for _, line := range wrapText(problem, errorDetailBytes) {
			details = append(details, &v1.Node{Kind: v1.KindText, Text: line})
		}
	}
	return &v1.Node{
		Kind: v1.KindColumn, Fill: fill, Radius: 12, Padding: panelCardPadding, Gap: 4,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: iconAlert, Tone: tone},
				{Kind: v1.KindColumn, Gap: 3, Children: details},
			}},
			refreshButton(),
		},
	}
}

func refreshButton() *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: "refresh", Icon: iconRefresh, Text: "Retry",
		Name: "Retry air quality and pollen sources", Role: "button",
		Height: 36, Padding: 14, Events: activate,
	}
}

// forecastSection is the next few days, one row each. Days whose measures are
// all absent are skipped rather than drawn as empty rows.
func forecastSection(days []Day) *v1.Node {
	if len(days) == 0 {
		return nil
	}
	children := []*v1.Node{
		{Kind: v1.KindText, Text: "Forecast", Size: "label", Bold: true},
	}
	shown := 0
	for _, day := range days {
		if shown >= 3 {
			break
		}
		row := forecastRow(day)
		if row == nil {
			continue
		}
		children = append(children, row)
		shown++
	}
	if shown == 0 {
		return nil
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: children}
}

func forecastRow(day Day) *v1.Node {
	var parts []string
	for _, name := range []string{MeasureGrass, MeasureAirQuality, MeasureThunderstorm} {
		m, ok := day.Measures[name]
		if !ok || !m.Level.Known() {
			continue
		}
		parts = append(parts, measureShort(name)+" "+strings.ToLower(m.Level.Name))
	}
	if len(parts) == 0 {
		return nil
	}
	label := day.Title
	if label == "" {
		label = day.Date
	}
	return &v1.Node{
		// The shell overlays its four-pixel scroll thumb on the content edge.
		// Inset this right-pinned value so a long forecast stays readable.
		Kind: v1.KindRow, Height: 22, Padding: panelScrollInset, Children: []*v1.Node{
			{Kind: v1.KindText, Text: label, Width: 120, Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: strings.Join(parts, " · ")},
		},
	}
}

// measureShort is the one-or-two word form used where space is tight.
func measureShort(name string) string {
	switch name {
	case MeasureGrass:
		return "Grass"
	case MeasureAllergens:
		return "Allergens"
	case MeasureAirQuality:
		return "Air"
	case MeasureUV:
		return "UV"
	case MeasureThunderstorm:
		return "Storm asthma"
	case MeasureWoodsmoke:
		return "Smoke"
	default:
		return name
	}
}

// formatValue keeps concentrations to one decimal, dropping a trailing .0 so
// a whole number reads as a whole number.
func formatValue(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// unitsLabel renders the source's unit string in the form a reader expects.
// The service sends ASCII "ug/m3"; the panel shows µg/m³.
func unitsLabel(units string) string {
	switch strings.ToLower(strings.TrimSpace(units)) {
	case "ug/m3", "µg/m³":
		return "µg/m³"
	case "ppb", "pphm":
		return strings.TrimSpace(units)
	case "":
		return ""
	default:
		return units
	}
}

func iconToneFor(s Severity) v1.Tone {
	switch s {
	case SeverityHigh:
		return v1.ToneError
	case SeverityModerate:
		// Warning, not accent: the dial paints Moderate amber, and the brand
		// accent says nothing about severity.
		return v1.ToneWarning
	default:
		return v1.ToneNormal
	}
}

// ageText says how old the reading is. It is the honesty signal for a stale
// report: the panel never implies a cached figure is live.
func ageText(report Report) string {
	if report.UpdatedAt.IsZero() {
		return "never updated"
	}
	age := time.Since(report.UpdatedAt)
	if age < 0 {
		age = 0
	}
	var text string
	switch {
	case age < time.Minute:
		text = "just now"
	case age < time.Hour:
		text = fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 48*time.Hour:
		text = fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		text = fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
	if report.Stale {
		return "stale · " + text
	}
	return text
}

// reportHasData reports whether there is anything worth drawing. A cache
// restored from disk always has measures, so this is really "has a refresh
// ever succeeded".
func reportHasData(report Report) bool {
	if len(report.Snapshot.Measures) == 0 {
		return false
	}
	for _, m := range report.Snapshot.Measures {
		if m.Level.Known() {
			return true
		}
	}
	return false
}

// breakdownRow is the constituent strip under the worst measure. It is one
// line of chips rather than a table: it sits under a 44px row and a reader
// scanning it wants "which pollutant", not a grid.
func breakdownRow(m Measure) *v1.Node {
	parts := m.Breakdown
	if len(parts) > 4 {
		parts = parts[:4]
	}
	children := make([]*v1.Node, 0, len(parts)*2)
	for _, part := range parts {
		// A time-of-day entry names when the reading was taken, not what it
		// measures. Because the UV headline is today's peak, its "now" entry
		// reads "now 0" after dark, which looks like a measurement of nothing.
		// The dial already carries the peak, so the clock entry is dropped
		// rather than drawn as a zero.
		if isTimeOfDayEntry(part.Name) {
			continue
		}
		if len(children) > 0 {
			children = append(children, &v1.Node{Kind: v1.KindText, Text: "·", Tone: v1.ToneSubtle})
		}
		label := part.Name
		if part.Metric != nil {
			label += " " + formatValue(part.Metric.Value)
		} else if part.Level.Known() {
			label += " " + part.Level.Name
		}
		children = append(children, &v1.Node{Kind: v1.KindText, Text: label, Size: "caption"})
	}
	if len(children) == 0 {
		return nil
	}
	return &v1.Node{Kind: v1.KindRow, Padding: 4, Gap: 6, Children: children}
}

// isTimeOfDayEntry reports whether a breakdown name describes when a reading
// was taken rather than what was measured.
func isTimeOfDayEntry(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "now", "current", "today":
		return true
	}
	return false
}

// dialWidth and dialHeight are the box the worst measure's dial occupies. The
// gauge is a semicircle plus its label, so it is wider than it is tall.
const (
	dialWidth  = 200
	dialHeight = 120
)

// Dial renders the worst measure as a half gauge with severity bands.
//
// It returns nil when no measure can honestly be drawn as a dial: either the
// report has no numeric axis at all, or the host is too old to paint one. The
// caller falls back to the row list, which is the same information in a worse
// shape rather than an error.
func Dial(report Report, protocolMinor int) *v1.Node {
	if protocolMinor < dialProtocolMinor {
		return nil
	}
	m, ok := dialMeasureForProtocol(report, protocolMinor)
	if !ok {
		return nil
	}
	return dialNode(m, protocolMinor)
}

// dialProtocolMinor is the protocol minor that introduced gauge bands and the
// half form. A host below it ignores both fields and would paint a full ring
// with an accent arc, which is not wrong so much as misleading: the zones are
// the entire point.
const dialProtocolMinor = 24

const dialBandLabelsProtocolMinor = 25

// dialMeasure picks the measure to put on the dial: the worst one that has a
// real numeric scale. A report whose worst measure is thunderstorm asthma still
// gets a dial, from the worst measure that can support one, because the asthma
// alert is already shown as its own card above.
func dialMeasure(report Report) (Measure, bool) {
	return dialMeasureForProtocol(report, dialBandLabelsProtocolMinor)
}

func dialMeasureForProtocol(report Report, protocolMinor int) (Measure, bool) {
	if worst, ok := report.Snapshot.Worst(); ok {
		if dialable(worst, protocolMinor) {
			return worst, true
		}
	}
	best, found := Measure{}, false
	for _, m := range report.Snapshot.Measures {
		if !dialable(m, protocolMinor) {
			continue
		}
		if !found || m.Level.Severity > best.Level.Severity {
			best, found = m, true
		}
	}
	return best, found
}

// dialable requires a numeric axis except for grass, whose published category
// can be shown at the midpoint of its named band once labels are supported.
func dialable(m Measure, protocolMinor int) bool {
	if !m.Level.Known() || len(m.Bands) == 0 {
		return false
	}
	if m.Metric != nil {
		return m.Max > 0
	}
	if protocolMinor < dialBandLabelsProtocolMinor || m.Name != MeasureGrass || m.Max != 0 {
		return false
	}
	_, ok := categoryBandMidpoint(m.Bands, m.Level.Name)
	return ok
}

// dialNode builds the gauge itself.
func dialNode(m Measure, protocolMinor int) *v1.Node {
	bands := make([]v1.GaugeBand, 0, len(m.Bands))
	for _, band := range m.Bands {
		out := v1.GaugeBand{
			Until:    band.Until,
			Severity: gaugeSeverity(band.Severity),
		}
		if protocolMinor >= dialBandLabelsProtocolMinor {
			out.Label = band.Label
		}
		bands = append(bands, out)
	}
	return &v1.Node{
		Kind:    v1.KindGauge,
		Form:    v1.GaugeHalf,
		Bands:   bands,
		Value:   dialFraction(m),
		Width:   dialWidth,
		Height:  dialHeight,
		Key:     "worst",
		Animate: true,
		// The name carries the reading: a screen reader announcing only
		// "Grass pollen" for a picture of a gauge says nothing.
		Name:    dialTooltip(m),
		Role:    "img",
		Tooltip: dialTooltip(m),
		// The centre label carries the number and the category, because a
		// colour ramp alone cannot be read: the audit rule is that a threshold
		// chart labels its zones and states the value in text.
		ValueText: dialLabel(m),
	}
}

func dialTooltip(m Measure) string {
	value := dialLabel(m)
	if m.Name == MeasureUV {
		period := "today's peak "
		if uvIsCurrent(m) {
			period = "now "
		}
		value = period + value
	}
	return Title(m.Name) + ": " + value
}

// DialLabel names the measure a dial is drawing, for the line under it. The
// gauge centre holds the reading, so the panel has to say which scale that
// reading belongs to; a bare "6 High" under a red arc is not answerable
// without it.
func DialLabel(report Report, protocolMinor int) string {
	if protocolMinor < dialProtocolMinor {
		return ""
	}
	m, ok := dialMeasureForProtocol(report, protocolMinor)
	if !ok {
		return ""
	}
	if m.Metric == nil {
		return Title(m.Name) + " · category position"
	}
	if metricScaleLabel(m) == "UPI" {
		return Title(m.Name) + " · Universal Pollen Index, 0–5"
	}
	if m.Name == MeasureUV {
		if uvIsCurrent(m) {
			return Title(m.Name) + " · now"
		}
		return Title(m.Name) + " · today's peak"
	}
	return Title(m.Name)
}

// dialFraction places the reading on its own scale.
func dialFraction(m Measure) float64 {
	if m.Metric == nil {
		position, _ := categoryBandMidpoint(m.Bands, m.Level.Name)
		return position
	}
	if m.Max <= 0 {
		return 0
	}
	return Place(m.Bands, m.Metric.Value)
}

func categoryBandMidpoint(bands []Band, name string) (float64, bool) {
	previous := 0.0
	for _, band := range bands {
		if strings.EqualFold(strings.TrimSpace(band.Label), strings.TrimSpace(name)) {
			return (previous + band.Until) / 2, true
		}
		previous = band.Until
	}
	return 0, false
}

// dialLabel is the text under the arc: the figure, then the category, because
// "19.9" without "Fair" is a number the reader has to memorise a scale for.
func dialLabel(m Measure) string {
	if m.Metric == nil {
		return m.Level.Name
	}
	num := formatValue(m.Metric.Value)
	if scale := metricScaleLabel(m); scale != "" {
		num += " " + scale
	}
	if m.Name != MeasureUV && m.Metric.Units != "" {
		num += " " + unitsLabel(m.Metric.Units)
	}
	if m.Level.Name == "" {
		return num
	}
	return num + " " + m.Level.Name
}

// gaugeSeverity maps the domain axis onto the wire's severity names.
func gaugeSeverity(s Severity) v1.Severity {
	switch s {
	case SeverityModerate:
		return v1.SeverityModerate
	case SeverityHigh:
		return v1.SeverityHigh
	}
	return v1.SeverityGood
}
