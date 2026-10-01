package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/notes"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestInvalidNotesFolderLeavesManagerRecoverable(t *testing.T) {
	sess := applySettings(nil, map[string]any{"notes_dir": "\x00"})
	if sess == nil {
		t.Fatal("invalid folder prevented Notes from creating a recoverable session")
	}
	if got := sess.Snap().ScanError; !strings.Contains(got, "Could not open notes folder") {
		t.Fatalf("folder error = %q", got)
	}
	sess = applySettings(sess, map[string]any{"notes_dir": t.TempDir()})
	if got := sess.Snap().ScanError; got != "" {
		t.Fatalf("successful folder recovery left error %q", got)
	}
}

func TestPinnedSurfacesAreScopedByNoteAndOutput(t *testing.T) {
	pins := []pinnedSurface{
		{Name: "Plan.md", Output: "DP-1"},
		{Name: "Plan.md", Output: "DP-2"},
		{Name: "Inbox.md", Output: "DP-1"},
	}
	if !isPinned(pins, "Plan.md", "DP-2") {
		t.Fatal("second output pin was lost")
	}
	got := setPinned(pins, "Plan.md", "DP-1", false)
	if isPinned(got, "Plan.md", "DP-1") || !isPinned(got, "Plan.md", "DP-2") || !isPinned(got, "Inbox.md", "DP-1") {
		t.Fatalf("unpin changed unrelated surfaces: %+v", got)
	}
}

func TestInvalidStickyColorFallsBackToThemePalette(t *testing.T) {
	for _, color := range []string{"sun", "mint", "sky", "rose", "lilac"} {
		if got := validColor(color); got != color {
			t.Errorf("validColor(%q) = %q", color, got)
		}
	}
	if got := validColor("#ff00ff"); got != "sun" {
		t.Fatalf("arbitrary color = %q, want the safe default", got)
	}
}

func newSessionForTest(t *testing.T) (*notes.Session, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := notes.Open(dir, "md")
	if err != nil {
		t.Fatal(err)
	}
	return notes.NewSession(st, time.Now), dir
}

func TestCascadeStepsAndWraps(t *testing.T) {
	views := map[string]view{}
	for i := 0; i < 9; i++ {
		x, y := cascade(views, "DP-1")
		step := (i % 8) * 28
		if x != 56+step || y != 92+step {
			t.Fatalf("sticky %d at %d,%d", i, x, y)
		}
		views[string(rune('a'+i))] = view{kind: v1.ViewFloating, output: "DP-1"}
	}
	if x, _ := cascade(views, "HDMI-A-1"); x != 56 {
		t.Fatal("cascade is per output")
	}
}

func TestPublisherSkipsUnchangedTrees(t *testing.T) {
	p := publisher{}
	if !p.changed("v1", notes.BarTree()) || p.changed("v1", notes.BarTree()) {
		t.Fatal("first publish must send and an identical one must not")
	}
	p.forget("v1")
	if !p.changed("v1", notes.BarTree()) {
		t.Fatal("a forgotten view must publish again")
	}
}

func TestStickyAndPanelShareOneDocument(t *testing.T) {
	sess, dir := newSessionForTest(t)
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sess.Select("a.md"); err != nil {
		t.Fatal(err)
	}
	if err := sess.TypeNote("a.md", "from sticky"); err != nil {
		t.Fatal(err)
	}
	sess.Tick()
	if snap := sess.Snap(); snap.Body != "from sticky" {
		t.Fatalf("panel sees %q", snap.Body)
	}
}
