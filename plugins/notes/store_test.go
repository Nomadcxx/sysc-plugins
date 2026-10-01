package notes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStoreSearchFavoritesAndDirectChildren(t *testing.T) {
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("alpha.md", "Plan the launch\nremember the checklist"); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("beta.md", "A quiet afternoon"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, ".hidden.md"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.Dir, "folder.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Favorite("beta.md", true); err != nil {
		t.Fatal(err)
	}
	items, err := s.List("checklist", false)
	if err != nil || len(items) != 1 || items[0].Name != "alpha.md" {
		t.Fatalf("search = %#v, %v", items, err)
	}
	items, err = s.List("", true)
	if err != nil || len(items) != 2 || items[0].Name != "beta.md" || !items[0].Favorite {
		t.Fatalf("favorites first = %#v, %v", items, err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(s.Dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List("", false); err == nil {
		t.Fatal("symlink note was silently followed or ignored")
	}
	if err := s.Delete("../outside.md"); err == nil {
		t.Fatal("path traversal was accepted")
	}
}

func TestSaveIfUnchangedPreservesExternalEdit(t *testing.T) {
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("plan.md", "Obsidian version"); err != nil {
		t.Fatal(err)
	}
	conflict, err := s.SaveIfUnchanged("plan.md", "stale version", "Notes version")
	if !errors.Is(err, errNoteChanged) || conflict != "Obsidian version" {
		t.Fatalf("stale save = %q, %v", conflict, err)
	}
	body, _, err := s.Read("plan.md")
	if err != nil || body != "Obsidian version" {
		t.Fatalf("external edit changed to %q, %v", body, err)
	}
}

func TestAtomicWriteCheckKeepsExternalReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := atomicWriteChecked(path, []byte("Notes version"), 0o600, func() error {
		if err := os.WriteFile(path, []byte("Obsidian version"), 0o600); err != nil {
			return err
		}
		return errNoteChanged
	})
	if !errors.Is(err, errNoteChanged) {
		t.Fatalf("checked atomic write error = %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "Obsidian version" {
		t.Fatalf("external replacement = %q, %v", body, err)
	}
}

func TestRenameDoesNotReplaceDestination(t *testing.T) {
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("draft.md", "draft"); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("plan.md", "existing plan"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("draft.md", "plan.md"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("rename over existing note = %v", err)
	}
	for name, want := range map[string]string{"draft.md": "draft", "plan.md": "existing plan"} {
		body, _, err := s.Read(name)
		if err != nil || body != want {
			t.Errorf("%s = %q, %v; want %q", name, body, err, want)
		}
	}
}

func TestFailedFavoriteWriteRollsBackMemoryState(t *testing.T) {
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("plan.md", "Plan"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.Dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Favorite("plan.md", true); err == nil {
		t.Fatal("favorite succeeded with a missing notes folder")
	}
	if s.IsFavorite("plan.md") {
		t.Fatal("failed favorite write changed in-memory state")
	}
}

func TestBlankCreatesDoNotOverwriteWithinOneSecond(t *testing.T) {
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 9, 24, 12, 34, 56, 0, time.UTC) }
	s.now = now
	sess := NewSession(s, now)
	first, err := sess.CreateFromQuery()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Type("one"); err != nil {
		t.Fatal(err)
	}
	second, err := sess.CreateFromQuery()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first != "note-2026-09-24-123456.md" || second != "note-2026-09-24-123456 2.md" {
		t.Fatalf("blank note names = %q and %q", first, second)
	}
	body, _, err := s.Read(first)
	if err != nil || body != "one" {
		t.Fatalf("first note = %q, %v", body, err)
	}
	if got := displayTitle(second, "Second idea"); got != "Second idea" {
		t.Fatalf("a numbered capture name must still show its first line, got %q", got)
	}
}

func TestSessionConflictKeepLocalAndCleanExternalReload(t *testing.T) {
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("plan.md", "base"); err != nil {
		t.Fatal(err)
	}
	nowAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	sess := NewSession(s, func() time.Time { return nowAt })
	if err := sess.Select("plan.md"); err != nil {
		t.Fatal(err)
	}
	if err := sess.Type("local version"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("plan.md", "edited in Obsidian"); err != nil {
		t.Fatal(err)
	}
	nowAt = nowAt.Add(time.Second)
	sess.Tick()
	if got := sess.Snap(); !got.Conflict || got.ConflictBody != "edited in Obsidian" || !got.Dirty {
		t.Fatalf("conflict snapshot = %+v", got)
	}
	if err := sess.KeepLocal(""); err != nil {
		t.Fatal(err)
	}
	nowAt = nowAt.Add(time.Second)
	sess.Tick()
	body, _, err := s.Read("plan.md")
	if err != nil || body != "local version" {
		t.Fatalf("kept body = %q, %v", body, err)
	}
	if err := s.Save("plan.md", "changed cleanly in Obsidian"); err != nil {
		t.Fatal(err)
	}
	if got := sess.Snap(); got.Body != "changed cleanly in Obsidian" || got.Dirty {
		t.Fatalf("clean external edit = %+v", got)
	}
}

func TestFailedSaveKeepsTheBufferAcrossNavigation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("note.md", "disk"); err != nil {
		t.Fatal(err)
	}
	sess := NewSession(s, time.Now)
	if err := sess.Select("note.md"); err != nil {
		t.Fatal(err)
	}
	if err := sess.Type("local text that must survive"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "note.md")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "note.md")); err != nil {
		t.Fatal(err)
	}
	if err := sess.Select(""); err != nil {
		t.Fatalf("a note that will not save must not trap the editor: %v", err)
	}
	if err := sess.Select("note.md"); err != nil {
		t.Fatal(err)
	}
	got := sess.Snap()
	if got.Selected != "note.md" || got.Body != "local text that must survive" || !got.Dirty || got.SaveError == "" {
		t.Fatalf("buffer not retained: %+v", got)
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "untouched" {
		t.Fatalf("outside file changed: %q, %v", body, err)
	}
}

func TestRenameAndConfirmedDelete(t *testing.T) {
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create("draft.md", "content"); err != nil {
		t.Fatal(err)
	}
	sess := NewSession(s, time.Now)
	if err := sess.Select("draft.md"); err != nil {
		t.Fatal(err)
	}
	sess.SetTitleDraft("Roadmap")
	if err := sess.CommitTitle(); err != nil {
		t.Fatal(err)
	}
	if got := sess.Snap(); got.Selected != "Roadmap.md" || got.Title != "Roadmap" {
		t.Fatalf("renamed state = %+v", got)
	}
	sess.ProposeDelete("Roadmap.md")
	name, err := sess.ConfirmDeleteName()
	if err != nil || name != "Roadmap.md" {
		t.Fatalf("confirm delete = %q, %v", name, err)
	}
	if _, _, err := s.Read(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted note still exists: %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustCreate(t *testing.T, s *Store, name, body string) {
	t.Helper()
	if err := s.Create(name, body); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayTitleUsesFirstLineForCaptureNames(t *testing.T) {
	cases := map[[2]string]string{
		{"note-2026-10-01-091233.md", "\n# Call back about the lease\nbody"}: "Call back about the lease",
		{"note-2026-10-01-091233-02.md", ""}:                                 "note-2026-10-01-091233-02",
		{"Weekly review.md", "# Something else"}:                             "Weekly review",
	}
	for in, want := range cases {
		if got := displayTitle(in[0], in[1]); got != want {
			t.Errorf("displayTitle(%q) = %q, want %q", in[0], got, want)
		}
	}
}

func TestListShowsDisplayTitlesAndWordCounts(t *testing.T) {
	s := openTestStore(t)
	mustCreate(t, s, "note-2026-10-01-091233.md", "Call back\nabout the lease")
	items, err := s.List("", false)
	if err != nil || len(items) != 1 || items[0].Title != "Call back" || items[0].Words != 5 {
		t.Fatalf("items = %+v, %v", items, err)
	}
}

func TestCreateTitledNamesTheFileAfterTheTitle(t *testing.T) {
	s := openTestStore(t)
	name, err := s.CreateTitled("Groceries", "Groceries\n")
	if err != nil || name != "Groceries.md" {
		t.Fatalf("got %q, %v", name, err)
	}
	name, err = s.CreateTitled("Groceries", "")
	if err != nil || name != "Groceries 2.md" {
		t.Fatalf("collision got %q, %v", name, err)
	}
}

func TestCreateTitledSanitisesHostileTitles(t *testing.T) {
	s := openTestStore(t)
	for _, title := range []string{"../escape", "a/b\\c:d", "   ", ".hidden", strings.Repeat("x", 300), strings.Repeat("é", 100)} {
		name, err := s.CreateTitled(title, "")
		if err != nil {
			t.Fatalf("%q: %v", title, err)
		}
		if strings.ContainsAny(name, `/\:`) || strings.HasPrefix(name, ".") || len(name) > 84 || !utf8.ValidString(name) {
			t.Errorf("%q produced unsafe name %q", title, name)
		}
	}
}

func TestListRereadsOnlyChangedFiles(t *testing.T) {
	s := openTestStore(t)
	mustCreate(t, s, "a.md", "alpha")
	if _, err := s.List("", false); err != nil {
		t.Fatal(err)
	}
	reads := s.bodyReads
	if _, err := s.List("", false); err != nil {
		t.Fatal(err)
	}
	if s.bodyReads != reads {
		t.Fatalf("unchanged folder re-read %d bodies", s.bodyReads-reads)
	}
	if err := s.Save("a.md", "alpha beta gamma"); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List("gamma", false)
	if len(items) != 1 || s.bodyReads != reads+1 {
		t.Fatalf("changed file not re-read: items %d reads %d", len(items), s.bodyReads-reads)
	}
}
