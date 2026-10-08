package thumbnail

import (
	"image"
	"image/color"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Palette, taken from the sysc-shell theme the approved mock used.
var (
	colBackground = color.NRGBA{0x0d, 0x10, 0x19, 0xff}
	colFrameFill  = color.NRGBA{0x09, 0x0b, 0x12, 0xff}
	colBorder     = color.NRGBA{0x2c, 0x35, 0x4f, 0xff}
	colBlue       = color.NRGBA{0x1f, 0x8b, 0xff, 0xff}
	colInk        = color.NRGBA{0xee, 0xf2, 0xfa, 0xff}
	colMuted      = color.NRGBA{0x8f, 0x9a, 0xb4, 0xff}
	colChipText   = color.NRGBA{0xa9, 0xcf, 0xff, 0xff}
	colFooter     = color.NRGBA{0x4a, 0x56, 0x76, 0xff}
)

// over returns p composited on the opaque colour bg.
func over(p, bg color.NRGBA) color.NRGBA {
	a := float64(p.A) / 255
	f := func(x, y uint8) uint8 { return uint8(float64(x)*a + float64(y)*(1-a) + 0.5) }
	return color.NRGBA{f(p.R, bg.R), f(p.G, bg.G), f(p.B, bg.B), 0xff}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = clamp(t, 0, 1)
	f := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.NRGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 0xff}
}

// blend composites colour c with coverage a over the opaque pixel at (x, y).
func blend(img *image.NRGBA, x, y int, c color.NRGBA, a float64) {
	b := img.Bounds()
	if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y || a <= 0 {
		return
	}
	a = clamp(a, 0, 1)
	i := img.PixOffset(x, y)
	for k, v := range [3]uint8{c.R, c.G, c.B} {
		img.Pix[i+k] = uint8(float64(img.Pix[i+k])*(1-a) + float64(v)*a + 0.5)
	}
	img.Pix[i+3] = 0xff
}

// rrCover is the anti-aliased coverage of the rounded rectangle r at pixel
// (x, y). Radii are per half: top radius for the upper two corners, bottom
// for the lower two, so a panel can be rounded on top and square where it
// bleeds off the frame.
func rrCover(r image.Rectangle, top, bottom float64, x, y int) float64 {
	cy := float64(r.Min.Y+r.Max.Y) / 2
	rad := top
	if float64(y)+0.5 > cy {
		rad = bottom
	}
	cx := float64(r.Min.X+r.Max.X) / 2
	hx, hy := float64(r.Dx())/2-rad, float64(r.Dy())/2-rad
	px, py := math.Abs(float64(x)+0.5-cx)-hx, math.Abs(float64(y)+0.5-cy)-hy
	d := math.Hypot(math.Max(px, 0), math.Max(py, 0)) + math.Min(math.Max(px, py), 0) - rad
	return clamp(0.5-d, 0, 1)
}

func fillRR(img *image.NRGBA, r image.Rectangle, rad float64, c color.NRGBA, alpha float64) {
	for y := r.Min.Y - 1; y <= r.Max.Y; y++ {
		for x := r.Min.X - 1; x <= r.Max.X; x++ {
			blend(img, x, y, c, rrCover(r, rad, rad, x, y)*alpha)
		}
	}
}

// strokeRR draws a w-pixel border just inside r.
func strokeRR(img *image.NRGBA, r image.Rectangle, rad float64, w int, c color.NRGBA, alpha float64) {
	in := r.Inset(w)
	inner := math.Max(rad-float64(w), 0)
	for y := r.Min.Y - 1; y <= r.Max.Y; y++ {
		for x := r.Min.X - 1; x <= r.Max.X; x++ {
			ring := rrCover(r, rad, rad, x, y) - rrCover(in, inner, inner, x, y)
			blend(img, x, y, c, ring*alpha)
		}
	}
}

func boxBlur(a []float32, w, h, r int) {
	tmp := make([]float32, len(a))
	n := float32(2*r + 1)
	at := func(buf []float32, x, y int) float32 { return buf[min(max(y, 0), h-1)*w+min(max(x, 0), w-1)] }
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < h; y++ {
			var sum float32
			for x := -r; x <= r; x++ {
				sum += at(a, x, y)
			}
			for x := 0; x < w; x++ {
				tmp[y*w+x] = sum / n
				sum += at(a, x+r+1, y) - at(a, x-r, y)
			}
		}
		for x := 0; x < w; x++ {
			var sum float32
			for y := -r; y <= r; y++ {
				sum += at(tmp, x, y)
			}
			for y := 0; y < h; y++ {
				a[y*w+x] = sum / n
				sum += at(tmp, x, y+r+1) - at(tmp, x, y-r)
			}
		}
	}
}

// glow paints a blurred halo of the rounded rectangle r around it, limited to
// where clip is non-zero (the frame, so the halo never leaks past it).
func glow(img *image.NRGBA, r image.Rectangle, rad float64, blurRadius int, c color.NRGBA, alpha float64, clip func(x, y int) float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	m := make([]float32, w*h)
	for y := max(r.Min.Y, 0); y < min(r.Max.Y, h); y++ {
		for x := max(r.Min.X, 0); x < min(r.Max.X, w); x++ {
			m[y*w+x] = float32(rrCover(r, rad, rad, x, y))
		}
	}
	boxBlur(m, w, h, blurRadius)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if v := float64(m[y*w+x]); v > 0.002 {
				blend(img, x, y, c, v*alpha*clip(x, y))
			}
		}
	}
}

// strokeLine draws an anti-aliased segment of the given thickness.
func strokeLine(img *image.NRGBA, x0, y0, x1, y1, thick float64, c color.NRGBA, alpha float64) {
	minX, maxX := int(math.Floor(math.Min(x0, x1)-thick)), int(math.Ceil(math.Max(x0, x1)+thick))
	minY, maxY := int(math.Floor(math.Min(y0, y1)-thick)), int(math.Ceil(math.Max(y0, y1)+thick))
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float64(x)+0.5-x0, float64(y)+0.5-y0
			t := 0.0
			if l2 > 0 {
				t = clamp((px*dx+py*dy)/l2, 0, 1)
			}
			d := math.Hypot(px-t*dx, py-t*dy)
			blend(img, x, y, c, clamp(thick/2+0.5-d, 0, 1)*alpha)
		}
	}
}

// scaleTo resamples src to w by h pixels.
func scaleTo(src image.Image, w, h int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

// ---- text ----

func advance(f font.Face, s string, track float64) float64 {
	return float64(font.MeasureString(f, s))/64 + track*float64(len([]rune(s)))
}

// drawText draws s with its baseline-left at (x, baseline), one glyph at a
// time so tracking applies, and returns the x position after the last glyph.
func drawText(img *image.NRGBA, f font.Face, s string, x, baseline float64, c color.NRGBA, track float64) float64 {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f}
	for _, r := range s {
		d.Dot = fixed.Point26_6{X: fixed.Int26_6(math.Round(x * 64)), Y: fixed.Int26_6(math.Round(baseline * 64))}
		d.DrawString(string(r))
		x += float64(font.MeasureString(f, string(r)))/64 + track
	}
	return x
}

// wrapLines greedily breaks s into lines no wider than maxW. fits is false
// when a single word is wider than maxW and so cannot be broken.
func wrapLines(f font.Face, s string, maxW, track float64) (lines []string, fits bool) {
	fits = true
	cur := ""
	for _, w := range strings.Fields(s) {
		t := strings.TrimSpace(cur + " " + w)
		if advance(f, t, track) > maxW && cur != "" {
			lines = append(lines, cur)
			cur = w
		} else {
			cur = t
		}
		if advance(f, cur, track) > maxW {
			fits = false
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines, fits
}
