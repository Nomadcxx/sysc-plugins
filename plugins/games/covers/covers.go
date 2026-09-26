// Package covers guarantees a card never renders blank: real art if it
// exists, a deterministic generated tile otherwise.
package covers

import (
	"hash/fnv"

	"golang.org/x/image/font"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"golang.org/x/image/draw"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var palette = []color.RGBA{
	{40, 53, 74, 255}, {62, 39, 35, 255}, {24, 64, 56, 255}, {58, 42, 74, 255},
	{28, 60, 90, 255}, {80, 48, 26, 255}, {48, 32, 64, 255}, {30, 70, 70, 255},
}

// Resolve returns an absolute image path for the card: the source's local
// art when the file still exists, else a cached/generated initials tile.
func Resolve(g source.Game, cacheDir string) string {
	if g.CoverPath != "" {
		if _, err := os.Stat(g.CoverPath); err == nil {
			return g.CoverPath
		}
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(g.Slug)
	if safe == "" {
		safe = "game"
	}
	tile := filepath.Join(cacheDir, safe+".png")
	if _, err := os.Stat(tile); err == nil {
		return tile
	}
	if err := GenerateTile(g.Name, tile); err != nil {
		return ""
	}
	return tile
}

// GenerateTile draws a 300x400 poster tile with two-letter initials,
// deterministic per name (color picked from an FNV hash).
func GenerateTile(name, path string) error {
	const scale = 4
	small := image.NewRGBA(image.Rect(0, 0, 300/scale, 400/scale))
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	bg := palette[int(h.Sum32())%len(palette)]
	for y := small.Bounds().Min.Y; y < small.Bounds().Max.Y; y++ {
		for x := small.Bounds().Min.X; x < small.Bounds().Max.X; x++ {
			small.Set(x, y, bg)
		}
	}
	text := initials(name)
	d := &font.Drawer{
		Dst:  small,
		Src:  image.NewUniform(color.White),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(75/2-len(text)*7/2, 50+5),
	}
	d.DrawString(text)

	big := image.NewRGBA(image.Rect(0, 0, 300, 400))
	draw.NearestNeighbor.Scale(big, big.Bounds(), small, small.Bounds(), draw.Over, nil)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	fh, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := png.Encode(fh, big); err != nil {
		fh.Close()
		os.Remove(tmp)
		return err
	}
	if err := fh.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// initials picks the leading letters of the first two words, uppercased;
// digits are not word starts. Stop words stay — a name list is the kind of
// abstraction that never survives first contact with a game library.
func initials(name string) string {
	var out []byte
	for _, w := range strings.Fields(name) {
		for _, r := range w {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				out = append(out, byte(strings.ToUpper(string(r))[0]))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}
