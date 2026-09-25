# World Clock Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the world-clock plugin so it exceeds the Noctalia and DMS references: city search, labelled zones with bar visibility, relative-to-local readings, four bar modes, minute-aligned ticking, and visible errors.

**Architecture:** One plugin process (it is the service). The package splits by responsibility: `zones.go` (model + persistence codec), `reading.go` (pure time formatting), `search.go` (tz-table index), `panel.go` and `bar.go` (view trees), and `cmd/.../main.go` (event loop). sysc-shell only gains two material glyphs; the wire protocol is unchanged.

**Tech Stack:** Go 1.26, `github.com/Nomadcxx/sysc-shell/plugin/v1` (JSONL wire), `plugin/lint` (host layout pipeline), system tzdata under `/usr/share/zoneinfo`, fontTools (glyph subset only).

**Spec:** `docs/plans/2026-09-25-world-clock-design.md`

## Global Constraints

- Protocol declared in `plugins/world-clock/manifest.json`: `{"major": 1, "minor": 7}` (host maximum is 7, `sysc-shell/internal/plugin/protocol.go:10`).
- Manifest version `2.0.0`; handshake fallback identity version `2.0.0`.
- State key stays `zones`; value `[{"id":…,"label":…,"on_bar":…}]`; legacy `["UTC", …]` must still load.
- Defaults, seeded only when the key is not found: `UTC`, `America/New_York`, `Europe/Berlin`, `Asia/Tokyo`. A stored empty list stays empty.
- Never write state except in response to a user mutation.
- Panel box 420 × 360 (`attached`); bar lint box 240 × 32; tooltip 280 × 200.
- Settings: `hour24` bool default true; `bar_mode` select `icon|primary|all|cycle` default `primary`; `cycle_seconds` int 3–120 default 15.
- Every icon name used must be in the shell catalogue at the pinned sysc-shell version; an unknown name gets the whole view refused (`internal/plugin/view.go` `iconNode`). New names: `public`, `edit`.
- No new Go module dependencies.
- Commit messages carry no AI attribution (a repo hook rejects it).
- Copy is verbatim from the spec: placeholder `Search a city or country`; empty state `No zones yet. Search for a city above.`; `No matching zone`; `Couldn't save zones`; `Limited search: tz tables not found`; `<City> is already in the list`; `No zone matches "<query>"`.

## Review Focus

1. **Long or multibyte labels** (e.g. an 18-rune label in CJK or with emoji): bar and card must still pass lint; the bar truncates the label, never the time. Test in Task 6 (`TestBarLongMultibyteLabelFitsAndKeepsTime`) and Task 5 (`TestPanelLintEveryState`, 8-zone state uses a long CJK label).
2. **Local zone equals a listed zone, or local is UTC**: reading shows `Same time`, day shift 0, no panic. Test in Task 4 (`TestReadSameZoneAsLocal`).
3. **Quarter-hour zones and DST days** (Kathmandu +5:45, Chatham +13:45, New York on the March transition): offsets and relatives exact. Test in Task 4 (`TestReadQuarterHourAndDST`).
4. **Settings with wrong types or out-of-range values** (string for `cycle_seconds`, unknown `bar_mode`, `cycle_seconds: 500`): defaults kept or value clamped, never a crash. Test in Task 7 (`TestSettingsApplyIgnoresBadValues`).
5. **The zone being renamed or confirmed for deletion disappears** (deleted, or state reloaded): edit state clears instead of pointing at a missing zone. Test in Task 2 (`TestEditStateClearsWhenZoneGoes`).

---

## File map

| File | Status | Responsibility |
|---|---|---|
| `~/sysc-shell/internal/render/icons/material/build.py` | modify | add `public`, `edit` to `ICONS` |
| `~/sysc-shell/internal/render/icons/material/material-symbols-rounded.ttf` | regenerate | subset font |
| `~/sysc-shell/internal/render/icons/material/SOURCE.md` | modify | inventory, size, hash |
| `~/sysc-shell/internal/render/materialfont.go` | modify | accept the two names |
| `~/sysc-shell/internal/render/materialfont_test.go` | modify | inventory list |
| `go.mod`, `go.sum` | modify | shell pin bump; repairs HEAD's missing `go.sum` lines |
| `plugins/world-clock/zones.go` (+`_test`) | create | `Zone`, `Store`, `Decode`, `Encode`, `ValidZone` |
| `plugins/world-clock/reading.go` (+`_test`) | create | `Reading`, `Read`, `ShortLabel`, formatting |
| `plugins/world-clock/search.go` (+`_test`) | create | `Index`, `Match`, `NewIndex`, `OpenSystemIndex`, aliases, fold |
| `plugins/world-clock/testdata/{zone.tab,iso3166.tab,tzdata.zi}` | create | search fixtures |
| `plugins/world-clock/panel.go` (+`_test`) | create | `PanelState`, `Panel`, `PanelPatch`, `SuggestionTitle` |
| `plugins/world-clock/bar.go` (+`_test`) | create | `BarMode`, `Bar`, `BarButton`, `BarText`, `Tooltip` |
| `plugins/world-clock/clock.go`, `clock_test.go`, `view.go`, `view_test.go` | delete (Task 7) | superseded |
| `cmd/sysc-plugin-world-clock/main.go` (+`main_test.go`) | rewrite / create | event loop |
| `plugins/world-clock/manifest.json` | modify | version, minor, settings |
| `README.md` | modify | plugin table row |
| `docs/plans/README.md` | modify | register row for this plan |

Until Task 7, the old `clock.go`/`view.go` keep compiling beside the new files; new code uses non-colliding names (`Store`, `Panel`, `Bar`, `Tooltip`, `PanelPatch`). Task 4 moves `Reading`, `ShortLabel`, `formatClock`, `formatOffset` out of `clock.go`.

Test command used throughout (from `~/sysc-plugins`): `go test ./plugins/world-clock/ ./cmd/sysc-plugin-world-clock/`.

---

### Task 1: Shell glyphs `public` and `edit` (sysc-shell repo)

**Files:**
- Modify: `~/sysc-shell/internal/render/icons/material/build.py` (end of `ICONS`, after `"link_off",`)
- Regenerate: `~/sysc-shell/internal/render/icons/material/material-symbols-rounded.ttf`
- Modify: `~/sysc-shell/internal/render/icons/material/SOURCE.md` (Result line, Inventory block, hash block, fontTools version)
- Modify: `~/sysc-shell/internal/render/materialfont.go` (`materialIcons` map)
- Test: `~/sysc-shell/internal/render/materialfont_test.go` (`materialInventory`)

**Interfaces:**
- Produces: `render.ValidMaterialIcon("public") == true`, `render.ValidMaterialIcon("edit") == true` at the sysc-shell commit Task 2 pins.

sysc-shell has unrelated uncommitted work (`.beads/issues.jsonl`, `docs/plans/*`, `.tmp-thumbcheck/`). Stage only the five files above.

- [ ] **Step 1: Write the failing test**

In `materialfont_test.go`, append to `materialInventory` after `"drag_indicator", "tune", "link_off",`:

```go
	"public", "edit",
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/sysc-shell && go test ./internal/render/ -run Material -v`
Expected: FAIL naming `public` and `edit` (not accepted / not covered by the font).

- [ ] **Step 3: Add the names to the builder and the map**

`build.py`, after `"link_off",`:

```python
    # The world clock: a globe for its icon-only bar mode, and a pencil for
    # renaming a zone in place.
    "public",
    "edit",
```

`materialfont.go`, after the `"drag_indicator": {}, "tune": {}, "link_off": {},` line:

```go
	// The world clock's bar glyph and rename control.
	"public": {}, "edit": {},
```

- [ ] **Step 4: Rebuild the subset**

```bash
S=/tmp/claude-1000/-home-nomadx-sysc-plugins/fb399c47-f171-48dc-b00f-6526c3f7330f/scratchpad
curl -fL -o "$S/MaterialSymbolsRounded.ttf" \
  'https://raw.githubusercontent.com/google/material-design-icons/84ccef280841abfac506afc4ad4a2782f6d0a1d0/variablefont/MaterialSymbolsRounded%5BFILL%2CGRAD%2Copsz%2Cwght%5D.ttf'
cd ~/sysc-shell && python3 internal/render/icons/material/build.py "$S/MaterialSymbolsRounded.ttf"
sha256sum internal/render/icons/material/material-symbols-rounded.ttf
stat -c %s internal/render/icons/material/material-symbols-rounded.ttf
```

The script verifies the upstream SHA-256 itself and prints the glyph count. Local fontTools is 4.66.0 (the committed font was built with 4.65.0), so the hash will differ from the recorded one; that is expected.

- [ ] **Step 5: Update SOURCE.md**

- Result line: the new byte size, glyph count, and name count (previous: 26,204 bytes, 114 glyphs, 83 names; the build output gives the new glyph count, names become 85).
- Inventory block: append a line `public edit`.
- Hash block: replace with the `sha256sum` output from Step 4.
- `Built with fontTools 4.65.0.` → `Built with fontTools 4.66.0.`

- [ ] **Step 6: Run tests**

Run: `cd ~/sysc-shell && go test ./internal/render/ ./internal/plugin/`
Expected: PASS.

- [ ] **Step 7: Commit (do not push yet)**

```bash
cd ~/sysc-shell
git add internal/render/icons/material/build.py internal/render/icons/material/material-symbols-rounded.ttf \
  internal/render/icons/material/SOURCE.md internal/render/materialfont.go internal/render/materialfont_test.go
git commit -m "feat(render): cut the world clock's globe and pencil glyphs"
```

- [ ] **Step 8: Push — ask the user first**

Pushing sysc-shell `main` is outward-facing. Ask for confirmation, then `git push origin main`. Task 2 needs the commit on `origin`.

---

### Task 2: Pin the shell, repair go.sum, and add the zone model

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `plugins/world-clock/zones.go`
- Test: `plugins/world-clock/zones_test.go`

**Interfaces:**
- Consumes: Task 1's pushed commit.
- Produces:
  ```go
  var DefaultZones = []string{"UTC", "America/New_York", "Europe/Berlin", "Asia/Tokyo"}
  var ErrDuplicate, ErrInvalidZone error
  const MaxLabelRunes = 18
  type Zone struct { ID string `json:"id"`; Label string `json:"label"`; OnBar bool `json:"on_bar"` }
  func ValidZone(id string) bool
  func Decode(raw []byte) ([]Zone, error)
  func Encode(zones []Zone) ([]byte, error)
  type Store struct{ /* unexported */ }
  func NewStore() *Store
  func (s *Store) Load(zones []Zone)
  func (s *Store) Zones() []Zone
  func (s *Store) Has(id string) bool
  func (s *Store) Label(id string) string
  func (s *Store) Add(id, label string) error
  func (s *Store) Rename(id, label string) bool
  func (s *Store) ToggleBar(id string) bool
  func (s *Store) Reorder(id string, insertBefore int) bool
  func (s *Store) ProposeDelete(id string)
  func (s *Store) ConfirmDelete() bool
  func (s *Store) StartRename(id string)
  func (s *Store) CancelEdit()
  func (s *Store) PendingDelete() string
  func (s *Store) Renaming() string
  ```

- [ ] **Step 1: Bump the pin and tidy**

```bash
cd ~/sysc-plugins
go get github.com/Nomadcxx/sysc-shell@main
go mod tidy
go build ./... && go test ./...
```

Expected: builds and passes (this is the first green build since `228113c`). If the in-progress calendar work fails to build against the new pin, stop and report; do not edit calendar files.

- [ ] **Step 2: Commit the pin on its own**

```bash
git add go.mod go.sum
git commit -m "build: pin sysc-shell with world clock glyphs and restore go.sum"
```

- [ ] **Step 3: Write the failing tests** — `plugins/world-clock/zones_test.go`

```go
package worldclock

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func ids(zones []Zone) []string {
	out := make([]string, len(zones))
	for i, z := range zones {
		out[i] = z.ID
	}
	return out
}

func TestDecodeLegacyStrings(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(`["Europe/Paris","bad","Europe/Paris","Asia/Tokyo"]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Zone{{ID: "Europe/Paris", OnBar: true}, {ID: "Asia/Tokyo", OnBar: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zones = %+v", got)
	}
}

func TestDecodeObjectsDefaultsOnBarAndClampsLabel(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(`[{"id":"Asia/Tokyo","label":"  Office  "},{"id":"UTC","on_bar":false},{"id":"Local"},{"id":""},{"nope":1},{"id":"Europe/Berlin","label":"` + strings.Repeat("x", 30) + `"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Zone{
		{ID: "Asia/Tokyo", Label: "Office", OnBar: true},
		{ID: "UTC", OnBar: false},
		{ID: "Europe/Berlin", Label: strings.Repeat("x", MaxLabelRunes), OnBar: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zones = %+v", got)
	}
}

func TestDecodeEmptyListStaysEmpty(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(`[]`))
	if err != nil || len(got) != 0 {
		t.Fatalf("zones = %+v err = %v", got, err)
	}
}

func TestDecodeRejectsNonList(t *testing.T) {
	t.Parallel()
	if _, err := Decode([]byte(`{"not":"a list"}`)); err == nil {
		t.Fatal("object accepted as a zone list")
	}
}

func TestEncodeRoundTrips(t *testing.T) {
	t.Parallel()
	in := []Zone{{ID: "Asia/Tokyo", Label: "Office", OnBar: false}}
	raw, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(raw)
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip = %+v err = %v", out, err)
	}
}

func TestNewStoreSeedsDefaults(t *testing.T) {
	t.Parallel()
	if got := ids(NewStore().Zones()); !reflect.DeepEqual(got, DefaultZones) {
		t.Fatalf("defaults = %v", got)
	}
}

func TestAddRejectsDuplicateAndInvalid(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if err := s.Add("UTC", ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate err = %v", err)
	}
	if err := s.Add("Not/AZone", ""); !errors.Is(err, ErrInvalidZone) {
		t.Fatalf("invalid err = %v", err)
	}
	if err := s.Add("Australia/Sydney", "Home"); err != nil {
		t.Fatal(err)
	}
	last := s.Zones()[len(s.Zones())-1]
	if last != (Zone{ID: "Australia/Sydney", Label: "Home", OnBar: true}) {
		t.Fatalf("added = %+v", last)
	}
}

func TestRenameAndToggleBar(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.StartRename("UTC")
	if !s.Rename("UTC", "  Server  ") || s.Label("UTC") != "Server" || s.Renaming() != "" {
		t.Fatalf("rename: label=%q renaming=%q", s.Label("UTC"), s.Renaming())
	}
	if !s.Rename("UTC", "") || s.Label("UTC") != "" {
		t.Fatal("empty rename did not reset the label")
	}
	if !s.ToggleBar("UTC") || s.Zones()[0].OnBar {
		t.Fatal("toggle did not hide UTC")
	}
	if s.Rename("Nope/Zone", "x") || s.ToggleBar("Nope/Zone") {
		t.Fatal("mutated a missing zone")
	}
}

func TestReorderAtEveryInsertionIndex(t *testing.T) {
	t.Parallel()
	want := map[int][]string{
		0: {"Asia/Tokyo", "UTC", "Europe/Paris"},
		1: {"UTC", "Asia/Tokyo", "Europe/Paris"},
		2: {"UTC", "Europe/Paris", "Asia/Tokyo"},
		3: {"UTC", "Europe/Paris", "Asia/Tokyo"},
	}
	for insert, w := range want {
		s := NewStore()
		s.Load([]Zone{{ID: "UTC", OnBar: true}, {ID: "Europe/Paris", OnBar: true}, {ID: "Asia/Tokyo", OnBar: true}})
		changed := s.Reorder("Asia/Tokyo", insert)
		if got := ids(s.Zones()); !reflect.DeepEqual(got, w) {
			t.Fatalf("insert %d: %v", insert, got)
		}
		if changed != (insert < 2) {
			t.Fatalf("insert %d: changed = %v", insert, changed)
		}
	}
	s := NewStore()
	if s.Reorder("Nope/Zone", 0) || s.Reorder("UTC", -1) || s.Reorder("UTC", 99) {
		t.Fatal("bad reorder reported a change")
	}
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.ProposeDelete("UTC")
	if s.PendingDelete() != "UTC" || len(s.Zones()) != 4 {
		t.Fatal("delete applied before confirm")
	}
	if !s.ConfirmDelete() || s.Has("UTC") || s.PendingDelete() != "" {
		t.Fatal("confirm did not delete")
	}
	if s.ConfirmDelete() {
		t.Fatal("confirm without a pending delete reported a change")
	}
}

func TestEditStateClearsWhenZoneGoes(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.StartRename("UTC")
	s.ProposeDelete("Asia/Tokyo")
	if s.Renaming() != "" || s.PendingDelete() != "Asia/Tokyo" {
		t.Fatal("rename and delete confirm must be exclusive")
	}
	s.StartRename("UTC")
	s.Load([]Zone{{ID: "Asia/Tokyo", OnBar: true}})
	if s.Renaming() != "" || s.PendingDelete() != "" {
		t.Fatal("reload kept edit state for a missing zone")
	}
	s.StartRename("Nope/Zone")
	if s.Renaming() != "" {
		t.Fatal("started renaming a missing zone")
	}
}
```

- [ ] **Step 4: Run to verify failure**

Run: `go test ./plugins/world-clock/ -run 'Decode|Encode|Store|Add|Rename|Reorder|Delete|EditState'`
Expected: FAIL — `undefined: Decode`, `undefined: Zone`, etc.

- [ ] **Step 5: Implement** — `plugins/world-clock/zones.go`

```go
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
```

- [ ] **Step 6: Run to verify pass**

Run: `go test ./plugins/world-clock/ ./cmd/sysc-plugin-world-clock/`
Expected: PASS (old and new tests together).

- [ ] **Step 7: Commit**

```bash
git add plugins/world-clock/zones.go plugins/world-clock/zones_test.go
git commit -m "feat(world-clock): labelled zone model with legacy state migration"
```

---

### Task 3: Search index

**Files:**
- Create: `plugins/world-clock/search.go`
- Create: `plugins/world-clock/testdata/zone.tab`, `testdata/iso3166.tab`, `testdata/tzdata.zi`
- Test: `plugins/world-clock/search_test.go`

**Interfaces:**
- Consumes: `ValidZone` (Task 2), `ShortLabel` (currently in `clock.go`; moves to `reading.go` in Task 4 with the same signature `func ShortLabel(zone string) string`).
- Produces:
  ```go
  type Match struct { ID, City, Country string; Alias bool }
  type Index struct{ /* unexported */ }
  func NewIndex(zoneTab, isoTab, tzdata io.Reader) *Index   // nil readers allowed
  func OpenSystemIndex(dir string) *Index
  func (ix *Index) Limited() bool
  func (ix *Index) Search(query string, limit int) []Match  // does NOT exclude added zones
  ```

- [ ] **Step 1: Create fixtures**

`plugins/world-clock/testdata/zone.tab` (tab-separated, copy exactly; `\t` means a TAB):

```
# fixture subset of tzdata zone.tab
JP	+353916+1394441	Asia/Tokyo
NO	+5955+01045	Europe/Oslo
US	+404251-0740023	America/New_York	Eastern (most areas)
US	+340308-1181434	America/Los_Angeles	Pacific
IN	+2232+08822	Asia/Kolkata
AU	-3352+15113	Australia/Sydney	New South Wales (most areas)
RE	-2052+05528	Indian/Reunion
AR	-3436-05827	America/Argentina/Buenos_Aires	Buenos Aires (BA, CF)
NZ	-3652+17446	Pacific/Auckland	New Zealand (most areas)
NZ	-4357-17633	Pacific/Chatham	Chatham Islands
NP	+2743+08519	Asia/Kathmandu
```

`plugins/world-clock/testdata/iso3166.tab`:

```
# fixture subset of tzdata iso3166.tab
AR	Argentina
AU	Australia
IN	India
JP	Japan
NO	Norway
NP	Nepal
NZ	New Zealand
RE	Réunion
US	United States
```

`plugins/world-clock/testdata/tzdata.zi`:

```
# fixture subset
L America/New_York US/Eastern
L Asia/Kolkata Asia/Calcutta
```

- [ ] **Step 2: Write the failing tests** — `plugins/world-clock/search_test.go`

```go
package worldclock

import (
	"os"
	"reflect"
	"testing"
)

func fixtureIndex(t *testing.T) *Index {
	t.Helper()
	open := func(name string) *os.File {
		f, err := os.Open("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	return NewIndex(open("zone.tab"), open("iso3166.tab"), open("tzdata.zi"))
}

func matchIDs(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestSearchCityIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	got := fixtureIndex(t).Search("tokyo", 5)
	if len(got) == 0 || got[0] != (Match{ID: "Asia/Tokyo", City: "Tokyo", Country: "Japan"}) {
		t.Fatalf("tokyo = %+v", got)
	}
}

func TestSearchWordPrefixFindsNewYork(t *testing.T) {
	t.Parallel()
	if got := matchIDs(fixtureIndex(t).Search("york", 5)); len(got) == 0 || got[0] != "America/New_York" {
		t.Fatalf("york = %v", got)
	}
}

func TestSearchAliasCarriesItsName(t *testing.T) {
	t.Parallel()
	got := fixtureIndex(t).Search("san francisco", 5)
	if len(got) == 0 || got[0] != (Match{ID: "America/Los_Angeles", City: "San Francisco", Alias: true}) {
		t.Fatalf("san francisco = %+v", got)
	}
}

func TestSearchRanksExactBeforePrefixBeforeAlias(t *testing.T) {
	t.Parallel()
	// "os": Oslo is a city prefix (rank 2), the alias Osaka -> Asia/Tokyo is an
	// alias prefix (rank 3), and "buenOS aires" / "lOS angeles" only contain it
	// in their ids (rank 5, ordered by city).
	got := matchIDs(fixtureIndex(t).Search("os", 5))
	want := []string{"Europe/Oslo", "Asia/Tokyo", "America/Argentina/Buenos_Aires", "America/Los_Angeles"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("os = %v, want %v", got, want)
	}
}

func TestSearchCountryName(t *testing.T) {
	t.Parallel()
	if got := matchIDs(fixtureIndex(t).Search("norway", 5)); !reflect.DeepEqual(got, []string{"Europe/Oslo"}) {
		t.Fatalf("norway = %v", got)
	}
}

func TestSearchFoldsAccents(t *testing.T) {
	t.Parallel()
	got := fixtureIndex(t).Search("reunion", 5)
	if len(got) == 0 || got[0].ID != "Indian/Reunion" || got[0].Country != "Réunion" {
		t.Fatalf("reunion = %+v", got)
	}
}

func TestSearchExactIDAndLinkCaseInsensitive(t *testing.T) {
	t.Parallel()
	ix := fixtureIndex(t)
	if got := ix.Search("asia/tokyo", 5); len(got) == 0 || got[0].ID != "Asia/Tokyo" {
		t.Fatalf("exact id = %+v", got)
	}
	if got := ix.Search("us/eastern", 5); len(got) == 0 || got[0].ID != "US/Eastern" {
		t.Fatalf("link = %+v", got)
	}
	// Not in any table but a valid id verbatim.
	if got := ix.Search("Europe/Paris", 5); len(got) == 0 || got[0].ID != "Europe/Paris" {
		t.Fatalf("verbatim id = %+v", got)
	}
}

func TestSearchDedupesAndCaps(t *testing.T) {
	t.Parallel()
	ix := fixtureIndex(t)
	got := ix.Search("a", 3)
	if len(got) != 3 {
		t.Fatalf("cap: %d results", len(got))
	}
	seen := map[string]bool{}
	for _, m := range ix.Search("a", 50) {
		if seen[m.ID] {
			t.Fatalf("duplicate %s", m.ID)
		}
		seen[m.ID] = true
	}
	if ix.Search("   ", 5) != nil || ix.Search("tokyo", 0) != nil {
		t.Fatal("empty query or zero limit returned matches")
	}
}

func TestSearchWithoutTablesIsLimited(t *testing.T) {
	t.Parallel()
	ix := NewIndex(nil, nil, nil)
	if !ix.Limited() {
		t.Fatal("index without zone.tab not limited")
	}
	if got := ix.Search("mumbai", 5); len(got) == 0 || got[0].ID != "Asia/Kolkata" {
		t.Fatalf("alias without tables = %+v", got)
	}
	if got := ix.Search("utc", 5); len(got) == 0 || got[0].ID != "UTC" {
		t.Fatalf("utc = %+v", got)
	}
	if fixtureIndex(t).Limited() {
		t.Fatal("fixture index limited")
	}
}

func TestOpenSystemIndexToleratesMissingDir(t *testing.T) {
	t.Parallel()
	if !OpenSystemIndex(t.TempDir()).Limited() {
		t.Fatal("empty dir not limited")
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./plugins/world-clock/ -run Search`
Expected: FAIL — `undefined: NewIndex`.

- [ ] **Step 4: Implement** — `plugins/world-clock/search.go`

```go
package worldclock

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Match is one search result. City is the alias when an alias matched, which
// is also what the zone is labelled with when it is added.
type Match struct {
	ID      string
	City    string
	Country string
	Alias   bool
}

type entry struct {
	Match
	cityKey, countryKey, idKey string
}

// Index answers city, country, and id queries from the system tz tables plus
// an embedded alias list. It is built once and read-only afterwards.
type Index struct {
	entries []entry
	ids     map[string]string // folded exact id or link name -> canonical spelling
	country map[string]string // zone id -> country name
	limited bool
}

// aliases are large cities that are not zone names.
var aliases = []struct{ name, id string }{
	{"San Francisco", "America/Los_Angeles"}, {"Seattle", "America/Los_Angeles"},
	{"Portland", "America/Los_Angeles"}, {"Las Vegas", "America/Los_Angeles"},
	{"San Diego", "America/Los_Angeles"},
	{"Boston", "America/New_York"}, {"Washington", "America/New_York"},
	{"Miami", "America/New_York"}, {"Atlanta", "America/New_York"},
	{"Philadelphia", "America/New_York"},
	{"Dallas", "America/Chicago"}, {"Houston", "America/Chicago"}, {"Austin", "America/Chicago"},
	{"Salt Lake City", "America/Denver"},
	{"Montreal", "America/Toronto"},
	{"Rio de Janeiro", "America/Sao_Paulo"},
	{"Mumbai", "Asia/Kolkata"}, {"Delhi", "Asia/Kolkata"}, {"New Delhi", "Asia/Kolkata"},
	{"Bangalore", "Asia/Kolkata"}, {"Bengaluru", "Asia/Kolkata"},
	{"Chennai", "Asia/Kolkata"}, {"Hyderabad", "Asia/Kolkata"},
	{"Beijing", "Asia/Shanghai"}, {"Shenzhen", "Asia/Shanghai"}, {"Guangzhou", "Asia/Shanghai"},
	{"Osaka", "Asia/Tokyo"}, {"Kyoto", "Asia/Tokyo"},
	{"Abu Dhabi", "Asia/Dubai"},
	{"Tel Aviv", "Asia/Jerusalem"},
	{"Saigon", "Asia/Ho_Chi_Minh"},
	{"Canberra", "Australia/Sydney"},
	{"Wellington", "Pacific/Auckland"},
	{"Geneva", "Europe/Zurich"},
	{"Munich", "Europe/Berlin"}, {"Frankfurt", "Europe/Berlin"}, {"Hamburg", "Europe/Berlin"},
	{"Milan", "Europe/Rome"},
	{"Barcelona", "Europe/Madrid"},
	{"Manchester", "Europe/London"}, {"Edinburgh", "Europe/London"},
	{"GMT", "UTC"},
}

// OpenSystemIndex reads zone.tab, iso3166.tab, and tzdata.zi from dir. A
// missing file degrades search instead of failing.
func OpenSystemIndex(dir string) *Index {
	open := func(name string) io.Reader {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			return nil
		}
		return strings.NewReader(string(data))
	}
	return NewIndex(open("zone.tab"), open("iso3166.tab"), open("tzdata.zi"))
}

func eachLine(r io.Reader, fn func(string)) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fn(line)
	}
}

func NewIndex(zoneTab, isoTab, tzdata io.Reader) *Index {
	ix := &Index{ids: map[string]string{}, country: map[string]string{}, limited: zoneTab == nil}
	names := map[string]string{}
	eachLine(isoTab, func(line string) {
		if code, name, ok := strings.Cut(line, "\t"); ok {
			names[code] = name
		}
	})
	eachLine(zoneTab, func(line string) {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			return
		}
		id := f[2]
		ix.country[id] = names[f[0]]
		ix.ids[fold(id)] = id
		ix.add(Match{ID: id, City: ShortLabel(id), Country: names[f[0]]})
	})
	eachLine(tzdata, func(line string) {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "L" {
			ix.ids[fold(f[2])] = f[2]
		}
	})
	ix.ids["utc"] = "UTC"
	ix.add(Match{ID: "UTC", City: "UTC"})
	for _, a := range aliases {
		ix.add(Match{ID: a.id, City: a.name, Alias: true})
	}
	return ix
}

func (ix *Index) add(m Match) {
	ix.entries = append(ix.entries, entry{
		Match:      m,
		cityKey:    fold(m.City),
		countryKey: fold(m.Country),
		idKey:      fold(strings.ReplaceAll(m.ID, "_", " ")),
	})
}

// Limited reports that zone.tab was unavailable, so only aliases and exact ids
// can match.
func (ix *Index) Limited() bool { return ix.limited }

// Ranks, best first.
const (
	rankExactID = iota
	rankExactName
	rankCityPrefix
	rankAliasPrefix
	rankCountry
	rankIDSubstring
)

func (e entry) rank(q string) (int, bool) {
	switch {
	case e.cityKey == q:
		return rankExactName, true
	case !e.Alias && wordPrefix(e.cityKey, q):
		return rankCityPrefix, true
	case e.Alias && wordPrefix(e.cityKey, q):
		return rankAliasPrefix, true
	case e.countryKey != "" && wordPrefix(e.countryKey, q):
		return rankCountry, true
	case !e.Alias && strings.Contains(e.idKey, q):
		return rankIDSubstring, true
	}
	return 0, false
}

// Search returns up to limit matches, best first, one per zone id. It does not
// exclude zones already added: the caller decides whether a duplicate is an
// error (submit) or should be hidden (suggestions).
func (ix *Index) Search(query string, limit int) []Match {
	raw := strings.TrimSpace(query)
	q := fold(raw)
	if q == "" || limit <= 0 {
		return nil
	}
	type scored struct {
		m    Match
		rank int
	}
	best := map[string]scored{}
	consider := func(m Match, rank int) {
		if cur, ok := best[m.ID]; !ok || rank < cur.rank {
			best[m.ID] = scored{m, rank}
		}
	}
	if id, ok := ix.ids[q]; ok {
		consider(Match{ID: id, City: ShortLabel(id), Country: ix.country[id]}, rankExactID)
	} else if strings.Contains(raw, "/") && ValidZone(raw) {
		consider(Match{ID: raw, City: ShortLabel(raw)}, rankExactID)
	}
	for _, e := range ix.entries {
		if r, ok := e.rank(q); ok {
			consider(e.Match, r)
		}
	}
	out := make([]scored, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		if a, b := fold(out[i].m.City), fold(out[j].m.City); a != b {
			return a < b
		}
		return out[i].m.ID < out[j].m.ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	ms := make([]Match, len(out))
	for i, s := range out {
		ms[i] = s.m
	}
	return ms
}

func wordPrefix(s, q string) bool {
	if strings.HasPrefix(s, q) {
		return true
	}
	for i := 1; i < len(s); i++ {
		if (s[i-1] == ' ' || s[i-1] == '-') && strings.HasPrefix(s[i:], q) {
			return true
		}
	}
	return false
}

var accentFold = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
	'ç': 'c', 'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i', 'ñ': 'n',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ý': 'y', 'ÿ': 'y',
	'’': '\'',
}

// fold lower-cases and strips common Latin accents so "Réunion" matches
// "reunion" without a text-normalisation dependency.
func fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		r = unicode.ToLower(r)
		if f, ok := accentFold[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

- [ ] **Step 5: Run to verify pass**

Run: `go test ./plugins/world-clock/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add plugins/world-clock/search.go plugins/world-clock/search_test.go plugins/world-clock/testdata
git commit -m "feat(world-clock): search zones by city, country, alias, or id"
```

---

### Task 4: Readings relative to local time

**Files:**
- Create: `plugins/world-clock/reading.go`
- Modify: `plugins/world-clock/clock.go` (remove `Reading`, `ShortLabel`, `formatClock`, `formatOffset`; make `Readings` delegate)
- Modify: `plugins/world-clock/zones.go` (add `Store.Readings`)
- Test: `plugins/world-clock/reading_test.go`

**Interfaces:**
- Consumes: `Zone`, `Store` (Task 2).
- Produces:
  ```go
  type Reading struct {
      Zone, Label, Clock, Offset, Relative string
      DayShift int   // -1, 0, +1
      Daytime  bool
      OnBar    bool
  }
  func Read(z Zone, now time.Time, local *time.Location, hour24 bool) (Reading, error)
  func DisplayLabel(z Zone) string
  func ShortLabel(zone string) string
  func DayMarker(shift int) string        // "+1", "−1", ""
  func (s *Store) Readings(now time.Time, local *time.Location, hour24 bool) []Reading
  ```

- [ ] **Step 1: Write the failing tests** — `plugins/world-clock/reading_test.go`

```go
package worldclock

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestReadTokyoFromMelbourne(t *testing.T) {
	t.Parallel()
	mel := mustLoc(t, "Australia/Melbourne")
	now := time.Date(2026, 6, 1, 23, 30, 0, 0, mel) // AEST, UTC+10
	r, err := Read(Zone{ID: "Asia/Tokyo", OnBar: true}, now, mel, true)
	if err != nil {
		t.Fatal(err)
	}
	want := Reading{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "22:30", Offset: "UTC+9", Relative: "−1h", DayShift: 0, Daytime: false, OnBar: true}
	if r != want {
		t.Fatalf("reading = %+v", r)
	}
}

func TestReadDayShiftAcrossMidnight(t *testing.T) {
	t.Parallel()
	mel := mustLoc(t, "Australia/Melbourne")
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, mel)
	ny, _ := Read(Zone{ID: "America/New_York"}, now, mel, true)
	if ny.DayShift != -1 || ny.Relative != "−14h" || ny.Clock != "19:00" {
		t.Fatalf("new york = %+v", ny)
	}
	utc := time.UTC
	late := time.Date(2026, 6, 1, 20, 0, 0, 0, utc)
	tk, _ := Read(Zone{ID: "Asia/Tokyo"}, late, utc, false)
	if tk.DayShift != 1 || tk.Relative != "+9h" || tk.Clock != "5:00 AM" || tk.Daytime {
		t.Fatalf("tokyo = %+v", tk)
	}
}

func TestReadQuarterHourAndDST(t *testing.T) {
	t.Parallel()
	utc := time.UTC
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, utc)
	ktm, _ := Read(Zone{ID: "Asia/Kathmandu"}, now, utc, true)
	if ktm.Offset != "UTC+5:45" || ktm.Relative != "+5h45" {
		t.Fatalf("kathmandu = %+v", ktm)
	}
	cht, _ := Read(Zone{ID: "Pacific/Chatham"}, now, utc, true)
	if cht.Offset != "UTC+13:45" || cht.Relative != "+13h45" || cht.DayShift != 1 {
		t.Fatalf("chatham = %+v", cht)
	}
	nfl, _ := Read(Zone{ID: "America/St_Johns"}, now, utc, true)
	if nfl.Offset != "UTC-3:30" || nfl.Relative != "−3h30" {
		t.Fatalf("st johns = %+v", nfl)
	}
	// 2026-03-08 07:00 UTC is 03:00 EDT, just after the spring-forward.
	ny, _ := Read(Zone{ID: "America/New_York"}, time.Date(2026, 3, 8, 7, 0, 0, 0, utc), utc, true)
	if ny.Offset != "UTC-4" || ny.Clock != "03:00" {
		t.Fatalf("new york dst = %+v", ny)
	}
	ist, _ := Read(Zone{ID: "Asia/Kolkata"}, now, mustLoc(t, "Asia/Kathmandu"), true)
	if ist.Relative != "−15m" {
		t.Fatalf("kolkata from kathmandu = %+v", ist)
	}
}

func TestReadSameZoneAsLocal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	r, err := Read(Zone{ID: "UTC"}, now, time.UTC, true)
	if err != nil || r.Relative != "Same time" || r.DayShift != 0 || !r.Daytime {
		t.Fatalf("utc = %+v err = %v", r, err)
	}
}

func TestReadLabelAndInvalidZone(t *testing.T) {
	t.Parallel()
	now := time.Now()
	r, _ := Read(Zone{ID: "America/New_York", Label: "HQ"}, now, time.UTC, true)
	if r.Label != "HQ" {
		t.Fatalf("label = %q", r.Label)
	}
	if _, err := Read(Zone{ID: "Not/AZone"}, now, time.UTC, true); err == nil {
		t.Fatal("invalid zone read")
	}
}

func TestDaytimeBounds(t *testing.T) {
	t.Parallel()
	for hour, want := range map[int]bool{5: false, 6: true, 17: true, 18: false} {
		r, _ := Read(Zone{ID: "UTC"}, time.Date(2026, 6, 1, hour, 59, 0, 0, time.UTC), time.UTC, true)
		if r.Daytime != want {
			t.Fatalf("%d:59 daytime = %v", hour, r.Daytime)
		}
	}
}

func TestDayMarker(t *testing.T) {
	t.Parallel()
	if DayMarker(1) != "+1" || DayMarker(-1) != "−1" || DayMarker(0) != "" {
		t.Fatal("day markers")
	}
}

func TestStoreReadingsSkipNothingValid(t *testing.T) {
	t.Parallel()
	got := NewStore().Readings(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), time.UTC, true)
	if len(got) != 4 || got[1].Label != "New York" || got[0].Clock != "12:00" {
		t.Fatalf("readings = %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./plugins/world-clock/ -run 'Read|Daytime|DayMarker'`
Expected: FAIL — `undefined: Read` (and `Reading` has no field `Relative`).

- [ ] **Step 3: Implement** — `plugins/world-clock/reading.go`

```go
package worldclock

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Reading is one zone at one instant, formatted for display.
type Reading struct {
	Zone     string
	Label    string
	Clock    string
	Offset   string // "UTC+9", "UTC-3:30"
	Relative string // "+9h", "−3h30", "Same time"
	DayShift int    // the zone's date minus local's date: -1, 0, +1
	Daytime  bool   // local hour in the zone is in [6, 18)
	OnBar    bool
}

var locations sync.Map // zone id -> *time.Location

// loadLocation caches LoadLocation, which reads the zone file on every call;
// readings are recomputed every minute for every zone.
func loadLocation(id string) (*time.Location, error) {
	if loc, ok := locations.Load(id); ok {
		return loc.(*time.Location), nil
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, err
	}
	locations.Store(id, loc)
	return loc, nil
}

func Read(z Zone, now time.Time, local *time.Location, hour24 bool) (Reading, error) {
	if !ValidZone(z.ID) {
		return Reading{}, fmt.Errorf("%w: %q", ErrInvalidZone, z.ID)
	}
	loc, err := loadLocation(z.ID)
	if err != nil {
		return Reading{}, err
	}
	there, here := now.In(loc), now.In(local)
	_, zoneOff := there.Zone()
	_, localOff := here.Zone()
	return Reading{
		Zone:     z.ID,
		Label:    DisplayLabel(z),
		Clock:    formatClock(there, hour24),
		Offset:   formatOffset(zoneOff),
		Relative: formatRelative(zoneOff - localOff),
		DayShift: dayShift(there, here),
		Daytime:  there.Hour() >= 6 && there.Hour() < 18,
		OnBar:    z.OnBar,
	}, nil
}

// DisplayLabel is the custom label, or the zone's short name when none is set.
func DisplayLabel(z Zone) string {
	if z.Label != "" {
		return z.Label
	}
	return ShortLabel(z.ID)
}

// ShortLabel renders a zone's last path segment with underscores as spaces:
// "America/New_York" becomes "New York".
func ShortLabel(zone string) string {
	if i := strings.LastIndex(zone, "/"); i >= 0 && i+1 < len(zone) {
		zone = zone[i+1:]
	}
	return strings.ReplaceAll(zone, "_", " ")
}

// DayMarker is the compact day-shift suffix cards and the bar show.
func DayMarker(shift int) string {
	switch {
	case shift > 0:
		return "+1"
	case shift < 0:
		return "−1"
	}
	return ""
}

func formatClock(t time.Time, hour24 bool) string {
	if hour24 {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

func formatOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	h, m := seconds/3600, seconds%3600/60
	if m == 0 {
		return fmt.Sprintf("UTC%s%d", sign, h)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, h, m)
}

func formatRelative(seconds int) string {
	if seconds == 0 {
		return "Same time"
	}
	sign := "+"
	if seconds < 0 {
		sign, seconds = "−", -seconds
	}
	h, m := seconds/3600, seconds%3600/60
	switch {
	case m == 0:
		return fmt.Sprintf("%s%dh", sign, h)
	case h == 0:
		return fmt.Sprintf("%s%dm", sign, m)
	}
	return fmt.Sprintf("%s%dh%02d", sign, h, m)
}

func dayShift(there, here time.Time) int {
	a := time.Date(there.Year(), there.Month(), there.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(here.Year(), here.Month(), here.Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case a.After(b):
		return 1
	case a.Before(b):
		return -1
	}
	return 0
}
```

Append to `zones.go`:

```go
// Readings formats every zone at now. A zone that stopped resolving (tzdata
// changed underneath) is skipped rather than shown as an error.
func (s *Store) Readings(now time.Time, local *time.Location, hour24 bool) []Reading {
	zones := s.Zones()
	out := make([]Reading, 0, len(zones))
	for _, z := range zones {
		if r, err := Read(z, now, local, hour24); err == nil {
			out = append(out, r)
		}
	}
	return out
}
```

- [ ] **Step 4: Remove the moved definitions from `clock.go`**

Delete from `clock.go`: the `ShortLabel` function, the `Reading` struct, `formatClock`, and `formatOffset`. Replace its `Readings` method body with:

```go
func (c *Clock) Readings(now time.Time) []Reading {
	c.mu.Lock()
	zones, hour24 := append([]string(nil), c.zones...), c.hour24
	c.mu.Unlock()
	out := make([]Reading, 0, len(zones))
	for _, z := range zones {
		if r, err := Read(Zone{ID: z, OnBar: true}, now, time.UTC, hour24); err == nil {
			out = append(out, r)
		}
	}
	return out
}
```

Remove now-unused imports from `clock.go` if the compiler reports them.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./plugins/world-clock/ ./cmd/sysc-plugin-world-clock/`
Expected: PASS, including the old `TestOffsetAndClockFormats` (`UTC+0`, `15:04`, `3:04 PM`).

- [ ] **Step 6: Commit**

```bash
git add plugins/world-clock/reading.go plugins/world-clock/reading_test.go plugins/world-clock/zones.go plugins/world-clock/clock.go
git commit -m "feat(world-clock): read zones relative to local time"
```

---

### Task 5: Panel tree

**Files:**
- Create: `plugins/world-clock/panel.go`
- Test: `plugins/world-clock/panel_test.go`

**Interfaces:**
- Consumes: `Reading`, `DayMarker` (Task 4), `Match` (Task 3).
- Produces:
  ```go
  const PanelWidth, PanelHeight = 420, 360
  type Suggestion struct { ID, Title string }
  type PanelState struct {
      Readings      []Reading
      Query         string
      QueryReseed   uint64
      Suggestions   []Suggestion
      PendingDelete string
      Renaming      string
      RenameDraft   string
      RenameReseed  uint64
      Error         string // error tone
      Notice        string // subtle tone
  }
  func Panel(s PanelState) *v1.Node
  func PanelPatch(readings []Reading, renaming string) []v1.Replacement
  func SuggestionTitle(m Match, relative string) string
  ```
- Node ids the loop (Task 7) routes on: `search`, `add`, `pick:<id>`, `drag:<id>`, `drop:<i>`, `bar:<id>`, `edit:<id>`, `label:<id>`, `rename-ok`, `rename-cancel`, `del:<id>`, `del-ok`, `del-cancel`. Drag type `world-clock-zone`.

- [ ] **Step 1: Write the failing tests** — `plugins/world-clock/panel_test.go`

```go
package worldclock

import (
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func visit(n *v1.Node, fn func(*v1.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		visit(c, fn)
	}
}

func findID(root *v1.Node, id string) *v1.Node {
	var hit *v1.Node
	visit(root, func(n *v1.Node) {
		if hit == nil && n.ID == id {
			hit = n
		}
	})
	return hit
}

func hasText(root *v1.Node, text string, tone v1.Tone) bool {
	found := false
	visit(root, func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text == text && n.Tone == tone {
			found = true
		}
	})
	return found
}

func sampleReadings() []Reading {
	return []Reading{
		{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "21:04", Offset: "UTC+9", Relative: "+9h", DayShift: 1, Daytime: false, OnBar: true},
		{Zone: "UTC", Label: "UTC", Clock: "12:04", Offset: "UTC+0", Relative: "Same time", Daytime: true, OnBar: false},
	}
}

func lintPanel(t *testing.T, name string, s PanelState) *v1.Node {
	t.Helper()
	root := Panel(s)
	for _, f := range shelllint.Tree(root, v1.ViewPanel, PanelWidth, PanelHeight) {
		t.Errorf("%s: %s", name, f)
	}
	return root
}

func TestPanelLintEveryState(t *testing.T) {
	t.Parallel()
	many := make([]Reading, 8)
	for i := range many {
		many[i] = Reading{Zone: "America/Argentina/Buenos_Aires", Label: strings.Repeat("東", MaxLabelRunes), Clock: "12:04 PM", Offset: "UTC-3:30", Relative: "−13h30", DayShift: -1, OnBar: true}
	}
	states := map[string]PanelState{
		"empty":       {},
		"zones":       {Readings: sampleReadings()},
		"eight":       {Readings: many},
		"error":       {Readings: sampleReadings(), Error: "Couldn't save zones", Notice: "Limited search: tz tables not found"},
		"suggestions": {Query: "to", Suggestions: []Suggestion{{ID: "Asia/Tokyo", Title: "Tokyo · Japan · +9h"}, {ID: "America/Toronto", Title: "Toronto · Canada · −14h"}}},
		"no-match":    {Query: "zzz"},
		"renaming":    {Readings: sampleReadings(), Renaming: "Asia/Tokyo", RenameDraft: "Office"},
		"deleting":    {Readings: sampleReadings(), PendingDelete: "UTC"},
	}
	for name, s := range states {
		lintPanel(t, name, s)
	}
}

func TestPanelZoneCard(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "zones", PanelState{Readings: sampleReadings()})
	for _, id := range []string{"drag:Asia/Tokyo", "bar:Asia/Tokyo", "edit:Asia/Tokyo", "del:Asia/Tokyo", "drop:0", "drop:1", "drop:2"} {
		if findID(root, id) == nil {
			t.Fatalf("missing %s", id)
		}
	}
	if !hasText(root, "Asia/Tokyo · UTC+9", v1.ToneSubtle) || !hasText(root, "+9h", v1.ToneSubtle) || !hasText(root, "+1", v1.ToneAccent) {
		t.Fatal("card lines missing")
	}
	if !hasText(root, "Tokyo", v1.ToneNormal) || !hasText(root, "UTC", v1.ToneSubtle) {
		t.Fatal("on-bar label should be normal tone, hidden label subtle")
	}
	if findID(root, "bar:Asia/Tokyo").Icon != "visibility" || findID(root, "bar:UTC").Icon != "visibility_off" {
		t.Fatal("bar toggle icons")
	}
	if findID(root, "search").Placeholder != "Search a city or country" {
		t.Fatal("search placeholder")
	}
	if !findID(root, "add").Disabled {
		t.Fatal("add enabled with an empty query")
	}
}

func TestPanelEmptyState(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "empty", PanelState{})
	if !hasText(root, "No zones yet. Search for a city above.", v1.ToneSubtle) {
		t.Fatal("empty state missing")
	}
	if findID(root, "drop:0") != nil {
		t.Fatal("drop zone in an empty list")
	}
}

func TestPanelSuggestionsReplaceTheList(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "suggestions", PanelState{Readings: sampleReadings(), Query: "to", QueryReseed: 3,
		Suggestions: []Suggestion{{ID: "America/Toronto", Title: "Toronto · Canada · −14h"}}})
	if findID(root, "pick:America/Toronto") == nil || findID(root, "drag:Asia/Tokyo") != nil {
		t.Fatal("suggestions should occupy the list slot")
	}
	if s := findID(root, "search"); s.Text != "to" || s.Reseed != 3 || findID(root, "add").Disabled {
		t.Fatalf("search = %+v", s)
	}
	none := lintPanel(t, "no-match", PanelState{Query: "zzz"})
	if !hasText(none, "No matching zone", v1.ToneSubtle) {
		t.Fatal("no-match line missing")
	}
}

func TestPanelInlineEdits(t *testing.T) {
	t.Parallel()
	ren := lintPanel(t, "renaming", PanelState{Readings: sampleReadings(), Renaming: "Asia/Tokyo", RenameDraft: "Office", RenameReseed: 2})
	in := findID(ren, "label:Asia/Tokyo")
	if in == nil || in.Kind != v1.KindTextInput || in.Text != "Office" || in.Reseed != 2 || in.Placeholder != "Tokyo" {
		t.Fatalf("rename input = %+v", in)
	}
	if findID(ren, "rename-ok") == nil || findID(ren, "rename-cancel") == nil || findID(ren, "edit:Asia/Tokyo") != nil {
		t.Fatal("rename actions")
	}
	del := lintPanel(t, "deleting", PanelState{Readings: sampleReadings(), PendingDelete: "UTC"})
	if ok := findID(del, "del-ok"); ok == nil || ok.Fill != "error" || findID(del, "del-cancel") == nil || findID(del, "del:UTC") != nil {
		t.Fatal("delete confirm must replace the row's actions")
	}
	if findID(del, "del:Asia/Tokyo") == nil {
		t.Fatal("other rows keep their actions")
	}
}

func TestPanelErrorAndNotice(t *testing.T) {
	t.Parallel()
	root := lintPanel(t, "error", PanelState{Error: "Couldn't save zones", Notice: "Limited search: tz tables not found"})
	if !hasText(root, "Couldn't save zones", v1.ToneError) || !hasText(root, "Limited search: tz tables not found", v1.ToneSubtle) {
		t.Fatal("error or notice missing")
	}
}

func TestPanelPatchKeysExistInPanel(t *testing.T) {
	t.Parallel()
	for _, renaming := range []string{"", "Asia/Tokyo"} {
		root := Panel(PanelState{Readings: sampleReadings(), Renaming: renaming})
		keys := map[string]bool{}
		visit(root, func(n *v1.Node) {
			if n.Key != "" {
				keys[n.Key] = true
			}
		})
		patch := PanelPatch(sampleReadings(), renaming)
		if len(patch) == 0 {
			t.Fatal("empty patch")
		}
		for _, r := range patch {
			if !keys[r.Key] || r.Node.Key != r.Key {
				t.Fatalf("renaming=%q: patch key %s not in panel", renaming, r.Key)
			}
			if err := v1.Validate(&v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{r.Node}}, v1.ViewPanel); err != nil {
				t.Fatalf("patch node %s: %v", r.Key, err)
			}
		}
	}
}

func TestSuggestionTitle(t *testing.T) {
	t.Parallel()
	if got := SuggestionTitle(Match{City: "Tokyo", Country: "Japan"}, "+9h"); got != "Tokyo · Japan · +9h" {
		t.Fatalf("title = %q", got)
	}
	if got := SuggestionTitle(Match{City: "UTC"}, "Same time"); got != "UTC · Same time" {
		t.Fatalf("title = %q", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./plugins/world-clock/ -run 'Panel|Suggestion'`
Expected: FAIL — `undefined: Panel`.

- [ ] **Step 3: Implement** — `plugins/world-clock/panel.go`

```go
package worldclock

import (
	"strconv"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// PanelWidth and PanelHeight are the manifest's panel box; tests lint at it.
const (
	PanelWidth  = 420
	PanelHeight = 360
)

const (
	zoneDragType = "world-clock-zone"
	listHeight   = 248
	clockWidth   = 120
	iconButton   = 28
)

type Suggestion struct {
	ID    string
	Title string
}

// PanelState is everything the panel draws; the loop owns it.
type PanelState struct {
	Readings      []Reading
	Query         string
	QueryReseed   uint64
	Suggestions   []Suggestion
	PendingDelete string
	Renaming      string
	RenameDraft   string
	RenameReseed  uint64
	Error         string
	Notice        string
}

var (
	activate = []v1.EventKind{v1.EventActivate}
	editing  = []v1.EventKind{v1.EventChange, v1.EventSubmit}
)

func Panel(s PanelState) *v1.Node {
	children := []*v1.Node{
		{Kind: v1.KindText, Text: "World Clock", Size: "title", Bold: true},
		searchRow(s),
	}
	if s.Error != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: s.Error, Tone: v1.ToneError})
	}
	if s.Notice != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: s.Notice, Tone: v1.ToneSubtle})
	}
	if s.Query != "" {
		children = append(children, suggestionList(s.Suggestions))
	} else {
		children = append(children, zoneList(s))
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: children}
}

func searchRow(s PanelState) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindTextInput, ID: "search", Text: s.Query, Name: "Search time zones", Role: "textbox",
			Placeholder: "Search a city or country", Height: 40, SubmitOnEnter: true, Reseed: s.QueryReseed, Events: editing},
		{Kind: v1.KindButton, ID: "add", Icon: "add", Name: "Add the top match", Role: "button", Fill: "accent",
			Width: 40, Height: 40, Disabled: strings.TrimSpace(s.Query) == "", Events: activate},
	}}
}

func suggestionList(suggestions []Suggestion) *v1.Node {
	rows := make([]*v1.Node, 0, len(suggestions)+1)
	if len(suggestions) == 0 {
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: "No matching zone", Tone: v1.ToneSubtle})
	}
	for _, m := range suggestions {
		rows = append(rows, &v1.Node{Kind: v1.KindButton, ID: "pick:" + m.ID, Text: m.Title, Name: "Add " + m.Title,
			Role: "button", Fill: "soft", Height: 36, Events: activate})
	}
	return &v1.Node{Kind: v1.KindList, Height: listHeight, Gap: 4, Children: rows}
}

func zoneList(s PanelState) *v1.Node {
	rows := make([]*v1.Node, 0, 2*len(s.Readings)+1)
	if len(s.Readings) == 0 {
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: "No zones yet. Search for a city above.", Tone: v1.ToneSubtle})
	}
	for i, r := range s.Readings {
		rows = append(rows, dropGap(i), zoneCard(r, s))
	}
	if len(s.Readings) > 0 {
		rows = append(rows, dropGap(len(s.Readings)))
	}
	return &v1.Node{Kind: v1.KindList, Height: listHeight, Children: rows}
}

func dropGap(i int) *v1.Node {
	return &v1.Node{Kind: v1.KindDropZone, ID: "drop:" + strconv.Itoa(i), Accept: []string{zoneDragType},
		Height: 6, Events: []v1.EventKind{v1.EventDrop}}
}

func zoneCard(r Reading, s PanelState) *v1.Node {
	var name *v1.Node
	if s.Renaming == r.Zone {
		name = &v1.Node{Kind: v1.KindTextInput, ID: "label:" + r.Zone, Text: s.RenameDraft, Name: "Label for " + r.Zone,
			Role: "textbox", Placeholder: ShortLabel(r.Zone), Height: 36, SubmitOnEnter: true, Reseed: s.RenameReseed, Events: editing}
	} else {
		tone := v1.ToneNormal
		if !r.OnBar {
			tone = v1.ToneSubtle
		}
		name = &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
			{Kind: v1.KindText, Text: r.Label, Bold: true, Tone: tone},
			metaText(r),
		}}
	}
	return &v1.Node{Kind: v1.KindRow, Key: "row:" + r.Zone, Gap: 6, Fill: "card", Radius: 10, Padding: 8, Children: []*v1.Node{
		{Kind: v1.KindDragSource, ID: "drag:" + r.Zone, Text: "≡", Name: "Reorder " + r.Label, Role: "button",
			DragType: zoneDragType, Payload: r.Zone, Width: 24, Events: []v1.EventKind{v1.EventPointer}},
		name,
		clockColumn(r),
		skyIcon(r),
		actions(r, s),
	}}
}

func metaText(r Reading) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Key: "meta:" + r.Zone, Text: r.Zone + " · " + r.Offset, Tone: v1.ToneSubtle}
}

func clockColumn(r Reading) *v1.Node {
	tone := v1.ToneAccent
	if !r.OnBar {
		tone = v1.ToneSubtle
	}
	top := []*v1.Node{{Kind: v1.KindText, Text: r.Clock, Bold: true, Tabular: true, Tone: tone}}
	if m := DayMarker(r.DayShift); m != "" {
		top = append(top, &v1.Node{Kind: v1.KindText, Text: m, Tone: v1.ToneAccent})
	}
	return &v1.Node{Kind: v1.KindColumn, Key: "clock:" + r.Zone, Gap: 2, Width: clockWidth, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 4, Children: top},
		{Kind: v1.KindText, Text: r.Relative, Tone: v1.ToneSubtle},
	}}
}

func skyIcon(r Reading) *v1.Node {
	icon := "bedtime"
	if r.Daytime {
		icon = "sunny"
	}
	return &v1.Node{Kind: v1.KindIcon, Key: "sky:" + r.Zone, Icon: icon}
}

func iconAction(id, icon, name, fill string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Icon: icon, Name: name, Role: "button", Fill: fill,
		Width: iconButton, Height: iconButton, Events: activate}
}

func actions(r Reading, s PanelState) *v1.Node {
	var buttons []*v1.Node
	switch {
	case s.Renaming == r.Zone:
		buttons = []*v1.Node{
			iconAction("rename-ok", "check", "Save label for "+r.Zone, "accent"),
			iconAction("rename-cancel", "close", "Cancel rename", ""),
		}
	case s.PendingDelete == r.Zone:
		buttons = []*v1.Node{
			iconAction("del-ok", "check", "Remove "+r.Label, "error"),
			iconAction("del-cancel", "close", "Keep "+r.Label, ""),
		}
	default:
		eye, verb := "visibility", "Hide "
		if !r.OnBar {
			eye, verb = "visibility_off", "Show "
		}
		buttons = []*v1.Node{
			iconAction("bar:"+r.Zone, eye, verb+r.Label+" on the bar", ""),
			iconAction("edit:"+r.Zone, "edit", "Rename "+r.Label, ""),
			iconAction("del:"+r.Zone, "delete", "Remove "+r.Label, ""),
		}
	}
	return &v1.Node{Kind: v1.KindRow, Gap: 2, PinEnd: true, Children: buttons}
}

// PanelPatch is the minute update for a panel showing zone cards. The meta
// line of the zone being renamed is absent from the tree, so it is skipped.
func PanelPatch(readings []Reading, renaming string) []v1.Replacement {
	out := make([]v1.Replacement, 0, 3*len(readings))
	for _, r := range readings {
		out = append(out,
			v1.Replacement{Key: "clock:" + r.Zone, Node: clockColumn(r)},
			v1.Replacement{Key: "sky:" + r.Zone, Node: skyIcon(r)})
		if r.Zone != renaming {
			out = append(out, v1.Replacement{Key: "meta:" + r.Zone, Node: metaText(r)})
		}
	}
	return out
}

func SuggestionTitle(m Match, relative string) string {
	parts := []string{m.City}
	if m.Country != "" {
		parts = append(parts, m.Country)
	}
	return strings.Join(append(parts, relative), " · ")
}
```

- [ ] **Step 4: Run and fix geometry**

Run: `go test ./plugins/world-clock/ -run 'Panel|Suggestion' -v`
Expected: PASS. If `TestPanelLintEveryState` reports a refused row, read the finding (it names the node and both sizes) and adjust only these constants/fields: `clockWidth`, the drag source `Width`, `iconButton`, card `Gap`/`Padding`, `listHeight`. Do not drop content. Record the final values in the commit message body.

- [ ] **Step 5: Commit**

```bash
git add plugins/world-clock/panel.go plugins/world-clock/panel_test.go
git commit -m "feat(world-clock): search-first panel with inline row edits"
```

---

### Task 6: Bar modes and tooltip

**Files:**
- Create: `plugins/world-clock/bar.go`
- Test: `plugins/world-clock/bar_test.go`

**Interfaces:**
- Consumes: `Reading`, `DayMarker` (Task 4).
- Produces:
  ```go
  type BarMode string
  const (BarIcon BarMode = "icon"; BarPrimary = "primary"; BarAll = "all"; BarCycle = "cycle")
  func ParseBarMode(s string) (BarMode, bool)
  func OnBar(readings []Reading) []Reading
  func BarText(mode BarMode, onBar []Reading, cycle int) string
  func BarButton(mode BarMode, onBar []Reading, cycle int) *v1.Node   // Key "bar", ID "open"
  func Bar(mode BarMode, onBar []Reading, cycle int) *v1.Node         // row root
  func Tooltip(onBar []Reading) *v1.Node
  ```

- [ ] **Step 1: Write the failing tests** — `plugins/world-clock/bar_test.go`

```go
package worldclock

import (
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func barZones() []Reading {
	return []Reading{
		{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "21:04", Relative: "+9h", DayShift: 1, OnBar: true},
		{Zone: "Europe/Berlin", Label: "Berlin", Clock: "14:04", Relative: "+2h", OnBar: true},
		{Zone: "America/New_York", Label: "New York", Clock: "08:04", Relative: "−4h", OnBar: true},
	}
}

func lintBar(t *testing.T, root *v1.Node) {
	t.Helper()
	for _, f := range shelllint.Tree(root, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
		t.Errorf("bar: %s", f)
	}
}

func TestBarModes(t *testing.T) {
	t.Parallel()
	z := barZones()
	cases := []struct {
		mode  BarMode
		cycle int
		want  string
	}{
		{BarPrimary, 0, "Tokyo 21:04 +1"},
		{BarAll, 0, "Tokyo 21:04 +1"}, // a second entry would exceed barTextBytes
		{BarCycle, 1, "Berlin 14:04"},
		{BarCycle, 5, "New York 08:04"},
		{BarIcon, 0, ""},
	}
	for _, c := range cases {
		if got := BarText(c.mode, z, c.cycle); got != c.want {
			t.Fatalf("%s/%d = %q, want %q", c.mode, c.cycle, got, c.want)
		}
		lintBar(t, Bar(c.mode, z, c.cycle))
	}
	short := []Reading{
		{Label: "UTC", Clock: "12:04", OnBar: true},
		{Label: "Berlin", Clock: "14:04", OnBar: true},
		{Label: "Tokyo", Clock: "21:04", OnBar: true},
	}
	if got := BarText(BarAll, short, 0); got != "UTC 12:04 · Berlin 14:04" {
		t.Fatalf("all = %q", got)
	}
	lintBar(t, Bar(BarAll, short, 0))
}

func TestBarFallsBackToGlobe(t *testing.T) {
	t.Parallel()
	for _, mode := range []BarMode{BarIcon, BarPrimary, BarAll, BarCycle} {
		b := BarButton(mode, nil, 0)
		if b.Icon != "public" || b.Text != "" || b.ID != "open" || b.Key != "bar" {
			t.Fatalf("%s with no zones = %+v", mode, b)
		}
		lintBar(t, Bar(mode, nil, 0))
	}
}

func TestBarLongMultibyteLabelFitsAndKeepsTime(t *testing.T) {
	t.Parallel()
	long := []Reading{{Zone: "Asia/Tokyo", Label: strings.Repeat("東", MaxLabelRunes), Clock: "12:04 PM", DayShift: -1, OnBar: true}}
	got := BarText(BarPrimary, long, 0)
	if !strings.HasSuffix(got, " 12:04 PM −1") || !strings.Contains(got, "…") {
		t.Fatalf("bar = %q", got)
	}
	lintBar(t, Bar(BarPrimary, long, 0))
	lintBar(t, Bar(BarAll, append(long, barZones()...), 0))
}

func TestParseBarMode(t *testing.T) {
	t.Parallel()
	if m, ok := ParseBarMode("cycle"); !ok || m != BarCycle {
		t.Fatal("cycle")
	}
	if _, ok := ParseBarMode("sideways"); ok {
		t.Fatal("unknown mode accepted")
	}
}

func TestOnBarFilters(t *testing.T) {
	t.Parallel()
	z := append(barZones(), Reading{Zone: "UTC", OnBar: false})
	if got := OnBar(z); len(got) != 3 {
		t.Fatalf("on bar = %d", len(got))
	}
}

func TestTooltipListsZonesAndCaps(t *testing.T) {
	t.Parallel()
	root := Tooltip(barZones())
	if err := v1.Validate(root, v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if !hasText(root, "Tokyo 21:04 · +9h, tomorrow", v1.ToneNormal) || !hasText(root, "Berlin 14:04 · +2h", v1.ToneNormal) {
		t.Fatal("tooltip lines")
	}
	many := make([]Reading, 11)
	for i := range many {
		many[i] = Reading{Zone: "UTC", Label: "UTC", Clock: "12:00", Relative: "Same time", OnBar: true}
	}
	capped := Tooltip(many)
	if !hasText(capped, "+3 more", v1.ToneSubtle) {
		t.Fatal("tooltip not capped")
	}
	for _, f := range shelllint.Tree(capped, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
		t.Errorf("tooltip: %s", f)
	}
	if !hasText(Tooltip(nil), "No zones on the bar", v1.ToneSubtle) {
		t.Fatal("empty tooltip")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./plugins/world-clock/ -run 'Bar|Tooltip'`
Expected: FAIL — `undefined: BarText`.

- [ ] **Step 3: Implement** — `plugins/world-clock/bar.go`

```go
package worldclock

import (
	"fmt"
	"strings"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type BarMode string

const (
	BarIcon    BarMode = "icon"
	BarPrimary BarMode = "primary"
	BarAll     BarMode = "all"
	BarCycle   BarMode = "cycle"
)

// barTextBytes budgets the bar label in the host's crude metric (8 px per
// byte) so the button never overruns the 240 px bar slot.
const barTextBytes = 28

const maxTooltipZones = 8

func ParseBarMode(s string) (BarMode, bool) {
	switch m := BarMode(s); m {
	case BarIcon, BarPrimary, BarAll, BarCycle:
		return m, true
	}
	return "", false
}

func OnBar(readings []Reading) []Reading {
	out := make([]Reading, 0, len(readings))
	for _, r := range readings {
		if r.OnBar {
			out = append(out, r)
		}
	}
	return out
}

// barEntry is "Label clock [marker]", shortening the label (never the time) to
// fit budget bytes.
func barEntry(r Reading, budget int) string {
	suffix := " " + r.Clock
	if m := DayMarker(r.DayShift); m != "" {
		suffix += " " + m
	}
	label := r.Label
	if len(label)+len(suffix) > budget {
		room := budget - len(suffix) - len("…")
		for len(label) > room && label != "" {
			_, size := utf8.DecodeLastRuneInString(label)
			label = label[:len(label)-size]
		}
		label += "…"
	}
	return label + suffix
}

// BarText is the bar's label for mode, or "" when the bar should show the
// globe instead.
func BarText(mode BarMode, onBar []Reading, cycle int) string {
	if mode == BarIcon || len(onBar) == 0 {
		return ""
	}
	switch mode {
	case BarAll:
		out := barEntry(onBar[0], barTextBytes)
		for _, r := range onBar[1:] {
			next := out + " · " + barEntry(r, barTextBytes)
			if len(next) > barTextBytes {
				break
			}
			out = next
		}
		return out
	case BarCycle:
		if cycle < 0 {
			cycle = 0
		}
		return barEntry(onBar[cycle%len(onBar)], barTextBytes)
	}
	return barEntry(onBar[0], barTextBytes)
}

// BarButton is the whole bar control: it opens the panel, and the minute
// patch replaces it by its key.
func BarButton(mode BarMode, onBar []Reading, cycle int) *v1.Node {
	n := &v1.Node{Kind: v1.KindButton, ID: "open", Key: "bar", Name: "Open world clock", Role: "button",
		Tabular: true, Events: []v1.EventKind{v1.EventActivate}}
	if text := BarText(mode, onBar, cycle); text != "" {
		n.Text = text
	} else {
		n.Icon = "public"
	}
	return n
}

func Bar(mode BarMode, onBar []Reading, cycle int) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{BarButton(mode, onBar, cycle)}}
}

func Tooltip(onBar []Reading) *v1.Node {
	lines := []*v1.Node{{Kind: v1.KindText, Text: "World Clock", Bold: true}}
	if len(onBar) == 0 {
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: "No zones on the bar", Tone: v1.ToneSubtle})
	}
	for i, r := range onBar {
		if i == maxTooltipZones {
			lines = append(lines, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("+%d more", len(onBar)-i), Tone: v1.ToneSubtle})
			break
		}
		rel := r.Relative
		switch {
		case r.DayShift > 0:
			rel += ", tomorrow"
		case r.DayShift < 0:
			rel += ", yesterday"
		}
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: strings.Join([]string{r.Label + " " + r.Clock, rel}, " · ")})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: lines}
}
```

- [ ] **Step 4: Run and fix geometry**

Run: `go test ./plugins/world-clock/ -run 'Bar|Tooltip' -v`
Expected: PASS. If lint refuses the bar button, lower `barTextBytes` one byte at a time until it passes. `"UTC 12:04 · Berlin 14:04"` is 25 bytes, so the `short` case holds down to 25; below that, change its expectation to `"UTC 12:04"` and say so in the commit body along with the final value.

- [ ] **Step 5: Commit**

```bash
git add plugins/world-clock/bar.go plugins/world-clock/bar_test.go
git commit -m "feat(world-clock): labelled bar with icon, primary, all, and cycle modes"
```

---

### Task 7: Event loop, manifest, and removal of the old model

**Files:**
- Rewrite: `cmd/sysc-plugin-world-clock/main.go`
- Create: `cmd/sysc-plugin-world-clock/main_test.go`
- Delete: `plugins/world-clock/clock.go`, `clock_test.go`, `view.go`, `view_test.go`
- Modify: `plugins/world-clock/manifest.json`
- Modify: `README.md` (World Clock row, line 33)

**Interfaces:**
- Consumes: everything in Tasks 2–6.
- Produces: `runPlugin(in io.Reader, out io.Writer, env environment) error`; `nextMinute(now time.Time) time.Duration`; `settings.apply(map[string]any)`.

- [ ] **Step 1: Delete the superseded files and verify the package still builds**

```bash
git rm plugins/world-clock/clock.go plugins/world-clock/clock_test.go plugins/world-clock/view.go plugins/world-clock/view_test.go
go test ./plugins/world-clock/
```

Expected: PASS (the cmd package will not compile until Step 4).

- [ ] **Step 2: Write the failing tests** — `cmd/sysc-plugin-world-clock/main_test.go`

```go
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	worldclock "github.com/Nomadcxx/sysc-plugins/plugins/world-clock"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const zoneTab = "JP\t+353916+1394441\tAsia/Tokyo\nAU\t-3352+15113\tAustralia/Sydney\n"
const isoTab = "AU\tAustralia\nJP\tJapan\n"

type harness struct {
	t       *testing.T
	host    io.WriteCloser
	lines   chan []byte
	done    chan error
	stopped bool
}

func start(t *testing.T, stored *v1.StateGetResult) *harness {
	t.Helper()
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &harness{t: t, host: host, lines: make(chan []byte, 64), done: make(chan error, 1)}
	now := time.Date(2026, 9, 15, 12, 0, 30, 0, time.UTC)
	go func() {
		err := runPlugin(input, output, environment{
			now: func() time.Time { return now }, local: time.UTC,
			index: worldclock.NewIndex(strings.NewReader(zoneTab), strings.NewReader(isoTab), nil),
		})
		_ = output.Close() // ends the line reader so a test can drain everything
		h.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(plugin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			h.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(h.lines)
	}()
	h.send(map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 7}}, "capabilities": []string{"panels", "settings", "state"}})
	if got := h.next(); messageType(got) != "plugin.hello" {
		t.Fatalf("first message = %s", got)
	}
	call := h.nextCall(v1.CallStateGet)
	result := v1.StateGetResult{Found: false}
	if stored != nil {
		result = *stored
	}
	raw, _ := json.Marshal(result)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: raw})
	t.Cleanup(h.stop)
	return h
}

func (h *harness) send(message any) {
	h.t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.host.Write(append(data, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) next() []byte {
	h.t.Helper()
	select {
	case line, ok := <-h.lines:
		if !ok {
			h.t.Fatal("plugin output closed")
		}
		return line
	case <-time.After(3 * time.Second):
		h.t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

func (h *harness) nextCall(kind v1.CallKind) v1.HostCall {
	h.t.Helper()
	for {
		line := h.next()
		var call v1.HostCall
		if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil && call.Call == kind {
			return call
		}
	}
}

// snapshotWhere returns the first snapshot whose root satisfies ok.
func (h *harness) snapshotWhere(ok func(*v1.Node) bool) v1.ViewSnapshot {
	h.t.Helper()
	for {
		line := h.next()
		var s v1.ViewSnapshot
		if messageType(line) == v1.TypeViewSnapshot && json.Unmarshal(line, &s) == nil && ok(s.Root) {
			return s
		}
	}
}

func (h *harness) openPanel() v1.ViewSnapshot {
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel", Width: 420, Height: 360})
	return h.snapshotWhere(func(*v1.Node) bool { return true })
}

func (h *harness) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		h.t.Fatal("plugin did not stop")
	}
}

func messageType(line []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &m)
	return m.Type
}

func find(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if n := find(c, id); n != nil {
			return n
		}
	}
	return nil
}

func contains(root *v1.Node, text string) bool {
	if root == nil {
		return false
	}
	if root.Text == text {
		return true
	}
	for _, c := range root.Children {
		if contains(c, text) {
			return true
		}
	}
	return false
}

func TestPickSuggestionAddsZoneAndClearsSearch(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventChange, Text: "syd"})
	s := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "pick:Australia/Sydney") != nil })
	if !contains(s.Root, "Sydney · Australia · +10h") {
		t.Fatal("suggestion title")
	}
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: s.Revision, Node: "pick:Australia/Sydney", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	var params v1.StateSetParams
	_ = json.Unmarshal(call.Params, &params)
	if params.Key != "zones" || !strings.Contains(string(params.Value), `"id":"Australia/Sydney"`) {
		t.Fatalf("saved %s", call.Params)
	}
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
	after := h.snapshotWhere(func(n *v1.Node) bool { return find(n, "drag:Australia/Sydney") != nil })
	if in := find(after.Root, "search"); in.Text != "" || in.Reseed == 0 {
		t.Fatalf("search not cleared: %+v", in)
	}
}

func TestSubmitDuplicateShowsError(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventSubmit, Text: "tokyo"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Tokyo is already in the list") })
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "search", Event: v1.EventSubmit, Text: "qqqq"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, `No zone matches "qqqq"`) })
}

func TestFailedSaveShowsError(t *testing.T) {
	h := start(t, nil)
	p := h.openPanel()
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: p.Revision, Node: "bar:UTC", Event: v1.EventActivate})
	call := h.nextCall(v1.CallStateSet)
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: false, Error: "disk full"})
	h.snapshotWhere(func(n *v1.Node) bool { return contains(n, "Couldn't save zones") })
}

func TestStoredEmptyListStaysEmpty(t *testing.T) {
	h := start(t, &v1.StateGetResult{Found: true, Value: json.RawMessage(`[]`)})
	p := h.openPanel()
	if !contains(p.Root, "No zones yet. Search for a city above.") {
		t.Fatal("empty list was reseeded")
	}
}

func TestUndecodableStateShowsDefaultsAndIsNotWritten(t *testing.T) {
	h := start(t, &v1.StateGetResult{Found: true, Value: json.RawMessage(`{"not":"a list"}`)})
	p := h.openPanel()
	if find(p.Root, "drag:Asia/Tokyo") == nil {
		t.Fatal("defaults not shown")
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	for line := range h.lines {
		var call v1.HostCall
		if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil && call.Call == v1.CallStateSet {
			t.Fatal("undecodable state was overwritten without a user change")
		}
	}
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
}

func TestSettingsSwitchBarMode(t *testing.T) {
	h := start(t, nil)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar"})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil && find(n, "open").Text == "UTC 12:00" })
	h.send(v1.SettingsChanged{Type: "settings.changed", Scope: v1.ScopePlugin, Values: map[string]any{"bar_mode": "icon"}})
	h.snapshotWhere(func(n *v1.Node) bool { return find(n, "open") != nil && find(n, "open").Icon == "public" })
}

func TestSettingsApplyIgnoresBadValues(t *testing.T) {
	s := defaultSettings()
	s.apply(map[string]any{"hour24": "yes", "bar_mode": "sideways", "cycle_seconds": "fast"})
	if s != defaultSettings() {
		t.Fatalf("bad values changed settings: %+v", s)
	}
	s.apply(map[string]any{"cycle_seconds": 500.0})
	if s.cycle != 120*time.Second {
		t.Fatalf("cycle not clamped: %v", s.cycle)
	}
	s.apply(map[string]any{"cycle_seconds": 1.0, "hour24": false, "bar_mode": "all"})
	if s.cycle != 3*time.Second || s.hour24 || s.mode != worldclock.BarAll {
		t.Fatalf("settings = %+v", s)
	}
}

func TestNextMinute(t *testing.T) {
	if got := nextMinute(time.Date(2026, 1, 1, 10, 0, 45, 0, time.UTC)); got != 15*time.Second {
		t.Fatalf("next minute = %v", got)
	}
	if got := nextMinute(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)); got != time.Minute {
		t.Fatalf("on the boundary = %v", got)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./cmd/sysc-plugin-world-clock/`
Expected: FAIL to compile — `undefined: runPlugin`, `environment`, `defaultSettings`, `nextMinute`.

- [ ] **Step 4: Implement** — rewrite `cmd/sysc-plugin-world-clock/main.go`

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	worldclock "github.com/Nomadcxx/sysc-plugins/plugins/world-clock"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const maxSuggestions = 5

func main() {
	env := environment{now: time.Now, local: time.Local, index: worldclock.OpenSystemIndex("/usr/share/zoneinfo")}
	if err := runPlugin(os.Stdin, os.Stdout, env); err != nil {
		os.Exit(1)
	}
}

type environment struct {
	now   func() time.Time
	local *time.Location
	index *worldclock.Index
}

type settings struct {
	hour24 bool
	mode   worldclock.BarMode
	cycle  time.Duration
}

func defaultSettings() settings {
	return settings{hour24: true, mode: worldclock.BarPrimary, cycle: 15 * time.Second}
}

// apply takes the values the host sent; a missing or mistyped key keeps its
// current value, and cycle_seconds is clamped to the manifest's 3–120.
func (s *settings) apply(values map[string]any) {
	if v, ok := values["hour24"].(bool); ok {
		s.hour24 = v
	}
	if v, ok := values["bar_mode"].(string); ok {
		if m, ok := worldclock.ParseBarMode(v); ok {
			s.mode = m
		}
	}
	if v, ok := values["cycle_seconds"].(float64); ok {
		v = min(max(v, 3), 120)
		s.cycle = time.Duration(v) * time.Second
	}
}

// nextMinute is the wait until the next wall-clock minute boundary.
func nextMinute(now time.Time) time.Duration {
	return now.Truncate(time.Minute).Add(time.Minute).Sub(now)
}

type view struct {
	kind v1.ViewKind
	rev  uint64
}

type session struct {
	env         environment
	client      *v1.Client
	store       *worldclock.Store
	settings    settings
	views       map[string]view
	query       string
	queryReseed uint64
	renameDraft string
	renameGen   uint64
	addErr      string
	saveErr     string
	cycle       int
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.world-clock", Name: "World Clock", Version: "2.0.0"})); err != nil {
		return err
	}
	s := &session{env: env, client: c, store: worldclock.NewStore(), settings: defaultSettings(), views: map[string]view{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			incoming <- msg
		}
	}()
	s.restore(ctx)

	minute := time.NewTimer(nextMinute(env.now()))
	defer minute.Stop()
	var cycle *time.Ticker
	var cycleC <-chan time.Time
	resetCycle := func() {
		if cycle != nil {
			cycle.Stop()
			cycle, cycleC = nil, nil
		}
		if s.settings.mode == worldclock.BarCycle {
			cycle = time.NewTicker(s.settings.cycle)
			cycleC = cycle.C
		}
	}
	defer func() {
		if cycle != nil {
			cycle.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-minute.C:
			s.tick()
			minute.Reset(nextMinute(env.now()))
		case <-cycleC:
			s.cycle++
			s.patchBars()
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View}
				s.snapshot(m.ViewID)
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := s.views[m.ViewID]; ok {
					v.rev = 0
					s.views[m.ViewID] = v
					s.snapshot(m.ViewID)
				}
			case *v1.InputEvent:
				s.handle(ctx, m)
				s.snapshotAll()
			case *v1.SettingsChanged:
				s.settings.apply(m.Values)
				s.cycle = 0
				resetCycle()
				s.snapshotAll()
			}
		}
	}
}

func (s *session) restore(ctx context.Context) {
	reply, err := s.client.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "zones"})
	if err != nil || !reply.OK {
		return
	}
	var result v1.StateGetResult
	if json.Unmarshal(reply.Result, &result) != nil || !result.Found {
		return
	}
	// An undecodable value keeps the defaults in memory; nothing is written
	// until the user changes something, so the stored value is not clobbered.
	if zones, err := worldclock.Decode(result.Value); err == nil {
		s.store.Load(zones)
	}
}

func (s *session) save(ctx context.Context) {
	raw, err := worldclock.Encode(s.store.Zones())
	if err == nil {
		var reply v1.HostReply
		reply, err = s.client.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "zones", Value: raw})
		if err == nil && !reply.OK {
			err = errors.New(reply.Error)
		}
	}
	if err != nil {
		s.saveErr = "Couldn't save zones"
		return
	}
	s.saveErr = ""
}

func (s *session) readings() []worldclock.Reading {
	return s.store.Readings(s.env.now(), s.env.local, s.settings.hour24)
}

func (s *session) suggestions() []worldclock.Suggestion {
	if strings.TrimSpace(s.query) == "" {
		return nil
	}
	var out []worldclock.Suggestion
	for _, m := range s.env.index.Search(s.query, maxSuggestions*2) {
		if s.store.Has(m.ID) {
			continue
		}
		rel := ""
		if r, err := worldclock.Read(worldclock.Zone{ID: m.ID}, s.env.now(), s.env.local, s.settings.hour24); err == nil {
			rel = r.Relative
		}
		out = append(out, worldclock.Suggestion{ID: m.ID, Title: worldclock.SuggestionTitle(m, rel)})
		if len(out) == maxSuggestions {
			break
		}
	}
	return out
}

func (s *session) panelState(readings []worldclock.Reading) worldclock.PanelState {
	errText := s.saveErr
	if errText == "" {
		errText = s.addErr
	}
	notice := ""
	if s.env.index.Limited() {
		notice = "Limited search: tz tables not found"
	}
	return worldclock.PanelState{
		Readings: readings, Query: s.query, QueryReseed: s.queryReseed, Suggestions: s.suggestions(),
		PendingDelete: s.store.PendingDelete(), Renaming: s.store.Renaming(),
		RenameDraft: s.renameDraft, RenameReseed: s.renameGen, Error: errText, Notice: notice,
	}
}

func (s *session) tree(kind v1.ViewKind, readings []worldclock.Reading) *v1.Node {
	onBar := worldclock.OnBar(readings)
	switch kind {
	case v1.ViewBar:
		return worldclock.Bar(s.settings.mode, onBar, s.cycle)
	case v1.ViewTooltip:
		return worldclock.Tooltip(onBar)
	}
	return worldclock.Panel(s.panelState(readings))
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	v.rev++
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, s.tree(v.kind, s.readings()))
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}

func (s *session) patch(id string, repl []v1.Replacement) {
	v := s.views[id]
	if v.rev == 0 {
		s.snapshot(id)
		return
	}
	if err := s.client.Patch(id, v.rev, v.rev+1, repl); err != nil {
		return
	}
	v.rev++
	s.views[id] = v
}

// tick is the minute update: keyed patches where the tree shape is stable,
// snapshots where it is not (tooltips, panels showing suggestions).
func (s *session) tick() {
	readings := s.readings()
	for id, v := range s.views {
		switch {
		case v.kind == v1.ViewBar:
			s.patch(id, []v1.Replacement{{Key: "bar", Node: worldclock.BarButton(s.settings.mode, worldclock.OnBar(readings), s.cycle)}})
		case v.kind == v1.ViewPanel && s.query == "":
			s.patch(id, worldclock.PanelPatch(readings, s.store.Renaming()))
		default:
			s.snapshot(id)
		}
	}
}

func (s *session) patchBars() {
	onBar := worldclock.OnBar(s.readings())
	for id, v := range s.views {
		if v.kind == v1.ViewBar {
			s.patch(id, []v1.Replacement{{Key: "bar", Node: worldclock.BarButton(s.settings.mode, onBar, s.cycle)}})
		}
	}
}

func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	node := m.Node
	id := node[strings.Index(node, ":")+1:]
	switch {
	case node == "open":
		_, _ = s.client.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
	case node == "search":
		s.query, s.addErr = m.Text, ""
		if m.Event == v1.EventSubmit {
			s.addTop(ctx)
		}
	case node == "add":
		s.addTop(ctx)
	case strings.HasPrefix(node, "pick:"):
		s.addPick(ctx, id)
	case strings.HasPrefix(node, "drop:") && m.Event == v1.EventDrop:
		if at, err := strconv.Atoi(id); err == nil && s.store.Reorder(m.Text, at) {
			s.save(ctx)
		}
	case strings.HasPrefix(node, "bar:"):
		if s.store.ToggleBar(id) {
			s.cycle = 0
			s.save(ctx)
		}
	case strings.HasPrefix(node, "edit:"):
		s.store.StartRename(id)
		s.renameDraft = s.store.Label(id)
		s.renameGen++
	case strings.HasPrefix(node, "label:"):
		s.renameDraft = m.Text
		if m.Event == v1.EventSubmit {
			s.commitRename(ctx)
		}
	case node == "rename-ok":
		s.commitRename(ctx)
	case node == "rename-cancel", node == "del-cancel":
		s.store.CancelEdit()
	case strings.HasPrefix(node, "del:"):
		s.store.ProposeDelete(id)
	case node == "del-ok":
		if s.store.ConfirmDelete() {
			s.save(ctx)
		}
	}
}

func (s *session) addTop(ctx context.Context) {
	q := strings.TrimSpace(s.query)
	if q == "" {
		return
	}
	top := s.env.index.Search(q, 1)
	if len(top) == 0 {
		s.addErr = fmt.Sprintf("No zone matches %q", q)
		return
	}
	s.add(ctx, top[0])
}

func (s *session) addPick(ctx context.Context, id string) {
	for _, m := range s.env.index.Search(s.query, maxSuggestions*2) {
		if m.ID == id {
			s.add(ctx, m)
			return
		}
	}
	s.add(ctx, worldclock.Match{ID: id, City: worldclock.ShortLabel(id)})
}

func (s *session) add(ctx context.Context, m worldclock.Match) {
	label := ""
	if m.Alias {
		label = m.City
	}
	switch err := s.store.Add(m.ID, label); {
	case errors.Is(err, worldclock.ErrDuplicate):
		s.addErr = m.City + " is already in the list"
	case err != nil:
		s.addErr = fmt.Sprintf("No zone matches %q", strings.TrimSpace(s.query))
	default:
		s.query, s.addErr = "", ""
		s.queryReseed++
		s.save(ctx)
	}
}

func (s *session) commitRename(ctx context.Context) {
	if id := s.store.Renaming(); id != "" && s.store.Rename(id, s.renameDraft) {
		s.save(ctx)
	}
}
```

If `v1.PanelParams` has no `Instance` field at the pinned version, drop that field (the old code set it, so it exists at the old pin; confirm with `go doc github.com/Nomadcxx/sysc-shell/plugin/v1 PanelParams`).

- [ ] **Step 5: Update the manifest** — replace `plugins/world-clock/manifest.json` with:

```json
{
  "schema": 1,
  "id": "org.sysc.world-clock",
  "name": "World Clock",
  "version": "2.0.0",
  "protocol": {
    "major": 1,
    "minor": 7
  },
  "exec": "bin/sysc-plugin-world-clock",
  "capabilities": [
    "panels",
    "settings",
    "state"
  ],
  "requires": {
    "commands": []
  },
  "services": [
    {
      "id": "world-clock"
    }
  ],
  "widgets": [
    {
      "id": "bar",
      "settings": []
    }
  ],
  "panels": [
    {
      "id": "panel",
      "width": 420,
      "height": 360,
      "placement": "attached"
    }
  ],
  "settings": [
    {"key": "hour24", "type": "bool", "label": "24-hour clock", "default": true},
    {"key": "bar_mode", "type": "select", "label": "Bar shows", "default": "primary",
      "options": [
        {"value": "icon", "label": "Globe only"},
        {"value": "primary", "label": "First clock"},
        {"value": "all", "label": "All bar clocks"},
        {"value": "cycle", "label": "Cycle through clocks"}
      ]},
    {"key": "cycle_seconds", "type": "int", "label": "Cycle interval (seconds)", "default": 15, "min": 3, "max": 120}
  ]
}
```

- [ ] **Step 6: Run the full suite**

Run: `go vet ./plugins/world-clock/ ./cmd/sysc-plugin-world-clock/ && go test ./... && make validate`
Expected: PASS. (`make validate` checks manifests; if the Makefile target is named differently, run `make` with no args to list and pick the manifest validation target.)

- [ ] **Step 7: Update the README row** — `README.md:33`:

```
| World Clock | `org.sysc.world-clock` | 2.0.0 redesign | city search, labels, bar modes; see docs/plans/2026-09-25-world-clock-design.md |
```

Match the column count of the surrounding rows; if the table has a different number of columns, keep its columns and only change the status and notes cells.

- [ ] **Step 8: Commit**

```bash
git add cmd/sysc-plugin-world-clock plugins/world-clock README.md
git commit -m "feat(world-clock): search-first loop, minute ticks, and 2.0.0 manifest"
```

---

### Task 8: Live acceptance

**Files:** none (evidence only; screenshots go to the scratchpad).

- [ ] **Step 1: Build and install**

Run: `cd ~/sysc-plugins && make install`
Expected: `Installed 10 plugins into ~/.config/sysc-shell/plugins`.

- [ ] **Step 2: Reload the running shell against the pinned sysc-shell build**

The running shell must include Task 1's glyphs. Ask the user how they restart their sysc-shell session (earlier plugin passes used a live compositor round); do not kill the session unasked.

- [ ] **Step 3: Exercise and screenshot**

With the user or via the repo's usual screenshot tool (`grim`), capture into the scratchpad:
1. Panel with default zones (cards, sun/moon glyphs, +1 marker on Tokyo when applicable).
2. Typing `mum` → `Mumbai` alias suggestion showing `Mumbai · +Nh`; clicking it adds `Asia/Kolkata` labelled `Mumbai` and clears the input.
3. Submitting `tokyo` → `Tokyo is already in the list`.
4. Rename via pencil, then delete confirm on a row.
5. Bar in `icon`, `primary`, `all`, `cycle` (wait one cycle).
6. Drag a card to a new gap; reopen the panel after a plugin restart to confirm order, labels, and bar visibility persisted.

View each PNG with the Read tool and compare against the spec. The drag grip glyph `≡` must render; if it shows a missing-glyph box, change it to `=` in `panel.go`, re-run Task 5 tests, and commit `fix(world-clock): use a grip the bar font carries`.

- [ ] **Step 4: Record**

Report what was verified and any deviations to the user. Do not mark acceptance complete on test results alone.
