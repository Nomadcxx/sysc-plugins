package wallpaperdepth

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/wallpaper-depth/screenshot.png when
// CAPTURE=1: a ready helper with two outputs, one generated and cached.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "wallpaper-depth", PanelTree(ControllerSnapshot{
		Helper:   HelperStatus{Ready: true, RuntimeReady: true, ModelReady: true},
		Checked:  true,
		Settings: Settings{AutoGenerate: true, Threshold: 55, Feather: 8},
		Rows: []OutputRow{
			{Output: "DP-1", State: "image", WallpaperPath: "/wallpapers/mountains.jpg", Status: "ready", CacheHit: true, ElapsedMs: 840},
			{Output: "HDMI-A-1", State: "image", WallpaperPath: "/wallpapers/harbour.jpg", Status: "ready", ElapsedMs: 2300},
			{Output: "eDP-1", State: "video", Status: "unsupported"},
		},
	}))
}
