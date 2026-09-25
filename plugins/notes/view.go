package notes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	FillCard     = "card"
	FillSoft     = "soft"
	maxNoteCards = 60
)

func BarTree() *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{
		{Kind: v1.KindButton, ID: "open", Text: "Notes", Name: "Open Notes", Role: "button", Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func TooltipTree() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Notes", Bold: true, Size: "label"},
		{Kind: v1.KindText, Text: "Open your Markdown library", Tone: v1.ToneSubtle},
	}}
}

func PanelTree(s Snapshot) *v1.Node {
	if s.Editing {
		return editorTree(s)
	}
	capture := input("capture", "capture", "Capture a thought…", s.CaptureText, 52, true)
	children := []*v1.Node{
		&v1.Node{Kind: v1.KindRow, Key: "header", Height: 42, Gap: 8, Children: []*v1.Node{
			text("title", "Notes", "headline", false),
			button("new", "+  New note", "Create a blank note", "accent"),
		}},
		text("subtitle", "Your ideas, saved as Markdown", "caption", true),
		&v1.Node{Kind: v1.KindRow, Key: "capture-card", Fill: FillCard, Radius: 16, Padding: 12, Gap: 10, Children: []*v1.Node{
			capture,
			button("capture-save", "Save", "Capture note", "accent"),
		}},
		&v1.Node{Kind: v1.KindRow, Key: "scratchpad", Fill: FillSoft, Radius: 14, Padding: 10, Gap: 8, Children: []*v1.Node{
			column("scratch-info", 40, 0, 2,
				text("scratch-title", "Scratchpad", "label", false),
				text("scratch-subtitle", "Always here · autosaves", "caption", true)),
			button("scratch", "Open", "Open Scratchpad", ""),
		}},
		input("search", "search", "Search titles and notes…", s.Query, 48, false),
		&v1.Node{Kind: v1.KindRow, Height: 42, Children: []*v1.Node{
			text("library-label", fmt.Sprintf("LIBRARY  ·  %d", len(s.Notes)), "caption", true),
			button("sort", sortLabel(s.SortByName), "Change note ordering", ""),
		}},
	}
	if s.LibraryError != "" {
		children = append(children, notice("library-error", s.LibraryError, true))
	} else if len(s.Notes) == 0 {
		copy := "Create your first note or capture a thought above."
		if s.Query != "" {
			copy = "No notes match this search. Try a shorter phrase."
		}
		children = append(children, notice("empty-library", copy, false))
	} else {
		var favorites, recent []Summary
		for _, note := range s.Notes {
			if note.Favorite {
				favorites = append(favorites, note)
			} else {
				recent = append(recent, note)
			}
		}
		children = append(children, text("favorites-heading", fmt.Sprintf("FAVORITES  ·  %d", len(favorites)), "caption", true))
		if len(favorites) == 0 {
			children = append(children, text("favorites-empty", "Favorite a note to keep it close", "caption", true))
		} else {
			children = append(children, noteList("favorite-notes", favorites, 170))
			if len(favorites) > maxNoteCards {
				children = append(children, text("favorites-more", fmt.Sprintf("Showing %d of %d · search to narrow", maxNoteCards, len(favorites)), "caption", true))
			}
		}
		children = append(children, text("recent-heading", fmt.Sprintf("RECENT  ·  %d", len(recent)), "caption", true))
		if len(recent) == 0 {
			children = append(children, text("recent-empty", "No other notes yet", "caption", true))
		} else {
			children = append(children, noteList("recent-notes", recent, 210))
			if len(recent) > maxNoteCards {
				children = append(children, text("recent-more", fmt.Sprintf("Showing %d of %d · search to narrow", maxNoteCards, len(recent)), "caption", true))
			}
		}
	}
	return &v1.Node{Kind: v1.KindList, ID: "library", Key: "library", Height: 800, Padding: 16, Gap: 10, Children: children}
}

func noteList(id string, items []Summary, height int) *v1.Node {
	rows := make([]*v1.Node, 0, min(len(items), maxNoteCards))
	for _, note := range items[:min(len(items), maxNoteCards)] {
		rows = append(rows, noteCard(note))
	}
	return &v1.Node{Kind: v1.KindList, ID: id, Key: id, Height: height, Gap: 8, Children: rows}
}

func noteCard(note Summary) *v1.Node {
	favoriteName, favoriteText := "Favorite "+note.Title, "☆"
	if note.Favorite {
		favoriteText = "★"
	}
	preview := note.Preview
	if preview == "" {
		preview = "No preview yet"
	}
	token := Token(note.Name)
	return &v1.Node{Kind: v1.KindColumn, Key: "note:" + token, Fill: FillCard, Radius: 14, Padding: 10, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: 42, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindButton, ID: "open:" + token, Text: note.Title, Width: 240, MaxWidth: 220, Height: 36, Padding: 6, Name: accessible("Open note " + note.Title), Role: "button", Events: []v1.EventKind{v1.EventActivate}},
			button("fav:"+token, favoriteText, accessible(favoriteName), ""),
		}},
		text("preview:"+token, preview, "body", false),
		{Kind: v1.KindRow, Height: 42, Gap: 6, Children: []*v1.Node{
			text("modified:"+token, relativeTime(note.Modified), "caption", true),
			button("sticky:"+token, "Sticky", accessible("Open "+note.Title+" as a sticky note"), "soft"),
		}},
	}}
}

func editorTree(s Snapshot) *v1.Node {
	status := "Saved to your notes folder"
	statusTone := v1.ToneSubtle
	if s.Dirty {
		status = "Unsaved changes · autosaving"
	}
	if s.SaveError != "" {
		status, statusTone = s.SaveError, v1.ToneError
	}
	children := []*v1.Node{
		{Kind: v1.KindRow, Height: 44, Gap: 8, Children: []*v1.Node{
			button("back", "←  Library", "Back to Notes library", ""),
			text("editor-heading", "Note", "title", false),
		}},
		input("title", "title:"+Token(s.Current), "Note title", s.Title, 52, false),
		{Kind: v1.KindRow, Height: 42, Gap: 8, Children: []*v1.Node{
			button("favorite-current", favoriteLabel(s.Pinned), "Toggle favorite", ""),
			button("sticky-current", "Open sticky", "Open this note as a sticky note", "soft"),
			button("delete-current", "Delete", "Delete this note", ""),
		}},
		&v1.Node{Kind: v1.KindTextInput, ID: "body", Key: "editor-body:" + Token(s.Current), Name: "Markdown note body", Role: "textbox", Events: []v1.EventKind{v1.EventChange}, Text: s.Body, Height: 470, Multiline: true},
		{Kind: v1.KindText, ID: "save-state", Text: status, Tone: statusTone, Size: "caption"},
	}
	if s.LibraryError != "" {
		children = append(children, notice("library-error", s.LibraryError, true))
	}
	if s.Conflict {
		children = append(children, notice("conflict", "This note changed in another app. Choose which copy to keep.", true),
			&v1.Node{Kind: v1.KindRow, Height: 42, Gap: 8, Children: []*v1.Node{
				button("reload", "Reload file", "Discard local text and reload file", ""),
				button("keep", "Keep my text", "Overwrite file with local text", "accent"),
			}})
	}
	if s.PendingDelete != "" {
		children = append(children, notice("delete-confirm", "Delete this Markdown file? This cannot be undone.", true),
			&v1.Node{Kind: v1.KindRow, Height: 42, Gap: 8, Children: []*v1.Node{
				button("cancel", "Cancel", "Cancel delete", ""),
				button("confirm-delete", "Delete note", "Confirm delete note", "error"),
			}})
	}
	return &v1.Node{Kind: v1.KindList, ID: "editor", Key: "editor", Height: 800, Padding: 16, Gap: 10, Children: children}
}

func StickyTree(doc Document, color string, pinned bool) *v1.Node {
	colors := []struct{ id, label string }{{"sun", "Sunshine"}, {"mint", "Mint"}, {"sky", "Sky"}, {"rose", "Rose"}, {"lilac", "Lilac"}}
	buttons := make([]*v1.Node, 0, len(colors))
	token := Token(doc.Name)
	for _, c := range colors {
		fill := stickyFill(c.id)
		label := c.label + " note color"
		mark := "●"
		if c.id == color {
			fill = "accent"
			label += " (selected)"
			mark = "✓"
		}
		buttons = append(buttons, &v1.Node{Kind: v1.KindButton, ID: "color:" + token + ":" + c.id, Text: mark, Fill: fill, Width: 34, Height: 34, Name: label, Role: "button", Events: []v1.EventKind{v1.EventActivate}})
	}
	return &v1.Node{Kind: v1.KindColumn, ID: "sticky:" + token, Key: "sticky:" + token, Padding: 14, Gap: 10, Fill: stickyFill(color), Radius: 16, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: 38, Gap: 4, Children: buttons},
		&v1.Node{Kind: v1.KindTextInput, ID: "sticky-body:" + token, Key: "sticky-body:" + token, Name: "Sticky note Markdown body", Role: "textbox", Events: []v1.EventKind{v1.EventChange}, Text: doc.Body, Height: 230, Multiline: true},
		{Kind: v1.KindRow, Height: 28, Gap: 8, Children: []*v1.Node{
			text("sticky-layer:"+token, stickyLayerLabel(pinned), "caption", true),
			{Kind: v1.KindText, ID: "sticky-status:" + token, Text: stickyStatus(doc), Tone: stickyTone(doc), Size: "caption"},
		}},
	}}
}

func stickyFill(color string) string {
	switch color {
	case "mint":
		return "note-mint"
	case "sky":
		return "note-sky"
	case "rose":
		return "note-rose"
	case "lilac":
		return "note-lilac"
	default:
		return "note-sun"
	}
}

func Token(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:12])
}

func accessible(value string) string {
	if len(value) <= v1.MaxIdentBytes {
		return value
	}
	limit := v1.MaxIdentBytes
	for limit > 0 && value[limit]&0xc0 == 0x80 {
		limit--
	}
	if limit == 0 {
		return "Note action"
	}
	return value[:limit]
}

func stickyStatus(d Document) string {
	if d.Error != "" {
		return d.Error
	}
	if d.Conflict != "" {
		return "Changed outside Notes · local text kept"
	}
	if d.Dirty {
		return "Saving…"
	}
	return "Saved · Markdown"
}

func stickyTone(d Document) v1.Tone {
	if d.Error != "" || d.Conflict != "" {
		return v1.ToneError
	}
	return v1.ToneSubtle
}
func conditionalFill(on bool) string {
	if on {
		return "accent"
	}
	return ""
}
func pinLabel(on bool) string {
	if on {
		return "Pinned"
	}
	return "Pin"
}
func stickyLayerLabel(on bool) string {
	if on {
		return "Always on top"
	}
	return "Window note"
}
func favoriteLabel(on bool) string {
	if on {
		return "★  Favorited"
	}
	return "☆  Favorite"
}
func sortLabel(byName bool) string {
	if byName {
		return "Name ↑"
	}
	return "Recent ↓"
}

func text(id, value, size string, subtle bool) *v1.Node {
	n := &v1.Node{Kind: v1.KindText, ID: id, Text: value, Size: size}
	if subtle {
		n.Tone = v1.ToneSubtle
	}
	return n
}

func button(id, value, name, fill string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: value, Name: name, Role: "button", Events: []v1.EventKind{v1.EventActivate}, Fill: fill, Height: 36, Padding: 8}
}

func input(id, key, placeholder, value string, height int, multiline bool) *v1.Node {
	return &v1.Node{Kind: v1.KindTextInput, ID: id, Key: key, Text: value, Placeholder: placeholder, Name: placeholder, Role: "textbox", Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}, Height: height, Multiline: multiline}
}

func column(id string, height, padding, gap int, children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, ID: id, Height: height, Padding: padding, Gap: gap, Children: children}
}

func notice(id, value string, errorState bool) *v1.Node {
	tone := v1.ToneSubtle
	fill := FillSoft
	if errorState {
		tone, fill = v1.ToneError, "error-container"
	}
	return &v1.Node{Kind: v1.KindColumn, ID: id, Fill: fill, Radius: 12, Padding: 14, Children: []*v1.Node{{Kind: v1.KindText, Text: value, Tone: tone}}}
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "Just now"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "Just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2 Jan")
	}
}

func filepathExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}
