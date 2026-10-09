package kdeconnect

import (
	"image/color"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/kdeconnect/screenshot.png when CAPTURE=1: a
// paired, reachable phone with its readings known, a tablet behind it, and
// four recent photos.
func TestCapturePanel(t *testing.T) {
	assets, err := filepath.Abs("assets")
	if err != nil {
		t.Fatal(err)
	}
	prev := mockupAssetDir
	mockupAssetDir = assets
	t.Cleanup(func() { mockupAssetDir = prev })

	hues := []color.NRGBA{{R: 230, G: 140, B: 60, A: 255}, {R: 60, G: 160, B: 200, A: 255}, {R: 120, G: 190, B: 90, A: 255}, {R: 190, G: 90, B: 170, A: 255}}
	var recent []RecentImage
	for i, hue := range hues {
		recent = append(recent, RecentImage{
			ID: strconv.Itoa(i), Source: "/DCIM/photo-" + strconv.Itoa(i) + ".jpg",
			Thumb: capture.Gradient(t, 96, 96, hue, color.NRGBA{R: 30, G: 30, B: 50, A: 255}),
		})
	}
	snap := Snapshot{
		Available: true, BackendName: "KDE Connect", AnnouncedName: "Demo Desktop",
		SelectedID: "demo-phone", RecentImages: recent,
		Devices: []Device{
			{
				ID: "demo-phone", Name: "Demo Phone", Type: "phone", Reachable: true, Paired: true,
				SupportedPlugins: []string{"kdeconnect_battery", "findmyphone", "ping", "sftp", "clipboard", "share", "sms", "connectivity_report", "notifications"},
				BatteryCharge:    78, BatteryKnown: true, NetworkType: "LTE", NetworkStrength: 3, NetworkKnown: true,
				NotificationCount: 2, NotificationsKnown: true,
			},
			{ID: "demo-tablet", Name: "Demo Tablet", Type: "tablet", Reachable: true, Paired: true, BatteryKnown: true, BatteryCharge: 55, BatteryCharging: true},
		},
	}
	capture.Panel(t, "kdeconnect", PanelTree(snap, DefaultSettings(), ComposerNone, Drafts{}))
}
