package kdeconnect

import (
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func testSettings() Settings { return DefaultSettings() }

// pairedSnap is one reachable, paired phone with every reading known, plus
// a reachable tablet behind it.
func pairedSnap() Snapshot {
	return Snapshot{
		Available:     true,
		BackendName:   "KDE Connect",
		AnnouncedName: "My Desktop",
		SelectedID:    "devA",
		Devices: []Device{
			{
				ID: "devA", Name: "Pixel 10 Pro XL", Type: "phone",
				Reachable: true, Paired: true,
				SupportedPlugins:   []string{"kdeconnect_battery", "findmyphone", "ping", "sftp", "clipboard", "share", "sms", "connectivity_report", "notifications"},
				BatteryCharge:      98,
				BatteryKnown:       true,
				NetworkType:        "LTE",
				NetworkStrength:    1,
				NetworkKnown:       true,
				NotificationCount:  3,
				NotificationsKnown: true,
			},
			{
				ID: "devB", Name: "Galaxy Tab", Type: "tablet",
				Reachable: true, Paired: true,
				BatteryKnown: true, BatteryCharge: 55, BatteryCharging: true,
			},
		},
	}
}

func TestBarTreeShowsOfflineState(t *testing.T) {
	t.Parallel()
	open := BarTree(Snapshot{}).Children[0]
	if open.ID != "open" || open.Name == "" || open.Role == "" {
		t.Fatalf("open control = %+v", open)
	}
	if open.Icon != "phonelink-off" || open.Text != "N/A" {
		t.Fatalf("offline pill = %q %q", open.Icon, open.Text)
	}
}

func TestBarTreeShowsBatteryForSelectedDevice(t *testing.T) {
	t.Parallel()
	open := BarTree(pairedSnap()).Children[0]
	if open.Icon != "smartphone" {
		t.Fatalf("available glyph = %q", open.Icon)
	}
	if open.Text != "98%" {
		t.Fatalf("battery label = %q", open.Text)
	}
	unknown := pairedSnap()
	unknown.Devices[0].BatteryKnown = false
	if got := BarTree(unknown).Children[0].Text; got != "" {
		t.Fatalf("unknown battery label = %q, want icon only", got)
	}
}

func TestTooltipTreeTracksState(t *testing.T) {
	t.Parallel()
	if got := TooltipTree(Snapshot{}).Children[1].Text; got != "unavailable" {
		t.Fatalf("unavailable tooltip = %q", got)
	}
	if got := TooltipTree(Snapshot{Available: true}).Children[1].Text; got != "no devices" {
		t.Fatalf("empty tooltip = %q", got)
	}
	if got := TooltipTree(pairedSnap()).Children[1].Text; got != "connected · 98%" {
		t.Fatalf("selected tooltip = %q", got)
	}
	charging := pairedSnap()
	charging.Devices[0].BatteryCharging = true
	if got := TooltipTree(charging).Children[1].Text; got != "connected · 98% · charging" {
		t.Fatalf("charging tooltip = %q", got)
	}
}

func TestPanelTreeHeaderCounts(t *testing.T) {
	t.Parallel()
	header := PanelTree(pairedSnap(), testSettings()).Children[0]
	texts := headerTexts(header)
	if len(texts) != 2 || texts[0] != "KDE Connect" || texts[1] != "2 connected • 2 paired" {
		t.Fatalf("header texts = %v", texts)
	}
}

func TestPanelTreeUnavailableAndEmptyStates(t *testing.T) {
	t.Parallel()
	down := PanelTree(Snapshot{}, testSettings())
	if len(down.Children) != 2 || down.Children[1].Fill != "card" {
		t.Fatalf("unavailable panel = %+v", down)
	}
	if text := down.Children[1].Children[0].Text; text != "KDE Connect daemon unreachable" {
		t.Fatalf("unavailable headline = %q", text)
	}

	empty := PanelTree(Snapshot{Available: true}, testSettings())
	if len(empty.Children) != 2 || empty.Children[1].Fill != "card" {
		t.Fatalf("empty panel = %+v", empty)
	}
	if text := empty.Children[1].Children[0].Text; text != "No devices" {
		t.Fatalf("empty headline = %q", text)
	}

	populated := PanelTree(pairedSnap(), testSettings())
	if len(populated.Children) <= 1 {
		t.Fatal("populated panel has no device sections")
	}
}

func TestHeaderRefreshControlPinsRight(t *testing.T) {
	t.Parallel()
	header := PanelTree(pairedSnap(), testSettings()).Children[0]
	if !header.PinEnd {
		t.Fatal("header row is not a pin-end row")
	}
	refresh := header.Children[1]
	if refresh.ID != "refresh" || refresh.Icon != "refresh" || refresh.Name == "" || refresh.Role == "" {
		t.Fatalf("refresh control = %+v", refresh)
	}
}

func TestPanelTreeSwitcherOnlyWhenMultipleDevices(t *testing.T) {
	t.Parallel()
	solo := pairedSnap()
	solo.Devices = solo.Devices[:1]
	soloPanel := PanelTree(solo, testSettings())
	for _, section := range soloPanel.Children {
		for _, row := range section.Children {
			if row.Kind == v1.KindRow && row.PinEnd && len(row.Children) == 2 {
				if button := row.Children[1]; button.ID == "select-devA" {
					t.Fatal("switcher row for the selected device itself")
				}
			}
		}
	}

	panel := PanelTree(pairedSnap(), testSettings())
	var switchRows int
	for _, section := range panel.Children {
		for _, row := range section.Children {
			if row.Kind == v1.KindRow && len(row.Children) == 2 && row.Children[1].ID == "select-devB" {
				switchRows++
			}
		}
	}
	if switchRows != 1 {
		t.Fatalf("switcher rows for the tablet = %d, want 1", switchRows)
	}
}

func TestPanelTreeUnpairedDeviceShowsRequestCard(t *testing.T) {
	t.Parallel()
	snap := pairedSnap()
	snap.Devices[0].Paired = false
	panel := PanelTree(snap, testSettings())
	if len(panel.Children) != 2 {
		t.Fatalf("unpaired panel sections = %d, want header + card", len(panel.Children))
	}
	card := panel.Children[1]
	if hint := cardText(card); hint == nil || *hint != "Not paired" {
		t.Fatalf("request card hint = %+v", hint)
	}
	if findButton(card, "pair") == nil {
		t.Fatalf("request card = %+v", card)
	}
}

func TestPanelTreePairingRequestShowsVerificationAndActions(t *testing.T) {
	t.Parallel()
	incoming := pairedSnap()
	incoming.Devices[0].PairRequestedByPeer = true
	incoming.Devices[0].VerificationKey = "123456"
	panel := PanelTree(incoming, testSettings())
	card := panel.Children[1]
	hint := cardText(card)
	if hint == nil || *hint != "Verification: 123456" {
		t.Fatalf("verification hint missing: %+v", card)
	}
	if findButton(card, "pair-accept") == nil || findButton(card, "pair-reject") == nil {
		t.Fatalf("accept/reject missing: %+v", card)
	}
	if findButton(card, "pair-cancel") != nil {
		t.Fatal("cancel button on an incoming request")
	}

	outgoing := pairedSnap()
	outgoing.Devices[0].PairRequested = true
	outCard := PanelTree(outgoing, testSettings()).Children[1]
	if findButton(outCard, "pair-cancel") == nil || findButton(outCard, "pair-accept") != nil {
		t.Fatalf("outgoing card actions = %+v", outCard)
	}
}

func TestActionRowGating(t *testing.T) {
	t.Parallel()
	panel := PanelTree(pairedSnap(), testSettings())
	actions := findSection(panel, func(n *v1.Node) bool {
		return n.Kind == v1.KindRow && len(n.Children) == 6 && n.Children[0].ID == "ring"
	})
	if actions == nil {
		t.Fatal("action row missing")
	}
	for _, b := range actions.Children {
		if b.Disabled {
			t.Fatalf("%s disabled with every capability present", b.ID)
		}
	}

	noClipboard := testSettings()
	noClipboard.EnableClipboard = false
	trimmed := findSection(PanelTree(pairedSnap(), noClipboard), func(n *v1.Node) bool {
		return n.Kind == v1.KindRow && len(n.Children) == 6 && n.Children[0].ID == "ring"
	})
	if !trimmed.Children[3].Disabled {
		t.Fatal("clipboard action enabled with the setting off")
	}

	offline := pairedSnap()
	offline.Devices[0].Reachable = false
	for _, b := range findSection(PanelTree(offline, testSettings()), func(n *v1.Node) bool {
		return n.Kind == v1.KindRow && len(n.Children) == 6 && n.Children[0].ID == "ring"
	}).Children {
		if !b.Disabled {
			t.Fatalf("%s enabled while offline", b.ID)
		}
	}

	uncapable := pairedSnap()
	uncapable.Devices[0].SupportedPlugins = nil
	for _, b := range findSection(PanelTree(uncapable, testSettings()), func(n *v1.Node) bool {
		return n.Kind == v1.KindRow && len(n.Children) == 6 && n.Children[0].ID == "ring"
	}).Children {
		if !b.Disabled {
			t.Fatalf("%s enabled without capabilities", b.ID)
		}
	}
}

func TestInfoRowsAndBatteryIcons(t *testing.T) {
	t.Parallel()
	panel := PanelTree(pairedSnap(), testSettings())
	rows := findSection(panel, func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && len(n.Children) == 4 && n.Children[0].Key == "info-battery"
	})
	if rows == nil {
		t.Fatal("info rows missing")
	}
	if rows.Children[0].Children[1].Text != "98%" {
		t.Fatalf("battery value = %+v", rows.Children[0])
	}
	if got := rows.Children[1].Children[1].Text; got != "Weak" {
		t.Fatalf("signal value = %q", got)
	}
	if got := rows.Children[2].Children[1].Text; got != "LTE" {
		t.Fatalf("network value = %q", got)
	}
	if got := rows.Children[3].Children[1].Text; got != "3" {
		t.Fatalf("notification value = %q", got)
	}
}

func TestBatteryIconNameBands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		charge int
		icon   string
	}{
		{3, "battery-critical"},
		{10, "battery-0"},
		{15, "battery-1"},
		{50, "battery-3"},
		{98, "battery-6"},
		{100, "battery-6"},
	}
	for _, tc := range cases {
		dev := &Device{BatteryKnown: true, BatteryCharge: tc.charge}
		if got := batteryIconName(dev); got != tc.icon {
			t.Fatalf("charge %d icon = %q, want %q", tc.charge, got, tc.icon)
		}
	}
	charging := &Device{BatteryKnown: true, BatteryCharge: 98, BatteryCharging: true}
	if got := batteryIconName(charging); got != "battery-charging-6" {
		t.Fatalf("charging icon = %q", got)
	}
}

func TestPanelDeltaPatchesReadings(t *testing.T) {
	t.Parallel()
	prev := pairedSnap()
	next := pairedSnap()
	next.Devices[0].BatteryCharge = 42
	next.Devices[0].BatteryCharging = true
	next.Devices[0].NotificationCount = 5

	delta := PanelDelta(prev, next)
	if delta == nil {
		t.Fatal("reading delta produced no patch")
	}
	keys := map[string]bool{}
	for _, r := range delta {
		keys[r.Key] = true
	}
	if !keys["battery-progress"] || !keys["info-battery"] || !keys["info-notifications"] {
		t.Fatalf("patch keys = %v", keys)
	}
	if len(delta) != 3 {
		t.Fatalf("patch has %d replacements, want 3", len(delta))
	}
	for _, r := range delta {
		if r.Node == nil || r.Node.Key != r.Key {
			t.Fatalf("replacement %q carries a foreign key", r.Key)
		}
	}
}

func TestPanelDeltaNilOnStructuralChange(t *testing.T) {
	t.Parallel()
	prev := pairedSnap()

	switched := pairedSnap()
	switched.SelectedID = "devB"
	if got := PanelDelta(prev, switched); got != nil {
		t.Fatal("selection change produced a patch")
	}

	gone := pairedSnap()
	gone.Devices = gone.Devices[:1]
	if got := PanelDelta(prev, gone); got != nil {
		t.Fatal("device removal produced a patch")
	}

	unknown := pairedSnap()
	unknown.Devices[0].BatteryKnown = false
	if got := PanelDelta(prev, unknown); got != nil {
		t.Fatal("a reading flag flip produced a patch")
	}

	reconnected := pairedSnap()
	reconnected.Devices[1].Reachable = false
	if got := PanelDelta(prev, reconnected); got != nil {
		t.Fatal("a reachability flip produced a patch")
	}
}

func TestTreesValidate(t *testing.T) {
	t.Parallel()
	if err := v1.Validate(BarTree(Snapshot{}), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree(pairedSnap()), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(TooltipTree(Snapshot{}), v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(TooltipTree(pairedSnap()), v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}

	states := []Snapshot{
		{},
		{Available: true},
		pairedSnap(),
	}
	incoming := pairedSnap()
	incoming.Devices[0].PairRequestedByPeer = true
	incoming.Devices[0].VerificationKey = "123456"
	states = append(states, incoming)

	outgoing := pairedSnap()
	outgoing.Devices[0].PairRequested = true
	states = append(states, outgoing)

	unpaired := pairedSnap()
	unpaired.Devices[0].Paired = false
	states = append(states, unpaired)

	offline := pairedSnap()
	offline.Devices[0].Reachable = false
	states = append(states, offline)

	bare := pairedSnap()
	bare.Devices[0].SupportedPlugins = nil
	bare.Devices[0].BatteryKnown = false
	bare.Devices[0].NetworkKnown = false
	bare.Devices[0].NotificationsKnown = false
	states = append(states, bare)

	for i, snap := range states {
		if err := v1.Validate(PanelTree(snap, testSettings()), v1.ViewPanel); err != nil {
			t.Fatalf("panel state %d: %v", i, err)
		}
	}
}

// headerTexts collects the text of the title and detail runs inside the
// header's nested row.
func headerTexts(header *v1.Node) []string {
	var texts []string
	var walk func(n *v1.Node)
	walk = func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text != "" {
			texts = append(texts, n.Text)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, c := range header.Children {
		walk(c)
	}
	return texts
}

// cardText returns the first muted hint text of a card, depth-first.
func cardText(card *v1.Node) *string {
	if card == nil {
		return nil
	}
	if card.Kind == v1.KindText && card.Tone == v1.ToneSubtle {
		s := card.Text
		return &s
	}
	for _, c := range card.Children {
		if found := cardText(c); found != nil {
			return found
		}
	}
	return nil
}

func findButton(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.Kind == v1.KindButton && n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if found := findButton(c, id); found != nil {
			return found
		}
	}
	return nil
}

// findSection returns the first direct child of the panel matching want.
func findSection(n *v1.Node, want func(*v1.Node) bool) *v1.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if want(c) {
			return c
		}
	}
	return nil
}
