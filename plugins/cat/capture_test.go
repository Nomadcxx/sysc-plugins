package cat

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/cat/screenshot.png when CAPTURE=1: the real
// cat fed a light, wavering CPU load, with a full history behind the sparkline.
func TestCapturePanel(t *testing.T) {
	s := DefaultSettings()
	c := New(s.Bands, s.NapAfter, 7)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var h History
	for i := 0; i < HistoryLen; i++ {
		load := 0.14 + 0.2*float64(i%12)/12
		c.Observe(load, now)
		h.Push(load)
		now = now.Add(s.SampleEvery)
	}
	capture.Panel(t, "cat", PanelTree(FrameOf(c), s, h.Values(nil)))
}
