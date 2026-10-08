package thumbnail

import (
	"strings"
	"testing"

	"golang.org/x/image/font/opentype"
)

func TestEmbeddedFontsCoverTheLayout(t *testing.T) {
	fs, err := loadFonts()
	if err != nil {
		t.Fatal(err)
	}
	// Every literal the layout draws in the monospace face.
	const mono = "┌─┐│█░▒▓·[]/+ ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789:._-"
	check := func(name string, f *opentype.Font, text string) {
		t.Helper()
		if r, ok := missingRune(f, text); !ok {
			t.Errorf("%s has no glyph for %q", name, r)
		}
	}
	check("JetBrains Mono Bold", fs.monoBold, mono)
	var ascii strings.Builder
	for r := rune(0x20); r <= 0x7e; r++ {
		ascii.WriteRune(r)
	}
	ascii.WriteString("…’“”–—")
	check("Inter Regular", fs.interRegular, ascii.String())
	check("Inter ExtraBold", fs.interExtraBold, ascii.String())
}

func TestWordmarkDecodesAsTheExpectedMask(t *testing.T) {
	img, err := wordmark()
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 930 || b.Dy() != 128 {
		t.Fatalf("wordmark is %dx%d, want 930x128", b.Dx(), b.Dy())
	}
}
