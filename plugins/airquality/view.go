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

// Bar geometry. A bar view is one row and gets BarHeight; text measures 16
// tall, which leaves room for the row's padding.
// barIconBytes is the horizontal space the leading glyph takes, in text
// advances. The host's bar row must fit its content box, and the glyph plus
// its gap is not free.
const barIconBytes = 3

// barMinLabelBytes is the shortest label worth showing. Anything under it is
// a fragment of a word.
const barMinLabelBytes = 8

// barLabelBytes is how many bytes of label fit beside the icon at a given bar
// width. Zero means no label: at that size the glyph is the whole widget. A
// width of zero means unconstrained, which only happens in tests and in the
// tooltip, where the full label is wanted.
func barLabelBytes(width int) int {
	if width <= 0 {
		return barTextBytes
	}
	budget := width/8 - barIconBytes
	// A couple of bytes is not a label, it is a fragment: below a useful
	// minimum the glyph alone reads better than "Gr".
	if budget < barMinLabelBytes {
		return 0
	}
	return min(budget, barTextBytes)
}

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
	iconPollen   = "wind"
	iconAir      = "fog"
	iconStorm    = "thunderstorm"
	iconSmoke    = "cloud"
	iconGood     = "clear-day"
	iconModerate = "partly-cloudy"
	iconHigh     = "thunderstorm"
	iconAlert    = "notifications-active"
	iconRefresh  = "refresh"
)

var activate = []v1.EventKind{v1.EventActivate}

// SeverityIcon picks a glyph for a severity. It is the only place severity
// becomes a picture, so the mapping stays consistent across bar and panel.
func SeverityIcon(s Severity) string {
	switch s {
	case SeverityHigh:
		return iconHigh
	case SeverityModerate:
		return iconModerate
	default:
		return iconGood
	}
}

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
	default:
		return iconAir
	}
}

// BarText is the bar widget's single line: the worst current measure, its
// category, and the numeric reading when one exists.
func BarText(report Report) string {
	if !reportHasData(report) {
		return "No reading"
	}
	m, ok := report.Snapshot.Worst()
	if !ok {
		return "No reading"
	}
	return measureText(m, report)
}

// Bar renders the horizontal bar: an icon for the worst measure and its
// category, or the icon alone when the slot is narrow.
//
// The root is a row because the host refuses any other kind of bar root, and
// the row holds exactly one control so the whole slot is one click target.
//
// width is the viewport the host opened, which is 240 for a horizontal bar and
// can be smaller in a side bar. A zero width is the protocol's legacy
// unspecified size and keeps the labelled form.
func Bar(report Report, width int) *v1.Node {
	return BarFor(report, "worst", width)
}

// BarFor is Bar with the measure the widget was configured to show. An
// unknown or unavailable choice falls back to the worst measure, because a bar
// that shows nothing is worse than one showing the thing that matters.
func BarFor(report Report, show string, width int) *v1.Node {
	measureIcon := iconAir
	severity := SeverityGood
	label := "No reading"
	chosen, has := chooseMeasure(report, show)
	if reportHasData(report) && has {
		measureIcon = MeasureIcon(chosen.Name)
		severity = chosen.Level.Severity
		label = measureText(chosen, report)
	} else if report.Stale && len(report.Errors) > 0 {
		label = "Offline"
	}

	// The icon carries the state; the label is the detail. The host measures
	// text at a fixed advance per byte, so the label has to be cut to the
	// space the icon leaves rather than to a fixed length: a 240px bar fits
	// more than a 120px one, and a label that overruns refuses the whole row.
	children := []*v1.Node{{
		Kind: v1.KindIcon, Icon: pick(severity, measureIcon), Name: label,
	}}
	if show != "icon" {
		if budget := barLabelBytes(width); budget > 0 {
			children = append(children, &v1.Node{
				Kind: v1.KindText, Text: truncateBytes(label, budget), Tabular: true,
			})
		}
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Key: "bar", Name: label, Role: "button",
		Tabular: true, Events: activate, Children: children,
	}}}
}

// chooseMeasure resolves a widget's measure choice against a report. "worst"
// is the report's own ranking; a named measure is used only when it is known,
// so an out-of-season thunderstorm reading never renders as a fake Low.
func chooseMeasure(report Report, show string) (Measure, bool) {
	if !reportHasData(report) {
		return Measure{}, false
	}
	var name string
	switch show {
	case "grass":
		name = MeasureGrass
	case "air":
		name = MeasureAirQuality
	case "storm":
		name = MeasureThunderstorm
	case "icon", "worst":
		return report.Snapshot.Worst()
	default:
		return report.Snapshot.Worst()
	}
	if m, ok := report.Snapshot.Measure(name); ok && m.Level.Known() {
		return m, true
	}
	return report.Snapshot.Worst()
}

// measureText is one measure as a bar label: its short name, its category and
// the numeric reading when the source supplies one.
func measureText(m Measure, report Report) string {
	text := measureShort(m.Name) + " " + strings.ToLower(m.Level.Name)
	if scale := metricScaleLabel(m); scale != "" {
		text = measureShort(m.Name) + " " + scale + " " + strings.ToLower(m.Level.Name)
	}
	if m.Metric != nil && m.Metric.Value > 0 {
		if m.Name == MeasureUV {
			// UV is quoted as a bare number nationally, so the figure leads and
			// the band follows rather than the other way round.
			text = measureShort(m.Name) + " "
			if uvIsCurrent(m) {
				text += "now "
			}
			text += formatValue(m.Metric.Value) + " " + strings.ToLower(m.Level.Name)
		} else {
			text += " " + formatValue(m.Metric.Value)
		}
	}
	if report.Stale {
		text += " ·"
	}
	if len(text) > barTextBytes {
		text = truncateBytes(text, barTextBytes)
	}
	return text
}

// pick prefers the severity glyph, but keeps the measure's own icon when the
// reading is calm: a clear-day symbol says more than "wind" when the grass
// count is low.
func pick(severity Severity, measureIcon string) string {
	if severity >= SeverityModerate {
		return SeverityIcon(severity)
	}
	return measureIcon
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
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "No reading", Tone: v1.ToneSubtle})
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
		EPAAttribution, "EPA Victoria (CC BY 4.0)",
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
		children = append(children, sourceErrorCard(report.Errors))
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
	if section := forecastSection(report.Snapshot.Forecast); section != nil {
		children = append(children, section)
	}
	if report.Snapshot.Attribution != "" {
		children = append(children, attributionNode(report.Snapshot.Attribution))
	}
	return panelRoot(children)
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

func metricScaleLabel(m Measure) string {
	if m.Name == MeasureAirQuality && m.Metric != nil && strings.EqualFold(strings.TrimSpace(m.Metric.Subcat), "US AQI") {
		return "US AQI"
	}
	return ""
}

func uvIsCurrent(m Measure) bool {
	return m.Name == MeasureUV && m.Metric != nil && strings.EqualFold(strings.TrimSpace(m.Metric.Subcat), "now")
}

func alertCard(a Alert, stale bool) *v1.Node {
	fill, tone, detailTone := "card", v1.ToneWarning, v1.ToneNormal
	title := a.Title + ": " + a.Level.Name
	if stale {
		title = "Last alert · " + a.Title + " · " + a.Level.Name
		detailTone = v1.ToneSubtle
	} else if a.Level.Severity >= SeverityHigh {
		// The error container already carries the warning colour. Normal
		// foreground text keeps the copy legible against that surface.
		fill, tone, detailTone = "error-container", v1.ToneNormal, v1.ToneNormal
	}
	children := []*v1.Node{{
		Kind: v1.KindRow, Gap: 8, Height: 24, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: iconAlert, Tone: tone},
			{Kind: v1.KindText, Text: title, Tone: tone, Bold: true},
		},
	}}
	if explanation := strings.TrimSpace(a.Text); explanation != "" {
		for _, line := range wrapText(explanation, cardTextBytes) {
			children = append(children, &v1.Node{Kind: v1.KindText, Text: line, Tone: detailTone})
		}
	}
	return &v1.Node{Kind: v1.KindColumn, Fill: fill, Radius: 12, Padding: panelCardPadding, Gap: 4, Children: children}
}

func sourceErrorCard(problems []string) *v1.Node {
	details := []*v1.Node{{Kind: v1.KindText, Text: "Source update failed", Bold: true}}
	for _, problem := range problems {
		for _, line := range wrapText(problem, errorDetailBytes) {
			details = append(details, &v1.Node{Kind: v1.KindText, Text: line})
		}
	}
	return errorCard(
		&v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: iconAlert},
			{Kind: v1.KindColumn, Gap: 3, Children: details},
		}},
		refreshButton(),
	)
}

func errorCard(children ...*v1.Node) *v1.Node {
	return &v1.Node{
		Kind: v1.KindColumn, Fill: "error-container", Radius: 12,
		Padding: panelCardPadding, Gap: 4, Children: children,
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
		return v1.ToneAccent
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
	default:
		text = fmt.Sprintf("%dh ago", int(age.Hours()))
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
		Name:    Title(m.Name),
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
