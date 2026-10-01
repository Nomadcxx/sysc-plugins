// Package screenshot is the Screenshot plugin: a bar camera and a small panel
// that start the shell's own capture engine through host calls.
package screenshot

import (
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Node ids are the contract between the view and the input handler.
const (
	NodeOpen   = "open"
	NodeClose  = "close"
	NodeRegion = "region"
	NodeWindow = "window"
	NodeScreen = "screen"
	NodeFolder = "folder"
)

// PathRunes bounds the folder caption. The plugin has no text metric, so this
// is a conservative character count for the 360 px panel.
const PathRunes = 36

// Model is everything the panel shows that changes.
type Model struct {
	Directory string
	Error     string
}

// BarTree is one camera button that opens the panel.
func BarTree() *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: NodeOpen, Key: NodeOpen,
		Icon: "camera", Name: "Screenshot", Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
	}}}
}

// TooltipTree is read-only text: the host rejects a control in a tooltip.
func TooltipTree() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Screenshot"},
	}}
}

func row(id, text, icon string) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Key: id, Icon: icon, Text: text,
		Name: text, Role: "button", Events: []v1.EventKind{v1.EventActivate},
	}
}

// PanelTree is the launcher: the four rows in one card over the save folder.
func PanelTree(m Model) *v1.Node {
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 16, Padding: 12, Gap: 8, Children: []*v1.Node{
		row(NodeRegion, "Region", "add"),
		row(NodeWindow, "Window", "web_asset"),
		row(NodeScreen, "Screen", "desktop_windows"),
		row(NodeFolder, "Open folder", "folder_open"),
	}}
	if m.Directory != "" {
		card.Children = append(card.Children, &v1.Node{
			Kind: v1.KindText, Text: ShortenPath(m.Directory, PathRunes),
			Size: "caption", Tone: v1.ToneSubtle,
		})
	}
	children := []*v1.Node{{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Screenshot", Size: "title", Bold: true},
		{Kind: v1.KindButton, ID: NodeClose, Icon: "close", Name: "Close", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
	}}}
	if m.Error != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: m.Error, Tone: v1.ToneError})
	}
	children = append(children, card)
	return &v1.Node{Kind: v1.KindColumn, Padding: 16, Gap: 12, Children: children}
}

// ShortenPath keeps the head and the tail of p within max runes, joined by an
// ellipsis, so the folder's own name stays visible.
func ShortenPath(p string, max int) string {
	r := []rune(p)
	if len(r) <= max {
		return p
	}
	if max <= 0 {
		return ""
	}
	head := (max - 1) / 2
	tail := max - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
