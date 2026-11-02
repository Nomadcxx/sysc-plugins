package lidguard

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// TestCapturePanel renders the enabled service with fictional status only.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "lid-guard", PanelTree(Snapshot{
		State:     v1.ServiceStateActive,
		Available: true,
	}))
}
