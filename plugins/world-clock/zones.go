package worldclock

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// DefaultZones mirrors the Noctalia world clock's starting list. It is seeded
// only when nothing has ever been stored; a stored empty list stays empty.
var DefaultZones = []string{"UTC", "America/New_York", "Europe/Berlin", "Asia/Tokyo"}

var (
	ErrDuplicate   = errors.New("zone already added")
	ErrInvalidZone = errors.New("unknown zone")
)

// MaxLabelRunes bounds a custom label so a card and the bar can always fit it
// beside the time.
const MaxLabelRunes = 18

// Zone is one stored clock. An empty Label displays the zone's short name.
type Zone struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	OnBar bool   `json:"on_bar"`
}

// ValidZone reports whether id names an IANA zone. "Local" and "" are
// refused: LoadLocation accepts both, but neither is a place.
func ValidZone(id string) bool {
	if id == "" || id == "Local" {
		return false
	}
	_, err := time.LoadLocation(id)
	return err == nil
}

func clampLabel(label string) string {
	label = strings.TrimSpace(label)
	if r := []rune(label); len(r) > MaxLabelRunes {
		label = string(r[:MaxLabelRunes])
	}
	return label
}

// Decode reads the stored zone list. It accepts the v1 shape (plain IANA
// strings) and the v2 object shape, drops invalid and duplicate entries, and
// fails only when the value is not a list at all.
func Decode(raw []byte) ([]Zone, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := []Zone{}
	seen := map[string]bool{}
	for _, item := range items {
		var z Zone
		var id string
		if json.Unmarshal(item, &id) == nil {
			z = Zone{ID: id, OnBar: true}
		} else {
			var o struct {
				ID    string `json:"id"`
				Label string `json:"label"`
				OnBar *bool  `json:"on_bar"`
			}
			if json.Unmarshal(item, &o) != nil {
				continue
			}
			z = Zone{ID: o.ID, Label: clampLabel(o.Label), OnBar: o.OnBar == nil || *o.OnBar}
		}
		if !ValidZone(z.ID) || seen[z.ID] {
			continue
		}
		seen[z.ID] = true
		out = append(out, z)
	}
	return out, nil
}

func Encode(zones []Zone) ([]byte, error) {
	if zones == nil {
		zones = []Zone{}
	}
	return json.Marshal(zones)
}

// Store is the ordered zone list plus the panel's one in-flight row edit:
// renaming a zone or confirming its deletion, never both.
type Store struct {
	mu            sync.Mutex
	zones         []Zone
	pendingDelete string
	renaming      string
}

func NewStore() *Store {
	s := &Store{}
	for _, id := range DefaultZones {
		s.zones = append(s.zones, Zone{ID: id, OnBar: true})
	}
	return s
}

func (s *Store) Load(zones []Zone) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zones = append([]Zone(nil), zones...)
	s.pendingDelete, s.renaming = "", ""
}

func (s *Store) Zones() []Zone {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Zone(nil), s.zones...)
}

func (s *Store) indexLocked(id string) int {
	for i, z := range s.zones {
		if z.ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.indexLocked(id) >= 0
}

// Label returns the stored custom label, "" when none is set.
func (s *Store) Label(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.indexLocked(id); i >= 0 {
		return s.zones[i].Label
	}
	return ""
}

func (s *Store) Add(id, label string) error {
	if !ValidZone(id) {
		return ErrInvalidZone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexLocked(id) >= 0 {
		return ErrDuplicate
	}
	s.zones = append(s.zones, Zone{ID: id, Label: clampLabel(label), OnBar: true})
	s.pendingDelete, s.renaming = "", ""
	return nil
}

func (s *Store) Rename(id, label string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return false
	}
	s.zones[i].Label = clampLabel(label)
	s.renaming = ""
	return true
}

func (s *Store) ToggleBar(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return false
	}
	s.zones[i].OnBar = !s.zones[i].OnBar
	return true
}

// Reorder moves id in front of the zone currently at insertBefore
// (len(zones) means the end). It reports whether the order changed.
func (s *Store) Reorder(id string, insertBefore int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.indexLocked(id)
	n := len(s.zones)
	if from < 0 || insertBefore < 0 || insertBefore > n || insertBefore == from || insertBefore == from+1 {
		return false
	}
	item := s.zones[from]
	rest := append(append([]Zone(nil), s.zones[:from]...), s.zones[from+1:]...)
	if insertBefore > from {
		insertBefore--
	}
	next := append(append(append([]Zone(nil), rest[:insertBefore]...), item), rest[insertBefore:]...)
	s.zones = next
	return true
}

func (s *Store) ProposeDelete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexLocked(id) >= 0 {
		s.pendingDelete, s.renaming = id, ""
	}
}

func (s *Store) ConfirmDelete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(s.pendingDelete)
	s.pendingDelete = ""
	if i < 0 {
		return false
	}
	s.zones = append(s.zones[:i:i], s.zones[i+1:]...)
	return true
}

func (s *Store) StartRename(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexLocked(id) >= 0 {
		s.renaming, s.pendingDelete = id, ""
	}
}

func (s *Store) CancelEdit() {
	s.mu.Lock()
	s.pendingDelete, s.renaming = "", ""
	s.mu.Unlock()
}

func (s *Store) PendingDelete() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingDelete
}

func (s *Store) Renaming() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renaming
}
