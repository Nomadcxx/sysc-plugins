# Mini Docker development tranche — independent review

**Date:** 2026-09-22
**Worktree:** `.worktrees/feat/mini-docker-audit` (branch `feat/mini-docker-audit`, base `405c0f9`)
**Scope reviewed:** `405c0f9..145e3a6` (T0 → T4), files `plugins/mini-docker/{view,service,docker,manifest.json,mini_docker_test,live_test}`, `cmd/sysc-plugin-mini-docker/{main,main_test}.go`, `docs/plans/*`.
**Protocol ground truth:** `github.com/Nomadcxx/sysc-shell@v0.0.0-20260920172831-70f495ebb798/plugin/v1` plus the host implementation `internal/plugin/{view,hostcall,supervisor}.go` and `internal/shell/pluginhost.go` from the same pinned module.
**Method:** every file read in full (not just the diff); every claim cross-checked against the module-cache protocol sources. Findings ranked P1 (blocker / integrity) → P3 (polish) with file:line.

## Verification limitation (read this first)

This review session had **no shell/exec tool** in its Code Mode catalog and **subagent nesting was disabled** (`Subagent depth limit reached (1)`), so I could **not** run `go test -race`, `go vet`, `gofmt -l`, or the `live` tag test. All conclusions below are **static**, derived from reading the code and the pinned protocol/host sources in the Go module cache.

The task statement says "Inside the worktree everything is green." I could not independently reproduce that. Treat the test-evidence claims below as "pinned by inspecting the test bodies", not "observed PASS". If any finding hinges on runtime behaviour, it is marked as such.

---

## Verdict

**Verdict: fix-first** — one documentation correction (P1) and two small code fixes (P2) should land before the tranche is declared done. **No blocking correctness or security defect was found in the shipped code**; the argv path is safe, the shared-state mutex discipline is sound for `views`/`settings`, and the core tests genuinely pin the behaviours they claim. The "green live gate" claim in the workplan is, however, materially at odds with the committed `live_test.go`.

---

## Findings

### P1 — The workplan's T4 verification claim does not match the committed live test

**What:** `docs/plans/2026-09-22-mini-docker-audit-workplan.md:60` states:

> T4 verification: live gate `go test -tags live ./plugins/mini-docker/` passes against the real daemon (**11 containers, 6 running, all trees validate**); ...

But `plugins/mini-docker/live_test.go` only *logs* the counts it observes and asserts nothing numeric:

- `live_test.go:50-52` — `t.Logf("live: %d containers, %d running", len(containers), running)`.
- `live_test.go:35-42` — the only hard assertions are `available==true`, the session state fields are zero, and `len(containers) != 0` (else `t.Skip`).
- There is **no `11` and no `6`** anywhere in the test.

**Why it matters:** "11 containers, 6 running" is a snapshot of one machine's state, not a property of the test. A reader of the workplan reasonably concludes the live test encodes those numbers; it does not, and a daemon with a different container count would still pass. The roadmap (`roadmap.md:66`) correctly says "**live SSH acceptance on the docker laptop**" — a manual gate — so the workplan line reads as if an automated gate produced the numbers. This is a truthfulness defect in the tranche's own verification artifact, which is exactly the class the tranche set out to eliminate.

**Fix (one line):** Reword T4 to separate the two: "automated live gate (`-tags live`) passed — daemon reachable and all three trees validate; observed 11 containers / 6 running on the audit laptop (logged, not asserted)."

---

### P2 — Encoder writes are not serialized for every sender: `c.Call` bypasses the mutex

**What:** `cmd/sysc-plugin-mini-docker/main.go:69-90` holds `mu` across the whole `publish` (including every `c.Snapshot`), which correctly serializes snapshots among the poller and all action goroutines. But `main.go:139-140` calls `c.Call(ctx, v1.CallPanelOpen, …)` **directly on the main loop, outside `mu`**. `v1.Client`'s own mutex guards only the call-wait map, not the encoder:

- `plugin/v1/client.go:79` — `func (c *Client) Send(m Message) error { return c.enc.Encode(m) }` (no lock).
- `plugin/v1/framing.go:85-105` — `Encoder.Encode` does a single `e.w.Write(line)`, documented as atomic *against a pipe reader*, not against a concurrent writer.
- `plugin/v1/client.go:18-22` — `Client.mu` guards `wait` only.

So a panel-open click that coincides with any publish lets two goroutines write frames to the same stdout pipe. The host reads that pipe through **one** decoder (`internal/plugin/supervisor.go:153,170` stdout pipe → `v1.NewDecoder(stdout, ToHost)`; single consumer in `internal/shell/pluginhost.go:280-295`), and a spliced/partial line surfaces as `plugin/v1: unknown message type` or `malformed message` (`framing.go:164-194`), which breaks the reader loop and fails the session.

**Why it matters:** This is the exact concurrency class T0 claimed to close ("one sync.Mutex … all concurrent publish paths"). The window is narrow (only `Call`-vs-`Snapshot`, because snapshot-vs-snapshot is serialized by `mu`), but it is real: open the panel while the 5 s poll or an action's trailing publish is in flight. This is the plugin-side half of sysc-**496** ("encoder write mutex", filed by this very tranche per `roadmap.md:75`); the roadmap acknowledges the shell-side hole but the plugin still creates a second writer.

**Fix (smallest):** Make **all** client I/O go through one send mutex: a `sendMu sync.Mutex` wrapped around `c.Send` / `c.Call` in `main.go` (`3 lines), or move the panel-open dispatch inside the existing locked region. Do this locally rather than relying on the shell-side sysc-496 fix having shipped.

---

### P2 — Missing `ViewResync` handling is a latent gap, not a present defect (answered fully in Q5)

**What:** `main.go:120-177` has no `case *v1.ViewResync`; the type exists in the protocol (`plugin/v1/message.go:148-155`, registry `framing.go:53`) and the reference weather plugin handles it (`cmd/sysc-plugin-weather/main.go:149-154`, resetting `rev=0` then republishing). The host emits `view.resync` in exactly two places, both in the **view-patch** path:

- `internal/shell/pluginhost.go:343-347` — a patch arrived but there is no tree yet.
- `internal/shell/pluginhost.go:355-358` — `ApplyPatch` returned `resync=true` (base revision mismatch, or the tree failed validation), documented at `internal/plugin/view.go:67-79` and `message.go:148-153`: "asks the plugin to send a snapshot after the host dropped or rejected a patch."

**Why it matters:** This plugin only ever sends `ViewSnapshot`, never `ViewPatch`, and its per-view revision is monotonic because every publish is serialized under `mu`. The host's `ApplySnapshot` path (`pluginhost.go:314-331`) does **not** gate on revision, so the host has no reason to emit a resync today. The gap is latent: it becomes a real defect the moment the plugin adopts `ViewPatch` (the natural next optimisation, mirroring weather), at which point an ignored `view.resync` leaves the view stuck on a rejected base. It is also a cheap robustness win now.

**Fix (one line, mirrors weather):** add under `mu` in the main loop:

    case *v1.ViewResync:
        if v, ok := views[msg.ViewID]; ok {
            v.rev = 0
            views[msg.ViewID] = v
        }
        publish()

Setting `rev=0` intentionally regresses the revision so the host treats the next snapshot as a fresh base — that is what weather does and what the host's `awaiting` flag expects.

---

### P2 — Panel can freeze after a concurrent action (stale revision, no resync) — user-reachable, recovers by close/reopen

**What:** `main.go:150-155` spawns a detached goroutine per action activation; each does `session.Act` then `publish()`. If two activations race — e.g. the user clicks "Stop" on container A and "Start" on container B before the first publish repaints the disabled state — both goroutines run. Their `publish` calls serialize on `mu`, but the host's view state and the plugin's revision are updated at different times: the host stamps `v.Revision` in `applyResult` (`pluginhost.go:380`) when the *prepared* result is applied, while the plugin's counter advances at send time. A snapshot can be applied by the host out of order relative to the plugin's own counter when the preparer coalesces (newest-wins, `internal/plugin/prepare.go:98-101,153-170`), producing a host revision behind the next snapshot's revision.

**Why it matters:** When that happens the host rejects the snapshot and — because the plugin ignores `view.resync` — **that view stops updating** until the user closes and reopens the panel (a fresh view id), or the plugin restarts. The user sees frozen container state and buttons that appear to do nothing. This is a second, independent reason the resync case is worth adding; it also means the "buttons disabled while acting" mitigation (`view.go:138`, `main.go:150-155`) does not actually prevent double-fires in the window before the first disable-publish lands.

**Fix:** The resync case above resolves the freeze. To also narrow the double-fire window, resolve the action against the live snapshot before dispatch (see P3 on stale IDs) and/or set `actingID` in the main loop before spawning the goroutine. The minimal, boring version is just the resync case.

---

### P3 — "+N more" renders above the list, not below it

**What:** `view.go:113-121`: when `len(rows) > maxPanelRows` the overflow text is appended to `col.Children` **before** the `KindList` is appended, so the render order is `[header, +N more, list]` — the "footer" (`view.go:116`, comment at `view.go:125-127`) paints as a **header**.

**Why it matters:** Cosmetic/UX: the summary sits where a title would, above a 400 px scroll region. The test's `+N more` walk (`mini_docker_test.go`, `TestPanelListScrollsAndCaps`) only searches for the text anywhere in the tree, so it does not catch the placement.

**Fix:** Build and append the list first, then append the overflow node after it; or state "header" in the comment. One reorder.

---

### P3 — `CLI.List` parsing is still only tested by a copy of itself

**What:** The backend audit (finding #15, `2026-09-22-mini-docker-backend-audit.md:69-71`) asked for a `parseContainers` extraction so the test exercises production parsing. `TestCLIParsesDockerJSONLines` (`mini_docker_test.go:86-109`) **still re-implements** the split/trim/unmarshal loop from `docker.go:76-88`; the production loop remains uncovered. T3 did not claim this item, so it is not a broken promise — but it is a standing test-quality gap: `CLI.List`'s real argv, its `LimitReader` boundary, and its malformed-line skip (`docker.go:83-85`) have no test.

**Why it matters:** `docker.go:83-85` silently *drops* any line that fails to unmarshal. A docker format change (or a truncated read at the 1 MiB limit producing a partial JSON line, `docker.go:69`) would make containers vanish with no error — exactly the "silent drop" class this tranche was chartered to remove, and no test would notice.

**Fix:** Extract `parseContainers([]byte) []Container` in `docker.go` and call it from both `List` and the test (`≈+4/−11` net, per the original ledger). Optional: surface a `listErr` when >0 lines were dropped so truncation/corruption is not silent.

---

### P3 — Full republish of `900 nodes every tick, no change detection

**What:** `publish` (`main.go:69-90`) unconditionally re-renders and re-sends **every** view on **every** poll (default every 5 s) and every action. The panel tree is `≈6×150 + header ≈ 905` nodes (`view.go:112-119`, cap at `view.go:127`). The host's per-plugin budget is `UpdatesPerSecond: 60` (`plugin/v1/message.go:77`). The reference weather plugin avoids this with `sameWeather` change-detection + `ViewPatch` (`cmd/sysc-plugin-weather/main.go:98-114,123-129`).

**Why it matters:** CPU/battery on a shell that repaints on every frame; also more opportunity for the encoder race above. Not a correctness bug at the default 5 s cadence, but the roadmap's own "busy host" gate (`roadmap.md:66`) is served by a cap that is *not* changed by the 60 Hz ceiling.

**Fix (ponytail: YAGNI unless measured):** add a cheap equality check before republishing (compare `available`, `loading`, `listErr`, `actErr`, `actingID`, a container-list hash, and the settings that affect text), or adopt `ViewPatch` + resync. Do **not** do this speculatively; record it as the known ceiling. If nothing else, add a `ponytail:` note on `publish` naming the no-change-detection cost and the patch upgrade path.

---

### P3 — Stale container IDs are accepted; `ParseAction` validates shape, not existence

**What:** `view.go:172` allow-lists the ID half to `^[A-Za-z0-9][A-Za-z0-9_.-]*$` and `ParseAction` (`view.go:176-187`) rejects anything else before the ID reaches argv (`docker.go:91-101`). That is the security-relevant half and it is correct (see Q2). What is *not* checked: the ID still exists in the current snapshot. A stale row (container removed since last poll) dispatches `docker start <gone-id>`, producing a daemon error that lands in `actErr`. The backend audit (#9) noted this and suggested resolving against the snapshot, as kdeconnect does.

**Why it matters:** Low: argv invocation means no shell injection, the allow-list blocks option-like IDs, and the daemon error is now shown (T1). It is a UX sharp edge, not a security hole.

**Fix:** After `ParseAction`, confirm the ID is in `session.Snapshot()` before spawning; `4 lines, no dep. (Also closes the stale-action half of the P2 freeze path.)

---

### P3 — Tests that can pass on broken code, and a fragile canary

**What:**
- `TestPanelDisablesButtonsWhileActing` (`mini_docker_test.go`): it walks the tree for `Disabled` buttons and fails only if a disabled node belongs to a *different* container. If the view disabled **every** button (over-disable bug), the loop over `disabled` still passes (all disabled nodes are the acting container's), so the defect is not caught. It also does not assert that some button is **enabled**.
- `TestRefreshClickDoesNotBlockMainLoop` (`cmd/sysc-plugin-mini-docker/main_test.go`): the drain goroutine decodes `Root.Children[0].Text` from **any** `view.snapshot` and arms/fires on the bar text. Today only a bar view is opened, so it is safe, but the assertion is coupled to the bar being the first child of whatever view arrives; a future panel view in the same harness would make `text` empty/other and could silently disarm.

**Why it matters:** Both are "would pass on broken code" risks. The race storm (`TestRunConcurrentTraffic`) and the erasure tests are genuinely strong; these two are the weak points.

**Fix:** Assert `len(disabled) > 0 && len(enabled) > 0` in the disables test; in the canary, filter snapshots to `view_id == "v1"` (the bar) before inspecting.

---

### P3 — Manifest protocol minor understates the fields used; handshake always claims 1.0

**What:** `manifest.json:6-9` declares `protocol.minor: 1`, but the plugin uses `Padding`, `Gap`, `Height`, `Bold`, `Size`, `PinEnd`, and `Disabled` — all of which the protocol documents as **minor two or later** (`plugin/v1/node.go:142-147,194-199`). Independently, the client always answers the handshake with `Version{Major:1, Minor:0}` (`plugin/v1/client.go:43-45`), so the host records the plugin as speaking 1.0. The host's validator/converter honour the fields regardless (`internal/plugin/view.go:276-340,429-433`), so nothing breaks today.

**Why it matters:** Metadata hygiene only; the manifest minor is informational (`internal/plugin/manifest.go:382-383` only checks non-negative). It is also a **repo-wide** pattern, not mini-docker-specific.

**Fix:** If the project wants honest protocol metadata, bump the manifest minor to match the highest field in use and (centrally) have `Client.Handshake` accept the negotiated minor. Otherwise leave it and note that protocol.minor is documentation. Out of scope for this tranche.

---

## Answers to the five explicit questions

### (1) Is the mutex discipline sound?

**For `views` and `settings`: yes. For the shared stdout encoder: no.** Every access to `views` and `settings` I could find is under `mu`:
- poller reads `settings.interval` under `mu` (`main.go:106-108`);
- `ViewOpen` / `ViewClose` mutate `views` under `mu` (`main.go:129-136`);
- `SettingsChanged` mutates `settings` under `mu` (`main.go:159-171`);
- `publish` reads both and writes `views[id].rev` under `mu` (`main.go:69-90`).

`Session` has its own `sync.Mutex` and every field is behind it (`service.go:32-49,52-70,77-110`), and `Snapshot` returns a **copy** of the container slice (`service.go:34-35`), so the caller cannot race the next `Refresh`. There is no missing lock on `views` / `settings` / session state.

The gaps: (a) `c.Call` for panel-open is issued **outside** `mu` (`main.go:140`) and `Client` does not serialize `Send` against itself (`client.go:79`), so two writers can hit the pipe — the **P2** finding; and (b) `publish` takes the session lock twice (`Snapshot` then `RunningCount`, `main.go:72-73`), so a concurrent `Act`→`Refresh` can make the bar count and the panel list disagree by one refresh (cosmetic). Note `c.Snapshot` calls inside `publish` are all serialized by `mu`, so snapshot-vs-snapshot is actually safe — the code comment (`main.go:50-54`, "the v1 encoder serializes one publish at a time with it") is **true for publishes** and only incomplete about `Call`.

### (2) Is the argv path safe?

**Yes.** `docker` is invoked with an explicit argv and no shell (`exec.CommandContext(ctx, "docker", args...)`, `docker.go:59,106`), so there is no shell-injection surface. The container ID is re-validated by `ParseAction` before use: it must match `^[A-Za-z0-9][A-Za-z0-9_.-]*$` (`view.go:172,176-187`), which blocks whitespace, shell metacharacters, and — because the first character cannot be `-` — option-injection such as `start --help` or `stop -f`. The tests pin the rejection table (`TestParseAction` in `mini_docker_test.go`: `start:bad id`, `start:;rm -rf /`, `start:-lead` all rejected). Remaining notes, both low: `docker` is resolved through ambient `PATH` (`docker.go:59`) — standard for CLI wrappers in this repo and mediated by the user's own environment, with the manifest's `requires.commands:["docker"]` as the declared gate; and `List` stdout is bounded by `io.LimitReader(pipe, 1<<20)` (`docker.go:54,69`), so stdout buffering **is** bounded. `runDocker` (actions) uses `cmd.Run()` with a `strings.Builder` for stderr bounded to the 500-byte tail in `diagnose` (`docker.go:135-149`); docker action stdout is not read into memory. No unbounded buffer.

### (3) Do tests pin the claims?

Mostly yes; the weak spots are called out above. Specifically:

| Claim | Test | Pins it? |
|---|---|---|
| Act error survives its own refresh | `mini_docker_test.go TestActErrorSurvivesItsOwnRefresh` | **Yes** — `failStartCLI` fails Start while List succeeds, the exact erasure shape. |
| Act error clears on next success | `…TestActErrorClearsOnNextSuccess` | **Yes.** |
| In-flight buttons disabled | `…TestPanelDisablesButtonsWhileActing` | **Partial** — no assertion any button stays enabled (P3). |
| Race storm (views+settings+publish) | `cmd/…/main_test.go TestRunConcurrentTraffic` | **Yes** — 300 settings+action events + view churn under `-race`, drains output. |
| Refresh click non-blocking | `…TestRefreshClickDoesNotBlockMainLoop` | **Mostly** — 1500 ms hung CLI + settings canary; canary is bar-coupled (P3). |
| Row cap + "+N more" | `…TestPanelListScrollsAndCaps` | **Yes** for count; placement not checked (P3). |
| Running-first stable order + accent | `…TestPanelOrdersRunningFirstWithAccentState` | **Yes.** |
| ParseAction allow-list | `…TestParseAction` | **Yes** for dispatch/rejection; no stale-existence check (P3). |
| fallbackVersion == manifest | `…TestHandshakeFallbackMatchesManifest` | **Yes** (reads `../../plugins/mini-docker/manifest.json`). |
| Tooltip read-only column | `…TestTooltipTreeIsReadOnlyColumn` | **Yes** (`v1.Validate(..., ViewTooltip)`). |
| Bar pill activatable, `ID:"open"` | `…TestBarTreeValidate` | **Yes.** |
| Live gate | `live_test.go` | **No numeric assertion** — see P1. |

No test in the tranche is *purely* tautological; `TestCLIParsesDockerJSONLines` (P3) is the one that cannot catch a regression in production `List`. Note also that the roadmap's T4.2 asked for a `tests/integration/plugin_mini_docker_gate_test.go` (L); it was **not** delivered — the T0–T4 commits added `cmd/…/main_test.go` instead. That is a reasonable substitute (it does give the previously-untested entry point its first coverage) but the full end-to-end gate (fake docker script → handshake → open panel → assert `panel.open` call recorded) is still missing.

### (4) Ponytail violations?

**Few, and none severe.** Positive signals: no new dependency (go.mod unchanged), `slices.SortStableFunc` + `slices.Clone` (stdlib over a hand-rolled sort), `strings.Builder` instead of a new error type, deletion over addition for `show_count` / dead `instance` / notifications capability, and explicit `ponytail:` comments naming ceilings and upgrade paths (`docker.go:45-46` single action timeout; `docker.go:52-53` buffered list; `main.go:53-54` single mutex vs funnel). Flagged:

- **`maxPanelRows` comment is misleading** (`view.go:125-126`): it claims the cap keeps the tree "inside the host's MaxNodes budget of 1024", but 150 rows ⇒ `≈905 nodes today`, and the tree is a pure function of the cap + error/loading branches, so the cap *does* bound it — the arithmetic (`905 < 1024`) is thin (12% headroom) for a claim worded as a guarantee. Either lower the cap (e.g. 100 ⇒ `≈605 nodes`, ample margin) or state the exact bound. Not over-engineering, but the comment overpromises.
- **No change-detection in `publish`** (P3) is the one "clever vs boring" tension: re-rendering `900 nodes on every tick is simple but wasteful; weather's `sameWeather` guard is the boring precedent. Acceptable as-is with a `ponytail:` note; a speculative patch/change-detection layer now would be over-engineering.
- No unrequested abstractions, no new files beyond the test file, no premature interfaces. The `Docker` interface is justified by the test seam and pre-dates this tranche.

### (5) Is the missing `ViewResync` handling a real defect?

**Latent, not present — but worth fixing cheaply.** The host emits `view.resync` **only** from the view-patch path (`internal/shell/pluginhost.go:343-347,355-358`); the message documentation confirms it is for "a patch [the host] dropped or rejected" (`plugin/v1/message.go:148-153`). This plugin sends **only** `ViewSnapshot` and never `ViewPatch`, and its per-view revisions are monotonic because all publishes are serialized under `mu`; the host's snapshot path does not reject on revision (`pluginhost.go:314-331`). So **today the host has no reason to send a resync and the omission is invisible.** It becomes a real defect on two plausible paths: (a) the plugin adopts `ViewPatch` (the natural next optimisation, and what weather does), or (b) a snapshot is effectively dropped/coalesced such that the host's applied revision lags the next snapshot (the P2 freeze path above). Because the fix is one small `case` copied from weather (`cmd/sysc-plugin-weather/main.go:149-154`), and every other panel-owning plugin implements it, add it. Ignoring a resync means "that view never updates again", a worse failure mode than the patch feature it guards.

---

## Summary table

| # | Sev | File:line | Issue | Fix |
|---|---|---|---|---|
| 1 | P1 | `docs/plans/2026-09-22-mini-docker-audit-workplan.md:60` | "live gate … 11 containers, 6 running" not asserted by `live_test.go` | Reword to separate logged observation from asserted gate |
| 2 | P2 | `cmd/…/main.go:140` + `plugin/v1/client.go:79` | `c.Call` outside `mu`; encoder has no write mutex → interleaved frames | Serialize all client I/O with one send mutex |
| 3 | P2 | `cmd/…/main.go:120-177` | No `ViewResync` case (latent; weather handles it) | Add weather-style case under `mu` |
| 4 | P2 | `cmd/…/main.go:150-155` | Concurrent actions can outrun the host revision → frozen view, no resync | Same resync case; narrow with stale-ID check |
| 5 | P3 | `plugins/mini-docker/view.go:113-121` | "+N more" appended before list ⇒ renders above it | Reorder append; fix comment |
| 6 | P3 | `plugins/mini-docker/mini_docker_test.go:86-109` | Test duplicates `CLI.List` parser; production parse untested | Extract `parseContainers`; share it |
| 7 | P3 | `cmd/…/main.go:69-90` | `900-node republish every tick, no change detection | `ponytail:` note or cheap equality guard |
| 8 | P3 | `plugins/mini-docker/view.go:176-187` | Stale/vanished IDs still dispatched | Resolve against `Snapshot()` before acting |
| 9 | P3 | `mini_docker_test.go` / `cmd/…/main_test.go` | Over-disable passes; canary is bar-coupled | Assert enabled>0; filter by `view_id` |
| 10 | P3 | `plugins/mini-docker/manifest.json:6-9` | minor:1 understates fields used (repo-wide) | Metadata only; out of scope |

**Verdict: fix-first** — items 1–3 are cheap and worth landing before the tranche is declared done; the code itself is otherwise in good shape and safe to ship.
