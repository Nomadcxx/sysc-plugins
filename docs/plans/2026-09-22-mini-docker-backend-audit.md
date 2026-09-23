# Mini Docker — Functionality / Backend Audit

**Date:** 2026-09-22
**Worktree:** `.worktrees/feat/mini-docker-audit` (branch `feat/mini-docker-audit`, base `405c0f9`)
**Scope:** Read-only functionality, backend, security, and test-coverage audit of `org.sysc.mini-docker` v0.2.0, every line of `plugins/mini-docker/{manifest.json,docker.go,service.go,view.go,mini_docker_test.go}` plus `cmd/sysc-plugin-mini-docker/main.go` (note: the `cmd/` entry lives at repo root, not under `plugins/mini-docker/`). Correctness of the docker CLI invocation, the service lifecycle and state, plugin/v1 protocol wiring, security posture, coverage against the repo's `tests/integration/` gate conventions, and a ponytail shrink pass. Findings are evidence-based against the pinned `github.com/Nomadcxx/sysc-shell` protocol source (`plugin/v1`).

---

## Findings

### 1. [P1] The 720x560 panel is unreachable — no view ever opens it, and resync is ignored
**Evidence:** `view.go:11-15` — `BarTree` returns a `KindRow` holding a single non-interactive `KindText`; it has no `ID`, no `Events`, no `KindButton`. `main.go:101-116` handles `ViewOpen`, `ViewClose`, and `InputEvent`, but **never calls `v1.CallPanelOpen`** anywhere in the file. `main.go:98-139` has no `*v1.ViewResync` case at all. By contrast every other plugin that owns a panel wires an interactive `"open"` bar node and calls `CallPanelOpen` on activate (world-clock `view.go:13-17` + `main.go:129`; timer `view.go:31` + `main.go:85`; notes `view.go:29` + `main.go:162`; kdeconnect `view.go:128` + `main.go:164`; screen-recorder, aiusage). In this shell the *only* path that opens a plugin panel is the plugin's own `CallPanelOpen` host call (`internal/plugin/hostcall.go:137-141,183-200`, `internal/shell/pluginhost.go:222`); the bar never opens plugin panels on its own. `PanelTree` is therefore dead in production.
**Impact:** The plugin's advertised panel (`manifest.json:32-39`) never renders. Container start/stop/restart buttons and the container list are inaccessible. The feature is effectively a read-only count pill.
**Fix (ponytail):** Make the bar pill a single interactive button exactly like world-clock: `BarTree` returns a `KindRow` → `KindButton{ID:"open", Key:"docker", Role:"button", Events:[activate]}`; add `case m.Node == "open": _, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry:"panel", Output:m.Output, Instance:m.ViewID})` to the `InputEvent` switch. That is ~6 added lines and one changed literal; no new abstraction. Also add `case *v1.ViewResync: publish()` so a dropped snapshot recovers (`internal/plugin/view.go` behavior every peer implements).

### 2. [P1] Concurrent map/state races: `publish` runs from three goroutines while the main loop mutates the same state
**Evidence:** `main.go:55-74` — `publish` iterates and writes the `views` map. It is invoked from: (a) the poll goroutine (`main.go:81-91`, `poll` → `publish`, line 78), (b) the main event loop on `ViewOpen` (`main.go:103`), and (c) detached action goroutines (`main.go:111,113,115`). The main loop simultaneously writes `views` on `ViewOpen`/`ViewClose` (`main.go:102,105`) and writes `settings` on `SettingsChanged` (`main.go:117-138`), while the poll goroutine reads `settings.interval` (`main.go:87`) and the poll/action goroutines read `settings.showCount/statusMode` inside `publish` (`main.go:58`). `v1.Client`/`Encoder` have **no write mutex** (`plugin/v1/framing.go:71-106`; `Client.mu` guards only the call-wait map, `client.go:14-22,78-79`), so concurrent `c.Snapshot` calls interleave writes on one stdout pipe. `make test` runs `go test -race ./...` (`Makefile:32-33`), but the cmd entry has **no test files** so the race is never executed.
**Impact:** `fatal error: concurrent map read and map write` (or `concurrent map writes`) can crash the plugin at any refresh coinciding with a settings change / view open / view close. Silently, interleaved encoder writes can corrupt a frame and disconnect the plugin.
**Fix (ponytail):** Confine all state mutation and all sends to the single main loop. Have the poll and action goroutines send a completion signal (a small `chan struct{}` / `chan result`) and let the `select` call `publish()`. This deletes the detached `go func(){...; publish()}` wrappers and the concurrent `publish` paths rather than adding a lock — net line-negative and removes the race by construction. Alternatively guard `views`+`settings`+encoder with one mutex, but that is more code.

### 3. [P2] Docker daemon-down vs docker-missing is not distinguished; the user-visible error is "exit status 1"
**Evidence:** `docker.go:44-47` — `exec.CommandContext(...).Output()` returns **stdout only**; on failure stderr is discarded and `err` is a bare `*exec.ExitError` whose `Error()` is `"exit status 1"`. `service.go:55` stores `err.Error()` into `errMsg`, which the panel paints verbatim (`view.go:57-60`). So "Cannot connect to the Docker daemon" is never shown; the user sees "exit status 1". A missing binary does surface (`*exec.Error` → "executable file not found"), but the daemon case — the common one — does not.
**Fix (ponytail):** In `List`, attach a `strings.Builder` to `cmd.Stderr` and use `cmd.Output()`/`cmd.Run()`, then `fmt.Errorf("%w: %s", err, stderr)` exactly as `runDocker` already does (`docker.go:75-83`). Reuse that shape instead of reinventing it; ~4 lines.

### 4. [P2] Action failures are erased immediately by the refresh that follows
**Evidence:** `service.go:66-83` — `Act` records `s.errMsg = err.Error()` on failure (lines 78-82) and then unconditionally calls `s.Refresh(ctx)` (line 83). `Refresh` sets `s.errMsg = ""` whenever the list succeeds (`service.go:61`). A failed `docker stop` on a still-listable daemon therefore clears its own error before any view renders it.
**Fix (ponytail):** Keep one error field for list state and surface action errors through it only when the follow-up list also fails, or set `errMsg` *after* `Refresh` on action failure. Minimum change: call `Refresh` first, then if `err != nil` overwrite `errMsg`. ~3 lines, no new type.

### 5. [P2] `start`/`stop`/`restart` have no timeout
**Evidence:** `docker.go:75-83` — `runDocker` calls `exec.CommandContext(ctx, ...)` with the caller's context, which in `main.go` is the long-lived root context (`main.go:26-27`, passed at `main.go:111-115`) cancelled only at shutdown. A wedged `docker stop` (daemon hang, container refusing SIGTERM) can block an action goroutine indefinitely; with the #2 fix it would block the main loop. `List` has `listTimeout` (`docker.go:39-43`) but the mutating calls do not.
**Fix (ponytail):** Wrap inside `runDocker` with its own `context.WithTimeout(ctx, actionTimeout)` mirroring `listTimeout`; ~3 lines, reuses the existing pattern.

### 6. [P2] Changing only `refresh_interval_seconds` never republishes, and the setting is read across goroutines
**Evidence:** `main.go:117-138` — the interval branch (lines 119-123) validates and assigns `settings.interval` but never sets `changed = true`, unlike the `show_count` (126-128) and `status_mode` (130-135) branches. Independently, the assigned field is read by the poll goroutine at `main.go:87`, which is the cross-goroutine read in finding #2. Also note the manifest declares this as `type:"int"` (`manifest.json:42-48`) while the handler requires `raw.(float64)` (`main.go:120`) — this matches the JSON-number wire encoding and other plugins (kdeconnect `main.go:334`), so the type check itself is correct.
**Fix (ponytail):** Set `changed = true` in the interval branch and, per #2, route the reassignment through the main loop so the ticker can be rescheduled with `time.NewTicker`/reset instead of a raced read. Minimum version: read interval through an atomic or move the timer into the main-loop select.

### 7. [P2] `notifications` capability is declared but never used
**Evidence:** `manifest.json:11-15` requests `["notifications","panels","settings"]`; a repo-wide check finds **no `v1.CallNotify`/`NotifyParams`** in `cmd/sysc-plugin-mini-docker/main.go` or `plugins/mini-docker/` (grep returns zero matches in the plugin; every other notifying plugin calls `CallNotify`, e.g. kdeconnect `main.go:297-303`). For contrast, kdeconnect's panel *is* used so its `panels` grant is earned, but its notification surface exists.
**Fix (ponytail):** Either delete `"notifications"` from the manifest (one line, least privilege — preferred until an action-failure toast is actually wired per #4) or implement the action-failure notification that #4 implies. Do not keep an unused grant.

### 8. [P3] `status_mode: "hidden"` does not hide anything
**Evidence:** `view.go:18-34` — `BarLabel` returns the literal `"docker"` for `hidden` (lines 22-24) and the host always renders the bar view it was given; nothing in `view.go` or `main.go` suppresses the pill. The manifest labels the option "Hidden" (`manifest.json:69-72`).
**Fix (ponytail):** Either make `hidden` return an empty/near-zero tree (a bar view with no text) or, if the shell has no "hide widget" affordance, rename the option to reflect what it does. Minimum: document intent and emit a minimal node; no new API.

### 9. [P3] Echoed container IDs are passed to argv unvalidated
**Evidence:** `main.go:110-115` slices the node string after the `start:`/`stop:`/`restart:` prefix and hands it straight to `docker.go:63-73`. The node value comes from the host (`InputEvent.Node`, `plugin/v1/message.go:196-211`), which in normal operation echoes the ID the plugin emitted in `view.go:87-92` — but the plugin re-derives nothing and never checks the ID still exists in the snapshot. Because argv (not a shell) is used, there is no shell-injection risk, and docker IDs are hex so they cannot begin with `-`; the exposure is a stale/forged node running e.g. `docker stop --help` or acting on a container that vanished. The kdeconnect plugin demonstrates the disciplined pattern, resolving IDs against the current snapshot before acting (`main.go:266-276`).
**Fix (ponytail):** Resolve the ID against `session.Snapshot()` before dispatch, or reject ids outside `[0-9a-f]{12,64}`; ~4 lines, no new dep.

### 10. [P3] Unbounded node count: many containers silently exceed `MaxNodes` and the snapshot is dropped
**Evidence:** `view.go:73-75` emits one `containerRow` (≈5 nodes: column, 2 texts, row, buttons) per container. `plugin/v1/node.go:27` caps a view at `MaxNodes = 1024`, enforced by `Validate`; an oversize tree is rejected host-side and the panel stops updating with no plugin-side error. ~200 containers crosses the cap.
**Fix (ponytail):** Cap the rendered list to a fixed maximum (e.g. 100) and append a "... N more" text row; ~5 lines. Do not add pagination unless the UX audit calls for it.

### 11. [P3] Per-tick timer allocation via `time.After` in a loop
**Evidence:** `main.go:87` — `case <-time.After(settings.interval):` inside `for`. Each iteration allocates a timer that lingers until it fires; on shutdown the pending timer is not stopped. Combined with the dynamic interval this is the one place a ticker is not naturally usable.
**Fix (ponytail):** Move to `time.NewTicker` with a reset on settings change (interacts with #2/#6), or accept the allocation with a `// ponytail:` note. Low urgency.

### 12. [P3] Dead field in the local view struct
**Evidence:** `main.go:30-34` declares `instance string`; it is set at `main.go:102` and never read. (`instance` is not needed: panel-open already passes `Instance: m.ViewID`.)
**Fix (ponytail):** Delete the field and the `instance: msg.Instance` assignment; −2 lines.

### 13. [P4] Stale hardcoded fallback identity
**Evidence:** `main.go:23` falls back to `Version: "0.1.0"` while the manifest is `0.2.0` (`manifest.json:5`). `identity.FromManifest` normally reads the manifest (`internal/identity/identity.go:21-46`), but on `go run`/odd layouts the plugin announces the wrong version and the host's mismatch check (`message.go:41-48`) can reject it.
**Fix (ponytail):** Bump the fallback to the current manifest version, or omit the fallback by reading the manifest unconditionally. One-line change; the pattern is repository-wide, so raise it centrally if audited elsewhere.

### 14. [P4] `docker` resolved through ambient PATH; stdout read unbounded
**Evidence:** `docker.go:44,76` — `exec.CommandContext(ctx, "docker", ...)` trusts `PATH`; a shadowing `docker` earlier in the user's `PATH` executes with the plugin's privileges. `.Output()` (line 44) buffers all stdout with no size cap. These are standard for CLI-wrapper plugins (kdeconnect likewise shells `xdg-open`, `kdeconnect-cli`) and are mediated by the user's own environment, so this is a note rather than a defect.
**Fix (ponytail):** None required now. If hardened later, a manifest `requires.commands:["docker"]` check plus an absolute-path lookup is the minimum; do not add filesystem walking.

### 15. [P4] Test reimplements the parser instead of testing it
**Evidence:** `mini_docker_test.go:41-65` duplicates the exact line-split/trim/unmarshal loop from `docker.go:49-59` inside the test (`TestCLIParsesDockerJSONLines`). It therefore cannot fail if production parsing regresses; it only locks the assumed docker output shape.
**Fix (ponytail):** Extract `parseContainers([]byte) []Container` in `docker.go` and have both `List` and the test call it. This is a stdlib-first shrink: add ~4 lines to production, delete ~11 lines of duplicated test logic, and gain real coverage.

---

## Test-coverage map and gate-test recommendation

**What existing tests actually cover (`mini_docker_test.go`):**

| Behavior | Test | Notes |
|---|---|---|
| docker JSON-line contract (shape only) | `TestCLIParsesDockerJSONLines` (41-65) | Duplicates the parser; does **not** exercise `CLI.List` |
| `Session.Refresh` failure → unavailable + tooltip | `TestSessionRefreshUnavailable` (67-80) | Uses `fakeDocker` |
| `Refresh` success + `RunningCount` | `TestSessionRefreshAndRunningCount` (82-94) | |
| `Act` dispatch + unknown action ignored | `TestSessionActRunsActionAndRefreshes` (96-110) | Uses `fakeDocker`; does not cover the error path |
| `BarLabel` mode matrix | `TestBarLabelModes` (112-131) | Good breadth |
| `PanelTree` + `BarTree` validate | `TestPanelTreeValidate` (133-150) | Validation only; not reachability |

**Critical untested paths:**
- `CLI.List` real argv (`docker ps -a --format {{json .}}`), exit-code handling, and stderr classification (#3).
- `runDocker` / `Start`/`Stop`/`Restart` argv and timeout (#5).
- `Session.Act` **error** path and the follow-up-refresh overwrite (#4).
- Panel reachability: any bar `"open"` node and `CallPanelOpen` (#1) — currently impossible to test because it does not exist.
- `ViewResync` handling (#1).
- `SettingsChanged` application, including the interval branch (#6) and the declared-vs-used capabilities (#7).
- Concurrency: the `-race` suite never loads the cmd entry, so #2 is invisible to CI.
- `MaxNodes` overflow (#10).
- The entire `cmd/sysc-plugin-mini-docker` entry has **no test files** (confirmed: only `plugins/mini-docker/*_test.go` exist; no `cmd/.../main_test.go`).

**Gate test to write first (follows `tests/integration/plugin_kdeconnect_gate_test.go` + `harness_test.go`):**
Add `tests/integration/plugin_mini_docker_gate_test.go` with `TestPluginMiniDockerGateServesViewsAndSurvivesInput`. It builds `./cmd/sysc-plugin-mini-docker` into a temp plugin dir beside a copied `manifest.json` (kdeconnect `startKDEConnect`, lines 94-113), sends `HostHello{Supported:[{1,2}], Plugin:{ID:"org.sysc.mini-docker",...}, Capabilities:["panels","settings"], Limits:DefaultLimits}`, and drives a scripted host that replies `OK` to every `HostCall` (kdeconnect lines 159-164; add a record of `CallPanelOpen` so the panel-open assertion has evidence, as `pluginhost_test.go:399` does). Flow:
1. `openBar()` → `waitView("bar-1", ...)` asserting an interactive `ID:"open"` node exists — **this fails today and is the red test for finding #1.**
2. `clickOn("bar-1","open")` → assert the recorded host calls contain `panel.open` with `Entry:"panel"`.
3. `openPanel()` → `waitView("panel-1", miniDockerPanelLegal)` where legal accepts "Docker is not available" (no docker on CI), "No containers", or a populated list — the kdeconnect daemon-optional pattern (lines 66-83).
4. `clickOn("panel-1","refresh")` → panel still legal.
5. `SettingsChanged{Scope:ScopePlugin, Values:{"refresh_interval_seconds":float64(1),"show_count":false,"status_mode":"running_only"}}` → bar view re-serves; and a second change to only `refresh_interval_seconds` must also move a revision (red test for #6).
6. `ViewResync{ViewID:"panel-1"}` → revision advances (red test for #1's resync gap).
7. Both views remain served by the one process, then `HostShutdown` and clean exit within the 2s cleanup window (kdeconnect lines 143-152).
Run the gate under `go test -race` so the scripted-host concurrency in #2 is exercised. This single file also gives the previously-untested `cmd/` entry its first mechanical coverage and earns the `panels`/notification capability claims.

---

## Ponytail shrink ledger

| # | Item | Category | Evidence | Est. lines |
|---|---|---|---|---|
| S1 | Delete dead `instance` field + assignment from local `view` struct | delete | `main.go:30-34,102` | −2 |
| S2 | Extract `parseContainers`; test calls it instead of copying the loop | stdlib / shrink | `docker.go:49-59`, `mini_docker_test.go:44-55` | +4 / −11 = −7 net |
| S3 | Collapse the three near-identical `start:`/`stop:`/`restart:` action goroutines into one table-driven dispatch | shrink | `main.go:110-115` | −4 |
| S4 | Route poll/action completion through the main loop, deleting detached `publish` goroutines and the raced `settings` read | shrink (also fixes P1 #2) | `main.go:55-91,106-116` | −2 (net-neutral once the completion channel is added; removes race by construction) |
| S5 | Reuse `runDocker`'s stderr pattern in `List` rather than a bespoke error path | shrink | `docker.go:44-47` vs `75-83` | ±0 (quality) |
| S6 | Remove `"notifications"` capability until used | delete | `manifest.json:11-15` | −1 |
| | **Net** | | | **≈ −16 lines** |

Net effect after the P1/P2 fixes is roughly line-neutral (fixes #1 and #5 add ~9 lines) but the *deletions* above offset them; nothing in this ledger requires a new dependency or a new type.

---

## Backend verdict

**Does it work on a live machine with docker installed?** Partially — it will show a fragile, misleading count and a useless panel. The bar label math is correct and the container parse matches `docker ps -a --format '{{json .}}'`, so the pill will render "docker N" on a machine with docker and degrade to "docker" when the daemon is down. But the plugin's headline feature — the 720x560 management panel — **cannot be opened at all** (finding #1), and the plugin is one concurrent refresh away from a hard crash (finding #2) because the entire `publish` path runs unsynchronized from three goroutines against state the main loop mutates with no lock. On a machine where docker is slow or the user toggles a setting while a refresh is in flight, the crash is reproducible. It has never been run live (per the workplan) and the cmd entry has no tests, so neither defect has surfaced.

**Top functional risks, ranked:**
1. **#1 panel unreachable** — the primary surface is dead; the plugin is a count-only pill in practice.
2. **#2 unsynchronized `views`/`settings`/`encoder`** — `fatal error: concurrent map read and map write` crashes the plugin; masked only by the absence of any test on the entry point.
3. **#3 + #4 misleading/erased errors** — daemon outages read as "exit status 1", and failed start/stop/restart never reach the user, so the panel cannot explain what went wrong even once it opens.
4. **#5 missing action timeout** — a wedged `docker stop` blocks an action goroutine indefinitely (and, after the #2 fix, the whole loop).
5. **#6 interval settings path** — changing only the refresh interval silently no-ops and races the poller.
6. **#10 unbounded container list** — a busy host silently exceeds `MaxNodes` and the panel freezes.
