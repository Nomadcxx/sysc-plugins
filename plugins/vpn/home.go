package vpn

import (
	"fmt"
	"path/filepath"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Home screen geometry: the top row, the status card, the 2×2 tiles and the
// location row. Tile height leaves room for one caption under the grid when
// the provider's settings are read-only.
const (
	heroH     = 272
	heroInner = content - 2*13
	discD     = 80
	detailH   = 2*16 + 2
	actionH   = 48
	tileW     = (content - gap) / 2
	tileH     = 60
	navH      = 60
)

func home(v ViewState) *v1.Node {
	root := &v1.Node{Kind: v1.KindColumn, Padding: panelPad, Gap: gap, Children: []*v1.Node{topRow(v), hero(v)}}
	if !v.Machine.State(v.Machine.Selected).Installed {
		return root
	}
	root.Children = append(root.Children, tiles(v))
	if !settingsEditable(v) {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: "Change these in the Obscura app", Tone: v1.ToneSubtle, Size: "caption", Height: 16})
	}
	root.Children = append(root.Children, locationRow(v))
	return root
}

func refreshButton() *v1.Node {
	return iconButton("refresh", "Refresh status", "refresh", control, control, false)
}

// topRow is the provider switch when both CLIs are installed (or neither, so
// both setup notes stay reachable), else the installed provider's lockup.
// Either way the refresh control ends it.
func topRow(v ViewState) *v1.Node {
	if sw := providerSwitch(v); sw != nil {
		return row(gap, sw, refreshButton())
	}
	return &v1.Node{Kind: v1.KindRow, Gap: gap, Height: control, PinEnd: true, Children: []*v1.Node{lockup(v), refreshButton()}}
}

// lockup names the one installed provider: Mullvad's colour badge beside its
// tinted wordmark, or Obscura's wordmark. Without shipped marks the name is
// set in text.
func lockup(v ViewState) *v1.Node {
	p := v.Machine.Selected
	h := 20
	if p == Mullvad {
		h = 15
	}
	mark, w := wordmark(v, p, h)
	if mark == nil {
		return column(0, spacer(1, 6), &v1.Node{Kind: v1.KindText, Text: p.Name(), Size: "title", Bold: true})
	}
	lead := mark
	if p == Mullvad {
		badge := &v1.Node{Kind: v1.KindImage, Path: filepath.Join(v.BrandDir, "mullvad-badge.png"), ImageSize: 28, Name: "Mullvad", Role: "img"}
		lead = row(8, sized(28, 28, badge), column(0, spacer(1, (28-h)/2), mark))
		w += 28 + 8
		h = 28
	}
	return &v1.Node{Kind: v1.KindColumn, Width: w, Height: control, Children: []*v1.Node{spacer(1, (control-h)/2), lead}}
}

// providerSwitch carries the provider wordmarks; the selected segment keeps
// Material's check, and a provider whose tunnel is up shows the shield so a
// conflict is visible before Connect explains it.
func providerSwitch(v ViewState) *v1.Node {
	if v.Machine.State(Mullvad).Installed != v.Machine.State(Obscura).Installed {
		return nil
	}
	var segs []*v1.Node
	for _, p := range []Provider{Mullvad, Obscura} {
		b := segment("provider:"+string(p), p.Name(), p == v.Machine.Selected)
		mark := ""
		if b.Selected {
			mark = "check"
		}
		if s := v.Machine.State(p); shown(s, v.Now) && s.Observed.Phase == Connected {
			mark, b.Name = "verified_user", p.Name()+", connected"
		}
		h := 17
		if p == Mullvad {
			h = 14
		}
		if wm, _ := wordmark(v, p, h); wm != nil {
			inner := row(6, wm)
			if mark != "" {
				inner.Children = append([]*v1.Node{sizedIcon(mark, 16, v1.ToneNormal)}, inner.Children...)
			}
			inner.CenterX = true
			// The spacer centres the mark: a segment lays its children from
			// the top.
			b.Text, b.Children = "", []*v1.Node{column(0, spacer(1, (control-16)/2), inner)}
		} else {
			b.Icon = mark
		}
		segs = append(segs, b)
	}
	return &v1.Node{Kind: v1.KindSegmented, Width: content - control - gap, Height: control, Gap: 2, Children: segs}
}

// statusCard is the hero's content for the selected provider's state.
type statusCard struct {
	glyph      string
	tone       v1.Tone
	word       string
	spinner    bool
	place      string
	code       string
	detail     string
	detailTone v1.Tone
	mono       bool
}

func ipv4(ip string) string {
	if strings.Contains(ip, ":") {
		return ""
	}
	return ip
}

// tunnelLine is relay · IPv4 · protocol, dropping the protocol and then the
// IP until it fits the card at the mono metric.
func tunnelLine(o Observation) string {
	parts := nonempty(o.Exit, ipv4(o.IP), o.Transport)
	for len(parts) > 1 && len(strings.Join(parts, " · "))*8 > heroInner {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, " · ")
}

// message is the one line the home screen owes the user, or "": action
// failures and blockers win over the state's own explanation.
func message(v ViewState) (string, v1.Tone) {
	m := v.Machine
	s := m.State(m.Selected)
	switch {
	case m.ActionError != "":
		return m.ActionError, v1.ToneError
	case v.Notice != "":
		return v.Notice, v1.ToneError
	case !s.Installed:
		return "", v1.ToneNormal
	case s.ReadError != "" && s.HasObservation && !strings.HasPrefix(s.ReadError, "Refreshing"):
		return s.ReadError, v1.ToneError
	case m.Busy || m.Pending != "":
		return "", v1.ToneNormal
	}
	if problem := otherProblem(v); problem != "" && PrimaryAction(v).Kind == "connect" {
		return problem, v1.ToneNormal
	}
	if s.Observed.Detail != "" && shown(s, v.Now) {
		tone := v1.ToneSubtle
		if s.Observed.Phase == TunnelError {
			tone = v1.ToneError
		}
		return s.Observed.Detail, tone
	}
	return "", v1.ToneNormal
}

func status(v ViewState) statusCard {
	m := v.Machine
	p := m.Selected
	s := m.State(p)
	o := s.Observed
	glyph, tone, word := look(v, p)
	c := statusCard{glyph: glyph, tone: tone, word: word, detailTone: v1.ToneSubtle}
	if !s.Installed {
		c.word = "Set up " + p.Name()
		c.detail = "Install the " + string(p) + " CLI, sign in with the " + p.Name() + " app, then refresh."
		if v.Notice != "" {
			c.detail, c.detailTone = v.Notice, v1.ToneError
		}
		return c
	}
	t := m.Transition(p)
	live := shown(s, v.Now)
	c.spinner = t != "" || live && (o.Phase == Connecting || o.Phase == Disconnecting)
	switch {
	case (live || t == Connecting) && (o.City != "" || o.Country != "") && o.Phase != Disconnected:
		c.place, c.code = place(v, o.City, o.Country), countryCode(v, o.Country)
	case live && o.Phase == Disconnected || t != "":
		if p == Mullvad {
			c.place, c.code = savedPlace(v)
		}
		if c.place == "" {
			c.place = "Automatic"
		}
	case s.HasObservation:
		c.place, c.code = place(v, o.City, o.Country), countryCode(v, o.Country)
	}
	if msg, msgTone := message(v); msg != "" {
		c.detail, c.detailTone = msg, msgTone
		return c
	}
	switch {
	case c.spinner:
	case !live:
		c.detail = "Waiting for status"
	case o.Phase == Connected:
		c.detail, c.mono = tunnelLine(o), true
	case o.Phase == Disconnected && o.Traffic == TrafficBlocked:
		c.detail = "No traffic leaves this computer"
	case o.Phase == Disconnected:
		c.detail = "Your traffic is not protected"
	}
	return c
}

// hero is the status card: a tonal disc, the state word, the place with its
// flag, one detail line and the action, centred on a large card.
func hero(v ViewState) *v1.Node {
	c := status(v)
	fill := "chip"
	if c.tone == v1.ToneAccent {
		fill = "soft"
	}
	d := disc(discD, sizedIcon(c.glyph, 44, c.tone), fill)
	if c.spinner {
		spin := &v1.Node{Kind: v1.KindSpinner, Key: "vpn-transition", Width: 32, CenterX: true}
		d = sized(discD, discD, &v1.Node{Kind: v1.KindColumn, Width: discD, Height: discD, Radius: discD / 2, Fill: fill, Padding: (discD - 32) / 2, Children: []*v1.Node{spin}})
	}
	stateTone := c.tone
	if stateTone == v1.ToneSubtle {
		stateTone = v1.ToneNormal
	}
	word := &v1.Node{Kind: v1.KindText, Text: c.word, Size: "headline", Bold: true, Tone: stateTone, CenterX: true, Height: 32}
	where := &v1.Node{Kind: v1.KindColumn, Height: 28}
	if c.place != "" {
		label := &v1.Node{Kind: v1.KindText, Text: c.place, Size: "title", MaxWidth: heroInner - 34}
		r := row(8, label)
		if f := flagText(c.code); f != "" {
			r.Children = append([]*v1.Node{flag(c.code, "title", 26)}, r.Children...)
		}
		r.CenterX = true
		where.Children = []*v1.Node{r}
	}
	detail := &v1.Node{Kind: v1.KindColumn, Height: detailH}
	switch {
	case c.detail == "":
	case c.mono:
		detail.Children = []*v1.Node{{Kind: v1.KindText, Text: c.detail, Size: "mono", Tone: v1.ToneSubtle, CenterX: true, MaxWidth: heroInner}}
	default:
		detail.Children = []*v1.Node{note(c.detail, c.detailTone, heroInner, true)}
	}
	action := actionButton(v)
	if action == nil {
		action = spacer(1, actionH)
	}
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 24, Padding: 13, Gap: 6, Height: heroH, Children: []*v1.Node{
		centred(d, discD, heroInner), word, where, detail, action}}
}

// actionButton morphs with the phase: Connect, Cancel mid-flight,
// Disconnect once up, Refresh when the status cannot be read.
func actionButton(v ViewState) *v1.Node {
	m := v.Machine
	s := m.State(m.Selected)
	if !s.Installed {
		return nil
	}
	phase := s.Observed.Phase
	t := m.Transition(m.Selected)
	if t == "" && !shown(s, v.Now) {
		b := button("retry-read", "Refresh", "soft", heroInner, actionH, m.Busy)
		b.Icon, b.Shape, b.Name = "refresh", "stadium", "Refresh status"
		return b
	}
	disabled := mutationDisabled(v) || phase == Disconnecting
	b := button("action", "Connect", "accent", heroInner, actionH, disabled)
	b.Icon, b.Shape = "power_settings_new", "stadium"
	switch {
	case t == Connecting:
		b.Text, b.Name = "Connecting…", "Connecting"
	case t == Disconnecting:
		b.Text, b.Name, b.Fill = "Disconnecting…", "Disconnecting", "error-container"
	case phase == Connecting:
		b.Text, b.Name, b.Fill = "Cancel", "Cancel connection", "soft"
	case PrimaryAction(v).Kind == "disconnect" || phase == Disconnecting:
		b.Text, b.Name, b.Fill = "Disconnect", "Disconnect", "error-container"
	case otherProblem(v) != "":
		b.Disabled = true
	}
	return b
}

// tile is one quick setting: a glyph beside its name and state word. A tile
// that opens a page carries a chevron; one with nothing behind it is status.
type tile struct {
	id, glyph, name, value string
	on, opens, busy        bool
}

func (t tile) node(interactive, disabled bool) *v1.Node {
	var g *v1.Node
	if t.busy {
		g = &v1.Node{Kind: v1.KindSpinner, Key: "vpn-tile-" + t.id, Width: 18}
	} else {
		tone := v1.ToneNormal
		if t.on {
			tone = v1.ToneAccent
		}
		g = sizedIcon(t.glyph, 22, tone)
	}
	lead := row(12, &v1.Node{Kind: v1.KindColumn, Width: 22, Height: 36, Children: []*v1.Node{spacer(1, 7), g}},
		column(2, bold(t.name), caption(t.value, v1.ToneSubtle)))
	content := lead
	if t.opens {
		content = &v1.Node{Kind: v1.KindRow, Gap: gap, PinEnd: true, Children: []*v1.Node{lead, sizedIcon("chevron_right", 20, v1.ToneSubtle)}}
	}
	fill := "card"
	if t.on {
		fill = "soft"
	}
	if !interactive {
		return &v1.Node{Kind: v1.KindColumn, Fill: fill, Shape: "large", Width: tileW, Height: tileH, Padding: 12, Children: []*v1.Node{content}}
	}
	kind := "toggle"
	if t.opens {
		kind = "opens settings"
	}
	return &v1.Node{Kind: v1.KindButton, ID: t.id, Name: t.name + ", " + strings.ToLower(t.value) + ", " + kind, Role: "button", Fill: fill, Shape: "large",
		Width: tileW, Height: tileH, Padding: 12, Disabled: disabled, Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{content}}
}

func dnsValue(v ViewState) (string, bool) {
	dns := v.Settings.DNS
	switch {
	case !v.SettingsLoaded:
		return "…", false
	case dns == nil:
		return "—", false
	case dns.Mode != "default":
		return "Off", false
	}
	n := 0
	for _, on := range dns.Blocks {
		if on {
			n++
		}
	}
	if n == 0 {
		return "Off", false
	}
	return fmt.Sprintf("%d of %d", n, len(dns.Blocks)), true
}

func bypassValue(v ViewState) (string, bool) {
	if !v.SettingsLoaded {
		return "…", false
	}
	var on []string
	if b := v.Settings.Tailscale; b != nil && *b {
		on = append(on, "Tailscale")
	}
	if b := v.Settings.WireGuard; b != nil && *b {
		on = append(on, "WireGuard")
	}
	switch len(on) {
	case 0:
		return "Off", false
	case 1:
		return on[0] + " on", true
	}
	return "Tailscale, WireGuard", true
}

// toggle is a setting tile's value word and state.
func toggle(v ViewState, value *bool, on, off string) (string, bool) {
	switch {
	case !v.SettingsLoaded:
		return "…", false
	case value == nil:
		return "—", false
	case *value:
		return on, true
	}
	return off, false
}

// tiles is the 2×2 quick-settings grid. Toggles flip in place (Local
// network on and Lockdown off ask first); DNS and Bypass open their pages,
// which stay reachable even when the settings are read-only.
func tiles(v ViewState) *v1.Node {
	s := v.Settings
	editable := settingsEditable(v)
	disabled := mutationDisabled(v) || v.SettingsError != "" || !v.SettingsLoaded
	acting := ""
	if a, ok := v.Machine.ActiveAction(); ok && a.Kind == "setting" {
		acting = a.Key
	}
	setting := func(key, glyph, name string, value *bool, on, off string) *v1.Node {
		word, lit := toggle(v, value, on, off)
		t := tile{id: "set:" + key, glyph: glyph, name: name, value: word, on: lit, busy: acting == key}
		return t.node(editable, disabled || value == nil)
	}
	page := func(id, glyph, name, value string, lit bool) *v1.Node {
		busy := id == "open:dns" && strings.HasPrefix(acting, "dns:")
		return tile{id: id, glyph: glyph, name: name, value: value, on: lit, opens: true, busy: busy}.node(true, false)
	}
	dns, dnsOn := dnsValue(v)
	third := setting("lockdown-mode", "lock", "Lockdown", s.Lockdown, "On", "Off")
	if v.Machine.Selected == Obscura {
		bypass, bypassOn := bypassValue(v)
		third = page("open:bypass", "lan", "Bypass", bypass, bypassOn)
	}
	return column(gap,
		row(gap, setting("auto-connect", "restart_alt", "Auto-connect", s.AutoConnect, "On", "Off"),
			setting("lan", "lan", "Local network", s.LAN, "Allowed", "Blocked")),
		row(gap, third, page("open:dns", "security", "DNS blocking", dns, dnsOn)))
}

// locationRow names where the tunnel goes and opens the location page. A
// provider that cannot choose locations gets the same row as status.
func locationRow(v ViewState) *v1.Node {
	p := v.Machine.Selected
	o := v.Machine.State(p).Observed
	connected := shown(v.Machine.State(p), v.Now) && o.Phase == Connected
	where, code := "", ""
	switch {
	case !locationsSupported(v):
		where = "Automatic · Obscura picks the exit"
		if connected {
			code = countryCode(v, o.Country)
		}
	case connected:
		where, code = place(v, o.City, o.Country), countryCode(v, o.Country)
	case p == Mullvad:
		where, code = savedPlace(v)
	}
	if where == "" {
		where = "Automatic"
	}
	lead := row(13, flag(code, "headline", 36), column(2, caption("Location", v1.ToneSubtle),
		&v1.Node{Kind: v1.KindText, Text: where, Bold: true, MaxWidth: content - 2*12 - 36 - 13 - 22 - gap}))
	if !locationsSupported(v) {
		return &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "large", Width: content, Height: navH, Padding: 12, Children: []*v1.Node{lead}}
	}
	body := &v1.Node{Kind: v1.KindRow, Gap: gap, PinEnd: true, Children: []*v1.Node{lead, sizedIcon("chevron_right", 22, v1.ToneSubtle)}}
	return &v1.Node{Kind: v1.KindButton, ID: "open:locations", Name: "Location, " + where + ", opens the location list", Role: "button",
		Fill: "card", Shape: "large", Width: content, Height: navH, Padding: 12, Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{body}}
}
