package thinkpad

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MaxKeyboardLevel bounds what a profile may ask for. Real machines top out at
// 2 or 3; the extra headroom keeps a hand-edited file from being rejected, and
// every write is still clamped to the hardware's own maximum.
const MaxKeyboardLevel = 8

// storeVersion is bumped when the on-disk shape changes incompatibly.
const storeVersion = 1

// errNoStorePath means there is nowhere to persist to; the plugin still works,
// it just cannot remember.
var errNoStorePath = errors.New("no writable store path")

// Settings is the set of states this plugin can put the hardware into. It
// doubles as both a live record of what the user asked for and the body of a
// named profile, so "save these settings as a profile" is a copy rather than a
// translation.
type Settings struct {
	// FanLevel is "auto", "disengaged", "0".."7", or empty to leave the fan
	// exactly as it is.
	FanLevel string `json:"fan_level"`
	// KeyboardOn and KeyboardLevel are separate so the backlight has a real
	// off switch that does not lose the level you had dialled in.
	KeyboardOn    bool `json:"keyboard_on"`
	KeyboardLevel int  `json:"keyboard_level"`
	LidOn         bool `json:"lid_on"`
}

// Equal reports whether two settings agree, so a redundant apply can be
// skipped and hardware left alone.
func (s Settings) Equal(other Settings) bool {
	return s.FanLevel == other.FanLevel &&
		s.KeyboardOn == other.KeyboardOn &&
		s.KeyboardLevel == other.KeyboardLevel &&
		s.LidOn == other.LidOn
}

// KeyboardValue is the brightness a profile actually asks the backlight for:
// zero when off, otherwise the dialled-in level.
// Summary is the one-line description shown beside a profile name.
func (s Settings) Summary() string {
	backlight := "backlight off"
	if s.KeyboardOn {
		backlight = fmt.Sprintf("backlight %d", s.KeyboardLevel)
	}
	lid := "lid off"
	if s.LidOn {
		lid = "lid on"
	}
	// The fan level label is capitalised for a heading ("Auto", "Full"); inside
	// this sentence it has to fold back down.
	return fmt.Sprintf("fan %s · %s · %s",
		strings.ToLower(FanLevelLabel(s.FanLevel)), backlight, lid)
}

func (s Settings) KeyboardValue() int {
	if !s.KeyboardOn {
		return 0
	}
	return clampLevel(s.KeyboardLevel, MaxKeyboardLevel)
}

// Profile is one named hardware state.
type Profile struct {
	Name     string   `json:"name"`
	Settings Settings `json:"settings"`
}

// storeFile is the on-disk shape.
type storeFile struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
	// LastUsed is the profile the panel applied most recently. A configured
	// active_profile setting overrides it at startup.
	LastUsed string `json:"last_used"`
}

// Store is the plugin's own profile database. The host owns settings and
// validates them, but it will not let a plugin write them, so anything that
// has to survive a reboot is recorded here and replayed on attach.
type Store struct {
	path string

	mu         sync.Mutex
	data       storeFile
	loadIssue  string
	dirtySeeds bool
}

// DefaultStorePath returns the per-user location profiles live in.
func DefaultStorePath() string {
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, "sysc-plugins", "thinkpad.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "sysc-plugins", "thinkpad.json")
}

// OpenStore loads the store at path, seeding sensible profiles when the file
// is missing. A file that exists but cannot be parsed is reported through
// LoadIssue and left completely untouched: the plugin keeps running on
// defaults and only rewrites the file when the user deliberately saves, so a
// hand-edited store is never silently destroyed.
func OpenStore(path string) *Store {
	s := &Store{path: path, data: storeFile{Version: storeVersion}}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var f storeFile
		if err := json.Unmarshal(raw, &f); err != nil {
			s.loadIssue = fmt.Sprintf("the profile file at %s is not valid JSON; it has been left alone", path)
		} else {
			s.adopt(f)
		}
	case os.IsNotExist(err):
		s.data = defaultStoreFile()
		s.dirtySeeds = true
	case err != nil:
		s.loadIssue = "the profile file could not be read: " + err.Error()
		s.data = defaultStoreFile()
	}
	return s
}

func (s *Store) adopt(f storeFile) {
	seen := map[string]bool{}
	profiles := make([]Profile, 0, len(f.Profiles))
	for _, p := range f.Profiles {
		p = sanitizeProfile(p)
		if p.Name == "" || seen[strings.ToLower(p.Name)] {
			continue
		}
		seen[strings.ToLower(p.Name)] = true
		profiles = append(profiles, p)
	}
	s.data = storeFile{Version: storeVersion, Profiles: profiles, LastUsed: strings.TrimSpace(f.LastUsed)}
}

// LoadIssue reports why the store fell back to defaults, or "" when the file
// was read cleanly.
func (s *Store) LoadIssue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadIssue
}

// Path reports where the store persists.
func (s *Store) Path() string { return s.path }

// Profiles returns a copy of the profile table.
func (s *Store) Profiles() []Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Profile, len(s.data.Profiles))
	copy(out, s.data.Profiles)
	return out
}

// Profile looks one up by exact name.
func (s *Store) Profile(name string) (Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.data.Profiles {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Profile{}, false
}

// LastUsed reports the profile the panel applied most recently.
func (s *Store) LastUsed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LastUsed
}

// Save inserts or replaces a profile by name and persists the change.
func (s *Store) Save(p Profile) error {
	p = sanitizeProfile(p)
	if err := validateName(p.Name); err != nil {
		return err
	}

	s.mu.Lock()
	replaced := false
	for i := range s.data.Profiles {
		if strings.EqualFold(s.data.Profiles[i].Name, p.Name) {
			s.data.Profiles[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		s.data.Profiles = append(s.data.Profiles, p)
	}
	s.dirtySeeds = false
	err := s.writeLocked()
	s.mu.Unlock()
	return err
}

// Delete removes a profile. The last one is never removed, so the store always
// has something to fall back on.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := make([]Profile, 0, len(s.data.Profiles))
	removed := false
	for _, p := range s.data.Profiles {
		if !removed && strings.EqualFold(p.Name, name) {
			removed = true
			continue
		}
		kept = append(kept, p)
	}
	if !removed {
		return fmt.Errorf("no profile named %q", name)
	}
	if len(kept) == 0 {
		return errors.New("the last profile cannot be deleted")
	}
	s.data.Profiles = kept
	if strings.EqualFold(s.data.LastUsed, name) {
		s.data.LastUsed = ""
	}
	s.dirtySeeds = false
	return s.writeLocked()
}

// Rename changes a profile's name, keeping its settings.
func (s *Store) Rename(from, to string) error {
	to = strings.TrimSpace(to)
	if err := validateName(to); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.data.Profiles {
		if strings.EqualFold(s.data.Profiles[i].Name, from) {
			idx = i
			continue
		}
		if strings.EqualFold(s.data.Profiles[i].Name, to) {
			return fmt.Errorf("a profile named %q already exists", to)
		}
	}
	if idx < 0 {
		return fmt.Errorf("no profile named %q", from)
	}
	s.data.Profiles[idx].Name = to
	if strings.EqualFold(s.data.LastUsed, from) {
		s.data.LastUsed = to
	}
	s.dirtySeeds = false
	return s.writeLocked()
}

// Remember records which profile is in effect so it can be restored even if
// host settings have not arrived yet.
func (s *Store) Remember(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.LastUsed == name {
		return nil
	}
	s.data.LastUsed = name
	s.dirtySeeds = false
	return s.writeLocked()
}

// writeLocked persists the store. The rename is only durable once the
// directory entry itself has been flushed, so that is not skipped.
func (s *Store) writeLocked() error {
	if s.path == "" {
		return errNoStorePath
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".thinkpad-tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// seedSeeded persists the starter profiles on first run, so the file exists
// for the user to find and edit.
func (s *Store) seedSeeded() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirtySeeds {
		return nil
	}
	err := s.writeLocked()
	s.dirtySeeds = false
	return err
}

// validateName keeps profile names printable and short enough for the panel's
// two-column layout.
func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("a profile needs a name")
	}
	if len([]rune(name)) > 40 {
		return errors.New("profile names are limited to 40 characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return errors.New("profile names cannot contain control characters")
		}
	}
	return nil
}

func sanitizeProfile(p Profile) Profile {
	p.Name = strings.TrimSpace(p.Name)
	p.Settings = sanitizeSettings(p.Settings)
	return p
}

// sanitizeSettings normalises anything read from disk, so a stale or
// hand-edited file cannot push a bad value at the hardware.
func sanitizeSettings(v Settings) Settings {
	v.FanLevel = strings.ToLower(strings.TrimSpace(v.FanLevel))
	if v.FanLevel != "" && !ValidFanLevel(v.FanLevel) {
		v.FanLevel = ""
	}
	v.KeyboardLevel = clampLevel(v.KeyboardLevel, MaxKeyboardLevel)
	return v
}

// defaultStoreFile is the starter set: a safe everyday profile, a dark-room
// one, and a loud-and-bright one.
func defaultStoreFile() storeFile {
	return storeFile{
		Version: storeVersion,
		Profiles: []Profile{
			{Name: "default", Settings: Settings{FanLevel: FanLevelAuto, KeyboardOn: true, KeyboardLevel: 1, LidOn: false}},
			{Name: "night", Settings: Settings{FanLevel: FanLevelAuto, KeyboardOn: false, KeyboardLevel: 0, LidOn: false}},
			{Name: "bright", Settings: Settings{FanLevel: FanLevelDisengaged, KeyboardOn: true, KeyboardLevel: 2, LidOn: true}},
		},
		LastUsed: "default",
	}
}
