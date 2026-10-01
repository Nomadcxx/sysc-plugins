package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSession(t *testing.T) (*Session, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	st, err := Open(t.TempDir(), "md")
	if err != nil {
		t.Fatal(err)
	}
	st.now = func() time.Time { return clock }
	return NewSession(st, func() time.Time { return clock }), &clock
}

func newTestSession(t *testing.T) *Session {
	t.Helper()
	s, _ := testSession(t)
	return s
}

func mustCreateNote(t *testing.T, s *Session, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.store.Dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableFolderErrorLasts(t *testing.T) {
	sess := NewSession(nil, time.Now)
	sess.ReportFolderError("Could not open notes folder: test failure")
	sess.ClearNotice()
	if got := sess.Snap().ScanError; got != "Could not open notes folder: test failure" {
		t.Fatalf("folder error = %q", got)
	}
}

func TestSubmitQueryOpensTopMatchOrCreates(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "Weekly review.md", "ship it")
	s.Search("weekly")
	name, err := s.SubmitQuery()
	if err != nil || name != "Weekly review.md" || s.Selected() != name {
		t.Fatalf("match: %q %v selected %q", name, err, s.Selected())
	}
	s.Search("Groceries")
	name, err = s.SubmitQuery()
	if err != nil || name != "Groceries.md" {
		t.Fatalf("create: %q %v", name, err)
	}
	if snap := s.Snap(); snap.Body != "Groceries\n" || snap.Query != "" || snap.Selected != "Groceries.md" {
		t.Fatalf("new note body %q query %q selected %q", snap.Body, snap.Query, snap.Selected)
	}
}

func TestSubmitQuerySanitisesFilename(t *testing.T) {
	s := newTestSession(t)
	s.Search("../../etc/passwd")
	name, err := s.SubmitQuery()
	if err != nil || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		t.Fatalf("got %q %v", name, err)
	}
	s.Search("   ")
	if name, err := s.SubmitQuery(); err != nil || name != "" {
		t.Fatalf("blank query must do nothing, got %q %v", name, err)
	}
}

func TestCreateFromQueryMakesBlankDatedNoteWhenEmpty(t *testing.T) {
	s := newTestSession(t)
	name, err := s.CreateFromQuery()
	if err != nil || name != "note-2026-09-02-120000.md" || s.Snap().Body != "" {
		t.Fatalf("blank create = %q %v", name, err)
	}
}

func TestCreateWithBodyNamesNoteAfterFirstLine(t *testing.T) {
	s := newTestSession(t)
	name, err := s.CreateWithBody("\n# Pasted idea\nmore text")
	if err != nil || name != "Pasted idea.md" || s.Selected() != name {
		t.Fatalf("paste create = %q %v", name, err)
	}
}

func TestNoticeClearsOnNextSuccess(t *testing.T) {
	s := newTestSession(t)
	s.Notify("The clipboard has no plain text")
	if s.Snap().Notice == "" {
		t.Fatal("notice not shown")
	}
	s.ClearNotice()
	if got := s.Snap(); got.Notice != "" || got.ScanError != "" {
		t.Fatalf("notice %q scan %q", got.Notice, got.ScanError)
	}
}

func TestSelectCommitsAPendingTitle(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "a.md", "x")
	mustCreateNote(t, s, "b.md", "y")
	must(t, s.Select("a.md"))
	s.SetTitleDraft("alpha")
	if got := s.Snap().Title; got != "alpha" {
		t.Fatalf("draft title shown as %q", got)
	}
	must(t, s.Select("b.md"))
	if _, _, err := s.store.Read("alpha.md"); err != nil {
		t.Fatalf("rename on leave did not happen: %v", err)
	}
	if from, to, ok := s.TakeRename(); !ok || from != "a.md" || to != "alpha.md" {
		t.Fatalf("rename not reported: %q %q %v", from, to, ok)
	}
	if _, _, ok := s.TakeRename(); ok {
		t.Fatal("a rename is reported once")
	}
}

func TestFailedRenameDoesNotBlockTheNextSelection(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "a.md", "x")
	mustCreateNote(t, s, "b.md", "y")
	must(t, s.Select("a.md"))
	s.SetTitleDraft("b")
	if err := s.Select("b.md"); err == nil {
		t.Fatal("renaming onto an existing note must fail")
	}
	if s.Selected() != "a.md" {
		t.Fatal("a failed rename must keep the selection")
	}
	must(t, s.Select("b.md"))
}

func TestSnapshotCarriesListAndSelectionTogether(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "a.md", "one two three")
	must(t, s.Select("a.md"))
	snap := s.Snap()
	if len(snap.Notes) != 1 || snap.Selected != "a.md" || snap.Words != 3 || snap.Now.IsZero() {
		t.Fatalf("snap = %+v", snap)
	}
}

func TestSessionDirtyAutosaveFlushAndSaveError(t *testing.T) {
	s, now := testSession(t)
	name, err := s.CreateFromQuery()
	must(t, err)
	must(t, s.Type("hello"))
	if !s.Dirty() {
		t.Fatal("typed text was not dirty")
	}
	s.Tick()
	if body, _, err := s.store.Read(name); err != nil || body == "hello" {
		t.Fatalf("saved before idle: %q %v", body, err)
	}
	*now = now.Add(2 * time.Second)
	s.Tick()
	if body, _, err := s.store.Read(name); err != nil || body != "hello" {
		t.Fatalf("autosave = %q %v", body, err)
	}
	must(t, s.Type("kept"))
	if err := os.Chmod(s.store.Dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.store.Dir, 0o755) })
	*now = now.Add(2 * time.Second)
	s.Tick()
	_ = os.Chmod(s.store.Dir, 0o755)
	if s.Snap().SaveError == "" || s.Buffer() != "kept" {
		t.Fatalf("save error = %+v", s.Snap())
	}
	must(t, s.Type("closed"))
	must(t, s.Close())
	if body, _, err := s.store.Read(name); err != nil || body != "closed" {
		t.Fatalf("close flush = %q %v", body, err)
	}
}

func TestSessionFavoriteDeleteConfirm(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "draft.md", "x")
	must(t, s.SetFavorite("draft.md", true))
	must(t, s.Select("draft.md"))
	if !s.Snap().Favorite {
		t.Fatal("favourite not shown")
	}
	s.ProposeDelete("draft.md")
	s.CancelPending()
	if _, _, err := s.store.Read("draft.md"); err != nil {
		t.Fatal("cancel deleted the note")
	}
	s.ProposeDelete("draft.md")
	name, err := s.ConfirmDeleteName()
	if err != nil || name != "draft.md" || s.Selected() != "" {
		t.Fatalf("confirm = %q %v selected %q", name, err, s.Selected())
	}
	if _, _, err := s.store.Read("draft.md"); err == nil {
		t.Fatal("confirm left the file")
	}
}

func TestSessionExternalCleanAndDirtyConflict(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "n.md", "local")
	must(t, s.Select("n.md"))
	must(t, s.store.Save("n.md", "disk"))
	s.Tick()
	if s.Buffer() != "disk" || s.Dirty() {
		t.Fatalf("clean reload = %+v", s.Snap())
	}
	must(t, s.Type("typed"))
	must(t, s.store.Save("n.md", "other"))
	s.Tick()
	if !s.Snap().Conflict || s.Buffer() != "typed" {
		t.Fatalf("dirty conflict = %+v", s.Snap())
	}
	must(t, s.KeepLocal())
	if s.Snap().Conflict || s.Buffer() != "typed" || !s.Dirty() {
		t.Fatalf("keep local = %+v", s.Snap())
	}
	must(t, s.store.Save("n.md", "again"))
	s.Tick()
	must(t, s.Reload())
	if s.Buffer() != "again" || s.Dirty() || s.Snap().Conflict {
		t.Fatalf("reload = %+v", s.Snap())
	}
}

func TestOpenScratchCreatesAndSelects(t *testing.T) {
	s := newTestSession(t)
	must(t, s.OpenScratch())
	if s.Selected() != "scratchpad.md" {
		t.Fatalf("selected %q", s.Selected())
	}
}

func TestConflictOnOneNoteDoesNotBlockOthers(t *testing.T) {
	s, now := testSession(t)
	mustCreateNote(t, s, "a.md", "base")
	mustCreateNote(t, s, "b.md", "other")
	must(t, s.TypeNote("a.md", "typed in a sticky"))
	must(t, s.store.Save("a.md", "changed in Obsidian"))
	*now = now.Add(time.Second)
	s.Tick()
	must(t, s.Select("b.md"))
	if _, err := s.CreateFromQuery(); err != nil {
		t.Fatalf("a conflict on another note blocked create: %v", err)
	}
	must(t, s.Select("a.md"))
	if snap := s.Snap(); !snap.Conflict || snap.Body != "typed in a sticky" {
		t.Fatalf("the conflicted note must open with its text and banner: %+v", snap)
	}
}

func TestSelectDropsCleanDocumentsItIsNotShowing(t *testing.T) {
	s, now := testSession(t)
	for _, n := range []string{"a.md", "b.md", "c.md"} {
		mustCreateNote(t, s, n, n)
		must(t, s.Select(n))
	}
	must(t, s.TypeNote("a.md", "unsaved"))
	must(t, s.store.Save("a.md", "changed in Obsidian"))
	*now = now.Add(time.Second)
	s.Tick()
	must(t, s.Select("b.md"))
	if len(s.docs) != 2 || s.docs["a.md"] == nil || s.docs["b.md"] == nil {
		t.Fatalf("docs kept = %v, want only the selected note and the one that cannot save", len(s.docs))
	}
}

func TestCloseCommitsAPendingTitle(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "a.md", "x")
	must(t, s.Select("a.md"))
	s.SetTitleDraft("alpha")
	must(t, s.Close())
	if _, _, err := s.store.Read("alpha.md"); err != nil {
		t.Fatalf("closing the panel must commit the typed title: %v", err)
	}
}

// Rename and create clean a title the same way, and neither keeps characters
// that break a path or an Obsidian link.
func TestRenameAndCreateCleanTitlesAlike(t *testing.T) {
	s := newTestSession(t)
	name, err := s.CreateWithBody("a [b] #c ^d|e: f/g\nbody")
	if err != nil || name != "a b c d e f g.md" {
		t.Fatalf("create = %q %v", name, err)
	}
	s.SetTitleDraft("x:y [z]")
	must(t, s.CommitTitle())
	if got := s.Selected(); got != "x y z.md" {
		t.Fatalf("rename = %q, want x y z.md", got)
	}
}

// The Scratchpad is a fixed place, not a note to rename: its title reads as
// Scratchpad and a stray draft never moves the file.
func TestScratchpadKeepsItsName(t *testing.T) {
	s := newTestSession(t)
	must(t, s.OpenScratch())
	if got := s.Snap().Title; got != ScratchpadTitle {
		t.Fatalf("title = %q, want %q", got, ScratchpadTitle)
	}
	s.SetTitleDraft("Ideas")
	must(t, s.CommitTitle())
	if got := s.Selected(); got != "scratchpad.md" {
		t.Fatalf("scratchpad renamed to %q", got)
	}
}

// New with an empty search makes a note to type into; leaving it untouched
// must not leave an empty file behind.
func TestAnUntouchedBlankNoteIsDiscardedOnLeave(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "kept.md", "x")
	blank, err := s.CreateFromQuery()
	must(t, err)
	must(t, s.Select("kept.md"))
	if _, _, err := s.store.Read(blank); !os.IsNotExist(err) {
		t.Fatalf("blank note survived leaving it: %v", err)
	}

	typed, err := s.CreateFromQuery()
	must(t, err)
	must(t, s.Type("something"))
	must(t, s.Select("kept.md"))
	if body, _, err := s.store.Read(typed); err != nil || body != "something" {
		t.Fatalf("a typed note must stay: %q %v", body, err)
	}

	held, err := s.CreateFromQuery()
	must(t, err)
	s.Keep(held)
	must(t, s.Close())
	if _, _, err := s.store.Read(held); err != nil {
		t.Fatalf("a note kept open elsewhere was discarded: %v", err)
	}
}

// One file the library cannot show is skipped and counted, not a reason to
// show nothing.
func TestOneBadFileDoesNotBlankTheLibrary(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "good.md", "fine")
	must(t, os.WriteFile(filepath.Join(s.store.Dir, "huge.md"), make([]byte, maxNoteBytes+1), 0o600))
	must(t, os.Symlink(filepath.Join(s.store.Dir, "good.md"), filepath.Join(s.store.Dir, "link.md")))
	snap := s.Snap()
	if snap.ScanError != "" || len(snap.Notes) != 1 || snap.Notes[0].Name != "good.md" {
		t.Fatalf("library = %+v, scan error %q", snap.Notes, snap.ScanError)
	}
	if snap.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2", snap.Skipped)
	}
}

// The bar tooltip counts notes in the folder: not the panel's search, and
// not the Scratchpad.
func TestTooltipStatsIgnoreSearchAndScratchpad(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "a.md", "alpha")
	mustCreateNote(t, s, "b.md", "beta")
	must(t, s.OpenScratch())
	s.Search("alpha")
	if count, _ := s.Stats(); count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

// A note moved or deleted elsewhere while it has unsaved text keeps the text
// and offers to save it back or let it go.
func TestAMovedNoteCanBeSavedBackOrDiscarded(t *testing.T) {
	s, clock := testSession(t)
	mustCreateNote(t, s, "a.md", "old")
	must(t, s.Select("a.md"))
	must(t, s.Type("unsaved words"))
	must(t, os.Remove(filepath.Join(s.store.Dir, "a.md")))
	*clock = clock.Add(time.Second)
	s.Tick()
	snap := s.Snap()
	if !snap.Missing || snap.Body != "unsaved words" {
		t.Fatalf("missing=%v body=%q, want the text kept and the loss flagged", snap.Missing, snap.Body)
	}
	must(t, s.Restore())
	if body, _, err := s.store.Read("a.md"); err != nil || body != "unsaved words" {
		t.Fatalf("restored file = %q %v", body, err)
	}
	if snap := s.Snap(); snap.Missing || snap.Dirty || snap.SaveError != "" {
		t.Fatalf("after restore: %+v", snap)
	}

	must(t, os.Remove(filepath.Join(s.store.Dir, "a.md")))
	s.Tick()
	must(t, s.Discard())
	if snap := s.Snap(); snap.Selected != "" {
		t.Fatalf("discard left %q open", snap.Selected)
	}
}

// Ctrl+S saves at once rather than at the next autosave tick.
func TestSaveNowWritesImmediately(t *testing.T) {
	s := newTestSession(t)
	mustCreateNote(t, s, "a.md", "")
	must(t, s.Select("a.md"))
	must(t, s.Type("right now"))
	must(t, s.SaveNow())
	if body, _, err := s.store.Read("a.md"); err != nil || body != "right now" {
		t.Fatalf("file = %q %v", body, err)
	}
	if s.Snap().Dirty {
		t.Fatal("still dirty after SaveNow")
	}
}
