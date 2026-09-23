# Mini Docker audit consolidation & roadmap

Date: 2026-09-22 · Sources: `2026-09-22-mini-docker-{ui,ux,backend,hallmark}-audit.md` (this directory) · Status: awaiting user sign-off before any implementation.

## Verdict

Four audits, one conclusion: **mini-docker is a first-sweep port that was never run against a live host.** The panel cannot open (triple-confirmed), the plugin can crash under its own concurrency, and every error the user could act on is discarded before display. The fix set is small and shallow — no architectural work, net code addition is modest, and the shrink ledger deletes roughly as much as the fixes add. No new dependencies; nothing here leaves the stdlib + existing v1 vocabulary.

## Cross-audit consensus

| Finding | UI | UX | Backend | Hallmark | Severity |
|---|---|---|---|---|---|
| Panel unreachable (bare-text pill, no `open` case) | #1 | #1 | #1 | cross-ref | P1 ×3 |
| Unsynchronized publish/state (data race → crash) | — | #8 | #2 | — | P1 |
| Refresh wipes panel / blocks input on hung docker | — | #2,#6 | — | — | P1/P2 |
| stderr discarded → "exit status 1" | — | #4 | #3 | — | P2 |
| Action errors erased by follow-up Refresh | — | #3 | #4 | — | P2 |
| No scroll / MaxNodes 1024 overflow | #2 | #7 | #10 | — | P1/P2 |
| `notifications` capability declared, never used | P3 | P3 | #7 | — | P3 ×3 |
| Version drift (handshake 0.1.0 vs manifest 0.2.0) | P4 | P4 | #13 | — | P4 ×2 |
| `show_count` ≡ `status_mode: hidden` redundancy | — | P3 | #8 | — | P3 ×2 |
| Tooltip declared, silently rejected (row root) | #3 | — | — | major | P2 |
| Bar hides failure (no tone when unavailable) | P3 | #5 | — | — | P2/P3 |
| No running-first ordering | P3 | — | — | major | P3 |
| No panel padding / header hierarchy / Tabular count / dead Gap / 720px width | P2–P4 | — | — | major/minor | P2–P4 |

## Roadmap

Sizes: S <20 lines, M 20–60, L >60 (non-test lines). All items: no new deps. TDD applies — failing test first for every behavior change (workplan contract).

### T0 — Gates (must land first; nothing else is observable until these do)

- **T0.1 Panel reachable** — wrap the pill in a `Button` node `ID: "open"` and handle it with `CallPanelOpen` (world-clock shape, ~10 lines). `view.go` `BarTree`, `main.go` input switch. Test: `TestBarTreeHasOpenButton` + gate test asserts the published bar contains the button. **S**
- **T0.2 Serialize state** — one `sync.Mutex` guarding `views` + `settings` + `publish` (minimum-code option over a channel funnel; `ponytail:` upgrade path is the single-writer funnel if races ever reappear). `main.go`. Test: gate test with `-race` driving concurrent act + tick. **M**
- **T0.3 Refresh off the main loop** — the `refresh` click signals the poller goroutine (channel) instead of calling `poll()` inline; a hung `docker ps` can no longer freeze input. `main.go`. Test: code-review + live check (hard to unit-test honestly; YAGNI on a fake-stall harness). **S**
- **T0.4 Keep-last-known rendering** — the service retains the last good snapshot; `loading` is only true when there is no data yet; a subtle "Refreshing…" line appears while a refresh is in flight. `service.go`, `view.go`. Test: `TestPanelTreeKeepsLastKnown` + service refresh-twice test. **M**

### T1 — Truthfulness (errors the user can act on)

- **T1.1 stderr captured and diagnosed** — `List`/`Act` keep stderr (reuse the `runDocker` pattern), map "Cannot connect to the Docker daemon…" → "Docker daemon not running", missing binary → "docker command not found"; wrap stdout in a 1 MiB `io.LimitReader` (cheap memory bound at the trust boundary). `docker.go`, `service.go`. Test: fake-CLI stderr paths. **M**
- **T1.2 Action errors persist + in-flight Disabled** — `Act` errors survive until the next *successful* refresh; affected buttons go `Disabled` while an action is in flight (kills double-click dupes and silent failures). `service.go`, `view.go`, `main.go`. Test: `TestActErrorSurvivesRefresh`. **M**
- **T1.3 Action timeout** — `exec.CommandContext` with a ~15 s cap on start/stop/restart; timeout renders as a readable error. `docker.go`. Test: fake-CLI hang case. **S**
- **T1.4 Bar failure tone** — unavailable pill renders with `ToneError` (timer precedent: pill reflects state). `view.go`. Test: label/tone table extension. **S**

### T2 — Surface (hallmark majors + scroll)

- **T2.1 Scrollable, bounded list** — wrap container rows in a `KindList` with fixed `Height`; cap rendered rows and append a "+N more" line so the tree can never approach `MaxNodes`. `view.go`. Test: tree-size table (1, 50, 500 containers). **M**
- **T2.2 Tooltip column root** — 3-line `TooltipTree` so the host stops silently rejecting it. `view.go`, `main.go`. Test: tooltip root-kind assertion. **S**
- **T2.3 Status tone split + running-first ordering** — status leaves the merged "name · status" string into its own toned node (accent/subtle running, subtle exited); rows ordered running-first, stable within group. `view.go` (+ small sort helper). Test: ordering + tone table. **M**
- **T2.4 Craft pass** — panel padding (12–16), header title size/Bold/PinEnd, `Tabular: true` on the count node, drop the dead `Gap: 6`. `view.go`. Test: existing tree assertions updated. **S**
- **T2.5 Panel width** — resize manifest to ~440–480 once row anatomy settles (T2.3). `manifest.json`. **S**

### T3 — Shrink and cleanup (the ponytail ledger; mostly deletions)

- **T3.1** Delete `notifications` capability from the manifest. **S**
- **T3.2** Delete `show_count` — fully subsumed by `status_mode` (false ≡ hidden); rename the "hidden" option to "never" with label "Show count" so the setting stops lying about what it does. *(Decision D1 below.)* **S**
- **T3.3** Delete the dead `view.instance` field (written, never read — confirm during dev). **S**
- **T3.4** Version single-source: handshake reads the manifest constant; kills the 0.1.0/0.2.0 drift permanently. **S**
- **T3.5** Table-driven action dispatch replacing the four prefix-string cases; parse node IDs strictly (`start:`/`stop:`/`restart:` + `^[A-Za-z0-9][A-Za-z0-9_.-]*$`) before anything reaches argv — the ID is echoed into docker's argv, and this is a trust boundary (ponytail: not lazy here). Net-negative lines. **S**
- **T3.6** Dispositions (recorded, not worked): per-tick `time.After` — idiomatic, no leak, skip; freshness indicator — covered by T0.4's "Refreshing…" line, skip; lifecycle notifications / logs / exec / stats / compose / filter / confirm dialogs — YAGNI, out of scope.

### T4 — Verification

- **T4.1** Unit tests per item above, failing-first.
- **T4.2** `tests/integration/plugin_mini_docker_gate_test.go` following the kdeconnect gate: fake docker CLI script, drive handshake → open → act → settings, assert published trees; **run with `-race`** (the race class is invisible to the current suite, and `cmd/` has zero tests today). **L**
- **T4.3** Live SSH acceptance on the docker laptop. Gates: pill click opens the panel; list survives a refresh with docker hung; daemon-down error is readable; 200+ containers scroll and stay under the node budget; start/stop works and its failure path reports.
- Bump manifest to 0.3.0 at tranche end (settings schema changed via T3.2).

Net estimate: **≈ +100–140 non-test lines in T0–T2, ≈ −25–35 in T3** → net ≈ +80–110, no new deps, no new files except the gate test.

## Decisions — signed off 2026-09-22

- **D1 — settings reduction (T3.2): APPROVED** — delete `show_count`, keep `status_mode` renamed to honest labels ("Always / When running / Never").
- **D2 — panel width (T2.5): decide-live** at T2.3 time.
- **D3 — sysc-shell bd filing: DONE** — sysc-496 (encoder write mutex, P1), sysc-497 (silent view rejection, P2), sysc-498 (open-panel pattern docs, P3).
- Development tranche approved; starting at T0.

## What we are explicitly not building

Notifications on container lifecycle events, log viewing, exec, stats, compose, filtering/search, confirmation dialogs, freshness timestamps. All YAGNI per the UX audit; revisit only on request.
