# KDE Connect Gap-Fill Plan (Plan D)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Status:** Ready, audited (`2026-09-21-kdeconnect-gap-fill-audit-report.md` — executable after
the amendments below, which are applied). Execution is gated on two merges: `feat/kdeconnect`
(carrying Phase 0, sysc-472/473/475/476/477 and the plugin half of 474) into this repo, and
sysc-shell `feature/wire-minor-5` (Designs A/B/C, minor five and six) **plus
`feature/kdeconnect-icons`** (the kdeconnect glyph catalogue) into sysc-shell `main`.

**Goal:** Close the remaining DMS parity gaps in `plugins/kdeconnect` that the plugin wire
could not express before: icon-led buttons, tap-to-ping, the recent-images grid, the device
mockup, dynamic panel width, composer focus-on-open, and the animated charging fill.

**Architecture:** The plugin already speaks snapshot-and-patch (service → Snapshot →
`PanelDelta` keyed replacements). Every task here either adopts a new wire capability from
sysc-shell minor 5/6 (image nodes, stroke, button children, `panel.resize`, `view.focus`,
`animate`) or is plugin-local service/view work that needed those capabilities to be worth
building. No sysc-shell changes are in scope; the shell side is done and committed.

**Tech Stack:** Go 1.26, `plugin/v1` wire protocol (minor 6 after Task 0),
`github.com/godbus/dbus/v5`, one new dependency `golang.org/x/image` (thumbnail scaling),
`find` over the mounted SFTP share for the image scan.

**Repos:** `~/sysc-plugins` (all tasks). bd tracking stays in the sysc-shell tracker —
sysc-plugins has no `.beads` database; the issues this plan closes (sysc-478, 468, 445, 447,
430 under epic 420) already live there.

**Gates per commit:** `make build test vet fmt validate` (the Makefile targets exist; there
is no aggregate `gate` target).

**Reference:** DMS DankKDEConnect v3.0.0; the element-by-element verdict table lives in
`docs/plans/2026-09-19-kdeconnect-parity-audit-report.md` (on `feat/kdeconnect` until that
branch merges — register it in `docs/plans/README.md` in the same commit that merges it).

---

## Task 0: prerequisites — merge order, pin bump, manifest minor

**Files:**
- Modify: `go.mod`, `go.sum` (sysc-shell pin)
- Modify: `plugins/kdeconnect/manifest.json` (protocol minor)

**Interfaces:**
- Produces: the plugin compiles against `plugin/v1` carrying `KindImage`
  (`Path`, `ImageSize`, `ImageW`, `ImageH`, `Background`, `Stroke`, `StrokeFill`),
  `Animate`, `CallPanelResize`/`PanelResizeParams{Width,Height}`,
  `CallViewFocus`/`ViewFocusParams{View,Node}`, and the button-children rule
  (button children must be non-interactive leaves).

- [ ] **Step 1: Merge `feat/kdeconnect` into this repo's `main`** (Phase 0 included), then
      cut the implementation branch: `git checkout -b feat/kdeconnect-gap-fill`.
- [ ] **Step 2: Merge sysc-shell `feature/wire-minor-5` and `feature/kdeconnect-icons` into
      sysc-shell `main`**, then bump the pin:
      `go get github.com/Nomadcxx/sysc-shell@<merge-commit> && go mod tidy`.
      The pin moves off `v0.0.0-20260916042624-cc684ee8c131` (minor 2) per the pinning rule.
      The icon branch (`3c9dd81`) carries the kdeconnect glyph catalogue (`smartphone`,
      `battery-*`, `phone-in-talk`, `folder-open`, `content-paste`, `share`, `sms`,
      `notifications-active`, `refresh`, the network glyphs); without it `iconNode` rejects
      every kdeconnect view — including the Phase 0 views already on `feat/kdeconnect` —
      while the plugin-side gates still pass, masking the breakage.
- [ ] **Step 3: Bump the manifest protocol** to `{"major": 1, "minor": 6}` — the plugin will
      send minor-5 fields (image, stroke) and the minor-6 `animate` flag. The host's
      handshake checks the major only, but the manifest should not lie about what the view
      trees carry.
- [ ] **Step 4: Sanity gates.** Run: `make build test vet fmt validate`. Expected: all green
      with no source changes yet — the pin bump alone must not break the plugin (minor 2
      vocabulary remains valid under minor 6; the wire is additive).

- [ ] **Step 5: Commit** `build: pin sysc-shell wire minor six and declare it in the manifest`.

## Task 1: icon-led buttons (sysc-478)

DMS's `DankKDEActionButton` pairs icon and label. Today `gatedButton`
(`plugins/kdeconnect/view.go:285`) renders text only, and the pairing buttons
(`pairingCard` :309, `unpairedCard` :342) are inline text-only literals.

**Files:**
- Modify: `plugins/kdeconnect/view.go` (`gatedButton`, `pairingCard`, `unpairedCard`,
  `shareComposerTree`, `smsComposerTree`)
- Test: `plugins/kdeconnect/view_test.go`

**Interfaces:**
- Produces: every actionable button in the panel carries an icon and a label; disabled
  gating is unchanged.

- [ ] **Step 1: Write the failing test** — assert `pairingCard`'s accept/reject/cancel
      buttons and `unpairedCard`'s request button carry `Icon` values `check`, `close`,
      `close`, `link`, and that the composer send buttons (`share-*-send`, `sms-send`)
      carry `send`. Run `go test ./plugins/kdeconnect/ -run TestPairing -count=1`;
      expected: FAIL (icons empty).
- [ ] **Step 2: Implement.** Set `Icon` on the button alongside `Text` — the converter already
      synthesizes `[KindIcon, KindText]` children and clears `Text` when both are present
      (sysc-shell `internal/plugin/view.go`, button branch), so no explicit children are needed.
      Extend `gatedButton` to `gatedButton(id, icon, text, name string, enabled bool)` and update
      its four call sites (`share-url-send`, `share-text-send`, `share-file-send`, `sms-send`).
      The pairing buttons are built inline, not via `gatedButton`; give their literals `Icon`
      values `check`, `close`, `close`, `link`.
      Extend `gatedButton` to `gatedButton(id, icon, text, name string, enabled bool)`.
- [ ] **Step 3: Run the view tests.** `go test ./plugins/kdeconnect/ -count=1`. Expected: PASS.
- [ ] **Step 4: Commit** `feat(kdeconnect): pair icons with button labels (sysc-478)`.

## Task 2: tap-to-ping on the device display (sysc-468)

DMS's `PhoneDisplay` pings when tapped. The device card (`deviceCardTree`,
`view.go:456`) is a static column; minor 5's button-children rule makes it a button.

**Files:**
- Modify: `plugins/kdeconnect/view.go` (`deviceCardTree`, `actionRowTree`)
- Modify: `cmd/sysc-plugin-kdeconnect/main.go` (`handleInput` action routing)
- Test: `plugins/kdeconnect/view_test.go`

**Interfaces:**
- Produces: tapping the device card sends a ping; the explicit ping button in the action
  row disappears while the card handles it (the sysc-475 rule flips: Phase 0 kept ping
  visible because tap-to-ping did not exist yet).

- [ ] **Step 1: Write the failing test** — `deviceCardTree` returns a `KindButton` with
      `ID: "device-ping"`, `Events: [EventActivate]`, whose children are exactly the
      current card content (icon, name, status, battery progress — all non-interactive
      leaves, which the minor-5 validator accepts). Run the view tests; expected: FAIL.
- [ ] **Step 2: Implement the card.** Wrap the existing children in the button node. Keep
      `Fill: "card"`, `Radius: 12`, `Padding: 14` on the button itself (chrome paints the
      button's own fill; children paint inside).
- [ ] **Step 3: Route the action.** In `handleInput`, add routing for `"device-ping"` **and**
      `"ping"` — no ping routing exists today (`handleInput` has no ping/ring/browse/clipboard
      cases; the action-row ping button is currently dead). Map both to
      `kdeconnect.Action{Kind: kdeconnect.ActionPing, ...}`. The distinct ID keeps input events
      distinguishable: the host stamps `plugin:<view>:<nodeID>` from `Action = ID`, so duplicate
      IDs would make the two buttons indistinguishable (`PanelDelta` replacements match `Key`,
      not `ID`).
- [ ] **Step 4: Hide the redundant ping button.** In `actionRowTree`, include the ping
      button only when the device card is not shown (`!settings.ShowDeviceCard`), matching
      DMS ("ping hidden while the placeholder handles it"). Update `TestActionRowGating`
      (`view_test.go:312`), which asserts the action row has exactly six children with
      `Children[0].ID == "ring"`; with ping hidden the row has five.
- [ ] **Step 5: Run the view tests and gates.** `go test ./plugins/kdeconnect/ -count=1`.
- [ ] **Step 6: Commit** `feat(kdeconnect): tap the device display to ping (sysc-468)`.

## Task 3: recent images grid (sysc-445)

DMS scans the SFTP mount with `find` (maxdepth, png/jpg/jpeg/webp), shows a thumbnail
grid, and offers per-image open and share. The wire now carries image nodes keyed to
absolute local paths, so the plugin downloads nothing new — it thumbnails the mounted
files into a local cache and hands the host those paths.

**Files:**
- Modify: `plugins/kdeconnect/manifest.json` (three settings)
- Modify: `plugins/kdeconnect/service.go` (Settings, Snapshot, scan, thumbnails, actions)
- Modify: `plugins/kdeconnect/daemon.go` (sftp interface) and
  `plugins/kdeconnect/service.go` (mount call beside `startBrowsing` :585)
- Modify: `plugins/kdeconnect/view.go` (`recentImagesTree`)
- Modify: `cmd/sysc-plugin-kdeconnect/main.go` (action routing)
- Test: `plugins/kdeconnect/service_test.go`, `plugins/kdeconnect/view_test.go`
- Modify: `go.mod` (add `golang.org/x/image` — the plan's only new dependency, for
  `draw.CatmullRom` scaling; stdlib cannot scale)

**Interfaces:**
- Produces: `Settings{RecentImagesPath string, MaxRecentImages int, ScanSubdirectories bool}`;
  `Snapshot.RecentImages []RecentImage` with `RecentImage{ID, Source, Thumb string}`;
  actions `recent-open-<ID>` (xdg-open the source) and `recent-share-<ID>` (shareUrl the
  source to the device).
- Host contract this relies on: `icons.FileResolver` accepts absolute local paths with
  extensions `.png .xpm .jpg .jpeg .gif .bmp` (sysc-shell `internal/icons/theme.go`);
  `.webp` is **not** decodable host-side, so the scan filters it out (recorded deviation —
  DMS scans webp too).

- [ ] **Step 1: Manifest settings.** `recent_images_path` (string, default ""),
      `max_recent_images` (float, default 6, min 1, max 12), `scan_subdirectories`
      (bool, default false). Extend `Settings` and `DefaultSettings` to match.
- [ ] **Step 2: Write the failing scan test.** `scanRecentImages(root, mountPoint string,
      max int, sub bool) ([]string, error)`: rejects a root that is not under `mountPoint`,
      runs `find <root> -maxdepth <1|2> \( -iname '*.png' -o -iname '*.jpg'
      -o -iname '*.jpeg' \) -printf '%T@ %p\n'`, sorts descending by mtime, caps at
      `max`. Test against a temp dir with mixed-age files of all four extensions plus a
      nested file (depth gate). Run; expected: FAIL (undefined).
- [ ] **Step 3: Implement the scan** in `service.go` as a pure function over a mounted
      path (no DBus), so the test needs no daemon.
- [ ] **Step 4: Write the failing thumbnail test.** `thumbnail(src, cacheDir string)
      (string, error)`: decode (stdlib `image/png`, `image/jpeg`), scale the longest side
      to 512 (`x/image/draw.CatmullRom`), encode JPEG, cache at
      `<cacheDir>/<sha1(src|mtime)>.jpg`, return the cached path unchanged on a second
      call. Run; expected: FAIL.
- [ ] **Step 5: Implement the thumbnailer.** Cache root:
      `os.UserCacheDir()/sysc-plugins/kdeconnect/thumbs/`. The hash key makes mtime
      changes re-thumbnail and stale entries harmless.
- [ ] **Step 6: Wire the service.** In the refresh path, when
      `settings.RecentImagesPath != ""` and the selected device is reachable with the
      `sftp` plugin: ensure the SFTP mount (extend the seam at `service.go:585`,
      `ActionBrowse` → `sftpIface.startBrowsing`, with a `mountPoint()` call on
      `…device.sftp` — a method, not a property;
      `docs/plans/2026-09-19-kdeconnect-phone-connect.md:61,258` — the DMS
      `KDEConnectService` sftp flow), scan, thumbnail, fill `Snapshot.RecentImages`,
      and keep the `ID → Source` map for action routing. Failures degrade to an empty
      grid, never an unavailable panel.
- [ ] **Step 7: Write the failing view test.** `recentImagesTree` renders a card titled
      "Recent" containing rows of three per-image columns: `KindImage` with
      `Path: thumb`, `ImageSize: 96`, `ID: "recent-<ID>"`, plus a button row
      (`recent-open-<ID>` icon `folder-open`, `recent-share-<ID>` icon `share` — both ride
      the Task 0 icon-branch merge; the material name `folder_open` is a different glyph).
      Hidden entirely when `RecentImages` is empty. Run; expected: FAIL.
- [ ] **Step 8: Implement the view** and route the two action prefixes in `handleInput`
      (`xdg-open` via `os/exec` for open — plugin-side, as DMS does; `ActionShareURL`
      with a `file://` URI for share).
- [ ] **Step 9: Gates.** `make build test vet fmt validate`.
- [ ] **Step 10: Commit** `feat(kdeconnect): recent images grid over the sftp mount (sysc-445)`.

## Task 4: device mockup (sysc-445)

DMS's `PhoneDisplay` renders a type-sized mockup (135–260 px) instead of a bare icon.

**Files:**
- Create: `plugins/kdeconnect/assets/{phone,tablet,desktop,laptop}.png`
- Modify: `plugins/kdeconnect/view.go` (`deviceMockup`, `deviceCardTree`)
- Test: `plugins/kdeconnect/view_test.go`

**Interfaces:**
- Produces: `deviceMockup(dev *Device) (path string, w, h int, ok bool)` — type-keyed
  asset with DMS's size classes (phone 135×260, tablet 180×240, desktop 260×160,
  laptop 260×170); `deviceCardTree` swaps the `KindIcon` for
  `KindImage{Path, ImageW, ImageH, Background: true, Shape: "card"}` when the asset
  resolves, and falls back to the icon otherwise.

- [ ] **Step 1: Assets.** Generate the four PNGs once (a small `tools/mockup.py` or
      hand-placed artwork; neutral device silhouettes are enough — DMS draws its mockup
      in QML, which the wire cannot express, so bitmaps are the honest substitute).
- [ ] **Step 2: Write the failing test.** Mockup node shape per device type; icon
      fallback when the asset is missing. Make the asset directory a package var so the
      test can point it at a temp dir (`os.Executable()`-relative resolution does not
      work under `go test`). Run; expected: FAIL.
- [ ] **Step 3: Implement.** Asset resolution: `os.Executable()` → dir → `../assets/`.
      `Background: true` requires the explicit `ImageW`+`ImageH` box (minor-5 validator);
      both are set. The mockup is a non-interactive leaf, so it may sit inside the
      Task 2 ping button — compose them.
- [ ] **Step 4: Record the deviations** in this doc's deviation ledger: offline dimming
      (the host does not dim images; the status line already carries offline) and custom
      device images / per-device type overrides (the wire has no per-device map settings).
- [ ] **Step 5: Run the view tests and gates.**
- [ ] **Step 6: Commit** `feat(kdeconnect): type-sized device mockup (sysc-445)`.

## Task 5: dynamic panel width (panel.resize)

DMS's popout is 400 px wide plus a type-based placeholder up to 525. The manifest fixes
the panel at 400; minor 5's `panel.resize` retargets the open panel.

**Files:**
- Modify: `cmd/sysc-plugin-kdeconnect/main.go` (`panelWidth`, uiState loop)
- Test: `cmd/sysc-plugin-kdeconnect/main_test.go`

**Interfaces:**
- Produces: `panelWidth(snap kdeconnect.Snapshot, showCard bool) int` — 400 base, 525
  when the mockup shows for a large type (tablet/desktop/laptop). The uiState loop sends
  `c.Call(ctx, v1.CallPanelResize, v1.PanelResizeParams{Width: w, Height: 520})` only
  when the class changes.

- [ ] **Step 1: Write the failing test** — table over device types and `showCard`:
      phone/no-card → 400, tablet/card → 525, desktop/card → 525, unavailable → 400.
      Run; expected: FAIL.
- [ ] **Step 2: Implement** the pure function and the change-detector in the uiState
      loop (track the last sent width; send after the panel publish). The host bounds the
      request to 64–4096 (525 is fine), replies *requested*, and the compositor's
      configure completes it — the plugin must not wait on the reply beyond the call
      returning. Treat the call as best-effort: ignore a failure reply — the panel can
      close between publish and call, and `resizePanel` errors when no panel is open.
- [ ] **Step 3: Run the main tests and gates.**
- [ ] **Step 4: Commit** `feat(kdeconnect): resize the panel with the device type`.

## Task 6: composer focus-on-open (view.focus)

DMS force-focuses the first field when a dialog opens. Minor 5's `view.focus` targets a
stamped node; the converter stamps a text input's `Action` from its `ID`
(sysc-shell `internal/plugin/view.go:419`), so the input's wire ID is the focus target.

**Files:**
- Modify: `cmd/sysc-plugin-kdeconnect/main.go` (uiState composer transition)
- Test: `cmd/sysc-plugin-kdeconnect/main_test.go`

**Interfaces:**
- Produces: on composer open, after the panel publish,
  `c.Call(ctx, v1.CallViewFocus, v1.ViewFocusParams{View: viewID, Node: "share-text"})`
  (share) or `Node: "sms-number"` (SMS). Best-effort: a failure reply (composer closed
  first, node gone) is ignored.

- [ ] **Step 1: Write the failing test** — opening the share composer issues exactly one
      `view.focus` for `share-text`; opening SMS targets `sms-number`; closing issues
      none. Run; expected: FAIL.
- [ ] **Step 2: Implement** the transition hook in the uiState loop.
- [ ] **Step 3: Run the main tests and gates.**
- [ ] **Step 4: Commit** `feat(kdeconnect): focus the composer's first field on open`.

## Task 7: charging fill and animated progress (sysc-447)

DMS fills the bar pill with the charge level in error/warning/success tints while
charging, and every level move animates. Minor 6's `animate` flag gives the panel
progress the glide; the pill gets the level fill as a progress child of the pill button
(legal since minor 5 — non-interactive child).

**Files:**
- Modify: `plugins/kdeconnect/view.go` (`batteryProgressNode` :469, `BarTree` :51)
- Test: `plugins/kdeconnect/view_test.go`

**Interfaces:**
- Produces: `batteryProgressNode` sets `Animate: true` (  `Key: "battery-progress"` is
  already present — minor 6 requires the key and the host validator enforces it). While
  charging, the bar pill button gains a first child
  `KindProgress{Key: "kdeconnect-pill", Value: level, Animate: true, Tone: <tone>}` with
  the tone mapped `<20 → error, else accent` (the wire has no warning/success fills, and
  progress cannot carry `Fill` — `fillAllowed` is container/button), followed by the
  existing icon+label row.

- [ ] **Step 1: Write the failing test** — progress carries `Animate`; the charging pill
      carries the progress child with the right tint per level; the idle pill is
      unchanged. Run; expected: FAIL.
- [ ] **Step 2: Implement.** If the button-child layout fights the pill's shape (children
      stack where the pill needs an overlay), fall back to the audit's static
      approximation — set the button's own `Tone` (`error` below 20, else `accent`) while
      charging — and record the fallback in the deviation ledger. Decide by rendering,
      not by hope.
- [ ] **Step 3: Run the view tests and gates.**
- [ ] **Step 4: Commit** `feat(kdeconnect): animated charging fill (sysc-447)`.

## Task 8: live Niri gate and completion handover (sysc-430)

**Files:**
- Create: `docs/plans/2026-09-XX-kdeconnect-gap-fill-completion-handover.md` (snapshot —
  write once, never edit; later corrections go to bd or the register)

- [ ] **Step 1: Full gates** on the laptop: `make build test vet fmt validate`.
- [ ] **Step 2: Live smoke** with the real daemon and the paired device
      (`SYSC_KDECONNECT_LIVE=1` precedent from e3edef9), verifying each Plan D item live:
      icons on buttons, tap-to-ping, the grid scanning the real mount, the mockup, the
      width change on device switch, composer focus, the animated charging fill.
      Screenshot each into the handover.
- [ ] **Step 3: Write the completion handover** — commit hashes, gate output, live
      observations, and the deviation ledger below as accepted deviations.
- [ ] **Step 4: Resolve the two decision tickets** (owner-ruled 2026-09-21, folded into
      this task because both block the sysc-420 epic):
      - `sysc-469` — **keep the dedupe.** Fire once on the pairing-request rising edge;
        DMS's refire on every fetch is arguably a bug. Record in the deviation ledger.
      - `sysc-470` — **match DMS's 3→2-bar mapping.** DMS's rationale is deliberate and
        documented (a 3-bar glyph reads as 2-of-3 next to 4/5-bar glyphs); parity is the
        goal. One-line change in the strength-icon mapping plus a test row.
- [ ] **Step 5: Close the bd items** in the sysc-shell tracker: sysc-478, 468, 445, 447,
      469, 470, 430, then the sysc-420 epic.

---

## Deviation ledger (accepted, recorded so nothing is silently dropped)

| Deviation | Why |
|---|---|
| `.webp` excluded from the image scan | The host's `FileResolver` decodes png/xpm/jpg/jpeg/gif/bmp only; a webp decoder is sysc-shell work. |
| Drag-out and portal share per image | The wire has no OS drag and no portal surface; open (xdg-open) and share-to-device (shareUrl) are the in-wire affordances. |
| Per-device recent-images path, custom device images, type overrides | The settings schema is flat; per-device map settings are a wire gap. Single global settings ship instead. |
| Offline mockup dimming | The host does not dim image nodes; the status line carries offline state. |
| Warning tone for pairing statuses | The wire has no warning tone (audit wire gap); accent stays. |
| Charging tint collapse: error/accent only | The wire has no warning/success fills and progress cannot carry `Fill`; DMS's three-tint mapping collapses to `Tone` error below 20, accent otherwise. |
| Pairing-request toast fires once on the rising edge (sysc-469 ruled) | DMS re-toasts on every device fetch while a request pends — arguably a bug; the dedupe is the better behaviour. |
| Header border, hover states, dialog open/close animation | Host-owned chrome; the wire deliberately carries none. |
| MPRIS player section, Valent backend, keyboard shortcuts, i18n | Separate ledger items (sysc-444, 443, 449, 448); not this plan's scope. |
