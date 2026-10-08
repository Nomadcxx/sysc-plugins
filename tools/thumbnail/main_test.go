package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{0x3a, 0x40, 0x4c, 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const notesManifest = `{"schema":1,"id":"org.sysc.notes","name":"Notes","description":"Markdown notes.","version":"1.0.0","exec":"bin/x","widgets":[{"id":"bar"}],"panels":[{"id":"panel"}]}`

// newRepo is a minimal repository: one plugin with a valid capture.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "plugins/notes/manifest.json"), []byte(notesManifest))
	write(t, filepath.Join(root, "plugins/notes/screenshot.png"), pngBytes(t, 560, 700))
	write(t, filepath.Join(root, "catalog-meta.json"), []byte(`{"org.sysc.notes":{"category":"productivity","author":"x"}}`))
	return root
}

func runIn(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(root, args, &out, &errOut)
	return out.String() + errOut.String(), err
}

func TestWriteThenCheckPasses(t *testing.T) {
	root := newRepo(t)
	if out, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatalf("write: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(root, "plugins/notes/thumbnail.webp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := thumbnail.Validate(data); err != nil {
		t.Fatalf("written file is not a valid thumbnail: %v", err)
	}
	if out, err := runIn(t, root, "-check"); err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
}

func TestCheckFailsWhenTheManifestChanges(t *testing.T) {
	root := newRepo(t)
	if _, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(notesManifest, "Markdown notes.", "Markdown notes, now with more.", 1)
	write(t, filepath.Join(root, "plugins/notes/manifest.json"), []byte(changed))
	_, err := runIn(t, root, "-check")
	if err == nil || !strings.Contains(err.Error(), "out of date") ||
		!strings.Contains(err.Error(), "go run ./tools/thumbnail -plugin plugins/notes") {
		t.Fatalf("err = %v, want an out-of-date failure naming the regenerate command", err)
	}
}

func TestCheckFailsWhenTheCategoryChanges(t *testing.T) {
	root := newRepo(t)
	if _, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "catalog-meta.json"), []byte(`{"org.sysc.notes":{"category":"utilities","author":"x"}}`))
	if _, err := runIn(t, root, "-check"); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("err = %v, want out of date", err)
	}
}

func TestCheckFailsWhenTheThumbnailIsMissing(t *testing.T) {
	root := newRepo(t)
	_, err := runIn(t, root, "-check")
	if err == nil || !strings.Contains(err.Error(), "missing thumbnail.webp") {
		t.Fatalf("err = %v, want a missing-thumbnail failure", err)
	}
}

func TestCheckSkipsGrandfatheredPlugins(t *testing.T) {
	root := newRepo(t)
	write(t, filepath.Join(root, "plugins/old/manifest.json"), []byte(strings.ReplaceAll(notesManifest, "notes", "old")))
	write(t, filepath.Join(root, "tools/thumbnail/grandfathered.txt"), []byte("old\n"))
	if _, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatal(err)
	}
	if out, err := runIn(t, root, "-check"); err != nil {
		t.Fatalf("a grandfathered plugin with no files must not fail -check: %v\n%s", err, out)
	}
}

func TestBadScreenshotsAreRejected(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 400, 400)), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", jpg.Bytes(), "must be a PNG"},
		{"narrow", pngBytes(t, 200, 300), "at least 320"},
		{"not an image", []byte("hello"), "screenshot.png"},
		{"oversized", append(pngBytes(t, 400, 400), make([]byte, maxShotBytes)...), "4 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRepo(t)
			write(t, filepath.Join(root, "plugins/notes/screenshot.png"), tc.data)
			_, err := runIn(t, root, "-plugin", "plugins/notes")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestMissingScreenshotNamesTheFile(t *testing.T) {
	root := newRepo(t)
	if err := os.Remove(filepath.Join(root, "plugins/notes/screenshot.png")); err != nil {
		t.Fatal(err)
	}
	_, err := runIn(t, root, "-plugin", "plugins/notes")
	if err == nil || !strings.Contains(err.Error(), "missing screenshot.png") {
		t.Fatalf("err = %v, want a missing-screenshot failure", err)
	}
}

// Every manifest the repo ships must be drawable: a name that cannot fit in
// two lines, or a character the embedded fonts lack, fails here instead of
// on the day someone adds that plugin's capture.
func TestShippedManifestsRender(t *testing.T) {
	root := "../.."
	meta, err := readCategories(filepath.Join(root, "catalog-meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := filepath.Glob(filepath.Join(root, "plugins", "*", "manifest.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no manifests: %v", err)
	}
	for _, path := range dirs {
		dir := filepath.Base(filepath.Dir(path))
		in, err := loadInput(root, dir, meta)
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		in.Screenshot = image.NewNRGBA(image.Rect(0, 0, 560, 700))
		if _, _, err := thumbnail.Render(in); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
}
