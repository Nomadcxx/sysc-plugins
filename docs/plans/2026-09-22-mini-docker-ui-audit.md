# Mini Docker UI audit

**Date:** 2026-09-22
**Scope:** UI-only audit of `plugins/mini-docker/` (bar widget, 720×560 attached panel, manifest declaration) against the repo's rebuilt prior art — `plugins/timer` (first widget rebuilt against the current patterns) and `plugins/aiusage` (newest, just audit-hardened). Structure, layout, tones, icons, hierarchy, panel sizing, and manifest declaration quality; flows, backend behavior, and protocol wiring are the other audits' territory. Protocol facts taken from `github.com/Nomadcxx/sysc-shell` `plugin/v1`: minor 1 adds the `subtle`/`accent` text tones, the host honors node `Height`, `KindList` is baseline panel vocabulary the host converts to a scroll viewport, and a bar view needs a row root while a panel or tooltip view needs a column root (`internal/plugin/view.go:28-33`). Read-only except this document.

## Findings

1. **P1 — The bar has no activate control, so the panel is unreachable.**
   `plugins/mini-docker/view.go:11-15` renders the bar as a bare `KindText` inside a row; `cmd/sysc-plugin-mini-docker/main.go:104-118` (the `InputEvent` switch) has no `"open"` case. The host owns no pill click — the plugin's own button is what opens the panel — so the declared 720×560 panel can never be opened by clicking the widget. Every rebuilt widget makes the whole pill one `Button` with `ID: "open"` and wires it to `CallPanelOpen`: timer `view.go:30-34` + `main.go` open case, world-clock `view.go:13-17` ("the whole thing opens the panel instead of a small zone chip beside dead text") + `main.go:128-129`, aiusage `view.go:153-157` + `main.go:187-189`.
   **Fix:** `BarTree` returns a single `KindButton` (`ID: "open"`, `Text: label`, `Name: "Open Mini Docker"`, `Role: "button"`, `Events: [activate]`), and `main.go` gains the two-line `case "open"` → `CallPanelOpen` copied from world-clock. Two files, no new dependencies. An icon is optional — world-clock proves a text-only pill is established prior art; if a glyph is wanted, `lan` exists in the host's material subset (there is no docker glyph).

2. **P1 — Panel content overflows with no scroll; action buttons below the fold are unreachable, and past ~170 containers the whole view is rejected.**
   `plugins/mini-docker/view.go:73-75` appends one row per container to a bare column with no height and no viewport. A container row is a gap-2 column with two text lines plus a button row ≈ 76-80 px, so a 560-high panel shows roughly six containers before clipping — and `docker ps -a` lists stopped containers too, so real machines exceed that immediately. Worse, the wire's ceilings bind: each row is 5-6 nodes (`view.go:79-97`), so `MaxNodes` 1024 rejects the entire tree past ~170 containers, and `MaxChildren` 256 rejects the root column past ~253 (`plugin/v1/node.go:27-32`). The panel then fails validation wholesale — blank surface, no error the user can act on. Prior art: aiusage puts each pane in a `KindList` with an explicit `Height` precisely because "the scroll rule: it clips only with a height" (`plugins/aiusage/view.go:303-307`); notes (`view.go:90`) and world-clock (`view.go:109`) do the same, and the host converts `KindList` to a scroll viewport honoring `Height` (`internal/plugin/view.go:429-433`).
   **Fix:** wrap the container rows in one `KindList` with a fixed `Height` (root padding subtracted, aiusage's `paneHeight` pattern). `KindList` is baseline panel vocabulary — notes uses it at protocol minor 0 — so no manifest bump is required. The 256-children ceiling then only binds past ~250 containers, an acceptable documented edge.

3. **P2 — The tooltip view is published with a row root and dies in conversion.**
   `cmd/sysc-plugin-mini-docker/main.go:67-68` handles `ViewTooltip` with `minidocker.BarTree(tooltip)`, whose root is a `KindRow` (`view.go:12`). The host requires a column root for tooltips (`internal/plugin/view.go:28-33`) and rejects the tree, so the hover tooltip silently never renders. timer, aiusage, and calendar each have a `TooltipTree` returning a column; mini-docker is the only plugin that reuses its bar builder. The existing test validates `BarTree` only as `ViewBar` (`mini_docker_test.go:147-149`), which is why this slipped through.
   **Fix:** a three-line `TooltipTree` returning `{KindColumn, Children: [text]}` like calendar's (`plugins/calendar/view.go:18-23`). Delete the tooltip arm's reuse of `BarTree`.

4. **P2 — The panel root has no padding; content touches the panel chrome on all four sides.**
   `plugins/mini-docker/view.go:50`: the root column carries only `Gap: 8`. timer's root is `Padding: 16` (`plugins/timer/view.go:103`), aiusage's `Padding: 12` (`plugins/aiusage/view.go:338`) — "the root carries padding so the cards never touch the panel chrome."
   **Fix:** `Padding: 12` on the root column. One field.

5. **P3 — 720 width is unjustified by the content.**
   `plugins/mini-docker/manifest.json:35` declares 720×560, but each row's natural content is a name·status line, an image line, and one or two short text buttons — roughly 350 px wide, left-aligned, leaving ~370 px dead on the right of every row. aiusage earns 750 with two side-by-side panes (290 + 412 + gaps); timer needs 360. The repo's one-column panels cluster at 320-500 (calendar 320, world-clock 420, kdeconnect 400, wallpaper-depth 500).
   **Fix:** shrink to ~440 wide in the manifest. Deletion, not addition — no grid invented to soak up the width. With finding 2's scroll list, the 560 height becomes honest (more rows visible).

6. **P3 — Header has no title hierarchy and Refresh glues to the title.**
   `plugins/mini-docker/view.go:51-55`: "Docker containers" is plain body text and the Refresh button sits immediately after it with gap 8. timer's header is `Size: "title", Bold: true` with `PinEnd: true` pushing the close control to the right edge (`plugins/timer/view.go:104-108`).
   **Fix:** `Size: "title"`, `Bold` on the title text; `PinEnd: true` on the header row so Refresh right-aligns. Optionally `Icon: "refresh"` on the button — the glyph is in the catalogue and aiusage's header pairs icon + title the same way.

7. **P3 — Container rows are flat: one merged line, no state tone, no hierarchy.**
   `plugins/mini-docker/view.go:81` renders `name + " · " + status` as a single default-tone run, so "Up 2 hours" and "Exited (0) 5 minutes ago" read exactly like the container name; only the image line is differentiated (`view.go:82`, `ToneSubtle` — correct). aiusage rows give each line a role: bold name, toned second line, pinned tabular value (`plugins/aiusage/view.go:392-421`). The tone rule in the vocabulary is explicit that tone is a hierarchy signal, never a state (`plugin/v1/node.go:115-123`).
   **Fix:** split the line — name in its own text (`Bold`), status in a second text with `ToneAccent` when `Running()` (the "active player's name" case the vocabulary names) and `ToneSubtle` otherwise. This stays inside minor-1 vocabulary. Card fills (`Fill: "card"`, `Shape: "card"`, `Padding: 8`, the aiusage row treatment) are minor-2 vocabulary — adopt them only alongside a manifest minor bump; the tones alone restore the hierarchy without one.

8. **P3 — No ordering: the containers users act on are buried under exited ones.**
   `plugins/mini-docker/view.go:73-75` renders `docker ps -a` order verbatim. The bar counts running containers, but the panel lists every stopped container first-come, so on a real machine the actionable rows start below the fold (compounding finding 2). aiusage sorts its list by the dimension that matters (`sortProviderRows`, `plugins/aiusage/view.go:345-367`).
   **Fix:** a stable running-first sort of the slice before rendering — a few lines in `view.go`, no new abstraction.

9. **P3 — The bar gives no failure signal: an unavailable daemon is indistinguishable from the hidden setting.**
   `plugins/mini-docker/view.go:18-21`: `BarLabel` returns `"docker"` both when docker is unavailable and when `status_mode` is `hidden`, and `BarTree` (`view.go:11-15`) carries no tone at all — the user's only hint is the tooltip. The vocabulary's rule: "a failure stays ToneError" (`plugin/v1/node.go:115-119`). timer threads state through `barTone` for exactly this reason (`plugins/timer/view.go:11-19`).
   **Fix:** thread a tone into `BarTree` — `ToneError` when `!available`; optionally `ToneAccent` when running > 0, matching timer's live-state accent.

10. **P3 — The manifest declares the notifications capability but nothing sends a notification.**
    `plugins/mini-docker/manifest.json:11-15` lists `notifications`; `cmd/sysc-plugin-mini-docker/main.go` contains no `CallNotify`. timer earns the capability (fired-pomodoro notify); mini-docker's lifecycle actions report through the panel's error line instead.
    **Fix:** delete `"notifications"` from capabilities. Re-add only if a future tranche actually notifies.

11. **P4 — Dead `Gap: 6` on a single-child row.**
    `plugins/mini-docker/view.go:12`: the bar row has one child, so the gap does nothing.
    **Fix:** drop it (or it disappears naturally with finding 1's single-button rewrite).

12. **P4 — Loading and unavailable lines paint in the default tone.**
    `plugins/mini-docker/view.go:62` ("Loading…") and `view.go:66` ("Docker is not available") are plain text. Per the tone rule, secondary/in-progress text is `ToneSubtle` and failures are `ToneError`.
    **Fix:** `ToneSubtle` on the loading line, `ToneError` on the unavailable line.

13. **P4 — A stale error and the loading line can render simultaneously.**
    `plugins/mini-docker/view.go:57-64`: the error line is appended first and the loading line unconditionally after it, so the refresh that follows a failed action shows the old error and "Loading…" stacked. The branches should be mutually exclusive while a refresh is in flight.
    **Fix:** reorder so `loading` short-circuits before the error line (the view-side fix; clearing `errMsg` when a refresh starts is the backend audit's call).

14. **P4 — Bar count is not tabular.**
    `plugins/mini-docker/view.go:30-33` builds `"docker " + N` as one proportional run; aiusage marks every number in the bar `Tabular: true` so digit changes don't shift neighbors (`plugins/aiusage/view.go:171,178`).
    **Fix:** `Tabular: true` on the bar text (rides along with finding 1's button node).

15. **P4 — Handshake fallback version drifts from the manifest.**
    `cmd/sysc-plugin-mini-docker/main.go:23` pins `Version: "0.1.0"` while `manifest.json:5` says `0.2.0`. `identity.FromManifest` reads the shipped manifest so the fallback rarely binds, but the helper's whole point is that the pinned value never lies — timer's fallback matches its manifest (1.4.0).
    **Fix:** update the fallback string to `0.2.0`.

## What is already right

- Root discipline is correct: bar root is a row, panel root is a column (`view.go:12`, `view.go:50`) — both convert.
- State branches exist for error, loading, unavailable, and empty (`view.go:57-72`) — better coverage than many first-sweep panels; the error line already uses `ToneError` and the image line `ToneSubtle` (`view.go:59`, `view.go:82`), so the minor-1 tone vocabulary is in use, just incompletely.
- The Refresh button's shape (`ID`/`Text`/`Name`/`Role`/`Events`) matches aiusage's refresh button exactly (`view.go:53-54` vs `plugins/aiusage/view.go:312-315`).
- The manifest's settings block is well-formed — types, bounds, select options — and `requires.commands: ["docker"]` is honest (`manifest.json:16-20,40-75`).
- The bar tree is node-count stable across all states (one text node), trivially satisfying aiusage's bar invariant.
- A text-only bar has prior art (world-clock), so no icon is required for parity.

## UI verdict

Mini-docker is first-sweep quality, not rebuilt-plugin quality. The trees are structurally valid and the root disciplines hold, but every discipline the rebuilt cohort added after the first sweep is missing here: the bar carries no control, so the panel — the plugin's entire surface — is unreachable; the panel neither scrolls nor pads and its buttons fall below the fold on ordinary machines; hierarchy is flat (no title rung, no card rhythm, no state tones); the tooltip tree is malformed and dies silently in conversion; and the manifest over-declares a capability. The shape matches calendar's first-sweep noctalia port, not timer/aiusage. The good news is that the gap is small and shallow: one button node plus one `open` case, one list wrapper with a height, one padding field, a handful of tone/size fields, a running-first sort, and one capability deletion — none of it architectural, all of it within the existing vocabulary, mostly inside `view.go` and `manifest.json`.
