package vpn

import (
	"fmt"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const searchH = 40

func page(children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: panelPad, Gap: gap, Children: children}
}

// header leads every sub-page: back, the page's title, and an optional chip
// at the right edge.
func header(back, title string, trailing *v1.Node) *v1.Node {
	lead := row(gap, iconButton("back", back, "chevron_left", control, control, false),
		column(0, spacer(1, 6), &v1.Node{Kind: v1.KindText, Text: title, Size: "title", Bold: true, MaxWidth: content - 2*control - 160}))
	if trailing == nil {
		return &v1.Node{Kind: v1.KindRow, Gap: gap, Height: control, Children: []*v1.Node{lead}}
	}
	return &v1.Node{Kind: v1.KindRow, Gap: gap, Height: control, PinEnd: true, Children: []*v1.Node{lead, trailing}}
}

// connectedChip answers "where am I connected now?" on the location page.
func connectedChip(v ViewState) *v1.Node {
	s := v.Machine.State(v.Machine.Selected)
	if !shown(s, v.Now) || s.Observed.Phase != Connected {
		return nil
	}
	label := "Connected"
	if f := flagText(countryCode(v, s.Observed.Country)); f != "" {
		label = f + "  " + label
	}
	return lowered(chip("", label, "soft"), chipWidth("", label), 6)
}

func searchField(v ViewState) *v1.Node {
	// The host draws the magnifier and the clear control on a field named
	// "Search", so the plugin adds neither.
	return &v1.Node{Kind: v1.KindTextInput, ID: "search", Key: "search", Name: "Search", Role: "textbox", Text: v.Query, Reseed: v.SearchSeed,
		Placeholder: "Search country, city or relay", Width: content, Height: searchH, Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}}
}

// locationTitle names where a drilled-in list is, and what back climbs to.
func locationTitle(v ViewState) (title, back string) {
	if v.Query != "" || v.Country == "" {
		if v.Query != "" {
			return "Choose location", "Clear the search"
		}
		return "Choose location", "Back to the home screen"
	}
	country, city := v.Country, v.City
	for _, l := range v.Locations {
		if l.Country == v.Country {
			country = l.CountryName
			if l.City == v.City {
				city = l.CityName
			}
		}
	}
	if v.City != "" {
		return country + " · " + city, "Back to " + country
	}
	return country, "Back to all countries"
}

func locationsPage(v ViewState) *v1.Node {
	title, back := locationTitle(v)
	root := page(header(back, title, connectedChip(v)), searchField(v))
	height := bodyH - control - gap - searchH - gap
	problem := v.Notice
	if problem == "" && v.Machine.Selected == Mullvad {
		problem = v.LocationsError
	}
	if problem != "" {
		n := note(problem, v1.ToneError, content, false)
		root.Children = append(root.Children, n)
		height -= n.Height + gap
	}
	items := list(height)
	root.Children = append(root.Children, items)
	if !v.LocationsLoaded {
		items.Children = append(items.Children, infoCard("language", "Loading locations…", "Refresh from the home screen if this", "takes too long."))
		return root
	}
	choices := LocationChoices(v)
	if len(choices) == 0 {
		items.Children = append(items.Children, infoCard("search", "No locations match", "Try a country, city or relay name."))
		return root
	}
	start, end := pageBounds(v.Page, len(choices))
	for _, l := range choices[start:end] {
		items.Children = append(items.Children, locationItem(v, l))
	}
	if len(choices) > PageSize {
		items.Children = append(items.Children, wrap(pager(v.Page, len(choices))))
	}
	return root
}

func pager(page, count int) *v1.Node {
	start, end := pageBounds(page, count)
	return &v1.Node{Kind: v1.KindRow, Gap: gap, Width: rowW, PinEnd: true, Children: []*v1.Node{
		row(gap, iconButton("page:prev", "Previous page", "chevron_left", control, 32, start == 0),
			&v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d–%d of %d", start+1, end, count), Tone: v1.ToneSubtle, Tabular: true}),
		iconButton("page:next", "Next page", "chevron_right", control, 32, end >= count),
	}}
}

// counts reports the cities and relays under a country, or the relays under
// a city, from the parsed relay list.
func counts(all []Location, l Location) (cities, relays int) {
	seen := map[string]bool{}
	for _, x := range all {
		if x.Country == l.Country && (l.City == "" || x.City == l.City) {
			relays++
			if !seen[x.City] {
				seen[x.City] = true
				cities++
			}
		}
	}
	return cities, relays
}

// locationItem is one whole-row button that connects (or switches) to the
// location, beside a chevron that opens its cities or relays: the Mullvad
// app's tap-to-connect with the Network panel's row.
func locationItem(v ViewState, l Location) *v1.Node {
	o := v.Machine.State(v.Machine.Selected).Observed
	connected := o.Phase == Connected
	name, detail := l.CountryName, ""
	glyph := flag(l.Country, "headline", 36)
	current := connected && (o.Country == l.CountryName || strings.EqualFold(o.Country, l.Country))
	cities, relays := counts(v.Locations, l)
	switch {
	case l.Host != "":
		name, detail = l.Host, l.CityName+", "+l.CountryName
		glyph = &v1.Node{Kind: v1.KindColumn, Width: 36, Children: []*v1.Node{{Kind: v1.KindIcon, Icon: "dns", Tone: v1.ToneSubtle, CenterX: true}}}
		current = connected && o.Exit == l.Host
	case l.City != "":
		name, detail = l.CityName, plural(relays, "relay", "relays")
		if v.Query != "" {
			detail += " · " + l.CountryName
		}
		current = current && o.City == l.CityName
	default:
		detail = plural(cities, "city", "cities") + " · " + plural(relays, "relay", "relays")
	}
	lead := row(13, glyph, column(2, &v1.Node{Kind: v1.KindText, Text: name, Bold: true, MaxWidth: 200}, caption(detail, v1.ToneSubtle)))
	width := rowW
	var chevron *v1.Node
	if l.Host == "" {
		chevron = &v1.Node{Kind: v1.KindButton, ID: "browse:" + l.ID(), Name: "Show locations in " + name, Role: "button", Icon: "chevron_right",
			Fill: "card", Shape: "medium", Width: 40, Height: 56, Events: []v1.EventKind{v1.EventActivate}}
		width -= 40 + 4
	}
	var main *v1.Node
	if current {
		main = item("", "", lead, lowered(chip("check", "Connected", "soft"), chipWidth("check", "Connected"), 6), width, false)
		main.Stroke, main.StrokeFill = 1, "accent"
	} else {
		label, spoken := "Connect", "Connect to "+name
		if connected {
			label, spoken = "Switch", "Switch to "+name
		}
		main = item("location:"+l.ID(), spoken, lead, lowered(caption(label, v1.ToneNormal), 56, 9), width,
			mutationDisabled(v) || v.LocationsError != "" || otherProblem(v) != "")
	}
	main.Shape, main.Height, main.Padding = "medium", 56, 10
	if chevron == nil {
		return wrap(main)
	}
	return wrap(main, chevron)
}

func dnsDetail(v ViewState) string {
	dns := v.Settings.DNS
	switch {
	case dns == nil:
		return "Unavailable from the " + v.Machine.Selected.Name() + " CLI."
	case dns.Mode == "custom":
		return "Off while custom DNS is set."
	case dns.Mode == "system":
		return "Off while system DNS is used."
	}
	return v.Machine.Selected.Name() + " DNS drops these domains before they load."
}

// blocker is one DNS content blocker as a Material filter chip: tonal with a
// check when on, outlined when off, or the same look as status when nothing
// can change it.
func blocker(i int, value *bool, width int, editable, disabled bool) *v1.Node {
	name := BlockerNames[i]
	on := value != nil && *value
	if !editable {
		fill := "outline"
		if on {
			fill = "soft"
		}
		t := &v1.Node{Kind: v1.KindText, Text: name, CenterX: true}
		return sized(width, 32, &v1.Node{Kind: v1.KindColumn, Fill: fill, Shape: "small", Width: width, Height: 32, Padding: 7, Stroke: 1, StrokeFill: "outline", Children: []*v1.Node{t}})
	}
	_, spoken := onOff(value)
	b := button(fmt.Sprintf("set:dns:%d", i), name, "outline", width, 32, disabled || value == nil)
	b.Shape, b.Name = "small", "Block "+strings.ToLower(name)+", "+spoken
	if on {
		b.Fill, b.Icon = "soft", "check"
	} else {
		b.Stroke, b.StrokeFill = 1, "outline"
	}
	return b
}

func dnsPage(v ViewState) *v1.Node {
	editable := settingsEditable(v)
	dns := v.Settings.DNS
	off := dns == nil || dns.Mode != "default"
	disabled := mutationDisabled(v) || v.SettingsError != "" || off
	explain, tone := dnsDetail(v), v1.ToneSubtle
	if v.SettingsError != "" && !strings.HasPrefix(v.SettingsError, "Refreshing") {
		explain, tone = v.SettingsError, v1.ToneError
	}
	inner := content - 2*13
	chipW := (inner - 2*6) / 3
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 24, Padding: 13, Gap: 9, Width: content, Children: []*v1.Node{note(explain, tone, inner, false)}}
	if !v.SettingsLoaded {
		card.Children = []*v1.Node{note("Loading settings…", v1.ToneSubtle, inner, false)}
		return page(header("Back to the home screen", "DNS blocking", nil), card)
	}
	for start := 0; start < len(BlockerNames); start += 3 {
		r := row(6)
		for i := start; i < start+3; i++ {
			var value *bool
			if dns != nil {
				value = &dns.Blocks[i]
			}
			r.Children = append(r.Children, blocker(i, value, chipW, editable, disabled))
		}
		card.Children = append(card.Children, r)
	}
	if !editable {
		card.Children = append(card.Children, caption("Change these in the Obscura app", v1.ToneSubtle))
	}
	return page(header("Back to the home screen", "DNS blocking", nil), card)
}

// stateChip shows a setting's value as status. Text stays in the normal tone;
// a check marks On.
func stateChip(value *bool, width int) *v1.Node {
	shown, _ := onOff(value)
	on := value != nil && *value
	v := row(4, &v1.Node{Kind: v1.KindText, Text: shown, Bold: on})
	if on {
		v.Children = append([]*v1.Node{icon("check", v1.ToneAccent)}, v.Children...)
	}
	v.CenterX = true
	return sized(width, 28, &v1.Node{Kind: v1.KindColumn, Fill: "chip", Shape: "stadium", Width: width, Height: 28, Padding: 6, Children: []*v1.Node{v}})
}

func statusRow(label, detail string, value *bool) *v1.Node {
	r := item("", "", labelled(label, detail), stateChip(value, 72), content, false)
	r.Height, r.Shape = 60, "medium"
	return r
}

func bypassPage(v ViewState) *v1.Node {
	s := v.Settings
	return page(header("Back to the home screen", "Bypass", nil),
		statusRow("Tailscale bypass", "Let Tailscale traffic skip the tunnel", s.Tailscale),
		statusRow("WireGuard bypass", "Let other WireGuard tunnels skip it", s.WireGuard),
		caption("Change these in the Obscura app", v1.ToneSubtle))
}

func confirmPage(v ViewState) *v1.Node {
	title, glyph := "Local network", "lan"
	question := "Allow local network access?"
	detail := []string{"Devices on your network can reach this", "computer while the VPN is on."}
	label, fill := "Allow access", "accent"
	if v.Confirm == "lockdown-mode" {
		title, glyph = "Lockdown", "lock"
		question = "Turn off lockdown mode?"
		detail = []string{"Traffic can leave unprotected whenever", "the VPN is disconnected."}
		label, fill = "Turn off", "error"
	}
	inner := content - 2*13
	bw := (inner - gap) / 2
	q := &v1.Node{Kind: v1.KindText, Text: question, Size: "title", Bold: true, CenterX: true}
	l1, l2 := text(detail[0], v1.ToneSubtle), text(detail[1], v1.ToneSubtle)
	l1.CenterX, l2.CenterX = true, true
	cancel := button("cancel", "Cancel", "chip", bw, 44, false)
	cancel.Shape = "stadium"
	confirm := button("confirm", label, fill, bw, 44, mutationDisabled(v))
	confirm.Shape = "stadium"
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 24, Padding: 13, Gap: 8, Width: content, Children: []*v1.Node{
		spacer(1, 4), centred(disc(72, sizedIcon(glyph, 36, v1.ToneAccent), "soft"), 72, inner), spacer(1, 2),
		q, column(2, l1, l2), spacer(1, 6), row(gap, cancel, confirm),
	}}
	return page(header("Cancel and go back", title, nil), card)
}
