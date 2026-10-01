package notes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	ScratchpadTitle = "Scratchpad"
	autosaveDelay   = 600 * time.Millisecond
)

// Document is one note's editing state. The panel editor and every sticky for
// the same file share it, so they cannot overwrite each other.
type Document struct {
	Name     string
	Body     string
	Disk     string
	Dirty    bool
	Error    string
	Conflict string
	// Missing says the file was moved or deleted outside Notes while open;
	// Body still holds the text.
	Missing  bool
	Modified time.Time
	Edited   time.Time
}

// Snapshot is everything the panel draws: the library and the selected note
// together, since both are on screen at once.
type Snapshot struct {
	Notes        []Summary
	Selected     string
	Title        string
	Body         string
	Dirty        bool
	SaveError    string
	Conflict     bool
	ConflictBody string
	Missing      bool
	Favorite     bool
	Words        int
	Modified     time.Time
	ScanError    string
	// Skipped counts notes the library could not show.
	Skipped       int
	Notice        string
	PendingDelete string
	Query         string
	SortByName    bool
	Now           time.Time
}

type Session struct {
	mu            sync.Mutex
	store         *Store
	docs          map[string]*Document
	selected      string
	query         string
	sortByName    bool
	pendingDelete string
	// notice is a transient action failure; folderError is a lasting one.
	notice      string
	folderError string
	titleDraft  string
	// renamed records a rename the session made on its own (rename on
	// leave), for the caller to carry sticky state across.
	renamedFrom, renamedTo string
	// blank names notes New made with no text. One still empty when the
	// user moves on is removed, so New leaves no empty files behind.
	blank map[string]bool
	now   func() time.Time
}

func NewSession(store *Store, now func() time.Time) *Session {
	if now == nil {
		now = time.Now
	}
	return &Session{store: store, docs: map[string]*Document{}, blank: map[string]bool{}, now: now}
}

func (s *Session) Snap() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapLocked()
}

func (s *Session) snapLocked() Snapshot {
	snap := Snapshot{Notice: s.notice, Query: s.query, SortByName: s.sortByName, PendingDelete: s.pendingDelete, Now: s.now()}
	if s.store == nil {
		snap.ScanError = s.folderError
		if snap.ScanError == "" {
			snap.ScanError = "Notes folder is unavailable"
		}
		return snap
	}
	if doc := s.docs[s.selected]; doc != nil {
		if !doc.Dirty {
			if body, modified, err := s.store.Read(doc.Name); err == nil && body != doc.Disk {
				doc.Body, doc.Disk, doc.Modified, doc.Error, doc.Conflict = body, body, modified, "", ""
			}
		}
		snap.Selected = doc.Name
		snap.Title = strings.TrimSuffix(doc.Name, filepath.Ext(doc.Name))
		if s.titleDraft != "" {
			snap.Title = s.titleDraft
		}
		if isScratch(doc.Name) {
			snap.Title = ScratchpadTitle
		}
		snap.Body, snap.Dirty, snap.SaveError = doc.Body, doc.Dirty, doc.Error
		snap.ConflictBody, snap.Conflict = doc.Conflict, doc.Conflict != ""
		snap.Missing = doc.Missing
		snap.Words, snap.Modified = len(strings.Fields(doc.Body)), doc.Modified
		snap.Favorite = s.store.IsFavorite(doc.Name)
	}
	items, err := s.store.List(s.query, s.sortByName)
	if err != nil {
		snap.ScanError = err.Error()
	} else {
		snap.Notes = items
		snap.Skipped = s.store.Skipped()
	}
	if s.folderError != "" {
		snap.ScanError = s.folderError
	}
	return snap
}

// Document returns the shared editing state used by the panel and a sticky.
func (s *Session) Document(name string) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(name); err != nil {
		return Document{}, err
	}
	doc := s.docs[name]
	if doc.Dirty {
		if disk, _, err := s.store.Read(name); err == nil && disk != doc.Disk {
			doc.Conflict = disk
		}
	} else if body, modified, err := s.store.Read(name); err == nil && body != doc.Disk {
		doc.Body, doc.Disk, doc.Modified = body, body, modified
	}
	return *doc, nil
}

// Select makes name the note in the editor. It tries to save every open note
// and commits a pending title first. A note that will not save keeps its text
// and error in memory, so it never blocks moving to another note; a failed
// rename does keep the selection, once. An empty name clears the selection.
func (s *Session) Select(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectLocked(name)
}

func (s *Session) selectLocked(name string) error {
	if s.store == nil {
		return errors.New("notes: folder is unavailable")
	}
	s.saveAllLocked()
	if err := s.commitTitleLocked(); err != nil {
		return err
	}
	s.discardBlankLocked(name)
	if name != "" {
		if err := s.ensureLocked(name); err != nil {
			return err
		}
	}
	s.selected, s.pendingDelete = name, ""
	s.dropIdleLocked()
	return nil
}

// dropIdleLocked forgets documents with nothing unsaved that the editor is
// not showing, so the tick only re-reads notes that are open somewhere. A
// sticky's document comes back on its next refresh.
func (s *Session) dropIdleLocked() {
	for name, doc := range s.docs {
		if name != s.selected && !doc.Dirty && doc.Conflict == "" && doc.Error == "" {
			delete(s.docs, name)
		}
	}
}

func (s *Session) OpenScratch() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return errors.New("notes: folder is unavailable")
	}
	name := s.store.ScratchName()
	if _, _, err := s.store.Read(name); errors.Is(err, os.ErrNotExist) {
		if err := s.store.Create(name, ""); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	} else if err != nil {
		return err
	}
	return s.selectLocked(name)
}

// SubmitQuery is Enter in the search box: open the top match, or, when
// nothing matches, create a note whose first line is the query. A blank query
// does nothing.
func (s *Session) SubmitQuery() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.TrimSpace(s.query)
	if q == "" || s.store == nil {
		return "", nil
	}
	items, err := s.store.List(q, s.sortByName)
	if err != nil {
		return "", err
	}
	if len(items) > 0 {
		return items[0].Name, s.selectLocked(items[0].Name)
	}
	name, err := s.createLocked(q, q+"\n")
	if err == nil {
		s.query = ""
	}
	return name, err
}

// CreateFromQuery is the New note button: a note from the search text, or a
// blank dated note when the box is empty.
func (s *Session) CreateFromQuery() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.TrimSpace(s.query)
	body := ""
	if q != "" {
		body = q + "\n"
	}
	name, err := s.createLocked(q, body)
	if err == nil {
		s.query = ""
	}
	return name, err
}

// CreateWithBody creates a note from text (a paste), named after its first line.
func (s *Session) CreateWithBody(body string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(firstLine(body), body)
}

func (s *Session) createLocked(title, body string) (string, error) {
	if s.store == nil {
		return "", errors.New("notes: folder is unavailable")
	}
	if len(body) > v1.MaxInputBytes {
		return "", fmt.Errorf("notes: text exceeds the %d-byte editor limit", v1.MaxInputBytes)
	}
	s.saveAllLocked()
	if err := s.commitTitleLocked(); err != nil {
		return "", err
	}
	name, err := s.store.CreateTitled(title, body)
	if err != nil {
		return "", err
	}
	s.discardBlankLocked(name)
	s.docs[name] = &Document{Name: name, Body: body, Disk: body, Modified: s.now()}
	s.selected, s.pendingDelete = name, ""
	if body == "" {
		s.blank[name] = true
	}
	return name, nil
}

// discardBlankLocked removes every note New made that is still empty, other
// than keep. A note whose file has text, however it got there, stays.
func (s *Session) discardBlankLocked(keep string) {
	for name := range s.blank {
		if name == keep {
			continue
		}
		delete(s.blank, name)
		if doc := s.docs[name]; doc != nil && (doc.Dirty || doc.Body != "") {
			continue
		}
		if body, _, err := s.store.Read(name); err != nil || body != "" {
			continue
		}
		if err := s.store.Delete(name); err != nil {
			continue
		}
		delete(s.docs, name)
		if s.selected == name {
			s.selected = ""
		}
	}
}

// Keep marks name as wanted though it may still be empty: a sticky is open
// on it.
func (s *Session) Keep(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blank, name)
}

// Stats is what the bar tooltip reports: the notes in the folder, ignoring
// the panel's search and the Scratchpad, and the latest edit to any of them.
func (s *Session) Stats() (count int, last time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return 0, time.Time{}
	}
	items, err := s.store.List("", false)
	if err != nil {
		return 0, time.Time{}
	}
	for _, n := range items {
		if n.Modified.After(last) {
			last = n.Modified
		}
		if !isScratch(n.Name) {
			count++
		}
	}
	return count, last
}

func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); t != "" {
			return t
		}
	}
	return ""
}

func (s *Session) ensureLocked(name string) error {
	if s.store == nil {
		return errors.New("notes: folder is unavailable")
	}
	if _, ok := s.docs[name]; ok {
		return nil
	}
	body, modified, err := s.store.Read(name)
	if err != nil {
		return fmt.Errorf("notes: open %s: %w", name, err)
	}
	if len(body) > v1.MaxInputBytes {
		return fmt.Errorf("notes: %s exceeds the %d-byte editor limit; edit this file in Obsidian", name, v1.MaxInputBytes)
	}
	s.docs[name] = &Document{Name: name, Body: body, Disk: body, Modified: modified}
	return nil
}

// Type edits the selected note.
func (s *Session) Type(body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected == "" {
		return errors.New("notes: no note is open")
	}
	return s.typeLocked(s.selected, body)
}

func (s *Session) TypeNote(name, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(name); err != nil {
		return err
	}
	return s.typeLocked(name, body)
}

func (s *Session) typeLocked(name, body string) error {
	doc := s.docs[name]
	if len(body) > v1.MaxInputBytes {
		doc.Error = fmt.Sprintf("Note exceeds the %d-byte editor limit", v1.MaxInputBytes)
		return errors.New(doc.Error)
	}
	doc.Body, doc.Dirty, doc.Edited, doc.Error = body, body != doc.Disk, s.now(), ""
	return nil
}

func (s *Session) flushDocLocked(doc *Document) error {
	if doc == nil || !doc.Dirty {
		return nil
	}
	if doc.Missing {
		return errors.New(missingNote)
	}
	conflict, err := s.store.SaveIfUnchanged(doc.Name, doc.Disk, doc.Body)
	if errors.Is(err, os.ErrNotExist) {
		doc.Missing, doc.Error = true, missingNote
		return errors.New(missingNote)
	}
	if errors.Is(err, errNoteChanged) {
		doc.Conflict, doc.Error = conflict, "This note changed outside Notes"
		return errors.New(doc.Error)
	}
	if err != nil {
		doc.Error = "Save failed: " + err.Error()
		return err
	}
	doc.Disk, doc.Dirty, doc.Conflict, doc.Error, doc.Modified = doc.Body, false, "", "", s.now()
	return nil
}

// saveAllLocked tries to save every document and leaves any failure on the
// document itself, where its editor or sticky shows it.
func (s *Session) saveAllLocked() {
	for _, doc := range s.docs {
		_ = s.flushDocLocked(doc)
	}
}

func (s *Session) flushAllLocked() error {
	for _, doc := range s.docs {
		if err := s.flushDocLocked(doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return
	}
	for _, doc := range s.docs {
		body, modified, readErr := s.store.Read(doc.Name)
		if errors.Is(readErr, os.ErrNotExist) {
			// Moved or deleted elsewhere. Saving would recreate it behind the
			// user's back, so the text waits for Restore or Discard.
			doc.Missing, doc.Error = true, missingNote
			continue
		}
		if readErr == nil && doc.Missing {
			// Back again (undone elsewhere): compare as usual from here.
			doc.Missing, doc.Error = false, ""
		}
		if readErr == nil && !doc.Dirty && body != doc.Disk {
			doc.Body, doc.Disk, doc.Modified = body, body, modified
		}
		if readErr == nil && doc.Dirty && body != doc.Disk {
			doc.Conflict, doc.Error = body, "This note changed outside Notes"
			continue
		}
		if !doc.Dirty {
			continue
		}
		if s.now().Sub(doc.Edited) >= autosaveDelay {
			_ = s.flushDocLocked(doc)
		}
	}
}

// Reload discards local text for name (the selected note when empty) and
// takes the file's current contents.
func (s *Session) Reload(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.target(names)
	if name == "" {
		return errors.New("notes: no note is open")
	}
	body, modified, err := s.store.Read(name)
	if err != nil {
		return err
	}
	doc := s.docs[name]
	if doc == nil {
		doc = &Document{Name: name}
		s.docs[name] = doc
	}
	doc.Body, doc.Disk, doc.Dirty, doc.Error, doc.Conflict, doc.Modified = body, body, false, "", "", modified
	return nil
}

// SaveNow writes the selected note at once, with any pending title, for
// Ctrl+S: autosave would get there within a second, but a writer who asks to
// save should not have to wait to see "Saved".
func (s *Session) SaveNow() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.commitTitleLocked(); err != nil {
		return err
	}
	return s.flushDocLocked(s.docs[s.selected])
}

// missingNote is the error a note shows once its file has gone.
const missingNote = "Moved or deleted outside Notes"

// Restore writes a missing note's text back under its old name. It never
// replaces a file: if one has appeared there since, that is a conflict.
func (s *Session) Restore(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[s.target(names)]
	if doc == nil || !doc.Missing {
		return errors.New("notes: nothing to restore")
	}
	err := s.store.Create(doc.Name, doc.Body)
	if errors.Is(err, os.ErrExist) {
		disk, modified, readErr := s.store.Read(doc.Name)
		if readErr != nil {
			return readErr
		}
		doc.Missing, doc.Disk, doc.Modified = false, disk, modified
		doc.Conflict, doc.Error = disk, "This note changed outside Notes"
		return nil
	}
	if err != nil {
		doc.Error = "Save failed: " + err.Error()
		return err
	}
	doc.Missing, doc.Disk, doc.Dirty, doc.Error, doc.Conflict, doc.Modified = false, doc.Body, false, "", "", s.now()
	return nil
}

// Discard lets a missing note go: its text is dropped and it closes.
func (s *Session) Discard(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.target(names)
	doc := s.docs[name]
	if doc == nil || !doc.Missing {
		return errors.New("notes: nothing to discard")
	}
	delete(s.docs, name)
	delete(s.blank, name)
	if s.selected == name {
		s.selected, s.titleDraft = "", ""
	}
	return nil
}

// KeepLocal resolves a conflict in favour of the local text, which the next
// save writes over the file.
func (s *Session) KeepLocal(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[s.target(names)]
	if doc == nil {
		return errors.New("notes: no note is open")
	}
	disk, modified, err := s.store.Read(doc.Name)
	if err != nil {
		doc.Error = "Save failed: " + err.Error()
		return err
	}
	doc.Disk, doc.Modified, doc.Conflict = disk, modified, ""
	doc.Dirty, doc.Edited, doc.Error = true, s.now(), ""
	return nil
}

func (s *Session) target(names []string) string {
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return s.selected
}

// SetTitleDraft records what the user typed in the title field; it becomes a
// rename on Enter or when the selection moves.
func (s *Session) SetTitleDraft(title string) {
	s.mu.Lock()
	s.titleDraft = title
	s.mu.Unlock()
}

func (s *Session) CommitTitle() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitTitleLocked()
}

func (s *Session) commitTitleLocked() error {
	draft := strings.TrimSpace(s.titleDraft)
	doc := s.docs[s.selected]
	if draft == "" || doc == nil || isScratch(doc.Name) {
		s.titleDraft = ""
		return nil
	}
	base := cleanTitle(strings.TrimSuffix(draft, "."+s.store.Ext))
	newName := base + "." + s.store.Ext
	if base == "" || newName == doc.Name {
		s.titleDraft = ""
		return nil
	}
	if err := s.flushDocLocked(doc); err != nil {
		return err
	}
	// A failed rename is reported once; keeping the draft would block every
	// later selection on the same collision.
	s.titleDraft = ""
	if err := s.store.Rename(doc.Name, newName); err != nil {
		doc.Error = "Rename failed: " + err.Error()
		return fmt.Errorf("notes: rename to %s: %w", newName, err)
	}
	old := doc.Name
	// Naming a note is a reason to keep it, empty or not.
	delete(s.blank, old)
	delete(s.docs, old)
	doc.Name = newName
	s.docs[newName] = doc
	s.selected = newName
	s.renamedFrom, s.renamedTo = old, newName
	return nil
}

// TakeRename reports, once, the last rename the session made, so the caller
// can move per-note state (sticky colour, pins) to the new name.
func (s *Session) TakeRename() (from, to string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from, to = s.renamedFrom, s.renamedTo
	s.renamedFrom, s.renamedTo = "", ""
	return from, to, from != ""
}

func (s *Session) SetFavorite(name string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(name); err != nil {
		return err
	}
	return s.store.Favorite(name, on)
}

func (s *Session) ProposeDelete(name string) { s.mu.Lock(); s.pendingDelete = name; s.mu.Unlock() }
func (s *Session) CancelPending()            { s.mu.Lock(); s.pendingDelete = ""; s.mu.Unlock() }

// ConfirmDeleteName deletes the pending note after saving everything else, and
// returns its name so the caller can close its stickies.
func (s *Session) ConfirmDeleteName() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.pendingDelete
	if name == "" {
		return "", errors.New("notes: no delete is pending")
	}
	s.saveAllLocked()
	if err := s.store.Delete(name); err != nil {
		return "", fmt.Errorf("notes: delete %s: %w", name, err)
	}
	delete(s.docs, name)
	if s.selected == name {
		s.selected, s.titleDraft = "", ""
	}
	s.pendingDelete = ""
	return name, nil
}

// Close is the panel closing, which is the title field's blur: a typed title
// becomes a rename, then every note is saved.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	err := errors.Join(s.commitTitleLocked(), s.flushAllLocked())
	s.discardBlankLocked("")
	return err
}

func (s *Session) SetStore(store *Store) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		if err := s.flushAllLocked(); err != nil {
			return err
		}
	}
	s.store, s.docs, s.selected, s.titleDraft = store, map[string]*Document{}, "", ""
	s.folderError = ""
	return nil
}

func (s *Session) Search(query string) { s.mu.Lock(); s.query = query; s.mu.Unlock() }
func (s *Session) ToggleSort()         { s.mu.Lock(); s.sortByName = !s.sortByName; s.mu.Unlock() }

// Notify shows a transient message above the library; ClearNotice drops it
// when the next action starts, DismissNotice when the user closes it.
func (s *Session) Notify(message string) { s.mu.Lock(); s.notice = message; s.mu.Unlock() }
func (s *Session) ClearNotice()          { s.mu.Lock(); s.notice = ""; s.mu.Unlock() }
func (s *Session) DismissNotice()        { s.ClearNotice() }

// ReportFolderError records why the notes folder cannot be used. Unlike a
// notice it lasts until a folder opens.
func (s *Session) ReportFolderError(message string) {
	s.mu.Lock()
	s.folderError = message
	s.mu.Unlock()
}

func (s *Session) ReportNoteError(name, message string) {
	s.mu.Lock()
	if doc := s.docs[name]; doc != nil {
		doc.Error = message
	}
	s.mu.Unlock()
}

func (s *Session) FlushNote(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	return s.flushDocLocked(s.docs[name])
}

func (s *Session) Selected() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selected
}

func (s *Session) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[s.selected]
	return doc != nil && doc.Dirty
}

func (s *Session) Buffer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if doc := s.docs[s.selected]; doc != nil {
		return doc.Body
	}
	return ""
}
