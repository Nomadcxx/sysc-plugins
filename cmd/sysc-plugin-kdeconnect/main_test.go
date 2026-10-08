package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	"github.com/Nomadcxx/sysc-plugins/plugins/kdeconnect"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestSettingsFromAppliesKnownKeys(t *testing.T) {
	t.Parallel()
	s := settingsFrom(map[string]any{
		"refresh_seconds":         float64(60),
		"enable_clipboard_action": false,
		"show_device_card":        false,
		"recent_images_path":      "DCIM/Camera",
		"max_recent_images":       float64(9),
		"scan_subdirectories":     true,
		"unknown":                 "ignored",
	})
	if s.RefreshSeconds != 60 || s.EnableClipboard || s.ShowDeviceCard {
		t.Fatalf("settings = %+v", s)
	}
	if s.RecentImagesPath != "DCIM/Camera" || s.MaxRecentImages != 9 || !s.ScanSubdirectories {
		t.Fatalf("recent-image settings = %+v", s)
	}
}

func TestSettingsFromFallsBackToDefaults(t *testing.T) {
	t.Parallel()
	if s := settingsFrom(map[string]any{}); s != kdeconnect.DefaultSettings() {
		t.Fatalf("empty settings = %+v, want the defaults", s)
	}
	if s := settingsFrom(map[string]any{"refresh_seconds": "not a number"}); s.RefreshSeconds != 30 {
		t.Fatalf("malformed refresh_seconds = %+v, want the default", s)
	}
}

func TestShareKindFor(t *testing.T) {
	t.Parallel()
	if got := shareKindFor("https://example.com"); got != kdeconnect.ActionShareURL {
		t.Fatalf("https = %v", got)
	}
	if got := shareKindFor("http://example.com"); got != kdeconnect.ActionShareURL {
		t.Fatalf("http = %v", got)
	}
	if got := shareKindFor("just some text"); got != kdeconnect.ActionShareText {
		t.Fatalf("text = %v", got)
	}
}

func TestOpenOnlyRespondsToActivation(t *testing.T) {
	t.Parallel()
	call := func(event v1.EventKind) (v1.HostCall, bool) {
		var out bytes.Buffer
		c := v1.NewClient(bytes.NewReader(nil), &out)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		done := make(chan struct{})
		go func() {
			handleInput(ctx, c, nil, &v1.InputEvent{Node: "open", Event: event}, nil, kdeconnect.Snapshot{}, new(bool), nil)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("open input did not return")
		}
		if out.Len() == 0 {
			return v1.HostCall{}, false
		}
		msg, err := v1.NewDecoder(bytes.NewReader(out.Bytes()), v1.ToHost).Decode()
		if err != nil {
			t.Fatalf("decode host call: %v", err)
		}
		hc, ok := msg.(*v1.HostCall)
		if !ok {
			t.Fatalf("message = %T, want host call", msg)
		}
		return *hc, true
	}

	if _, ok := call(v1.EventPointer); ok {
		t.Fatal("pointer event opened the panel")
	}
	hc, ok := call(v1.EventActivate)
	if !ok || hc.Call != v1.CallPanelOpen {
		t.Fatalf("activation call = %+v, present=%v", hc, ok)
	}
	var params v1.PanelParams
	if err := json.Unmarshal(hc.Params, &params); err != nil {
		t.Fatalf("panel params: %v", err)
	}
	if params.Entry != "panel" {
		t.Fatalf("panel entry = %q, want panel", params.Entry)
	}
}

func TestDeviceSwitcherActivationTogglesPanelState(t *testing.T) {
	t.Parallel()
	ui := uiState{}
	busy := false
	msg := &v1.InputEvent{Node: "device-switcher", Event: v1.EventActivate}
	if !handleInput(context.Background(), nil, nil, msg, &ui, kdeconnect.Snapshot{}, &busy, nil) || !ui.switcherOpen {
		t.Fatal("activating the device switcher did not open it")
	}
	if !handleInput(context.Background(), nil, nil, msg, &ui, kdeconnect.Snapshot{}, &busy, nil) || ui.switcherOpen {
		t.Fatal("activating the device switcher did not close it")
	}
	msg.Event = v1.EventPointer
	if handleInput(context.Background(), nil, nil, msg, &ui, kdeconnect.Snapshot{}, &busy, nil) || ui.switcherOpen {
		t.Fatal("pointer input changed the device switcher")
	}
}

// The panel tree derives every fixed width from kdeconnect.PanelWidth, so
// the manifest must declare that same width or the cells overflow the panel.
func TestManifestPanelWidthMatchesTheView(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../plugins/kdeconnect/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Panels []struct {
			ID     string `json:"id"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Panels) != 1 || manifest.Panels[0].Width != kdeconnect.PanelWidth ||
		manifest.Panels[0].Height != kdeconnect.PanelHeight {
		t.Fatalf("manifest panels = %+v, want one panel %dx%d", manifest.Panels, kdeconnect.PanelWidth, kdeconnect.PanelHeight)
	}
}

func TestComposerFocusTargetsTheFirstField(t *testing.T) {
	t.Parallel()
	if node, ok := composerFocus(kdeconnect.ComposerNone, kdeconnect.ComposerShare); !ok || node != "share-text" {
		t.Fatalf("share focus = %q, %v, want share-text, true", node, ok)
	}
	if node, ok := composerFocus(kdeconnect.ComposerNone, kdeconnect.ComposerSMS); !ok || node != "sms-number" {
		t.Fatalf("sms focus = %q, %v, want sms-number, true", node, ok)
	}
	if _, ok := composerFocus(kdeconnect.ComposerShare, kdeconnect.ComposerNone); ok {
		t.Fatal("closing the composer issued a focus")
	}
	if _, ok := composerFocus(kdeconnect.ComposerShare, kdeconnect.ComposerShare); ok {
		t.Fatal("an unchanged composer issued a focus")
	}
}

func TestActionNodesCoverEveryActionButton(t *testing.T) {
	panel := kdeconnect.PanelTree(kdeconnect.Snapshot{
		Available: true, SelectedID: "dev1",
		Devices: []kdeconnect.Device{{
			ID: "dev1", Name: "Phone", Type: "phone",
			Reachable: true, Paired: true,
			SupportedPlugins: []string{"findmyphone", "ping", "sftp", "clipboard", "share", "sms"},
		}},
	}, kdeconnect.Settings{}, kdeconnect.ComposerNone, kdeconnect.Drafts{})
	seen := map[string]bool{}
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		seen[n.ID] = true
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(panel)
	for id := range actionNodes {
		if id == "device-ping" {
			continue // the tap-to-ping card alias, not an action-row button
		}
		if !seen[id] {
			t.Fatalf("tree has no %q button for its action", id)
		}
	}
	if _, ok := actionNodes["share"]; ok {
		t.Fatal("share must stay a composer toggle")
	}
	if _, ok := actionNodes["sms"]; ok {
		t.Fatal("sms must stay a composer toggle")
	}
}

func TestRecentImageStagingRunsOffCaller(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	results := make(chan recentImageResult, 1)
	returned := make(chan struct{})
	go func() {
		startRecentImageAction(context.Background(), results, recentImageResult{}, func(kdeconnect.RecentImage) (string, error) {
			close(started)
			<-release
			return "/tmp/staged.png", nil
		})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("recent image staging blocked its caller")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("staging worker did not start")
	}
	select {
	case <-results:
		t.Fatal("staging result arrived before the worker completed")
	default:
	}
	close(release)
	select {
	case result := <-results:
		if result.path != "/tmp/staged.png" || result.err != nil {
			t.Fatalf("staging result = {path: %q, err: %v}", result.path, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("staging worker did not report its result")
	}
}

func TestFilesBrowseParamsFromSuccessfulBrowse(t *testing.T) {
	t.Parallel()
	params, ok := filesBrowseParams(kdeconnect.Event{
		Kind: kdeconnect.EventActionResult, DeviceName: "Pixel", Path: "/run/user/1000/phone",
	})
	if !ok || params.Root != "/run/user/1000/phone" || params.Mode != "open" || params.Title != "Pixel" {
		t.Fatalf("params = %+v ok=%v", params, ok)
	}
	if _, ok := filesBrowseParams(kdeconnect.Event{Kind: kdeconnect.EventActionResult, Path: "/x", Err: os.ErrPermission}); ok {
		t.Fatal("failed browse produced files.browse params")
	}
	if _, ok := filesBrowseParams(kdeconnect.Event{Kind: kdeconnect.EventActionResult}); ok {
		t.Fatal("browse without a mount produced files.browse params")
	}
}

// The pairing card renders "pair-accept", "pair-reject" and "pair-cancel",
// while a device card renders "pair-<deviceID>" to request pairing. All four
// share the "pair-" prefix, so a prefix match alone turns the card's three
// buttons into a pairing request for a device named "accept".
func TestPairingCardButtonsRouteToTheirOwnAction(t *testing.T) {
	tests := []struct {
		node     string
		wantKind kdeconnect.ActionKind
		wantID   string
	}{
		{"pair-accept", kdeconnect.ActionAcceptPair, "dev1"},
		{"pair-reject", kdeconnect.ActionRejectPair, "dev1"},
		{"pair-dev2", kdeconnect.ActionPair, "dev2"},
	}
	for _, tt := range tests {
		got, ok := pairingAction(tt.node, "dev1")
		if !ok {
			t.Errorf("pairingAction(%q) routed nothing", tt.node)
			continue
		}
		if got.Kind != tt.wantKind || got.DeviceID != tt.wantID {
			t.Errorf("pairingAction(%q) = {%v %q}, want {%v %q}",
				tt.node, got.Kind, got.DeviceID, tt.wantKind, tt.wantID)
		}
	}

	// Cancel withdraws our own request, which has no daemon call. It must
	// not become a pairing request aimed at a device named "cancel".
	if got, ok := pairingAction("pair-cancel", "dev1"); ok {
		t.Errorf("pairingAction(%q) = {%v %q}, want no action", "pair-cancel", got.Kind, got.DeviceID)
	}
}

func TestReplyBehindInputBurstDoesNotWedge(t *testing.T) {
	orig := hostcall.Default
	hostcall.Default = 400 * time.Millisecond
	defer func() { hostcall.Default = orig }()

	toPluginR, hostOutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	hostInR, pluginOutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer toPluginR.Close()
	defer hostOutW.Close()
	defer hostInR.Close()
	defer pluginOutW.Close()

	go func() { _ = run(toPluginR, pluginOutW) }()

	var sendMu sync.Mutex
	send := func(m v1.Message) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return v1.NewEncoder(hostOutW).Encode(m)
	}

	held := make(chan struct{}, 1)
	snapshotted := make(chan struct{}, 1)
	go func() {
		dec := v1.NewDecoder(hostInR, v1.ToHost)
		for {
			m, err := dec.Decode()
			if err != nil {
				return
			}
			switch msg := m.(type) {
			case *v1.HostCall:
				if msg.Call == v1.CallPanelOpen {
					select {
					case held <- struct{}{}:
					default:
					}
					continue
				}
				var result json.RawMessage
				if msg.Call == v1.CallStateGet {
					result, _ = json.Marshal(v1.StateGetResult{Found: false})
				}
				_ = send(&v1.HostReply{ID: msg.ID, OK: true, Result: result})
			case *v1.ViewSnapshot:
				if msg.ViewID == "bar-after" {
					select {
					case snapshotted <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	hello := &v1.HostHello{
		Supported:    []v1.Version{{Major: v1.ProtocolMajor, Minor: v1.ProtocolMinor}},
		Plugin:       v1.Identity{ID: "org.sysc.kdeconnect", Name: "Phone Connect", Version: "0.1.0"},
		Capabilities: []string{"notifications", "panels", "settings", "state"},
		Limits:       v1.DefaultLimits,
	}
	if err := send(hello); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	if err := send(&v1.InputEvent{ViewID: "panel", Node: "open", Event: v1.EventActivate}); err != nil {
		t.Fatalf("send open: %v", err)
	}
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("plugin never reached the panel.open call")
	}

	go func() {
		for i := 0; i < 10; i++ {
			_ = send(&v1.InputEvent{Node: "sms-body", Event: v1.EventChange})
		}
		_ = send(&v1.ViewOpen{ViewID: "bar-after", View: v1.ViewBar, Width: 200})
	}()

	select {
	case <-snapshotted:
	case <-time.After(3 * time.Second):
		t.Fatal("plugin is wedged: it never rendered the view opened after the input burst")
	}
}
