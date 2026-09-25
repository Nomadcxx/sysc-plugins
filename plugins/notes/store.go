package notes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const maxNoteBytes = 4 << 20

var errNoteChanged = errors.New("notes: note changed outside Notes")

// Store only reads and writes direct children of one configured notes folder.
type Store struct {
	mu        sync.Mutex
	Dir, Ext  string
	favorites map[string]bool
	now       func() time.Time
}

type Summary struct {
	Name     string
	Title    string
	Preview  string
	Modified time.Time
	Favorite bool
}

func Open(dir, ext string) (*Store, error) {
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, dir[2:])
	}
	if ext == "" {
		ext = "md"
	}
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	if ext == "" || strings.ContainsAny(ext, `/\\`) || strings.Contains(ext, "..") {
		return nil, errors.New("notes: extension must be a simple file extension")
	}
	abs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("notes: create folder: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("notes: inspect folder: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("notes: configured folder must be a real directory")
	}
	s := &Store{Dir: abs, Ext: ext, favorites: map[string]bool{}, now: time.Now}
	if err := s.loadFavorites(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Extension() string   { return s.Ext }
func (s *Store) ScratchName() string { return "scratchpad." + s.Ext }

func (s *Store) validName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\\`) {
		return errors.New("notes: invalid note name")
	}
	if !strings.HasSuffix(strings.ToLower(name), "."+s.Ext) {
		return fmt.Errorf("notes: note must end in .%s", s.Ext)
	}
	return nil
}

func (s *Store) path(name string) (string, error) {
	if err := s.validName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, name), nil
}

func (s *Store) Read(name string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(name)
	if err != nil {
		return "", time.Time{}, err
	}
	body, modified, _, err := readNoteFile(name, path)
	return body, modified, err
}

func readNoteFile(name, path string) (string, time.Time, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", time.Time{}, 0, errors.New("notes: note is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxNoteBytes+1))
	closeErr := f.Close()
	if err != nil {
		return "", time.Time{}, 0, err
	}
	if closeErr != nil {
		return "", time.Time{}, 0, closeErr
	}
	if len(b) > maxNoteBytes {
		return "", time.Time{}, 0, fmt.Errorf("notes: %s exceeds %d bytes", name, maxNoteBytes)
	}
	return string(b), info.ModTime(), info.Mode().Perm(), nil
}

func (s *Store) Save(name, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(name)
	if err != nil {
		return err
	}
	if len(body) > maxNoteBytes {
		return fmt.Errorf("notes: note exceeds %d bytes", maxNoteBytes)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("notes: refusing to replace a non-regular note")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicWrite(path, []byte(body), mode)
}

// SaveIfUnchanged checks the expected disk content again after preparing the
// temporary file, so an external Obsidian save during a large write is kept.
func (s *Store) SaveIfUnchanged(name, expected, body string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(name)
	if err != nil {
		return "", err
	}
	if len(body) > maxNoteBytes {
		return "", fmt.Errorf("notes: note exceeds %d bytes", maxNoteBytes)
	}
	disk, _, mode, err := readNoteFile(name, path)
	if err != nil {
		return "", err
	}
	if disk != expected {
		return disk, errNoteChanged
	}
	var conflict string
	err = atomicWriteChecked(path, []byte(body), mode, func() error {
		latest, _, _, readErr := readNoteFile(name, path)
		if readErr != nil {
			return readErr
		}
		if latest != expected {
			conflict = latest
			return errNoteChanged
		}
		return nil
	})
	return conflict, err
}

func (s *Store) Create(name, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(name)
	if err != nil {
		return err
	}
	if len(body) > maxNoteBytes {
		return fmt.Errorf("notes: note exceeds %d bytes", maxNoteBytes)
	}
	return atomicCreate(path, []byte(body), 0o600)
}

func (s *Store) List(query string, byName bool) ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("notes: scan folder: %w", err)
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	out := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), "."+s.Ext) {
			continue
		}
		path := filepath.Join(s.Dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("notes: inspect %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("notes: refusing symlink %s", name)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("notes: read %s: %w", name, err)
		}
		b, readErr := io.ReadAll(io.LimitReader(f, maxNoteBytes+1))
		closeErr := f.Close()
		if readErr != nil {
			return nil, fmt.Errorf("notes: read %s: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("notes: close %s: %w", name, closeErr)
		}
		if len(b) > maxNoteBytes {
			return nil, fmt.Errorf("notes: %s exceeds %d bytes", name, maxNoteBytes)
		}
		body := string(b)
		title := strings.TrimSuffix(name, filepath.Ext(name))
		preview := excerpt(body, 150)
		if needle != "" && !strings.Contains(strings.ToLower(title+"\n"+body), needle) {
			continue
		}
		out = append(out, Summary{Name: name, Title: title, Preview: preview, Modified: info.ModTime(), Favorite: s.favorites[name]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Favorite != out[j].Favorite {
			return out[i].Favorite
		}
		if byName {
			return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
		}
		return out[i].Modified.After(out[j].Modified)
	})
	return out, nil
}

func (s *Store) Rename(oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldPath, err := s.path(oldName)
	if err != nil {
		return err
	}
	newPath, err := s.path(newName)
	if err != nil {
		return err
	}
	if oldName == newName {
		return nil
	}
	info, err := os.Lstat(oldPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("notes: refusing to rename a non-regular note")
	}
	if _, err := os.Lstat(newPath); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := renameNoReplace(oldPath, newPath); err != nil {
		return fmt.Errorf("notes: rename: %w", err)
	}
	if s.favorites[oldName] {
		delete(s.favorites, oldName)
		s.favorites[newName] = true
		if err := s.saveFavoritesLocked(); err != nil {
			delete(s.favorites, newName)
			s.favorites[oldName] = true
			if rollbackErr := renameNoReplace(newPath, oldPath); rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("notes: restore original filename: %w", rollbackErr))
			}
			return err
		}
	}
	return nil
}

func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(name)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("notes: refusing to delete a non-regular note")
	}
	wasFavorite := s.favorites[name]
	if wasFavorite {
		delete(s.favorites, name)
		if err := s.saveFavoritesLocked(); err != nil {
			s.favorites[name] = true
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		deleteErr := fmt.Errorf("notes: delete: %w", err)
		if wasFavorite {
			s.favorites[name] = true
			if rollbackErr := s.saveFavoritesLocked(); rollbackErr != nil {
				return errors.Join(deleteErr, fmt.Errorf("notes: restore favorite: %w", rollbackErr))
			}
		}
		return deleteErr
	}
	return nil
}

func (s *Store) Favorite(name string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.path(name); err != nil {
		return err
	}
	wasFavorite := s.favorites[name]
	if on {
		s.favorites[name] = true
	} else {
		delete(s.favorites, name)
	}
	if err := s.saveFavoritesLocked(); err != nil {
		if wasFavorite {
			s.favorites[name] = true
		} else {
			delete(s.favorites, name)
		}
		return err
	}
	return nil
}

func (s *Store) IsFavorite(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.favorites[name]
}

func (s *Store) loadFavorites() error {
	path := filepath.Join(s.Dir, ".pinned.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("notes: read favorites: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("notes: favorites file is not regular")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("notes: read favorites: %w", err)
	}
	var names []string
	if json.Unmarshal(b, &names) == nil {
		for _, name := range names {
			if s.validName(name) == nil {
				s.favorites[name] = true
			}
		}
		return nil
	}
	var values map[string]bool
	if err := json.Unmarshal(b, &values); err != nil {
		return fmt.Errorf("notes: parse favorites: %w", err)
	}
	for name, on := range values {
		if on && s.validName(name) == nil {
			s.favorites[name] = true
		}
	}
	return nil
}

func (s *Store) saveFavoritesLocked() error {
	names := make([]string, 0, len(s.favorites))
	for name, on := range s.favorites {
		if on {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	b, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.Dir, ".pinned.json"), b, 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	return atomicWriteChecked(path, data, mode, nil)
}

func atomicWriteChecked(path string, data []byte, mode os.FileMode, beforeCommit func() error) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".notes-tmp-")
	if err != nil {
		return fmt.Errorf("notes: create temporary file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		_ = f.Close()
		return fmt.Errorf("notes: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("notes: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("notes: close: %w", err)
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("notes: commit: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func renameNoReplace(oldPath, newPath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
}

func atomicCreate(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".notes-tmp-")
	if err != nil {
		return fmt.Errorf("notes: create temporary file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		_ = f.Close()
		return fmt.Errorf("notes: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("notes: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("notes: close: %w", err)
	}
	if err := os.Link(tmp, path); err != nil {
		return fmt.Errorf("notes: create %s: %w", filepath.Base(path), err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func excerpt(body string, limit int) string {
	body = strings.TrimSpace(strings.Join(strings.Fields(body), " "))
	if len(body) <= limit {
		return body
	}
	for limit > 0 && !utf8Boundary(body, limit) {
		limit--
	}
	return strings.TrimSpace(body[:limit]) + "…"
}

func utf8Boundary(s string, i int) bool { return i >= len(s) || i <= 0 || s[i]&0xc0 != 0x80 }
