package capture

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Gradient writes a w by h PNG that fades diagonally from one colour to the
// other into the test's temp directory and returns its path. Scenes use it for
// the pictures a panel shows (photo thumbnails, cover art), so no artwork is
// committed and no real picture leaks into a public screenshot.
func Gradient(t testing.TB, w, h int, from, to color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	span := max(w+h-2, 1)
	mix := func(a, b uint8, step int) uint8 { return uint8((int(a)*(span-step) + int(b)*step) / span) }
	for y := range h {
		for x := range w {
			s := x + y
			img.SetNRGBA(x, y, color.NRGBA{mix(from.R, to.R, s), mix(from.G, to.G, s), mix(from.B, to.B, s), 255})
		}
	}
	path := filepath.Join(t.TempDir(), "gradient-"+strconv.Itoa(w)+"x"+strconv.Itoa(h)+".png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}
