// Package v1 is the language-neutral wire contract between the shell's plugin
// host and a plugin process. It holds the JSON Lines message set, the
// declarative view tree, and the bounds both sides enforce.
//
// The package deliberately imports nothing from the shell. A plugin is an
// ordinary process that speaks JSON on standard output, so the wire, not this
// Go package, is the compatibility surface: the committed JSON fixtures decide
// what version one means, and a plugin written in another language that
// produces the same bytes is equally valid.
//
// Nothing here describes rendering. The tree carries what a plugin means, and
// the host decides what that looks like: no bounds, callbacks, focus indexes,
// IME state, colours outside the theme's own roles, or renderer objects cross
// this boundary.
package v1

import (
	"fmt"
	"math"
)

// The version-one ceilings. They are constants rather than configuration
// because both sides must agree on them without negotiating, and because a
// limit a plugin could raise would not be a limit.
const (
	// MaxNodes counts the root, so a tree is at most MaxNodes nodes in total.
	MaxNodes = 1024
	// MaxDepth counts the root as level one.
	MaxDepth = 16
	// MaxChildren bounds one node's child list.
	MaxChildren = 256
	// MaxTextBytes bounds the text a single node carries.
	MaxTextBytes = 64 << 10
	// MaxIdentBytes bounds node IDs, keys, icon names, accessible names, and
	// roles. These are addresses and labels, not content.
	MaxIdentBytes = 256
	// MaxExtent bounds any logical-pixel measurement on the wire. No output
	// this shell supports is wider than this, so a larger number is a mistake
	// or an attempt to make layout expensive.
	MaxExtent = 8192
)

// NodeKind names a view element. Kinds are strings so that a plugin written in
// any language is self-describing on the wire and an unknown kind produces a
// diagnosable error rather than a silently different element.
type NodeKind string

const (
	KindRow        NodeKind = "row"
	KindColumn     NodeKind = "column"
	KindText       NodeKind = "text"
	KindIcon       NodeKind = "icon"
	KindProgress   NodeKind = "progress"
	KindButton     NodeKind = "button"
	KindTextInput  NodeKind = "text_input"
	KindList       NodeKind = "list"
	KindDragSource NodeKind = "drag_source"
	KindDropZone   NodeKind = "drop_zone"
)

// EventKind names an input event a node declares it can emit. A node receives
// only the kinds it declares, which is what lets the host derive hit testing
// and focus order without asking the plugin.
type EventKind string

const (
	EventActivate EventKind = "activate"
	EventPointer  EventKind = "pointer"
	EventChange   EventKind = "change"
	EventSubmit   EventKind = "submit"
	EventScroll   EventKind = "scroll"
	EventDrop     EventKind = "drop"
)

// Tone selects the semantic theme role a text node paints in. Plugins name
// roles rather than colours so that a plugin cannot break contrast or ignore
// the user's palette.
type Tone string

const (
	ToneNormal Tone = ""
	ToneError  Tone = "error"
)

// ViewKind names where a tree is going to be shown. The same vocabulary is not
// legal everywhere: a bar strip has no keyboard focus and a tooltip is not
// interactive at all, so the kind is part of validation rather than a hint.
type ViewKind string

const (
	ViewBar     ViewKind = "bar"
	ViewTooltip ViewKind = "tooltip"
	ViewPanel   ViewKind = "panel"
)

// Node is one declarative view element.
//
// Every field is data the plugin chose. Arranged bounds, focus state, hover,
// pressed state, the live text buffer, and preedit belong to the host and have
// no representation here.
type Node struct {
	Kind NodeKind `json:"kind"`

	// ID addresses this node in input events. It is required on interactive
	// nodes and must be unique within a view revision.
	ID string `json:"id,omitempty"`
	// Key is stable identity across revisions. The host uses it to keep
	// retained state, such as a live editor buffer, attached to the same
	// element when the surrounding tree changes.
	Key string `json:"key,omitempty"`

	// Text is a label on a button, the content of a text node, and the
	// plugin's view of a field's value.
	Text string `json:"text,omitempty"`
	// Icon names a symbol from the shell's catalogue. It is a name rather than
	// a codepoint or a path so that the shell owns which glyphs exist.
	Icon string `json:"icon,omitempty"`
	// Value is a progress fraction from zero through one.
	Value float64 `json:"value,omitempty"`

	// Tone selects semantic presentation. Size tiers are deliberately absent
	// until the shell has a measure path that can honour them; a field the
	// host would have to ignore is worse than one that is not offered.
	Tone Tone `json:"tone,omitempty"`
	// Tabular requests fixed-advance figures. A countdown sets it: with
	// proportional digits the rendered width changes every second, which
	// visibly shifts everything beside it.
	Tabular bool `json:"tabular,omitempty"`

	// Width fixes a logical width; MaxWidth caps a measured one. Padding and
	// Gap open a container. Zero means natural in every case.
	Width    int `json:"width,omitempty"`
	Height   int `json:"height,omitempty"`
	MaxWidth int `json:"max_width,omitempty"`
	Padding  int `json:"padding,omitempty"`
	Gap      int `json:"gap,omitempty"`

	// DragType is the payload class a drag source offers. Accept is the set a
	// drop zone will take. Payload is the value delivered on drop.
	DragType string   `json:"drag_type,omitempty"`
	Accept   []string `json:"accept,omitempty"`
	Payload  string   `json:"payload,omitempty"`

	// Name and Role are the accessible identity. They are required on every
	// interactive node: the host derives the accessibility tree from the
	// plugin's declaration and has no other source for it.
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
	// Events are the input kinds this node emits. An empty list on an
	// interactive node is a mistake, not a read-only element.
	Events []EventKind `json:"events,omitempty"`

	// Multiline and SubmitOnEnter apply only to text inputs. The host owns the
	// live buffer; Reseed is the generation the plugin uses to replace it.
	Multiline     bool   `json:"multiline,omitempty"`
	SubmitOnEnter bool   `json:"submit_on_enter,omitempty"`
	Reseed        uint64 `json:"reseed,omitempty"`

	Children []*Node `json:"children,omitempty"`
}

// container reports whether a kind holds children.
func (k NodeKind) container() bool {
	return k == KindRow || k == KindColumn || k == KindList || k == KindDropZone
}

// interactive reports whether a kind produces input and therefore needs an
// address and an accessible identity.
func (k NodeKind) interactive() bool {
	return k == KindButton || k == KindTextInput || k == KindDragSource
}

// keyboard reports whether a kind needs keyboard focus to be usable. Bar views
// have no keyboard focus, so placing one there would render a control the user
// could never reach.
func (k NodeKind) keyboard() bool { return k == KindTextInput }

// allowedEvents is the exact event set each kind may declare.
var allowedEvents = map[NodeKind]map[EventKind]bool{
	KindButton:     {EventActivate: true, EventPointer: true},
	KindTextInput:  {EventChange: true, EventSubmit: true},
	KindDragSource: {EventPointer: true, EventDrop: true},
	KindDropZone:   {EventDrop: true},
	KindList:       {EventScroll: true},
}

var knownKinds = map[NodeKind]bool{
	KindRow: true, KindColumn: true, KindText: true, KindIcon: true,
	KindProgress: true, KindButton: true, KindTextInput: true,
	KindList: true, KindDragSource: true, KindDropZone: true,
}

var knownViews = map[ViewKind]bool{ViewBar: true, ViewTooltip: true, ViewPanel: true}

var knownTones = map[Tone]bool{ToneNormal: true, ToneError: true}

// Validate reports whether root is a legal version-one tree for the given view.
//
// It is the only gate between plugin JSON and anything the shell will convert,
// lay out, or paint, so it checks structure, bounds, addressing, accessibility,
// and view-specific vocabulary in one pass. An error names the offending path
// so a plugin author can find the node without instrumenting the host.
func Validate(root *Node, view ViewKind) error {
	if !knownViews[view] {
		return fmt.Errorf("unknown view kind %q", view)
	}
	if root == nil {
		return fmt.Errorf("view has no root node")
	}
	v := &validator{
		view: view,
		ids:  make(map[string]bool),
		keys: make(map[string]bool),
	}
	return v.node(root, "root", 1)
}

type validator struct {
	view  ViewKind
	ids   map[string]bool
	keys  map[string]bool
	nodes int
}

func (v *validator) node(n *Node, path string, depth int) error {
	if n == nil {
		return fmt.Errorf("%s: nil node", path)
	}
	if depth > MaxDepth {
		return fmt.Errorf("%s: tree deeper than %d levels", path, MaxDepth)
	}
	v.nodes++
	if v.nodes > MaxNodes {
		return fmt.Errorf("%s: view holds more than %d nodes", path, MaxNodes)
	}
	if !knownKinds[n.Kind] {
		return fmt.Errorf("%s: unknown kind %q", path, n.Kind)
	}
	if err := v.identity(n, path); err != nil {
		return err
	}
	if err := v.presentation(n, path); err != nil {
		return err
	}
	if err := v.vocabulary(n, path); err != nil {
		return err
	}
	if err := v.events(n, path); err != nil {
		return err
	}

	if !n.Kind.container() && len(n.Children) > 0 {
		return fmt.Errorf("%s: %s takes no children", path, n.Kind)
	}
	if len(n.Children) > MaxChildren {
		return fmt.Errorf("%s: %d children, more than the %d allowed", path, len(n.Children), MaxChildren)
	}
	for i, c := range n.Children {
		if err := v.node(c, fmt.Sprintf("%s.children[%d]", path, i), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// identity checks addressing and accessibility.
func (v *validator) identity(n *Node, path string) error {
	for _, f := range []struct{ what, value string }{
		{"id", n.ID}, {"key", n.Key}, {"name", n.Name}, {"role", n.Role}, {"icon", n.Icon},
	} {
		if len(f.value) > MaxIdentBytes {
			return fmt.Errorf("%s: %s is %d bytes, more than the %d allowed", path, f.what, len(f.value), MaxIdentBytes)
		}
	}
	if n.ID != "" {
		if v.ids[n.ID] {
			return fmt.Errorf("%s: node id %q is already used in this view", path, n.ID)
		}
		v.ids[n.ID] = true
	}
	if n.Key != "" {
		if v.keys[n.Key] {
			return fmt.Errorf("%s: key %q is already used in this view", path, n.Key)
		}
		v.keys[n.Key] = true
	}
	if !n.Kind.interactive() {
		return nil
	}
	if n.ID == "" {
		return fmt.Errorf("%s: %s needs an id to receive input", path, n.Kind)
	}
	if n.Name == "" || n.Role == "" {
		return fmt.Errorf("%s: %s needs an accessible name and role", path, n.Kind)
	}
	return nil
}

// presentation checks the theme roles and every measurement.
func (v *validator) presentation(n *Node, path string) error {
	if !knownTones[n.Tone] {
		return fmt.Errorf("%s: unknown tone %q", path, n.Tone)
	}
	for _, f := range []struct {
		what string
		v    int
	}{{"width", n.Width}, {"height", n.Height}, {"max_width", n.MaxWidth}, {"padding", n.Padding}, {"gap", n.Gap}} {
		if f.v < 0 {
			return fmt.Errorf("%s: %s is negative", path, f.what)
		}
		if f.v > MaxExtent {
			return fmt.Errorf("%s: %s is %d, past the %d limit", path, f.what, f.v, MaxExtent)
		}
	}
	return nil
}

// vocabulary checks the per-kind payload and what this view kind permits.
func (v *validator) vocabulary(n *Node, path string) error {
	if len(n.Text) > MaxTextBytes {
		return fmt.Errorf("%s: text is %d bytes, more than the %d allowed", path, len(n.Text), MaxTextBytes)
	}
	switch n.Kind {
	case KindIcon:
		if err := icon(n.Icon); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case KindButton:
		if n.Icon != "" {
			if err := icon(n.Icon); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	case KindProgress:
		if math.IsNaN(n.Value) || math.IsInf(n.Value, 0) {
			return fmt.Errorf("%s: progress value is not finite", path)
		}
		if n.Value < 0 || n.Value > 1 {
			return fmt.Errorf("%s: progress value %v is outside zero through one", path, n.Value)
		}
	}
	if v.view == ViewTooltip && n.Kind.interactive() {
		return fmt.Errorf("%s: a tooltip is read-only and cannot hold a %s", path, n.Kind)
	}
	if v.view == ViewBar && n.Kind.keyboard() {
		return fmt.Errorf("%s: a bar view has no keyboard focus and cannot hold a %s", path, n.Kind)
	}
	if v.view != ViewPanel && (n.Kind == KindList || n.Kind == KindDragSource || n.Kind == KindDropZone) {
		return fmt.Errorf("%s: a %s view cannot hold a %s", path, v.view, n.Kind)
	}
	if n.Kind == KindDragSource && n.Name == "" {
		return fmt.Errorf("%s: a drag handle needs an accessible name", path)
	}
	if n.Kind != KindTextInput && (n.Multiline || n.SubmitOnEnter || n.Reseed != 0) {
		return fmt.Errorf("%s: %s cannot carry editor flags", path, n.Kind)
	}
	return nil
}

// events checks that a node declares a legal, non-empty, duplicate-free set.
func (v *validator) events(n *Node, path string) error {
	allowed := allowedEvents[n.Kind]
	if len(allowed) == 0 && len(n.Events) > 0 {
		return fmt.Errorf("%s: %s emits no events", path, n.Kind)
	}
	if n.Kind.interactive() && len(n.Events) == 0 {
		return fmt.Errorf("%s: %s declares no events and could never be used", path, n.Kind)
	}
	seen := make(map[EventKind]bool, len(n.Events))
	for _, e := range n.Events {
		if !allowed[e] {
			return fmt.Errorf("%s: %s cannot emit %q", path, n.Kind, e)
		}
		if seen[e] {
			return fmt.Errorf("%s: event %q declared twice", path, e)
		}
		seen[e] = true
	}
	return nil
}

// icon checks that a name addresses the shell's catalogue rather than the
// filesystem. Resolution to a glyph belongs to the host; this only proves the
// name is an identifier and can never be a path.
func icon(name string) error {
	if name == "" {
		return fmt.Errorf("icon has no name")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
		if !ok {
			return fmt.Errorf("icon name %q is not a lower-case identifier", name)
		}
	}
	return nil
}
