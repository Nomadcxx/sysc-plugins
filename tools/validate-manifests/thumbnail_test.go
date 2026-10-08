package main

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

func validThumbnail(t *testing.T) []byte {
	t.Helper()
	data, err := thumbnail.Encode(image.NewNRGBA(image.Rect(0, 0, thumbnail.Width, thumbnail.Height)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func plugin(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCheckThumbnail(t *testing.T) {
	good := validThumbnail(t)
	small, err := thumbnail.Encode(image.NewNRGBA(image.Rect(0, 0, 100, 100)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		files map[string][]byte
		grand bool
		want  string // substring of the error; empty means no error
	}{
		{"complete", map[string][]byte{"screenshot.png": []byte("x"), "thumbnail.webp": good}, false, ""},
		{"no thumbnail", map[string][]byte{"screenshot.png": []byte("x")}, false, "missing thumbnail.webp"},
		{"no screenshot", map[string][]byte{"thumbnail.webp": good}, false, "missing screenshot.png"},
		{"wrong size", map[string][]byte{"screenshot.png": []byte("x"), "thumbnail.webp": small}, false, "100x100"},
		{"not webp", map[string][]byte{"screenshot.png": []byte("x"), "thumbnail.webp": []byte("nope")}, false, "not a WebP"},
		{"grandfathered and empty", map[string][]byte{}, true, ""},
		{"grandfathered but has thumbnail", map[string][]byte{"thumbnail.webp": good}, true, "remove it from tools/thumbnail/grandfathered.txt"},
		{"grandfathered but has screenshot", map[string][]byte{"screenshot.png": []byte("x")}, true, "remove it from tools/thumbnail/grandfathered.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkThumbnail(plugin(t, tc.files), tc.grand)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestGrandfatheredNamesMustBePlugins(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plugins", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins", "notes", "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkGrandfatheredExist(root, map[string]bool{"notes": true, "ghost": true})
	if err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("err = %v, want one naming ghost", err)
	}
	if err := checkGrandfatheredExist(root, map[string]bool{"notes": true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The shipped repository must satisfy its own rule.
func TestShippedPluginsMeetTheThumbnailRule(t *testing.T) {
	root := "../.."
	grand, err := thumbnail.LoadGrandfathered(filepath.Join(root, "tools/thumbnail/grandfathered.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkGrandfatheredExist(root, grand); err != nil {
		t.Fatal(err)
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "plugins", "*", "manifest.json"))
	for _, m := range dirs {
		d := filepath.Dir(m)
		if err := checkThumbnail(d, grand[filepath.Base(d)]); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}
