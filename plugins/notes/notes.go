package notes

import (
	"strings"
	"time"
	"unicode/utf8"
)

const autosaveIdle = time.Second

// Session is the Notes process owner: list vs editor, dirty buffer, autosave,
// and external-change choice. The store owns files.
type Session struct {
	store *Store
	now   func() time.Time
	idle  time.Duration

	page          string
	current       string
	buffer        string
	loaded        string
	dirty         bool
	reseed        uint64
	pendingDelete string
	saveErr       string
	conflictBody  string
	lastEdit      time.Time
	sortByDate    bool
}

func NewSession(store *Store, now func() time.Time) *Session {
	if now == nil {
		now = time.Now
	}
	s := &Session{store: store, now: now, idle: autosaveIdle, page: "list"}
	_, _ = store.Scratchpad()
	return s
}

func (s *Session) Page() string    { return s.page }
func (s *Session) Current() string { return s.current }
func (s *Session) Buffer() string  { return s.buffer }
func (s *Session) Dirty() bool     { return s.dirty }

func (s *Session) Create() error {
	if err := s.Flush(); err != nil {
		return err
	}
	name, err := s.store.Create()
	if err != nil {
		return err
	}
	return s.Open(name)
}

func (s *Session) Open(name string) error {
	if err := s.Flush(); err != nil {
		return err
	}
	body, _, err := s.store.Read(name)
	if err != nil {
		return err
	}
	s.page = "editor"
	s.current = name
	s.buffer = body
	s.loaded = body
	s.dirty = false
	s.saveErr = ""
	s.conflictBody = ""
	s.pendingDelete = ""
	s.reseed++
	return nil
}

func (s *Session) Back() {
	_ = s.Flush()
	s.page = "list"
	s.current = ""
	s.buffer = ""
	s.loaded = ""
	s.dirty = false
	s.conflictBody = ""
	s.saveErr = ""
}

func (s *Session) Type(text string) {
	s.buffer = text
	s.dirty = true
	s.lastEdit = s.now()
	s.saveErr = ""
}

func (s *Session) Tick() {
	if s.page == "editor" && s.current != "" {
		s.probe()
	}
	if s.page == "editor" && s.dirty && s.conflictBody == "" && !s.lastEdit.IsZero() && s.now().Sub(s.lastEdit) >= s.idle {
		_ = s.Flush()
	}
}

func (s *Session) Flush() error {
	if s.current == "" || !s.dirty {
		return nil
	}
	if err := s.store.Save(s.current, s.buffer); err != nil {
		s.saveErr = err.Error()
		return err
	}
	s.loaded = s.buffer
	s.dirty = false
	s.saveErr = ""
	s.conflictBody = ""
	return nil
}

func (s *Session) Close() error { return s.Flush() }

func (s *Session) Pin(name string, on bool) error { return s.store.SetPinned(name, on) }

func (s *Session) Rename(next string) error {
	if err := s.Flush(); err != nil {
		return err
	}
	old := s.current
	if next == old {
		return nil
	}
	if err := s.store.Rename(old, next); err != nil {
		s.saveErr = err.Error()
		return err
	}
	s.current = next
	return nil
}

func (s *Session) ProposeDelete(name string) { s.pendingDelete = name }

func (s *Session) CancelPending() { s.pendingDelete = "" }

func (s *Session) ConfirmDelete() error {
	name := s.pendingDelete
	s.pendingDelete = ""
	if name == "" {
		return nil
	}
	if s.current == name {
		s.page = "list"
		s.current = ""
		s.buffer = ""
		s.loaded = ""
		s.dirty = false
		s.conflictBody = ""
		s.saveErr = ""
	}
	return s.store.Delete(name)
}

func (s *Session) OpenScratch() error {
	name, err := s.store.Scratchpad()
	if err != nil {
		return err
	}
	return s.Open(name)
}

func (s *Session) SetStore(st *Store) {
	_ = s.Flush()
	s.store = st
	s.page = "list"
	s.current = ""
	s.buffer = ""
	s.loaded = ""
	s.dirty = false
	s.conflictBody = ""
	s.saveErr = ""
	s.pendingDelete = ""
	_, _ = st.Scratchpad()
}

func (s *Session) KeepLocal() {
	if s.conflictBody == "" {
		return
	}
	s.loaded = s.conflictBody
	s.conflictBody = ""
}

func (s *Session) Reload() {
	if s.conflictBody == "" {
		return
	}
	s.buffer = s.conflictBody
	s.loaded = s.conflictBody
	s.dirty = false
	s.conflictBody = ""
	s.reseed++
}

func (s *Session) probe() {
	kind, body, err := s.store.Probe(s.current, s.loaded, s.dirty)
	if err != nil {
		s.saveErr = err.Error()
		return
	}
	switch kind {
	case CleanReload:
		s.buffer = body
		s.loaded = body
		s.dirty = false
		s.reseed++
	case DirtyConflict:
		s.conflictBody = body
	case Deleted:
		if s.dirty {
			s.saveErr = "missing"
			return
		}
		s.Back()
	}
}

func (s *Session) Snap() Snapshot {
	notes, _ := s.store.List(s.sortByDate)
	status := "saved"
	switch {
	case s.saveErr != "":
		status = s.saveErr
	case s.conflictBody != "":
		status = "conflict"
	case s.dirty:
		status = "dirty"
	}
	title := strings.TrimSuffix(s.current, "."+s.store.Ext)
	return Snapshot{
		Page: s.page, Notes: notes, Current: s.current, Buffer: s.buffer,
		Title: title, Reseed: s.reseed, Status: status,
		Words: len(strings.Fields(s.buffer)), Chars: utf8.RuneCountInString(s.buffer),
		PendingDelete: s.pendingDelete, Conflict: s.conflictBody != "", SaveErr: s.saveErr,
	}
}
