package notes

import (
	"os"
	"testing"
	"time"
)

func TestSessionListCreateOpenAndBack(t *testing.T) {
	t.Parallel()
	s, now := testSession(t)
	if s.Page() != "list" {
		t.Fatalf("page = %q", s.Page())
	}
	if err := s.Create(); err != nil {
		t.Fatal(err)
	}
	if s.Page() != "editor" || s.Current() == "" {
		t.Fatalf("create did not open editor: %+v", s.Snap())
	}
	s.Back()
	if s.Page() != "list" {
		t.Fatalf("back = %q", s.Page())
	}
	if err := s.Open("scratchpad.md"); err != nil {
		t.Fatal(err)
	}
	if s.Page() != "editor" || s.Current() != "scratchpad.md" {
		t.Fatal("scratchpad not opened")
	}
	_ = now
}

func TestSessionDirtyAutosaveFlushAndSaveError(t *testing.T) {
	t.Parallel()
	s, now := testSession(t)
	if err := s.Create(); err != nil {
		t.Fatal(err)
	}
	name := s.Current()
	s.Type("hello")
	if !s.Dirty() {
		t.Fatal("typed text was not dirty")
	}
	s.Tick()
	body, _, err := s.store.Read(name)
	if err != nil || body == "hello" {
		t.Fatalf("saved before idle: %q %v", body, err)
	}
	*now = now.Add(2 * time.Second)
	s.Tick()
	body, _, err = s.store.Read(name)
	if err != nil || body != "hello" {
		t.Fatalf("autosave = %q %v", body, err)
	}
	if s.Dirty() {
		t.Fatal("still dirty after autosave")
	}
	s.Type("kept")
	if err := os.Chmod(s.store.Dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.store.Dir, 0755) })
	*now = now.Add(2 * time.Second)
	s.Tick()
	_ = os.Chmod(s.store.Dir, 0755)
	if s.Snap().SaveErr == "" || s.Buffer() != "kept" {
		t.Fatalf("save error = %+v", s.Snap())
	}
	s.Type("closed")
	_ = os.Chmod(s.store.Dir, 0755)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	body, _, err = s.store.Read(name)
	if err != nil || body != "closed" {
		t.Fatalf("close flush = %q %v", body, err)
	}
}

func TestSessionPinRenameDeleteConfirm(t *testing.T) {
	t.Parallel()
	s, _ := testSession(t)
	if err := s.Create(); err != nil {
		t.Fatal(err)
	}
	old := s.Current()
	s.Type("x")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(old, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(old); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("renamed.md"); err != nil {
		t.Fatal(err)
	}
	if s.Current() != "renamed.md" {
		t.Fatalf("current = %q", s.Current())
	}
	s.Back()
	s.ProposeDelete("renamed.md")
	s.CancelPending()
	if _, _, err := s.store.Read("renamed.md"); err != nil {
		t.Fatal("cancel deleted the note")
	}
	s.ProposeDelete("renamed.md")
	if err := s.ConfirmDelete(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.Read("renamed.md"); err == nil {
		t.Fatal("confirm left the file")
	}
}

func TestSessionExternalCleanAndDirtyConflict(t *testing.T) {
	t.Parallel()
	s, _ := testSession(t)
	if err := s.Create(); err != nil {
		t.Fatal(err)
	}
	name := s.Current()
	s.Type("local")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(name); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Save(name, "disk"); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	if s.Buffer() != "disk" || s.Dirty() {
		t.Fatalf("clean reload = %+v", s.Snap())
	}
	s.Type("typed")
	if err := s.store.Save(name, "other"); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	if !s.Snap().Conflict || s.Buffer() != "typed" {
		t.Fatalf("dirty conflict = %+v", s.Snap())
	}
	s.KeepLocal()
	if s.Snap().Conflict || s.Buffer() != "typed" || !s.Dirty() {
		t.Fatalf("keep local = %+v", s.Snap())
	}
	if err := s.store.Save(name, "again"); err != nil {
		t.Fatal(err)
	}
	s.Tick()
	s.Reload()
	if s.Buffer() != "again" || s.Dirty() || s.Snap().Conflict {
		t.Fatalf("reload = %+v", s.Snap())
	}
}

func testSession(t *testing.T) (*Session, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	clock := now
	st, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	st.now = func() time.Time { return clock }
	s := NewSession(st, func() time.Time { return clock })
	return s, &clock
}

func TestSessionCounts(t *testing.T) {
	t.Parallel()
	s, _ := testSession(t)
	_ = s.Create()
	s.Type("two words")
	snap := s.Snap()
	if snap.Words != 2 || snap.Chars != 9 {
		t.Fatalf("counts = %+v", snap)
	}
}
