package hermes

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The chat panel's vertical budget, from the plan: padding 14, gap 8, a
// 56 header, a 40 action row, the section block, a 112 approval card, a 28
// activity line, a 48 composer, and the transcript gets the rest but never
// less than 160.
const (
	panelPad        = 14
	panelGap        = 8
	panelHeaderH    = 56
	panelActionsH   = 40
	panelApprovalH  = 112
	panelActivityH  = 28
	panelComposerH  = 48
	minTranscriptH  = 160
	panelAvatarSize = 40
	stripH          = 32

	maxTranscriptMessages = 24
	maxTranscriptEvents   = 8
	maxBubbleLines        = 12
	bubblePad             = 8
	bubbleGap             = 2
	transcriptGap         = 6

	iconUser      = "person"
	iconHermes    = "ai-usage"
	iconEvent     = "terminal"
	iconApproval  = "gpp_maybe"
	iconSessions  = "schedule"
	iconPin       = "push_pin"
	iconClose     = "close"
	iconSend      = "send"
	iconReset     = "replay"
	iconChat      = "sms"
	iconOneShot   = "content_copy"
	iconStop      = "stop"
	iconOpen      = "expand_more"
	iconClosed    = "chevron_right"
	iconStartGate = "power_settings_new"
)

// Section is the one collapsible section open under the action row.
type Section string

const (
	SectionNone     Section = ""
	SectionSummary  Section = "summary"
	SectionSessions Section = "sessions"
	SectionModel    Section = "model"
)

var sectionTitles = map[Section]string{
	SectionSummary: "Summary", SectionSessions: "Sessions", SectionModel: "Model",
}

// ToggleSection opens s and closes whatever else was open; pressing the
// open section closes it.
func ToggleSection(open, s Section) Section {
	if open == s {
		return SectionNone
	}
	return s
}

// SectionForNode maps a section toggle's id to its section.
func SectionForNode(id string) (Section, bool) {
	s := Section(strings.TrimPrefix(id, "section:"))
	if !strings.HasPrefix(id, "section:") || sectionTitles[s] == "" {
		return SectionNone, false
	}
	return s, true
}

// ToolEvent is one background action from the bridge's event stream.
type ToolEvent struct {
	ID   string
	Type string
	Name string
	Text string
}

// ComposerState is the composer's identity. Reseed bumps only on send, so
// a republish keeps what the user is typing.
type ComposerState struct {
	Reseed  uint64
	OneShot bool
}

// AfterSend clears the composer by bumping its reseed.
func AfterSend(c ComposerState) ComposerState {
	c.Reseed++
	return c
}

// PanelState is everything the chat panel draws. It stands in for the
// runtime snapshot until that lands.
type PanelState struct {
	Summary          SummaryState
	ApprovalTool     string
	SessionID        string
	Messages         []Message
	Events           []ToolEvent
	LastTool         string
	ClientOnly       bool
	Pinned           bool
	ShowToolActivity bool
	DefaultProvider  string
	DefaultModel     string
	Sessions         SessionsSnapshot
	Models           []ModelOption
	Picker           ModelPicker
	Composer         ComposerState
	Section          Section
	Avatar           string
	Now              time.Time
}

// BridgeCall is one POST the panel asks the runtime to make.
type BridgeCall struct {
	Path string
	Body any
}

// PromptRequest is the body of /prompt and /oneshot.
type PromptRequest struct {
	Text string `json:"text"`
}

var errEmptyPrompt = errors.New("Prompt is empty")

// SendPlan is what Send does: a one-shot goes to /oneshot; otherwise a
// prompt with no session creates one first, as noctalia does.
func SendPlan(sessionID, text string, oneShot bool) ([]BridgeCall, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errEmptyPrompt
	}
	body := PromptRequest{Text: text}
	if oneShot {
		return []BridgeCall{{Path: "/oneshot", Body: body}}, nil
	}
	var calls []BridgeCall
	if sessionID == "" {
		calls = append(calls, BridgeCall{Path: "/session/create"})
	}
	return append(calls, BridgeCall{Path: "/prompt", Body: body}), nil
}

// ResetPlan is Reset: noctalia starts a new session.
func ResetPlan() []BridgeCall {
	return []BridgeCall{{Path: "/session/create"}}
}

// panelStatusIcon is the design's panel status table. It is not
// BarStatus.Glyph: the panel keeps its own locked set.
func panelStatusIcon(s BarStatus) string {
	switch s {
	case BarStatusOffline:
		return "power_settings_new"
	case BarStatusIdle:
		return "check"
	case BarStatusBusy:
		return "refresh"
	case BarStatusAttention:
		return "notifications-active"
	case BarStatusDegraded:
		return "gpp_maybe"
	case BarStatusError:
		return "cancel"
	}
	return "ai-usage"
}

// panelModelLabel is provider / model from the bridge, else the
// configured default, else the detected model, else No model selected.
func panelModelLabel(p PanelState) string {
	join := func(provider, model string) string {
		if provider != "" {
			return provider + " / " + model
		}
		return model
	}
	s := p.Summary
	switch {
	case s.ModelName != "":
		return join(s.Provider, s.ModelName)
	case p.DefaultModel != "":
		return join(p.DefaultProvider, p.DefaultModel)
	case s.Model != "":
		return join(s.Provider, s.Model)
	}
	return "No model selected"
}

func turnRunning(p PanelState) bool {
	return p.Summary.SessionRunning || deriveBarStatus(p.Summary.BarState) == BarStatusBusy
}

// activityLine is noctalia's background-action caption.
func activityLine(p PanelState) string {
	s := p.Summary
	if s.RunningTools > 0 {
		return fmt.Sprintf("%d background action running", s.RunningTools)
	}
	line := fmt.Sprintf("%d background actions", s.ToolEvents)
	if p.LastTool != "" {
		line += ", last: " + p.LastTool
	}
	return line
}

func showActivity(p PanelState) bool {
	busy := p.Summary.ToolEvents > 0 || p.Summary.RunningTools > 0
	return busy && (p.ShowToolActivity || turnRunning(p))
}

// PanelTree renders the chat panel into the width by height box the host
// opened. Fixed rows take their heights first; the open section takes what
// is left over a 160 transcript, and a pending approval always stays.
func PanelTree(p PanelState, width, height int) *v1.Node {
	inner := width - 2*panelPad
	status := deriveBarStatus(p.Summary.BarState)
	running := turnRunning(p)

	header := panelHeader(p, status, inner)
	actions := panelActions(p, running, inner)
	composer := panelComposer(p, running, inner)
	var approval, activity *v1.Node
	if p.Summary.ApprovalPending {
		approval = approvalCard(p, inner)
	}
	if showActivity(p) {
		activity = activityRow(p, inner)
	}

	fixed := []*v1.Node{header, actions, approval, activity, composer}
	used, count := 2*panelPad, 1 // the transcript
	for _, n := range fixed {
		if n != nil {
			used += n.Height
			count++
		}
	}
	gaps := func(n int) int { return (n - 1) * panelGap }

	var sections *v1.Node
	if room := height - used - gaps(count+1) - minTranscriptH; room >= stripH {
		sections = sectionBlock(p, inner, room)
		used += sections.Height
		count++
	}
	transcriptH := height - used - gaps(count)

	children := []*v1.Node{header, actions}
	if sections != nil {
		children = append(children, sections)
	}
	for _, n := range []*v1.Node{approval, activity} {
		if n != nil {
			children = append(children, n)
		}
	}
	children = append(children, transcript(p, inner, transcriptH), composer)
	return &v1.Node{
		Kind: v1.KindColumn, Width: width, Height: height,
		Padding: panelPad, Gap: panelGap, Children: children,
	}
}

func textWidth(s string) int { return 8 * len(s) }

// The host's own metric is 8 a byte, but the painter elides a label that
// overruns its button in the real font, which runs wider; a project glyph
// on an icon-only button is painted as a label too. Buttons are sized with
// this margin so neither is cut.
const (
	realByteWidth = 10
	buttonGlyphW  = 20
	iconButtonW   = 36
)

// buttonWidth fits an optional glyph, its gap and the label inside 6
// padding either side, in the real font.
func buttonWidth(text, icon string) int {
	if text == "" {
		return iconButtonW
	}
	w := 12 + realByteWidth*len(text)
	if icon != "" {
		w += buttonGlyphW + 8
	}
	return w
}

// labelBytes is how much of a label a button width holds in the real font.
func labelBytes(width int, icon bool) int {
	w := width - 12
	if icon {
		w -= buttonGlyphW + 8
	}
	return w / realByteWidth
}

func autoButton(id, text, name, icon string, height int) *v1.Node {
	return button(id, text, name, icon, buttonWidth(text, icon), height)
}

func panelHeader(p PanelState, status BarStatus, inner int) *v1.Node {
	pin := "Pin"
	if p.Pinned {
		pin = "Unpin"
	}
	sessIcon := iconSessions
	sessions := autoButton("section:sessions", "Sessions", "Sessions", sessIcon, sectionButtonH)
	sessions.Fill = "soft"
	if p.Section == SectionSessions {
		sessions.Fill = "accent"
	}
	right := &v1.Node{Kind: v1.KindRow, Gap: 6, Height: sectionButtonH, Children: []*v1.Node{
		sessions,
		autoButton("pin", pin, pin+" the panel", iconPin, sectionButtonH),
		autoButton("close", "", "Close", iconClose, sectionButtonH),
	}}
	rightW := 2 * 6
	for _, c := range right.Children {
		rightW += c.Width
	}
	right.Width = rightW

	var mark *v1.Node
	if p.Avatar != "" {
		mark = &v1.Node{Kind: v1.KindImage, Path: p.Avatar, Name: "Hermes", Role: "img", ImageSize: panelAvatarSize}
	} else {
		mark = &v1.Node{Kind: v1.KindIcon, Icon: fallbackAvatarIcon, IconSize: 32, Tone: status.Tone(), Name: "Hermes"}
	}
	textW := inner - rightW - panelAvatarSize - 2*10
	label := clip(status.Label(), textW/8-3)
	left := &v1.Node{Kind: v1.KindRow, Gap: 10, Height: panelHeaderH, Width: inner - rightW - 10, Children: []*v1.Node{
		mark,
		{Kind: v1.KindColumn, Width: textW, Gap: 0, Children: []*v1.Node{
			{Kind: v1.KindRow, Height: lineHeight + 4, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindText, Text: "Hermes", Bold: true, Size: "title"},
			}},
			{Kind: v1.KindRow, Height: lineHeight, Gap: 6, Children: []*v1.Node{
				{Kind: v1.KindIcon, Key: "header-status-icon", Icon: panelStatusIcon(status), IconSize: 14, Tone: status.Tone(), Name: label},
				{Kind: v1.KindText, Text: label, Size: "caption", Tone: status.Tone()},
			}},
			{Kind: v1.KindText, Text: clip(panelModelLabel(p), textW/8), Size: "caption", Tone: v1.ToneSubtle},
		}},
	}}
	return &v1.Node{
		Kind: v1.KindRow, Key: "header", Width: inner, Height: panelHeaderH, Gap: 10, PinEnd: true,
		Children: []*v1.Node{left, right},
	}
}

func panelActions(p PanelState, running bool, inner int) *v1.Node {
	type action struct{ id, text, icon string }
	actions := []action{{"new-session", "New session", menuIconNewSession}}
	if running {
		actions = append(actions, action{"interrupt", "Interrupt", menuIconInterrupt})
	}
	actions = append(actions, action{"refresh", "Refresh", menuIconRefresh})
	if !p.ClientOnly {
		// Stop is offered only for a gateway this machine runs; a remote
		// gateway is client-only mode and gets neither.
		if p.Summary.GatewayStatus == "running" {
			actions = append(actions, action{"stop-gateway", "Stop gateway", iconStop})
		} else {
			actions = append(actions, action{"start-gateway", "Start gateway", iconStartGate})
		}
	}
	actions = append(actions, action{"settings", "Settings", menuIconSettings})

	// Labels when they fit. Otherwise drop them, least ambiguous glyph
	// first, so Interrupt and Stop gateway, which share a glyph, keep their
	// words longest; every button keeps its label as its accessible name.
	bare := map[string]bool{}
	width := func() int {
		w := (len(actions) - 1) * 8
		for _, a := range actions {
			text := a.text
			if bare[a.id] {
				text = ""
			}
			w += buttonWidth(text, a.icon)
		}
		return w
	}
	for _, id := range []string{"settings", "refresh", "new-session", "start-gateway", "stop-gateway", "interrupt"} {
		if width() <= inner {
			break
		}
		bare[id] = true
	}
	row := &v1.Node{Kind: v1.KindRow, Key: "actions", Width: inner, Height: panelActionsH, Gap: 8}
	for _, a := range actions {
		text := a.text
		if bare[a.id] {
			text = ""
		}
		row.Children = append(row.Children, autoButton(a.id, text, a.text, a.icon, sectionButtonH))
	}
	return row
}

// sectionBlock is the Summary and Model toggles with the open section's
// body under them, in room pixels at most.
func sectionBlock(p PanelState, inner, room int) *v1.Node {
	strip := &v1.Node{Kind: v1.KindRow, Width: inner, Height: stripH, Gap: 8}
	for _, s := range []Section{SectionSummary, SectionModel} {
		icon := iconClosed
		if p.Section == s {
			icon = iconOpen
		}
		b := autoButton("section:"+string(s), sectionTitles[s], sectionTitles[s], icon, stripH)
		b.Fill = "soft"
		if p.Section == s {
			b.Fill = "accent"
		}
		strip.Children = append(strip.Children, b)
	}
	block := &v1.Node{Kind: v1.KindColumn, Key: "sections", Width: inner, Height: stripH, Gap: sectionGap, Children: []*v1.Node{strip}}
	if p.Section == SectionNone {
		return block
	}
	bodyRoom := room - stripH - sectionGap
	if bodyRoom < lineHeight {
		return block
	}
	body := sectionBody(p, inner, bodyRoom)
	body.Key = "section-body:" + string(p.Section)
	block.Children = append(block.Children, body)
	block.Height += sectionGap + body.Height
	return block
}

func sectionBody(p PanelState, inner, room int) *v1.Node {
	var natural, min int
	switch p.Section {
	case SectionSummary:
		natural, min = gridHeight(len(summaryRows(p.Summary))), lineHeight
	case SectionSessions:
		natural, min = sessionsNaturalHeight(p.Sessions), sessionsMinHeight
	case SectionModel:
		natural = modelNaturalHeight(p.Models, p.Picker, p.DefaultProvider, p.DefaultModel)
		min = natural
		if min > modelMinHeight {
			min = modelMinHeight
		}
	}
	h := natural
	if h > room {
		h = room
	}
	if h < min {
		return &v1.Node{Kind: v1.KindText, Height: lineHeight, Size: "caption", Tone: v1.ToneSubtle,
			Text: clip("Enlarge the panel to show "+sectionTitles[p.Section], inner/8)}
	}
	switch p.Section {
	case SectionSummary:
		return summaryRowsFit(p.Summary, inner, h)
	case SectionSessions:
		return SessionsSection(p.Sessions, p.Now, inner, h)
	}
	return ModelSection(p.Models, p.Picker, p.DefaultProvider, p.DefaultModel, inner, h)
}

// summaryRowsFit is the summary popup's grid, keeping as many of its five
// lines as height holds.
func summaryRowsFit(s SummaryState, inner, height int) *v1.Node {
	grid := summaryGrid(s, inner)
	keep := (height + rowGap) / (lineHeight + rowGap)
	if keep < len(grid.Children) {
		grid.Children = grid.Children[:keep]
		grid.Height = keep*lineHeight + (keep-1)*rowGap
	}
	return grid
}

// approvalCard is the design's approval fixture: the shield, the tool,
// the message, and Deny, Allow, Allow Session. The buttons carry preview
// ids; posting a choice is Task 6.
func approvalCard(p PanelState, inner int) *v1.Node {
	title := strings.TrimSpace(p.ApprovalTool)
	if title == "" {
		title = "Approval required"
	}
	msg := strings.TrimSpace(p.Summary.ApprovalMessage)
	content := inner - 2*10
	buttons := &v1.Node{Kind: v1.KindRow, Width: content, Height: sectionButtonH, Gap: 8, Children: []*v1.Node{
		autoButton("approval-preview:deny", "Deny", "Deny", "", sectionButtonH),
		autoButton("approval-preview:allow", "Allow", "Allow", "", sectionButtonH),
		autoButton("approval-preview:allow-session", "Allow Session", "Allow Session", "", sectionButtonH),
	}}
	buttons.Children[0].Fill = "error-container"
	buttons.Children[1].Fill = "accent"
	card := &v1.Node{
		Kind: v1.KindColumn, Key: "approval", Fill: "container", Radius: 10,
		Width: inner, Height: panelApprovalH, Padding: 10, Gap: 6,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Width: content, Height: 20, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: iconApproval, IconSize: 18, Tone: v1.ToneWarning, Name: "Approval"},
				{Kind: v1.KindText, Text: clip(title, (content-26)/8), Bold: true},
			}},
		},
	}
	if msg != "" {
		card.Children = append(card.Children, &v1.Node{Kind: v1.KindText, Text: clip(msg, content/8), Size: "caption", Height: lineHeight})
	}
	card.Children = append(card.Children, buttons)
	return card
}

func activityRow(p PanelState, inner int) *v1.Node {
	return &v1.Node{
		Kind: v1.KindRow, Key: "activity", Fill: "soft", Radius: 8,
		Width: inner, Height: panelActivityH, Padding: 6, Gap: 8,
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: iconEvent, IconSize: 16, Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: clip(activityLine(p), (inner-12-24)/8), Size: "caption"},
		},
	}
}

func transcript(p PanelState, inner, height int) *v1.Node {
	if len(p.Messages) == 0 {
		return emptyTranscript(p, inner, height)
	}
	list := &v1.Node{Kind: v1.KindList, Key: "transcript", Width: inner, Height: height, Gap: transcriptGap}
	msgs := p.Messages
	if len(msgs) > maxTranscriptMessages {
		msgs = msgs[len(msgs)-maxTranscriptMessages:]
		list.Children = append(list.Children, &v1.Node{
			Kind: v1.KindText, Key: "transcript-caption", Text: "Showing the last 24 messages",
			Size: "caption", Tone: v1.ToneSubtle, Height: lineHeight,
		})
	}
	for _, m := range msgs {
		list.Children = append(list.Children, bubble(m, inner))
	}
	if p.ShowToolActivity {
		events := p.Events
		if len(events) > maxTranscriptEvents {
			events = events[len(events)-maxTranscriptEvents:]
		}
		for _, e := range events {
			list.Children = append(list.Children, eventRow(e, inner))
		}
	}
	return list
}

func emptyTranscript(p PanelState, inner, height int) *v1.Node {
	lines := []string{"No active session", "Send a message to start one."}
	tone := v1.ToneSubtle
	icon := iconChat
	switch {
	case deriveBarStatus(p.Summary.BarState) == BarStatusOffline:
		lines = []string{"Bridge offline", strings.TrimSpace(p.Summary.BridgeError)}
		tone, icon = v1.ToneError, iconStartGate
	case p.SessionID != "":
		lines = []string{"No messages yet", "Ask Hermes anything below."}
	}
	col := &v1.Node{Kind: v1.KindColumn, Key: "transcript", Width: inner, Height: height, Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: icon, IconSize: 28, Tone: tone},
	}}
	for i, l := range lines {
		if l == "" {
			continue
		}
		n := &v1.Node{Kind: v1.KindText, Text: clip(l, inner/8), Size: "caption", Tone: v1.ToneSubtle}
		if i == 0 {
			n.Bold, n.Size, n.Tone = true, "label", tone
		}
		col.Children = append(col.Children, n)
	}
	return col
}

// bubble is one message. Text never wraps in the host, so lines are split
// here to the bubble's width and capped at twelve.
func bubble(m Message, inner int) *v1.Node {
	fill, icon, who := "card", iconHermes, "Hermes"
	if m.Role == "user" {
		fill, icon, who = "accent", iconUser, "You"
	}
	content := inner - 2*bubblePad
	lines := wrapLines(m.Text, content/8, maxBubbleLines)
	b := &v1.Node{
		Kind: v1.KindColumn, Key: "msg:" + m.ID, Fill: fill, Radius: 10,
		Width: inner, Padding: bubblePad, Gap: bubbleGap,
		Children: []*v1.Node{{Kind: v1.KindRow, Height: lineHeight, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon, IconSize: 14},
			{Kind: v1.KindText, Text: who, Bold: true, Size: "caption"},
		}}},
	}
	for _, l := range lines {
		b.Children = append(b.Children, &v1.Node{Kind: v1.KindText, Text: l, Height: lineHeight})
	}
	n := len(b.Children)
	b.Height = 2*bubblePad + n*lineHeight + (n-1)*bubbleGap
	return b
}

func eventRow(e ToolEvent, inner int) *v1.Node {
	text := e.Name
	if e.Text != "" {
		text += ": " + e.Text
	}
	return &v1.Node{
		Kind: v1.KindRow, Key: "event:" + e.ID, Width: inner, Height: lineHeight, Gap: 6,
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: iconEvent, IconSize: 14, Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: clip(text, (inner-20)/8), Size: "caption", Tone: v1.ToneSubtle},
		},
	}
}

// wrapLines splits text on newlines and then on words to maxBytes a line,
// breaking a word longer than a line, and ends with an ellipsis past
// maxLines.
func wrapLines(text string, maxBytes, maxLines int) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			for len(w) > maxBytes {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				cut := maxBytes
				for cut > 0 && !utf8.RuneStart(w[cut]) {
					cut--
				}
				out = append(out, w[:cut])
				w = w[cut:]
			}
			switch {
			case line == "":
				line = w
			case len(line)+1+len(w) <= maxBytes:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	if len(out) > maxLines {
		const more = " …"
		out = out[:maxLines]
		last := out[maxLines-1]
		cut := len(last)
		if cut > maxBytes-len(more) {
			cut = maxBytes - len(more)
		}
		for cut > 0 && !utf8.RuneStart(last[cut]) {
			cut--
		}
		out[maxLines-1] = last[:cut] + more
	}
	return out
}

func panelComposer(p PanelState, running bool, inner int) *v1.Node {
	h := 40
	oneShot := toggle("one-shot", "One-shot", "Send as a one-shot prompt", iconOneShot, p.Composer.OneShot, buttonWidth("One-shot", iconOneShot))
	oneShot.Height = h
	var send *v1.Node
	if running {
		send = autoButton("composer-interrupt", "Interrupt", "Interrupt", iconStop, h)
	} else {
		send = autoButton("send", "Send", "Send", iconSend, h)
		send.Fill = "accent"
	}
	buttons := []*v1.Node{
		autoButton("composer-new-session", "", "New Session", iconChat, h),
		autoButton("composer-reset", "", "Reset", iconReset, h),
		oneShot,
		send,
	}
	used := 0
	for _, b := range buttons {
		used += b.Width + 8
	}
	input := &v1.Node{
		Kind: v1.KindTextInput, ID: "composer", Key: "composer", Name: "Message Hermes", Role: "textbox",
		Placeholder: "Ask Hermes...", SubmitOnEnter: true, Reseed: p.Composer.Reseed,
		Width: inner - used, Height: h, Padding: 10,
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit},
	}
	return &v1.Node{
		Kind: v1.KindRow, Key: "composer-row", Width: inner, Height: panelComposerH, Gap: 8,
		Children: append([]*v1.Node{input}, buttons...),
	}
}
