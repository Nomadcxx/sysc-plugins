package notes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Panel geometry, in logical pixels: a 280px library beside a 432px editor,
// the AI Usage master/detail shape.
const (
	PanelWidth, PanelHeight = 760, 540
	pad, paneGap            = 16, 16
	leftW                   = 280
	rightW                  = PanelWidth - 2*pad - paneGap - leftW // 432
	innerH                  = PanelHeight - 2*pad                  // 508
	ctl                     = 36
	listPad                 = 6
	// Rows stop 12px short of the list's inner edge so its scrollbar never
	// covers a row.
	rowW, rowH = leftW - 2*listPad - 12, 60
	rightGap   = 10
	bannerH    = 52
	footerH    = 24
	// maxRows keeps the library inside the protocol's node budget (a row is
	// six nodes); search narrows anything longer.
	maxRows = 120
)

func BarTree() *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{
		{Kind: v1.KindButton, ID: "open", Icon: "sticky_note_2", Name: "Open Notes", Role: "button", Width: 28, Height: 28, Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func TooltipTree(count int, last, now time.Time) *v1.Node {
	line := "No notes yet"
	if count > 0 {
		line = fmt.Sprintf("%d notes · last edited %s", count, relativeTime(last, now))
		if count == 1 {
			line = "1 note · last edited " + relativeTime(last, now)
		}
	}
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Notes", Bold: true, Size: "label"},
		{Kind: v1.KindText, Text: line, Tone: v1.ToneSubtle, Size: "caption"},
	}}
}

func PanelTree(s Snapshot, clipboard bool) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: pad, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: innerH, Gap: paneGap, Children: []*v1.Node{libraryPane(s, clipboard), editorPane(s)}},
	}}
}

func libraryPane(s Snapshot, clipboard bool) *v1.Node {
	searchW := leftW - ctl - 8
	var tools []*v1.Node
	if clipboard {
		searchW -= ctl + 8
		tools = append(tools, iconButton("clipboard-import", "content_paste", "New note from clipboard", ""))
	}
	tools = append(tools, iconButton("new", "note_add", "New note from the search text", "accent"))
	box := &v1.Node{Kind: v1.KindRow, Height: 44, Gap: 8, Children: append([]*v1.Node{{
		Kind: v1.KindTextInput, ID: "omnibox", Key: "omnibox", Name: "Search", Role: "textbox",
		Placeholder: "Search or create…", Text: s.Query, Width: searchW, Height: 44, Padding: 12,
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit},
	}}, tools...)}
	children := []*v1.Node{box}
	listH := innerH - 44 - 8
	if s.Notice != "" {
		children = append(children, banner("notice", s.Notice, true, leftW, iconButton("notice-dismiss", "close", "Dismiss message", "")))
		listH -= bannerH + 8
	}
	children = append(children, &v1.Node{Kind: v1.KindList, ID: "library", Key: "library", Fill: "card", Radius: 14,
		Padding: listPad, Gap: 6, Height: listH, Children: libraryRows(s)})
	return &v1.Node{Kind: v1.KindColumn, Width: leftW, Gap: 8, Children: children}
}

func isScratch(name string) bool { return strings.HasPrefix(name, "scratchpad.") }

func libraryRows(s Snapshot) []*v1.Node {
	scratchPreview := "Always here"
	var pinned, rest []Summary
	for _, n := range s.Notes {
		switch {
		case isScratch(n.Name):
			if n.Preview != "" {
				scratchPreview = n.Preview
			}
		case n.Favorite:
			pinned = append(pinned, n)
		default:
			rest = append(rest, n)
		}
	}
	rows := []*v1.Node{noteRow("scratch", ScratchpadTitle, scratchPreview, "", isScratch(s.Selected), "edit")}
	if s.ScanError != "" {
		return append(rows, caption("scan-error", s.ScanError, v1.ToneError))
	}
	shown, total := 0, len(pinned)+len(rest)
	add := func(items []Summary) {
		for _, n := range items {
			if shown == maxRows {
				return
			}
			rows = append(rows, noteRow("open:"+Token(n.Name), n.Title, n.Preview, relativeTime(n.Modified, s.Now), n.Name == s.Selected, ""))
			shown++
		}
	}
	if len(pinned) > 0 {
		rows = append(rows, sectionHeader("pinned-heading", fmt.Sprintf("Pinned · %d", len(pinned)), nil))
		add(pinned)
	}
	sort := "Recent"
	if s.SortByName {
		sort = "A–Z"
	}
	rows = append(rows, sectionHeader("notes-heading", fmt.Sprintf("Notes · %d", len(rest)), textButton("sort", sort, "Change note order", "")))
	add(rest)
	switch {
	case total == 0 && strings.TrimSpace(s.Query) != "":
		rows = append(rows, caption("empty-library", "No matches · Enter creates “"+strings.TrimSpace(s.Query)+"”", v1.ToneSubtle))
	case total == 0:
		rows = append(rows, caption("empty-library", "No notes yet. Type above and press Enter.", v1.ToneSubtle))
	case shown < total:
		rows = append(rows, caption("library-more", fmt.Sprintf("Showing %d of %d · search to narrow", shown, total), v1.ToneSubtle))
	}
	return rows
}

// noteRow is one library entry: the title and its age on the first line, a
// preview under it. Rows blend into the list card; only the selection stands
// out, with the AI Usage tint and accent rim.
func noteRow(id, title, preview, age string, selected bool, icon string) *v1.Node {
	row := &v1.Node{Kind: v1.KindButton, ID: id, Key: "row:" + id, Name: accessible("Open " + title), Role: "button",
		Fill: "card", Radius: 10, Padding: 10, Width: rowW, Height: rowH, Events: []v1.EventKind{v1.EventActivate}}
	if selected {
		row.Fill, row.Stroke, row.StrokeFill = "chip", 1, "accent"
	}
	head := []*v1.Node{{Kind: v1.KindText, Text: title, Bold: true, Size: "label"}}
	if icon != "" {
		head = append([]*v1.Node{{Kind: v1.KindIcon, Icon: icon, IconSize: 16, Tone: v1.ToneSubtle}}, head...)
	}
	if age != "" {
		head = append(head, &v1.Node{Kind: v1.KindText, Text: age, Size: "caption", Tone: v1.ToneSubtle, Tabular: true})
	}
	if preview == "" {
		preview = "Empty note"
	}
	row.Children = []*v1.Node{{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: 22, Gap: 6, PinEnd: age != "", Children: head},
		{Kind: v1.KindText, Text: preview, Size: "caption", Tone: v1.ToneSubtle},
	}}}
	return row
}

func sectionHeader(id, label string, action *v1.Node) *v1.Node {
	children := []*v1.Node{{Kind: v1.KindText, ID: id, Text: label, Size: "caption", Tone: v1.ToneSubtle, Bold: true}}
	if action != nil {
		children = append(children, action)
	}
	return &v1.Node{Kind: v1.KindRow, Width: rowW, Height: 32, Padding: 2, PinEnd: action != nil, Children: children}
}

func editorPane(s Snapshot) *v1.Node {
	if s.Selected == "" {
		// Centred by a spacer: the plugin vocabulary has no vertical centring.
		contentH := 20 + 10 + 16 // label, gap, caption
		art := EmptyArtPath()
		if art != "" {
			contentH += 140 + 10
		}
		children := []*v1.Node{{Kind: v1.KindColumn, Height: (innerH - contentH) / 2}}
		if art != "" {
			children = append(children, &v1.Node{Kind: v1.KindImage, Path: art, ImageW: 200, ImageH: 140, Name: "Sticky notes", Role: "img", CenterX: true})
		}
		children = append(children,
			&v1.Node{Kind: v1.KindText, Text: "No note open", Bold: true, Size: "label", CenterX: true},
			&v1.Node{Kind: v1.KindText, Text: "Search, pick a note, or press Enter to start one.", Size: "caption", Tone: v1.ToneSubtle, CenterX: true})
		return &v1.Node{Kind: v1.KindColumn, Width: rightW, Gap: 10, Children: children}
	}
	token := Token(s.Selected)
	favName, favFill := "Favourite", ""
	if s.Favorite {
		favName, favFill = "Unfavourite", "accent"
	}
	children := []*v1.Node{{Kind: v1.KindRow, Height: 44, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindTextInput, ID: "title", Key: "title:" + token, Name: "Note title", Role: "textbox", Text: s.Title,
			Width: rightW - 3*ctl - 3*8, Height: 44, Padding: 12, Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
		iconButton("favorite", "star", favName, favFill),
		iconButton("sticky", "sticky_note_2", "Open as sticky note", ""),
		iconButton("delete", "delete", "Delete note", ""),
	}}}
	banners := 0
	if s.Conflict {
		children = append(children, banner("conflict", "Changed in another app.", false, rightW,
			textButton("reload", "Reload file", "Discard your text and reload the file", ""),
			textButton("keep", "Keep mine", "Overwrite the file with your text", "accent")))
		banners++
	}
	if s.PendingDelete != "" {
		children = append(children, banner("delete-confirm", "Delete this note for good?", true, rightW,
			textButton("cancel", "Cancel", "Keep the note", ""),
			textButton("confirm-delete", "Delete", "Delete the note file", "error")))
		banners++
	}
	bodyH := innerH - 44 - footerH - rightGap*(2+banners) - bannerH*banners
	children = append(children, &v1.Node{Kind: v1.KindTextInput, ID: "body", Key: "editor-body:" + token, Name: "Note text", Role: "textbox",
		Events: []v1.EventKind{v1.EventChange}, Text: s.Body, Height: bodyH, Multiline: true, Padding: 12})
	status, tone := "Saved", v1.ToneSubtle
	switch {
	case s.SaveError != "":
		status, tone = s.SaveError, v1.ToneError
	case s.Dirty:
		status = "Saving…"
	}
	meta := fmt.Sprintf("%d words · edited %s", s.Words, relativeTime(s.Modified, s.Now))
	if s.Words == 1 {
		meta = "1 word · edited " + relativeTime(s.Modified, s.Now)
	}
	metaW := len(meta)*8 + 8
	children = append(children, &v1.Node{Kind: v1.KindRow, Height: footerH, Gap: 8, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindColumn, Width: rightW - metaW - 8, Children: []*v1.Node{{Kind: v1.KindText, ID: "save-state", Text: status, Tone: tone, Size: "caption"}}},
		{Kind: v1.KindText, Text: meta, Size: "caption", Tone: v1.ToneSubtle, Tabular: true},
	}})
	return &v1.Node{Kind: v1.KindColumn, Width: rightW, Gap: rightGap, Children: children}
}

func iconButton(id, icon, name, fill string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Icon: icon, Name: name, Role: "button", Fill: fill, Shape: "circle",
		Width: ctl, Height: ctl, Events: []v1.EventKind{v1.EventActivate}}
}

func textButton(id, text, name, fill string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: text, Name: name, Role: "button", Fill: fill,
		Width: textButtonWidth(text), Height: 28, Padding: 6, Events: []v1.EventKind{v1.EventActivate}}
}

func textButtonWidth(text string) int { return len(text)*8 + 24 }

// banner is a one-line message with trailing actions. The text sits in a
// sized column so a long message clips instead of pushing the actions out.
func banner(id, text string, isError bool, width int, actions ...*v1.Node) *v1.Node {
	// The text takes the fill's paired foreground; an error tone on the error
	// container is pink on red.
	fill := "soft"
	if isError {
		fill = "error-container"
	}
	actionsW := 0
	for i, a := range actions {
		if i > 0 {
			actionsW += 8
		}
		actionsW += a.Width
	}
	group := &v1.Node{Kind: v1.KindRow, Width: actionsW, Height: ctl, Gap: 8, Children: actions}
	return &v1.Node{Kind: v1.KindRow, ID: id, Height: bannerH, Padding: 8, Gap: 8, Radius: 12, Fill: fill, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindColumn, Width: width - 16 - 8 - actionsW, Padding: 6, Children: []*v1.Node{{Kind: v1.KindText, Text: text}}},
		group,
	}}
}

func caption(id, text string, tone v1.Tone) *v1.Node {
	return &v1.Node{Kind: v1.KindText, ID: id, Text: text, Size: "caption", Tone: tone}
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

func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "Just now"
	}
	d := now.Sub(t)
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
			{Kind: v1.KindText, ID: "sticky-layer:" + token, Text: stickyLayerLabel(pinned), Size: "caption", Tone: v1.ToneSubtle},
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

func stickyLayerLabel(on bool) string {
	if on {
		return "Always on top"
	}
	return "Window note"
}
