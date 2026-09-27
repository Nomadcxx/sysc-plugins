package protonvpn

import (
	"fmt"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// ConnectionsState is everything the Connections tab renders from.
type ConnectionsState struct {
	Snap        Snapshot
	Query       string
	QueryReseed uint64
	Countries   []Country
	Expanded    string // country code currently expanded
	Page        int    // zero-based country page
	Flags       bool   // flag emoji spike outcome (true = render flag emoji)
	Notice      string // "Server list unavailable"
}

// The tab lives inside the panel's 12px padding, so its rows span 436; the
// lint tests also call it standalone at the full 460, where everything has
// more room. Text clips, so only the fixed controls are budgeted.
const (
	panelContent  = 460 - 24
	qcWidth       = (panelContent - 3*8) / 4 // four equal shares minus gaps
	searchGap     = 8
	searchInput   = panelContent - searchGap - 40
	listHeight    = 316
	noticeHeight  = 16
	nameMax       = 200 // country name clip; keeps the trailing controls in reach
	cityMax       = 80  // server city clip
	serverNameMax = 170 // server name clip
)

// The shell rejects any view over v1.MaxNodes (1024), so the country list is
// paged and each expanded country shows a bounded slice of its servers.
// Worst case: 20 country rows plus four expanded countries (one manual, three
// auto) at 20 servers each, well under the limit.
const (
	countriesPerPage  = 20
	serversPerCountry = 20
	autoExpandMax     = 3
	pagerHeight       = 36
)

// ConnectionsTree is the Connections tab: quick connect, search, an optional
// outage notice, and the country list with one country expanded.
func ConnectionsTree(s ConnectionsState) *v1.Node {
	root := &v1.Node{Kind: v1.KindColumn, Gap: 4}
	root.Children = append(root.Children, quickConnectRow(s.Snap.Phase), searchRow(s))
	list := &v1.Node{Kind: v1.KindList, Height: listHeight, Gap: 4}
	if s.Notice != "" {
		root.Children = append(root.Children, &v1.Node{
			Kind: v1.KindText, Key: "notice", Text: s.Notice, Tone: v1.ToneSubtle,
		})
		list.Height = listHeight - noticeHeight - 4
	}
	entries := countryEntries(s)
	page, pages := clampPage(s.Page, len(entries))
	list.Children = countryList(s, entries, page)
	if len(list.Children) == 0 && s.Query != "" {
		list.Children = append(list.Children, &v1.Node{
			Kind: v1.KindText, Text: "No matching location", Tone: v1.ToneSubtle,
		})
	}
	root.Children = append(root.Children, list)
	if pages > 1 {
		list.Height -= pagerHeight + 4
		root.Children = append(root.Children, pagerRow(page, pages, len(entries)))
	}
	return root
}

// quickConnectRow is the four one-tap targets, dead while a transition runs.
func quickConnectRow(p Phase) *v1.Node {
	disabled := p == PhaseConnecting || p == PhaseDisconnecting
	row := &v1.Node{Kind: v1.KindRow, Gap: 8}
	for _, qc := range []struct{ id, label string }{
		{"qc:fastest", "Fastest"},
		{"qc:random", "Random"},
		{"qc:p2p", "P2P"},
		{"qc:tor", "Tor"},
	} {
		row.Children = append(row.Children, &v1.Node{
			Kind: v1.KindButton, ID: qc.id, Name: qc.label, Role: "button",
			Text: qc.label, Fill: "soft", Width: qcWidth, Height: 36, Disabled: disabled,
			Events: []v1.EventKind{v1.EventActivate},
		})
	}
	return row
}

// searchRow is the filter field and its clear button. The host owns the live
// buffer under the "search" key; QueryReseed is bumped only to replace it.
func searchRow(s ConnectionsState) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: searchGap, Children: []*v1.Node{
		{Kind: v1.KindTextInput, ID: "search", Key: "search", Name: "Search", Role: "textbox",
			Text: s.Query, Placeholder: "Search country or server", Reseed: s.QueryReseed,
			Width: searchInput, Height: 40,
			Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
		{Kind: v1.KindButton, ID: "clear-search", Name: "Clear search", Role: "button",
			Icon: "close", Fill: "soft", Width: 40, Height: 40, Disabled: s.Query == "",
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

// countryEntry is one country in the filtered list, with the servers to show
// when it is expanded.
type countryEntry struct {
	country  Country
	expanded bool
	servers  []Server
}

// countryEntries filters the countries against the query and decides which
// are expanded. A country-name or code match keeps the manual expansion
// state; a server-name match surfaces its country auto-expanded with only
// the matching servers, capped so a broad query cannot blow the node budget.
func countryEntries(s ConnectionsState) []countryEntry {
	q := strings.ToLower(s.Query)
	var entries []countryEntry
	auto := 0
	for _, c := range s.Countries {
		countryMatch := q != "" &&
			(strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.Code), q))
		var serverMatches []Server
		for _, srv := range c.Servers {
			if q != "" && strings.Contains(strings.ToLower(srv.Name), q) {
				serverMatches = append(serverMatches, srv)
			}
		}
		if q != "" && !countryMatch && len(serverMatches) == 0 {
			continue
		}
		autoExpand := q != "" && !countryMatch && len(serverMatches) > 0 && auto < autoExpandMax
		if autoExpand {
			auto++
		}
		e := countryEntry{country: c, expanded: s.Expanded == c.Code || autoExpand}
		if e.expanded {
			e.servers = c.Servers
			if autoExpand {
				e.servers = serverMatches
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// countryList renders one page of entries, interleaving the expanded
// country's server rows.
func countryList(s ConnectionsState, entries []countryEntry, page int) []*v1.Node {
	start := page * countriesPerPage
	end := min(start+countriesPerPage, len(entries))
	var rows []*v1.Node
	for _, e := range entries[start:end] {
		rows = append(rows, countryRow(s, e.country, e.expanded))
		if e.expanded {
			rows = append(rows, serversColumn(e.country, e.servers))
		}
	}
	return rows
}

// clampPage keeps the requested page inside the entry count.
func clampPage(page, entries int) (int, int) {
	pages := (entries + countriesPerPage - 1) / countriesPerPage
	if pages < 1 {
		pages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	return page, pages
}

// CountryPages is how many pages the current filter needs; the session
// clamps its page against this before stepping.
func CountryPages(s ConnectionsState) int {
	_, pages := clampPage(0, len(countryEntries(s)))
	return pages
}

// pagerRow steps through the country pages.
func pagerRow(page, pages, total int) *v1.Node {
	first := page*countriesPerPage + 1
	last := min((page+1)*countriesPerPage, total)
	// PinEnd reserves the tail only for a two-child row, so the count
	// travels with the previous button and next holds the right edge.
	return &v1.Node{Kind: v1.KindRow, Gap: 8, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindButton, ID: "page:prev", Name: "Previous page", Role: "button",
				Icon: "chevron_left", Fill: "soft", Width: 36, Height: 32, Disabled: page == 0,
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindText, Text: fmt.Sprintf("%d–%d of %d", first, last, total),
				Tone: v1.ToneSubtle, Tabular: true},
		}},
		{Kind: v1.KindButton, ID: "page:next", Name: "Next page", Role: "button",
			Icon: "chevron_right", Fill: "soft", Width: 36, Height: 32, Disabled: page >= pages-1,
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

// countryRow is one list entry: flag or code badge, name over the server
// summary, then expand and connect pinned to the end.
func countryRow(s ConnectionsState, c Country, expanded bool) *v1.Node {
	row := &v1.Node{
		Kind: v1.KindRow, ID: "country:" + c.Code, Fill: "card", Radius: 10,
		Padding: 8, Height: 52, Gap: 8, PinEnd: true,
	}
	if s.Snap.Phase == PhaseConnected && s.Snap.Status.Country == c.Code {
		row.Fill = "container"
	}
	if c.Maintenance {
		row.Tone = v1.ToneSubtle
		row.Tooltip = c.Name + " is under maintenance"
	}
	name := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: c.Name, Bold: true, MaxWidth: nameMax},
	}}
	meta := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if c.Maintenance {
		meta.Children = append(meta.Children, &v1.Node{
			Kind: v1.KindText, Text: "Under maintenance", Tone: v1.ToneSubtle, MaxWidth: nameMax,
		})
	} else {
		meta.Children = append(meta.Children,
			&v1.Node{Kind: v1.KindText, Text: serverSummary(c), Tone: v1.ToneSubtle, MaxWidth: nameMax},
			loadProgress(c))
	}
	name.Children = append(name.Children, meta)
	// PinEnd is a two-child contract: the engine reserves the trailing
	// child's width only for a [leading, trailing] row, while a zero-width
	// column inside any PinEnd row swallows the whole remainder and pushes
	// later siblings out of the box. Group glyph plus text as the lead and
	// the two controls as the tail, the way the shell's device rows do.
	leading := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		flagGlyph(c, s.Flags), name,
	}}
	trailing := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		expandButton(c, expanded), connectButton(c),
	}}
	row.Children = []*v1.Node{leading, trailing}
	return row
}

// flagGlyph is the regional-indicator emoji when the spike passed, else a
// chip capsule carrying the two-letter code.
func flagGlyph(c Country, flags bool) *v1.Node {
	if flags && len(c.Code) == 2 {
		ok := true
		rs := make([]rune, 0, 2)
		for i := 0; i < 2; i++ {
			r := rune(c.Code[i])
			if r < 'A' || r > 'Z' {
				ok = false
				break
			}
			rs = append(rs, 0x1F1E6+r-'A')
		}
		if ok {
			return &v1.Node{Kind: v1.KindText, Text: string(rs)}
		}
	}
	return &v1.Node{
		Kind: v1.KindRow, Fill: "chip", Shape: "stadium", Width: 36, Height: 28,
		Children: []*v1.Node{{Kind: v1.KindText, Text: c.Code, Bold: true}},
	}
}

// serverSummary is the "N servers · L%" line, singular for one.
func serverSummary(c Country) string {
	label := "servers"
	if len(c.Servers) == 1 {
		label = "server"
	}
	return fmt.Sprintf("%d %s · %d%%", len(c.Servers), label, c.Load)
}

// loadProgress is the 48×6 load bar; tone escalates as the country fills up.
func loadProgress(c Country) *v1.Node {
	load := c.Load
	if load < 0 {
		load = 0
	}
	if load > 100 {
		load = 100
	}
	tone := v1.ToneNormal
	switch {
	case load > 90:
		tone = v1.ToneError
	case load > 75:
		tone = v1.ToneAccent
	}
	return &v1.Node{
		Kind: v1.KindProgress, ID: "load:" + c.Code, Width: 48, Height: 6,
		Value: float64(load) / 100, Tone: tone,
	}
}

func expandButton(c Country, expanded bool) *v1.Node {
	verb := "Expand"
	if expanded {
		verb = "Collapse"
	}
	return &v1.Node{
		Kind: v1.KindButton, ID: "expand:" + c.Code, Name: verb + " " + c.Name, Role: "button",
		Icon: "expand_more", Width: 28, Height: 28,
		Events: []v1.EventKind{v1.EventActivate},
	}
}

func connectButton(c Country) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: "connect:" + c.Code, Name: "Connect " + c.Name, Role: "button",
		Text: "Connect", Fill: "accent", Width: 84, Height: 32, Disabled: c.Maintenance,
		Events: []v1.EventKind{v1.EventActivate},
	}
}

// serversColumn is the indented server list under an expanded country,
// capped so a country with thousands of servers stays inside the node budget.
func serversColumn(c Country, servers []Server) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Padding: 8, Gap: 4}
	shown := servers
	if len(shown) > serversPerCountry {
		shown = shown[:serversPerCountry]
	}
	for _, srv := range shown {
		col.Children = append(col.Children, serverRow(srv))
	}
	if len(servers) > len(shown) {
		col.Children = append(col.Children, &v1.Node{
			Kind: v1.KindText, Tone: v1.ToneSubtle,
			Text: fmt.Sprintf("Showing %d of %d servers — search to narrow", len(shown), len(servers)),
		})
	}
	return col
}

// serverRow is one expanded entry: dns glyph, name over city · load ·
// feature tags, connect pinned to the end. Down servers dim and refuse.
func serverRow(srv Server) *v1.Node {
	row := &v1.Node{
		Kind: v1.KindRow, ID: "server:" + srv.Name, Padding: 8, Height: 50, Gap: 8, PinEnd: true,
	}
	meta := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if srv.City != "" {
		meta.Children = append(meta.Children, &v1.Node{
			Kind: v1.KindText, Text: srv.City, Tone: v1.ToneSubtle, MaxWidth: cityMax,
		})
	}
	meta.Children = append(meta.Children, &v1.Node{
		Kind: v1.KindText, Text: fmt.Sprintf("%d%%", srv.Load), Tabular: true,
	})
	meta.Children = append(meta.Children, featureIcons(srv.Features)...)
	lead := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: srv.Name, MaxWidth: serverNameMax},
		meta,
	}}
	connect := &v1.Node{
		Kind: v1.KindButton, ID: "server-connect:" + srv.Name, Name: "Connect " + srv.Name, Role: "button",
		Text: "Connect", Fill: "accent", Width: 72, Height: 28, Disabled: !srv.Up,
		Events: []v1.EventKind{v1.EventActivate},
	}
	if !srv.Up {
		row.Tone = v1.ToneSubtle
		connect.Tooltip = srv.Name + " is under maintenance"
	}
	// Two-child PinEnd, as in countryRow: the dns glyph joins the text
	// column in the lead, the connect button is the whole tail.
	row.Children = []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "dns", Tone: v1.ToneSubtle}, lead,
		}},
		connect,
	}
	return row
}

// featureIcons maps the feature bitmask onto catalogue glyphs, subtle so they
// read as tags rather than actions.
func featureIcons(features int) []*v1.Node {
	var icons []*v1.Node
	for _, f := range []struct {
		bit  int
		name string
	}{
		{FeatSecureCore, "security"},
		{FeatTor, "visibility_off"},
		{FeatP2P, "lan"},
		{FeatStreaming, "play_arrow"},
	} {
		if features&f.bit != 0 {
			icons = append(icons, &v1.Node{Kind: v1.KindIcon, Icon: f.name, Tone: v1.ToneSubtle})
		}
	}
	return icons
}
