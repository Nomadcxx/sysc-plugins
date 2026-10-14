package updates

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/updates/screenshot.png when CAPTURE=1: a
// mixed update list with kernel, NVIDIA, base-system and application updates.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 45, 0, 0, time.UTC)
	capture.Panel(t, "updates", PanelTree(State{
		Updates: []Update{
			{Name: "linux", Old: "6.17.1.arch1-1", New: "6.17.2.arch1-1", Source: SourceRepo, Core: true},
			{Name: "nvidia-dkms", Old: "580.95-1", New: "580.101-1", Source: SourceRepo, Core: true},
			{Name: "systemd", Old: "258-1", New: "258.1-1", Source: SourceRepo, Core: true},
			{Name: "firefox", Old: "143.0.1-1", New: "143.0.2-1", Source: SourceRepo},
			{Name: "paru-bin", Old: "2.0.4-1", New: "2.0.5-1", Source: SourceAUR},
			{Name: "org.mozilla.firefox", New: "143.0.2", Source: SourceFlatpak},
		},
		CheckedAt:    now.Add(-3 * time.Minute),
		RebootNeeded: true,
		RebootDetail: "linux 6.17.2.arch1-1 is installed, 6.16.9-arch1-1 is running",
	}, now, ""))
}
