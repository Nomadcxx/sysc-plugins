package notes

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreExpandsHomeAndRejectsEscapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Notes")
	s, err := Open("~/Notes", "md")
	if err != nil {
		t.Fatal(err)
	}
	if s.Dir != dir {
		t.Fatalf("dir = %q, want expanded %q", s.Dir, dir)
	}
	if _, _, err := s.Read("../etc/passwd.md"); err == nil {
		t.Fatal("escaped parent was accepted")
	}
	if _, _, err := s.Read("sub/note.md"); err == nil {
		t.Fatal("nested path was accepted")
	}
	if err := s.Save("note.txt", "x"); err == nil {
		t.Fatal("wrong extension was accepted")
	}
}

func TestStoreScratchpadListOrderAndPins(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	if name, err := s.Scratchpad(); err != nil || name != "scratchpad.md" {
		t.Fatalf("scratchpad = %q %v", name, err)
	}
	mustSave(t, s, "b.md", "b")
	mustSave(t, s, "a.md", "a")
	if err := s.SetPinned("b.md", true); err != nil {
		t.Fatal(err)
	}
	notes, err := s.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 || notes[0].Name != "b.md" || notes[1].Name != "a.md" {
		t.Fatalf("list = %+v, want pinned b then a", notes)
	}
	if notes[0].Pinned != true || notes[1].Pinned {
		t.Fatalf("pin flags = %+v", notes)
	}
}

func TestStoreCreateSanitizesAndAvoidsCollisions(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	s.now = func() time.Time { return time.Date(2026, 9, 2, 15, 4, 5, 0, time.UTC) }
	n1, err := s.Create()
	if err != nil || n1 != "2026-09-02 15.04.05.md" {
		t.Fatalf("create = %q %v", n1, err)
	}
	n2, err := s.Create()
	if err != nil || n2 == n1 {
		t.Fatalf("collision reuse %q %v", n2, err)
	}
	if err := s.Rename(n1, "ok.md"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("ok.md", "../x.md"); err == nil {
		t.Fatal("rename escape accepted")
	}
	if err := s.Rename("ok.md", n2); err == nil {
		t.Fatal("rename onto existing accepted")
	}
}

func TestStoreAtomicSaveSurvivesFailure(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	mustSave(t, s, "n.md", "keep")
	if err := os.Chmod(s.Dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.Dir, 0755) })
	if err := s.Save("n.md", "lost"); err == nil {
		t.Fatal("save into a read-only directory succeeded")
	}
	_ = os.Chmod(s.Dir, 0755)
	got, _, err := s.Read("n.md")
	if err != nil || got != "keep" {
		t.Fatalf("after failed save: %q %v", got, err)
	}
}

func TestStoreDeleteAndExternalProbe(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	mustSave(t, s, "n.md", "old")
	kind, body, err := s.Probe("n.md", "old", false)
	if err != nil || kind != Unchanged || body != "" {
		t.Fatalf("unchanged: %s %q %v", kind, body, err)
	}
	mustSave(t, s, "n.md", "new")
	kind, body, err = s.Probe("n.md", "old", false)
	if err != nil || kind != CleanReload || body != "new" {
		t.Fatalf("clean: %s %q %v", kind, body, err)
	}
	kind, body, err = s.Probe("n.md", "typed", true)
	if err != nil || kind != DirtyConflict || body != "new" {
		t.Fatalf("dirty: %s %q %v", kind, body, err)
	}
	if err := s.Delete("n.md"); err != nil {
		t.Fatal(err)
	}
	kind, _, err = s.Probe("n.md", "typed", true)
	if err != nil || kind != Deleted {
		t.Fatalf("deleted: %s %v", kind, err)
	}
}

func TestStoreRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.Dir, "trap.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read("trap.md"); err == nil {
		t.Fatal("symlink escape was read")
	}
	if err := s.Save("trap.md", "x"); err == nil {
		t.Fatal("symlink escape was written")
	}
}

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustSave(t *testing.T, s *Store, name, body string) {
	t.Helper()
	if err := s.Save(name, body); err != nil {
		t.Fatal(err)
	}
}
