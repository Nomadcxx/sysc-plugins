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

type Document struct {
	Name     string
	Body     string
	Disk     string
	Dirty    bool
	Error    string
	Conflict string
	Modified time.Time
	Edited   time.Time
}

type Snapshot struct {
	Notes         []Summary
	Current       string
	Title         string
	Body          string
	Dirty         bool
	SaveError     string
	SaveErr       string
	Conflict      bool
	ConflictBody  string
	LibraryError  string
	PendingDelete string
	Query         string
	CaptureText   string
	SortByName    bool
	Editing       bool
	Pinned        bool // kept as a compatibility view of the current Favorite state
	Words         int
	Chars         int
}

type Session struct {
	mu            sync.Mutex
	store         *Store
	docs          map[string]*Document
	current       string
	query         string
	captureText   string
	sortByName    bool
	editing       bool
	pendingDelete string
	libraryError  string
	now           func() time.Time
}

func NewSession(store *Store, now func() time.Time) *Session {
	if now == nil {
		now = time.Now
	}
	return &Session{store: store, docs: map[string]*Document{}, now: now}
}

func (s *Session) Snap() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapLocked()
}

func (s *Session) snapLocked() Snapshot {
	var snap Snapshot
	snap.LibraryError = s.libraryError
	if s.store == nil {
		if snap.LibraryError == "" {
			snap.LibraryError = "Notes folder is unavailable"
		}
		return snap
	}
	for name, doc := range s.docs {
		if !doc.Dirty {
			if body, modified, err := s.store.Read(name); err == nil && body != doc.Disk {
				doc.Body, doc.Disk, doc.Modified, doc.Error, doc.Conflict = body, body, modified, "", ""
			}
		}
	}
	snap.Current, snap.Query, snap.CaptureText, snap.SortByName, snap.Editing = s.current, s.query, s.captureText, s.sortByName, s.editing
	snap.PendingDelete = s.pendingDelete
	if s.current != "" {
		if doc := s.docs[s.current]; doc != nil {
			snap.Title = strings.TrimSuffix(doc.Name, filepath.Ext(doc.Name))
			snap.Body, snap.Dirty, snap.SaveError, snap.SaveErr = doc.Body, doc.Dirty, doc.Error, doc.Error
			snap.ConflictBody, snap.Conflict = doc.Conflict, doc.Conflict != ""
			snap.Words, snap.Chars = len(strings.Fields(doc.Body)), len([]rune(doc.Body))
		}
		snap.Pinned = s.store.IsFavorite(s.current)
	}
	if !s.editing {
		items, err := s.store.List(s.query, s.sortByName)
		if err != nil {
			snap.LibraryError = err.Error()
		} else {
			snap.Notes = items
		}
	}
	return snap
}

// Document returns the shared editing state used by the manager and a sticky.
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

func (s *Session) Open(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil && name == s.store.ScratchName() {
		if _, _, err := s.store.Read(name); errors.Is(err, os.ErrNotExist) {
			if err := s.store.Create(name, ""); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	if err := s.flushAllLocked(); err != nil {
		return err
	}
	if err := s.ensureLocked(name); err != nil {
		return err
	}
	s.current, s.editing, s.pendingDelete = name, true, ""
	return nil
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
	if err := s.flushAllLocked(); err != nil {
		return err
	}
	if err := s.ensureLocked(name); err != nil {
		return err
	}
	s.current, s.editing, s.pendingDelete = name, true, ""
	return nil
}

func (s *Session) Create() error { return s.Capture("") }

func (s *Session) Capture(body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return errors.New("notes: folder is unavailable")
	}
	if err := s.flushAllLocked(); err != nil {
		return err
	}
	name, err := s.createNameLocked("note")
	if err != nil {
		return err
	}
	if err := s.store.Create(name, body); err != nil {
		return err
	}
	s.docs[name] = &Document{Name: name, Body: body, Disk: body, Modified: s.now()}
	s.current, s.editing, s.pendingDelete = name, true, ""
	s.captureText = ""
	return nil
}

func (s *Session) createNameLocked(prefix string) (string, error) {
	stamp := s.now().Format("2006-01-02-150405")
	base := prefix + "-" + stamp
	for n := 1; n < 10000; n++ {
		name := base + "." + s.store.Ext
		if n > 1 {
			name = fmt.Sprintf("%s-%02d.%s", base, n, s.store.Ext)
		}
		if _, _, err := s.store.Read(name); errors.Is(err, os.ErrNotExist) {
			return name, nil
		} else if err == nil {
			continue
		} else {
			return "", err
		}
	}
	return "", errors.New("notes: could not choose a unique capture name")
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

func (s *Session) Type(body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == "" {
		return errors.New("notes: no note is open")
	}
	return s.typeLocked(s.current, body)
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
	conflict, err := s.store.SaveIfUnchanged(doc.Name, doc.Disk, doc.Body)
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
	for _, doc := range s.docs {
		body, modified, readErr := s.store.Read(doc.Name)
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

func (s *Session) Reload(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := ""
	if len(names) > 0 {
		name = names[0]
	}
	if name == "" {
		name = s.current
	}
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

func (s *Session) KeepLocal(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := ""
	if len(names) > 0 {
		name = names[0]
	}
	if name == "" {
		name = s.current
	}
	doc := s.docs[name]
	if doc == nil {
		return errors.New("notes: no note is open")
	}
	disk, modified, err := s.store.Read(name)
	if err != nil {
		doc.Error = "Save failed: " + err.Error()
		return err
	}
	doc.Disk, doc.Modified, doc.Conflict = disk, modified, ""
	doc.Dirty, doc.Edited, doc.Error = true, s.now(), ""
	return nil
}

func (s *Session) Rename(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[s.current]
	if doc == nil {
		return errors.New("notes: no note is open")
	}
	base := strings.TrimSpace(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
	if base == "" {
		return errors.New("notes: title cannot be empty")
	}
	newName := base + "." + s.store.Ext
	if doc.Name == newName {
		return nil
	}
	if err := s.flushDocLocked(doc); err != nil {
		return err
	}
	if err := s.store.Rename(doc.Name, newName); err != nil {
		doc.Error = "Rename failed: " + err.Error()
		return err
	}
	delete(s.docs, doc.Name)
	doc.Name = newName
	s.docs[newName] = doc
	s.current = newName
	return nil
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

func (s *Session) ConfirmDelete() error {
	_, err := s.ConfirmDeleteName()
	return err
}

func (s *Session) ConfirmDeleteName() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.pendingDelete
	if name == "" {
		return "", errors.New("notes: no delete is pending")
	}
	if err := s.flushAllLocked(); err != nil {
		return "", err
	}
	if err := s.store.Delete(name); err != nil {
		return "", fmt.Errorf("notes: delete %s: %w", name, err)
	}
	delete(s.docs, name)
	if s.current == name {
		s.current, s.editing = "", false
	}
	s.pendingDelete = ""
	return name, nil
}

func (s *Session) Back() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.flushAllLocked(); err != nil {
		return err
	}
	s.current, s.editing, s.pendingDelete = "", false, ""
	return nil
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushAllLocked()
}

func (s *Session) SetStore(store *Store) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.flushAllLocked(); err != nil {
		return err
	}
	s.store, s.docs, s.current, s.editing = store, map[string]*Document{}, "", false
	s.libraryError = ""
	return nil
}

func (s *Session) Search(query string)        { s.mu.Lock(); s.query = query; s.mu.Unlock() }
func (s *Session) SetCaptureText(body string) { s.mu.Lock(); s.captureText = body; s.mu.Unlock() }
func (s *Session) ToggleSort()                { s.mu.Lock(); s.sortByName = !s.sortByName; s.mu.Unlock() }

func (s *Session) ReportError(message string) { s.mu.Lock(); s.libraryError = message; s.mu.Unlock() }

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
	return s.flushDocLocked(s.docs[name])
}

func (s *Session) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func (s *Session) Page() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.editing {
		return "editor"
	}
	return "list"
}

func (s *Session) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current != "" && s.docs[s.current] != nil && s.docs[s.current].Dirty
}

func (s *Session) Buffer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if doc := s.docs[s.current]; doc != nil {
		return doc.Body
	}
	return ""
}

// Compatibility method: favorites are independent from sticky always-on-top.
func (s *Session) Pin(name string, on bool) error { return s.SetFavorite(name, on) }
