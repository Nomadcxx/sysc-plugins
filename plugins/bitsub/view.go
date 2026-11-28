package bitsub

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Panel size, as manifest.json declares it: Moonbit's width, tall enough
// for the header, six or more rows, the stat cards and the footer.
const (
	PanelWidth  = 758
	PanelHeight = 600

	panelInset = 12
	panelGap   = 10
	cardPad    = 12
	controlH   = 32
	rowPad     = 6
	listHeight = 360
	statH      = 100
	offlineH   = 40
	// pendingDays: undecided and cancelling items this close show on the bar.
	pendingDays = 7
	urgentDays  = 2
	barIcon     = "schedule"
	// barChrome is the bar button's icon, dot and padding in measured pixels.
	barChrome = 48
)

// State is everything the views read.
type State struct {
	Snap         *subs.Snapshot
	Now          time.Time
	Online       bool
	LastOK       time.Time
	AuthFailed   bool
	Confirming   string // subscription id awaiting "Have you cancelled it?"
	HostMinor    int
	ClientConfig string
	ConfigErr    string // client.json could not be loaded
}

func (s *State) items() []subs.Item {
	if s.Snap == nil {
		return nil
	}
	return s.Snap.Items
}

func (s *State) today() subs.Date { return subs.Today(s.Now, time.Local) }

func (s *State) server() string {
	if s.Snap != nil && s.Snap.ServedBy != "" {
		return s.Snap.ServedBy
	}
	return "bitsubd"
}

// urgent is the item most in need of a decision within pendingDays, if any.
func (s *State) urgent() (subs.Item, int, bool) {
	today := s.today()
	var best subs.Item
	bestDays, found := 0, false
	for _, it := range s.items() {
		due := it.Target
		if it.Intent == subs.Cancel {
			due = it.CancelBy
		}
		days := max(subs.DaysBetween(today, due), 0)
		pending := it.ProbablyCharged || (it.Intent != subs.Keep && days <= pendingDays)
		if pending && (!found || days < bestDays) {
			best, bestDays, found = it, days, true
		}
	}
	return best, bestDays, found
}

// Bar is a row holding one button whose children never change in number:
// the icon, the alert dot and the label (icon and dot when compact).
func Bar(s *State, width int) *v1.Node {
	compact := barwidth.Compact(width)
	iconTone, dot, dotTone, label := v1.ToneNormal, "", v1.ToneNormal, "—"
	if s.Snap != nil {
		label = wholeAUD(s.Snap.Totals.MonthlyAUD, s.Snap.Totals.Approx) + "/mo"
	}
	if it, days, ok := s.urgent(); ok {
		dot, dotTone = "●", v1.ToneAccent
		if days <= urgentDays || it.ProbablyCharged {
			dotTone = v1.ToneError
		}
		label = urgentLabel(it.Name, daysShort(days), labelBudget(width))
	} else if s.Snap != nil && len(label) > labelBudget(width) {
		label = strings.TrimSuffix(label, "/mo")
	}
	if !s.Online {
		iconTone = v1.ToneSubtle
	}
	btn := &v1.Node{
		Kind: v1.KindButton, ID: "bar", Name: "bitsub subscriptions", Role: "button",
		Events:   []v1.EventKind{v1.EventActivate, v1.EventPointer},
		Children: []*v1.Node{{Kind: v1.KindIcon, Icon: barIcon, Tone: iconTone}, {Kind: v1.KindText, Text: dot, Tone: dotTone}},
	}
	if compact {
		btn.Height, btn.Padding = 32, 2
		btn.Children[0].IconSize = min(20, max(1, width-16))
	} else {
		btn.Children = append(btn.Children, &v1.Node{Kind: v1.KindText, Text: label, Tone: v1.ToneNormal, Tabular: true})
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{btn}}
}

// labelBudget is how many label bytes fit beside the icon and dot; the
// host measures text at eight pixels a byte.
func labelBudget(width int) int { return (width - barChrome) / 8 }

// urgentLabel is "Name · 3d", clipping the name to the budget, or just the
// days when there is no room for a name.
func urgentLabel(name, days string, budget int) string {
	room := budget - len(" · ") - len(days)
	if room < len("Abc…") {
		return days
	}
	return clipBytes(name, room) + " · " + days
}

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := 0
	for i := range s {
		if i > n-len("…") {
			break
		}
		cut = i
	}
	return strings.TrimRight(s[:cut], " ") + "…"
}

// Tooltip: totals, the most urgent item and connection state.
func Tooltip(s *State) *v1.Node {
	rows := []*v1.Node{{Kind: v1.KindText, Text: "bitsub", Bold: true}}
	if s.Snap != nil {
		t := s.Snap.Totals
		rows = append(rows, text(fmt.Sprintf("%s a month · %s", aud(t.MonthlyAUD, t.Approx), plural(t.Next30Count, "renewal", "renewals")+" in 30 days")))
	}
	if it, days, ok := s.urgent(); ok {
		rows = append(rows, text(clip(it.Name, 24)+" · "+daysLong(days)))
	} else if s.Snap != nil {
		rows = append(rows, subtle("Nothing to decide this week"))
	}
	if line := offlineLine(s); line != "" {
		rows = append(rows, errText(clip(line, 34)))
	}
	rows = append(rows, subtle("Click to open"))
	return &v1.Node{Kind: v1.KindColumn, Padding: 8, Gap: 4, Children: rows}
}

// Panel is design "C revised": header art, one list with Keep/Cancel, the
// stat cards and a footer, with an offline card above the list when the
// server is unreachable.
func Panel(s *State) *v1.Node {
	header := &v1.Node{Kind: v1.KindColumn}
	for _, row := range PlainRows() {
		header.Children = append(header.Children, &v1.Node{Kind: v1.KindText, Text: row, Size: "mono", Tone: v1.ToneAccent, Bold: true, CenterX: true})
	}
	children := []*v1.Node{header}
	listH := listHeight
	if line := offlineLine(s); line != "" {
		children = append(children, &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: cardPad, Height: offlineH,
			Children: []*v1.Node{errText(line)}})
		listH -= offlineH + panelGap
	}
	children = append(children, list(s, listH), stats(s), footer(s))
	return &v1.Node{Kind: v1.KindColumn, Padding: panelInset, Gap: panelGap, Children: children}
}

func offlineLine(s *State) string {
	switch {
	case s.ConfigErr != "":
		return "Client config: " + s.ConfigErr
	case s.AuthFailed:
		return "Token rejected: check " + s.ClientConfig + "."
	case s.Online:
		return ""
	case s.LastOK.IsZero():
		return "Offline: " + s.server() + " hasn't answered yet. Changes are paused."
	}
	return fmt.Sprintf("Offline: %s hasn't answered since %s (%s). Changes are paused.",
		s.server(), s.LastOK.In(time.Local).Format("15:04"), ago(s.Now.Sub(s.LastOK)))
}

func list(s *State, height int) *v1.Node {
	l := &v1.Node{Kind: v1.KindList, Key: "subscriptions", Height: height, Gap: 6, Events: []v1.EventKind{v1.EventScroll}}
	items := s.items()
	if len(items) == 0 {
		msg := "Nothing tracked yet. Ask an agent to add a subscription."
		if s.Snap == nil {
			msg = "Waiting for " + s.server() + "…"
		}
		l.Children = []*v1.Node{note(msg)}
		return l
	}
	today := s.today()
	for _, it := range items {
		l.Children = append(l.Children, row(s, it, today))
	}
	return l
}

func row(s *State, it subs.Item, today subs.Date) *v1.Node {
	line, tone := dateLine(it, today)
	main, orig := priceParts(it)
	// Always three children: the host lays out a two-child row differently,
	// which pushed AUD-only prices to the far edge.
	title := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: it.Name, Bold: true},
		{Kind: v1.KindText, Text: main, Tabular: true},
		subtle(orig),
	}}
	left := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{title, {Kind: v1.KindText, Text: line, Tone: tone}}}
	return &v1.Node{Kind: v1.KindRow, Key: "row:" + it.ID, PinEnd: true, Padding: rowPad, Fill: "surface", Shape: "medium",
		Children: []*v1.Node{left, controls(s, it)}}
}

func controls(s *State, it subs.Item) *v1.Node {
	if s.Confirming == it.ID {
		return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Have you cancelled it?"},
			button("confirm-yes:"+it.ID, "Yes", "Yes, I cancelled "+it.Name, "error", !s.Online),
			button("confirm-no:"+it.ID, "Not yet", "Not yet cancelled "+it.Name, "outline", !s.Online),
		}}
	}
	keepFill, cancelFill := "outline", "outline"
	switch it.Intent {
	case subs.Keep:
		keepFill = "accent"
	case subs.Cancel:
		cancelFill = "error"
	}
	return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		button("keep:"+it.ID, "Keep", "Keep "+it.Name, keepFill, !s.Online),
		button("cancel:"+it.ID, "Cancel", "Cancel "+it.Name, cancelFill, !s.Online),
	}}
}

// button is a row control. Offline it takes the dimmed soft fill and is
// disabled, because changes cannot reach the server.
func button(id, label, name, fill string, offline bool) *v1.Node {
	n := &v1.Node{Kind: v1.KindButton, ID: id, Name: name, Role: "button", Events: []v1.EventKind{v1.EventActivate},
		Fill: fill, Text: label, Height: controlH, Width: 28 + 9*utf8.RuneCountInString(label)}
	if offline {
		n.Fill, n.Disabled, n.Name = "soft", true, name+" (offline)"
	}
	return n
}

// dateLine is the row's second line and its tone: error within two days
// and accent within seven while the item (trial or renewal) is undecided.
func dateLine(it subs.Item, today subs.Date) (string, v1.Tone) {
	if it.ProbablyCharged {
		return "Charged — confirm whether you cancelled", v1.ToneError
	}
	days := subs.DaysBetween(today, it.Target)
	var line string
	if it.TargetKind == subs.TargetTrialEnd {
		line = "Trial ends " + it.Target.Short() + " · " + daysLong(days)
	} else {
		line = "Renews " + it.Target.Short()
	}
	if it.Intent == subs.Cancel {
		if it.CancelBy != it.Target {
			line += " · cancel by " + it.CancelBy.Short()
		} else {
			line += " · cancel before then"
		}
	}
	if it.Intent != subs.Undecided {
		return line, v1.ToneNormal
	}
	switch {
	case days <= urgentDays:
		return line, v1.ToneError
	case days <= pendingDays:
		return line, v1.ToneAccent
	}
	return line, v1.ToneNormal
}

// priceParts splits the price into the AUD figure with its cycle and the
// bracketed original, which the row draws in the subtle tone.
func priceParts(it subs.Item) (main, orig string) {
	suffix := subs.PriceSuffix(it.Cycle, it.Interval)
	if it.Price.Currency == subs.AUD || it.PriceAUD == 0 && it.Price.Minor != 0 {
		return subs.FormatPrice(it.PriceAUD, it.Price, it.PriceApprox, suffix), ""
	}
	return subs.FormatPrice(it.PriceAUD, subs.Money{Minor: it.PriceAUD, Currency: subs.AUD}, it.PriceApprox, suffix),
		"(" + it.Price.String() + ")"
}

func stats(s *State) *v1.Node {
	var t subs.Totals
	var series []subs.MonthTotal
	if s.Snap != nil {
		t, series = s.Snap.Totals, s.Snap.Series
	}
	month := statCard(200, "Per month", aud(t.MonthlyAUD, t.Approx), aud(t.YearlyAUD, t.Approx)+" a year")
	next := statCard(200, "Next 30 days", aud(t.Next30AUD, t.Approx), plural(t.Next30Count, "renewal", "renewals"))
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{month, next, graphCard(series)}}
}

func statCard(width int, title, figure, caption string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: cardPad, Gap: 4, Width: width, Height: statH,
		Children: []*v1.Node{subtle(title), {Kind: v1.KindText, Text: figure, Size: "title", Bold: true, Tabular: true}, subtle(caption)}}
}

func graphCard(series []subs.MonthTotal) *v1.Node {
	var peak int64
	for _, m := range series {
		peak = max(peak, m.AUDMinor)
	}
	g := &v1.Node{Kind: v1.KindGraph, Key: "charged", Height: 36}
	caption := "No charges yet"
	if peak > 0 && len(series) >= v1.MinGraphSamples {
		for _, m := range series {
			g.Values = append(g.Values, float64(m.AUDMinor)/float64(peak))
		}
		caption = "Peak " + wholeAUD(peak, false)
	} else {
		g.Absent = true
	}
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: cardPad, Gap: 4, Width: 318, Height: statH,
		Children: []*v1.Node{subtle("Charged per month · 12 months"), g, subtle(caption)}}
}

// footer: calendar sync age, next digest and the server, each part
// replaced by its error in the error tone.
func footer(s *State) *v1.Node {
	var parts []*v1.Node
	add := func(n *v1.Node) {
		if len(parts) > 0 {
			parts = append(parts, subtle("·"))
		}
		parts = append(parts, n)
	}
	if s.Snap == nil {
		add(subtle("Not connected"))
		return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: parts}
	}
	ch := s.Snap.Channels
	switch {
	case ch.CalendarError == "Google not configured":
		add(subtle("Google not configured"))
	case ch.CalendarError != "":
		add(errText(clip("Calendar out of date: "+ch.CalendarError, 48)))
	case ch.CalendarSyncedAt != nil:
		add(subtle("Calendar synced " + ago(s.Now.Sub(*ch.CalendarSyncedAt))))
	default:
		add(subtle("Calendar not synced yet"))
	}
	switch {
	case ch.EmailError != "" && ch.EmailError != "Google not configured":
		add(errText(clip("Email failing: "+ch.EmailError, 40)))
	case !ch.NextDigest.IsZero() && ch.EmailError == "":
		add(subtle("next email digest " + subs.Today(ch.NextDigest, time.Local).Short()))
	}
	switch {
	case ch.FXError != "" && ch.FXStale:
		add(errText("Exchange rates stale"))
	case ch.FXStale && hasForeign(s.items()):
		add(errText("Exchange rates stale"))
	}
	add(subtle("served by " + s.server()))
	return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: parts}
}

func hasForeign(items []subs.Item) bool {
	for _, it := range items {
		if it.Price.Currency != subs.AUD {
			return true
		}
	}
	return false
}

func aud(minor int64, approx bool) string { return subs.FormatTotal(minor, approx) }

// wholeAUD rounds to dollars for the bar and the graph peak, e.g. "A$342".
func wholeAUD(minor int64, approx bool) string {
	if approx && minor == 0 {
		return "—"
	}
	s := subs.Money{Minor: (minor + 50) / 100 * 100, Currency: subs.AUD}.String()
	s = strings.TrimSuffix(s, ".00")
	if approx {
		s = "≈" + s
	}
	return s
}

func daysShort(days int) string {
	if days == 0 {
		return "today"
	}
	return fmt.Sprintf("%dd", days)
}

func daysLong(days int) string {
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "tomorrow"
	}
	return fmt.Sprintf("in %d days", days)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func note(t string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneSubtle, CenterX: true}
}
func text(t string) *v1.Node    { return &v1.Node{Kind: v1.KindText, Text: t} }
func subtle(t string) *v1.Node  { return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneSubtle} }
func errText(t string) *v1.Node { return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneError} }
