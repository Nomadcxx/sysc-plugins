package herdr

import (
	"fmt"
	"sort"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Node-budget caps. The service bounds the raw model; the renderer bounds the
// tree independently so a pathological snapshot can never blow MaxNodes.
const (
	maxSessionsRendered = 20
	maxPanesPerSession  = 30
	cardNodeBudget      = 1000
	peekByteCap         = 2000
	panelIconSize       = 16
)

// Peek is one expanded output preview, keyed in PanelState by pane ID.
type Peek struct {
	PaneID string
	Text   string
	Lines  int
}

// PanelState is everything the panel needs to draw: the model, the settings,
// per-pane peek buffers, and the transient input/confirm/error state.
type PanelState struct {
	Model         *Model
	Settings      Settings
	Peeks         map[string]Peek
	ConfirmDelete string
	NewName       string
	ActionError   string
	SettingsEntry bool
}

// PanelTree builds the session panel.
func PanelTree(st PanelState, width, height int) *v1.Node {
	m := st.Model
	if m == nil {
		m = &Model{}
	}
	root := &v1.Node{Kind: v1.KindColumn, Padding: 8, Gap: 8}
	b := &nodeBudget{used: 1, max: v1.MaxNodes}

	b.add(root, panelHeader(m, st, width))
	b.add(root, &v1.Node{Kind: v1.KindSeparator})
	b.add(root, messageCards(m)...)

	sessions := sortedSessions(m)
	rendered := 0
	for _, s := range sessions {
		if rendered >= maxSessionsRendered {
			break
		}
		card := sessionCard(s, st, width)
		if b.used+countNodes(card) > cardNodeBudget {
			break
		}
		b.add(root, card)
		rendered++
	}
	if omitted := len(sessions) - rendered; omitted > 0 {
		b.add(root, moreText(omitted, "sessions"))
	}
	b.add(root, newSessionRow(st))
	if st.ActionError != "" {
		b.add(root, actionErrorCard(st.ActionError))
	}
	return root
}

// SettingsPanelTree is the plugin-owned card head above the shell's settings
// form; the host appends the validated form for this manifest entry.
func SettingsPanelTree(title string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{{
		Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: 12, Gap: 8,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: 8, Height: 36, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: barIconName, IconSize: panelIconSize},
				{Kind: v1.KindColumn, Width: 180, Children: []*v1.Node{
					{Kind: v1.KindText, Text: title, Bold: true, Size: "title"},
				}},
				{Kind: v1.KindButton, ID: "close", Icon: "close", Width: 32, Height: 32,
					Name: "Close settings", Role: "button", Tooltip: "Close settings",
					Events: []v1.EventKind{v1.EventActivate}},
			}},
			{Kind: v1.KindText, Text: "Settings save automatically.", Tone: v1.ToneSubtle, Size: "caption"},
		},
	}}}
}

// panelHeader is the fixed title bar: identity on the left, controls right.
func panelHeader(m *Model, st PanelState, width int) *v1.Node {
	// The title column shrinks before the controls do; the row must fit even
	// in a narrow panel.
	buttons := 32 + 32
	children := 4
	if st.SettingsEntry {
		buttons += 32
		children++
	}
	infoWidth := width - 16 - panelIconSize - buttons - 6*(children-1)
	if infoWidth > 232 {
		infoWidth = 232
	}
	if infoWidth < 80 {
		infoWidth = 80
	}

	row := &v1.Node{Kind: v1.KindRow, Gap: 6, Height: 32, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: barIconName, IconSize: panelIconSize},
		{Kind: v1.KindColumn, Width: infoWidth, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Herdr", Bold: true, Size: "title"},
			{Kind: v1.KindText, Text: summary(m), Size: "caption", Tone: v1.ToneSubtle},
		}},
		{Kind: v1.KindButton, ID: "refresh", Icon: "refresh", Width: 32, Height: 32,
			Name: "Refresh", Role: "button", Tooltip: "Refresh sessions",
			Events: []v1.EventKind{v1.EventActivate}},
		{Kind: v1.KindButton, ID: "close", Icon: "close", Width: 32, Height: 32,
			Name: "Close panel", Role: "button", Events: []v1.EventKind{v1.EventActivate}},
	}}
	if st.SettingsEntry {
		row.Children = append(row.Children, &v1.Node{
			Kind: v1.KindButton, ID: "settings", Icon: "desktop_windows", Width: 32, Height: 32,
			Name: "Herdr settings", Role: "button", Tooltip: "Session and notification settings",
			Events: []v1.EventKind{v1.EventActivate}})
	}
	return row
}

func summary(m *Model) string {
	n, agents := 0, 0
	for i := range m.Sessions {
		n++
		agents += m.Sessions[i].Counts.Agents
	}
	return fmt.Sprintf("%d sessions · %d agents", n, agents)
}

// messageCards are the distinct empty/missing/stale states above the list.
func messageCards(m *Model) []*v1.Node {
	var out []*v1.Node
	if m.HerdrMissing {
		out = append(out, messageCard("herdr not found on PATH", v1.ToneError, "error-container", ""))
	} else if len(m.Sessions) == 0 {
		out = append(out, &v1.Node{Kind: v1.KindColumn, Fill: "container", Shape: "small", Padding: 8, Gap: 4,
			Children: []*v1.Node{
				{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
					{Kind: v1.KindIcon, Icon: "ghost", IconSize: panelIconSize, Tone: v1.ToneSubtle},
					{Kind: v1.KindText, Text: "No herdr sessions", Bold: true},
				}},
				{Kind: v1.KindText, Text: "Start one with herdr or from the field below", Size: "caption", Tone: v1.ToneSubtle},
			}})
	}
	if m.DiscoveryErr != "" {
		out = append(out, messageCard("herdr unavailable — showing last known state", v1.ToneSubtle, "", "caption"))
	}
	return out
}

func messageCard(text string, tone v1.Tone, fill, size string) *v1.Node {
	n := &v1.Node{Kind: v1.KindColumn, Padding: 8, Fill: fill, Children: []*v1.Node{
		{Kind: v1.KindText, Text: text, Tone: tone, Bold: true, Size: size},
	}}
	return n
}

func actionErrorCard(msg string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "error-container", Padding: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: msg, Tone: v1.ToneError},
	}}
}

// sessionCard renders one session: a running card lists its workspaces and
// panes; a stopped card offers attach and two-step delete.
func sessionCard(s SessionRow, st PanelState, width int) *v1.Node {
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: 8, Gap: 4}
	if s.Running {
		card.Children = append(card.Children, runningHeader(s))
		if c := countsCaption(s.Counts); c != "" {
			card.Children = append(card.Children, &v1.Node{Kind: v1.KindText, Text: c, Size: "caption", Tone: v1.ToneSubtle})
		}
		budget := maxPanesPerSession
		for _, ws := range s.Workspaces {
			nodes, used := workspaceNodes(ws, s.Name, st, budget, width)
			card.Children = append(card.Children, nodes...)
			budget -= used
		}
		return card
	}
	card.Children = append(card.Children, stoppedHeader(s, st))
	if st.Settings.ShowStoppedSessions {
		for _, name := range s.StoppedWorkspaces {
			card.Children = append(card.Children, &v1.Node{Kind: v1.KindText, Text: name, Size: "caption", Tone: v1.ToneSubtle})
		}
	}
	return card
}

func runningHeader(s SessionRow) *v1.Node {
	attach := &v1.Node{Kind: v1.KindButton, ID: "attach:" + s.Name, Name: "Attach to " + s.Name,
		Role: "button", Tooltip: "Attach in terminal", Events: []v1.EventKind{v1.EventActivate},
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "dns", IconSize: panelIconSize, Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: s.Name, Bold: true, MaxWidth: 120},
			{Kind: v1.KindText, Text: "running", Size: "caption", Tone: v1.ToneSubtle},
		}}
	return &v1.Node{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
		attach,
		{Kind: v1.KindButton, ID: "stop:" + s.Name, Icon: "stop", Width: 24, Height: 24,
			Name: "Stop session " + s.Name, Role: "button", Tooltip: "Stop session",
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func stoppedHeader(s SessionRow, st PanelState) *v1.Node {
	row := &v1.Node{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: "terminal", IconSize: panelIconSize, Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: s.Name + " (stopped)", Bold: true, MaxWidth: 140},
		{Kind: v1.KindButton, ID: "attach:" + s.Name, Icon: "dns", Width: 24, Height: 24,
			Name: "Attach to " + s.Name, Role: "button", Tooltip: "Attach in terminal",
			Events: []v1.EventKind{v1.EventActivate}},
	}}
	if st.ConfirmDelete == s.Name {
		row.Children = append(row.Children,
			&v1.Node{Kind: v1.KindButton, ID: "confirmdelete", Icon: "check", Width: 24, Height: 24,
				Tone: v1.ToneError, Name: "Confirm delete " + s.Name, Role: "button", Tooltip: "Delete this session",
				Events: []v1.EventKind{v1.EventActivate}},
			&v1.Node{Kind: v1.KindButton, ID: "canceldelete", Icon: "close", Width: 24, Height: 24,
				Name: "Cancel delete", Role: "button", Tooltip: "Keep this session",
				Events: []v1.EventKind{v1.EventActivate}})
		return row
	}
	row.Children = append(row.Children, &v1.Node{Kind: v1.KindButton, ID: "delete:" + s.Name,
		Icon: "delete", Width: 24, Height: 24, Name: "Delete session " + s.Name, Role: "button",
		Tooltip: "Delete session", Events: []v1.EventKind{v1.EventActivate}})
	return row
}

// workspaceNodes renders one workspace and returns how many panes it spent
// from the session's pane budget.
func workspaceNodes(ws WorkspaceRow, sess string, st PanelState, paneBudget, width int) ([]*v1.Node, int) {
	if len(ws.Panes) == 0 || paneBudget <= 0 {
		return nil, 0
	}
	if len(ws.Panes) == 1 {
		p := ws.Panes[0]
		primary := ws.Label
		if primary == "" {
			primary = p.Title
		}
		nodes := []*v1.Node{paneRow(p, sess, primary, primary != p.Title, st)}
		if peek, ok := st.Peeks[p.PaneID]; ok {
			nodes = append(nodes, peekNode(peek, width))
		}
		return nodes, 1
	}

	out := []*v1.Node{workspaceHeader(ws)}
	col := &v1.Node{Kind: v1.KindColumn, Padding: 12, Gap: 4}
	used := 0
	for _, p := range ws.Panes {
		if used >= paneBudget {
			break
		}
		col.Children = append(col.Children, paneRow(p, sess, p.Title, false, st))
		if peek, ok := st.Peeks[p.PaneID]; ok {
			col.Children = append(col.Children, peekNode(peek, width))
		}
		used++
	}
	if omitted := len(ws.Panes) - used; omitted > 0 {
		col.Children = append(col.Children, moreText(omitted, "panes"))
	}
	out = append(out, col)
	return out, used
}

func workspaceHeader(ws WorkspaceRow) *v1.Node {
	agents := 0
	for _, p := range ws.Panes {
		if p.Status != StatusNone {
			agents++
		}
	}
	return &v1.Node{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: "folder-open", IconSize: panelIconSize, Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: ws.Label, Bold: true},
		{Kind: v1.KindText, Text: fmt.Sprintf("%d panes · %d agents", len(ws.Panes), agents), Size: "caption", Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: StatusLabel(nil, ws.Status), Size: "caption", Tone: statusTone(ws.Status)},
	}}
}

// paneRow is one agent line: a focus control plus the read toggle. The
// attention fills survive even without an icon so a blocked row still reads.
func paneRow(p PaneRow, sess, primary string, withTitle bool, st PanelState) *v1.Node {
	row := &v1.Node{Kind: v1.KindRow, Gap: 4}
	switch p.Status {
	case StatusBlocked:
		row.Fill = "error-container"
	case StatusDone:
		row.Fill = "soft"
	}

	button := &v1.Node{Kind: v1.KindButton, ID: "focus:" + sess + ":" + p.PaneID, Name: p.Title,
		Role: "button", Tooltip: "Focus agent", Events: []v1.EventKind{v1.EventActivate},
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: statusGlyph(p.Status), IconSize: panelIconSize, Tone: statusTone(p.Status)},
			{Kind: v1.KindText, Text: primary, Bold: true, MaxWidth: 72},
		}}
	if withTitle {
		button.Children = append(button.Children, &v1.Node{Kind: v1.KindText, Text: p.Title, Size: "caption", Tone: v1.ToneSubtle, MaxWidth: 40})
	}
	if p.TabLabel != "" {
		button.Children = append(button.Children, &v1.Node{Kind: v1.KindText, Text: p.TabLabel, Size: "caption", Tone: v1.ToneSubtle, MaxWidth: 40})
	}
	if p.StatusLabel != "" {
		button.Children = append(button.Children, &v1.Node{Kind: v1.KindText, Text: p.StatusLabel, Size: "caption", Tone: statusTone(p.Status), MaxWidth: 48})
	}
	row.Children = append(row.Children, button)

	if p.Readable {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindButton,
			ID: "read:" + sess + ":" + p.PaneID, Icon: "visibility", Width: 24, Height: 24,
			Name: "Show last output for " + p.Title, Role: "button", Tooltip: "Show last output",
			Events: []v1.EventKind{v1.EventActivate}})
	}
	if p.SinceLabel != "" {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: p.SinceLabel, Size: "caption", Tone: v1.ToneSubtle})
	}
	// time-in-state is pre-computed by the model (PaneRow.SinceLabel); the
	// renderer stays pure (no clock).
	return row
}

func peekNode(peek Peek, width int) *v1.Node {
	lineLen := width - 80
	if lineLen < 24 {
		lineLen = 24
	}
	lines := WrapText(peek.Text, lineLen, peekByteCap)
	if peek.Lines > 0 && len(lines) > peek.Lines {
		lines = lines[:peek.Lines]
	}
	col := &v1.Node{Kind: v1.KindColumn, Fill: "container", Shape: "small", Padding: 6, Gap: 2}
	for _, ln := range lines {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: ln, Size: "mono"})
	}
	if len(col.Children) == 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "(no output)", Size: "mono", Tone: v1.ToneSubtle})
	}
	return col
}

func newSessionRow(st PanelState) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindTextInput, ID: "newname", Name: "New session name", Role: "input",
			Placeholder: "session name", SubmitOnEnter: true, Text: st.NewName,
			Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
		{Kind: v1.KindButton, ID: "newstart", Icon: "add", Text: "New", Name: "Start new session",
			Role: "button", Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func countsCaption(c Counts) string {
	var parts []string
	if c.Agents > 0 {
		parts = append(parts, fmt.Sprintf("%d agents", c.Agents))
	}
	if c.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", c.Blocked))
	}
	if c.Done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", c.Done))
	}
	if c.Working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", c.Working))
	}
	return strings.Join(parts, " · ")
}

// statusGlyph is the pane's status mark from the allowed icon subset.
func statusGlyph(s Status) string {
	switch s {
	case StatusBlocked:
		return "do_not_disturb_on"
	case StatusDone:
		return "check"
	case StatusWorking:
		return "play_arrow"
	case StatusIdle:
		return "pause"
	default:
		return "ghost"
	}
}

// statusTone maps a pane status onto the wire's tone vocabulary.
func statusTone(s Status) v1.Tone {
	switch s {
	case StatusBlocked:
		return v1.ToneError
	case StatusDone:
		return v1.ToneAccent
	case StatusWorking:
		return v1.ToneNormal
	default:
		return v1.ToneSubtle
	}
}

func moreText(n int, noun string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("… %d more %s", n, noun), Size: "caption", Tone: v1.ToneSubtle}
}

// sortedSessions orders default first, then name, without mutating the model.
func sortedSessions(m *Model) []SessionRow {
	out := append([]SessionRow(nil), m.Sessions...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// nodeBudget counts nodes as the tree is assembled so the hard ceiling holds
// however large the model is.
type nodeBudget struct {
	used, max int
}

func (b *nodeBudget) add(root *v1.Node, nodes ...*v1.Node) {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		c := countNodes(n)
		if b.used+c > b.max {
			return
		}
		b.used += c
		root.Children = append(root.Children, n)
	}
}

func countNodes(n *v1.Node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += countNodes(c)
	}
	return total
}
