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

// Sections are the segmented-control modes, in display order.
var Sections = []string{"library", "favorites", "playing", "hidden"}

type State struct {
	Now             time.Time
	All             []source.Game
	Prefs           store.Prefs
	Query           string
	Selected        string
	Actions         bool // detail pane shows the action column
	Running         map[string]time.Time
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
	hdr := &v1.Node{Kind: v1.KindRow, Gap: 8}
	hdr.Children = append(hdr.Children, segmented(s.Prefs.View), searchInput(s.Query))

	list := &v1.Node{Kind: v1.KindList, ID: "game-list", Key: "game-list"}
	games := Visible(s)
	switch {
	case s.LibraryMissing:
		list.Children = append(list.Children, subtle("Lutris library not found"))
	case len(games) == 0:
		list.Children = append(list.Children, subtle("Nothing here yet"))
	default:
		for i := 0; i < len(games); i += 2 {
			r := &v1.Node{Kind: v1.KindRow, Gap: 8}
			r.Children = append(r.Children, card(s, games[i]))
			if i+1 < len(games) {
				r.Children = append(r.Children, card(s, games[i+1]))
			}
			list.Children = append(list.Children, r)
		}
	}

	body := &v1.Node{Kind: v1.KindRow, Gap: 12}
	body.Children = append(body.Children, list, detail(s, games))

	root := &v1.Node{Kind: v1.KindColumn, Gap: 12, Padding: 12}
	root.Children = append(root.Children, hdr, body)
	return root
}

func searchInput(q string) *v1.Node {
	return &v1.Node{
		Kind: v1.KindTextInput, ID: "search", Key: "search", Text: q,
		Placeholder: "search (f)", Width: 220,
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit},
		Name:   "search games", Role: "searchbox",
	}
}

func segmented(view string) *v1.Node {
	if view == "" {
		view = "library"
	}
	n := &v1.Node{Kind: v1.KindSegmented}
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
	fill := "card"
	if g.ID == s.Selected {
		fill = "accent"
	}
	c := &v1.Node{
		Kind: v1.KindButton, ID: "card-" + g.ID, Key: "card-" + g.ID,
		Fill: fill, Shape: "card", Width: 188,
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
		Name:   "select " + g.Name, Role: "option", Tooltip: g.Name,
	}
	col := &v1.Node{Kind: v1.KindColumn, Gap: 4, Padding: 6}
	col.Children = append(col.Children,
		&v1.Node{Kind: v1.KindImage, Path: covers.Resolve(g, s.CacheDir), ImageW: 174, ImageH: 100, Radius: 6, Background: true},
		cardTitle(s, g),
	)
	c.Children = append(c.Children, col)
	return c
}

func cardTitle(s State, g source.Game) *v1.Node {
	r := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if s.Prefs.Favorites[g.ID] {
		r.Children = append(r.Children, &v1.Node{Kind: v1.KindText, Text: "\u2605", Tone: v1.ToneAccent})
	}
	if _, running := s.Running[g.ID]; running {
		r.Children = append(r.Children, &v1.Node{Kind: v1.KindIcon, Icon: "play_arrow", IconSize: 14})
	}
	name := g.Name
	tone := v1.ToneNormal
	if s.Failed[g.ID] {
		name += " — launch failed"
		tone = v1.ToneError
	}
	r.Children = append(r.Children,
		&v1.Node{Kind: v1.KindText, Text: ellipsis(name, 22), Bold: true, Tone: tone},
		&v1.Node{Kind: v1.KindText, Text: g.Runner, Tone: v1.ToneSubtle, Size: "caption"},
	)
	return r
}

func detail(s State, games []source.Game) *v1.Node {
	d := &v1.Node{Kind: v1.KindColumn, ID: "detail", Key: "detail",
		Gap: 8, Padding: 8, Width: 250, Fill: "container", Shape: "panel"}
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
		d.Children = append(d.Children, subtle("Pick a game"))
		return d
	}
	g := *sel
	title := g.Name
	if g.Year != "" {
		title += " (" + g.Year + ")"
	}
	d.Children = append(d.Children,
		&v1.Node{Kind: v1.KindImage, Path: covers.Resolve(g, s.CacheDir), ImageW: 230, ImageH: 320, Radius: 6, Background: true},
		&v1.Node{Kind: v1.KindText, Text: title, Size: "title", Bold: true},
		subtle(strings.Join(badges(g), " · ")),
		subtle(fmt.Sprintf("%s · %s", fmtPlaytime(g.PlaytimeSec), fmtLastPlayed(g.LastPlayed, s.Now))),
	)
	if vals := normalized(store.DailyMinutes(s.Sessions, 14, s.Now)); len(vals) > 0 {
		d.Children = append(d.Children, &v1.Node{Kind: v1.KindGraph, ID: "session-graph", Values: vals, Height: 48, Tooltip: "minutes played, last 14 days"})
	} else {
		d.Children = append(d.Children, &v1.Node{Kind: v1.KindGraph, ID: "session-graph", Absent: true, Height: 48})
	}
	if s.Actions {
		d.Children = append(d.Children, actionButtons(s, g)...)
	} else {
		launch := &v1.Node{Kind: v1.KindButton, ID: "launch-" + g.ID, Text: "Launch", Icon: "play_arrow",
			Events: []v1.EventKind{v1.EventActivate}, Name: "launch " + g.Name, Role: "button"}
		if _, running := s.Running[g.ID]; running {
			launch.Text = "Playing"
			launch.Disabled = true
		}
		d.Children = append(d.Children, launch, &v1.Node{
			Kind: v1.KindButton, ID: "more-" + g.ID, Text: "More", Icon: "expand_more",
			Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
			Name:   "more actions for " + g.Name, Role: "button",
		})
	}
	return d
}

func actionButtons(s State, g source.Game) []*v1.Node {
	btn := func(text, icon, id string) *v1.Node {
		return &v1.Node{Kind: v1.KindButton, ID: id, Text: text, Icon: icon,
			Events: []v1.EventKind{v1.EventActivate}, Name: text + " " + g.Name, Role: "button"}
	}
	var out []*v1.Node
	if _, running := s.Running[g.ID]; running {
		out = append(out, btn("Stop", "stop", "stop-"+g.ID))
	} else {
		out = append(out, btn("Launch", "play_arrow", "launch-"+g.ID))
	}
	out = append(out,
		btn("Configure", "tune", "config-"+g.ID),
		btn("Open folder", "folder_open", "folder-"+g.ID),
		btn(favLabel(s, g), "", "favtoggle-"+g.ID),
		btn(hideLabel(s, g), "visibility", "hidetoggle-"+g.ID),
		btn("Uninstall", "delete", "remove-"+g.ID),
	)
	back := btn("Back", "chevron_left", "detail-back")
	back.Name = "back to game view"
	out = append(out, back)
	return out
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
		return "no session"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
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

func ellipsis(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
