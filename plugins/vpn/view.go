package vpn

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// PageSize bounds one page of location rows. The scroll list carries the
// page; paging exists only to keep a broad search under v1.MaxNodes.
const PageSize = 20

// Panel geometry follows the shell's own ladder (theme.Metrics, default
// density): panel padding 13, card padding and gaps 9, standard control 36,
// list item 48. Columns never refuse an overflow, so every page budgets its
// height here rather than discovering it from the host.
const (
	panelH   = 580
	panelPad = 13
	gap      = 9
	content  = 460 - 2*panelPad
	bodyH    = panelH - 2*panelPad
	control  = 36
	itemH    = 48
	// gutter keeps list rows clear of the scrollbar the host paints inside a
	// list's right edge; rows sit in a row wrapper, which honours a width.
	gutter = 8
	rowW   = content - gutter
)

// Screens of the panel. The home screen answers "am I protected, where and
// through whom"; the others open from it and lead with a back control.
const (
	ScreenHome      = "home"
	ScreenLocations = "locations"
	ScreenDNS       = "dns"
	ScreenBypass    = "bypass"
)

type ViewState struct {
	Machine              *Machine
	Now                  time.Time
	Screen               string
	Query, Country, City string
	Page                 int
	Locations            []Location
	LocationsLoaded      bool
	LocationsError       string
	Settings             Settings
	SettingsLoaded       bool
	SettingsError        string
	Confirm, Notice      string
	SearchSeed           uint64
	Obscura              ObscuraCaps
	// BrandDir holds the provider wordmarks; empty falls back to names in
	// text.
	BrandDir string
}

func text(s string, tone v1.Tone) *v1.Node { return &v1.Node{Kind: v1.KindText, Text: s, Tone: tone} }
func bold(s string) *v1.Node               { return &v1.Node{Kind: v1.KindText, Text: s, Bold: true} }
func caption(s string, tone v1.Tone) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: s, Tone: tone, Size: "caption"}
}
func icon(name string, tone v1.Tone) *v1.Node {
	return &v1.Node{Kind: v1.KindIcon, Icon: name, Tone: tone}
}
func sizedIcon(name string, size int, tone v1.Tone) *v1.Node {
	return &v1.Node{Kind: v1.KindIcon, Icon: name, IconSize: size, Tone: tone}
}

func button(id, label, fill string, width, height int, disabled bool) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Name: label, Role: "button", Text: label, Width: width, Height: height, Fill: fill, Disabled: disabled, Events: []v1.EventKind{v1.EventActivate}}
}

// iconButton is Material's standard icon button: an icon-only plugin button
// with no fill rests flat, as the shell's own do.
func iconButton(id, name, glyph string, width, height int, disabled bool) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Name: name, Role: "button", Icon: glyph, Width: width, Height: height, Disabled: disabled, Events: []v1.EventKind{v1.EventActivate}}
}
func row(gap int, children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: gap, Children: children}
}
func column(gap int, children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: gap, Children: children}
}
func spacer(w, h int) *v1.Node { return &v1.Node{Kind: v1.KindColumn, Width: w, Height: h} }

// sized wraps a node in an unfilled column of a fixed box: a filled
// container or an image otherwise stretches to its row or column.
func sized(w, h int, child *v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Width: w, Height: h, Children: []*v1.Node{child}}
}

// lowered drops a fixed-width trailing node by dy inside a sized column: a
// row pins its last child only when that child's width is known.
func lowered(n *v1.Node, w, dy int) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Width: w, Children: []*v1.Node{spacer(1, dy), n}}
}

// centred places a w-wide node in the middle of a track: rows honour fixed
// widths where a column's CenterX does not reach containers.
func centred(n *v1.Node, w, track int) *v1.Node {
	return row(0, spacer((track-w)/2, 1), n)
}

// disc is a tonal glyph circle. The host does not round a column by shape,
// so the radius is half the edge.
func disc(d int, glyph *v1.Node, fill string) *v1.Node {
	glyph.CenterX = true
	inner := &v1.Node{Kind: v1.KindColumn, Width: d, Height: d, Radius: d / 2, Fill: fill, Padding: (d - glyph.IconSize) / 2, Children: []*v1.Node{glyph}}
	return sized(d, d, inner)
}

// item is the shell's list row (the Network panel's access points): one
// whole-row button with a state layer, a lead and a trailing status. A nil
// id renders the same row as plain status.
func item(id, name string, lead, trailing *v1.Node, width int, disabled bool) *v1.Node {
	content := &v1.Node{Kind: v1.KindRow, Gap: gap, PinEnd: true, Children: []*v1.Node{lead, trailing}}
	if id == "" {
		// A column, not a row: a row nested in a row is measured from its
		// children and ignores its own width.
		return &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "small", Width: width, Height: itemH, Padding: gap, Children: []*v1.Node{content}}
	}
	return &v1.Node{Kind: v1.KindButton, ID: id, Name: name, Role: "button", Fill: "card", Shape: "small", Width: width, Height: itemH,
		Padding: gap, Disabled: disabled, Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{content}}
}

// wrap puts a fixed-width row inside a list, which otherwise stretches it
// under the scrollbar.
func wrap(children ...*v1.Node) *v1.Node { return row(4, children...) }

func labelled(title, detail string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{bold(title), caption(detail, v1.ToneSubtle)}}
}

// chipWidth is a chip's box for its label at the caption metric.
func chipWidth(glyph, label string) int {
	w := 16 + len([]rune(label))*7
	if glyph != "" {
		w += 18
	}
	return w
}

// chip is a small stadium label, sized up front: a filled container in a
// row stretches unless its box is fixed.
func chip(glyph, label, fill string) *v1.Node {
	w := 16 + len([]rune(label))*7
	r := row(4)
	if glyph != "" {
		r.Children = append(r.Children, sizedIcon(glyph, 14, v1.ToneNormal))
		w += 18
	}
	r.Children = append(r.Children, caption(label, v1.ToneNormal))
	return sized(w, 24, &v1.Node{Kind: v1.KindColumn, Fill: fill, Shape: "stadium", Width: w, Height: 24, Padding: 4, Children: []*v1.Node{r}})
}

// brandMarks are the shipped wordmarks: file, and width over height. Each
// carries a thin transparent margin, so the host's cover-crop after rounding
// a box to physical pixels trims margin rather than ink.
var brandMarks = map[Provider]struct {
	file string
	w, h int
}{
	Mullvad: {"mullvad-wordmark.png", 1220, 156},
	Obscura: {"obscura-wordmark.png", 1888, 470},
}

// wordmark is p's wordmark at height h, tinted so the host paints it in the
// theme's text colour, or nil without shipped marks.
func wordmark(v ViewState, p Provider, h int) (*v1.Node, int) {
	mark, ok := brandMarks[p]
	if v.BrandDir == "" || !ok {
		return nil, 0
	}
	w := h * mark.w / mark.h
	img := &v1.Node{Kind: v1.KindImage, Path: filepath.Join(v.BrandDir, mark.file), ImageW: w, ImageH: h, Tint: true, Name: p.Name(), Role: "img"}
	return sized(w, h, img), w
}

// shown reports whether p's last observation can be displayed. The refresh
// that follows every action keeps the old observation on screen: it is
// progress, not a failure.
func shown(s ProviderState, now time.Time) bool {
	return s.Installed && s.HasObservation && now.Sub(s.LastSuccess) <= 10*time.Second &&
		(s.ReadError == "" || strings.HasPrefix(s.ReadError, "Refreshing"))
}

// readFailed reports whether the last read failed, as opposed to the first
// read or the refresh after an action still being on its way.
func readFailed(s ProviderState) bool {
	return s.ReadError != "" && s.ReadError != waitingForStatus && !strings.HasPrefix(s.ReadError, "Refreshing")
}

// look is p's state as one glyph, tone and word, shared by the bar, tooltip
// and status card so they never disagree.
func look(v ViewState, p Provider) (string, v1.Tone, string) {
	s := v.Machine.State(p)
	switch t := v.Machine.Transition(p); {
	case !s.Installed:
		return "vpn_key_off", v1.ToneSubtle, "Not installed"
	case t == Connecting:
		return "shield", v1.ToneAccent, "Connecting…"
	case t == Disconnecting:
		return "shield", v1.ToneSubtle, "Disconnecting…"
	case !shown(s, v.Now):
		return "gpp_maybe", v1.ToneSubtle, "Status unavailable"
	}
	switch s.Observed.Phase {
	case Connected:
		return "verified_user", v1.ToneAccent, "Connected"
	case Connecting:
		return "shield", v1.ToneAccent, "Connecting…"
	case Disconnecting:
		return "shield", v1.ToneSubtle, "Disconnecting…"
	case TunnelError:
		return "gpp_bad", v1.ToneError, "Connection error"
	}
	if s.Observed.Traffic == TrafficBlocked {
		return "lock", v1.ToneWarning, "Blocked"
	}
	return "shield", v1.ToneSubtle, "Disconnected"
}

// barProvider is the VPN the bar reports: one that is up or moving, else the
// selected one when installed. Inspecting the other provider in the panel
// must not change what the bar says.
func barProvider(v ViewState) Provider {
	for _, p := range []Provider{v.Machine.Selected, v.Machine.Selected.Other()} {
		s := v.Machine.State(p)
		if v.Machine.Transition(p) != "" || shown(s, v.Now) && s.Observed.Phase != Disconnected {
			return p
		}
	}
	if !v.Machine.State(v.Machine.Selected).Installed && v.Machine.State(v.Machine.Selected.Other()).Installed {
		return v.Machine.Selected.Other()
	}
	return v.Machine.Selected
}

func mutationDisabled(v ViewState) bool {
	return v.Machine.Busy || v.Machine.Pending != "" || !v.Machine.State(v.Machine.Selected).Fresh(v.Now)
}

func PrimaryAction(v ViewState) Action {
	phase := v.Machine.State(v.Machine.Selected).Observed.Phase
	if phase == Connected || phase == Connecting || phase == TunnelError {
		return Action{Kind: "disconnect"}
	}
	return Action{Kind: "connect"}
}

func otherProblem(v ViewState) string {
	other := v.Machine.Selected.Other()
	state := v.Machine.State(other)
	if !state.Installed {
		return ""
	}
	if !state.Fresh(v.Now) {
		return "Refresh " + other.Name() + " status before connecting."
	}
	if err := checkOther(other, state.Observed); err != nil {
		return err.Error()
	}
	return ""
}

// lines wraps a message onto at most two lines at the host's 8px text
// metric for a track width, so its height is known before layout.
func lines(message string, width int) []string {
	out := []string{""}
	for _, word := range strings.Fields(message) {
		last := len(out) - 1
		if out[last] != "" && len(out[last])+len(word)+1 > width/8 {
			if last == 1 {
				out[last] += "…"
				break
			}
			out = append(out, "")
			last++
		}
		if out[last] != "" {
			out[last] += " "
		}
		out[last] += word
	}
	return out
}

func note(message string, tone v1.Tone, width int, centre bool) *v1.Node {
	ls := lines(message, width)
	n := &v1.Node{Kind: v1.KindColumn, Gap: 2, Height: len(ls)*16 + (len(ls)-1)*2}
	for _, line := range ls {
		t := caption(line, tone)
		t.CenterX = centre
		n.Children = append(n.Children, t)
	}
	return n
}

func segment(id, label string, selected bool) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Name: label, Role: "button", Text: label, Selected: selected, Events: []v1.EventKind{v1.EventActivate}}
}

// countryCode is the ISO code for a country as a provider reports it:
// Obscura gives the code, Mullvad the name, which the relay list maps.
func countryCode(v ViewState, country string) string {
	if len(country) == 2 {
		return strings.ToLower(country)
	}
	for _, l := range v.Locations {
		if l.CountryName == country {
			return l.Country
		}
	}
	return ""
}

// flagText is the regional-indicator pair for a two-letter code, or "".
func flagText(code string) string {
	if len(code) != 2 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r < 'A' || r > 'Z' {
			return ""
		}
		b.WriteRune(0x1F1E6 + r - 'A')
	}
	return b.String()
}

// flag is a country's flag in a fixed cell: the painter measures a
// regional-indicator pair wider than it draws it, so the cell's width sets
// layout. A code that is not two letters shows as a code chip.
func flag(code, size string, width int) *v1.Node {
	if f := flagText(code); f != "" {
		t := &v1.Node{Kind: v1.KindText, Text: f, Size: size, CenterX: true}
		return &v1.Node{Kind: v1.KindColumn, Width: width, Children: []*v1.Node{t}}
	}
	if code == "" {
		return spacer(width, 1)
	}
	t := &v1.Node{Kind: v1.KindText, Text: strings.ToUpper(code), Bold: true, Size: "caption", CenterX: true}
	return sized(width, 28, &v1.Node{Kind: v1.KindColumn, Fill: "chip", Shape: "stadium", Width: width, Height: 28, Padding: 6, Children: []*v1.Node{t}})
}

// place names a location for display. Obscura reports a lowercase country
// code; the Mullvad relay list supplies a name when it is loaded.
func place(v ViewState, city, country string) string {
	if len(country) == 2 {
		code := strings.ToLower(country)
		country = strings.ToUpper(code)
		for _, l := range v.Locations {
			if l.Country == code {
				country = l.CountryName
				break
			}
		}
	}
	return strings.Join(nonempty(city, country), ", ")
}

// savedPlace names Mullvad's saved relay location from the relay list, with
// its country code, or "" when it is any location, a custom list, or not
// loaded yet.
func savedPlace(v ViewState) (string, string) {
	id := v.Settings.SavedLocation
	for _, l := range v.Locations {
		switch id {
		case l.Country + "::":
			return l.CountryName, l.Country
		case l.Country + ":" + l.City + ":":
			return l.CityName + ", " + l.CountryName, l.Country
		case l.ID():
			return l.Host + " · " + l.CityName, l.Country
		}
	}
	return "", ""
}

func locationsSupported(v ViewState) bool {
	return v.Machine.Selected == Mullvad || v.Obscura.Locations
}

func settingsEditable(v ViewState) bool {
	return v.Machine.Selected == Mullvad || v.Obscura.Settings
}

// Panel is the screen the session is on; a pending confirmation covers it.
func Panel(v ViewState) *v1.Node {
	installed := v.Machine.State(v.Machine.Selected).Installed
	switch {
	case !installed:
		return home(v)
	case v.Confirm != "":
		return confirmPage(v)
	case v.Screen == ScreenLocations && locationsSupported(v):
		return locationsPage(v)
	case v.Screen == ScreenDNS:
		return dnsPage(v)
	case v.Screen == ScreenBypass && v.Machine.Selected == Obscura:
		return bypassPage(v)
	}
	return home(v)
}

func list(height int, children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindList, Height: height, Gap: 4, Children: children}
}

// infoCard is a non-interactive card: a glyph beside a title and up to two
// subtle lines.
func infoCard(glyph, title string, lines ...string) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{bold(title)}}
	for _, line := range lines {
		col.Children = append(col.Children, caption(line, v1.ToneSubtle))
	}
	return wrap(&v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "small", Width: rowW, Padding: gap, Height: 2*gap + 16 + len(lines)*18,
		Children: []*v1.Node{row(13, icon(glyph, v1.ToneSubtle), col)}})
}

func nonempty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func LocationChoices(v ViewState) []Location {
	choices := []Location{}
	seen := map[string]bool{}
	query := strings.ToLower(strings.TrimSpace(v.Query))
	for _, l := range v.Locations {
		if query != "" {
			if !strings.Contains(strings.ToLower(l.CountryName+" "+l.CityName+" "+l.Host+" "+l.Country), query) {
				continue
			}
		} else {
			if v.Country != "" && l.Country != v.Country {
				continue
			}
			if v.City != "" && l.City != v.City {
				continue
			}
			if v.Country == "" {
				l.City, l.CityName, l.Host = "", "", ""
			} else if v.City == "" {
				l.Host = ""
			}
		}
		if !seen[l.ID()] {
			choices = append(choices, l)
			seen[l.ID()] = true
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		a, b := choices[i], choices[j]
		return a.CountryName+"\x00"+a.CityName+"\x00"+a.Host < b.CountryName+"\x00"+b.CityName+"\x00"+b.Host
	})
	return choices
}

func pageBounds(page, count int) (int, int) {
	start := min(max(page, 0)*PageSize, max((count-1)/PageSize, 0)*PageSize)
	return start, min(start+PageSize, count)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func onOff(value *bool) (shown, spoken string) {
	switch {
	case value == nil:
		return "—", "unknown"
	case *value:
		return "On", "on"
	}
	return "Off", "off"
}

// brandGlyph is p's mark in the shell's icon face.
func brandGlyph(p Provider) string {
	if p == Obscura {
		return "obscura"
	}
	return "mullvad"
}

func Bar(v ViewState, width int, mode string) *v1.Node {
	p := barProvider(v)
	glyph, tone, word := look(v, p)
	// The mark names the VPN; its tone carries the phase, as the ProtonVPN
	// pill beside it does. Connecting shares the connected tone.
	if v.Machine.State(p).Installed {
		glyph = brandGlyph(p)
	}
	label := word
	if mode == "location" {
		state := v.Machine.State(p)
		if shown(state, v.Now) && state.Observed.Phase == Connected && state.Observed.City != "" {
			label = state.Observed.City
		}
	}
	b := &v1.Node{Kind: v1.KindButton, ID: "bar", Name: p.Name() + " VPN: " + word, Role: "button", Events: []v1.EventKind{v1.EventActivate},
		Children: []*v1.Node{icon(glyph, tone)}}
	if mode != "icon" && !barwidth.Compact(width) {
		if width <= 0 {
			width = barwidth.StandardWidth
		}
		// The glyph carries the state colour; accent label text measured
		// 4.3:1 on the bar.
		t := text(label, v1.ToneNormal)
		t.MaxWidth = min(width, barwidth.StandardWidth) - 36
		b.Children = append(b.Children, t)
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{b}}
}

func Tooltip(v ViewState) *v1.Node {
	p := barProvider(v)
	_, tone, word := look(v, p)
	s := v.Machine.State(p)
	o := s.Observed
	head := bold(p.Name() + " · " + word)
	if tone == v1.ToneError {
		head.Tone = tone
	}
	lines := []*v1.Node{head}
	if readFailed(s) {
		lines = append(lines, text(s.ReadError, v1.ToneError))
	}
	if o.Phase == Connected || o.Phase == Connecting {
		if where := place(v, o.City, o.Country); where != "" {
			lines = append(lines, text(where, v1.ToneNormal))
		}
		for _, line := range nonempty(o.Exit, o.IP) {
			lines = append(lines, text(line, v1.ToneSubtle))
		}
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: lines}
}
