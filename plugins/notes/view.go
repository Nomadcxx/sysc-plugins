package notes

import (
	"path/filepath"
	"strconv"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Snapshot is the immutable view the trees render.
type Snapshot struct {
	Page          string
	Notes         []Note
	Current       string
	Buffer        string
	Title         string
	Reseed        uint64
	Status        string
	Words, Chars  int
	PendingDelete string
	Conflict      bool
	SaveErr       string
}

func BarTree() *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindButton, ID: "open", Text: "Notes", Name: "Open notes", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func TooltipTree() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Notes"},
	}}
}

func PanelTree(snap Snapshot) *v1.Node {
	if snap.Page == "editor" {
		return editorTree(snap)
	}
	return listTree(snap)
}

func listTree(snap Snapshot) *v1.Node {
	rows := []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Notes"},
			{Kind: v1.KindButton, ID: "new", Text: "New", Name: "New note", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "scratch", Text: "Scratch", Name: "Open scratchpad", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
	}
	items := make([]*v1.Node, 0, len(snap.Notes)+1)
	for _, n := range snap.Notes {
		label := strings.TrimSuffix(n.Name, filepath.Ext(n.Name))
		pin := "Pin"
		if n.Pinned {
			pin = "Unpin"
		}
		row := []*v1.Node{
			{Kind: v1.KindButton, ID: "open:" + n.Name, Text: label, Name: "Open " + label, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "pin:" + n.Name, Text: pin, Name: pin + " " + label, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}
		if snap.PendingDelete == n.Name {
			row = append(row,
				&v1.Node{Kind: v1.KindButton, ID: "confirm-delete", Text: "Delete", Name: "Confirm delete", Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
				&v1.Node{Kind: v1.KindButton, ID: "cancel", Text: "Cancel", Name: "Cancel", Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
			)
		} else {
			row = append(row, &v1.Node{Kind: v1.KindButton, ID: "rm:" + n.Name, Text: "Delete", Name: "Delete " + label, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}})
		}
		items = append(items, &v1.Node{Kind: v1.KindRow, Gap: 8, Key: "row:" + n.Name, Children: row})
	}
	rows = append(rows, &v1.Node{Kind: v1.KindList, Height: 640, Children: items})
	return &v1.Node{Kind: v1.KindColumn, Gap: 8, Padding: 12, Children: rows}
}

func editorTree(snap Snapshot) *v1.Node {
	children := []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindButton, ID: "back", Text: "Back", Name: "Back to list", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindTextInput, ID: "title", Text: snap.Title, Name: "Title", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
		}},
		{Kind: v1.KindTextInput, ID: "body", Key: "body", Text: snap.Buffer, Name: "Note", Role: "textbox",
			Multiline: true, Reseed: snap.Reseed,
			Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
		{Kind: v1.KindText, Key: "status", Text: snap.Status + " · " + strconv.Itoa(snap.Words) + " words · " + strconv.Itoa(snap.Chars) + " chars"},
	}
	if snap.SaveErr != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: snap.SaveErr, Tone: v1.ToneError})
	}
	if snap.Conflict {
		children = append(children, &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "File changed on disk"},
			{Kind: v1.KindButton, ID: "reload", Text: "Reload", Name: "Reload from disk", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "keep", Text: "Keep local", Name: "Keep local edits", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 8, Padding: 12, Children: children}
}
