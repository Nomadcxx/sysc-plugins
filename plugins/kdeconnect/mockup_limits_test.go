package kdeconnect

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMockupArtDirLivesUnderTheUserCache is the guard for the path the
// composites used to be written to. A package-level initialiser ran before
// the init() that fixes the cache directory, so the composites landed in the
// shared temp directory, where another local user can read them, replace
// them and delete them.
func TestMockupArtDirLivesUnderTheUserCache(t *testing.T) {
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache directory: %v", err)
	}
	want := filepath.Join(base, "sysc-plugins", "kdeconnect", "mockups")
	if mockupArtDir != want {
		t.Errorf("mockupArtDir = %q, want %q", mockupArtDir, want)
	}
	if strings.HasPrefix(base, os.TempDir()+string(filepath.Separator)) {
		return // the cache directory itself is under the temp directory
	}
	if strings.HasPrefix(mockupArtDir, os.TempDir()+string(filepath.Separator)) {
		t.Errorf("mockupArtDir = %q, want it out of the shared temp directory", mockupArtDir)
	}
}

// TestComposeMockupRefusesAlbumArtOverThePixelLimit is the guard for the
// decode of an image that comes from the phone. The album art is compressed,
// so a small file can name a bitmap of any size: the dimensions have to be
// read before the pixels are decoded.
func TestComposeMockupRefusesAlbumArtOverThePixelLimit(t *testing.T) {
	dir := useRealMockups(t)
	art := filepath.Join(t.TempDir(), "art.png")
	// 8000x8000 is 64 megapixels, four times the package's limit, and the
	// file stays tiny because only the header is written.
	writePNGHeader(t, art, 8000, 8000)

	_, err := composeMockup("phone", art)
	if err == nil {
		t.Fatal("composeMockup accepted album art over the pixel limit")
	}
	if !strings.Contains(err.Error(), "album art") || !strings.Contains(err.Error(), "pixel") {
		t.Errorf("error = %v, want it to name the pixel limit of the album art", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the composite directory holds %d files, want none", len(entries))
	}
}

// TestComposeMockupRefusesAlbumArtOverTheByteLimit is the other half: the
// file itself is capped, so a large file is refused before it is read.
func TestComposeMockupRefusesAlbumArtOverTheByteLimit(t *testing.T) {
	useRealMockups(t)
	art := filepath.Join(t.TempDir(), "art.png")
	big := make([]byte, recentImageMaxSourceBytes+1)
	if err := os.WriteFile(art, big, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := composeMockup("phone", art)
	if err == nil {
		t.Fatal("composeMockup accepted album art over the byte limit")
	}
	if !strings.Contains(err.Error(), "album art") || !strings.Contains(err.Error(), "source limit") {
		t.Errorf("error = %v, want it to name the byte limit of the album art", err)
	}
}

// writePNGHeader writes a PNG that declares the given dimensions and stops
// there. The dimensions are all a decoder needs to size the bitmap, so a
// header alone names a bitmap no file on disk could carry.
func writePNGHeader(t *testing.T, path string, width, height uint32) {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 6 // colour type: truecolour with alpha
	writePNGChunk(&buf, "IHDR", ihdr)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writePNGChunk(buf *bytes.Buffer, kind string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	buf.Write(length[:])
	body := append([]byte(kind), data...)
	buf.Write(body)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE(body))
	buf.Write(sum[:])
}
