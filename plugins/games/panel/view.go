// Package panel builds the game-deck tree. Pure functions: State in,
// validated v1.Node out; no I/O, no clock reads (Now is a field).
package panel

import (
	"fmt"
	"sort"
	"strings"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/covers"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/store"
)

const maxCards = 60

// Geometry for the 1200x800 panel. The host lays rows out left to right and
// an unsized list takes every remaining pixel, so every box here is explicit:
//
//	content 1168x768 = 1200x800 - 2*pad
//	body    800 list + 16 gap + 352 detail, 768 - 40 header - 16 gap tall
//	grid    5 cards * 148 + 4 gaps * 12 = 788, leaving the scrollbar 12
const (
	pad         = 16
	gap         = 16
	headerH     = 40
	listWidth   = 800
	detailWidth = 352
	bodyHeight  = 712

	cardsPerRow = 5
	cardWidth   = 148
	cardGap     = 12
	cardPad     = 6
	// Lutris coverart is portrait 3:4; 136 is the card less its padding.
	coverW = cardWidth - 2*cardPad
	coverH = 181
	// cardHeight holds the cover and two caption lines. A row measures a
	// button around a column as zero tall unless it is sized.
	cardHeight = 236

	detailCoverW = 240
	detailCoverH = 320
	controlH     = 38
	launchH      = 44
	actionW      = 56
)

// Sections are the segmented-control modes, in display order.
var Sections = []string{"library", "favorites", "playing", "hidden"}

type State struct {
	Now             time.Time
	All             []source.Game
	Prefs           store.Prefs
	Query           string
	Selected        string
	Running         map[string]time.Time
	Launching       map[string]bool // asked to start, not seen running yet
	Failed          map[string]bool // launch never showed up
	Sessions        store.Log
	HideUnavailable bool
	CacheDir        string
	LibraryMissing  bool
}

// Visible returns the filtered, sorted, capped games for the current
// section and query.
func Visible(s State) []source.Game {
	var out []source.Game
	q := strings.ToLower(s.Query)
	for _, g := range s.All {
		hidden := s.Prefs.Hidden[g.ID]
		switch s.Prefs.View {
		case "favorites":
			if !s.Prefs.Favorites[g.ID] || hidden {
				continue
			}
		case "playing":
			if _, ok := s.Running[g.ID]; !ok {
				continue
			}
		case "hidden":
			if !hidden {
				continue
			}
		default: // library
			if hidden {
				continue
			}
			if s.HideUnavailable && !g.Installed {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(g.Name), q) {
			continue
		}
		out = append(out, g)
	}
	switch s.Prefs.Sort {
	case "recent":
		sort.SliceStable(out, func(i, j int) bool { return out[i].LastPlayed.After(out[j].LastPlayed) })
	case "playtime":
		sort.SliceStable(out, func(i, j int) bool { return out[i].PlaytimeSec > out[j].PlaytimeSec })
	default:
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	}
	if len(out) > maxCards {
		out = out[:maxCards]
	}
	return out
}

// BuildTree renders the whole panel. Output passes v1.Validate for ViewPanel.
func BuildTree(s State) *v1.Node {
	// PinEnd keeps the search field on the right edge of the header.
	hdr := &v1.Node{Kind: v1.KindRow, Gap: gap, Height: headerH, PinEnd: true}
	hdr.Children = append(hdr.Children, segmented(s.Prefs.View), searchInput(s.Query))

	list := &v1.Node{Kind: v1.KindList, ID: "game-list", Key: "game-list",
		Width: listWidth, Height: bodyHeight, Gap: cardGap}
	games := Visible(s)
	switch {
	case s.LibraryMissing:
		list.Children = append(list.Children, subtle("Lutris library not found"))
	case len(games) == 0:
		list.Children = append(list.Children, subtle(emptyListText(s)))
	default:
		for i := 0; i < len(games); i += cardsPerRow {
			r := &v1.Node{Kind: v1.KindRow, Gap: cardGap}
			for _, g := range games[i:min(i+cardsPerRow, len(games))] {
				r.Children = append(r.Children, card(s, g))
			}
			list.Children = append(list.Children, r)
		}
	}

	body := &v1.Node{Kind: v1.KindRow, Gap: gap}
	body.Children = append(body.Children, list, detail(s, games))

	root := &v1.Node{Kind: v1.KindColumn, Gap: gap, Padding: pad}
	root.Children = append(root.Children, hdr, body)
	return root
}

func emptyListText(s State) string {
	switch {
	case s.Query != "":
		return "No games match \u201c" + s.Query + "\u201d"
	case s.Prefs.View == "favorites":
		return "No favorites yet"
	case s.Prefs.View == "playing":
		return "Nothing is running"
	case s.Prefs.View == "hidden":
		return "No hidden games"
	}
	return "No games in your Lutris library"
}

func searchInput(q string) *v1.Node {
	return &v1.Node{
		Kind: v1.KindTextInput, ID: "search", Key: "search", Text: q,
		Placeholder: "Search games (F)", Width: 320, Height: controlH, Padding: 10,
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit},
		Name:   "search games", Role: "searchbox",
	}
}

func segmented(view string) *v1.Node {
	if view == "" {
		view = "library"
	}
	// Segments share the control's width evenly; 108 each keeps the labels
	// off one another.
	n := &v1.Node{Kind: v1.KindSegmented, Gap: 4, Height: controlH, Width: 4*108 + 3*4}
	for _, sec := range Sections {
		n.Children = append(n.Children, &v1.Node{
			Kind: v1.KindButton, ID: "section-" + sec, Text: capitalize(sec),
			Selected: sec == view, Events: []v1.EventKind{v1.EventActivate},
			Name: "show " + sec, Role: "tab",
		})
	}
	return n
}

func card(s State, g source.Game) *v1.Node {
	c := &v1.Node{
		Kind: v1.KindButton, ID: "card-" + g.ID, Key: "card-" + g.ID,
		Fill: "card", Shape: "card", Width: cardWidth, Height: cardHeight,
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
		Name:   "select " + g.Name, Role: "option", Tooltip: g.Name,
	}
	// Selected is reserved for segmented tabs; the accent rim marks the card.
	if g.ID == s.Selected {
		c.Stroke, c.StrokeFill = 2, "accent"
		c.Name += ", selected"
	}
	col := &v1.Node{Kind: v1.KindColumn, Gap: 4, Padding: cardPad}
	col.Children = append(col.Children,
		&v1.Node{Kind: v1.KindImage, Path: covers.Resolve(g, s.CacheDir), ImageW: coverW, ImageH: coverH, Radius: 6, Background: true},
		cardTitle(s, g),
		cardCaption(s, g),
	)
	c.Children = append(c.Children, col)
	return c
}

func cardTitle(s State, g source.Game) *v1.Node {
	r := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if _, running := s.Running[g.ID]; running {
		r.Children = append(r.Children, &v1.Node{Kind: v1.KindIcon, Icon: "play_arrow", IconSize: 14, Tone: v1.ToneAccent})
	}
	if s.Prefs.Favorites[g.ID] {
		r.Children = append(r.Children, &v1.Node{Kind: v1.KindIcon, Icon: "star", IconSize: 14, Tone: v1.ToneAccent})
	}
	limit := 18 - 2*(len(r.Children))
	r.Children = append(r.Children, &v1.Node{Kind: v1.KindText, Text: ellipsis(g.Name, limit), Bold: true})
	return r
}

func cardCaption(s State, g source.Game) *v1.Node {
	switch {
	case s.Launching[g.ID]:
		r := &v1.Node{Kind: v1.KindRow, Gap: 6}
		r.Children = append(r.Children,
			&v1.Node{Kind: v1.KindSpinner, Key: "launching-card-" + g.ID, Width: 14},
			&v1.Node{Kind: v1.KindText, Text: "Launching…", Size: "caption", Tone: v1.ToneAccent})
		return r
	case s.Failed[g.ID]:
		return &v1.Node{Kind: v1.KindText, Text: "Launch failed", Size: "caption", Tone: v1.ToneError}
	case !g.Installed:
		return &v1.Node{Kind: v1.KindText, Text: "Not installed", Size: "caption", Tone: v1.ToneSubtle}
	}
	return &v1.Node{Kind: v1.KindText, Text: ellipsis(strings.Join(badges(g), " · "), 22), Size: "caption", Tone: v1.ToneSubtle}
}

func detail(s State, games []source.Game) *v1.Node {
	d := &v1.Node{Kind: v1.KindColumn, ID: "detail", Key: "detail",
		Gap: 12, Padding: 16, Width: detailWidth, Height: bodyHeight, Fill: "card", Shape: "panel"}
	var sel *source.Game
	for i := range games {
		if games[i].ID == s.Selected {
			sel = &games[i]
			break
		}
	}
	if sel == nil {
		for i := range s.All {
			if s.All[i].ID == s.Selected {
				sel = &s.All[i]
				break
			}
		}
	}
	if sel == nil {
		d.Children = append(d.Children,
			&v1.Node{Kind: v1.KindColumn, Height: 200},
			&v1.Node{Kind: v1.KindIcon, Icon: "sports_esports", IconSize: 48, Tone: v1.ToneSubtle, CenterX: true},
			&v1.Node{Kind: v1.KindText, Text: "Select a game", Size: "title", Bold: true, CenterX: true},
			&v1.Node{Kind: v1.KindText, Text: "Click a cover again to launch it", Tone: v1.ToneSubtle, CenterX: true},
		)
		return d
	}
	g := *sel
	title := g.Name
	if g.Year != "" {
		title += " (" + g.Year + ")"
	}
	d.Children = append(d.Children,
		&v1.Node{Kind: v1.KindImage, Path: covers.Resolve(g, s.CacheDir), ImageW: detailCoverW, ImageH: detailCoverH,
			Radius: 10, Background: true, CenterX: true},
		&v1.Node{Kind: v1.KindText, Text: ellipsis(title, 26), Size: "title", Bold: true, Tooltip: title},
		subtle(strings.Join(badges(g), " · ")),
		subtle(fmt.Sprintf("%s · %s", fmtPlaytime(g.PlaytimeSec), fmtLastPlayed(g.LastPlayed, s.Now))),
	)
	if vals := normalized(store.DailyMinutes(store.Log{g.ID: s.Sessions[g.ID]}, 14, s.Now)); len(vals) > 0 {
		d.Children = append(d.Children,
			&v1.Node{Kind: v1.KindText, Text: "Last 14 days", Size: "caption", Tone: v1.ToneSubtle},
			&v1.Node{Kind: v1.KindGraph, ID: "session-graph", Values: vals, Height: 48, Tooltip: "minutes played, last 14 days"})
	} else {
		d.Children = append(d.Children, &v1.Node{Kind: v1.KindText, ID: "session-empty",
			Text: "No play sessions in the last 14 days", Size: "caption", Tone: v1.ToneSubtle})
	}
	d.Children = append(d.Children, primaryButton(s, g), actionRow(s, g))
	return d
}

// primaryButton launches a stopped game and stops a running one, so the one
// action that matters is never behind a menu.
func primaryButton(s State, g source.Game) *v1.Node {
	if _, running := s.Running[g.ID]; running {
		return &v1.Node{Kind: v1.KindButton, ID: "stop-" + g.ID, Text: "Stop", Icon: "stop",
			Fill: "error-container", Height: launchH, Events: []v1.EventKind{v1.EventActivate},
			Name: "stop " + g.Name, Role: "button"}
	}
	if s.Launching[g.ID] {
		// The spinner sits beside a disabled button: Lutris may take tens
		// of seconds, and a second press must not queue a second launch.
		r := &v1.Node{Kind: v1.KindRow, Gap: 12}
		r.Children = append(r.Children,
			&v1.Node{Kind: v1.KindButton, ID: "launch-" + g.ID, Text: "Launching…",
				Fill: "accent", Width: detailWidth - 2*16 - 12 - 28, Height: launchH, Disabled: true,
				Events: []v1.EventKind{v1.EventActivate}, Name: g.Name + " is starting", Role: "button"},
			&v1.Node{Kind: v1.KindSpinner, Key: "launching-detail-" + g.ID, Width: 28})
		return r
	}
	return &v1.Node{Kind: v1.KindButton, ID: "launch-" + g.ID, Text: "Launch", Icon: "play_arrow",
		Fill: "accent", Height: launchH, Disabled: !g.Installed, Events: []v1.EventKind{v1.EventActivate},
		Name: "launch " + g.Name, Role: "button"}
}

func actionRow(s State, g source.Game) *v1.Node {
	btn := func(id, icon, tip, fill string) *v1.Node {
		return &v1.Node{Kind: v1.KindButton, ID: id + "-" + g.ID, Icon: icon, Tooltip: tip,
			Fill: fill, Width: actionW, Height: controlH, Events: []v1.EventKind{v1.EventActivate},
			Name: tip + " " + g.Name, Role: "button"}
	}
	favFill, hideIcon := "soft", "visibility_off"
	if s.Prefs.Favorites[g.ID] {
		favFill = "accent"
	}
	if s.Prefs.Hidden[g.ID] {
		hideIcon = "visibility"
	}
	r := &v1.Node{Kind: v1.KindRow, Gap: 10}
	r.Children = append(r.Children,
		btn("config", "tune", "Configure", "soft"),
		btn("folder", "folder_open", "Open folder", "soft"),
		btn("favtoggle", "star", favLabel(s, g), favFill),
		btn("hidetoggle", hideIcon, hideLabel(s, g), "soft"),
		btn("remove", "delete", "Uninstall", "error-container"),
	)
	return r
}

func favLabel(s State, g source.Game) string {
	if s.Prefs.Favorites[g.ID] {
		return "Unfavorite"
	}
	return "Favorite"
}

func hideLabel(s State, g source.Game) string {
	if s.Prefs.Hidden[g.ID] {
		return "Unhide"
	}
	return "Hide"
}

func badges(g source.Game) []string {
	var b []string
	if g.Runner != "" {
		b = append(b, g.Runner)
	}
	if g.Platform != "" {
		b = append(b, g.Platform)
	}
	if !g.Installed {
		b = append(b, "not installed")
	}
	if len(b) == 0 {
		b = append(b, "lutris")
	}
	return b
}

func fmtPlaytime(sec float64) string {
	if sec <= 0 {
		return "never played"
	}
	h := int(sec) / 3600
	m := int(sec) % 3600 / 60
	if h == 0 {
		return fmt.Sprintf("%dm played", m)
	}
	return fmt.Sprintf("%dh %02dm played", h, m)
}

func fmtLastPlayed(t, now time.Time) string {
	if t.IsZero() {
		return "never launched"
	}
	d := now.Sub(t)
	days := int(d.Hours()) / 24
	switch {
	case d < time.Hour:
		return "played just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("played %dh ago", int(d.Hours()))
	case days < 30:
		return fmt.Sprintf("played %dd ago", days)
	case days < 365:
		return fmt.Sprintf("played %dmo ago", days/30)
	default:
		return fmt.Sprintf("played %dy ago", days/365)
	}
}

// normalized scales minute buckets to zero..one for KindGraph; all-zero or
// empty input returns nil, and the caller shows the Absent box instead.
func normalized(vals []float64) []float64 {
	max := 0.0
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		return nil
	}
	out := make([]float64, len(vals))
	for i, v := range vals {
		out[i] = v / max
	}
	return out
}

func subtle(text string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: text, Tone: v1.ToneSubtle}
}

// ellipsis cuts by rune so a multi-byte name never splits mid-character.
func ellipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
