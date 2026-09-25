package faith

import (
	"fmt"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Node IDs the plugin handles.
const (
	NodeCross      = "cross"
	NodePrev       = "prev"
	NodeNext       = "next"
	NodeNew        = "new"
	NodeRead       = "read"
	nodeXrefPrefix = "xref:"
)

// XrefNode is the ID of the i-th cross-reference button.
func XrefNode(i int) string { return fmt.Sprintf("%s%d", nodeXrefPrefix, i) }

// Layout constants. The panel box is the manifest's 440×460; text is
// wrapped for the list's content width at the host's eight pixels per byte.
const (
	panelPadding    = 16
	panelListHeight = 344
	verseLineBytes  = 36
	noteLineBytes   = 52
	cardPadding     = 14
	controlSize     = 36
	controlPadding  = 12
	chipHeight      = 30
	chipPadding     = 10
	chipGap         = 6
	chipRowWidth    = 380
	tooltipLines    = 2
	tooltipBytes    = 33
	// MaxCommentaryLines caps a commentary entry in the panel.
	MaxCommentaryLines = 40
)

// CommentaryStatus is where the commentary section stands.
type CommentaryStatus int

const (
	CommentaryOff CommentaryStatus = iota
	CommentaryLoading
	CommentaryReady
	CommentaryNone
	CommentaryUnavailable
)

// PanelModel is everything the panel shows.
type PanelModel struct {
	Ref         Ref
	Translation string
	Verse       string
	CanPrev     bool
	CanNext     bool
	CanRead     bool
	Commentary  CommentaryStatus
	Note        string
	Xrefs       []Ref
}

// BarTree is the cross, and the reference beside it when asked for.
func BarTree(ref Ref, showReference bool) *v1.Node {
	children := []*v1.Node{{
		Kind: v1.KindButton, ID: NodeCross, Key: NodeCross, Icon: "cross",
		Name: "Faith: click for a prayer", Role: "button",
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}}
	if showReference {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: ref.String(), Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindRow, Gap: 4, Children: children}
}

// TooltipTree is the reference, the start of the verse, and how to read on.
func TooltipTree(ref Ref, verse string) *v1.Node {
	children := []*v1.Node{{Kind: v1.KindText, Text: ref.String(), Tone: v1.ToneAccent, Bold: true}}
	lines := Wrap(verse, tooltipBytes)
	if len(lines) > tooltipLines {
		lines = lines[:tooltipLines]
		last := lines[tooltipLines-1]
		if len(last)+len("…") > tooltipBytes {
			last = Wrap(last, tooltipBytes-len("…"))[0]
		}
		lines[tooltipLines-1] = last + "…"
	}
	for _, l := range lines {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: l})
	}
	children = append(children, &v1.Node{Kind: v1.KindText, Text: "Right-click to read", Tone: v1.ToneSubtle, Size: "caption"})
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: children}
}

// PanelTree is the reading panel: the reference above a scrolling list that
// holds the verse in a card, the commentary, and the cross-references, with
// the controls fixed along the foot.
func PanelTree(m PanelModel) *v1.Node {
	header := &v1.Node{Kind: v1.KindRow, PinEnd: true, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Key: "ref", Text: m.Ref.String(), Tone: v1.ToneAccent, Bold: true, Size: "headline"},
		{Kind: v1.KindText, Text: m.Translation, Tone: v1.ToneSubtle, Size: "caption"},
	}}

	card := &v1.Node{Kind: v1.KindColumn, Key: "verse", Fill: "card", Radius: 12, Padding: cardPadding, Gap: 4}
	for _, l := range Wrap(m.Verse, verseLineBytes) {
		card.Children = append(card.Children, &v1.Node{Kind: v1.KindText, Text: l, Size: "title"})
	}
	body := &v1.Node{Kind: v1.KindList, Key: "body", Height: panelListHeight, Gap: 6, Children: []*v1.Node{card}}
	if m.Commentary != CommentaryOff {
		body.Children = append(body.Children, sectionHead("description", "Commentary · Adam Clarke"))
		body.Children = append(body.Children, commentaryNodes(m)...)
	}
	body.Children = append(body.Children, sectionHead("link", "See also"))
	if len(m.Xrefs) == 0 {
		body.Children = append(body.Children, subtle("No cross-references for this verse."))
	}
	body.Children = append(body.Children, chipRows(m.Xrefs)...)
	body.Children = append(body.Children, subtle("Cross-references: OpenBible.info, CC BY"))

	nav := &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		squareButton(NodePrev, "chevron_left", "Previous verse", !m.CanPrev),
		squareButton(NodeNext, "chevron_right", "Next verse", !m.CanNext),
		labelButton(NodeNew, "refresh", "New verse", "New verse"),
	}}
	controls := nav
	if m.CanRead {
		controls = &v1.Node{Kind: v1.KindRow, PinEnd: true, Gap: 6, Children: []*v1.Node{
			nav, labelButton(NodeRead, "menu_book", "Read chapter", "Read the chapter in a browser"),
		}}
	}
	return &v1.Node{Kind: v1.KindColumn, Padding: panelPadding, Gap: 10, Children: []*v1.Node{header, body, controls}}
}

// chipRows packs cross-reference chips into rows that fit the list's width,
// measuring each the way the host does: eight pixels a byte plus padding.
func chipRows(refs []Ref) []*v1.Node {
	var rows []*v1.Node
	var row *v1.Node
	used := 0
	for i, r := range refs {
		label := r.String()
		w := len(label)*8 + 2*chipPadding
		if row == nil || used+chipGap+w > chipRowWidth {
			row = &v1.Node{Kind: v1.KindRow, Gap: chipGap}
			rows = append(rows, row)
			used = -chipGap
		}
		used += chipGap + w
		row.Children = append(row.Children, &v1.Node{
			Kind: v1.KindButton, ID: XrefNode(i), Text: label, Fill: "chip",
			Height: chipHeight, Padding: chipPadding,
			Name: "Go to " + label, Role: "button", Events: []v1.EventKind{v1.EventActivate},
		})
	}
	return rows
}

func commentaryNodes(m PanelModel) []*v1.Node {
	switch m.Commentary {
	case CommentaryLoading:
		return []*v1.Node{subtle("Loading…")}
	case CommentaryNone:
		return []*v1.Node{subtle("Adam Clarke has no note on this verse.")}
	case CommentaryUnavailable:
		return []*v1.Node{subtle("Commentary unavailable offline.")}
	}
	lines := Wrap(m.Note, noteLineBytes)
	shortened := len(lines) > MaxCommentaryLines
	if shortened {
		lines = lines[:MaxCommentaryLines]
	}
	out := make([]*v1.Node, 0, len(lines)+1)
	for _, l := range lines {
		out = append(out, &v1.Node{Kind: v1.KindText, Text: l, Tone: v1.ToneSubtle, Size: "caption"})
	}
	if shortened {
		out = append(out, subtle("Shortened. The full note is online."))
	}
	return out
}

// squareButton is an icon-only control at the panel's control size.
func squareButton(id, icon, name string, disabled bool) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Key: id, Icon: icon, Name: name, Role: "button", Fill: "soft",
		Width: controlSize, Height: controlSize, Disabled: disabled, Events: []v1.EventKind{v1.EventActivate},
	}
}

// labelButton is an icon and a label at the panel's control size.
func labelButton(id, icon, text, name string) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Key: id, Icon: icon, Text: text, Name: name, Role: "button", Fill: "soft",
		Height: controlSize, Padding: controlPadding, Events: []v1.EventKind{v1.EventActivate},
	}
}

func sectionHead(icon, title string) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: icon},
		{Kind: v1.KindText, Text: title, Tone: v1.ToneSubtle, Size: "label", Bold: true},
	}}
}

func subtle(s string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: s, Tone: v1.ToneSubtle, Size: "caption"}
}
