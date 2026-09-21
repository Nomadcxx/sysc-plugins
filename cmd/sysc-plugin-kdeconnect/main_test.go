package main

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/kdeconnect"
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
