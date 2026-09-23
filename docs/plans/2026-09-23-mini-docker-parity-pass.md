# Mini Docker parity pass Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver the approved four-tab Docker manager in the existing mini-docker plugin, with safe actions, visible errors, bounded views, and no shell or dependency changes.

**Architecture:** Keep Docker process work in the existing CLI type, per-tab snapshots and interaction state in Session, pure tree construction in view.go, and protocol/timer orchestration in main.go. Preserve the current mutex-protected writer and resync handling; extend them for per-view digest suppression and text-input events.

**Tech Stack:** Go 1.26.4, the pinned sysc-shell plugin/v1 module, standard library only.

---

## 1. Review record and baseline

The initial review was against main at 7387866; implementation starts from main at c7c754b on 2026-09-23. Commit 47fe77e is the design-document commit, not the implementation baseline. The plugin remains v0.3.0 and go.mod pins sysc-shell at v0.0.0-20260923111103-dd221a064353. Mini-docker has no later path-specific commits in this checkout.

The current implementation is in plugins/mini-docker/{docker,service,view}.go and cmd/sysc-plugin-mini-docker/{main,main_test}.go. Existing safeguards/tests include sendMu serialization, ViewResync revision reset, concurrent pipe traffic, stale docker error reporting, bounded container rows, and TestViewsFitTheirHostSlots. Extend those tests; do not recreate those mechanisms. The mini-docker process integration gate does not exist yet.

The repository has no AGENTS.md. The project-level register is docs/plans/README.md; this draft currently lives outside the repository. Before implementation, promote the approved plan to docs/plans/2026-09-23-mini-docker-parity-pass.md and register it there. The tracked copy becomes canonical for execution.

### Findings applied to this plan

1. Preserve the existing sendMu around every encoder write, including c.Call. Preserve ViewResync revision reset and force a fresh snapshot on resync even when the view digest is unchanged.
2. Use the already signed roadmap decision D1: show_count stays deleted. Do not add the design's contradictory hidden tombstone. Amend the design before implementation so it matches D1.
3. Bump protocol.minor from 1 to 4 because the resulting manifest uses minor-2-and-later node fields, including Multiline. Update fallbackVersion with the manifest version.
4. The pinned protocol's InputEvent carries committed text in Text on change/submit. The form must handle those events and retain its draft; click-only ParseAction dispatch cannot implement the form.
5. The design permits actions on different entities concurrently. A single actingID cannot represent that state; use a set keyed by scope, action, and entity ID, and disable only matching controls.
6. A single global digest can suppress a newly opened view. Track the last published tree per view ID, force the first snapshot after ViewOpen, and force the snapshot after ViewResync.
7. Reset the polling timer when refresh_interval_seconds changes. The current time.After loop can wait out the old interval after a setting change.
8. Match the design's host/container port mapping: publish with -p P:P. The previous draft's -p P does not honor the requested host-port preflight.
9. Docker image JSON needs its Containers count to disable image removal while referenced. Include that field in parser fixtures and verify the installed Docker CLI template support in live acceptance; Docker remains the final authority and action errors must be shown.
10. Keep the fit matrix in the existing TestViewsFitTheirHostSlots. It is an extension of an existing test, not a new duplicate.
11. Reject list/inspect output over the 1 MiB cap instead of accepting a truncated snapshot. Stop an oversized child before waiting, since it can block on a full stdout pipe.

## 2. Decisions and execution gate

The four-tab scope and the delete-show_count decision are already in the signed 2026-09-22 roadmap. The design's D7 contradicted the signed setting decision. The owner approved the remaining design decisions and the plan-level choices below by asking to implement this plan on 2026-09-23; the design and this tracked copy record that approval:

- Container sorting: running first, then name. Current code preserves Docker order within running/stopped groups; the new sort is a user-visible change.
- Keep the rendered row cap at 150, as designed. The fit matrix must prove the 150-row case with a selected detail card fits MaxNodes and the 480×560 slot.
- Failure rendering: while a refresh is running, retain the last successful tree and show Refreshing. On a primary container-list failure, follow the design: show the diagnosis and an empty actionable list, keep cached data internally for recovery, and reject mutations until a successful refresh. On a secondary-tab failure, show the per-tab error and retain any last successful rows.
- Image-in-use display uses the Docker image listing's Containers count. If the installed Docker CLI does not expose it through the chosen JSON template, stop and revise the design before implementing a less reliable reference heuristic.

The signed D1 is not an open choice. Do not add a tombstone.

## 3. Scope and contracts

Keep the panel at 480×560 unless the approved fit matrix proves the scope row or a required row cannot fit. If it fails, stop and request approval for the design's 560-wide fallback; do not widen the manifest silently.

### In scope

- Text-only bar pill (docker or docker N), failure tone, tooltip diagnosis.
- Read-only tooltip column with running count and a diagnosis when Docker is unavailable, within the two-line design limit.
- Containers, images, volumes, and networks under one scope row.
- Container selection/detail actions; image run/remove; volume/network remove.
- Inline confirmation for every remove. Disabled actions follow the design's rules: running containers cannot be removed, referenced images cannot be removed, and builtin networks cannot be removed.
- Per-tab snapshots/errors, keep-last-known refresh behavior, refresh button, active-tab polling, and change detection.
- Image run form with name, optional host port, publish toggle, network cycling, multiline environment input, validation, exposed-port preselection, and Linux TCP listener preflight.

### Out of scope

No sysc-shell changes, new dependencies, docker events subprocess, notifications, compose, logs/exec/stats, search/filtering, pruning, i18n, or keyboard-navigation work.

### Ownership and invariants

- docker.go owns argv construction, bounded command I/O, stderr diagnosis, parsers, and the /proc port check. Use exec.CommandContext with argv; never invoke a shell.
- service.go owns snapshots, refresh state, form draft, current scope/selection, confirmation target, action errors, and in-flight action keys. Keep its lock around state only; never hold it while a Docker command runs.
- view.go remains a pure renderer. It must render from a copied Session snapshot and must not perform I/O.
- main.go owns the poll timer, input routing, settings, per-view revisions/digests, and client calls. sendMu serializes every write; the existing mu continues to protect views/settings.
- Reject an input aimed at an old view revision. Recheck scope and entity existence immediately before action dispatch, then validate the ID before argv construction.
- In-flight state is keyed by scope + verb + ID. Repeated identical actions are rejected/disabled; actions on other entities remain available.
- Only one refresh operation runs at a time. A size-one request channel coalesces clicks while polling is active.
- Confirmation stores scope + ID, survives refresh while that entity exists, and clears on cancel, dispatch, scope change, or entity removal from its snapshot. Clear selection if its entity leaves a successful snapshot.
- Refresh never blanks a populated panel while work is in flight. Action failures remain visible until a later successful action; list errors remain separate from action errors.
- Publish only when that view's rendered state changes. A new view and ViewResync always get a full snapshot; resync resets its revision to zero first. ViewClose also drops the cached digest.
- Keep the 150-row cap and prove every view state stays below MaxNodes and fits its host slot.

### Settings

| Key | Type | Default | Effect |
|---|---|---|---|
| refresh_interval_seconds | int, 1–30 | 5 | Poll cadence; a change resets the timer. |
| status_mode | select: always, running_only, hidden | always | Bar count visibility; labels remain Always, When running, Never. |
| default_network | string | bridge | Initial network selection for a new run form. |

Do not declare or read show_count.

### Node IDs and form input

| Node ID | Meaning |
|---|---|
| open, refresh | Open panel; refresh containers and active tab. |
| tab:<containers\|images\|volumes\|networks> | Change scope; clear selection, form, and pending confirmation. |
| select:<id> | Select/deselect the active tab's entity. |
| start:<id>, stop:<id>, restart:<id>, remove:<id> | Container action or arm its remove confirmation. |
| run:<image-id>, rmi:<image-id> | Open the selected image's run form; arm image removal. |
| volrm:<name>, netrm:<id> | Arm volume/network removal. |
| confirm, cancel | Dispatch or clear the pending removal. |
| name, port, env | Run-form text inputs; accept change events and retain InputEvent.Text. |
| form:publish, form:network, form:cancel, run-submit | Toggle publishing, cycle network, exit form, submit the retained image draft. |

The fixed run-submit ID avoids colliding with an image reference named submit. Validate entity ID halves against ^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$ before dispatch. Validate run names against ^[A-Za-z0-9][A-Za-z0-9_.-]*$, ports as integers from 1 through 65535, and env keys against ^[A-Za-z_][A-Za-z0-9_]*$ with non-empty values. Reject unknown networks against the network snapshot. The selected image must still exist in the image snapshot at submit time. No free text is interpolated into a shell command.

Run argv when all optional values are set:

    docker run -d --name NAME -e K=V -p PORT:PORT --network NETWORK IMAGE

Omit absent options. Emit -p PORT:PORT only when publishing is enabled and a port is present; an empty port emits no mapping. Parse environment lines at the first equals sign, require a valid key and non-empty value, and bound each input by the protocol's 64 KiB text limit. Validate network membership against the network snapshot. Use default_network when present, otherwise the first sorted network; if there are no networks, show an error and disable Run. Exposed-port selection sorts inspect keys before choosing the first valid TCP port so map iteration cannot change the default. The /proc check is advisory and race-prone by nature; Docker's run error remains visible if a port is taken after preflight.

### Docker command and record contracts

| Operation | Exact command shape | Parsed JSON fields |
|---|---|---|
| Containers | docker ps -a --format '{{json .}}' | ID, Names, Image, State, Status |
| Images | docker images --format '{{json .}}' | Repository, Tag, ID, CreatedSince, Size, Containers |
| Volumes | docker volume ls --format '{{json .}}' | Name, Driver, Scope, Mountpoint |
| Networks | docker network ls --format '{{json .}}' | Name, ID, Driver, Scope |
| Exposed ports | docker image inspect --format '{{json .Config.ExposedPorts}}' IMAGE | object keys such as 80/tcp |
| Container actions | docker start ID; docker stop ID; docker restart ID; docker rm ID | no stdout required |
| Image, volume, network remove | docker rmi ID; docker volume rm NAME; docker network rm ID | no stdout required |
| Run | docker run -d [--name NAME] [-e K=V ...] [-p PORT:PORT] [--network NETWORK] IMAGE | no JSON output |

Use a bounded stdout read for list and inspect commands and capture stderr for every command. Keep the existing 30-second list and 15-second action limits unless testing exposes a concrete need to change them. Image Containers is a Docker CLI template field, not a guessed count derived from container names. Parse a null or empty exposed-ports object as no preselection; only a valid TCP key yields a port.

Container ordering is running first then name; image ordering is Repository:Tag; volumes and networks sort by Name. The approved design's 150-row cap applies to each active entity list.

## 4. Files

| File | Work |
|---|---|
| docs/plans/2026-09-23-mini-docker-parity-pass.md | Add the approved, tracked canonical copy of this plan. |
| docs/plans/README.md | Register the canonical plan and update the design row after D7 is corrected. The integration gate is source code, not a document-register entry. |
| docs/plans/2026-09-23-mini-docker-design.md | After sign-off, align every show_count/tombstone reference with signed D1; document minor 4, image reference count, and the selected refresh/failure behavior. |
| plugins/mini-docker/manifest.json | Add default_network, set protocol.minor to 4, and bump version to 0.4.0. Do not restore show_count. |
| plugins/mini-docker/docker.go | Add image/volume/network types and commands, production parsers, bounded output handling, RunOpts argv construction, inspect parsing, and /proc port preflight. Retain existing timeout/error behavior. |
| plugins/mini-docker/service.go | Extend Session with per-tab snapshots, refresh state, scope/selection, run draft, confirmation, action errors, and keyed in-flight actions. |
| plugins/mini-docker/view.go | Render bar/tooltip, four tabs, container detail, other entity rows, inline confirmation, run form, and dynamic list height/footer. |
| plugins/mini-docker/mini_docker_test.go | Extend parser, service, state-machine, action, and existing fit tests. |
| plugins/mini-docker/testdata/ | Add line fixtures for containers/images/volumes/networks, exposed ports, and tcp4/tcp6. |
| plugins/mini-docker/live_test.go | Extend live read-only validation to all four views and the host-port preflight. Log counts; do not assert machine-specific counts. |
| cmd/sysc-plugin-mini-docker/main.go | Add form text routing, scope refresh scheduling, changed-setting timer reset, per-view digest, and action routing; update fallbackVersion. Preserve sendMu and ViewResync. |
| cmd/sysc-plugin-mini-docker/main_test.go | Extend existing pipe tests for text input, new-view/resync forced snapshots, timer reset, actions, and the view-specific canary. |
| tests/integration/plugin_mini_docker_gate_test.go | New fake-docker process gate: handshake, open, tab/form/action flows, exact argv, settings, resync, clean shutdown, and view fit checks. |

## 5. TDD task ladder

Run each task in the approved implementation worktree. For behavior changes, add the smallest failing test first, run it to observe the failure, implement the narrow change, rerun that test, then run the package test. Commit at each task boundary. Keep current regression tests unless they are directly revised for the new behavior.

### Task 0 — Approve and register the execution baseline

**Files:** add docs/plans/2026-09-23-mini-docker-parity-pass.md; modify docs/plans/README.md and the design after approval.

1. In the implementation session, use superpowers:using-git-worktrees to create an isolated worktree from the approved target branch; refresh the branch/commit record.
2. Confirm the remaining decisions in §2.
3. Copy this revised plan to the canonical in-repo path and register it. Update every design reference to the contradictory D7/tombstone to follow signed D1, then update the design row in README.
4. Review the staged docs diff; commit the approved plan/design synchronization before code.

### Task 1 — Extract and test production container parsing

**Files:** plugins/mini-docker/docker.go, plugins/mini-docker/service.go, plugins/mini-docker/mini_docker_test.go, plugins/mini-docker/testdata/containers.jsonl.

1. Add a failing parser test for valid lines, blank lines, and malformed lines, including the skipped-line count.
2. Extract the inline List loop into parseContainers and have CLI.List use it. Return the skipped count to Session for a subtle status note. Keep the existing 1 MiB stdout bound and error diagnosis.
3. Run: go test ./plugins/mini-docker -run 'TestParseContainers|TestSessionRefresh' -count=1
4. Expected: parser and existing refresh tests pass; malformed records are counted rather than silently disappearing.
5. Commit the parser extraction.

### Task 2 — Add other Docker reads and safe argv builders

**Files:** plugins/mini-docker/docker.go, plugins/mini-docker/mini_docker_test.go, cmd/sysc-plugin-mini-docker/main_test.go, and testdata/images.jsonl, volumes.jsonl, networks.jsonl, exposed-ports.json.

1. Add failing fixtures/tests for image, volume, network, and exposed-port parsing. Include image Containers count and malformed-line behavior.
2. Extend Docker and CLI for Images, Volumes, Networks, ImageExposedPorts, Remove, Rmi, VolRm, NetRm, and Run.
3. Reuse one bounded command-output path for list/inspect commands and the existing stderr diagnosis pattern. Reject output over 1 MiB without returning partial rows. Build Run arguments as discrete argv elements, with -p HOST:CONTAINER and no shell.
4. Test exact argv for optional/empty RunOpts and destructive commands with a fake executable.
5. Run: go test ./plugins/mini-docker -run 'TestParse|TestCLI' -count=1
6. Expected: parser, argv, and oversized-output cases pass, including paths/tags containing allowed punctuation.
7. Commit the Docker layer.

### Task 3 — Model tab snapshots and refresh lifecycle

**Files:** service.go, mini_docker_test.go.

1. Add failing service tests for one snapshot per tab, initial loading, repeated identical data, refresh over known data, primary failure, and secondary failure.
2. Add per-tab cached items, skipped-line count, error/loading/refreshed-at state, active scope, and the minimum snapshot API needed by view.go.
3. Keep last-known data during refresh; implement the approved primary/secondary failure rendering state; reject mutations while primary availability is false.
4. Add stale-scope/entity guards and tests before calling Docker.
5. Run: go test ./plugins/mini-docker -run 'TestSession|TestRefresh|TestStale' -count=1
6. Expected: service tests pass without holding the state lock during fake Docker calls.
7. Commit the session model.

### Task 4 — Build tabs and the container detail view

**Files:** service.go, view.go, mini_docker_test.go.

1. Add failing tree tests for scope buttons, selected rows, detail actions, empty/loading/error states, running-first/name sort, and overflow footer placement.
2. Render four-node selectable rows, the selected detail card, status band (including a subtle malformed-line count), and a list whose height is derived from visible bands.
3. Keep 150 rows and put +N more below the list. Show Start only when stopped; Stop/Restart only when running; Remove only when stopped.
4. Extend TestViewsFitTheirHostSlots to include the worst-case combination: 150 rows, selected card, overflow footer, and two status lines, plus empty/loading/error variants.
5. Run: go test ./plugins/mini-docker -run 'TestPanel|TestViewsFitTheirHostSlots' -count=1
6. Expected: every tree validates and fits the configured 480×560 slot.
7. Commit the container view.

### Task 5 — Add images, volumes, networks, and remove confirmation

**Files:** service.go, view.go, mini_docker_test.go.

1. Add failing tests for each tab's sort/order, row content, selected actions, reference/builtin disabling, confirm/cancel, and confirmation surviving refresh.
2. Render image/volume/network lists and the selected detail card. Disable image removal when Containers > 0 and network removal for bridge/host/none.
3. Store confirmation as scope + ID. Confirm dispatch rechecks the current snapshot and clears the armed state before starting the action.
4. Track concurrent actions by scope + verb + ID. Disable only matching controls; preserve errors independently from list errors.
5. Extend the fit matrix to all tabs and armed confirmation states.
6. Run: go test ./plugins/mini-docker -run 'TestImage|TestVolume|TestNetwork|TestConfirm|TestAction|TestViewsFitTheirHostSlots' -count=1
7. Expected: stale or ineligible IDs never reach the Docker fake; unrelated entities remain actionable during an action.
8. Commit the other tabs and confirmation state.

### Task 6 — Implement the run form and port rules

**Files:** docker.go, service.go, view.go, mini_docker_test.go, testdata/tcp4, testdata/tcp6.

1. Add failing tests for draft edits, required/optional validation, env parsing, network membership, exposed-port choice, and occupied/free TCP ports.
2. Open form mode from the selected image. Keep image identity in the draft; render name, port, publish toggle, network cycle, multiline env, error, Cancel, and Run.
3. Consume InputEvent.Text for the fixed form input IDs; reject oversized text and invalid values before building argv.
4. Implement /proc/net/tcp and tcp6 parsing with an injected read seam. Run the check only when publishing a requested host port; report an occupied port inline and do not run.
5. Choose the first valid exposed TCP port deterministically; inspection failure leaves the field empty.
6. Run: go test ./plugins/mini-docker -run 'TestRunForm|TestRunOpts|TestPort|TestExposed' -count=1
7. Expected: no invalid form reaches the Docker fake; emitted mapping is HOST:CONTAINER.
8. Commit the form flow.

### Task 7 — Connect protocol handling, polling, settings, and digest

**Files:** main.go, main_test.go, manifest.json.

1. Extend the pipe harness with failing cases for text changes, stale revisions, initial view snapshots, resync after an unchanged digest, and refresh interval changes. Update its HostHello Supported minor and plugin versions to match protocol minor 4 and v0.4.0.
2. Route InputEvent by view ID/revision/event kind. Keep click parsing separate from text-input draft updates.
3. Keep sendMu on all writes and c.Call. Make digest suppression per view; ViewOpen and ViewResync force a snapshot.
4. Poll containers every tick and the active tab on open, refresh, and while active. On tab switch, reuse a tab refreshed within the last interval; otherwise fetch it. Explicit refresh fetches containers and the active tab. Coalesce refresh requests; reset the timer when the interval setting changes.
5. Add default_network handling, version 0.4.0, protocol.minor 4, and fallbackVersion 0.4.0. Use the setting only when it exists in the network snapshot; otherwise select the first sorted network. Do not republish solely for refresh_interval_seconds because it does not change the rendered tree.
6. Run: go test -race ./cmd/sysc-plugin-mini-docker -count=1
7. Expected: existing concurrent-traffic and resync tests remain green; new form, forced-publish, and timer tests pass under the race detector.
8. Commit protocol orchestration and manifest.

### Task 8 — Add the fake-docker process gate and finish fit coverage

**Files:** tests/integration/plugin_mini_docker_gate_test.go, main_test.go, mini_docker_test.go, testdata/.

1. Add the process gate using the existing integration harness patterns. Put a fake docker executable on PATH and record argv.
2. Drive handshake, bar open, panel open, tab switches, text input, run submit, confirmed remove, settings, resync, and shutdown. Assert the expected published tree and exact Docker argv.
3. Filter snapshot assertions by view_id. Assert both disabled and enabled controls in the in-flight tree.
4. Extend the existing fit matrix to bar, tooltip, all tabs, run form, confirmation, and maximum rows.
5. Run: go test ./plugins/mini-docker/... ./cmd/sysc-plugin-mini-docker/...
6. Run: go test -run '^TestPluginMiniDockerGate$' -count=1 ./tests/integration/...
7. Run: GOMAXPROCS=2 go test -race -count=1 ./cmd/sysc-plugin-mini-docker/... ./plugins/mini-docker/...
8. Expected: offline tests pass without a real Docker daemon; every snapshot validates and fits.
9. Commit the integration gate and fit coverage.

### Task 9 — Live read-only check and documentation closeout

**Files:** live_test.go, docs/plans/README.md, design and tracked plan.

1. Extend the existing live-tag test to validate the four view roots and answer the /proc preflight. Keep it read-only and log counts without asserting host-specific values.
2. Run the normal offline suite, integration gate, race suite, go vet, and formatting checks listed in §6.
3. If Docker is available, run: go test -tags live -count=1 ./plugins/mini-docker/
4. Review the final diff for out-of-scope shell/dependency changes and ensure the docs register is current.
5. Commit the live gate and docs closeout. Live execution is an environment check, not a substitute for the fake-docker gate.

## 6. Verification commands

Required offline proof:

    go test -count=1 ./plugins/mini-docker/... ./cmd/sysc-plugin-mini-docker/...
    go test -run '^TestPluginMiniDockerGate$' -count=1 ./tests/integration/...
    GOMAXPROCS=2 go test -race -count=1 ./plugins/mini-docker/... ./cmd/sysc-plugin-mini-docker/...
    go vet ./plugins/mini-docker/... ./cmd/sysc-plugin-mini-docker/... ./tests/integration/...
    gofmt -l plugins/mini-docker/*.go cmd/sysc-plugin-mini-docker/*.go tests/integration/plugin_mini_docker_gate_test.go

The formatting check must print no files. If it prints any, run gofmt -w plugins/mini-docker/*.go cmd/sysc-plugin-mini-docker/*.go tests/integration/plugin_mini_docker_gate_test.go, then rerun the check.

Optional environment proof, only when a Docker daemon is available:

    go test -tags live -count=1 ./plugins/mini-docker/

Manual acceptance on the Docker host: bar click opens the panel; all tabs load; a refresh does not blank known data; daemon/list errors are readable; a 150-row view fits and shows the overflow footer; remove requires confirmation; run validates network/env/port and shows Docker failures.

## 7. Risks and boundaries

- /proc/net/tcp{,6} is Linux-specific and preflight cannot reserve a port. The CLI remains authoritative after the check.
- Docker output shapes are fixture-pinned and process-gate tested; the live check should confirm the installed CLI's Containers field. If absent, stop before shipping image-removal enablement.
- Retaining last-known rows on a secondary-tab error can show stale data; the visible error makes that state explicit. Mutations still recheck current snapshot membership.
- No new dependency is expected. If pinned wire behavior differs from the audited module, stop and update the plan/design before adding a dependency or shell change.
- Existing sendMu and ViewResync coverage is regression protection; do not remove it as part of digest optimization.

## 8. Decisions deliberately not reopened

- No hidden show_count tombstone: signed roadmap D1 deleted the setting, and the v0.3.0 manifest already omits it.
- No shell work for pruning undeclared settings in this pass. The historical migration concern is recorded in the shell-side notes and is separate from this plugin implementation.
- No icon catalogue additions, notification wiring, or protocol capability expansion.
