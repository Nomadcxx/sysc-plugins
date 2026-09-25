# World Clock redesign

Status: approved in brainstorm, 2026-09-25.

## Goal

A world clock that exceeds both references — the Noctalia official plugin
(`noctalia-dev/official-plugins` @ `f36c62a`, `world_clock/`) and the DMS plugin
(`rochacbruno/WorldClock` @ `f10e1f9`) — on panel and bar, while keeping the
existing zone list, drag reorder, and host state-store persistence.

The primary use is glancing at what time it is for people and servers in other
places, so at-a-glance legibility (named zones, relative-to-local offset, day
marker) outranks configuration depth.

## Audit of v1.2.0 (the starting point)

The plugin is already extracted (`plugins/world-clock`, `cmd/sysc-plugin-world-clock`,
moved from sysc-shell in `1dddfc8`). Its views pass `plugin/lint` at the declared
box. Findings:

Parity gaps:

- No custom labels (Noctalia pencil, DMS label field).
- No per-zone bar visibility (both references have an eye toggle).
- The bar shows the first zone's time with no label; with defaults, that is an
  unlabelled UTC time. Noctalia offers glyph or all visible clocks; DMS offers
  all, cycling, or icon only.
- No day marker (DMS `+1` / `-1`).
- Remove is a text button whose confirmation renders at the foot of the list,
  not inline on the row.
- Drop zones are trailing children of each row instead of gaps between rows.

Defects:

1. Adding takes two steps (`Add X?` → Add); neither reference confirms an add.
2. Invalid and duplicate input is discarded silently (`main.go` ignores the
   `ProposeAdd` error).
3. The input keeps its text after an add: `draft` resets but no `Reseed` is sent,
   and the host owns the buffer.
4. Input must be an exact, case-sensitive IANA name (`tokyo` fails).
5. Every view is patched every second although only minutes are displayed.
6. An emptied list restores as `UTC`; `state.set` failures are ignored.
7. The tooltip shows one zone; the input has no placeholder.
8. Repo-wide, not world-clock: `228113c` bumped the sysc-shell pin in `go.mod`
   and dropped its `go.sum` lines, so HEAD does not build. `go mod tidy` fixes
   it (the pinned commit is on `origin/main`).

## Scope

In: parity gaps above, every defect above, city search, relative-to-local
offset with day/night cue, bar cycling.

Out: a time scrubber / meeting planner; IPC commands (sysc has no plugin IPC
surface); a shell combobox primitive.

## Architecture

One plugin process, which is already the service. The current `clock.go` is
split by responsibility and deleted:

| File | Owns |
|---|---|
| `plugins/world-clock/zones.go` | The ordered zone list: `Zone{ID, Label, OnBar}`; add, rename, toggle bar, reorder, remove, pending delete; state encode/decode and migration. Mutex-guarded as today. |
| `plugins/world-clock/search.go` | The search index and `Search(query) []Match`. |
| `plugins/world-clock/reading.go` | Pure formatting of one zone at one instant against the local zone. |
| `plugins/world-clock/panel.go` | Panel tree and the minute patch for panels. |
| `plugins/world-clock/bar.go` | Bar modes, the bar button, and the tooltip tree. |
| `cmd/sysc-plugin-world-clock/main.go` | Event loop, settings, persistence calls, tick scheduling. |

No wire-protocol change. The manifest moves to protocol minor **7** (the host's
current maximum, `internal/plugin/protocol.go`); `Placeholder` and `Reseed` are
not minor-gated. Manifest version becomes `2.0.0` because the stored shape
changes.

### sysc-shell change (lands first)

Add material `public` (globe) and `edit` (pencil) to the icon subset, following
`59abfd7`: append to `ICONS` in `internal/render/icons/material/build.py`,
rebuild `material-symbols-rounded.ttf` from the pinned, SHA-verified upstream,
register both names in `internal/render/materialfont.go`, extend
`materialfont_test.go`, and record the new artefact hash in `SOURCE.md`. Then
bump the sysc-plugins pin and run `go mod tidy`, which also repairs `go.sum`.

## Data model and persistence

- State key stays `zones`. New value: `[{"id":"Asia/Tokyo","label":"","on_bar":true}, …]`.
- Decode accepts the legacy `["UTC", …]` shape (label `""`, on_bar `true`) and
  the object shape. Entries that fail `time.LoadLocation`, are empty, or
  duplicate an earlier id are dropped.
- Key not found → seed the Noctalia defaults (`UTC`, `America/New_York`,
  `Europe/Berlin`, `Asia/Tokyo`). A stored empty list stays empty.
- Value present but undecodable → run on defaults in memory and do not write
  until the user changes something, so corrupt data is never silently overwritten.
- Display label = `Label` if non-empty, else the short name (`America/New_York`
  → `New York`).
- Every mutation saves. A failed `state.set` sets a panel error line
  (`Couldn't save zones`) that clears on the next successful save.

## Search

Index built once at startup:

- `/usr/share/zoneinfo/zone.tab`: one line per country with its zone id; the
  city is the id's last path segment with underscores as spaces. `zone.tab`,
  not `zone1970.tab`, because the latter folds capitals such as `Europe/Oslo`
  into links and would make "Oslo" unsearchable.
- `/usr/share/zoneinfo/tzdata.zi` link lines (`L target name`): names accepted
  as exact ids, case-insensitively (e.g. `us/eastern`).
- `/usr/share/zoneinfo/iso3166.tab`: country code → country name.
- An embedded alias table (~40 entries) for major cities that are not zone
  names, e.g. San Francisco / Seattle → `America/Los_Angeles`, Mumbai / Delhi /
  Bangalore → `Asia/Kolkata`, Beijing → `Asia/Shanghai`, Washington / Boston /
  Miami → `America/New_York`. Also `UTC` and `GMT`.

`Search(query)` is case-insensitive on a trimmed query and folds Latin accents
(`Réunion` matches `reunion`) through a small built-in rune table; no new
dependency. Ranking:
exact city or alias → city prefix → alias prefix → country name → substring of
zone id. Results are de-duplicated by zone id, already-added zones are
excluded, and at most 5 are returned. A `Match` carries the zone id, the
display city (the alias when an alias matched), the country name, and the
relative offset text.

An exact id (a `zone.tab` id or a `tzdata.zi` link name) matches
case-insensitively and ranks first; any other string containing `/` that
`time.LoadLocation` accepts verbatim is also accepted.

If the tables are missing, the index holds only aliases and the exact-id
fallback, and the panel shows the subtle line `Limited search: tz tables not found`.

## Reading

For zone `z`, instant `now`, and `time.Local`:

- `Clock`: `15:04` or `3:04 PM` per `hour24`.
- `Offset`: `UTC+9`, `UTC-3:30`, `UTC+0` (unchanged format).
- `Relative`: difference of the two UTC offsets at `now`: `Same time`, `+9h`,
  `−3h30` (U+2212 minus).
- `DayShift`: `+1`, `-1`, or `0`, comparing calendar dates of `now` in `z` and
  in local. Cards and the bar render it as a compact `+1` / `−1` marker (the
  DMS convention, which fits the card's time column); the tooltip spells it
  `tomorrow` / `yesterday`.
- `Daytime`: true when the zone's local hour is in [6, 18).

All are computed from explicit arguments so tests pin both the zone and "local".

## Panel (420 × 360, attached)

Top to bottom:

1. Title row: `World Clock` (title size).
2. Search row: text input (`Placeholder: "Search a city or country"`, `Reseed`
   generation, change + submit events) and an accent `add` icon button that
   adds the top match.
3. While the query is non-empty, suggestions occupy the zone list's slot: up
   to 5 suggestion chips, each a button
   `Tokyo · Japan · +9h`. Clicking one, or submitting (adds the top match), adds
   the zone immediately, clears the query via a new `Reseed`, and uses the alias
   as the label when an alias matched. No matches → subtle `No matching zone`.
   Duplicate or invalid submit → error-tone line with the reason
   (`Tokyo is already in the list`, `No zone matches "xyz"`).
4. Scrolling list. A drop zone sits in each gap (before row 0, between rows,
   after the last). Each zone card (`card` fill, radius 10):
   - drag source with a `≡` text grip (drag sources carry text, not icons);
   - label (bold) over `Asia/Tokyo · UTC+9` (subtle, key `meta:<id>`);
   - right column (key `clock:<id>`): time (accent, bold, tabular) with the
     day marker, over the relative offset `+9h` (subtle);
   - `sunny` / `bedtime` icon (key `sky:<id>`);
   - actions: `visibility`/`visibility_off`, `edit`, `delete`.
   - Hidden-from-bar cards render label and time in subtle tone.
   - Renaming: the label column becomes a text input seeded with the current
     label (empty = short name); actions become `check` / `close`; submit saves.
   - Confirming delete: actions become `check` (error fill) / `close`, on the row.
   - Renaming and confirming delete are mutually exclusive, and both clear when
     their zone disappears.
5. Empty state: `No zones yet. Search for a city above.`
6. Error/notice lines (save failure, limited search) render under the search row.

## Bar and tooltip

Settings (`manifest.json` top-level `settings`):

| Key | Type | Default | Notes |
|---|---|---|---|
| `hour24` | bool | true | Kept. |
| `bar_mode` | select | `primary` | `icon`, `primary`, `all`, `cycle`. |
| `cycle_seconds` | int | 15 | 3–120; used only by `cycle`. |

The bar is one `open` button (whole control opens the panel), content by mode:

- `icon`: `public` glyph.
- `primary`: first on-bar zone, `Tokyo 21:04`.
- `all`: every on-bar zone, `Tokyo 21:04 · Berlin 14:04`. Width is budgeted by
  lint at 240 × 32; entries that do not fit are dropped from the end.
- `cycle`: one on-bar zone at a time, advancing every `cycle_seconds`; the index
  wraps and resets when the on-bar set changes.
- No on-bar zones in any text mode → `public` glyph.
- A `+1`/`-1` suffix is appended when the zone's date differs from local.

Tooltip: `World Clock` then one line per on-bar zone, `Tokyo 21:04 +9h`.

## Ticking

The loop replaces the one-second ticker with a timer armed for the next minute
boundary (re-armed after each fire). Bars are patched at key `bar`; panels
showing zone cards are patched at `clock:`, `meta:`, and `sky:` keys; panels
showing suggestions and tooltips are re-snapshotted. In `cycle` mode a second ticker at
`cycle_seconds` advances the index and re-snapshots bars only. A settings
change re-arms both.

## Error handling summary

| Case | Behavior |
|---|---|
| Invalid or duplicate add | Inline error line; nothing is added; query kept. |
| `state.set` fails | Inline `Couldn't save zones`; in-memory list kept. |
| Stored value undecodable | Defaults in memory, no write until a user change. |
| Stored zone no longer valid | Dropped on load. |
| tz tables missing | Alias + exact-id search, subtle notice. |

## Testing

Unit (plugin package):

- Decode: legacy strings, objects, junk, duplicates, empty list stays empty,
  missing key seeds defaults, undecodable value flags no-write.
- Mutations: add, rename (empty resets), toggle bar, reorder at every insertion
  index, pending delete confirm/cancel.
- Search against fixture tables in `testdata/`: ranking order, alias label,
  country match, diacritics, cap of 5, excluding added zones, exact-id fallback,
  missing-table degradation.
- Reading with fixed instants and zones: `+9h`, `−3h30`, `Same time`,
  Tomorrow/Yesterday across midnight, a DST transition, daytime bounds.
- Bar: every mode, no on-bar zones, cycle wrap and reset.
- Views: `plugin/lint.Tree` for the panel at 420 × 360 in empty, error,
  suggestions, renaming, confirming-delete, and 8-zone states; the bar at
  240 × 32 in every mode; the tooltip at 280 × 200.

Loop (`cmd` package, fake client): suggestion click adds and bumps `Reseed`;
failed save surfaces the error; settings change switches bar mode; minute patch
touches only keyed nodes.

Acceptance: `make install`, reload the shell, screenshot the panel (default,
suggestions, rename, delete confirm) and the bar in each mode, and view the PNGs.
