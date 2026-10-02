package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

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
			ID    string `json:"id"`
			Width int    `json:"width"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Panels) != 1 || manifest.Panels[0].Width != kdeconnect.PanelWidth {
		t.Fatalf("manifest panels = %+v, want one panel %d wide", manifest.Panels, kdeconnect.PanelWidth)
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
