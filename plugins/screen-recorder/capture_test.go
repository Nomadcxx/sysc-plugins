package recorder

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/screen-recorder/screenshot.png when
// CAPTURE=1: a recording four minutes in, with the replay buffer enabled.
func TestCapturePanel(t *testing.T) {
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ReplayEnabled = true
	snap := Snapshot{Mode: Recording, Elapsed: 4*time.Minute + 12*time.Second}
	capture.Panel(t, "screen-recorder", PanelTree(snap, cfg, time.Time{}))
}
