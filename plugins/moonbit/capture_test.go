package moonbit

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/moonbit/screenshot.png when CAPTURE=1: a
// finished scan under review, every category with files selected.
func TestCapturePanel(t *testing.T) {
	cats := []CategoryStat{
		{Name: "Pacman Cache", Files: 412, Bytes: 6_400_000_000},
		{Name: "npm Cache", Files: 2310, Bytes: 1_900_000_000},
		{Name: "Journal Logs", Files: 38, Bytes: 820_000_000},
		{Name: "Thumbnail Cache", Files: 5120, Bytes: 310_000_000},
		{Name: "Browser Cache", Files: 1740, Bytes: 640_000_000},
		{Name: "Trash", Files: 0, Bytes: 0},
	}
	s := State{Phase: PhaseReview, ScannedAt: "2026-10-09T12:00:00Z", Selected: map[string]bool{}}
	for _, c := range cats {
		if c.Files > 0 {
			s.Review = append(s.Review, c)
			s.Selected[c.Name] = true
		}
	}
	capture.Panel(t, "moonbit", Panel(&s))
}
