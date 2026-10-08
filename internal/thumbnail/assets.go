package thumbnail

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

//go:embed assets/*.ttf assets/sysc-mark.png
var assetFS embed.FS

// fonts holds the parsed typefaces.
type fonts struct {
	interRegular, interExtraBold, monoBold *opentype.Font
}

func loadFonts() (fonts, error) {
	var f fonts
	for _, e := range []struct {
		name string
		dst  **opentype.Font
	}{
		{"assets/Inter-Regular.ttf", &f.interRegular},
		{"assets/Inter-ExtraBold.ttf", &f.interExtraBold},
		{"assets/JetBrainsMono-Bold.ttf", &f.monoBold},
	} {
		data, err := assetFS.ReadFile(e.name)
		if err != nil {
			return fonts{}, err
		}
		if *e.dst, err = opentype.Parse(data); err != nil {
			return fonts{}, fmt.Errorf("%s: %w", e.name, err)
		}
	}
	return f, nil
}

// face returns f at px pixels. Hinting is off so output does not depend on
// a hinting engine.
func face(f *opentype.Font, px float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingNone})
}

// missingRune reports the first rune of text that f has no glyph for. Text
// the font cannot draw must fail loudly, since an uncovered rune renders as
// an empty box.
func missingRune(f *opentype.Font, text string) (rune, bool) {
	var buf sfnt.Buffer
	for _, r := range text {
		if idx, err := f.GlyphIndex(&buf, r); err != nil || idx == 0 {
			return r, false
		}
	}
	return 0, true
}

// wordmark decodes the sysc wordmark mask: grayscale, white where the
// letterforms are.
func wordmark() (image.Image, error) {
	data, err := assetFS.ReadFile("assets/sysc-mark.png")
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(data))
}
