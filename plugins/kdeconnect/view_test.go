package kdeconnect

import (
	"os"
	"path/filepath"
	"strings"
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

// findImage walks for the image node carrying id.
func findImage(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.Kind == v1.KindImage && n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if found := findImage(c, id); found != nil {
			return found
		}
	}
	return nil
}

// recentSnap is the paired snapshot with a four-image grid on the selected
// device: three fill the first row, the fourth wraps.
func recentSnap() Snapshot {
	snap := pairedSnap()
	snap.RecentImages = []RecentImage{
		{ID: "aaaaaaaaaaaa", Source: "/mnt/phone/DCIM/a.jpg", Thumb: "/cache/thumbs/a.jpg"},
		{ID: "bbbbbbbbbbbb", Source: "/mnt/phone/DCIM/b.jpg", Thumb: "/cache/thumbs/b.jpg"},
		{ID: "cccccccccccc", Source: "/mnt/phone/DCIM/c.jpg", Thumb: "/cache/thumbs/c.jpg"},
		{ID: "dddddddddddd", Source: "/mnt/phone/DCIM/d.jpg", Thumb: "/cache/thumbs/d.jpg"},
	}
	return snap
}

func TestRecentImagesTreeGrid(t *testing.T) {
	t.Parallel()
	card := recentImagesTree(recentSnap())
	if card == nil || card.Fill != "card" {
		t.Fatalf("recent card = %+v", card)
	}
	if card.Children[0].Kind != v1.KindText || card.Children[0].Text != "Recent" {
		t.Fatalf("headline = %+v", card.Children[0])
	}
	// Four images wrap into a row of three plus a row of one.
	rows := card.Children[1:]
	if len(rows) != 2 || len(rows[0].Children) != 3 || len(rows[1].Children) != 1 {
		t.Fatalf("grid rows = %+v", rows)
	}
	for i, img := range recentSnap().RecentImages {
		var cell *v1.Node
		if i < 3 {
			cell = rows[0].Children[i]
		} else {
			cell = rows[1].Children[0]
		}
		image := findImage(cell, "recent-"+img.ID)
		if image == nil || image.Path != img.Thumb || image.ImageSize != 96 {
			t.Fatalf("image %d = %+v, want Path %q at size 96", i, image, img.Thumb)
		}
		open := findButton(cell, "recent-open-"+img.ID)
		if open == nil || open.Icon != "folder-open" || open.Role != "button" {
			t.Fatalf("open button %d = %+v", i, open)
		}
		share := findButton(cell, "recent-share-"+img.ID)
		if share == nil || share.Icon != "share" || share.Role != "button" {
			t.Fatalf("share button %d = %+v", i, share)
		}
	}
}

func TestRecentImagesTreeHiddenWhenEmpty(t *testing.T) {
	t.Parallel()
	if got := recentImagesTree(Snapshot{}); got != nil {
		t.Fatalf("empty grid = %+v, want nil", got)
	}
	plain := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	for _, child := range plain.Children {
		if len(child.Children) > 0 && child.Children[0].Kind == v1.KindText && child.Children[0].Text == "Recent" {
			t.Fatal("panel shows a recent card without images")
		}
	}
}

func TestPanelTreeShowsRecentGrid(t *testing.T) {
	t.Parallel()
	panel := PanelTree(recentSnap(), testSettings(), ComposerNone, Drafts{})
	var card *v1.Node
	for _, child := range panel.Children {
		if len(child.Children) > 0 && child.Children[0].Kind == v1.KindText && child.Children[0].Text == "Recent" {
			card = child
			break
		}
	}
	if card == nil {
		t.Fatal("panel lacks the recent card")
	}
	if findImage(card, "recent-aaaaaaaaaaaa") == nil ||
		findButton(card, "recent-open-aaaaaaaaaaaa") == nil ||
		findButton(card, "recent-share-aaaaaaaaaaaa") == nil {
		t.Fatal("recent card lacks its image and actions")
	}
}

func TestPanelDeltaFullSnapshotWhenTheGridMoves(t *testing.T) {
	t.Parallel()
	prev, next := recentSnap(), recentSnap()
	next.RecentImages = append([]RecentImage{{ID: "eeeeeeeeeeee",
		Source: "/mnt/phone/DCIM/e.jpg", Thumb: "/cache/thumbs/e.jpg"}}, next.RecentImages[:3]...)
	if got := PanelDelta(prev, next); got != nil {
		t.Fatal("a moved grid produced a patch instead of a full snapshot")
	}
	// The same grid with a moved reading still patches the readings.
	same := recentSnap()
	same.Devices[0].BatteryCharge = 50
	if got := PanelDelta(prev, same); got == nil {
		t.Fatal("an identical grid lost the reading patch")
	}
}

func TestBarTreeShowsOfflineState(t *testing.T) {
	t.Parallel()
	open := BarTree(Snapshot{}).Children[0]
	if open.ID != "open" || open.Name == "" || open.Role == "" {
		t.Fatalf("open control = %+v", open)
	}
	if open.Icon != "smartphone" || open.Text != "N/A" {
		t.Fatalf("offline pill = %q %q", open.Icon, open.Text)
	}
}

func TestBarTreeOmitsBatteryForSelectedDevice(t *testing.T) {
	t.Parallel()
	open := BarTree(pairedSnap()).Children[0]
	if open.Icon != "smartphone" {
		t.Fatalf("available glyph = %q", open.Icon)
	}
	if open.Text != "" {
		t.Fatalf("battery label = %q, want empty", open.Text)
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
	if offlinePill.Icon != "devices_other" || offlinePill.Text != "" {
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
	panel := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	if panel.Kind != v1.KindList {
		t.Fatalf("panel root kind = %q, want list", panel.Kind)
	}
	header := panel.Children[0]
	texts := headerTexts(header)
	if len(texts) != 2 || texts[0] != "KDE Connect" || texts[1] != "2 connected • 2 paired" {
		t.Fatalf("header texts = %v", texts)
	}
}

func TestPanelTreeUsesRecoveryForStaleSelection(t *testing.T) {
	t.Parallel()
	snap := pairedSnap()
	snap.SelectedID = "removed-device"
	panel := PanelTree(snap, testSettings(), ComposerNone, Drafts{})
	if findButton(panel, "device-switcher") == nil {
		t.Fatal("stale selection has no device recovery control")
	}
	if len(panel.Children) <= 1 {
		t.Fatal("stale selection rendered a header-only panel")
	}
}

func TestPanelTreeCollapsesDeviceSwitcher(t *testing.T) {
	t.Parallel()
	closed := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	if findButton(closed, "device-switcher") == nil {
		t.Fatal("closed panel has no device switcher control")
	}
	if findButton(closed, "select-devA") != nil || findButton(closed, "select-devB") != nil {
		t.Fatal("closed panel exposes every device card")
	}

	open := PanelTreeForState(pairedSnap(), testSettings(), ComposerNone, Drafts{}, true)
	selected := findButton(open, "select-devA")
	if selected == nil || !selected.Disabled || selected.Text != "Selected" {
		t.Fatalf("selected device switcher state = %+v", selected)
	}
	if findButton(open, "select-devB") == nil {
		t.Fatal("open panel omits the other device")
	}
}

func TestActionGroupUsesVisibleLabels(t *testing.T) {
	t.Parallel()
	panel := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	actions := findSection(panel, func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && n.Fill == "card" && contains(allTexts(n), "Actions")
	})
	if actions == nil {
		t.Fatal("grouped action section missing")
	}
	for _, id := range []string{"ring", "browse", "share", "sms"} {
		button := findButton(actions, id)
		if button == nil || strings.TrimSpace(button.Text) == "" {
			t.Fatalf("action %q has no visible label: %+v", id, button)
		}
	}
}

func TestStateCardsOfferRetryAndIcon(t *testing.T) {
	t.Parallel()
	for name, snap := range map[string]Snapshot{
		"unavailable": {},
		"empty":       {Available: true},
	} {
		card := PanelTree(snap, testSettings(), ComposerNone, Drafts{}).Children[1]
		if findButton(card, "retry") == nil {
			t.Errorf("%s state has no retry action", name)
		}
		if !walkFindNode(card, func(n *v1.Node) bool { return n.Kind == v1.KindIcon }) {
			t.Errorf("%s state has no state icon", name)
		}
	}
}

func TestPanelTreeUnavailableAndEmptyStates(t *testing.T) {
	t.Parallel()
	down := PanelTree(Snapshot{}, testSettings(), ComposerNone, Drafts{})
	if len(down.Children) != 2 || down.Children[1].Fill != "error-container" {
		t.Fatalf("unavailable panel = %+v", down)
	}
	if !contains(allTexts(down.Children[1]), "Phone Connect Not Available") {
		t.Fatalf("unavailable headline = %v", allTexts(down.Children[1]))
	}

	empty := PanelTree(Snapshot{Available: true}, testSettings(), ComposerNone, Drafts{})
	if len(empty.Children) != 2 || empty.Children[1].Fill != "card" {
		t.Fatalf("empty panel = %+v", empty)
	}
	if !contains(allTexts(empty.Children[1]), "No devices found") {
		t.Fatalf("empty headline = %v", allTexts(empty.Children[1]))
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
	if refresh.ID != "refresh" || refresh.Icon != "restart_alt" || refresh.Name == "" || refresh.Role == "" {
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

	panel := PanelTreeForState(pairedSnap(), testSettings(), ComposerNone, Drafts{}, true)
	if findButton(panel, "select-devB") == nil {
		t.Fatal("switcher card for the tablet missing")
	}
}

func TestDeviceCardChipsAndStatus(t *testing.T) {
	t.Parallel()
	chips := pairedSnap()
	chips.Devices[1].NetworkKnown = true
	chips.Devices[1].NetworkStrength = 3
	card := deviceCard(PanelTreeForState(chips, testSettings(), ComposerNone, Drafts{}, true), "Galaxy Tab")
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
	offlineCard := deviceCard(PanelTreeForState(offline, testSettings(), ComposerNone, Drafts{}, true), "Galaxy Tab")
	if offlineCard == nil || !contains(allTexts(offlineCard), "Offline") {
		t.Fatal("offline status missing")
	}

	pairing := pairedSnap()
	pairing.Devices[1].PairRequested = true
	pairCard := deviceCard(PanelTreeForState(pairing, testSettings(), ComposerNone, Drafts{}, true), "Galaxy Tab")
	if pairCard == nil || !contains(allTexts(pairCard), "Pairing...") {
		t.Fatal("pairing-in-progress status missing")
	}

	connected := deviceCard(PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{}), "Galaxy Tab")
	if contains(allTexts(connected), "Offline") || contains(allTexts(connected), "Not paired") {
		t.Fatalf("connected card grew a status line: %v", allTexts(connected))
	}
}

// The tap-to-ping card test rides the default asset resolution: under go
// test the binary's ../assets never exists, so the lead child is always the
// type icon. Pin that assumption here; a resolution change would surface as
// a lead-child mismatch in this test.
func TestDeviceCardTapToPing(t *testing.T) {
	t.Parallel()
	card := findButton(PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{}), "device-ping")
	if card == nil {
		t.Fatal("device-ping button card missing")
	}
	if card.Fill != "card" || card.Radius != 12 || card.Padding != 14 {
		t.Fatalf("device card chrome = fill=%q radius=%d padding=%d, want card/12/14",
			card.Fill, card.Radius, card.Padding)
	}
	if len(card.Events) != 1 || card.Events[0] != v1.EventActivate {
		t.Fatalf("device card events = %v, want [activate]", card.Events)
	}
	if len(card.Children[0].Children) != 5 {
		t.Fatalf("device card children = %d, want 5 (icon, name, status, cue, battery)", len(card.Children[0].Children))
	}
	wantKinds := []v1.NodeKind{v1.KindIcon, v1.KindText, v1.KindText, v1.KindText, v1.KindProgress}
	for i, want := range wantKinds {
		if card.Children[0].Children[i].Kind != want {
			t.Fatalf("child %d kind = %q, want %q", i, card.Children[0].Children[i].Kind, want)
		}
	}
	if card.Children[0].Children[1].Text != "Pixel 10 Pro XL" {
		t.Fatalf("name child text = %q, want Pixel 10 Pro XL", card.Children[0].Children[1].Text)
	}
}

// useMockupAssets points the mockup resolver at a temp directory holding a
// zero-byte PNG per named type — the plugin only stats the file, decoding
// is host-side — and restores the previous directory afterwards. The tests
// using it stay sequential: parallel tests read the package var while
// these run, and a write would race them.
func useMockupAssets(t *testing.T, types ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, typ := range types {
		if err := os.WriteFile(filepath.Join(dir, typ+".png"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prev := mockupAssetDir
	mockupAssetDir = dir
	t.Cleanup(func() { mockupAssetDir = prev })
	return dir
}

func TestDeviceMockupResolvesTypeAssets(t *testing.T) {
	dir := useMockupAssets(t, "phone", "tablet", "desktop", "laptop")
	cases := map[string]struct {
		file string
		w, h int
	}{
		"phone":      {"phone.png", 135, 260},
		"smartphone": {"phone.png", 135, 260},
		"tablet":     {"tablet.png", 180, 240},
		"desktop":    {"desktop.png", 260, 160},
		"computer":   {"desktop.png", 260, 160},
		"laptop":     {"laptop.png", 260, 170},
	}
	for typ, want := range cases {
		path, w, h, ok := deviceMockup(&Device{Type: typ})
		if !ok || path != filepath.Join(dir, want.file) || w != want.w || h != want.h {
			t.Fatalf("deviceMockup(%q) = %q,%d,%d,%v, want %s at %dx%d",
				typ, path, w, h, ok, want.file, want.w, want.h)
		}
	}
}

func TestDeviceMockupFallsBackWithoutAsset(t *testing.T) {
	useMockupAssets(t) // empty directory: nothing installed
	for _, typ := range []string{"phone", "tablet", "desktop", "laptop"} {
		if _, _, _, ok := deviceMockup(&Device{Type: typ}); ok {
			t.Fatalf("deviceMockup(%q) resolved without an asset", typ)
		}
	}
	useMockupAssets(t, "phone", "tablet", "desktop", "laptop")
	if _, _, _, ok := deviceMockup(&Device{Type: "tv"}); ok {
		t.Fatal("deviceMockup resolved an unknown type")
	}
	if _, _, _, ok := deviceMockup(nil); ok {
		t.Fatal("deviceMockup resolved a nil device")
	}
}

func TestDeviceCardShowsMockupWhenAssetResolves(t *testing.T) {
	dir := useMockupAssets(t, "phone", "tablet")
	dev := &Device{ID: "devA", Name: "Pixel 10 Pro XL", Type: "phone",
		Paired: true, Reachable: true, BatteryKnown: true, BatteryCharge: 98}
	card := deviceCardTree(dev)
	mock := card.Children[0].Children[0]
	if mock.Kind != v1.KindImage {
		t.Fatalf("lead child = %+v, want the mockup image", mock)
	}
	if mock.Path != filepath.Join(dir, "phone.png") || mock.ImageW != 135 || mock.ImageH != 260 ||
		!mock.Background || mock.Shape != "card" || !mock.CenterX {
		t.Fatalf("mockup node = %+v", mock)
	}
	if len(card.Children[0].Children) != 5 {
		t.Fatalf("device card children = %d, want 5 (mockup, name, status, cue, battery)", len(card.Children[0].Children))
	}

	// The mockup-bearing card rides the panel, so it must stay wire-legal
	// for both mockup-carrying types.
	snap := pairedSnap()
	if err := v1.Validate(PanelTree(snap, testSettings(), ComposerNone, Drafts{}), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	snap.SelectedID = "devB"
	if err := v1.Validate(PanelTree(snap, testSettings(), ComposerNone, Drafts{}), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceCardKeepsIconWithoutAsset(t *testing.T) {
	useMockupAssets(t) // empty directory: the icon fallback
	card := deviceCardTree(&Device{ID: "devA", Name: "Pixel 10 Pro XL", Type: "phone"})
	if card.Children[0].Children[0].Kind != v1.KindIcon || card.Children[0].Children[0].Icon != "smartphone" {
		t.Fatalf("lead child = %+v, want the smartphone icon", card.Children[0].Children[0])
	}
}

func TestDeviceCardPairingActionsPerCard(t *testing.T) {
	t.Parallel()
	panel := PanelTreeForState(pairedSnap(), testSettings(), ComposerNone, Drafts{}, true)
	if findButton(panel, "pair-devB") != nil || findButton(panel, "accept-devB") != nil {
		t.Fatal("pairing actions on a paired, reachable card")
	}

	unpaired := pairedSnap()
	unpaired.Devices[1].Paired = false
	if findButton(PanelTreeForState(unpaired, testSettings(), ComposerNone, Drafts{}, true), "pair-devB") == nil {
		t.Fatal("request-pairing action missing on an unpaired card")
	}

	incoming := pairedSnap()
	incoming.Devices[1].PairRequestedByPeer = true
	incoming.Devices[1].VerificationKey = "999999"
	inPanel := PanelTreeForState(incoming, testSettings(), ComposerNone, Drafts{}, true)
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

func TestPairingCardIcons(t *testing.T) {
	t.Parallel()
	incoming := pairedSnap()
	incoming.Devices[0].PairRequestedByPeer = true
	incoming.Devices[0].VerificationKey = "123456"
	card := PanelTree(incoming, testSettings(), ComposerNone, Drafts{}).Children[1]
	if b := findButton(card, "pair-accept"); b == nil || b.Icon != "check" {
		t.Fatalf("pair-accept icon = %q, want check", iconOf(b))
	}
	if b := findButton(card, "pair-reject"); b == nil || b.Icon != "close" {
		t.Fatalf("pair-reject icon = %q, want close", iconOf(b))
	}

	outgoing := pairedSnap()
	outgoing.Devices[0].PairRequested = true
	outCard := PanelTree(outgoing, testSettings(), ComposerNone, Drafts{}).Children[1]
	if b := findButton(outCard, "pair-cancel"); b == nil || b.Icon != "close" {
		t.Fatalf("pair-cancel icon = %q, want close", iconOf(b))
	}
}

func TestPairingUnpairedCardIcon(t *testing.T) {
	t.Parallel()
	snap := pairedSnap()
	snap.Devices[0].Paired = false
	card := PanelTree(snap, testSettings(), ComposerNone, Drafts{}).Children[1]
	if b := findButton(card, "pair"); b == nil || b.Icon != "link" {
		t.Fatalf("pair icon = %q, want link", iconOf(b))
	}
}

func TestPairingComposerSendIcons(t *testing.T) {
	t.Parallel()
	share := PanelTree(pairedSnap(), testSettings(), ComposerShare, Drafts{ShareText: "https://example.com", ShareFile: "/tmp/x"})
	for _, id := range []string{"share-url-send", "share-text-send", "share-file-send"} {
		if b := findButton(share, id); b == nil || b.Icon != "send" {
			t.Fatalf("%s icon = %q, want send", id, iconOf(b))
		}
	}
	sms := PanelTree(pairedSnap(), testSettings(), ComposerSMS, Drafts{SmsNumber: "+1", SmsBody: "hi"})
	if b := findButton(sms, "sms-send"); b == nil || b.Icon != "send" {
		t.Fatalf("sms-send icon = %q, want send", iconOf(b))
	}
}

// iconOf returns the button's icon or a placeholder when missing, so a
// nil button still prints as something readable.
func iconOf(b *v1.Node) string {
	if b == nil {
		return "<missing>"
	}
	return b.Icon
}

func TestActionRowGating(t *testing.T) {
	t.Parallel()
	panel := PanelTree(pairedSnap(), testSettings(), ComposerNone, Drafts{})
	actions := findSection(panel, func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && n.Fill == "card" && contains(allTexts(n), "Actions")
	})
	if actions == nil {
		t.Fatal("action row missing")
	}
	for _, b := range buttonsIn(actions) {
		if b.Disabled {
			t.Fatalf("%s disabled with every capability present", b.ID)
		}
	}

	noClipboard := testSettings()
	noClipboard.EnableClipboard = false
	trimmed := findSection(PanelTree(pairedSnap(), noClipboard, ComposerNone, Drafts{}), func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && n.Fill == "card" && contains(allTexts(n), "Actions")
	})
	if b := findButton(trimmed, "clipboard"); b == nil || !b.Disabled {
		t.Fatal("clipboard action enabled with the setting off")
	}

	offline := pairedSnap()
	offline.Devices[0].Reachable = false
	for _, b := range buttonsIn(findSection(PanelTree(offline, testSettings(), ComposerNone, Drafts{}), func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && n.Fill == "card" && contains(allTexts(n), "Actions")
	})) {
		if !b.Disabled {
			t.Fatalf("%s enabled while offline", b.ID)
		}
	}

	uncapable := pairedSnap()
	uncapable.Devices[0].SupportedPlugins = nil
	for _, b := range buttonsIn(findSection(PanelTree(uncapable, testSettings(), ComposerNone, Drafts{}), func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && n.Fill == "card" && contains(allTexts(n), "Actions")
	})) {
		if !b.Disabled {
			t.Fatalf("%s enabled without capabilities", b.ID)
		}
	}
}

func TestActionRowRestoresPingWithoutTheCard(t *testing.T) {
	t.Parallel()
	plain := testSettings()
	plain.ShowDeviceCard = false
	actions := findSection(PanelTree(pairedSnap(), plain, ComposerNone, Drafts{}), func(n *v1.Node) bool {
		return n.Kind == v1.KindColumn && n.Fill == "card" && contains(allTexts(n), "Actions")
	})
	if actions == nil {
		t.Fatal("action row without the device card missing")
	}
	if ping := findButton(actions, "ping"); ping == nil || ping.Disabled {
		t.Fatalf("ping button = %+v, want enabled at index 1", ping)
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

	// The recent-images grid rides the panel too: its image nodes must be
	// wire-legal (absolute cached path, one box form) inside the full tree.
	states = append(states, recentSnap())

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

func buttonsIn(n *v1.Node) []*v1.Node {
	if n == nil {
		return nil
	}
	var out []*v1.Node
	if n.Kind == v1.KindButton {
		out = append(out, n)
	}
	for _, c := range n.Children {
		out = append(out, buttonsIn(c)...)
	}
	return out
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

func TestDeviceCardUsesVerticalContent(t *testing.T) {
	snap := pairedSnap()
	card := deviceCardTree(&snap.Devices[0])
	if len(card.Children) != 1 || card.Children[0].Kind != v1.KindColumn {
		t.Fatal("device artwork and labels must stack inside the button")
	}
}

func TestBarTreeChargingFill(t *testing.T) {
	t.Parallel()
	snap := pairedSnap()
	snap.Devices[0].BatteryCharging = true
	pill := BarTree(snap).Children[0]
	if len(pill.Children) != 2 {
		t.Fatalf("charging pill children = %d, want fill then icon", len(pill.Children))
	}
	fill := pill.Children[0]
	if fill.Kind != v1.KindProgress || fill.Key != "kdeconnect-pill" || !fill.Animate {
		t.Fatalf("charging fill = %+v", fill)
	}
	if fill.Tone != v1.ToneAccent || fill.Value != 0.98 {
		t.Fatalf("charging fill tone/value = %q %v", fill.Tone, fill.Value)
	}
	if pill.Children[1].Kind != v1.KindIcon || pill.Children[1].Icon != "smartphone" {
		t.Fatalf("charging pill icon = %+v", pill.Children[1])
	}
	low := pairedSnap()
	low.Devices[0].BatteryCharging = true
	low.Devices[0].BatteryCharge = 12
	if fill := BarTree(low).Children[0].Children[0]; fill.Tone != v1.ToneError {
		t.Fatalf("low charging fill tone = %q, want error", fill.Tone)
	}
	idle := BarTree(pairedSnap()).Children[0]
	if len(idle.Children) != 0 || idle.Icon != "smartphone" {
		t.Fatalf("idle pill changed: %+v", idle)
	}
}

func TestBatteryProgressAnimates(t *testing.T) {
	t.Parallel()
	p := batteryProgressNode(&Device{BatteryKnown: true, BatteryCharge: 40})
	if p == nil || !p.Animate || p.Key != "battery-progress" {
		t.Fatalf("battery progress = %+v, want an animated keyed node", p)
	}
}
