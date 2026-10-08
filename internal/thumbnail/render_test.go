package thumbnail

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
)

// panelShot is a stand-in capture w by h pixels: a grey panel with a blue
// header strip, so a render has real structure to scale.
func panelShot(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{0x3a, 0x40, 0x4c, 0xff}
			if y < h/10 {
				c = color.NRGBA{0x1f, 0x8b, 0xff, 0xff}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func notes() Input {
	return Input{
		Dir: "notes", ID: "org.sysc.notes", Name: "Notes",
		Description: "Markdown notes and pastel sticky notes for your Obsidian folder, with one box to search or start a note.",
		Category:    "productivity", HasPanel: true, HasBar: true,
		Screenshot: panelShot(560, 700),
	}
}

func TestRenderIsOpaqueAndTheRightSize(t *testing.T) {
	img, warnings, err := Render(notes())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if got := img.Bounds(); got != image.Rect(0, 0, Width, Height) {
		t.Fatalf("bounds = %v", got)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			t.Fatalf("pixel %d is not opaque", i/4)
		}
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	encode := func() []byte {
		img, _, err := Render(notes())
		if err != nil {
			t.Fatal(err)
		}
		b, err := Encode(img)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := encode(), encode(); !bytes.Equal(a, b) {
		t.Fatal("two renders of the same input differ")
	}
}

func TestEncodedThumbnailValidates(t *testing.T) {
	img, _, err := Render(notes())
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(data); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(data) > MaxBytes/2 {
		t.Fatalf("thumbnail is %d bytes; expected well under the %d cap", len(data), MaxBytes)
	}
}

func TestLongNameWrapsToTwoLines(t *testing.T) {
	in := notes()
	in.Name = "GitHub Notifications"
	if _, _, err := Render(in); err != nil {
		t.Fatalf("two-line name: %v", err)
	}
}

func TestNameThatCannotFitIsAnError(t *testing.T) {
	for _, name := range []string{
		"Wallpaper Depth Mask Studio Professional Edition Deluxe", // wraps to more than two lines
		"Supercalifragilisticexpialidocious",                      // one word wider than the column
		"   ",
	} {
		in := notes()
		in.Name = name
		if _, _, err := Render(in); err == nil {
			t.Errorf("Name %q rendered; want an error", name)
		}
	}
}

func TestLongDescriptionIsCutWithAWarning(t *testing.T) {
	in := notes()
	in.Description = strings.Repeat("A description that keeps going and going and going. ", 6)
	_, warnings, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "notes") {
		t.Fatalf("warnings = %v, want one naming the plugin", warnings)
	}
}

func TestTextTheFontCannotDrawIsAnError(t *testing.T) {
	in := notes()
	in.Name = "メモ"
	if _, _, err := Render(in); err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("Render = %v, want a font coverage error", err)
	}
}

func TestPanelFitsTallAndShortCaptures(t *testing.T) {
	tall, _, bleeds := panelGeometry(400)
	if !bleeds || tall.Max.Y <= Height-frameInset-frameBorder {
		t.Errorf("a 400px capture should bleed past the frame bottom, got %v bleeds=%v", tall, bleeds)
	}
	short, inner, bleeds := panelGeometry(150)
	if bleeds {
		t.Fatal("a 150px capture should be framed, not bleed")
	}
	mid := (short.Min.Y + short.Max.Y) / 2
	bodyMid := ((frameInset + frameBorder + titleBarH + 2) + (Height - frameInset - frameBorder)) / 2
	if d := mid - bodyMid; d < -1 || d > 1 {
		t.Errorf("short capture centre y=%d, body centre %d", mid, bodyMid)
	}
	if inner.Dy() != 150 {
		t.Errorf("inner height = %d, want 150", inner.Dy())
	}
}

func TestRenderHandlesAWideBarCrop(t *testing.T) {
	for _, size := range [][2]int{{1920, 80}, {3000, 4}, {320, 10}} {
		in := notes()
		in.Screenshot = panelShot(size[0], size[1])
		if _, _, err := Render(in); err != nil {
			t.Errorf("%dx%d capture: %v", size[0], size[1], err)
		}
	}
}

func TestRenderCompositesATransparentScreenshotOnTheFrame(t *testing.T) {
	in := notes()
	in.Screenshot = image.NewNRGBA(image.Rect(0, 0, 560, 200)) // fully transparent
	img, _, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	// 560x200 scales to 113 rows at the panel's 316 px inner width.
	_, inner, _ := panelGeometry(113)
	c := img.NRGBAAt((inner.Min.X+inner.Max.X)/2, (inner.Min.Y+inner.Max.Y)/2)
	if c != colFrameFill {
		t.Fatalf("centre of a transparent capture = %v, want the frame fill %v", c, colFrameFill)
	}
}

func TestRenderWithoutDescriptionOrFeatureChips(t *testing.T) {
	in := notes()
	in.Description = ""
	in.HasPanel, in.HasBar = false, false
	if _, warnings, err := Render(in); err != nil || len(warnings) != 0 {
		t.Fatalf("Render = %v, %v", warnings, err)
	}
}
