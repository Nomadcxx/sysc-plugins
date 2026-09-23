# Mini Docker research — prior art, shell capability, current state

Research for the `org.sysc.mini-docker` redesign: what three prior-art Docker managers do,
what sysc-shell's plugin protocol can actually render at the version this repo pins, and
what the plugin is today. Compiled from three shallow clones read in full by parallel
deep-dives, plus a capability audit against the pinned shell module. The clones lived in a
session scratchpad; the pinned SHAs below are the authoritative record of what was analyzed.

**Sources analyzed:**

| Source | Pin | What it is |
|---|---|---|
| noctalia-dev/community-plugins · `mini-docker` | `fea93782dbcb906ce37aee87628e37bc0bac4071` | Noctalia v5 Luau plugin, v1.0.4 — direct lineage of this repo's 0.2.0 port |
| LuckShiba/DmsDockerManager | `9e6a01283e169c46eff7a157202b1b8ab48577b1` | DMS/QML container + compose manager, v1.3.2 |
| noctalia-dev/legacy-v4-plugins · `mini-docker` | `ea21cb63d063075bc0acd72d8b946ce2c5eef00d` | The QML v4 ancestor, manifest 2.2.0 |
| sysc-shell (module pin) | `v0.0.0-20260923111103-dd221a064353` | Protocol and host ground truth (`go.mod:6`) |
| sysc-plugins | `main` @ `e60c8d9`; branch `feat/mini-docker-audit` @ `145e3a6` | The implementation being redesigned |

Prior-art runtimes are Noctalia (Luau) and DankMaterialShell (Quickshell/QML). Neither runs
inside a Go plugin host — the transport and UI vocabulary differ completely. What ports is
the data acquisition, the state machine, the information architecture, and the operational
lessons; what does not port is the widget vocabulary itself.

---

## 1. Prior art I — Noctalia v5 `mini-docker` (our direct lineage)

**Shape.** Three entries in `plugin.toml`: bar widget `mini-docker`, panel `manager`
(860×620, floating, centre), service `docker-service`, `dependencies = ["docker"]`
(plugin.toml:1-10,28-30,89-96,98-100). No permissions block.

**Architecture — single-writer service.** The service (service.luau:449-465) is the only
component that spawns processes; widget and panel are pure subscribers/publishers over
three global keys: `docker_snapshot` (service→UIs), `docker_command` (UIs→service),
`docker_action_result` (service→panel) (service.luau:1-2,135,293; panel.luau:77,387,403;
widget.luau:57,81). Refreshes coalesce with a `refreshPending`/`refreshAgain` guard and a
monotonic `refreshGeneration` that drops stale async callbacks (service.luau:150-165,
231-235,244-246).

**Data acquisition.** Sequential CLI calls, one JSON object per line, parsed per line with
malformed lines dropped and logged (service.luau:51-64,252-283):
`docker ps -a --format '{{json .}}'` (required — failure aborts the refresh),
`docker images`, `docker volume ls`, `docker network ls` (secondary — failures ignored),
plus `docker image inspect --format '{{json .Config.ExposedPorts}}'` to preselect a port.
Actions: `start|stop|restart|rm <id>`, `rmi`, `volume rm`, `network rm`, `run -d` with
name/env/port/network (service.luau:371-439). Default poll 5 s, clamped 1–30
(service.luau:146-148).

**Change detection.** A `dataSignature` over each command's `exitCode\0stdout\0stderr`
increments `snapshot.revision` only on real change (service.luau:166-171,223-229) — cheap,
and directly relevant to the host's update budget (§5).

**Features.** Four tabs: Containers (start/stop/restart/remove; remove only when stopped),
Images (run form; remove only when not referenced), Volumes (remove), Networks (remove;
`bridge`/`host`/`none` hard-blocked) (panel.luau:216-226; service.luau:383-389). Running
images are flagged by cross-referencing containers, including by 12-char ID prefix
(service.luau:118-131). The run form takes name, network, publish-port toggle, port, and a
multiline env textarea; the service validates name regex, port range, and env key shape
before anything reaches argv (panel.luau:261-306; service.luau:393-421).

**UI anatomy.** Bar: glyph 16 (`brand-docker`), optional bold running count, 7×7 status dot
tinted by active/inactive colour choice, column when the bar is vertical (widget.luau:27-55).
Panel: header (glyph 24, title 18 bold, refresh + close) → tab row with a "Last updated"
label → a single status column that escalates loading → busy → error → feedback
(panel.luau:319-361) → a scrollable list of button-cards whose selected row expands into a
detail card with a right-aligned action cluster (panel.luau:121-156,230-256). Empty states
are a centred 42 px tab glyph plus message. Gaps follow a 3/4/6/8/10/12 scale.

**Failure handling.** Missing binary → `available=false` + "docker_missing", arrays
emptied, snapshot still published (service.luau:164-174). Daemon down → `available=false` +
trimmed stderr (198-208). Secondary command failures are silent. Action failure → toast plus
a feedback line that persists until the next state change (298-308; panel.luau:403-421).

**Where it is dishonest or thin.** The README claims "destructive actions require
confirmation" but no confirmation exists — `onRemove` sends the command directly
(README:52 vs panel.luau:488-500). There is no row cap; `showing_limit` is a dead
translation key (translations/en.json:44). i18n keys for the image-in-use state are unused.

**Numbers.** Read timeout 30 s, action timeout 60 s (service.luau:46,438); panel 860×620;
no container cap.

---

## 2. Prior art II — DmsDockerManager (container + compose manager)

**Shape.** A DMS bar-widget plugin (`plugin.json:1-18`, id `dockerManager`, GPLv3,
`permissions: [settings_read, settings_write, process]`), 400×500 popout, v1.3.2. All docker
I/O lives in a `pragma Singleton` service; the widget reads DMS global vars
(DockerService.qml:245-249,135; DockerWidget.qml:56-84). Nothing blocks the UI thread:
one long-lived `docker events` process with a line parser, plus callback one-shots.

**Data acquisition — event-driven, not polled.** `docker events --format json --filter
type=container` (DockerService.qml:56) with a 300 ms debounce after relevant actions
(76-89,65-70); polling defaults to 0/disabled as a fallback (112-120). Containers are
modelled from `docker container inspect $(docker container ls -aq)` — whole-array JSON with
per-element try/catch, dropping bad elements (145-194). The events child auto-restarts 5 s
after death (92-110). Actions are wrapped in `systemd-run --user --scope` so they survive a
plugin or shell restart (261,298).

**Features.** Container list (all), start/stop/restart/pause/unpause, logs and an
interactive shell launched in the user's terminal, and Compose projects grouped purely from
container labels (209-234) with Start All / Restart All / Stop All / View Logs
(DockerWidget.qml:773-810). No images, volumes, networks, stats, or pruning — the ambition
here is interaction depth, not breadth.

**Notable engineering.** A shell-escaping helper with a conservative whitelist
(DockerService.qml:316-344); deterministic ordering running > paused > other, then recency,
then name (194-207); a full keyboard state machine including action-menu sub-navigation
(DockerWidget.qml:136-366). Regrettable: `compose up/down/pull` are implemented but
unreachable from the UI (281-288 vs 773-810) — dead paths, a caution against building
capability without a caller.

**Failure handling is its weak point.** `docker info` non-zero → "not available" + red icon
(133-141); but permission-denied is indistinguishable from daemon-down (exit code only,
stderr discarded), actions are fire-and-forget with an optimistic "Executing …" toast
(116-126) and failures are never reported, and Stop has no confirmation.

**Numbers.** Debounce 300 ms (100–2000); polling 0 (0–120000); popout 400×500; row heights
52/48/38; action buttons 44; port chips 24.

---

## 3. Prior art III — Noctalia v4 and the v4→v5 delta

**Shape.** A standalone 850×600 QML panel with a left sidebar (56 collapsed / 200 expanded)
over four tabs, and a bar widget showing a status dot only (no count). Per-component
`Process` objects rather than a service; a single serial command runner with a busy-guard
(Panel.qml:18-19,31-39,129-169).

**Row anatomy.** Each entity type has a delegate: container rows carry a 10 px state dot,
name + `image (status)`, and 32×32 start/stop-toggle, restart, and trash buttons
(ContainerDelegate.qml:48-101); image rows carry a glyph, `repo:tag`, `size • created`, and
Run + trash (ImageDelegate.qml:41-87); volume and network rows are single-button with a
glyph. All four expand inline into a two-column detail grid with a 200 ms accordion
animation (e.g. ContainerDelegate.qml:106-138).

**The run dialog.** `docker run -d` composed from name, network dropdown, host port
(1–65535), a publish toggle (default on), and dynamic add/remove env KV rows
(RunImageDialog.qml:149-199; Panel.qml:77-99). Two v4-only behaviours stand out: a
**port-conflict preflight** — `ss -tln | grep :<port>` before running, with a per-image
`guessDefaultPort` fallback (Panel.qml:377-411; dockerUtils.js:153-169) — and port
pre-selection by inspecting the image's exposed ports (dockerUtils.js:117-135).

**Delta v4 → v5.** v5 kept the four tabs and the same action set, and added: a central
service, snapshot pub/sub, timeouts, a busy/error/updated status column, revision change
detection, server-side input validation, default-network protection, a numeric bar count,
vertical-bar support, and IPC refresh. v5 dropped: the port-conflict preflight, the inline
accordion (replaced by select + detail card), dynamic env rows (replaced by a textarea), the
bar context menu to widget settings, the free icon-colour choice, the sidebar, and the
`ss`-based port check. Neither version ever implemented `docker pull` despite claiming it in
the v4 CHANGELOG, and both never showed logs, exec, stats, or Compose.

---

## 4. Cross-prior-art comparison

| Capability | noctalia v5 | DMS | noctalia v4 |
|---|---|---|---|
| Containers: list all, running-first | yes (client sort only for running images) | yes (running > paused > other) | yes (docker order) |
| Container start/stop/restart | yes | yes (+pause/unpause) | yes |
| Container remove | yes (stopped only) | no | yes |
| Images: list / run / remove | yes / yes / yes | no | yes / yes / yes |
| Volumes / Networks | yes / yes | no / no | yes / yes |
| Run-image form | name, network, port, env textarea | no | name, network, port, env KV rows |
| Port-conflict preflight | no | no | yes (`ss -tln`) |
| Per-row detail | selection card | expandable actions | inline accordion |
| Logs / interactive shell | no | yes (terminal) | no |
| Compose grouping / lifecycle | no | yes (labels) / partial UI | no |
| Live container events | no | yes (debounced) | no |
| Confirmations for destructive actions | claimed, absent | absent | absent |
| Cross-cutting | i18n, revision change detection, timeouts, server-side validation | escaping discipline, systemd-run, keyboard nav | i18n, port guess heuristics |

**What the three collectively establish.** (1) One blessed writer owning all subprocesses
beats per-component spawning — v5 and DMS both converged on it. (2) Change detection before
republishing is a real requirement, not polish. (3) Every implementation that fires
destructive actions without confirmation and without capturing the failure has no way to
tell the user the action failed — all three are guilty. (4) The genuinely useful parts of
the "manager" scope beyond the four tabs are the run-image workflow with a port
preflight, and Compose grouping from labels (zero extra daemon calls). (5) Logs/exec are
solved by handing off to a terminal, not by rendering a log pane.

---

## 5. What sysc-shell can render (pinned `v0.0.0-20260923111103-dd221a064353`)

Ground truth: `plugin/v1` (protocol + validator), `plugin/lint`, `internal/plugin`
(manifest, convert, prepare, hostcall), `internal/shell/pluginhost.go` (host). This section
is the design envelope; everything below is verified against that pin.

### 5.1 Surfaces and view roots

| Surface | Root must be | Fixed size | Notes |
|---|---|---|---|
| Bar view | `row` | 240×32 | no list/drag/drop/separator/image; interactive nodes allowed |
| Tooltip view | `column` | 280×200 | read-only — interactive kinds are rejected |
| Panel view | `column`, or `list` (→ scroll) | manifest `width`/`height` (64..4096) | everything else legal |

`placement` accepts only `attached` (manifest.go:530-532) — there is no floating panel in
v1. A rejected tree is logged and replaced by an error card (bar slot paints the label;
panels paint Close/Retry/Disable) — not silent, but visible only in the journal.

### 5.2 Node vocabulary (what we can draw)

- **Kinds:** `row`, `column`, `text`, `icon`, `progress`, `button`, `text_input`, `list`,
  `drag_source`, `drop_zone`, `gauge`, `graph`, `separator`, `image`.
- **Containers:** `row`, `column`, `list`, `drop_zone`; only containers and `button` take
  children, and a button's children cannot be interactive.
- **Interactive kinds:** `button`, `text_input`, `drag_source` — an interactive node must
  carry `id`, `name`, `role`, and a non-empty `events` list from its allowed matrix
  (button→activate/pointer, text_input→change/submit, list→scroll, drop_zone→drop).
- **Text:** four tones only — normal, `error`, `subtle`, `accent`; sizes `body`, `caption`,
  `label`, `title`, `headline`, `display`, `mono`; `bold`, `tabular`, `pin_end` (right-pins
  the last child of a 2-child row), `center_x`.
- **Shape/fill:** `fill` ∈ surface/accent/container/error/soft/card/outline/chip/
  error-container; `shape` ∈ circle/stadium/small/medium/large/card/panel; `radius`,
  `stroke`, `stroke_fill` (row/column/button only).
- **Values:** `progress`/`gauge` take a 0..1 value and optional `value_text`; `graph` takes
  2..64 samples; `animate` eases progress/gauge values but requires a `key`.
- **Media:** `image` takes an absolute filesystem path plus a square size or explicit box,
  optional cover `background`; panel-only. (kdeconnect already ships PNG assets and resolves
  them from `os.Executable()/../assets` — a precedent for a shipped Docker mark.)
- **Lists:** a `list` node is a scroll region with a fixed `Height`; `scroll` events are
  declared by the protocol but the host never delivers them (§5.4).

### 5.3 Interaction and host calls

An event arrives as `input.event` carrying the view id, the revision the user actually saw,
the node id, and the event kind. The complete set of host calls a plugin can make:
`state.get|set|list`, `panel.open|close|resize`, `view.focus`, `notify`, and
`output.context`. Panel open is the `Button-ID + CallPanelOpen` pattern: a bar button with
`id: "open"`, the plugin matching `msg.Node == "open"` and calling `panel.open` with the
view's output and instance (weather is the reference). `panel.open` **toggles** — the same
call closes an owned panel. Nothing else is gated: the plugin runs as the user with full OS
privileges, so spawning `docker` (as this plugin already does) is normal; host calls gate
shell surfaces only.

### 5.4 Host limits and rejection rules

| Limit | Value | Consequence for this plugin |
|---|---|---|
| MaxNodes | 1024 (root counts) | a 150-row panel already lands ≈905 nodes; budget rows explicitly |
| MaxDepth / MaxChildren | 16 / 256 | fine; forces flat composition |
| MaxViews | 64 per plugin | irrelevant |
| UpdatesPerSecond / burst | 60 / 120 | full-tree republish every 5 s is fine; bursts are not |
| Layout budget | 8 ms/job, 3 overruns/10 s ⇒ plugin "degraded" | keeps giant trees expensive |
| Message / text / tooltip caps | 1 MiB frame, 64 KiB text, 256 B tooltip | fine |
| State | 256 KiB/value, 4 MiB total, 256 keys | ample |
| Settings | ≤64 keys, ≤64 options | ample |

Snapshots replace a tree at any revision; patches require an exact base revision and are
re-validated; a dropped patch triggers `view.resync`, which the plugin must handle by
resetting its revision and re-snapshotting. Unknown JSON fields anywhere are a **decode
error that kills the session**, so an over-eager or stale manifest is fatal, not cosmetic.

### 5.5 Settings

The host renders `bool` as a toggle, `int` as a slider, `select` as a menu, and `float`,
`string`, `color`, `file`, `folder` as bare text fields (no picker, no colour wheel); only
`int`/`float` honour min/max. Values are merged over defaults and pushed via
`settings.changed`. **There is no migration on schema change**: a stored key that the
current schema no longer declares stays in the stored map and makes every later write to
that plugin fail validation — the schema is effectively additive-only.

### 5.6 `plugin/lint`

`plugin/lint.Tree(root, view, width, height) []Finding` runs the host's real pipeline
(`v1.Validate` → converter → fit check) with the host's own text metric and returns every
geometry violation at once. Bar and tooltip sizes are constants (240×32, 280×200); panels
should be linted at the manifest box, with the caveat that an `include_settings` panel lays
out in a smaller box. This is the prescribed way to make a view that validates but cannot be
drawn fail in `go test` (docs/plugin-ui-rules.md:14-41).

### 5.7 Icons

Two catalogues, resolved project-font first: 69 project names
(`cloud, network, devices, folder-open, share, ghost, schedule, refresh, close, stop,
replay, notifications, …`) and 85 Material names
(`play_arrow, pause, restart_alt, power_settings_new, delete, check, expand_more,
chevron_left/right, settings, description, lan, folder_open, wallpaper, search,
speed, visibility, …`). Names must be lower-case; an unknown name passes the wire validator
and then **fails the view** in the converter, listing both catalogues. There is no
Docker/container/image/volume/network-stack glyph in either catalogue — the nearest
mappings are `devices`/`widgets`/`apps` for containers, `wallpaper` for images,
`folder-open`/`folder_open` for volumes, `lan`/`network` for networks.

### 5.8 What is not renderable

Not in the protocol at all: tabs/accordions, checkbox/radio/toggle/slider/segmented
controls, grids or tables, multi-select, date pickers, rich text or links, arbitrary
drawing, images on the bar, images from bytes/URLs, per-row custom widgets, keyboard-driven
navigation, and any desktop-widget surface. Present but not usable: `notify` (the
capability is granted but the production shell never wires a notifier — calls fail with
"notifications are not available"), `plugin.status` (ignored by the host),
widget-level instance settings (parsed, never rendered or written), list `scroll` events
(never delivered), and `protocol.minor` beyond the manifest gate (the host advertises 1.7 in
`host.hello` but rejects a manifest declaring minor 7 — the gate is 6).

### 5.9 Constraints that will bite a Docker manager

1. **Node budget.** Every row is 4–6 nodes; a four-tab manager with per-row action clusters
   must cap rows and compute the tree size arithmetic, or it will be rejected at render.
2. **No tabs, no modals, no checkbox.** Navigation is a button row plus a swapped subtree;
   a "confirm" is a second click on an armed button; a form's network choice is a cycling
   button or a list of option buttons.
3. **Destructive-action feedback must be in-tree.** Without notifications, an action's
   failure must surface as text on the panel (the current `actErr` pattern) or it is
   invisible.
4. **Bar/tooltip are text-and-icon surfaces.** The bar cannot carry a count badge drawn as
   an image; the pill is text + at most one icon.
5. **Additive-only settings.** Renaming or dropping a setting without a shell-side fix
   bricks future writes for that plugin.

---

## 6. What exists today (branch `feat/mini-docker-audit`, v0.3.0)

The plugin on `main` is v0.2.0 — a first sweep that could not open its own panel. The
branch `feat/mini-docker-audit` (unmerged, worktree `.worktrees/feat/mini-docker-audit`)
carries the audit-driven tranche: reachable panel (bar button `id: "open"` → CallPanelOpen),
mutex-serialized state, off-loop refresh, diagnosed errors (daemon down / binary missing /
500-byte stderr tail), a 15 s action timeout, persistent action errors, a 1 MiB stdout
bound, running-first ordering, a scrollable capped list (150 rows, "+N more"), a read-only
tooltip, `status_mode` reduced to honest labels, table-driven action parsing behind an ID
allow-list, and a live test behind `-tags live` (v0.3.0, manifest 480×560, capabilities
`panels`+`settings`).

The tranche's own independent review returned **fix-first**, with these open findings:

| Severity | Finding |
|---|---|
| P1 | The workplan claims the live gate verified "11 containers, 6 running"; the live test asserts nothing numeric and only logs those counts |
| P2 | `c.Call` (panel open) writes the stdout encoder outside the mutex; the client has no write mutex → interleaved frames are possible |
| P2 | No `ViewResync` case; a dropped/coalesced snapshot leaves a frozen panel until close/reopen |
| P2 | Concurrent action goroutines can outrun the host revision — same freeze, user-reachable |
| P3 | `"+N more"` is appended before the list, so it renders above it |
| P3 | `CLI.List`'s production parser is still untested (the test re-implements the loop) |
| P3 | ~905 nodes republished every tick with no change detection |
| P3 | Stale container IDs are accepted (shape-validated, not existence-checked) |
| P3 | Two weak tests (over-disable passes; the non-blocking canary is bar-coupled) |
| P3 | Manifest `protocol.minor: 1` understates the fields actually used |
| P3 | An interval-only settings change never republishes; `status_mode: hidden` still renders a "docker" pill |

Also unfinished or unregistered: roadmap item T4.2 (the integration gate test) was never
delivered; T0.4 (keep-last-known rendering) is not in the code; T2.3 landed only its
ordering half; the seven workstream docs are in no `docs/plans/README.md` register (on
either branch), and the tranche review doc is untracked. `main` and the branch have not
been reconciled; worktrees for five other branches are live in the same repo.

---

## 7. Synthesis — what ports, what needs the shell, what is out

| Capability (prior art) | Available today | Disposition for the design |
|---|---|---|
| Container list, running-first, start/stop/restart | yes (`list`, rows, buttons) | keep; already shipped on the branch |
| Container remove | yes | include only with an armed-confirm two-tap and an in-tree failure path |
| Images / volumes / networks tabs | yes, as button-row + swapped subtree | scope decision; each tab is cheap to add and expensive in node budget |
| Run-image workflow (name, port, env, network) | `text_input` (panel-only), no select/checkbox | portable with substitutions: cycling network button, publish toggle button, multiline env field |
| Port-conflict preflight (v4-only) | plugin can read `/proc/net/tcp` or spawn `ss` | cheap, genuinely useful; revive |
| Port pre-selection from image inspect | one `docker image inspect` call | cheap; revive |
| Per-row expandable detail (v4) | per-row tree swap | possible, but multiplies node cost; prefer a detail card like v5 |
| Compose grouping from labels (DMS) | tree nesting; zero extra daemon calls | strong candidate: high value, no new I/O |
| `docker events` + debounce (DMS) | the plugin owns its children | optional; polling at 5 s may be enough, and the host budget discourages churn |
| Logs / interactive shell (DMS) | plugin spawns a terminal itself; no log pane is renderable | policy decision — a "shell out to terminal" action is possible but changes the plugin's threat surface |
| Stats / graphs | `progress`, `gauge`, `graph` exist | YAGNI unless asked; `docker stats` is a streaming cost with no cheap diff |
| Lifecycle notifications | `notify` is dead in this build | out of scope until the shell wires a notifier (candidate shell-side issue) |
| Keyboard navigation, rich rows | not renderable | out |
| i18n | not in the protocol | out (English labels, like every other plugin here) |
| Docker glyph on the bar | no matching icon; bar rejects images | decide: text-only pill (today), nearest existing icon (`devices`), or a shell font addition |

**Shell-side asks this research surfaces** (candidates for the sysc-shell bd tracker, not
work for this repo):

1. Wire the `notify` capability (`BindPlugins` never receives a notifier; `call notify`
   fails at runtime).
2. Add container/image/volume/network glyphs to the font catalogue, or an alias registry so
   plugins are not blocked on a shell release for one icon.
3. Make `include_settings` schema-driven instead of the recorder's hard-coded groups.
4. Add wire node kinds for checkbox/slider/select (the host already renders them internally)
   — the missing input vocabulary is the single biggest constraint on management UIs.
5. Prune or warn on stored setting keys that the current schema no longer declares.
6. Reconcile the manifest protocol gate (`MaxMinor: 6`) with `host.hello` (advertises 1.7).

**Open questions the design must answer.**

1. **Scope.** Containers only, the four-tab manager, or manager + Compose grouping? Each step
   is defensible; the node budget and the "who is this for" question decide it.
2. **Base.** Does the design build on the unmerged v0.3.0 branch (and the merge happens
   first), or restate the target from `main`?
3. **The review's fix-first findings.** Are the P1/P2 items a prerequisite patch, or does the
   redesign absorb them (resync handling and the encoder mutex are both in the publish path
   the redesign will touch anyway)?
4. **Refresh model.** Keep the 5 s poll, or adopt events + debounce with polling as a
   fallback? The host's update budget tolerates either.
5. **Destructive policy.** Armed two-tap confirmation for remove/stop, or none (as all three
   prior arts ship)?
6. **Verification standard.** What counts as a live gate this time — the numeric-free live
   test is exactly what the tranche review failed the workplan for.

Next step: the design doc, which fixes these decisions and the contract the implementation
plan will follow.
