package hermes

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The transcript is a window over wrapped message lines that the plugin
// moves itself. The host keeps no scroll offset for a plugin list across a
// snapshot and sends no scroll events, so a scrolled list would jump back
// to its top on every poll. The window follows the newest line until the
// user pages back; a paged-back window is anchored to a message line, so
// new output does not move it.
const (
	// bodyLineHeight is one line of the host's 15 px body text, as its
	// painter lays it out; caption rows are lineHeight.
	// It is measured, not declared: the painter sizes text by its font, so
	// this carries a pixel of headroom for a wider face.
	bodyLineHeight = 20
	transcriptBarH = 28
	// maxEventShare caps the tail's tool-event rows to a third of the
	// transcript, so they never crowd out the conversation.
	maxEventShare = 3
	// maxMessageLines only bounds pathological text; MaxMessageBytes
	// already caps a message.
	maxMessageLines = MaxMessageBytes

	nodeTranscriptOlder  = "transcript-older"
	nodeTranscriptNewer  = "transcript-newer"
	nodeTranscriptLatest = "transcript-latest"
)

// TranscriptAnchor is where a paged-back window ends: just before wrapped
// line Line of message MsgID. The zero value follows the newest line.
type TranscriptAnchor struct {
	MsgID string
	Line  int
}

// TranscriptTarget resolves a paging control's node ID to the anchor it
// moves the window to. The ID names the message by a short digest, because
// a bridge can send message IDs of any length.
func TranscriptTarget(node string, msgs []Message) (TranscriptAnchor, bool) {
	if node == nodeTranscriptLatest || node == nodeTranscriptNewer+":latest" {
		return TranscriptAnchor{}, true
	}
	rest, ok := strings.CutPrefix(node, nodeTranscriptOlder+":")
	if !ok {
		rest, ok = strings.CutPrefix(node, nodeTranscriptNewer+":")
	}
	if !ok {
		return TranscriptAnchor{}, false
	}
	sig, num, ok := strings.Cut(rest, ":")
	line, err := strconv.Atoi(num)
	if !ok || err != nil || line < 1 {
		return TranscriptAnchor{}, false
	}
	for _, m := range msgs {
		if messageSig(m.ID) == sig {
			return TranscriptAnchor{MsgID: m.ID, Line: line}, true
		}
	}
	return TranscriptAnchor{}, false
}

func messageSig(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:4])
}

// messageKey is a bubble's stable node key. A bridge can send an ID of any
// length, and keys are bounded, so a long one is named by its digest.
func messageKey(id string) string {
	if len(id) > 64 {
		return "msg:#" + messageSig(id)
	}
	return "msg:" + id
}

// anchorNode is a paging control's ID; a zero anchor is the tail.
func anchorNode(prefix string, a TranscriptAnchor) string {
	if a == (TranscriptAnchor{}) {
		return prefix + ":latest"
	}
	return prefix + ":" + messageSig(a.MsgID) + ":" + strconv.Itoa(a.Line)
}

// fragment is the visible run of one message's lines.
type fragment struct {
	msg   Message
	lines []string
}

// transcriptWindow is what the transcript shows and where its controls go.
type transcriptWindow struct {
	frags     []fragment
	following bool
	// older is nil when nothing is above; newer is meaningful only when the
	// window is paged back, and is the zero anchor once the tail is in reach.
	older *TranscriptAnchor
	newer TranscriptAnchor
}

// wrapper wraps each message once per render, only when the window needs it.
type wrapper struct {
	msgs  []Message
	width int
	cache map[int][]string
}

func (w *wrapper) lines(i int) []string {
	if l, ok := w.cache[i]; ok {
		return l
	}
	l := wrapLines(w.msgs[i].Text, w.width, maxMessageLines)
	w.cache[i] = l
	return l
}

func bubbleHeight(lines int) int {
	return 2*bubblePad + lineHeight + lines*(bodyLineHeight+bubbleGap)
}

// linesThatFit is how many body lines a bubble can show in room, after the
// gap that separates it from the bubble below.
func linesThatFit(room, gap int) int {
	return (room - gap - bubbleHeight(0)) / (bodyLineHeight + bubbleGap)
}

// end resolves the anchor to the message and line the window ends before.
func (w *wrapper) end(a TranscriptAnchor) (msg, line int, following bool) {
	last := len(w.msgs) - 1
	if a.MsgID != "" {
		for i, m := range w.msgs {
			if m.ID == a.MsgID {
				return i, min(max(a.Line, 1), len(w.lines(i))), false
			}
		}
	}
	return last, len(w.lines(last)), true
}

// fitBack fills budget upwards from the end line, newest first.
func (w *wrapper) fitBack(endMsg, endLine, budget int) (frags []fragment, older *TranscriptAnchor) {
	used := 0
	for i := endMsg; i >= 0; i-- {
		lines := w.lines(i)
		end := len(lines)
		if i == endMsg {
			end = endLine
		}
		gap := 0
		if len(frags) > 0 {
			gap = transcriptGap
		}
		k := min(linesThatFit(budget-used, gap), end)
		if k <= 0 {
			older = &TranscriptAnchor{MsgID: w.msgs[i].ID, Line: end}
			break
		}
		frags = append([]fragment{{msg: w.msgs[i], lines: lines[end-k : end]}}, frags...)
		used += gap + bubbleHeight(k)
		if k < end {
			older = &TranscriptAnchor{MsgID: w.msgs[i].ID, Line: end - k}
			break
		}
	}
	return frags, older
}

// fitForward is the end of the window that starts at the given line, or the
// zero anchor when that window reaches the newest line.
func (w *wrapper) fitForward(startMsg, startLine, budget int) TranscriptAnchor {
	used, first := 0, true
	for i, start := startMsg, startLine; i < len(w.msgs); i, start = i+1, 0 {
		lines := w.lines(i)
		avail := len(lines) - start
		if avail <= 0 {
			continue
		}
		gap := transcriptGap
		if first {
			gap = 0
		}
		k := linesThatFit(budget-used, gap)
		if first {
			k = max(k, 1)
		}
		if k <= 0 {
			return TranscriptAnchor{MsgID: w.msgs[i-1].ID, Line: len(w.lines(i - 1))}
		}
		if k < avail {
			return TranscriptAnchor{MsgID: w.msgs[i].ID, Line: start + k}
		}
		used += gap + bubbleHeight(avail)
		first = false
	}
	return TranscriptAnchor{}
}

func (w *wrapper) window(a TranscriptAnchor, budget int) transcriptWindow {
	endMsg, endLine, following := w.end(a)
	frags, older := w.fitBack(endMsg, endLine, budget)
	win := transcriptWindow{frags: frags, following: following, older: older}
	if !following {
		win.newer = w.fitForward(endMsg, endLine, budget)
	}
	return win
}

// visibleEvents is how many of the newest tool events the tail shows.
func visibleEvents(p PanelState, height int) int {
	if !p.ShowToolActivity {
		return 0
	}
	room := height / maxEventShare / (lineHeight + transcriptGap)
	return min(len(p.Events), maxTranscriptEvents, room)
}

func transcript(p PanelState, inner, height int) *v1.Node {
	if len(p.Messages) == 0 {
		return emptyTranscript(p, inner, height)
	}
	w := &wrapper{msgs: p.Messages, width: (inner - 2*bubblePad) / 8, cache: map[int][]string{}}
	// One gap is kept for the spacer that bottom-anchors the bubbles.
	budget := height - transcriptGap
	events := 0
	if a := p.Transcript; a == (TranscriptAnchor{}) {
		events = visibleEvents(p, height)
		budget -= events * (lineHeight + transcriptGap)
	}
	win := w.window(p.Transcript, budget)
	bar := win.older != nil || !win.following
	if bar {
		win = w.window(p.Transcript, budget-transcriptBarH-transcriptGap)
	}
	if !win.following {
		events = 0
	}

	col := &v1.Node{Kind: v1.KindColumn, Key: "transcript", Width: inner, Height: height, Gap: transcriptGap}
	if bar {
		col.Children = append(col.Children, transcriptBar(win, inner))
	}
	// The spacer takes the slack above the bubbles, so the newest line sits
	// on the composer as in any chat.
	spacer := &v1.Node{Kind: v1.KindRow, Key: "transcript-spacer", Width: inner}
	col.Children = append(col.Children, spacer)
	for _, f := range win.frags {
		col.Children = append(col.Children, bubble(f.msg, f.lines, inner))
	}
	for _, e := range p.Events[len(p.Events)-events:] {
		col.Children = append(col.Children, eventRow(e, inner))
	}
	used := 0
	for i, c := range col.Children {
		if i > 0 {
			used += col.Gap
		}
		used += c.Height
	}
	spacer.Height = height - used
	return col
}

// transcriptBar holds the paging controls: Older while anything is above,
// and Newer and Latest while the window is paged back.
func transcriptBar(win transcriptWindow, inner int) *v1.Node {
	older := autoButton(nodeTranscriptOlder, "Older", "Show older messages", "", transcriptBarH)
	if win.older != nil {
		older.ID = anchorNode(nodeTranscriptOlder, *win.older)
	} else {
		older.Disabled = true
	}
	children := []*v1.Node{older}
	used := older.Width
	if !win.following {
		newer := autoButton(anchorNode(nodeTranscriptNewer, win.newer), "Newer", "Show newer messages", "", transcriptBarH)
		latest := autoButton(nodeTranscriptLatest, "Latest", "Jump to the latest message", "", transcriptBarH)
		latest.Fill = "accent"
		used += newer.Width + latest.Width + 2*6
		caption := &v1.Node{Kind: v1.KindRow, Width: inner - used - 6, Height: transcriptBarH, Children: []*v1.Node{
			{Kind: v1.KindText, Text: clip("Earlier messages", (inner-used-18)/8), Size: "caption", Tone: v1.ToneSubtle},
		}}
		children = append(children, caption, newer, latest)
	}
	return &v1.Node{Kind: v1.KindRow, Key: "transcript-bar", Width: inner, Height: transcriptBarH, Gap: 6, Children: children}
}

func emptyTranscript(p PanelState, inner, height int) *v1.Node {
	lines := []string{"No active session", "Send a message to start one."}
	tone := v1.ToneSubtle
	icon := iconChat
	switch {
	case p.Summary.Status.Key == StatusOffline:
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

// bubble is the visible lines of one message. Text never wraps in the host,
// so lines are split to the bubble's width before they get here.
func bubble(m Message, lines []string, inner int) *v1.Node {
	fill, icon, who := "card", iconHermes, "Hermes"
	if m.Role == "user" {
		fill, icon, who = "accent", iconUser, "You"
	}
	b := &v1.Node{
		Kind: v1.KindColumn, Key: messageKey(m.ID), Fill: fill, Radius: 10,
		Width: inner, Height: bubbleHeight(len(lines)), Padding: bubblePad, Gap: bubbleGap,
		Children: []*v1.Node{{Kind: v1.KindRow, Height: lineHeight, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon, IconSize: 14},
			{Kind: v1.KindText, Text: who, Bold: true, Size: "caption"},
		}}},
	}
	for _, l := range lines {
		b.Children = append(b.Children, &v1.Node{Kind: v1.KindText, Text: l, Height: bodyLineHeight})
	}
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
		for cut > 0 && cut < len(last) && !utf8.RuneStart(last[cut]) {
			cut--
		}
		out[maxLines-1] = last[:cut] + more
	}
	return out
}
