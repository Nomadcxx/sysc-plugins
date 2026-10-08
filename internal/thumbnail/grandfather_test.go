package thumbnail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeList(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "grandfathered.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadGrandfatheredParsesNamesAndSkipsNoise(t *testing.T) {
	got, err := LoadGrandfathered(writeList(t, "# exempt until backfilled\nnotes\n\n  timer  \nworld-clock # trailing note\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notes", "timer", "world-clock"} {
		if !got[name] {
			t.Errorf("%s missing from %v", name, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %v, want exactly 3 names", got)
	}
}

func TestLoadGrandfatheredMissingFileIsEmpty(t *testing.T) {
	got, err := LoadGrandfathered(filepath.Join(t.TempDir(), "nope.txt"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want an empty list and no error", got, err)
	}
}

func TestLoadGrandfatheredRejectsBadLines(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"duplicate", "notes\nnotes\n", "listed twice"},
		{"path", "plugins/notes\n", "plugin directory name"},
		{"parent", "..\n", "plugin directory name"},
		{"space", "two words\n", "plugin directory name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadGrandfathered(writeList(t, tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
