package main

import (
	"strings"
	"testing"
)

func TestInvalidNotesFolderLeavesManagerRecoverable(t *testing.T) {
	sess := applySettings(nil, map[string]any{"notes_dir": "\x00"})
	if sess == nil {
		t.Fatal("invalid folder prevented Notes from creating a recoverable session")
	}
	if got := sess.Snap().LibraryError; !strings.Contains(got, "Could not open notes folder") {
		t.Fatalf("folder error = %q", got)
	}
	sess = applySettings(sess, map[string]any{"notes_dir": t.TempDir()})
	if got := sess.Snap().LibraryError; got != "" {
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
