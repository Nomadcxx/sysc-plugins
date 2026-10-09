# Plugin panel captures: backfill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every one of the 16 plugins has a fixture-driven capture scene, a committed `screenshot.png` and `thumbnail.webp`, and no plugin is grandfathered any more.

**Architecture:** Thirteen new `capture_test.go` scenes (the three pilots are already on `main`) call each plugin's real panel builder with invented state through `capture.Panel`. Scenes that need pictures (device mockup, provider logos, game covers) get them from the plugin's own shipped assets or from a new `capture.Gradient` helper that writes a synthetic PNG into the test's temp directory. One run of `make captures` writes all sixteen screenshots and thumbnails, and the same PR deletes `tools/thumbnail/grandfathered.txt`, because the validator forbids a listed plugin from having those files.

**Tech Stack:** Go; `internal/capture` (merged in #143); `sysc-panel-preview` from sysc-shell `main`; `tools/thumbnail`.

**Spec:** `docs/superpowers/specs/2026-10-09-plugin-panel-captures-design.md`, delivery step 3 ("backfill"). Prior plan: `docs/superpowers/plans/2026-10-09-plugin-panel-captures-pilot.md`. Every scene below was prototyped and rendered against the real command on 2026-10-09 before this plan was written, and the images were inspected.

## Global Constraints

- **Fictional data only.** The repository is public: invented names, titles, numbers, hosts and repositories; never real accounts, devices, paths or message text. Documentation addresses (`203.0.113.0/24`) for IPs. Product and tool names a plugin integrates with (Claude, Codex, Copilot, Proton VPN, Docker, KDE Connect) are fine; usage figures, accounts and servers are invented.
- Scenes call each plugin's **real** panel builder with its real state types; never hand-built wire nodes.
- A scene does nothing unless `CAPTURE=1`; a normal `go test` must write no files.
- Capture size is the first panel's `width`×`height` from the plugin's own `manifest.json`; the command's default scale (150%) applies.
- No `go.mod` / `go.sum` change.
- Unlike the pilot PR, this PR **does** commit the generated `screenshot.png` and `thumbnail.webp` for all 16 plugins and deletes `tools/thumbnail/grandfathered.txt` in the same commit. The validator fails if a listed plugin has the files, and fails a plugin without them once it is unlisted, so the images and the list change together.
- Generate thumbnails on **amd64** (the desktop is), because `go run ./tools/thumbnail -check` compares bytes and CI is amd64.
- Go commands must be capped on this machine: `go test -count=1 -p 2 <one package>`, one package per command; never a whole-repo run (a hook blocks it).
- Commit messages contain **no** AI attribution (the repo's hook rejects it, and words containing "bot"/"agent"/"claude"). The bd pre-commit hook fails in fresh worktrees: commit with a scratch DB: `cp /home/nomadx/sysc-plugins/.beads/beads.db "$SCRATCH/beads.db" && sqlite3 "$SCRATCH/beads.db" 'delete from dirty_issues;'` then `BEADS_DB="$SCRATCH/beads.db" git commit ...`.
- Work in a worktree off `origin/main`: `git worktree add -b feat/plugin-captures-backfill ~/worktrees/sysc-plugins-backfill-impl origin/main`. Paths below are relative to it. This plan lives on `docs/plugin-captures-backfill-plan`; cherry-pick its commit onto the implementation branch if the plan should be in the PR.
- `gofmt -l .` prints nothing before each commit.
- **Prerequisite:** `sysc-panel-preview` built from sysc-shell `origin/main` in a worktree that is not `~/sysc-shell` (that checkout is stale): `cd <worktree of sysc-shell origin/main> && go build -o "$SCRATCH/bin/sysc-panel-preview" ./cmd/sysc-panel-preview`, then `export SYSC_PANEL_PREVIEW="$SCRATCH/bin/sysc-panel-preview"`.

## Review Focus

1. **A scene whose artwork is missing renders silently blank or as a placeholder:** KDE Connect's device mockup, AI Usage's provider logos and Games' covers load files that the scene must supply. Tasks 2 and 3 name each source and the visual check looks for the pictures.
2. **A scene leaks a package-level override into other tests:** AI Usage replaces `providerLogos` and KDE Connect replaces `mockupAssetDir`. Each restores it in `t.Cleanup`; Task 3 runs the whole package's tests with the scene present.
3. **A real identifier ends up in a public image:** scenes must contain no `/home/`, user name, email or real device name. Task 4 greps every scene before committing images.
4. **Images and the grandfather list change out of step:** Task 4 deletes the list, regenerates, and runs `validate-manifests` and `thumbnail -check` before the commit.
5. **A normal `go test` writes files:** Tasks 2 and 3 check `git status` after a plain run of each new scene package.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/capture/image.go` | `Gradient`: synthetic PNG in the test's temp directory |
| `internal/capture/capture_test.go` | one added test for `Gradient` |
| `plugins/<dir>/capture_test.go` (cat, faith, screenshot, screen-recorder, wallpaper-depth, mini-docker, protonvpn, notes, moonbit, calendar, kdeconnect, aiusage) and `cmd/sysc-plugin-games/capture_test.go` | the thirteen scenes |
| `plugins/*/screenshot.png`, `plugins/*/thumbnail.webp` | generated, 16 plugins |
| `tools/thumbnail/grandfathered.txt` | deleted |
| `docs/publishing.md` | drop the paragraph about the grandfather list |

---

### Task 1: `capture.Gradient`

**Files:**
- Create: `internal/capture/image.go`
- Modify: `internal/capture/capture_test.go`

**Interfaces:**
- Produces: `func Gradient(t testing.TB, w, h int, from, to color.NRGBA) string`, the path of a w×h PNG written to `t.TempDir()`. Task 3 uses it for KDE Connect's photos and Games' covers.

- [ ] **Step 1: Write the failing test**

In `internal/capture/capture_test.go`, add `"image/color"` to the import block (after `"image"`), and append:

```go
func TestGradientWritesADecodablePNGOfTheAskedSize(t *testing.T) {
	path := Gradient(t, 40, 30, color.NRGBA{R: 200, A: 255}, color.NRGBA{B: 200, A: 255})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 30 {
		t.Fatalf("size = %v, want 40x30", b)
	}
	topLeft, bottomRight := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA), color.NRGBAModel.Convert(img.At(39, 29)).(color.NRGBA)
	if topLeft.R <= bottomRight.R || topLeft.B >= bottomRight.B {
		t.Errorf("corners %v and %v do not run from the first colour to the second", topLeft, bottomRight)
	}
	if !strings.HasPrefix(path, os.TempDir()) {
		t.Errorf("wrote %s outside the temp directory", path)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -count=1 -p 2 ./internal/capture/ 2>&1 | head -4`
Expected: FAIL to build, `undefined: Gradient`.

- [ ] **Step 3: Implement**

Create `internal/capture/image.go`:

```go
package capture

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Gradient writes a w by h PNG that fades diagonally from one colour to the
// other into the test's temp directory and returns its path. Scenes use it for
// the pictures a panel shows (photo thumbnails, cover art), so no artwork is
// committed and no real picture leaks into a public screenshot.
func Gradient(t testing.TB, w, h int, from, to color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	span := max(w+h-2, 1)
	mix := func(a, b uint8, step int) uint8 { return uint8((int(a)*(span-step) + int(b)*step) / span) }
	for y := range h {
		for x := range w {
			s := x + y
			img.SetNRGBA(x, y, color.NRGBA{mix(from.R, to.R, s), mix(from.G, to.G, s), mix(from.B, to.B, s), 255})
		}
	}
	path := filepath.Join(t.TempDir(), "gradient-"+strconv.Itoa(w)+"x"+strconv.Itoa(h)+".png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `gofmt -l internal; go vet -p 2 ./internal/capture/ && go test -count=1 -p 2 ./internal/capture/`
Expected: no gofmt output; `ok`. Then `go test -race -count=1 -p 2 ./internal/capture/` also `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/capture
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(capture): synthetic gradient images for scenes that show pictures"
```

---

### Task 2: Scenes that need no image files (ten plugins)

**Files:** Create a `capture_test.go` in each of `plugins/cat`, `plugins/faith`, `plugins/screenshot`, `plugins/screen-recorder`, `plugins/wallpaper-depth`, `plugins/mini-docker`, `plugins/protonvpn`, `plugins/notes`, `plugins/moonbit`, `plugins/calendar`.

**Interfaces:**
- Consumes: `capture.Panel(t testing.TB, dir string, tree *v1.Node)` (merged).
- Produces: `TestCapturePanel` in each package, which `make captures` finds by the `capture_test.go` file name.

Package names to know: `plugins/screen-recorder` is `package recorder`; `plugins/wallpaper-depth` is `package wallpaperdepth`; `plugins/mini-docker` is `package minidocker`.

- [ ] **Step 1: Write the ten scenes**

Create `plugins/cat/capture_test.go`:

```go
package cat

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/cat/screenshot.png when CAPTURE=1: the real
// cat fed a light, wavering CPU load, with a full history behind the sparkline.
func TestCapturePanel(t *testing.T) {
	s := DefaultSettings()
	c := New(s.Bands, s.NapAfter, 7)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var h History
	for i := 0; i < HistoryLen; i++ {
		load := 0.14 + 0.2*float64(i%12)/12
		c.Observe(load, now)
		h.Push(load)
		now = now.Add(s.SampleEvery)
	}
	capture.Panel(t, "cat", PanelTree(FrameOf(c), s, h.Values(nil)))
}
```

Create `plugins/faith/capture_test.go`:

```go
package faith

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/faith/screenshot.png when CAPTURE=1: Psalm
// 23:1 with three cross-references and both neighbours enabled.
func TestCapturePanel(t *testing.T) {
	var xrefs []Ref
	for _, s := range []string{"ISA 40:11", "JHN 10:11", "REV 7:17"} {
		ref, err := ParseRef(s)
		if err != nil {
			t.Fatal(err)
		}
		xrefs = append(xrefs, ref)
	}
	capture.Panel(t, "faith", PanelTree(PanelModel{
		Ref:         Ref{Book: 18, Chapter: 23, Verse: 1},
		Translation: "WEB",
		Verse:       "The LORD is my shepherd; I shall not want.",
		CanPrev:     true, CanNext: true, CanRead: true,
		Commentary: CommentaryNone,
		Xrefs:      xrefs,
	}))
}
```

Create `plugins/screenshot/capture_test.go`:

```go
package screenshot

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/screenshot/screenshot.png when CAPTURE=1.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "screenshot", PanelTree(Model{Directory: "~/Pictures/Screenshots"}))
}
```

Create `plugins/screen-recorder/capture_test.go`:

```go
package recorder

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/screen-recorder/screenshot.png when
// CAPTURE=1: a recording four minutes in, with the replay buffer enabled.
func TestCapturePanel(t *testing.T) {
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ReplayEnabled = true
	snap := Snapshot{Mode: Recording, Elapsed: 4*time.Minute + 12*time.Second}
	capture.Panel(t, "screen-recorder", PanelTree(snap, cfg, time.Time{}))
}
```

Create `plugins/wallpaper-depth/capture_test.go`:

```go
package wallpaperdepth

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/wallpaper-depth/screenshot.png when
// CAPTURE=1: a ready helper with two outputs, one generated and cached.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "wallpaper-depth", PanelTree(ControllerSnapshot{
		Helper:   HelperStatus{Ready: true, RuntimeReady: true, ModelReady: true},
		Checked:  true,
		Settings: Settings{AutoGenerate: true, Threshold: 55, Feather: 8},
		Rows: []OutputRow{
			{Output: "DP-1", State: "image", WallpaperPath: "/wallpapers/mountains.jpg", Status: "ready", CacheHit: true, ElapsedMs: 840},
			{Output: "HDMI-A-1", State: "image", WallpaperPath: "/wallpapers/harbour.jpg", Status: "ready", ElapsedMs: 2300},
			{Output: "eDP-1", State: "video", Status: "unsupported"},
		},
	}))
}
```

Create `plugins/mini-docker/capture_test.go`:

```go
package minidocker

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/mini-docker/screenshot.png when CAPTURE=1:
// the containers tab with five invented containers, four of them running.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "mini-docker", PanelTreeForSession(SessionSnapshot{
		Scope:        ScopeContainers,
		SelectedID:   "c3f1a9b2c4d5",
		ContainerTab: TabStatus{Available: true, RefreshedAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)},
		Containers: []Container{
			{ID: "a1b2c3d4e5f6", Names: "web-frontend", Image: "acme/frontend:2.4", State: "running", Status: "Up 3 hours"},
			{ID: "b2c3d4e5f6a1", Names: "api-gateway", Image: "acme/gateway:1.9", State: "running", Status: "Up 3 hours"},
			{ID: "c3f1a9b2c4d5", Names: "postgres-db", Image: "postgres:16", State: "running", Status: "Up 2 days"},
			{ID: "d4e5f6a1b2c3", Names: "redis-cache", Image: "redis:7", State: "running", Status: "Up 2 days"},
			{ID: "e5f6a1b2c3d4", Names: "nightly-backup", Image: "acme/backup:0.8", State: "exited", Status: "Exited (0) 9 hours ago"},
		},
	}))
}
```

Create `plugins/protonvpn/capture_test.go`:

```go
package protonvpn

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/protonvpn/screenshot.png when CAPTURE=1: a
// connected tunnel on the Connections tab with a list of exit countries.
func TestCapturePanel(t *testing.T) {
	country := func(code, name string, load int) Country {
		return Country{Code: code, Name: name, Load: load, Servers: []Server{{Name: code + "#1", Country: code, Load: load, Up: true}}}
	}
	capture.Panel(t, "protonvpn", Panel(PanelState{
		Tab: "connections", HasCLI: true, Traffic: true,
		Snap: Snapshot{
			Phase: PhaseConnected, IP: "203.0.113.24",
			Status: Status{Phase: PhaseConnected, Server: "CH#12", Location: "Zurich, Switzerland", Country: "ch", Load: 34, Protocol: "wireguard"},
			RxRate: 2.4e6, TxRate: 310e3,
			Interface: "tun0",
		},
		Conns: ConnectionsState{
			Countries: []Country{
				country("CH", "Switzerland", 34), country("IS", "Iceland", 21), country("JP", "Japan", 58),
				country("DE", "Germany", 47), country("NL", "Netherlands", 62), country("SE", "Sweden", 29),
				country("CA", "Canada", 51), country("AU", "Australia", 44),
			},
		},
	}))
}
```

Create `plugins/notes/capture_test.go`:

```go
package notes

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/notes/screenshot.png when CAPTURE=1: a
// library of invented notes with one pinned, and the packing list open.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	note := func(name, title, preview string, ago time.Duration, favorite bool, words int) Summary {
		return Summary{Name: name, Title: title, Preview: preview, Modified: now.Add(-ago), Favorite: favorite, Words: words}
	}
	body := "# Packing list\n\n- Passport and tickets\n- Phone charger\n- Rain jacket\n- Notebook\n\nLeave for the airport by 07:30."
	capture.Panel(t, "notes", PanelTree(Snapshot{
		Notes: []Summary{
			note("shopping", "Shopping list", "Oat milk, coffee beans, tomatoes", 90*time.Minute, true, 18),
			note("packing", "Packing list", "Passport and tickets, phone charger", 20*time.Minute, false, 22),
			note("book", "Book ideas", "A field guide to local birds", 26*time.Hour, false, 140),
			note("recipes", "Weeknight recipes", "Lentil soup with lemon and herbs", 3*24*time.Hour, false, 260),
			note("trip", "Trip itinerary", "Day 1: arrive, walk the old town", 5*24*time.Hour, false, 310),
			note("meeting", "Meeting notes", "Agree the release date and owners", 8*24*time.Hour, false, 95),
		},
		Selected: "packing", Title: "Packing list", Body: body, Words: 22,
		Modified: now.Add(-20 * time.Minute), Now: now,
	}, false))
}
```

Create `plugins/moonbit/capture_test.go`:

```go
package moonbit

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/moonbit/screenshot.png when CAPTURE=1: a
// finished scan under review, every category with files selected.
func TestCapturePanel(t *testing.T) {
	cats := []CategoryStat{
		{Name: "Pacman Cache", Files: 412, Bytes: 6_400_000_000},
		{Name: "npm Cache", Files: 2310, Bytes: 1_900_000_000},
		{Name: "Journal Logs", Files: 38, Bytes: 820_000_000},
		{Name: "Thumbnail Cache", Files: 5120, Bytes: 310_000_000},
		{Name: "Browser Cache", Files: 1740, Bytes: 640_000_000},
		{Name: "Trash", Files: 0, Bytes: 0},
	}
	s := State{Phase: PhaseReview, ScannedAt: "2026-10-09T12:00:00Z", Selected: map[string]bool{}}
	for _, c := range cats {
		if c.Files > 0 {
			s.Review = append(s.Review, c)
			s.Selected[c.Name] = true
		}
	}
	capture.Panel(t, "moonbit", Panel(&s))
}
```

Create `plugins/calendar/capture_test.go`:

```go
package calendar

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/calendar/screenshot.png when CAPTURE=1:
// October 2026 in month view with the 9th selected and a dozen invented
// events across two calendars.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	sources := []CalendarSource{
		{ID: "work", Name: "Work", Color: "#4f8cff"},
		{ID: "home", Name: "Home", Color: "#e0719a"},
	}
	event := func(id, cal, summary string, day, hour, minutes int) Event {
		start := time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC)
		return Event{ID: id, CalendarID: cal, Calendar: cal, Summary: summary,
			Start: start, End: start.Add(time.Duration(minutes) * time.Minute)}
	}
	events := []Event{
		event("1", "work", "Sprint planning", 5, 10, 60),
		event("2", "home", "Dentist", 7, 8, 45),
		event("3", "work", "Design review", 9, 9, 30),
		event("4", "work", "Team lunch", 9, 12, 60),
		event("5", "home", "Piano lesson", 9, 17, 45),
		event("6", "work", "Release freeze", 12, 14, 60),
		event("7", "home", "Farmers market", 17, 9, 120),
		event("8", "work", "Quarterly review", 20, 13, 90),
		event("9", "home", "Dinner with friends", 24, 19, 120),
		event("10", "work", "Demo day", 28, 15, 60),
	}
	state := PanelState{Date: now, View: ViewMonth, SelectedEventID: "3"}
	capture.Panel(t, "calendar", PanelTree(state, events, "monday", LoadStatus{Available: true, CalendarCount: 2}, now, sources, map[string]bool{}, false))
}
```

- [ ] **Step 2: Verify they compile and are inert**

Run, one package at a time:

```bash
for p in cat faith screenshot screen-recorder wallpaper-depth mini-docker protonvpn notes moonbit calendar; do
  go vet -p 2 ./plugins/$p/ && go test -count=1 -p 2 ./plugins/$p/ 2>&1 | tail -1
done
git status --short | grep -E "screenshot.png|thumbnail.webp"; echo "files written: $?"
```

Expected: ten `ok` lines (the scenes report as skipped) and `files written: 1` (the grep finds nothing).

- [ ] **Step 3: Render each against the real command**

```bash
for p in cat faith screenshot screen-recorder wallpaper-depth mini-docker protonvpn notes moonbit calendar; do
  CAPTURE=1 go test -count=1 -p 2 -run TestCapturePanel -v ./plugins/$p/ 2>&1 | grep -E "wrote|FAIL|lay out"
done
magick montage plugins/{cat,faith,screenshot,screen-recorder,wallpaper-depth}/screenshot.png -tile 5x1 -geometry 420x+8+8 -background '#444' "$SCRATCH/m-a1.png"
magick montage plugins/{mini-docker,protonvpn,notes,moonbit,calendar}/screenshot.png -tile 5x1 -geometry 420x+8+8 -background '#444' "$SCRATCH/m-a2.png"
```

Expected: ten `wrote .../screenshot.png` lines and no `FAIL`. Open both montages. Expected pictures: Cat strolling (blue cat, CPU meter, sparkline); Faith "Psalm 23:1" with a "See also" row of three references; Screenshot's four buttons; Screen Recorder "Recording 04:12" with Record/Stop/Replay buttons; Wallpaper Depth with three outputs; Docker containers list with five rows; Proton VPN "Protected" with a country list and flags; Notes with a library and the packing list open; Moonbit's review list with five checked categories; Calendar October 2026 with the 9th selected and three events. If a scene fails with `does not lay out`, the message names the node and size: shorten the fixture text or drop a row; never change the helper.

Known, accepted: the Screen Recorder panel is sparse and small-set by design, the Moonbit footer shows "Schedule unknown" (no schedule state is fed), and Notes' and Moonbit's wide panels shrink on the card. These are the plugins' real UIs.

- [ ] **Step 4: Remove the generated files and commit**

```bash
rm -f plugins/*/screenshot.png
git status --short
git add plugins/cat plugins/faith plugins/screenshot plugins/screen-recorder plugins/wallpaper-depth plugins/mini-docker plugins/protonvpn plugins/notes plugins/moonbit plugins/calendar
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(capture): scenes for ten more plugins"
```

Expected from `git status`: only the ten `capture_test.go` files.

---

### Task 3: Scenes that need artwork (KDE Connect, AI Usage, Games)

**Files:** Create `plugins/kdeconnect/capture_test.go`, `plugins/aiusage/capture_test.go`, `cmd/sysc-plugin-games/capture_test.go`.

**Interfaces:**
- Consumes: `capture.Panel`, `capture.Gradient` (Task 1).
- Each scene sets a package-level override and restores it with `t.Cleanup`: KDE Connect `mockupAssetDir` (an absolute path to the plugin's shipped `assets`, because the host refuses a relative image path), AI Usage `providerLogos` (a function returning the shipped `assets/logos/*.png`).
- Games builds `panel.State` directly and calls `panel.BuildTree`, the same function the plugin's session calls; the scene lives in `cmd/sysc-plugin-games` so `make captures` finds it.

- [ ] **Step 1: Write the three scenes**

Create `plugins/kdeconnect/capture_test.go`:

```go
package kdeconnect

import (
	"image/color"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/kdeconnect/screenshot.png when CAPTURE=1: a
// paired, reachable phone with its readings known, a tablet behind it, and
// four recent photos.
func TestCapturePanel(t *testing.T) {
	assets, err := filepath.Abs("assets")
	if err != nil {
		t.Fatal(err)
	}
	prev := mockupAssetDir
	mockupAssetDir = assets
	t.Cleanup(func() { mockupAssetDir = prev })

	hues := []color.NRGBA{{R: 230, G: 140, B: 60, A: 255}, {R: 60, G: 160, B: 200, A: 255}, {R: 120, G: 190, B: 90, A: 255}, {R: 190, G: 90, B: 170, A: 255}}
	var recent []RecentImage
	for i, hue := range hues {
		recent = append(recent, RecentImage{
			ID: strconv.Itoa(i), Source: "/DCIM/photo-" + strconv.Itoa(i) + ".jpg",
			Thumb: capture.Gradient(t, 96, 96, hue, color.NRGBA{R: 30, G: 30, B: 50, A: 255}),
		})
	}
	snap := Snapshot{
		Available: true, BackendName: "KDE Connect", AnnouncedName: "Demo Desktop",
		SelectedID: "demo-phone", RecentImages: recent,
		Devices: []Device{
			{
				ID: "demo-phone", Name: "Demo Phone", Type: "phone", Reachable: true, Paired: true,
				SupportedPlugins: []string{"kdeconnect_battery", "findmyphone", "ping", "sftp", "clipboard", "share", "sms", "connectivity_report", "notifications"},
				BatteryCharge:    78, BatteryKnown: true, NetworkType: "LTE", NetworkStrength: 3, NetworkKnown: true,
				NotificationCount: 2, NotificationsKnown: true,
			},
			{ID: "demo-tablet", Name: "Demo Tablet", Type: "tablet", Reachable: true, Paired: true, BatteryKnown: true, BatteryCharge: 55, BatteryCharging: true},
		},
	}
	capture.Panel(t, "kdeconnect", PanelTree(snap, DefaultSettings(), ComposerNone, Drafts{}))
}
```

Create `plugins/aiusage/capture_test.go`:

```go
package aiusage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/aiusage/screenshot.png when CAPTURE=1: three
// providers with invented usage, the first selected, and a day of history.
func TestCapturePanel(t *testing.T) {
	logos, err := filepath.Abs("assets/logos")
	if err != nil {
		t.Fatal(err)
	}
	prev := providerLogos
	providerLogos = func() map[string]string {
		out := map[string]string{}
		for _, id := range []string{"claude", "codex", "copilot"} {
			out[id] = filepath.Join(logos, id+".png")
		}
		return out
	}
	t.Cleanup(func() { providerLogos = prev })

	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	window := func(key, label, short string, used float64, minutes int, resets time.Duration) Window {
		return Window{Key: key, Label: label, ShortLabel: short, HasPercent: true, UsedPercent: used,
			WindowMinutes: minutes, ResetsAt: now.Add(resets)}
	}
	report := Report{CapturedAt: now, Providers: []ProviderReport{
		{ID: "claude", Name: "Claude", Plan: "Max", State: StateFresh, UpdatedAt: now, Windows: []Window{
			window("primary", "Session", "5h", 62, 300, 110*time.Minute),
			window("secondary", "Weekly", "Wk", 38, 10080, 71*time.Hour),
		}},
		{ID: "codex", Name: "Codex", Plan: "Plus", State: StateFresh, UpdatedAt: now, Windows: []Window{
			window("primary", "Session", "5h", 21, 300, 200*time.Minute),
			window("secondary", "Weekly", "Wk", 54, 10080, 40*time.Hour),
		}},
		{ID: "copilot", Name: "Copilot", Plan: "Pro", State: StateFresh, UpdatedAt: now, Windows: []Window{
			window("primary", "Monthly", "Mo", 87, 43200, 9*24*time.Hour),
		}},
	}}
	cfg := Config{Warn: 85, Crit: 95, Refresh: time.Minute, HostMinor: 4,
		Track: map[string]bool{"claude": true, "codex": true, "copilot": true}}
	var hist []float64
	for i := 0; i < 48; i++ {
		hist = append(hist, 20+float64((i*7)%40)+float64(i)/2)
	}
	capture.Panel(t, "aiusage", PanelTree(report, "claude", hist, cfg, 4, now))
}
```

Create `cmd/sysc-plugin-games/capture_test.go`:

```go
package main

import (
	"image/color"
	"strconv"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/panel"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/store"
)

// TestCapturePanel writes plugins/games/screenshot.png when CAPTURE=1: a
// library of invented games with generated cover art, one running and two
// favourited.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	hues := []color.NRGBA{
		{R: 220, G: 90, B: 60, A: 255}, {R: 60, G: 150, B: 210, A: 255}, {R: 110, G: 190, B: 100, A: 255},
		{R: 180, G: 90, B: 190, A: 255}, {R: 230, G: 180, B: 60, A: 255}, {R: 70, G: 200, B: 190, A: 255},
		{R: 200, G: 90, B: 130, A: 255}, {R: 110, G: 120, B: 230, A: 255},
	}
	names := []string{"Starfall Drift", "Hollow Lantern", "Ember Tide", "Quiet Orchard", "Iron Meridian", "Paper Skies", "Tidepool Kings", "Lunar Freight"}
	var games []source.Game
	for i, name := range names {
		games = append(games, source.Game{
			ID: strconv.Itoa(i + 1), Name: name, Slug: "game-" + strconv.Itoa(i+1),
			Runner: "wine", Platform: "Linux", Year: strconv.Itoa(2016 + i), Installed: true,
			PlaytimeSec: float64((i + 1) * 5400), LastPlayed: now.Add(-time.Duration(i+1) * 26 * time.Hour),
			CoverPath: capture.Gradient(t, 180, 240, hues[i], color.NRGBA{R: 20, G: 20, B: 40, A: 255}),
			Source:    "lutris",
		})
	}
	prefs := store.Prefs{Sort: "recent", View: "library", Favorites: map[string]bool{"1": true, "3": true}, Hidden: map[string]bool{}}
	capture.Panel(t, "games", panel.BuildTree(panel.State{
		Now: now, All: games, Prefs: prefs, Selected: "1",
		Running: map[string]time.Time{"1": now.Add(-47 * time.Minute)},
	}))
}
```

- [ ] **Step 2: Verify they are inert and the whole packages still pass**

```bash
for pkg in ./plugins/kdeconnect/ ./plugins/aiusage/ ./cmd/sysc-plugin-games/; do
  go vet -p 2 $pkg && go test -count=1 -p 2 $pkg 2>&1 | tail -2
done
git status --short | grep -E "screenshot.png|thumbnail.webp"; echo "files written: $?"
```

Expected: three `ok` (the whole package runs, so a leaked override would show as a failure here) and `files written: 1`.

- [ ] **Step 3: Render and look for the artwork**

```bash
for pkg in ./plugins/kdeconnect/ ./plugins/aiusage/ ./cmd/sysc-plugin-games/; do
  CAPTURE=1 go test -count=1 -p 2 -run TestCapturePanel -v $pkg 2>&1 | grep -E "wrote|FAIL|lay out"
done
magick montage plugins/kdeconnect/screenshot.png plugins/aiusage/screenshot.png plugins/games/screenshot.png -tile 3x1 -geometry 700x+8+8 -background '#444' "$SCRATCH/m-b.png"
```

Expected: three `wrote` lines. In the montage: KDE Connect shows the phone mockup (a dark phone outline above "Demo Phone") with the Ring/Files/Clipboard/Share/SMS actions; AI Usage shows the real Claude, Codex and Copilot marks beside the rows, not letter discs; Games shows eight gradient covers in a grid and a detail pane for "Starfall Drift" with a Stop button. KDE Connect's recent-photo grid sits below the visible area behind the panel's scrollbar; that is the real layout.

- [ ] **Step 4: Remove the generated files and commit**

```bash
rm -f plugins/*/screenshot.png
git add plugins/kdeconnect plugins/aiusage cmd/sysc-plugin-games
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(capture): scenes for KDE Connect, AI Usage and Games"
```

---

### Task 4: Generate everything, un-grandfather, validate

**Files:** Delete `tools/thumbnail/grandfathered.txt`; modify `docs/publishing.md`; create 16 `screenshot.png` and 16 `thumbnail.webp`.

**Interfaces:**
- Consumes: `make captures` (merged in #143): runs `TestCapturePanel` for every `plugins/*/capture_test.go` and `cmd/*/capture_test.go`, then `go run ./tools/thumbnail`.

- [ ] **Step 1: Fixture hygiene check**

```bash
grep -rnE '/home/|/Users/|nomadx|archpcx|@gmail|@proton|@[a-z]+\.(com|org|net)' plugins/*/capture_test.go cmd/*/capture_test.go; echo "matches: $?"
```

Expected: no output and `matches: 1`. A match is a real identifier: replace it with an invented one and rerun.

- [ ] **Step 2: Delete the grandfather list and its paragraph**

```bash
git rm tools/thumbnail/grandfathered.txt
```

In `docs/publishing.md`, delete this paragraph and its trailing blank line:

```markdown
Plugins listed in `tools/thumbnail/grandfathered.txt` predate this rule. The
list only shrinks: a plugin leaves it in the PR that adds its two files, and a
new plugin is never added to it.

```

The loader treats a missing file as an empty list, so no Go change is needed (the list code stays; removing it is a separate cleanup).

- [ ] **Step 3: Generate**

```bash
make captures 2>&1 | tail -25
ls plugins/*/screenshot.png | wc -l; ls plugins/*/thumbnail.webp | wc -l
```

Expected: a `capture <package>` line per scene (sixteen), `ok` for each, sixteen `wrote plugins/<dir>/thumbnail.webp` lines; `16` and `16`.

- [ ] **Step 4: Look at the cards**

```bash
for p in plugins/*/; do n=$(basename $p); magick "$p/thumbnail.webp" "$SCRATCH/card-$n.png"; done
magick montage "$SCRATCH"/card-*.png -tile 4x4 -geometry 360x+6+6 -background '#333' "$SCRATCH/cards.png"
```

Open `cards.png`. Expected: sixteen cards, each a recognisable panel, none blank. Wide panels (Notes, Moonbit, AI Usage, Games, Calendar) shrink: judge whether their text is still a legible shape. If one is unacceptable, tighten that scene (fewer rows, larger content) and rerun `make capture PLUGIN=<dir>`; do not hand-edit a PNG.

- [ ] **Step 5: Validate**

```bash
gofmt -l .
go run ./tools/validate-manifests >/dev/null && echo validate-ok
go run ./tools/thumbnail -check && echo thumbnails-ok
go test -count=1 -p 2 ./tools/thumbnail/
go test -count=1 -p 2 ./tools/validate-manifests/
git diff origin/main --stat -- go.mod go.sum
```

Expected: no gofmt output; `validate-ok`; `thumbnails-ok`; both tool packages `ok`; the last command prints nothing. Sizes: each `thumbnail.webp` is under 512 KB and each `screenshot.png` under 4 MiB (the validator checks).

- [ ] **Step 6: Commit**

```bash
git add plugins/*/screenshot.png plugins/*/thumbnail.webp docs/publishing.md
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(catalog): screenshots and thumbnails for all plugins, end the grandfather list"
```

(`git rm` already staged the deletion of `tools/thumbnail/grandfathered.txt`.)

---

### Task 5: Pull request

- [ ] **Step 1: Run the touched packages once more, one at a time**

```bash
go test -race -count=1 -p 2 ./internal/capture/
for p in aiusage calendar cat faith github-notifications kdeconnect mini-docker moonbit notes protonvpn screen-recorder screenshot timer wallpaper-depth world-clock; do go test -count=1 -p 2 ./plugins/$p/ 2>&1 | tail -1; done
go test -count=1 -p 2 ./cmd/sysc-plugin-games/
git status --short
```

Expected: every package `ok`; `git status` clean.

- [ ] **Step 2: Push and open the PR**

```bash
git push -u origin feat/plugin-captures-backfill
gh pr create --repo Nomadcxx/sysc-plugins --base main --head feat/plugin-captures-backfill \
  --title "feat: screenshots and thumbnails for all 16 plugins" --body "..."
```

PR body: why (the thumbnail requirement is live and every plugin was grandfathered), what (thirteen scenes, `capture.Gradient`, sixteen generated screenshots and thumbnails, the grandfather list ended), how it was checked (Task 4 Step 5, the card montage), and what reviewers should know: images are generated from fonts installed on one machine and are not byte-reproducible, only the thumbnails are byte-checked; Screen Recorder's and Wallpaper Depth's real panels are sparse; Notes' and Moonbit's cards shrink. Include one or two card images. End with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`. Do not merge without the user's go-ahead.

- [ ] **Step 3: After the merge (not part of this PR)**

Pin each catalog row's `screenshot` to the thumbnail at the merge commit, so rows released before thumbnails existed get cards without a new version: `go run ./tools/catalog thumbnails -ref <full 40-character merge commit>`, reviewed as its own small PR (see `docs/publishing.md`).

---

## Self-Review

**Spec coverage** (delivery step 3 of the spec): scenes for the remaining 13 plugins (Tasks 2 and 3; the three pilots are on `main`); `make captures` producing screenshots and thumbnails (Task 4 Step 3); grandfather lines removed with the files in one commit (Task 4 Steps 2 and 6); catalog pinning as a follow-up after merge (Task 5 Step 3); fictional-data rule (Global Constraints, Task 4 Step 1); Games' scene in `cmd/` (Task 3); Moonbit's scene feeds the review phase and Phone Connect's feeds a paired phone (Tasks 2 and 3).

**Deviations from the spec's wording**, each deliberate: the spec's image helper "writes a synthetic PNG" and is a small addition to `internal/capture`, built as `Gradient` (Task 1); Games builds `panel.State` directly instead of driving a `session`, because `panel.BuildTree` is the same call the session makes.

**Placeholder scan:** none; every scene is the code that was rendered and inspected. The PR body is described, not quoted, because it depends on what was run.

**Type consistency:** `capture.Panel(t, dir, tree)` and `capture.Gradient(t, w, h, from, to)` are used identically in every scene; package names for the three hyphenated plugin directories are listed in Task 2.

**Risks the executor should know**
- Screenshots depend on installed fonts; regenerate on the desktop (amd64) for `thumbnail -check` to pass in CI.
- `go run ./tools/thumbnail` rewrites all sixteen thumbnails each time; `make capture PLUGIN=<dir>` touches one.
- The prototypes rendered against `sysc-panel-preview` built from sysc-shell `3498ff7a`; a newer build may move a pixel but not a layout.
- Games' `LastPlayed`, KDE Connect's photos and the Faith verse come from fixtures; none is a real user's data.
