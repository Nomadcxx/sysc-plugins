# Mini Docker design

Date: 2026-09-23 · Implementation baseline: `c7c754b`, plugin v0.3.0, shell pin
`v0.0.0-20260923111103-dd221a064353` · Feeds: the approved implementation plan
`2026-09-23-mini-docker-parity-pass.md` · Research: `2026-09-23-mini-docker-research.md`.

## Goal

Make `org.sysc.mini-docker` the shell's honest Docker manager: the four surfaces the prior art
converged on (containers, images, volumes, networks), a bar pill that tells the truth, and
every action's outcome visible in the panel. Containers is the tab carried furthest; the
other three are deliberately shallow.

## Why now, and what the baseline is

The shipped plugin was a first sweep that could not open its own panel. The audit tranche
(v0.3.0, now merged) fixed reachability, diagnosed failures, added action timeouts, capped
and scrolled the list, and made the settings honest. An independent review then landed three
fix-first items (`5991b68`): all client writes serialized under one send mutex, `view.resync`
handled with a revision reset, and the workplan's live-gate claim corrected. The fit test
(`TestViewsFitTheirHostSlots`) now lays every view out with the host's own pipeline.

So the baseline is a plugin that works. This design is the parity pass: the four-tab manager
the prior art describes, within the protocol as it exists today (no shell changes assumed —
those are listed at the end, unfiled).

## Scope

**In.** Bar pill and tooltip; a panel with a scope row over four tabs; the containers tab at
depth (select, detail, start/stop/restart/remove); images (list, run-image workflow, remove);
volumes (list, remove); networks (list, remove); inline confirmation for every destructive
action; per-tab error surfaces; refresh button, poll cadence, change detection; the
run-image workflow with host-port preflight and exposed-port pre-selection.

**Out, explicitly.** Compose grouping and lifecycle (DMS's best idea, a second information
architecture — parked). Logs, exec, stats (a log pane cannot be rendered inside the node
budget, and terminal handoff changes the plugin's surface; all three are cheap to add later
as actions). Notifications (the capability is granted but unwired in this build — the shell
would have to change first). `docker events` streaming (polling suffices at this scale; the
upgrade path is named in §6). Container lifecycle confirmations beyond remove (stopping is
reversible). Multi-host contexts, pruning, filters/search, i18n, keyboard navigation (no
wire vocabulary for it).

### Non-goals that are decisions, not omissions

- **No events stream.** DMS uses `docker events` + a 300 ms debounce and keeps polling as a
  fallback; at a 5 s poll with four cheap CLI calls, an events child buys little and adds a
  permanently held subprocess, a debounce, and a new failure mode. Parked with the polling
  cadence as its upgrade path.
- **No per-row action clusters.** v4 put three 32 px buttons on every row; v5 moved to
  select-a-row + a detail card. The detail card is cheaper in nodes (a row costs 4 nodes
  instead of 6–9) and is our own lineage. Rows are the selectable control.
- **No confirmation setting.** Confirmation is always on, inline, and costs one node.

## Information architecture

**Bar pill.** One button, text only: `docker` or `docker N` (tabular figures). Tone `error`
when docker is unavailable; normal otherwise. The count is the state — accenting a
permanently-true condition is noise, and the repo's rule is that failure recolors, not that
success shouts. No icon: the catalogue has no docker/container glyph and the bar cannot draw
images (see Shell-side notes).

**Tooltip.** Read-only column, up to two lines: the running count (`3 containers running`),
then the diagnosis when unavailable (`docker daemon not running`).

**Panel.** Root column, `Padding 16`, `Gap 8`, fixed height from the manifest, top to bottom:

| Band | Content | Height |
|---|---|---|
| Header | `Docker containers` (title, bold) + `Refresh` button, `PinEnd` | 28 |
| Scope row | four buttons: Containers · Images · Volumes · Networks, selected one `Fill: accent` | 28 |
| Status | at most two lines: the action error (tone error) and the list error for the active tab (tone error); `Working…` (subtle) while an action is in flight | 0–40 |
| Entity list | `KindList`, scroll, `Gap 4`; rows are buttons | 320–400, state-dependent |
| Overflow footer | `+N more` (subtle) **below** the list — fixes the v0.3.0 inversion | 16 |
| Detail card | selected entity: name (bold), one or two `subtle` lines, action row; hidden when nothing is selected | 0–112 |

Height arithmetic: fixed chrome is 160 (32 padding, 28 header, 28 scope, 16 footer, five 8
gaps, and a 16 reserve), so `list = 400 − status − card`, floored at 200: 400 with neither,
380 with one status line, 288 with the card, 268 with the card and one status line, 248 with
two. The implementation computes the list height from the bands it is actually publishing
rather than hardcoding it, and the fit test lints every combination at 480×560.

**Panel box.** Keep 480×560 (house sizes run 360–750 wide; 480 holds a four-button scope row
plus an entity row with three actions — the fit test is the proof). Widening to 560 is the
fallback if the lint matrix refuses a row; that is a manifest edit, not a layout rewrite.

**Form mode (images tab).** Selecting `Run` swaps the entity list + detail card for the run
form; the bands above it stay. No modal exists on this wire; a form is a tree swap.

## View contracts

**Entity row (selectable).** `button` (ID `select:<id>`, `Fill: card` when selected else
`outline`, `Radius 10`, `Padding 8`) wrapping a column of two texts: line 1 `name`
(bold; tone `accent` when running — the state indication a dot would have carried), line 2
`image · status` (`subtle`, `caption`) — 4 nodes per row. Buttons may hold only
non-interactive children; this row complies, and the fit test pins the width.

**Detail card.** Name (bold), `image · status · id` (subtle), action row (`Gap 4`, buttons
`Height 28`). Action sets per tab:

| Tab | Actions | Enabled when |
|---|---|---|
| Containers | Start · Stop · Restart · Remove | Start only when not running; Stop/Restart only when running; Remove only when not running |
| Images | Run · Remove | Remove disabled while any container references the image (running or stopped — docker refuses either way) |
| Volumes | Remove | always |
| Networks | Remove | disabled for `bridge`, `host`, `none` |

**Confirmation.** `Remove` arms the row: the action row is replaced by
`Remove <name>?` + `Confirm remove` (`Fill: error-container`) + `Cancel`. Arming clears on
cancel, on confirm dispatch, on scope change, and when the entity leaves the snapshot; it
survives a poll refresh (a 5 s tick must not disarm mid-decision). One armed row at a time.
No setting; no modal.

**In-flight.** While an action runs: the acting entity's matching buttons are `Disabled`,
the status band shows `Working…`, and the poller's publishes continue. The state is keyed by
scope + action + entity ID, not by a global busy flag, so a hung action on one container does
not lock the panel or unrelated entities.

**Empty / loading / failure.**
- Loading is shown only while there is no data for the active tab; a refresh over known data
  keeps the last snapshot and adds `Refreshing…` (subtle) to the status band — a refresh must
  never blank the panel (roadmap T0.4, never delivered).
- Empty tab: one centred subtle line (`No containers`, `No images`, …), including the
  no-docker case with the diagnosis line above it.
- Primary failure (`docker ps` fails / binary missing / daemon down): the status band carries
  the diagnosis (`Docker daemon not running`, `docker command not found`), the rendered list
  is empty, cached data is retained internally for recovery, mutations are rejected until a
  successful refresh, and the bar pill's tone is `error`.
- Secondary failures (images/volumes/networks) render in the status band for the tab they
  belong to and retain that tab's last successful rows, never silently empty.

## Data layer

**Docker surface** (argv, no shell; stderr captured and diagnosed; stdout bounded, with
over-limit output rejected rather than returned as a partial list):

| Tab | Command |
|---|---|
| Containers | `docker ps -a --format '{{json .}}'` |
| Images | `docker images --format '{{json .}}'` (including the Containers reference count) |
| Volumes | `docker volume ls --format '{{json .}}'` |
| Networks | `docker network ls --format '{{json .}}'` |
| Port pre-select | `docker image inspect --format '{{json .Config.ExposedPorts}}' <image>` |
| Actions | `docker start\|stop\|restart\|rm <id>`, `docker rmi <id>`, `docker volume rm <name>`, `docker network rm <id>` |
| Run | `docker run -d [--name <n>] [-e K=V …] [-p P:P] [--network <net>] <image>` |

Containers' line parser moves into `parseContainers([]byte)` so production parsing is what the
tests exercise (the tranche's test re-implemented the loop; a dropped line must not vanish
silently — the count of skipped lines surfaces as a subtle status note).

**State model.** One `Session` per plugin process owning a snapshot per tab:
`{available, loading, listErr, refreshedAt, skippedLines, items[]}`, plus `actErr`,
in-flight action keys (scope + verb + ID), `confirmID`, `selectedID`, `scope`, and the
run-form draft. Sorting is deterministic and testable: running-first then name (containers);
`repo:tag` (images); name (volumes, networks). The image `Containers` count disables
removal while any container references that image.

**Refresh.** Poll every `refresh_interval_seconds` (1–30, default 5). Containers refresh
every tick — the bar pill depends on it. Other tabs refresh on first open, on the explicit
Refresh button, and on every tick **while they are the active tab**. A tab switch does not
re-fetch a tab refreshed within the last 5 s. One refresh is in flight at a time; a click
during a refresh coalesces into one more (the existing size-1 channel pattern).

**Change detection.** `publish` computes a digest of each rendered view and skips that
view's publish when its tree is unchanged. This keeps revisions meaningful and avoids sending
a large tree every 5 s. A new `ViewOpen` and every `view.resync` force a full snapshot;
`view.resync` resets the revision to zero before publishing. A second identical poll is
tested to produce no snapshot, while resync is tested to publish despite an unchanged digest.

## Actions

Node IDs stay the only input channel. `ParseAction` becomes a table-driven parser with a
strict allow-list per verb, and gains the existence check the tranche left open: after
parsing, the container ID must be present in the current snapshot before the goroutine
spawns — a stale row cannot dispatch `docker start <gone>`.

| Node ID | Verb |
|---|---|
| `tab:<containers\|images\|volumes\|networks>` | change scope |
| `select:<id>` | select/deselect the entity (deselect clears the card) |
| `start:<id>` `stop:<id>` `restart:<id>` `remove:<id>` | container action |
| `run:<id>` `rmi:<id>` | open the run form for an image; arm image removal |
| `volrm:<name>` `netrm:<id>` | volume/network action |
| `confirm` `cancel` | arm/disarm the pending removal |
| `run-submit` | submit the image and options retained in the run-form draft |
| `refresh` `open` | poll now; open panel |

Every ID half is validated against `^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$` (image references carry
`:`/`/`/`@`) before it can reach argv; anything else is dropped, not reported. The run form's
free text is validated separately (§Run form).

Text input change events carry the committed value in `InputEvent.Text`. The plugin retains
name, port, and env drafts by node ID and validates them before dispatch.

## Run form (images tab)

Fields, in order: image line (bold `repo:tag`), container name (`text_input`, optional),
port (`text_input`, optional, 1–65535), publish toggle (a button that flips
`Publish port: on/off` — the wire has no checkbox), network (a button that cycles through the
snapshot's networks, starting at `default_network` — the wire has no select), environment
(`text_input` multiline, one `K=V` per line), inline error line (tone `error`), then
`Cancel` + `Run` (`Fill: accent`, disabled while busy or invalid).

Validation before anything reaches argv: name `^[A-Za-z0-9][A-Za-z0-9_.-]*$`; port integer
1–65535; env key `^[A-Za-z_][A-Za-z0-9_]*$` with a non-empty value; image reference non-empty
and shape-checked. Unknown networks are rejected against the snapshot. Text input is bounded
by the protocol's 64 KiB input limit.

**Port preflight.** When a port is requested, the plugin first checks whether it is already
listening: read `/proc/net/tcp` and `/proc/net/tcp6` for a LISTEN entry on that port (state
`0A`), stdlib only, path injected behind a seam for fixtures. Occupied → inline error
(`Port 8080 is already in use on the host`) and no run. This revives the v4 behaviour that
v5 dropped, without spawning `ss`.

**Port pre-select.** On opening the form for an image, one
`docker image inspect --format '{{json .Config.ExposedPorts}}'` preselects the first exposed
port. Failure is silent (the field stays empty) — the v4 per-image guess table is not
revived.

## Settings

| Key | Type | Default | Effect |
|---|---|---|---|
| `refresh_interval_seconds` | int, 1–30 | 5 | poll cadence; changing it re-arms the ticker |
| `status_mode` | select `always`/`running_only`/`hidden` | `always` | bar count: always, only while running, never (labels: Always / When running / Never) |
| `default_network` | string | `bridge` | network the run form starts on |

The 2026-09-22 roadmap decision D1 deleted `show_count`; this design follows that signed
decision and keeps the manifest at these three settings. The host validates the stored
settings map on writes, so a pre-v0.3 configuration retaining `show_count` may cause later
settings writes to be rejected until the shell prunes undeclared keys. The plugin cannot
repair that host-owned migration. Record it as a shell-side risk; do not reintroduce a
tombstone in this pass.

Set `protocol.minor` to 4. The manifest uses node fields introduced through minor 4,
including multiline text input. The handshake fallback version remains pinned to the
manifest by the existing test.

Renaming is likewise off the table: `refresh_interval_seconds` and `status_mode` keep their
keys and their meanings.

## Icons and type

Catalogue-verified names only (project font, then Material); an unknown name fails the view.

| Use | Icon |
|---|---|
| Scope: Containers · Images · Volumes · Networks | `widgets` · `wallpaper` · `folder_open` · `lan` |
| Start · Stop · Restart | `play_arrow` · `pause` · `restart_alt` |
| Remove · Confirm remove · Cancel | `delete` · `check` · `close` |
| Refresh · Back from form | `refresh` · `chevron_left` |
| Bar pill | none (text only) |

Sizes: `title` for the panel header and the selected entity's name, `body` for rows,
`caption` + `subtle` for secondary lines. Tones: `error` for failures and the unavailable
pill, `accent` for the running state, `subtle` for secondary text. No new theme tokens, no
new icons — both are shell-side asks, not design requirements.

## Verification

1. **Unit (plugin).** Parsers against real captured payloads in `testdata/` (containers,
   images, volumes, networks, `/proc/net/tcp`), the argv builders, the action parser table
   (accept/reject), the confirmation state machine, sort orders, per-tab error surfacing,
   change detection (no snapshot when nothing changed), and the port preflight (occupied/free).
2. **Harness (cmd).** The existing pipe harness: handshake → open panel → tab switch → select
   → act → confirm → settings → resync, asserting the published trees and that each dispatch
   reached the fake docker with the exact argv. Fix the two weak tests the review named
   (assert an enabled button survives the in-flight disable; filter the non-blocking canary by
   `view_id`).
3. **Fit.** `TestViewsFitTheirHostSlots` extends to every tab, the form, the armed state, and
   both status-band heights, at bar/tooltip/480×560 — the geometry contract from
   `docs/plugin-ui-rules.md`.
4. **Integration gate.** `tests/integration/plugin_mini_docker_gate_test.go` uses a fake Docker
   executable on PATH, drives handshake → open → tab switch → form input → run → confirmed
   remove → settings → resync → shutdown, and asserts the published trees and recorded argv.
5. **Live gate (contract).** `go test -tags live` asserts what is assertable: daemon reachable,
   all four tab panel trees, bar, and tooltip validate, and the port preflight answers. Counts
   are logged; no host-specific count is asserted, correcting the earlier gate's claim. Manual
   acceptance on the machine with Docker:
   pill click opens the panel; the list survives a refresh with docker hung; the daemon-down
   line is readable; 200+ containers scroll and stay under the node budget; start/stop works
   and a failure is visible; a remove requires the second click.

## Parity against the reference prior art

| Element | v4 (QML) | v5 (Luau) | DMS | This design | Verdict |
|---|---|---|---|---|---|
| Four tabs, containers-first | yes | yes | no (containers+compose) | yes | parity with lineage |
| Container actions | start/stop/restart/remove | start/stop/restart/remove | +pause/unpause | start/stop/restart/remove | parity (pause parked: rare, one more button) |
| Images / volumes / networks | yes | yes | none | yes | parity |
| Run-image form | name, network, port, env rows | name, network, port, env textarea | none | same fields, cycling network button, env lines | parity, with wire-mandated substitutes |
| Host-port preflight | yes (`ss -tln`) | dropped | none | yes (`/proc/net/tcp`) | richer than v5, stdlib-only |
| Port pre-select from the image | yes | yes | none | yes | parity |
| Row interaction | inline accordion + 3 buttons/row | select + detail card | expandable actions | select + detail card | parity with v5, chosen for the node budget |
| Confirmations | none | none (README claims otherwise) | none | inline armed confirm | richer than all three; one node |
| Live state updates | poll | poll + revision signature | events + debounce | poll + digest change detection | between v5 and DMS; documented upgrade path |
| Compose grouping | none | none | yes (labels) | none (parked) | deliberate gap |
| Logs / exec | none | none | terminal handoff | none (parked) | deliberate gap |
| Bar pill | glyph + dot | glyph + count + dot | glyph + count | text + count, tone = failure | honest, plainer: no docker glyph in the catalogue |
| Settings | 5 (incl. colour choices) | 7 (incl. colour choices) | 6 | 3 | smaller on purpose: tones replace colour choice; signed roadmap D1 keeps `show_count` deleted |

**Verdict.** Feature parity with the lineage we ported, above it in three places (inline
confirmation, `/proc` preflight, per-tab error surfacing) and deliberately below DMS in two
(no events stream, no compose grouping). Not at parity with v5's visual richness: their panel
carries custom glyph colours, a 15-icon vocabulary and a floating 860×620 box; ours draws
from the shell's own catalogue and an attached 480×560 panel. The text-only bar pill (v5
shows a docker glyph) is a known visual difference accepted for this pass. `show_count` stays
deleted in accordance with signed roadmap decision D1.

## Shell-side notes (not requests, not filed)

Kept as a list so the design depends on nothing that does not exist today: wire the `notify`
capability; a container/image/volume/network glyph or alias registry; `include_settings`
generalisation; checkbox/select/slider wire kinds; prune undeclared stored setting keys on
load (the migration risk from deleting `show_count`); deliver or drop list `scroll` events;
reconcile the manifest protocol gate (6) with `host.hello` (1.7).

## Approved decisions

| # | Decision |
|---|---|
| D1 | Scope: four tabs, containers-first; Compose, logs, exec, stats, notifications, events streaming are explicit non-goals. |
| D2 | Navigation and rows: scope button row swaps the subtree; rows are selectable buttons; actions live in a detail card; no per-row action clusters. |
| D3 | Destructive actions: inline armed confirmation, always on, no setting; the arm survives a poll tick. |
| D4 | Refresh: 5 s poll (setting), containers every tick, other tabs on open/click/while active; publish skips unchanged trees. |
| D5 | Panel stays 480×560; list height computed from the status and card bands; fallback to 560 wide only if the lint matrix refuses a row. |
| D6 | Run-image workflow: four fields with the v5 validation rules, cycling network button, `/proc/net/tcp` preflight, inspect-based port pre-select. |
| D7 | Settings: declare `refresh_interval_seconds`, `status_mode`, and `default_network`; keep `show_count` deleted under signed roadmap D1. |
| D8 | Verification: unit + harness + fit + the missing integration gate, and the live gate asserts reachability and validation while only logging counts. |

These eight design decisions and the remaining plan-level choices were approved on
2026-09-23 before implementation.
