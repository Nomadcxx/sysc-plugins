package bitsub

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/bitsub/screenshot.png when CAPTURE=1: the
// view test fixture, a trial ending in three days and a mix of intents.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "bitsub", Panel(state()))
}
