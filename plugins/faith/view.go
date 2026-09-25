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
	panelListHeight = 340
	panelLineBytes  = 50
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

// PanelTree is the reading panel: the reference and controls stay put, and
// the verse, commentary, and cross-references scroll beneath them.
func PanelTree(m PanelModel) *v1.Node {
	header := &v1.Node{Kind: v1.KindRow, PinEnd: true, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Key: "ref", Text: m.Ref.String(), Tone: v1.ToneAccent, Bold: true, Size: "title"},
		{Kind: v1.KindText, Text: m.Translation, Tone: v1.ToneSubtle, Size: "caption"},
	}}
	controls := &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		iconButton(NodePrev, "chevron_left", "Previous verse", !m.CanPrev),
		iconButton(NodeNext, "chevron_right", "Next verse", !m.CanNext),
		iconButton(NodeNew, "refresh", "New verse", false),
	}}
	if m.CanRead {
		controls.Children = append(controls.Children, iconButton(NodeRead, "menu_book", "Read the chapter in a browser", false))
	}

	body := &v1.Node{Kind: v1.KindList, Key: "body", Height: panelListHeight, Gap: 4}
	for _, l := range Wrap(m.Verse, panelLineBytes) {
		body.Children = append(body.Children, &v1.Node{Kind: v1.KindText, Text: l})
	}
	if m.Commentary != CommentaryOff {
		body.Children = append(body.Children, &v1.Node{Kind: v1.KindSeparator}, sectionHead("description", "Commentary"))
		body.Children = append(body.Children, commentaryNodes(m)...)
	}
	body.Children = append(body.Children, &v1.Node{Kind: v1.KindSeparator}, sectionHead("link", "See also"))
	if len(m.Xrefs) == 0 {
		body.Children = append(body.Children, subtle("No cross-references for this verse."))
	}
	for i, r := range m.Xrefs {
		body.Children = append(body.Children, &v1.Node{
			Kind: v1.KindButton, ID: XrefNode(i), Text: r.String(),
			Name: "Go to " + r.String(), Role: "button", Events: []v1.EventKind{v1.EventActivate},
		})
	}
	body.Children = append(body.Children, subtle("Cross-references: OpenBible.info, CC BY"))

	return &v1.Node{Kind: v1.KindColumn, Padding: panelPadding, Gap: 8, Children: []*v1.Node{header, controls, body}}
}

func commentaryNodes(m PanelModel) []*v1.Node {
	switch m.Commentary {
	case CommentaryLoading:
		return []*v1.Node{subtle("Loading Adam Clarke's commentary…")}
	case CommentaryNone:
		return []*v1.Node{subtle("Adam Clarke has no note on this verse.")}
	case CommentaryUnavailable:
		return []*v1.Node{subtle("Commentary unavailable offline.")}
	}
	lines := Wrap(m.Note, panelLineBytes)
	shortened := len(lines) > MaxCommentaryLines
	if shortened {
		lines = lines[:MaxCommentaryLines]
	}
	out := make([]*v1.Node, 0, len(lines)+2)
	for _, l := range lines {
		out = append(out, &v1.Node{Kind: v1.KindText, Text: l})
	}
	if shortened {
		out = append(out, subtle("Shortened. The full note is online."))
	}
	return append(out, subtle("Adam Clarke (1762–1832), public domain"))
}

func iconButton(id, icon, name string, disabled bool) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Key: id, Icon: icon, Name: name, Role: "button",
		Disabled: disabled, Events: []v1.EventKind{v1.EventActivate},
	}
}

func sectionHead(icon, title string) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: icon},
		{Kind: v1.KindText, Text: title, Bold: true},
	}}
}

func subtle(s string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: s, Tone: v1.ToneSubtle, Size: "caption"}
}
