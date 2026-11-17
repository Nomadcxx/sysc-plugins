package hermes

import (
	"fmt"
	"strings"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// numberedReply is a reply of n short lines, "Line 01" upwards.
func numberedReply(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "Line %02d of the long streamed reply.\n", i)
	}
	return b.String()
}

func longReplyPanel() PanelState {
	p := basePanel(StatusIdle)
	p.Messages = append(p.Messages,
		Message{ID: "m3", Role: "user", Text: "Stream a long reply."},
		Message{ID: "m4", Role: "assistant", Text: numberedReply(60)})
	return p
}

func transcriptText(t *testing.T, tree *v1.Node) string {
	t.Helper()
	tr := findKey(tree, "transcript")
	if tr == nil {
		t.Fatal("no transcript")
	}
	return strings.Join(texts(tr), "|")
}

// transcriptButton is the paging control whose ID starts with prefix.
func transcriptButton(tree *v1.Node, prefix string) *v1.Node {
	var found *v1.Node
	walk(tree, func(n *v1.Node) {
		if found == nil && n.Kind == v1.KindButton && strings.HasPrefix(n.ID, prefix) {
			found = n
		}
	})
	return found
}

// childrenHeight is what a column's children take with its gaps.
func childrenHeight(n *v1.Node) int {
	h := 0
	for i, c := range n.Children {
		if i > 0 {
			h += n.Gap
		}
		h += c.Height
	}
	return h
}

func TestTranscriptFollowsTheNewestLine(t *testing.T) {
	for _, size := range panelSizes {
		tree := PanelTree(longReplyPanel(), size[0], size[1])
		got := transcriptText(t, tree)
		if !strings.Contains(got, "Line 60 of") {
			t.Errorf("%v: newest line not shown: %q", size, got)
		}
		if strings.Contains(got, "Line 01 of") {
			t.Errorf("%v: a following transcript should not start at the top: %q", size, got)
		}
		tr := findKey(tree, "transcript")
		if used := childrenHeight(tr); used != tr.Height {
			t.Errorf("%v: transcript content %d, want it to fill its %d", size, used, tr.Height)
		}
		// Bottom-anchored like a chat: any slack sits above the bubbles.
		last := tr.Children[len(tr.Children)-1]
		if last.Key != "msg:m4" {
			t.Errorf("%v: last child is %q, want the newest bubble at the bottom", size, last.Key)
		}
	}
}

// TestBubbleHeightCountsBodyLines pins the clipped last line: body text is
// the host's 15 px role, which the painter lays out about 19 px tall
// whatever height the node declares, so a caption-row budget clipped it.
func TestBubbleHeightCountsBodyLines(t *testing.T) {
	if bodyLineHeight < 19 {
		t.Fatalf("bodyLineHeight = %d, the host paints body text about 19 tall", bodyLineHeight)
	}
	p := basePanel(StatusIdle)
	tree := PanelTree(p, 780, 720)
	b := findKey(tree, "msg:m2")
	if b == nil {
		t.Fatal("no bubble for m2")
	}
	body := len(b.Children) - 1
	want := 2*bubblePad + lineHeight + body*(bodyLineHeight+bubbleGap)
	if body != 2 || b.Height != want {
		t.Errorf("bubble has %d body lines and height %d, want 2 and %d", body, b.Height, want)
	}
	for _, c := range b.Children[1:] {
		if c.Height != bodyLineHeight {
			t.Errorf("body line height %d, want %d", c.Height, bodyLineHeight)
		}
	}
}

func TestLongReplyIsReachableByPaging(t *testing.T) {
	p := longReplyPanel()
	tree := PanelTree(p, 780, 720)
	if transcriptButton(tree, "transcript-newer") != nil || transcriptButton(tree, "transcript-latest") != nil {
		t.Error("a following transcript offers Newer or Latest")
	}
	seen := map[int]bool{}
	for step := 0; step < 40; step++ {
		got := transcriptText(t, tree)
		for i := 1; i <= 60; i++ {
			if strings.Contains(got, fmt.Sprintf("Line %02d of", i)) {
				seen[i] = true
			}
		}
		older := transcriptButton(tree, "transcript-older")
		if older == nil {
			t.Fatalf("step %d: no Older control", step)
		}
		if older.Disabled {
			break
		}
		anchor, ok := TranscriptTarget(older.ID, p.Messages)
		if !ok {
			t.Fatalf("step %d: Older id %q does not resolve", step, older.ID)
		}
		p.Transcript = anchor
		tree = PanelTree(p, 780, 720)
	}
	for i := 1; i <= 60; i++ {
		if !seen[i] {
			t.Errorf("Line %02d never shown while paging back", i)
		}
	}
	if !strings.Contains(transcriptText(t, tree), "Tidy the release notes") {
		t.Error("the first message is not reachable")
	}

	latest := transcriptButton(tree, "transcript-latest")
	if latest == nil {
		t.Fatal("a paged-back transcript offers no Latest")
	}
	anchor, ok := TranscriptTarget(latest.ID, p.Messages)
	if !ok || anchor != (TranscriptAnchor{}) {
		t.Fatalf("Latest resolves to %+v, %v", anchor, ok)
	}
}

func TestNewerPagesForwardToTheTail(t *testing.T) {
	p := longReplyPanel()
	p.Transcript = TranscriptAnchor{MsgID: "m4", Line: 10}
	tree := PanelTree(p, 780, 720)
	got := transcriptText(t, tree)
	if !strings.Contains(got, "Line 10 of") || strings.Contains(got, "Line 11 of") {
		t.Fatalf("anchored window should end at Line 10: %q", got)
	}
	for step := 0; step < 10; step++ {
		newer := transcriptButton(tree, "transcript-newer")
		if newer == nil {
			break
		}
		anchor, ok := TranscriptTarget(newer.ID, p.Messages)
		if !ok {
			t.Fatalf("Newer id %q does not resolve", newer.ID)
		}
		p.Transcript = anchor
		tree = PanelTree(p, 780, 720)
	}
	if p.Transcript != (TranscriptAnchor{}) {
		t.Errorf("paging forward should end following the tail, at %+v", p.Transcript)
	}
	if !strings.Contains(transcriptText(t, tree), "Line 60 of") {
		t.Error("the tail is not shown after paging forward")
	}
}

func TestPagedBackTranscriptStaysPutWhenAMessageArrives(t *testing.T) {
	p := longReplyPanel()
	p.Transcript = TranscriptAnchor{MsgID: "m4", Line: 20}
	before := transcriptText(t, PanelTree(p, 780, 720))
	p.Messages = append(p.Messages, Message{ID: "m5", Role: "user", Text: "Another prompt."})
	p.Messages[3].Text += "More streamed text.\n"
	after := transcriptText(t, PanelTree(p, 780, 720))
	if before != after {
		t.Errorf("paged-back transcript moved:\nbefore %q\nafter  %q", before, after)
	}
}

func TestUnknownAnchorFollowsTheTail(t *testing.T) {
	p := longReplyPanel()
	p.Transcript = TranscriptAnchor{MsgID: "gone", Line: 3}
	if got := transcriptText(t, PanelTree(p, 780, 720)); !strings.Contains(got, "Line 60 of") {
		t.Errorf("an anchor on a vanished message should follow the tail: %q", got)
	}
}

func TestTranscriptControlIDsStayBounded(t *testing.T) {
	p := longReplyPanel()
	long := strings.Repeat("x", 1000)
	p.Messages[3].ID = long
	p.Transcript = TranscriptAnchor{MsgID: long, Line: 30}
	tree := PanelTree(p, 780, 720)
	for _, prefix := range []string{"transcript-older", "transcript-newer", "transcript-latest"} {
		b := transcriptButton(tree, prefix)
		if b == nil {
			t.Fatalf("no %s control", prefix)
		}
		if len(b.ID) > 64 {
			t.Errorf("%s id is %d bytes", prefix, len(b.ID))
		}
		if b.Name == "" || b.Role != "button" {
			t.Errorf("%s name %q role %q", prefix, b.Name, b.Role)
		}
	}
	if err := v1.Validate(tree, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	anchor, ok := TranscriptTarget(transcriptButton(tree, "transcript-older").ID, p.Messages)
	if !ok || anchor.MsgID != long {
		t.Errorf("Older on a long id resolves to %+v, %v", anchor, ok)
	}
}

func TestTranscriptTargetRejectsForeignIDs(t *testing.T) {
	msgs := longReplyPanel().Messages
	for _, id := range []string{"send", "transcript-older", "transcript-older:zz:1", "transcript-older:00000000:x", "transcript-newer:00000000:1"} {
		if _, ok := TranscriptTarget(id, msgs); ok {
			t.Errorf("TranscriptTarget(%q) accepted", id)
		}
	}
}

func TestToolEventsShowOnlyAtTheTail(t *testing.T) {
	p := heavyPanel(StatusIdle, SectionNone, false)
	tree := PanelTree(p, 780, 720)
	if findKey(tree, "event:e7") == nil {
		t.Error("the newest tool event is not at the tail")
	}
	if findKey(tree, "event:e0") != nil {
		t.Error("tool events should not crowd out the transcript")
	}
	p.Transcript = TranscriptAnchor{MsgID: "m20", Line: 1}
	if findKey(PanelTree(p, 780, 720), "event:e7") != nil {
		t.Error("tool events shown while paged back")
	}
}

func TestTranscriptLaysOutAtEverySize(t *testing.T) {
	for _, size := range panelSizes {
		for _, anchor := range []TranscriptAnchor{{}, {MsgID: "m4", Line: 25}, {MsgID: "m1", Line: 1}} {
			p := longReplyPanel()
			p.Transcript = anchor
			lintView(t, fmt.Sprintf("%v %+v", size, anchor), PanelTree(p, size[0], size[1]), v1.ViewPanel, size[0], size[1])
		}
	}
}
