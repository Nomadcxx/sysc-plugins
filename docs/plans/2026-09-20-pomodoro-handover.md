# Handover: Pomodoro timer redesign — audit + gap analysis commission

Date: 2026-09-20
From: the agent that executed the timer/world-clock parity plan and the pomodoro redesign
To: the auditing agent taking over
Repos: `/home/nomadx/sysc-plugins` (main @ `a3be2f3`), `/home/nomadx/sysc-shell` (main @ `8602bfa`)

You are commissioned to (1) bring the timer plugin back up on this desktop, (2) audit the work
listed below, (3) run a gap analysis against the prior art and the user's expectations, and (4)
produce a filled ledger the user can pick from. Do not trust this document's claims of quality —
verify everything; the user has already caught this session over-claiming twice.

---

## 0. IMMEDIATE: why the timer says "failed" (verified root cause)

The installed shell binary `/home/nomadx/.local/bin/sysc-shell` was built at **04:34**, but the
gauge vocabulary commits landed at **04:51–04:52** (`5674306` supervisor minor 3, `8602bfa`
gauge kind). The running shell therefore predates the gauge: it advertises protocol minor 2,
while the rebuilt timer plugin (`a3be2f3`) ships `manifest.json` with `"protocol": {"major": 1,
"minor": 3}` and emits `v1.KindGauge` nodes. The shell rejects the plugin at start — the timer
process is absent (only weather + world-clock run), and the bar shows the failed placeholder.

Fix (verified against repo state, not yet executed — the user asked for handover first):

```sh
cd /home/nomadx/sysc-shell && go build -o /home/nomadx/.local/bin/sysc-shell ./cmd/sysc-shell
systemctl --user restart sysc-shell.service
sleep 5 && pgrep -a sysc-plugin   # expect weather, timer, world-clock
```

If the timer still fails after that, check `journalctl --user -u sysc-shell --since '-5 min'`
(note: the shell logs little to journald; plugin start failures may not appear — the bar
placeholder label is often the only signal). Also try the binary directly:
`~/.config/sysc-shell/plugins/timer/bin/sysc-plugin-timer </dev/null; echo $?` (exits 0 on clean
EOF handshake failure — not diagnostic by itself).

## 1. What this session built (audit trail)

### sysc-shell (all on main, pushed)
| Commit | Subject | Why it matters |
|---|---|---|
| `04fd1c3` | feat(plugin): advertise protocol minor 2 | supervisor Supported bump + node.go doc note (parity plan Task 3) |
| `651ede4` | fix(ui): measure a column by its widest child | ROOT CAUSE of the world-clock panel crash: `measureNode` gave `Width<=0` columns a flat 220px, so the world-clock zone-card row measured ~764px in a 404px list and the Remove button hard-failed layout (`child 3 of kind 3 does not fit in 404x34`). Regression test `TestLayoutRowMeasuresColumnsByContent` in `internal/ui/layout_test.go`. |
| `fed2d9f` | feat(shell): anchor plugin panels under the clicked bar widget | Plugin panels previously opened at a default position (`trig := Trigger{}` never set AnchorX). Now `registry.go` computes `bar.actionCenterX(action)` at the pluginFn call sites (bar lock is released there — safe), `pluginHost.deliver` stashes it in `lastAnchor[pluginID]`, `openPanel` reads+clears it into `trig.AnchorX`. Test `TestPluginPanelAnchorsUnderTheClickedWidget`. |
| `5674306` | feat(plugin): expose the radial gauge to plugins | supervisor `Supported: []v1.Version{{Major: 1, Minor: 3}}` (internal/plugin/supervisor.go:201). NOTE: this commit contains ONLY the supervisor bump — the vocabulary edits were wiped by a concurrent session and re-landed in the next commit. |
| `8602bfa` | feat(plugin): add the gauge kind and value text | `plugin/v1/node.go`: `KindGauge "gauge"` (leaf: not container/interactive/keyboard, no allowedEvents entry, no Fill — arc color is theme accent), `Node.ValueText` (`value_text,omitempty`), validation (finite, 0..1, MaxTextBytes). `internal/plugin/view.go`: Convert case → `ui.KindRadialGauge{Value, ValueText, Icon}`. Tests: `TestValidateBoundsGaugeValue`, converter kind-coverage case. |

`ui.KindRadialGauge` pre-existed (system monitor uses it): renderer `internal/render/radial.go`
`paintRadialGauge` — square box min(W,H) centered, track `style.Track`, arc from top clockwise
for `Value`, color `LerpColor(Accent, Secondary, progress)`, centre shows `Icon` or `ValueText`
(default `%.0f%%`). The gauge is a layout LEAF — children are NOT laid out, so the mode label
must be a sibling below the ring.

### sysc-plugins (main @ `a3be2f3`, branch feat/pomodoro merged ff, pushed)
| Commit | Subject | Content |
|---|---|---|
| `04cfde7` | chore(deps): pin sysc-shell to protocol minor 2 main | go.mod pin (parity prerequisite) |
| `cc93243` | feat(timer): positional digit durations | `ParseDuration` digit blocks: `130`→90s, `1030`→10m30s, `10300`→1h3m, 7+ digits rejected (the plan snippet alone would have accepted them — added `len(s) > 6` guard) |
| `8c991bd` | feat(timer): noctalia panel on minor-2 vocabulary | display-size remaining, accent Start, error Reset, input disabled while running, close-on-start (paused Resume stays open) — NOW SUPERSEDED by the pomodoro rebuild |
| `0388f23` | feat(world-clock): noctalia zone cards on minor-2 vocabulary | card rows (Fill card, Radius 10), header + accent Add, empty state; this is the tree that crashed before `651ede4` |
| `798bc27` | test(screen-recorder): update bar tests to the toggle control | UNPLANNED: 5 stale tests (old `camera`/`record`/`stop` bar nodes) blocked the Task 7 gate; updated to the single `toggle` node model, `hide_inactive` now asserts 0 children |
| `8f44309` | chore(plugins): declare protocol minor 2 for timer and world clock | manifests 1.2.0 / minor 2 (timer now 1.3.0 / minor 3) |
| `a4a1630` | fix(timer): ignore duration edits while counting | duration-input guard while running/paused; fallback handshake identities aligned to manifests |
| `3b9610e` | chore(deps): pin sysc-shell to the gauge vocabulary | pin `v0.0.0-20260919185221-8602bfa185f5` |
| `a3be2f3` | feat(timer): rebuild the timer as a pomodoro session | the redesign, detailed below |

### The pomodoro redesign (`a3be2f3`) — audit this hardest
- `plugins/timer/session.go` (new): `Session` wraps `Timer`. `Mode` work/short/long; `completed`
  tally; `sessions` before long break (default 4, clamped ≥1); `autoWork`/`autoBreak`;
  `SetMode` cancels + resets; `Tick` on done: work → completed++, next = long iff
  `completed%sessions==0` else short, auto-start per `autoBreak`; break → work per `autoWork`.
  Delegates Remaining/Running/Duration/Progress/State/Start/Pause/Reset/Deadline/Restore.
- `plugins/timer/view.go`: `PanelTree(remaining, state, progress, mode, completed, sessions)` —
  root column **Padding 16 Gap 12** (user complained about clipping); header row `PinEnd:true`
  ["Pomodoro Timer" Size title Bold, close button `✕` ID close]; subtitle
  "Focus session • N completed" ToneSubtle caption CenterX; `KindGauge Height 160,
  Value = 1-Progress, ValueText = remaining`; mode label below ring; controls row
  (start/pause/resume Fill accent|soft + reset Fill soft); pills `mode-work`/`mode-short`/
  `mode-long` (active Fill accent, inactive Fill chip); footer column Fill card Radius 10
  Padding 10 with ✓ + "N pomodoros completed" + "Next long break after M more".
  `BarTree`/`TooltipTree` unchanged in shape.
- `cmd/sysc-plugin-timer/main.go`: Session wiring; settings `work_duration`/
  `short_break_duration`/`long_break_duration` (int minutes), `sessions_before_long_break`,
  `auto_start_work`, `auto_start_breaks`, `show_when_idle`; nodes close/mode-*; transition
  notify ("Work complete — time for a break" / "Break over — back to work"); deadline
  save/restore unchanged.
- `plugins/timer/manifest.json`: 1.3.0, protocol minor 3, panel **360x480**, the settings above
  (dropped `default_duration`).
- Tests: `session_test.go` (5 tests: work→short, long cadence, auto-start both flags, SetMode
  resets, sessions clamp), `view_test.go` `TestPanelTreeIsThePomodoroLayout` + validate across
  all states × modes. Full `go test -race ./...` green at commit time.

### Deliberate cuts (flagged to the user, not hidden)
- **Text-only controls.** The spec's icons (briefcase, coffee, couch, pause, reset, check) are
  NOT rendered: the plugin converter resolves v1 icons via `render.IconByName` = the project's
  own icon font catalogue only (`internal/render/iconfont.go` ~line 344 — no pause/play/check/
  coffee/briefcase/couch). The material subset (`internal/render/materialfont.go:23`) HAS
  pause/play_arrow/restart_alt/check/coffee but NOT briefcase/couch — and plugins cannot reach
  material names at all today (converter hard-errors on unknown names). Supporting material
  icons from plugins is a shell-side vocabulary change (ui.KindIcon path), not a plugin fix.
- The old duration text input was REMOVED (pomodoro phases are settings-driven). The
  per-keystroke flicker ledger item is thereby moot for the panel.

## 2. Desktop / environment playbook

- Shell is a systemd **user** unit: `sysc-shell.service`
  (`~/.config/systemd/user/sysc-shell.service`). Restart with
  `systemctl --user restart sysc-shell.service`. Plugins respawn with it.
- Plugin install root: `~/.config/sysc-shell/plugins/<dir>` (symlinks into the repo; kdeconnect
  symlink points at the feat/kdeconnect worktree but is NOT enabled).
- Config: `~/.config/sysc-shell/config.json`. Structure: top-level `bar`, `theme`, `weather`,
  `templates`, `plugins`. `plugins.enabled` lists plugin IDs; **`bar.items.{left,center,right}`
  need explicit plugin entries** — `{"id":"plugin","plugin":"org.sysc.timer","entry":"bar",
  "instance":"timer-1"}` — enabling alone shows nothing. Current state: enabled =
  [org.sysc.weather, org.sysc.timer, org.sysc.world-clock]; right side has timer-1 +
  worldclock-1 entries after weather. Backups: `config.json.bak-pre-plugins-20260920`,
  `config.json.bak2`.
- Deploy: `cd /home/nomadx/sysc-shell && go build -o /home/nomadx/.local/bin/sysc-shell
  ./cmd/sysc-shell`; `cd /home/nomadx/sysc-plugins && make build && make install`; restart the
  unit. ALWAYS rebuild the shell when plugin protocol minor bumps — this is the failure in §0.
- Screenshots: `eval "$(systemctl --user show-environment | grep -E '^(WAYLAND_DISPLAY|
  XDG_RUNTIME_DIR|DISPLAY|XDG_SESSION_TYPE)=')"` then `grim /tmp/x.png` (full screen 6000x1440;
  `grim -o <name>` needs the niri output name from `niri msg outputs`). To VIEW an image you
  must copy it under `/home/nomadx/opencode-cursor/` (read_media_file path restriction) — and
  the user has disputed screenshot readings before: zoom and verify before claiming anything
  renders.
- journald is sparse for this unit; do not treat "no error lines" as "no errors".

## 3. Hazards and gotchas (learned the hard way)

1. **Concurrent session**: another agent session is active on this machine. It wiped the gauge
   vocabulary edits once (between `5674306` and `8602bfa`) and overwrote an in-progress
   `layout.go` edit. Re-verify file state before every edit; never force-push shared branches.
2. **commit-msg hook** (both repos, `core.hooksPath=/home/nomadx/.git-hooks`): rejects messages
   containing (case-insensitive) claude, anthropic, chatgpt, openai, copilot, cursor, cody,
   tabnine, codex, gemini, bard, `gpt-[0-9]`, llm, "ai assistant", **bot**, agent. The word
   "both" contains "bot" — avoid it.
3. **bd pre-commit hook** in sysc-shell auto-stages `.beads/issues.jsonl` into every commit.
4. **Flaky test**: `internal/shell` `TestClipboardKeyboardRestoresDeletesAndNavigatesByID` has a
   pre-existing data race (surfaceFrameLoop write panelhost.go:2563 vs read :2530). Rerun that
   package alone; unrelated to plugin work.
5. **Freeze suspect (unfixed)**: a handler panic while holding `r.mu` leaves the registry lock
   held ("shell: closing without r.mu; a handler panic left it held") → every later panel op
   blocks → shell hang requiring SIGKILL. The user's world-clock freeze (opened once OK, second
   open errored, then freeze) may have been this plus the layout failure. Revisit only if it
   reproduces after `651ede4`.
6. **Layout hard-fails**: buttons/drag-sources error if taller than their container or past the
   right edge; text may clip silently. Scroll lists clip overflow without error. PinEnd only
   works on two-child rows (a PinEnd on a child column is a no-op).
7. Review subagents hit quota errors this session ("this model is not included in your free
   usage") — review inline instead.

## 4. User expectations (verbatim where it matters)

- m0202: "Ok here is what just happend.. I clicked to open the panel, the panel opened once,
  then again I tried and it errored, something about the child, then sysc-shell froze and I had
  to restart it. What I can see is that this is a huge fail both from a UI perspective (it looks
  exactly what it looked like before.. No half gauge/radial pomodoro style timer, just a buggy
  mess that looks no different to what was designed previously.. are you sure this is what you
  wanted to develop? You also added back Notes and GPU screen recorder and yet I never asked you
  to do that. Can you confirm what you THINK you are trying to develop in this session?"
- m0207: "Yes, lets turn off note and recorder, as for where the crash happens it happens with
  the world clock. In terms of the radial/half-guage not being part of the design and plan for
  the timer, thats an error on the side of the comissioning and or relevant agent that wrote
  it.. the prior art provided was literally for a pomdoro timer and links were provided.. What I
  see here is you added a simple horizontal progress bar and a simple colored counter.. Yet
  everything about this is both wrong and not working.. first the timer panel does not open
  under where its clicked.. When I click the timer I expect the panel to come down under it, not
  somewhere random (which is what its currently doing, left of screen) then when the panel opens
  I expect to see a styled elements.. look at the bar.. its gets clipped by the panel borders,
  no padding, look at the input field.. not styled.. just an input box that takes the runs the
  full width of the panel. Perhaps your reference here should be prior art and what we've
  developed elsewhere e.g. the weather panel, system monitor, notifcations panel etc etc etc"
- m0463: "No it doesn't launch just says failed.. please provied a handover .md that comissions
  another agent to audit your work and undertake a gap analysis relative to prior art and user
  expectations."

Course corrections already applied: notes + screen-recorder reverted out of the config (user
never asked for them); radial dial was the commissioned design; panels must anchor under the
clicked widget; panels must have padding/styling.

## 5. Prior art + the visual spec

- https://github.com/AvengeMedia/dms-plugins/tree/master/DankPomodoroTimer (QML widget)
- https://github.com/noctalia-dev/community-plugins/tree/main/pomodoro (Luau; settings: work 25 /
  short 5 / long 15, sessions-before-long-break 4, auto-start-work/breaks, notify+sound on
  transitions, bar shows status + remaining when panel closed; `open_near_click=true` anchoring)
- https://github.com/noctalia-dev/legacy-v4-plugins/tree/main/pomodoro
- User's structured description of `pomdoro.png` (the authoritative visual spec): dark theme;
  header "Pomodoro Timer" + close X top-right; subtext "Focus session • 0 completed"; circular
  progress ring, light purple/lavender on dark track, bold digital time inside, "Work" beneath;
  centered pause/stop/reset icons below the ring; three rounded pill buttons Work (briefcase,
  active/highlighted) / Short Break (coffee) / Long Break (couch); footer dark rounded card with
  checkmark: "0 pomodoros completed" + "Next long break after 4 more". Near-black #111 surfaces,
  lavender accents, white primary text, muted gray/lavender secondary; bold headers, extra-bold
  large numerals, medium labels.
- Styling references the user named: the shell's OWN panels (weather, system monitor,
  notifications). IMPORTANT: those are shell-internal panels built with full ui primitives
  (capsules etc.) — plugin panels are limited to the v1 vocabulary, so matching that polish may
  require new v1 vocabulary rather than plugin-side tweaks.

## 6. Gap analysis framework (what to audit, concretely)

1. **Launch**: after the §0 fix, does the timer start? Does the bar widget render (schedule
   glyph + remaining)? Does the panel open under the widget (fed2d9f anchoring)?
2. **Visual fidelity vs pomdoro.png**: ring present and animating? time inside the ring? mode
   label beneath? pills shaped/highlighted like the spec? footer card? padding? Compare against
   the shell-internal panels' polish and name every concrete delta (colors, spacing, radii,
   typography) with the v1 vocabulary limitation attached.
3. **Behavior vs noctalia pomodoro**: phase transitions correct (work→short, long every N)?
   auto-start flags honored? notify text sensible? bar shows status + remaining when panel
   closed? persistence across shell restarts (deadline state key)?
4. **Code quality**: `session.go` locking (Session.mu + Timer.mu nesting — deadlock-free?),
   Tick auto-start path, SetDurations partial-update semantics, restore across mode switches
   (Restore restores the raw Timer; the Session mode is NOT persisted — gap?), tests honest?
5. **Protocol hygiene**: minor 3 advertised (shell) and declared (timer manifest) consistently;
   world-clock still minor 2 — fine, but check the shell accepts mixed minors.
6. **The two unfixed shell suspects**: clipboard frame-loop data race; r.mu-held-after-panic.
7. **Over-reach check**: verify nothing else was enabled/changed in the user's config beyond
   weather/timer/world-clock.

## 7. Open ledger (carried from this session)

- B: README status table stale (timer row says "UI rebuild started"; no pomodoro/minor-3 note).
- D: PinEnd no-op on world-clock time column (cosmetic; shell pins two-child rows only).
- E: world-clock "add" button path untested.
- G backlog: mini-docker parity sweep, calendar first-sweep polish, wallpaper-depth stub
  (blocked on shell wallpaper API), kdeconnect P8 + merge (sysc-shell feature/kdeconnect-icons
  also unmerged).
- NEW: material-icon vocabulary for plugins (converter + briefcase/couch glyphs missing).
- NEW: Session mode not persisted across restarts (only the raw deadline is).
- H: rebase not needed — pin `04fd1c3` is ancestor-safe; re-pin only if sysc-shell changes
  plugin/v1 again (it just did: gauge — the pin is already `8602bfa`, so fine).

## 8. Verification protocol

- Gates (sysc-plugins): `gofmt -l .` empty; `go vet ./...`; `go run ./tools/validate-manifests`;
  `go test -race -count=1 ./...`.
- Gates (sysc-shell): `gofmt -l internal/ plugin/` empty; `go vet ./...`;
  `go test -race -count=1 ./...` (internal/shell flake → rerun alone).
- Live: restart unit → 3 plugin processes → open world-clock panel twice (no crash) → open
  timer panel (anchored, padded, ring animates) → run a 1-minute work session via settings →
  verify transition notify + auto-start → screenshot and actually LOOK at it.
- The user is at the desktop and is the final judge. Show, don't claim.
