package screenshot

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/screenshot/screenshot.png when CAPTURE=1.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "screenshot", PanelTree(Model{Directory: "~/Pictures/Screenshots"}))
}
