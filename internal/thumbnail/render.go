// Package thumbnail renders the 960x540 catalog thumbnail every sysc plugin
// ships, and checks that a file on disk is one.
package thumbnail

import (
	"errors"
	"fmt"
	"image"
	"math"
	"strings"

	"golang.org/x/image/font/opentype"
)

// Input is everything a thumbnail shows. Dir is the plugin directory name
// (plugins/<Dir>); ID, Name and Description come from its manifest, Category
// from catalog-meta.json, and Screenshot is the captured panel.
type Input struct {
	Dir         string
	ID          string
	Name        string
	Description string
	Category    string
	HasPanel    bool
	HasBar      bool
	Screenshot  image.Image
}

// Layout constants, in output pixels. They reproduce the approved "B1" mock.
const (
	frameInset   = 24
	frameRadius  = 18
	frameBorder  = 2
	titleBarH    = 58
	columnX      = frameInset + frameBorder + 40
	columnW      = 410
	panelInnerW  = 316
	panelRight   = Width - frameInset - frameBorder - 44
	panelTopGap  = 40
	maxDescLines = 3
	maxNameLines = 2

	// breadcrumbMaxW is the room left of the centred wordmark group.
	breadcrumbMaxW = 277
)

// Render draws the thumbnail. The returned warnings are non-fatal: today only
// a description cut to fit.
func Render(in Input) (img *image.NRGBA, warnings []string, err error) {
	if in.Screenshot == nil {
		return nil, nil, errors.New("thumbnail: no screenshot")
	}
	if b := in.Screenshot.Bounds(); b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, nil, errors.New("thumbnail: screenshot is empty")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, nil, errors.New("thumbnail: plugin has no name")
	}
	fs, err := loadFonts()
	if err != nil {
		return nil, nil, err
	}
	// Check each piece of text against the face it is drawn in: the name and
	// description in Inter, everything else in the narrower monospace subset.
	for _, field := range []struct {
		label, text string
		font        *opentype.Font
	}{
		{"name", in.Name, fs.interExtraBold},
		{"description", in.Description, fs.interRegular},
		{"category", strings.ToUpper(in.Category), fs.monoBold},
		{"directory", in.Dir, fs.monoBold},
		{"id", in.ID, fs.monoBold},
	} {
		if r, ok := missingRune(field.font, field.text); !ok {
			return nil, nil, fmt.Errorf("thumbnail: %s %q has %q, which the embedded font does not cover", field.label, field.text, r)
		}
	}
	mark, err := wordmark()
	if err != nil {
		return nil, nil, err
	}

	img = image.NewNRGBA(image.Rect(0, 0, Width, Height))
	paintBackground(img)

	frame := image.Rect(frameInset, frameInset, Width-frameInset, Height-frameInset)
	fillRR(img, frame, frameRadius, colFrameFill, 0.82)
	strokeRR(img, frame, frameRadius, frameBorder, colBorder, 1)

	if err := paintTitleBar(img, fs, mark, in); err != nil {
		return nil, nil, err
	}
	warnings, err = paintColumn(img, fs, in)
	if err != nil {
		return nil, nil, err
	}
	if err := paintPanel(img, fs, in, frame); err != nil {
		return nil, nil, err
	}
	if err := paintFooter(img, fs); err != nil {
		return nil, nil, err
	}
	return img, warnings, nil
}

func paintBackground(img *image.NRGBA) {
	// Bottom-centre blue glow over the base colour.
	cx, cy, rx, ry := float64(Width)*0.5, float64(Height)*1.12, 700.0, 420.0
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			d := math.Hypot((float64(x)-cx)/rx, (float64(y)-cy)/ry)
			c := mix(colBackground, colBlue, clamp(1-d, 0, 1)*0.3)
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 0xff
		}
	}
	// "+" crosshairs on a 24 px grid.
	for gy := 12; gy < Height; gy += 24 {
		for gx := 16; gx < Width; gx += 24 {
			for k := -3; k <= 3; k++ {
				blend(img, gx+k, gy, colBlue, 0.2)
				if k != 0 {
					blend(img, gx, gy+k, colBlue, 0.2)
				}
			}
		}
	}
}

func paintTitleBar(img *image.NRGBA, fs fonts, mark image.Image, in Input) error {
	mono, err := face(fs.monoBold, 13)
	if err != nil {
		return err
	}
	const baseline = frameInset + frameBorder + titleBarH/2 + 5
	// The breadcrumb must stay clear of the wordmark group, which starts at
	// x = 343; a long directory name drops the "sysc://" scheme to fit.
	prefix := " sysc://plugins/"
	if advance(mono, "┌─"+prefix+in.Dir, 0) > breadcrumbMaxW {
		prefix = " plugins/"
	}
	x := float64(columnX - 12)
	x = drawText(img, mono, "┌─", x, baseline, colBlue, 0)
	x = drawText(img, mono, prefix, x, baseline, colMuted, 0)
	drawText(img, mono, in.Dir, x, baseline, colInk, 0)

	right := fmt.Sprintf("%s ─┐", in.ID)
	rx := float64(Width-frameInset-frameBorder-28) - advance(mono, right, 0)
	rx = drawText(img, mono, in.ID, rx, baseline, colMuted, 0)
	drawText(img, mono, " ─┐", rx, baseline, colBlue, 0)

	// Wordmark flanked by slashes, centred on the bar.
	const slashCell, slashes, gap, markH = 10.8, 6, 14.0, 16
	markW := int(math.Round(float64(mark.Bounds().Dx()) * markH / float64(mark.Bounds().Dy())))
	total := 2*slashCell*slashes + 2*gap + float64(markW)
	sx := (Width - total) / 2
	midY := float64(frameInset + frameBorder + titleBarH/2)
	paintSlashes(img, sx, midY)
	paintMark(img, mark, int(math.Round(sx+slashCell*slashes+gap)), int(midY)-markH/2, markW, markH)
	paintSlashes(img, sx+slashCell*slashes+gap+float64(markW)+gap, midY)

	// Rule under the bar.
	ruleY := frameInset + frameBorder + titleBarH
	for rx := frameInset + frameBorder; rx < Width-frameInset-frameBorder; rx++ {
		for k := 0; k < 2; k++ {
			blend(img, rx, ruleY+k, colBorder, 1)
		}
	}
	return nil
}

func paintSlashes(img *image.NRGBA, x, midY float64) {
	for i := 0; i < 6; i++ {
		cx := x + 10.8*float64(i) + 5.4
		strokeLine(img, cx-3.6, midY+6.5, cx+3.6, midY-6.5, 2, colBlue, 0.95)
	}
}

func paintMark(img *image.NRGBA, mask image.Image, x, y, w, h int) {
	scaled := scaleTo(mask, w, h)
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			// The mask is grayscale: white (R=255) is the letterform.
			blend(img, x+px, y+py, colBlue, float64(scaled.NRGBAAt(px, py).R)/255)
		}
	}
}

// paintColumn draws the left text column, vertically centred in the body,
// and returns any warnings.
func paintColumn(img *image.NRGBA, fs fonts, in Input) (warnings []string, err error) {
	// Name: one line at 54 px, else two at 46 px.
	nameSize := 54.0
	nameFace, err := face(fs.interExtraBold, nameSize)
	if err != nil {
		return nil, err
	}
	const nameTrack = -0.8
	nameLines, fits := wrapLines(nameFace, in.Name, columnW-40, nameTrack)
	if len(nameLines) > 1 || !fits {
		nameSize = 46
		if nameFace, err = face(fs.interExtraBold, nameSize); err != nil {
			return nil, err
		}
		nameLines, fits = wrapLines(nameFace, in.Name, columnW-40, nameTrack)
	}
	if !fits || len(nameLines) > maxNameLines {
		return nil, fmt.Errorf("thumbnail: name %q does not fit in %d lines", in.Name, maxNameLines)
	}

	descFace, err := face(fs.interRegular, 21)
	if err != nil {
		return nil, err
	}
	const gutterW = 24.8 // gutter glyph advance plus the gap to the text
	descLines, _ := wrapLines(descFace, in.Description, columnW-gutterW, 0)
	if len(descLines) > maxDescLines {
		descLines = descLines[:maxDescLines]
		last := descLines[maxDescLines-1]
		for advance(descFace, last+"…", 0) > columnW-gutterW && len(last) > 0 {
			last = strings.TrimRight(last[:len(last)-1], " ")
		}
		descLines[maxDescLines-1] = strings.TrimRight(last, " .,;:") + "…"
		warnings = append(warnings, fmt.Sprintf("description of %s is longer than %d lines and was cut with an ellipsis", in.Dir, maxDescLines))
	}

	const labelH, chipH, gap = 16.0, 28.0, 12.0
	nameH := float64(len(nameLines)) * nameSize * 1.02
	descH := float64(len(descLines)) * 29
	blockH := labelH + gap + nameH + gap + descH + gap + 6 + chipH
	bodyTop := float64(frameInset + frameBorder + titleBarH + 2)
	bodyH := float64(Height-frameInset-frameBorder) - bodyTop
	top := bodyTop + (bodyH-blockH)/2

	mono12, err := face(fs.monoBold, 12)
	if err != nil {
		return nil, err
	}
	y := top
	drawText(img, mono12, "// PLUGIN", columnX, y+12, colBlue, 2)
	y += labelH + gap

	var lastX, lastBase float64
	for i, line := range nameLines {
		lastBase = y + float64(i)*nameSize*1.02 + nameSize*0.86
		lastX = drawText(img, nameFace, line, columnX, lastBase, colInk, nameTrack)
	}
	// Terminal cursor: a block as wide as half the type and as tall as its caps.
	cur := image.Rect(int(lastX+8), int(lastBase-nameSize*0.74), int(lastX+8+nameSize*0.5), int(lastBase))
	fillRR(img, cur, 1, colBlue, 1)
	y += nameH + gap

	gutter, err := face(fs.monoBold, 18)
	if err != nil {
		return nil, err
	}
	for i, line := range descLines {
		base := y + float64(i)*29 + 22
		drawText(img, gutter, "│", columnX, base-1, colBlue, 0)
		drawText(img, descFace, line, columnX+gutterW, base, colMuted, 0)
	}
	y += descH + gap + 6

	chips := []string{"[ " + strings.ToUpper(in.Category) + " ]"}
	if in.HasPanel {
		chips = append(chips, "[ PANEL ]")
	}
	if in.HasBar {
		chips = append(chips, "[ BAR ]")
	}
	cx := float64(columnX)
	for i, label := range chips {
		w := advance(mono12, label, 1.4) + 20
		r := image.Rect(int(cx), int(y), int(cx+w), int(y+chipH))
		if i == 0 {
			fillRR(img, r, 6, colBlue, 0.14)
			strokeRR(img, r, 6, 1, colBlue, 0.55)
			drawText(img, mono12, label, cx+10, y+18, colChipText, 1.4)
		} else {
			strokeRR(img, r, 6, 1, colBorder, 1)
			drawText(img, mono12, label, cx+10, y+18, colMuted, 1.4)
		}
		cx += w + 8
	}
	return warnings, nil
}

// panelGeometry places the screenshot panel for a screenshot scaled to h
// pixels tall at panelInnerW wide. outer is the bordered box and inner the
// pixels inside the border; bleeds reports a tall capture that runs off the
// bottom of the frame (no bottom border, faded) instead of sitting fully
// framed and centred.
func panelGeometry(h int) (outer, inner image.Rectangle, bleeds bool) {
	left := panelRight - (panelInnerW + 2*frameBorder)
	bodyTop := frameInset + frameBorder + titleBarH + 2
	bodyBottom := Height - frameInset - frameBorder
	avail := bodyBottom - bodyTop - panelTopGap

	bleeds = h+frameBorder >= avail
	if bleeds {
		outer = image.Rect(left, bodyTop+panelTopGap, panelRight, bodyTop+panelTopGap+h+frameBorder)
		inner = image.Rect(left+frameBorder, outer.Min.Y+frameBorder, panelRight-frameBorder, outer.Max.Y)
		return outer, inner, true
	}
	elemH := h + 2*frameBorder
	top := bodyTop + (bodyBottom-bodyTop-elemH)/2
	outer = image.Rect(left, top, panelRight, top+elemH)
	inner = image.Rect(left+frameBorder, top+frameBorder, panelRight-frameBorder, top+frameBorder+h)
	return outer, inner, false
}

// paintPanel draws the captured screenshot in its glowing frame. A tall
// capture bleeds off the bottom of the frame and fades over its last 22%; a
// short one is centred with a full border.
func paintPanel(img *image.NRGBA, fs fonts, in Input, frame image.Rectangle) error {
	b := in.Screenshot.Bounds()
	// A very wide crop would round to zero rows; keep at least one.
	h := max(int(math.Round(float64(b.Dy())*panelInnerW/float64(b.Dx()))), 1)
	outer, inner, bleeds := panelGeometry(h)
	top, left := outer.Min.Y, outer.Min.X
	elemH := outer.Dy()
	bodyBottom := Height - frameInset - frameBorder
	bottomRad := 12.0
	if bleeds {
		bottomRad = 0
	}
	innerBottomRad := math.Max(bottomRad-float64(frameBorder), 0)

	inFrame := func(x, y int) float64 { return rrCover(frame, frameRadius, frameRadius, x, y) }
	glow(img, outer, 12, 34, colBlue, 0.42, inFrame)

	shot := scaleTo(in.Screenshot, panelInnerW, h)
	for y := outer.Min.Y; y < min(outer.Max.Y, bodyBottom); y++ {
		fade := 1.0
		if bleeds {
			fade = clamp(float64(outer.Max.Y-y)/float64(elemH)/0.22, 0, 1)
		}
		for x := outer.Min.X; x < outer.Max.X; x++ {
			a := rrCover(outer, 12, bottomRad, x, y) * fade
			c := colFrameFill
			if image.Pt(x, y).In(image.Rect(inner.Min.X, inner.Min.Y, inner.Max.X, inner.Min.Y+h)) {
				c = over(shot.NRGBAAt(x-inner.Min.X, y-inner.Min.Y), colFrameFill)
			}
			blend(img, x, y, c, a)
			ring := rrCover(outer, 12, bottomRad, x, y) - rrCover(inner, 10, innerBottomRad, x, y)
			blend(img, x, y, colBlue, ring*0.7*fade)
		}
	}

	corner, err := face(fs.monoBold, 22)
	if err != nil {
		return err
	}
	drawText(img, corner, "┌", float64(left-14), float64(top-20)+18, colBlue, 0)
	drawText(img, corner, "┐", float64(panelRight+14)-13.2, float64(top-20)+18, colBlue, 0)
	return nil
}

func paintFooter(img *image.NRGBA, fs fonts) error {
	mono, err := face(fs.monoBold, 12)
	if err != nil {
		return err
	}
	const base = Height - frameInset - frameBorder - 17
	x := drawText(img, mono, "░▒▓█▓▒░", columnX, base, colBlue, 0)
	drawText(img, mono, "  sysc-shell · plugin catalog", x, base, colFooter, 0)
	return nil
}
