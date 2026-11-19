package vpn

import (
	"path/filepath"
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func find(root *v1.Node, id string) *v1.Node {
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if n := find(child, id); n != nil {
			return n
		}
	}
	return nil
}

func walk(n *v1.Node, visit func(*v1.Node)) {
	visit(n)
	for _, c := range n.Children {
		walk(c, visit)
	}
}

func hasText(n *v1.Node, want string) bool {
	found := false
	walk(n, func(c *v1.Node) { found = found || c.Text == want })
	return found
}

func containsText(n *v1.Node, part string) bool {
	found := false
	walk(n, func(c *v1.Node) { found = found || strings.Contains(c.Text, part) })
	return found
}

func brandDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("assets/brand")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func settingsOn() Settings {
	on, off := true, false
	return Settings{AutoConnect: &on, LAN: &off, Lockdown: &off, Tailscale: &on, WireGuard: &off,
		DNS: &DNS{Mode: "default", Blocks: [6]bool{true, true, true}}, SavedLocation: "se::"}
}

func lintPanel(t *testing.T, label string, root *v1.Node) {
	t.Helper()
	for _, f := range shelllint.Tree(root, v1.ViewPanel, 460, 580) {
		t.Errorf("%s: %s", label, f)
	}
	checkColumnBudget(t, root)
	checkPinEnd(t, root)
}

func TestViewStatesFit(t *testing.T) {
	locations, _ := ParseMullvadRelays(relayFixture)
	screens := []struct{ screen, confirm string }{
		{ScreenHome, ""}, {ScreenLocations, ""}, {ScreenDNS, ""}, {ScreenBypass, ""}, {ScreenHome, "lan"}, {ScreenHome, "lockdown-mode"},
	}
	for _, p := range []Provider{Mullvad, Obscura} {
		for _, phase := range []Phase{Disconnected, Connecting, Connected, Disconnecting, TunnelError} {
			for _, sc := range screens {
				for _, brand := range []string{"", brandDir(t)} {
					m, now := readyMachine(t)
					m.Selected = p
					st := m.State(p)
					st.Observed = Observation{Phase: phase, Traffic: TrafficBlocked, City: strings.Repeat("Long city ", 20), Exit: "se-sto-wg-001", Entry: "entry-1", IP: "198.51.100.2", Transport: "WireGuard"}
					m.Providers[p] = st
					v := ViewState{Machine: m, Now: now, Screen: sc.screen, Confirm: sc.confirm, Locations: locations, LocationsLoaded: true,
						SettingsLoaded: true, Settings: settingsOn(), BrandDir: brand, Obscura: ObscuraCaps{Locations: true}}
					for _, notice := range []string{"", strings.Repeat("Long diagnostic ", 30)} {
						v.Notice = notice
						lintPanel(t, string(p)+"/"+string(phase)+"/"+sc.screen+"/"+sc.confirm, Panel(v))
					}
					for _, width := range []int{240, 120, 64, 32} {
						for _, mode := range []string{"icon", "status", "location"} {
							for _, f := range shelllint.Tree(Bar(v, width, mode), v1.ViewBar, width, 32) {
								t.Errorf("bar %d %s: %s", width, mode, f)
							}
						}
					}
					for _, f := range shelllint.Tree(Tooltip(v), v1.ViewTooltip, 280, 200) {
						t.Error(f)
					}
				}
			}
		}
	}
	// One provider installed: the lockup row replaces the switch.
	for _, brand := range []string{"", brandDir(t)} {
		m, now := readyMachine(t)
		m.Providers[Mullvad] = ProviderState{}
		m.Selected = Obscura
		lintPanel(t, "obscura only", Panel(ViewState{Machine: m, Now: now, BrandDir: brand, SettingsLoaded: true, Settings: settingsOn()}))
		m.Providers[Obscura], m.Providers[Mullvad] = ProviderState{}, m.Providers[Obscura]
		lintPanel(t, "nothing installed", Panel(ViewState{Machine: m, Now: now, BrandDir: brand}))
	}
}

func checkColumnBudget(t *testing.T, n *v1.Node) {
	t.Helper()
	if n.Kind == v1.KindColumn && n.Height > 0 {
		used := n.Padding*2 + max(len(n.Children)-1, 0)*n.Gap
		for _, c := range n.Children {
			h := c.Height
			if h == 0 && c.Kind == v1.KindText {
				h = 16
			}
			used += h
		}
		if used > n.Height {
			t.Errorf("column uses %d of %d pixels", used, n.Height)
		}
	}
	for _, c := range n.Children {
		checkColumnBudget(t, c)
	}
}

func checkPinEnd(t *testing.T, n *v1.Node) {
	t.Helper()
	if n.PinEnd && len(n.Children) != 2 {
		t.Fatalf("PinEnd with %d children", len(n.Children))
	}
	for _, c := range n.Children {
		checkPinEnd(t, c)
	}
}

// The home screen's budget holds with the read-only caption, the tallest
// layout: top row, status card, tiles, caption, location row.
func TestHomeHeightBudget(t *testing.T) {
	used := control + heroH + (2*tileH + gap) + 16 + navH + 4*gap
	if used > bodyH {
		t.Fatalf("home uses %d of %d pixels", used, bodyH)
	}
}

func TestViewLocationHierarchyAndSearch(t *testing.T) {
	all, _ := ParseMullvadRelays(relayFixture)
	v := ViewState{Locations: all}
	got := LocationChoices(v)
	if len(got) != 2 || got[0].Country != "au" || got[0].City != "" {
		t.Fatalf("%+v", got)
	}
	v.Country = "se"
	if got = LocationChoices(v); len(got) != 1 || got[0].City != "sto" || got[0].Host != "" {
		t.Fatalf("%+v", got)
	}
	v.City = "sto"
	if got = LocationChoices(v); len(got) != 2 || got[0].Host == "" {
		t.Fatalf("%+v", got)
	}
	v.Query = "MELBOURNE"
	if got = LocationChoices(v); len(got) != 1 || got[0].Country != "au" {
		t.Fatalf("%+v", got)
	}
}

func TestStatusCardStates(t *testing.T) {
	locations, _ := ParseMullvadRelays(relayFixture)
	connected := func(o Observation) ViewState {
		m, now := readyMachine(t)
		st := m.State(Mullvad)
		st.Observed = o
		m.Providers[Mullvad] = st
		return ViewState{Machine: m, Now: now, Locations: locations, LocationsLoaded: true, SettingsLoaded: true, Settings: settingsOn()}
	}
	v := connected(Observation{Phase: Connected, City: "Stockholm", Country: "Sweden", Exit: "se-sto-wg-001", IP: "198.51.100.2", Transport: "WireGuard"})
	c := status(v)
	if c.word != "Connected" || c.detail != "se-sto-wg-001 · 198.51.100.2 · WireGuard" || !c.mono || c.code != "se" {
		t.Errorf("connected card: %+v", c)
	}
	if a := find(Panel(v), "action"); a == nil || a.Text != "Disconnect" || a.Fill != "error-container" {
		t.Errorf("connected action: %+v", a)
	}
	if got := tunnelLine(Observation{Exit: "se-sto-wg-001", IP: "2001:db8::1", Transport: "WireGuard"}); got != "se-sto-wg-001 · WireGuard" {
		t.Errorf("IPv6 kept: %q", got)
	}
	if got := tunnelLine(Observation{Exit: strings.Repeat("x", 40), IP: "198.51.100.2", Transport: "WireGuard"}); strings.Contains(got, "WireGuard") {
		t.Errorf("a long line keeps the protocol: %q", got)
	}
	v = connected(Observation{Phase: Disconnected, Traffic: TrafficBlocked})
	if c := status(v); c.word != "Blocked" || c.glyph != "lock" || c.detail != "No traffic leaves this computer" {
		t.Errorf("blocked card: %+v", c)
	}
	v = connected(Observation{Phase: Disconnected, Traffic: TrafficNotBlocked})
	if c := status(v); c.word != "Disconnected" || c.place != "Sweden" || c.detail != "Your traffic is not protected" {
		t.Errorf("disconnected card shows the saved place: %+v", c)
	}
	v = connected(Observation{Phase: TunnelError, Detail: "Check the connection in the Mullvad app."})
	c = status(v)
	if c.word != "Connection error" || c.detailTone != v1.ToneError || !strings.Contains(c.detail, "Mullvad app") {
		t.Errorf("error card: %+v", c)
	}
	if a := find(Panel(v), "action"); a == nil || a.Text != "Disconnect" {
		t.Errorf("error action keeps Disconnect: %+v", a)
	}
	// The other VPN is up: Connect is disabled and the card says why.
	v = connected(Observation{Phase: Disconnected, Traffic: TrafficNotBlocked})
	st := v.Machine.State(Obscura)
	st.Observed = Observation{Phase: Connected, Country: "de"}
	v.Machine.Providers[Obscura] = st
	if a := find(Panel(v), "action"); a == nil || !a.Disabled || !containsText(Panel(v), "Disconnect Obscura") {
		t.Errorf("conflict: action %+v", a)
	}
	// An unreadable status offers Refresh instead of a dead Connect.
	m, now := readyMachine(t)
	s := m.State(Mullvad)
	s.ReadError = "service unavailable"
	m.Providers[Mullvad] = s
	root := Panel(ViewState{Machine: m, Now: now})
	if find(root, "action") != nil || find(root, "retry-read") == nil {
		t.Error("unavailable status should offer Refresh")
	}
}

func TestPanelStatesReadDistinctly(t *testing.T) {
	m, now := readyMachine(t)
	m.Busy = true
	m.ActionError = "Mullvad: permission denied; check operator access"
	v := ViewState{Machine: m, Now: now, SettingsLoaded: true, Settings: settingsOn()}
	root := Panel(v)
	errorLine := false
	walk(root, func(n *v1.Node) {
		errorLine = errorLine || n.Kind == v1.KindText && n.Tone == v1.ToneError && strings.Contains(n.Text, "permission")
	})
	if !errorLine {
		t.Error("action error not in the error tone on the status card")
	}
	if n := find(root, "set:auto-connect"); n == nil || !n.Disabled || !hasText(n, "On") {
		t.Errorf("busy On setting should stay On and disabled: %+v", n)
	}
	// Read-only Obscura: no toggle is pressable, the pages still open.
	m.Busy, m.ActionError, m.Selected = false, "", Obscura
	v = ViewState{Machine: m, Now: now, SettingsLoaded: true, Settings: settingsOn()}
	walk(Panel(v), func(n *v1.Node) {
		if strings.HasPrefix(n.ID, "set:") {
			t.Errorf("read-only Obscura setting is pressable: %s", n.ID)
		}
	})
	if find(Panel(v), "open:dns") == nil || find(Panel(v), "open:bypass") == nil {
		t.Error("read-only Obscura hides its DNS and Bypass pages")
	}
	v.Screen = ScreenDNS
	walk(Panel(v), func(n *v1.Node) {
		if strings.HasPrefix(n.ID, "set:") {
			t.Errorf("read-only DNS chip is pressable: %s", n.ID)
		}
	})
	if !hasText(Panel(v), "Change these in the Obscura app") {
		t.Error("read-only DNS page does not say where to change it")
	}
	m.Providers[Mullvad] = ProviderState{}
	m.Selected = Mullvad
	missing := Panel(ViewState{Machine: m, Now: now})
	lintPanel(t, "not installed", missing)
	if find(missing, "action") != nil || find(missing, "set:lan") != nil {
		t.Error("not installed: offers dead controls")
	}
}

func TestProviderSwitchCarriesWordmarks(t *testing.T) {
	m, now := readyMachine(t)
	for _, p := range []Provider{Mullvad, Obscura} {
		m.Selected = p
		root := Panel(ViewState{Machine: m, Now: now, BrandDir: brandDir(t)})
		sel, other := find(root, "provider:"+string(p)), find(root, "provider:"+string(p.Other()))
		if !sel.Selected || other.Selected {
			t.Fatalf("%s: selected %+v other %+v", p, sel, other)
		}
		tinted, check := false, false
		walk(sel, func(n *v1.Node) {
			tinted = tinted || n.Kind == v1.KindImage && n.Tint && strings.HasSuffix(n.Path, brandMarks[p].file)
			check = check || n.Kind == v1.KindIcon && n.Icon == "check"
		})
		if !tinted || !check || sel.Text != "" || sel.Name != p.Name() {
			t.Errorf("%s segment: tinted %v check %v %+v", p, tinted, check, sel)
		}
		plain := find(Panel(ViewState{Machine: m, Now: now}), "provider:"+string(p))
		if plain.Text != p.Name() {
			t.Errorf("%s: without marks the segment names the provider: %+v", p, plain)
		}
	}
}

func TestObscuraSettingsFollowCLISupport(t *testing.T) {
	m, now := readyMachine(t)
	m.Selected = Obscura
	v := ViewState{Machine: m, Now: now, SettingsLoaded: true, Obscura: ObscuraCaps{Settings: true}, Settings: settingsOn()}
	root := Panel(v)
	for _, id := range []string{"set:auto-connect", "set:lan", "open:dns", "open:bypass"} {
		if n := find(root, id); n == nil || n.Disabled {
			t.Errorf("%s should be a live control: %+v", id, n)
		}
	}
	if find(root, "set:lockdown-mode") != nil {
		t.Error("Obscura has no lockdown command")
	}
	v.Screen = ScreenDNS
	if n := find(Panel(v), "set:dns:0"); n == nil || n.Disabled {
		t.Errorf("DNS chip should be live: %+v", n)
	}
	v.Screen = ScreenBypass
	bypass := Panel(v)
	if find(bypass, "set:tailscale") != nil || !hasText(bypass, "Tailscale bypass") {
		t.Error("bypass page must be read-only status")
	}
	// Without location targets the row is status and the page is closed.
	v.Screen = ScreenLocations
	root = Panel(v)
	if find(root, "open:locations") != nil || find(root, "search") != nil || !hasText(root, "Automatic · Obscura picks the exit") {
		t.Error("Obscura without locations should show automatic status on home")
	}
}

func TestTilesToggleAndOpenPages(t *testing.T) {
	m, now := readyMachine(t)
	root := Panel(ViewState{Machine: m, Now: now, SettingsLoaded: true, Settings: settingsOn()})
	for id, want := range map[string]string{"set:auto-connect": "On", "set:lan": "Blocked", "set:lockdown-mode": "Off", "open:dns": "3 of 6"} {
		n := find(root, id)
		if n == nil || n.Kind != v1.KindButton || n.Disabled || !hasText(n, want) {
			t.Errorf("%s: want a live tile reading %q: %+v", id, want, n)
			continue
		}
		if !strings.Contains(n.Name, strings.ToLower(want)) {
			t.Errorf("%s: accessible name %q lacks its state", id, n.Name)
		}
	}
	if n := find(root, "set:auto-connect"); n.Fill != "soft" {
		t.Errorf("an on tile is tonal: %+v", n)
	}
	if _, err := m.BeginMutation(Mullvad, Action{Kind: "setting", Key: "lan", Enabled: true}, now); err != nil {
		t.Fatal(err)
	}
	busy := find(Panel(ViewState{Machine: m, Now: now, SettingsLoaded: true, Settings: settingsOn()}), "set:lan")
	spinner := false
	walk(busy, func(n *v1.Node) { spinner = spinner || n.Kind == v1.KindSpinner })
	if !busy.Disabled || !spinner {
		t.Errorf("the acting tile shows a spinner and every toggle waits: %+v", busy)
	}
}

func TestSubPagesLeadWithBack(t *testing.T) {
	locations, _ := ParseMullvadRelays(relayFixture)
	m, now := readyMachine(t)
	for _, v := range []ViewState{
		{Screen: ScreenLocations}, {Screen: ScreenDNS}, {Confirm: "lan"}, {Confirm: "lockdown-mode"},
	} {
		v.Machine, v.Now, v.Locations, v.LocationsLoaded, v.SettingsLoaded, v.Settings = m, now, locations, true, true, settingsOn()
		root := Panel(v)
		if find(root, "back") == nil {
			t.Errorf("%s%s: no back control", v.Screen, v.Confirm)
		}
		if find(root, "action") != nil {
			t.Errorf("%s%s: sub-page carries the status card", v.Screen, v.Confirm)
		}
	}
	lockdown := Panel(ViewState{Machine: m, Now: now, Confirm: "lockdown-mode", SettingsLoaded: true, Settings: settingsOn()})
	if n := find(lockdown, "confirm"); n == nil || n.Fill != "error" || n.Text != "Turn off" {
		t.Errorf("lockdown confirm: %+v", n)
	}
	if n := find(lockdown, "cancel"); n == nil || n.Fill != "chip" {
		t.Errorf("cancel is a plain tonal button: %+v", n)
	}
	list := Panel(ViewState{Machine: m, Now: now, Screen: ScreenLocations, Locations: locations, LocationsLoaded: true})
	if find(list, "search") == nil || find(list, "location:au::") == nil || find(list, "browse:se::") == nil {
		t.Error("location page lacks search or rows")
	}
}

func TestFlags(t *testing.T) {
	if got := flagText("ch"); got != "\U0001F1E8\U0001F1ED" {
		t.Errorf("ch = %q", got)
	}
	for _, code := range []string{"", "c", "c1", "chx"} {
		if got := flagText(code); got != "" {
			t.Errorf("%q gave a flag %q", code, got)
		}
	}
	if n := flag("x1", "", 36); n.Width != 36 || !hasText(n, "X1") {
		t.Errorf("non-ISO code should be a code chip: %+v", n)
	}
}

func TestBarAndTooltip(t *testing.T) {
	m, now := readyMachine(t)
	// L2: Obscura is up; inspecting Mullvad must not change the bar.
	st := m.State(Obscura)
	st.Observed = Observation{Phase: Connected, City: "Melbourne", Country: "au"}
	m.Providers[Obscura], m.Selected = st, Mullvad
	b := Bar(ViewState{Machine: m, Now: now}, 240, "status")
	if !strings.HasPrefix(b.Children[0].Name, "Obscura") || b.Children[0].Children[0].Icon != "obscura" || b.Children[0].Children[0].Tone != v1.ToneAccent {
		t.Errorf("bar: %+v", b.Children[0])
	}
	// L11: the code is shown as a code, uppercased.
	if tip := Tooltip(ViewState{Machine: m, Now: now}); !hasText(tip, "Melbourne, AU") {
		t.Error("lowercase country code in the tooltip")
	}
	// A running connect shows in the status card, with no message line.
	m, now = readyMachine(t)
	if _, err := m.BeginMutation(Mullvad, Action{Kind: "connect"}, now); err != nil {
		t.Fatal(err)
	}
	v := ViewState{Machine: m, Now: now}
	root := Panel(v)
	if a := find(root, "action"); a == nil || a.Text != "Connecting…" || !a.Disabled {
		t.Errorf("action during connect: %+v", a)
	}
	spinner := false
	walk(root, func(n *v1.Node) { spinner = spinner || n.Kind == v1.KindSpinner })
	if msg, _ := message(v); !spinner || msg != "" {
		t.Errorf("spinner %v message %q", spinner, msg)
	}
	if g := Bar(v, 32, "icon").Children[0].Children[0]; g.Icon != "mullvad" || g.Tone != v1.ToneAccent {
		t.Errorf("connecting bar glyph: %+v", g)
	}
	// One installed provider gets no switch.
	m = NewMachine(false, true)
	if find(Panel(ViewState{Machine: m, Now: now}), "provider:mullvad") != nil {
		t.Error("switch offers a provider that is not installed")
	}
}
