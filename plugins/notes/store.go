package notes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	scratchpadBase = "scratchpad"
	pinsFile       = ".pinned.json"
)

// Change is how an on-disk note compares to the buffer the host holds.
type Change int

const (
	Unchanged Change = iota
	CleanReload
	DirtyConflict
	Deleted
)

func (c Change) String() string {
	switch c {
	case Unchanged:
		return "unchanged"
	case CleanReload:
		return "clean"
	case DirtyConflict:
		return "dirty"
	case Deleted:
		return "deleted"
	default:
		return fmt.Sprintf("change(%d)", c)
	}
}

// Store owns one notes directory. Names are always a single base file with
// the configured extension; nothing here follows a symlink or `..`.
type Store struct {
	Dir string
	Ext string
	now func() time.Time
}

// Note is one list row. Scratchpad is not a list row.
type Note struct {
	Name   string
	Pinned bool
}

func Open(dir, ext string) (*Store, error) {
	ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
	if ext == "" {
		ext = "md"
	}
	if strings.ContainsAny(ext, `./\`) {
		return nil, fmt.Errorf("extension %q", ext)
	}
	dir = expandHome(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &Store{Dir: abs, Ext: ext, now: time.Now}, nil
}

func expandHome(dir string) string {
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, dir[2:])
		}
	}
	return dir
}

func (s *Store) Scratchpad() (string, error) {
	name := scratchpadBase + "." + s.Ext
	path, err := s.namePath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if err := s.Save(name, ""); err != nil {
			return "", err
		}
	}
	return name, nil
}

func (s *Store) List(sortByDate bool) ([]Note, error) {
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	pins, err := s.loadPins()
	if err != nil {
		return nil, err
	}
	scratch := scratchpadBase + "." + s.Ext
	type row struct {
		Note
		mod time.Time
	}
	var rows []row
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == scratch {
			continue
		}
		if !strings.HasSuffix(name, "."+s.Ext) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rows = append(rows, row{
			Note: Note{Name: name, Pinned: pins[name]},
			mod:  info.ModTime(),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if sortByDate && !a.mod.Equal(b.mod) {
			return a.mod.After(b.mod)
		}
		return a.Name < b.Name
	})
	out := make([]Note, len(rows))
	for i, r := range rows {
		out[i] = r.Note
	}
	return out, nil
}

func (s *Store) Read(name string) (string, time.Time, error) {
	path, err := s.namePath(name)
	if err != nil {
		return "", time.Time{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", time.Time{}, err
	}
	return string(raw), info.ModTime(), nil
}

func (s *Store) Save(name, content string) error {
	path, err := s.namePath(name)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Store) Create() (string, error) {
	base := s.now().UTC().Format("2006-01-02 15.04.05")
	name := base + "." + s.Ext
	for n := 2; ; n++ {
		path, err := s.namePath(name)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			if err := s.Save(name, ""); err != nil {
				return "", err
			}
			return name, nil
		} else if err != nil {
			return "", err
		}
		name = fmt.Sprintf("%s-%d.%s", base, n, s.Ext)
	}
}

func (s *Store) Rename(old, next string) error {
	from, err := s.namePath(old)
	if err != nil {
		return err
	}
	to, err := s.namePath(next)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("rename onto %s", next)
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	pins, err := s.loadPins()
	if err != nil {
		return err
	}
	if pins[old] {
		delete(pins, old)
		pins[next] = true
		return s.savePins(pins)
	}
	return nil
}

func (s *Store) Delete(name string) error {
	path, err := s.namePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	pins, err := s.loadPins()
	if err != nil {
		return err
	}
	if _, ok := pins[name]; ok {
		delete(pins, name)
		return s.savePins(pins)
	}
	return nil
}

func (s *Store) SetPinned(name string, on bool) error {
	if _, err := s.namePath(name); err != nil {
		return err
	}
	pins, err := s.loadPins()
	if err != nil {
		return err
	}
	if on {
		pins[name] = true
	} else {
		delete(pins, name)
	}
	return s.savePins(pins)
}

func (s *Store) Probe(name, loaded string, dirty bool) (Change, string, error) {
	body, _, err := s.Read(name)
	if os.IsNotExist(err) {
		return Deleted, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	if body == loaded {
		return Unchanged, "", nil
	}
	if dirty {
		return DirtyConflict, body, nil
	}
	return CleanReload, body, nil
}

func (s *Store) namePath(name string) (string, error) {
	if name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
		return "", fmt.Errorf("path %q", name)
	}
	if !strings.HasSuffix(name, "."+s.Ext) {
		return "", fmt.Errorf("extension %q", name)
	}
	full := filepath.Join(s.Dir, name)
	root := s.Dir + string(os.PathSeparator)
	if !strings.HasPrefix(full, root) {
		return "", fmt.Errorf("path %q", name)
	}
	if info, err := os.Lstat(full); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlink %q", name)
	}
	return full, nil
}

func (s *Store) loadPins() (map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Join(s.Dir, pinsFile))
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var pins map[string]bool
	if err := json.Unmarshal(raw, &pins); err != nil || pins == nil {
		return map[string]bool{}, nil
	}
	return pins, nil
}

func (s *Store) savePins(pins map[string]bool) error {
	raw, err := json.Marshal(pins)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".pinned-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, filepath.Join(s.Dir, pinsFile))
}
