package timer

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/timer/screenshot.png when CAPTURE=1: a work
// session in progress, two of four pomodoros done.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "timer", PanelTree("18:42", StateRunning, 0.75, ModeWork, 2, 4))
}
