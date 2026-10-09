package faith

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/faith/screenshot.png when CAPTURE=1: Psalm
// 23:1 with three cross-references and both neighbours enabled.
func TestCapturePanel(t *testing.T) {
	var xrefs []Ref
	for _, s := range []string{"ISA 40:11", "JHN 10:11", "REV 7:17"} {
		ref, err := ParseRef(s)
		if err != nil {
			t.Fatal(err)
		}
		xrefs = append(xrefs, ref)
	}
	capture.Panel(t, "faith", PanelTree(PanelModel{
		Ref:         Ref{Book: 18, Chapter: 23, Verse: 1},
		Translation: "WEB",
		Verse:       "The LORD is my shepherd; I shall not want.",
		CanPrev:     true, CanNext: true, CanRead: true,
		Commentary: CommentaryNone,
		Xrefs:      xrefs,
	}))
}
