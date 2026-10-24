package kdeconnect

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDeclaredSizePNG writes a PNG whose header declares a canvas of the
// given size, without encoding that many pixels. The decoder reads the
// header before the pixels, so the declared size is what a limit check sees.
func writeDeclaredSizePNG(t *testing.T, path string, width, height uint32) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// The IHDR chunk starts at offset 8: a 4-byte length, a 4-byte type,
	// then the width and the height. Its checksum covers the type and the
	// data, so it has to be recomputed after the size changes.
	binary.BigEndian.PutUint32(raw[16:20], width)
	binary.BigEndian.PutUint32(raw[20:24], height)
	binary.BigEndian.PutUint32(raw[29:33], crc32.ChecksumIEEE(raw[12:29]))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestComposeMockupRejectsAlbumArtOverThePixelLimit covers the album-art
// path: a small file that declares a huge canvas must be refused before the
// decode allocates it.
func TestComposeMockupRejectsAlbumArtOverThePixelLimit(t *testing.T) {
	useRealMockups(t)
	art := filepath.Join(t.TempDir(), "cover.png")
	writeDeclaredSizePNG(t, art, 5400, 5400)

	_, err := composeMockup("phone", art)
	if err == nil {
		t.Fatal("composeMockup accepted album art over the pixel limit")
	}
	if !strings.Contains(err.Error(), "-pixel image limit") {
		t.Fatalf("composeMockup error = %v, want the pixel limit", err)
	}
}

// TestComposeMockupRejectsAlbumArtOverTheSourceByteLimit covers the other
// bound: a file past the source byte limit is refused before it is read.
func TestComposeMockupRejectsAlbumArtOverTheSourceByteLimit(t *testing.T) {
	useRealMockups(t)
	art := filepath.Join(t.TempDir(), "cover.png")
	f, err := os.Create(art)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(recentImageMaxSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = composeMockup("phone", art)
	if err == nil {
		t.Fatal("composeMockup accepted album art over the source byte limit")
	}
	if !strings.Contains(err.Error(), "-byte image source limit") {
		t.Fatalf("composeMockup error = %v, want the source byte limit", err)
	}
}

// TestDecodeBoundedFileAcceptsAnOrdinaryImage keeps the guard from refusing
// the covers a phone really sends.
func TestDecodeBoundedFileAcceptsAnOrdinaryImage(t *testing.T) {
	art := filepath.Join(t.TempDir(), "cover.png")
	writeArt(t, art, color.RGBA{R: 220, A: 255})

	img, err := decodeBoundedFile(art)
	if err != nil {
		t.Fatalf("decodeBoundedFile: %v", err)
	}
	if got := img.Bounds().Dx(); got != 64 {
		t.Fatalf("decoded width = %d, want 64", got)
	}
}
