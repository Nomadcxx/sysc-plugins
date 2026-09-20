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
	// An unreachable selected device reads as offline even with the daemon
	// up, the reference pill's behaviour.
	offline := pairedSnap()
	offline.Devices[0].Reachable = false
	offlinePill := BarTree(offline).Children[0]
	if offlinePill.Icon != "phonelink-off" || offlinePill.Text != "" {
		t.Fatalf("offline pill = %q %q", offlinePill.Icon, offlinePill.Text)
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
	header := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{}).Children[0]
	texts := headerTexts(header)
	if len(texts) != 2 || texts[0] != "KDE Connect" || texts[1] != "2 connected • 2 paired" {
		t.Fatalf("header texts = %v", texts)
	}
}

func TestPanelTreeUnavailableAndEmptyStates(t *testing.T) {
	t.Parallel()
	down := PanelTree(Snapshot{}, testSettings(), ComposerNone, Drafts{})
	if len(down.Children) != 2 || down.Children[1].Fill != "error-container" {
		t.Fatalf("unavailable panel = %+v", down)
	}
	if text := down.Children[1].Children[0].Text; text != "Phone Connect Not Available" {
		t.Fatalf("unavailable headline = %q", text)
	}

	empty := PanelTree(Snapshot{Available: true}, testSettings(), ComposerNone, Drafts{})
	if len(empty.Children) != 2 || empty.Children[1].Fill != "card" {
		t.Fatalf("empty panel = %+v", empty)
	}
	if text := empty.Children[1].Children[0].Text; text != "No devices found" {
		t.Fatalf("empty headline = %q", text)
	}

	populated := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	if len(populated.Children) <= 1 {
		t.Fatal("populated panel has no device sections")
	}
}

func TestHeaderRefreshControlPinsRight(t *testing.T) {
	t.Parallel()
	header := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{}).Children[0]
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
	soloPanel := PanelTree(solo, testSettings(), ComposerNone, Drafts{})
	if findButton(soloPanel, "select-devA") != nil {
		t.Fatal("switcher row for the selected device itself")
	}

	panel := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	if findButton(panel, "select-devB") == nil {
		t.Fatal("switcher card for the tablet missing")
	}
}

func TestDeviceCardChipsAndStatus(t *testing.T) {
	t.Parallel()
	chips := pairedSnap()
	chips.Devices[1].NetworkKnown = true
	chips.Devices[1].NetworkStrength = 3
	card := deviceCard(PanelTree(chips, testSettings(), ComposerNone, Drafts{}), "Galaxy Tab")
	if card == nil {
		t.Fatal("tablet card missing")
	}
	if !contains(allTexts(card), "55%") {
		t.Fatalf("battery chip missing: %v", allTexts(card))
	}
	if !walkFindNode(card, func(n *v1.Node) bool {
		return n.Kind == v1.KindIcon && n.Icon == "signal-cellular-3-bar"
	}) {
		t.Fatal("network chip icon missing")
	}

	offline := pairedSnap()
	offline.Devices[1].Reachable = false
	offlineCard := deviceCard(PanelTree(offline, testSettings(), ComposerNone, Drafts{}), "Galaxy Tab")
	if offlineCard == nil || !contains(allTexts(offlineCard), "Offline") {
		t.Fatal("offline status missing")
	}

	pairing := pairedSnap()
	pairing.Devices[1].PairRequested = true
	pairCard := deviceCard(PanelTree(pairing, testSettings(), ComposerNone, Drafts{}), "Galaxy Tab")
	if pairCard == nil || !contains(allTexts(pairCard), "Pairing...") {
		t.Fatal("pairing-in-progress status missing")
	}

	connected := deviceCard(PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{}), "Galaxy Tab")
	if contains(allTexts(connected), "Offline") || contains(allTexts(connected), "Not paired") {
		t.Fatalf("connected card grew a status line: %v", allTexts(connected))
	}
}

func TestDeviceCardPairingActionsPerCard(t *testing.T) {
	t.Parallel()
	panel := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	if findButton(panel, "pair-devB") != nil || findButton(panel, "accept-devB") != nil {
		t.Fatal("pairing actions on a paired, reachable card")
	}

	unpaired := pairedSnap()
	unpaired.Devices[1].Paired = false
	if findButton(PanelTree(unpaired, testSettings(), ComposerNone, Drafts{}), "pair-devB") == nil {
		t.Fatal("request-pairing action missing on an unpaired card")
	}

	incoming := pairedSnap()
	incoming.Devices[1].PairRequestedByPeer = true
	incoming.Devices[1].VerificationKey = "999999"
	inPanel := PanelTree(incoming, testSettings(), ComposerNone, Drafts{})
	if findButton(inPanel, "accept-devB") == nil || findButton(inPanel, "reject-devB") == nil {
		t.Fatal("accept/reject actions missing on the requesting card")
	}
	if findButton(inPanel, "pair-devB") != nil {
		t.Fatal("request-pairing offered while a request is incoming")
	}
}

// deviceCard finds the deepest filled card column whose subtree names the
// device.
func deviceCard(n *v1.Node, name string) *v1.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if card := deviceCard(c, name); card != nil {
			return card
		}
	}
	if n.Kind == v1.KindColumn && n.Fill == "card" && contains(allTexts(n), name) {
		return n
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func allTexts(n *v1.Node) []string {
	var texts []string
	walkFindNode(n, func(x *v1.Node) bool {
		if x.Kind == v1.KindText {
			texts = append(texts, x.Text)
		}
		return false
	})
	return texts
}

func walkFindNode(n *v1.Node, ok func(*v1.Node) bool) bool {
	if n == nil {
		return false
	}
	if ok(n) {
		return true
	}
	for _, c := range n.Children {
		if walkFindNode(c, ok) {
			return true
		}
	}
	return false
}

func TestPanelTreeUnpairedDeviceShowsRequestCard(t *testing.T) {
	t.Parallel()
	snap := pairedSnap()
	snap.Devices[0].Paired = false
	panel := PanelTree(snap, testSettings(), ComposerNone, Drafts{})
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
	panel := PanelTree(incoming, testSettings(), ComposerNone, Drafts{})
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
	outCard := PanelTree(outgoing, testSettings(), ComposerNone, Drafts{}).Children[1]
	if findButton(outCard, "pair-cancel") == nil || findButton(outCard, "pair-accept") != nil {
		t.Fatalf("outgoing card actions = %+v", outCard)
	}
}

func TestActionRowGating(t *testing.T) {
	t.Parallel()
	panel := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
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
	trimmed := findSection(PanelTree(pairedSnap(), noClipboard, ComposerNone, Drafts{}), func(n *v1.Node) bool {
		return n.Kind == v1.KindRow && len(n.Children) == 6 && n.Children[0].ID == "ring"
	})
	if !trimmed.Children[3].Disabled {
		t.Fatal("clipboard action enabled with the setting off")
	}

	offline := pairedSnap()
	offline.Devices[0].Reachable = false
	for _, b := range findSection(PanelTree(offline, testSettings(), ComposerNone, Drafts{}), func(n *v1.Node) bool {
		return n.Kind == v1.KindRow && len(n.Children) == 6 && n.Children[0].ID == "ring"
	}).Children {
		if !b.Disabled {
			t.Fatalf("%s enabled while offline", b.ID)
		}
	}

	uncapable := pairedSnap()
	uncapable.Devices[0].SupportedPlugins = nil
	for _, b := range findSection(PanelTree(uncapable, testSettings(), ComposerNone, Drafts{}), func(n *v1.Node) bool {
		return n.Kind == v1.KindRow && len(n.Children) == 6 && n.Children[0].ID == "ring"
	}).Children {
		if !b.Disabled {
			t.Fatalf("%s enabled without capabilities", b.ID)
		}
	}
}

func TestInfoRowsAndBatteryIcons(t *testing.T) {
	t.Parallel()
	panel := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	rows := findSection(panel, func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && len(n.Children) == 4 && n.Children[0].Key == "info-battery"
	})
	if rows == nil {
		t.Fatal("info rows missing")
	}
	// Stacked layout: value is the second text of the label column.
	if got := rows.Children[0].Children[1].Children[1].Text; got != "98%" {
		t.Fatalf("battery value = %q", got)
	}
	if got := rows.Children[1].Children[1].Children[1].Text; got != "Weak" {
		t.Fatalf("signal value = %q", got)
	}
	if got := rows.Children[2].Children[1].Children[1].Text; got != "LTE" {
		t.Fatalf("network value = %q", got)
	}
	if got := rows.Children[3].Children[1].Children[1].Text; got != "3" {
		t.Fatalf("notification value = %q", got)
	}
}

func TestNetworkTypeAndStrengthLabels(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"NR":       "5G",
		"5G":       "5G",
		"5G_NR":    "5G",
		"LTE":      "LTE",
		"4G":       "LTE",
		"LTE_CA":   "LTE+",
		"LTE+":     "LTE+",
		"HSPAP":    "3G",
		"UMTS":     "3G",
		"EDGE":     "2G",
		"GPRS":     "2G",
		"td-scdma": "Td-scdma",
		"":         "N/A",
	}
	for raw, want := range cases {
		if got := networkTypeLabel(raw); got != want {
			t.Fatalf("networkTypeLabel(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := strengthLabel(0); got != "No Signal" {
		t.Fatalf("strength 0 = %q, want No Signal", got)
	}
}

func TestBatteryIconNameBands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		charge int
		icon   string
	}{
		{5, "battery-critical"},
		{9, "battery-critical"},
		{10, "battery-0"},
		{15, "battery-0"},
		{20, "battery-1"},
		{35, "battery-2"},
		{50, "battery-3"},
		{65, "battery-4"},
		{80, "battery-5"},
		{95, "battery-6"},
		{100, "battery-6"},
	}
	for _, tc := range cases {
		dev := &Device{BatteryKnown: true, BatteryCharge: tc.charge}
		if got := batteryIconName(dev); got != tc.icon {
			t.Fatalf("charge %d icon = %q, want %q", tc.charge, got, tc.icon)
		}
	}
	chargingCases := []struct {
		charge int
		icon   string
	}{
		{98, "battery-charging-6"},
		{90, "battery-charging-6"},
		{70, "battery-charging-4"},
		{45, "battery-charging-3"},
		{25, "battery-charging-2"},
		{10, "battery-charging-1"},
	}
	for _, tc := range chargingCases {
		dev := &Device{BatteryKnown: true, BatteryCharge: tc.charge, BatteryCharging: true}
		if got := batteryIconName(dev); got != tc.icon {
			t.Fatalf("charging %d icon = %q, want %q", tc.charge, got, tc.icon)
		}
	}
}

func TestNetworkStrengthAndTypeIcons(t *testing.T) {
	t.Parallel()
	strengths := map[int]string{
		0: "signal-cellular-null",
		1: "signal-cellular-1-bar",
		2: "signal-cellular-2-bar",
		3: "signal-cellular-3-bar",
		4: "signal-cellular-4-bar",
		5: "signal-cellular-4-bar",
	}
	for strength, want := range strengths {
		if got := networkStrengthIcon(strength); got != want {
			t.Fatalf("strength %d icon = %q, want %q", strength, got, want)
		}
	}
	types := map[string]string{
		"NR":    "5g",
		"LTE":   "4g-mobiledata",
		"LTE+":  "4g-mobiledata",
		"HSPAP": "3g-mobiledata",
		"EDGE":  "g-mobiledata",
		"":      "signal-cellular-null",
		"other": "signal-cellular-4-bar",
	}
	for raw, want := range types {
		if got := networkTypeIcon(raw); got != want {
			t.Fatalf("type %q icon = %q, want %q", raw, got, want)
		}
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
	// Composer variants validate too.
	if err := v1.Validate(PanelTree(pairedSnap(), testSettings(), ComposerShare, Drafts{}), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree(pairedSnap(), testSettings(), ComposerSMS, Drafts{}), v1.ViewPanel); err != nil {
		t.Fatal(err)
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
		if err := v1.Validate(PanelTree(snap, testSettings(), ComposerNone, Drafts{}), v1.ViewPanel); err != nil {
			t.Fatalf("panel state %d: %v", i, err)
		}
	}
}

func TestPanelTreeComposers(t *testing.T) {
	t.Parallel()
	none := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	if findInput(none, "share-text") != nil || findInput(none, "sms-number") != nil {
		t.Fatal("composer closed by default")
	}

	share := PanelTree(pairedSnap(), testSettings(), ComposerShare, Drafts{})
	if findInput(share, "share-text") == nil || findInput(share, "share-file") == nil {
		t.Fatal("share composer inputs missing")
	}
	if findButton(share, "share-close") == nil {
		t.Fatal("share composer close button missing")
	}
	if findInput(share, "sms-number") != nil {
		t.Fatal("sms composer visible with share open")
	}

	sms := PanelTree(pairedSnap(), testSettings(), ComposerSMS, Drafts{})
	body := findInput(sms, "sms-body")
	if body == nil || body.Multiline {
		t.Fatal("sms body missing or multiline; the DMS dialog is single-line")
	}
	if findButton(sms, "sms-close") == nil {
		t.Fatal("sms composer close button missing")
	}
	if findInput(sms, "share-text") != nil {
		t.Fatal("share composer visible with sms open")
	}
}

func TestShareComposerSendGating(t *testing.T) {
	t.Parallel()
	empty := PanelTree(pairedSnap(), testSettings(), ComposerShare, Drafts{})
	if b := findButton(empty, "share-url-send"); !b.Disabled {
		t.Fatal("send URL enabled with no draft")
	}
	if b := findButton(empty, "share-text-send"); !b.Disabled {
		t.Fatal("send text enabled with no draft")
	}
	if b := findButton(empty, "share-file-send"); !b.Disabled {
		t.Fatal("send file enabled with no draft")
	}

	urlDraft := PanelTree(pairedSnap(), testSettings(), ComposerShare, Drafts{ShareText: "https://example.com"})
	if b := findButton(urlDraft, "share-url-send"); b.Disabled {
		t.Fatal("send URL disabled for a valid URI")
	}
	if b := findButton(urlDraft, "share-text-send"); b.Disabled {
		t.Fatal("send text disabled for a non-empty draft")
	}

	textDraft := PanelTree(pairedSnap(), testSettings(), ComposerShare, Drafts{ShareText: "just words here"})
	if b := findButton(textDraft, "share-url-send"); !b.Disabled {
		t.Fatal("send URL enabled for prose")
	}
	if b := findButton(textDraft, "share-text-send"); b.Disabled {
		t.Fatal("send text disabled for prose")
	}

	fileDraft := PanelTree(pairedSnap(), testSettings(), ComposerShare, Drafts{ShareFile: "/home/me/photo.png"})
	if b := findButton(fileDraft, "share-file-send"); b.Disabled {
		t.Fatal("send file disabled with a path")
	}
}

func TestSMSComposerSendGating(t *testing.T) {
	t.Parallel()
	empty := PanelTree(pairedSnap(), testSettings(), ComposerSMS, Drafts{})
	if b := findButton(empty, "sms-send"); !b.Disabled {
		t.Fatal("send enabled with empty fields")
	}
	half := PanelTree(pairedSnap(), testSettings(), ComposerSMS, Drafts{SmsNumber: "+1 555"})
	if b := findButton(half, "sms-send"); !b.Disabled {
		t.Fatal("send enabled with an empty body")
	}
	ready := PanelTree(pairedSnap(), testSettings(), ComposerSMS, Drafts{SmsNumber: "+1 555", SmsBody: "hi"})
	if b := findButton(ready, "sms-send"); b.Disabled {
		t.Fatal("send disabled with both fields")
	}
}

func TestIsURILike(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"https://example.com": true,
		"mailto:someone@x":    true,
		"custom+scheme:rest":  true,
		"just words here":     false,
		"noscheme":            false,
		"":                    false,
		"http://x with space": false,
	}
	for in, want := range cases {
		if got := isURILike(in); got != want {
			t.Fatalf("isURILike(%q) = %v, want %v", in, got, want)
		}
	}
}

func findInput(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.Kind == v1.KindTextInput && n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if found := findInput(c, id); found != nil {
			return found
		}
	}
	return nil
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
