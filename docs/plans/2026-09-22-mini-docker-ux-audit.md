# Mini Docker UX audit

Date: 2026-09-22

Scope: UX-only audit of `plugins/mini-docker/` (v0.2.0) in worktree `feat/mini-docker-audit` — the user journey (bar pill → panel → actions), runtime state coverage, refresh/freshness behavior, settings comprehension, failure communication, and copy. Read-only except this file. Quality bar is the repo baseline: `plugins/timer` (first rebuilt widget: state-toned pill, panel open/close routing) and `plugins/aiusage` (newest, audit-hardened: stale-data-kept, disabled-during-load, remediation-flavored errors). Wire-protocol facts were verified against `github.com/Nomadcxx/sysc-shell@70f495e` (`plugin/v1`, `internal/plugin`, `internal/shell/pluginhost.go`); the live-failure audit lens follows `docs/plans/2026-09-22-kdeconnect-live-failure-audit-handover.md` (what a user actually experiences, states not handled, live-usability gates).

---

## Findings

**1. P1 — The panel is unreachable; the plugin's entire action surface is dead code.**
Evidence: `plugins/mini-docker/view.go:11-15` — `BarTree` renders only a bare `KindText` node, no `KindButton`, no `ID`; `cmd/sysc-plugin-mini-docker/main.go` contains no `CallPanelOpen` and no `open` input case (grep: zero panel references in the whole plugin). The shell opens panel views only when the plugin itself calls `panel.open` (`internal/shell/pluginhost.go:222,740` — bar/tooltip views open automatically at :536/:542, panels do not), which is exactly how timer routes it (`cmd/sysc-plugin-timer/main.go:85`).
User experience: clicking the "docker 3" pill does nothing. The 720×560 panel, every start/stop/restart button, and the Refresh button can never be reached. The plugin is a read-only counter that looks clickable and isn't.
Fix: wrap the pill in `KindButton` ID `open` (timer's `BarTree` is the pattern) and route its activate to `c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", ...})` in main.go. ~10 lines, no new concepts.

**2. P1 — Every refresh wipes the panel to "Loading…"; the user never sees a stable list.**
Evidence: `plugins/mini-docker/view.go:61-64` — when `loading`, `PanelTree` returns early and drops the container list entirely; `service.go:45-47` sets `loading` for the whole `List` call; `main.go:82-88` polls every interval and `service.go:83` refreshes after every action.
User experience: with the default 5 s interval the panel blanks to "Loading…" and repopulates every cycle — flicker, scroll-position loss, buttons vanishing under the cursor mid-click. If `docker ps` hangs, `listTimeout` is 30 s (`docker.go:39`), so the panel is an empty "Loading…" screen for up to 30 s. After a transient daemon hiccup, the last-known list is dropped from display even though `s.containers` still holds it (`service.go:58`), turning a 5-second outage into "the panel lost my data".
Fix: never replace data with a spinner — keep rendering the last snapshot; at most append a subtle "Refreshing…" line. On failure keep the last-known list with the error above it (aiusage's rule: stale data is kept and drawn, flagged — `plugins/aiusage/loop.go:114`, `view.go:545` "Last reading, X ago"). This is deletion: both early returns in `PanelTree` shrink.

**3. P2 — Container actions have no feedback, and their failures are erased before anyone can read them.**
Evidence: `service.go:66-83` — `Act` records the action error in `errMsg`, then immediately calls `Refresh`, which overwrites `errMsg` on success (`service.go:61`); `main.go:111-115` publishes only after both complete, so a failed action publishes a clean-looking panel. No `Disabled` on action buttons anywhere (`view.go:99-102`), no in-flight marker per row.
User experience: click Stop → nothing visibly happens for as long as docker's stop grace takes (10 s+ is normal); the row still reads "Up 2 hours" with active Stop and Restart buttons, inviting a second click that issues a duplicate stop. If the action fails (daemon gone, race with a removed container, paused container), the panel flashes back to normal as if nothing happened — the error is written and wiped between two publishes. Success is likewise silent.
Fix: keep the action error in a field `Refresh` does not clear (clear it on the next user-initiated action or successful *manual* refresh), and disable that container's buttons while its action is in flight (`Disabled` exists in the vocabulary, minor two). One snapshot field, no new UI concepts.

**4. P2 — Docker failures communicate "exit status 1"; the actual reason is discarded.**
Evidence: `docker.go:44-47` — `List` returns `Output()`'s error bare. `exec.ExitError.Error()` is `"exit status 1"`; docker's stderr ("Cannot connect to the Docker daemon … Is the docker daemon running?") is captured by `Output()` but never surfaced. The stderr-plumbing pattern already exists one function below (`runDocker`, `docker.go:75-81`). The test's fake models errors as descriptive `errors.New` (`mini_docker_test.go:68`), so this never shows up in tests. The panel then prints this in `ToneError` (`view.go:57-60`) plus a redundant generic "Docker is not available" line (`view.go:65-67`); the tooltip's "Docker unavailable" (`view.go:39`) carries no reason either.
User experience: the daemon is down; the panel says "exit status 1" — an error the user can neither understand nor act on, twice-formatted (raw + generic).
Fix: in `CLI.List`, capture stderr exactly like `runDocker` does and wrap the error with it; delete the redundant "Docker is not available" line (the real message says it all once it's real). No new state.

**5. P2 — The bar pill gives no failure signal; dead daemon is indistinguishable from "nothing running".**
Evidence: `view.go:18-34` — `BarLabel` returns the same `"docker"` for: unavailable, `status_mode: hidden`, and `running_only` with zero running. `BarTree` sets no tone; contrast with timer, where the pill's tone is the state (`plugins/timer/view.go:9-19`: accent while running, error when fired).
User experience: the daemon dies → the count silently disappears from the pill. A user with `running_only` cannot tell "docker is broken" from "my containers stopped"; a user with `always` sees "docker" where "docker 3" was, and must open the panel (currently impossible, finding 1) to learn why. The only failure surface is a tooltip the user has no reason to hover.
Fix: tone the pill `ToneError` when `!available` — one field on the existing text node, no new states, no new nodes.

**6. P2 — Clicking Refresh can freeze the whole plugin for 30 s.**
Evidence: `main.go:108-109` — the `refresh` InputEvent calls `poll()` directly on the main message loop; `poll` → `session.Refresh` → `docker.List` blocks up to `listTimeout` (30 s, `docker.go:39`). The action path already demonstrates the correct shape (goroutines, `main.go:111-115`).
User experience: docker is slow/hung, the user clicks Refresh, and the plugin stops answering: further clicks queue, settings changes stall, the pill and panel freeze. To the user the plugin is dead.
Fix: run the poll on the background goroutine like every other long operation (e.g. signal the existing poll loop, or `go poll()` — `Session` is already mutex-guarded). Smallest change: make the input handler never block on docker.

**7. P2 — Many containers overflow the panel with no scroll, and past ~170 the panel silently stops updating.**
Evidence: `view.go:73-76` renders every container from `docker ps -a` (which includes all stopped containers, `docker.go:44`) as plain column rows; the protocol's only scrollable container, `KindList` (panel-only, `plugin/v1/node.go:74,292,499`), is unused — aiusage uses it with an explicit height for exactly this (`plugins/aiusage/view.go:308-310`). Each container row costs ~6 nodes, and the wire ceiling is `MaxNodes = 1024` (`node.go:27`): past ~170 containers `Validate` fails, `Snapshot` errors, and main.go discards it (`_ = c.Snapshot`).
User experience: on a long-lived dev machine (dozens of stopped containers accumulate fast) rows past ~10 are clipped off the 560 px panel and unreachable — the user cannot start a container they can't see, and nothing says the list continues. Past ~170 the panel freezes on its last good revision with no error anywhere.
Fix: wrap the container rows in `KindList` with an explicit height (aiusage's scroll rule: it clips only with a height). YAGNI: no pagination, no search, no filtering.

**8. P2 — A publish race can crash the plugin, and the symptom is the pill silently vanishing.**
Evidence: `main.go:55-79` — `publish` mutates the `views` map and revision counters with no synchronization, and it is invoked both from the main receive loop (`main.go:103,137`) and from the action goroutines (`main.go:111-115`).
User experience: clicking Start/Stop while opening or closing the panel is a concurrent map write → runtime panic → the plugin process dies → the pill disappears from the bar with no message anywhere. Intermittent, which reads as "this plugin is flaky".
Fix: confine `publish` to the main loop — have `Act` goroutines report completion over a channel the receive loop drains. Small structural change, no behavior change.

**9. P3 — Two settings control one dimension, and the labels mislead.**
Evidence: `manifest.json:50-73` — `show_count` (bool "Show running count") and `status_mode` (select "Bar count mode") overlap: the `status_mode: hidden` case precedes the `show_count` check in `BarLabel` (`view.go:22-29`), so `hidden` ≡ `show_count: false`; `running_only` + `show_count: false` is also plain "docker" forever. The label "Bar count mode" doesn't say it only governs the count, and the option "Hidden" suggests the pill itself disappears — it doesn't; the pill stays "docker".
User experience: a user who wants the pill gone picks "Hidden" and still sees "docker"; a user setting both knobs gets outcomes that don't compose intuitively.
Fix: delete `show_count` (deletion over addition); keep one select with clear labels — "Show running count: Always / Only when containers run / Never". Defaults (`always`) are sensible. `refresh_interval_seconds` bounds 1–30 are reasonable for a `docker ps` poll; at the extremes: 1 s means a fork+exec per second (tolerable once finding 2 removes the flicker), 30 s means a count up to 30 s old (finding 10 covers saying so). No bound changes needed.

**10. P3 — No freshness contract: stale numbers are silent.**
Evidence: nothing in the plugin reports data age (contrast aiusage: "Updated 5m ago · 14:32", stale flags in error tone — `plugins/aiusage/view.go:512-514,806-808`). Opening the panel does not refresh: the `ViewOpen` handler only republishes the existing snapshot (`main.go:100-103`), so the panel can open showing a list up to `refresh_interval_seconds` (≤30 s) old with no indication, and the next scheduled refresh is what eventually updates it.
User experience: the panel says "Up 2 hours" for a container the user stopped five seconds ago in a terminal; with a 30 s interval the bar count lags visibly.
Fix (minimal): trigger one `Refresh` when a panel view opens, and rely on the short interval otherwise. An "Updated Xs ago" caption is polish, not required — the interval is short enough that an honest refresh-on-open covers the real complaint.

**11. P4 — The `notifications` capability is declared and never used.**
Evidence: `manifest.json:12` declares `notifications`; there is no `CallNotify` anywhere in the plugin (timer, aiusage, kdeconnect, screen-recorder all call it). The host grants and advertises the capability in the plugin manager (`internal/shell/popout_plugins.go:56-60` area).
User experience: none today — which is the point: the declaration is a lie of intent, and the spam question ("would a user want a notification per container state change?") answers itself as no by default.
Fix: delete `notifications` from the manifest. YAGNI: container-died notifications would be genuinely useful but are an unrequested feature with real spam/cooldown design cost (see aiusage's alert machinery); they belong on a roadmap only if the owner asks.

**12. P4 — Version drift between the handshake fallback and the manifest.**
Evidence: `cmd/sysc-plugin-mini-docker/main.go:23` pins `Version: "0.1.0"`; the manifest says `0.2.0`. Benign today because `identity.FromManifest` overrides from the shipped manifest (and the host rejects a mismatch, `internal/plugin/supervisor.go:252-256`), but the fallback string is a falsehood waiting for a layout where the manifest isn't found.
Fix: change the fallback to "0.2.0" or drop the version from the literal so the drift can't recur. One string.

**13. P4 — Copy and interaction polish (small, mostly for the UI audit).**
Evidence: `view.go:70` "No containers" is accurate for an empty `docker ps -a` but ambiguous to a user staring at three stopped containers in `docker ps` elsewhere — "No containers (docker ps -a)" overstates it; plain "No containers" plus the visible list behavior is acceptable once finding 2 lands. `TooltipText` pluralizes correctly including 0/1 (`view.go:37-45`) — good. Container names and images are rendered unbounded with no `MaxWidth`/truncation (`view.go:81-82`); long composite names ("a,b,c" multi-name containers) will strain the row. The panel header has no close button (timer has one, `timer/view.go:106-107`); if the host's outside-click/Esc dismissal covers it, fine — verify live rather than build one.
Fix: nothing structural; fold truncation into the UI audit's layout pass.

---

## What NOT to build (YAGNI calls)

- **Notifications for container lifecycle events** — unrequested, spam-prone, and the correct version (cooldowns, thresholds) is a project, as aiusage's alert machinery shows. Delete the capability instead (finding 11).
- **Logs viewer, exec/terminal, compose support, port mappings, per-container stats (CPU/mem)** — each doubles the plugin's scope; "mini" is the product. The lifecycle trio (start/stop/restart) plus an honest list is the whole spec.
- **Filtering/search/sorting options** — once `KindList` makes long lists reachable (finding 7), sorting is docker's own order (newest first), which is what `docker ps` users already expect.
- **Confirmation dialogs for Stop/Restart** — a stop is reversible with Start; a modal per click would make the panel feel like a corporate admin tool. The in-flight disabled state (finding 3) is the right-sized safety.
- **Settings beyond the three** — the redundant one should die (finding 9), not spawn siblings.
- **A retry/backoff subsystem for polling** — the fixed interval plus keep-last-known (finding 2) covers the user-visible behavior; scheduler sophistication is invisible from the bar.

## UX verdict

Not usable on a live machine today. The blocker is total: the bar pill has no activate target and the plugin never calls `panel.open`, so the panel — the only place anything can be done — cannot open at all; everything below it is unreachable code until that lands. The second gate is the refresh design: as built, the panel wipes itself to "Loading…" on every cycle and every action, which on the default 5 s interval means the user effectively never sees a stable, clickable list, and a hung `docker ps` blacks the panel out for 30 s while freezing the whole plugin if Refresh was clicked (findings 2, 6). Third, failure communication is currently worse than silence in two places: docker's real error text is thrown away in favor of "exit status 1" (finding 4), and action failures are erased before the next publish (finding 3).

Top gates for "usable on the owner's machine": (1) clicking the pill opens the panel; (2) the panel keeps its list and scroll position while refreshing, including during a slow or hung `docker ps`; (3) a failed docker command leaves a readable, actionable error on screen — the daemon-down message, not "exit status 1"; (4) a machine with more containers than fit 560 px can still reach every container, and past ~170 the panel doesn't silently freeze. Findings 5 and 8 (failure tone on the pill, publish race) should ride the same tranche — they are small and both manifest as "the plugin mysteriously looks dead". After those, what remains is honest-polish work (settings dedup, freshness caption, capability deletion), and the plugin matches the bar timer and aiusage have already set.
