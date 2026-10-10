# herdr — terminal agent monitor for sysc-shell

Status: design approved 2026-10-11 (maintainer dialogue). Port of the
Noctalia community plugin `herdr` by Hy4ri (MIT), improved in UI/UX and
functionality.

## Goal

Show what the user's herdr sessions and coding agents are doing, from the
sysc-shell bar and an attached panel, and let the user act on them: focus a
blocked agent, read its recent output, start or attach a session, stop or
delete one. Upstream polls the `herdr` CLI every 1–5 s and renders Lua trees;
this port reads the herdr socket API directly, is event-driven, and adds
notifications, an on-demand output peek, and session creation from the panel.

## Decisions already made

| Question | Decision |
|---|---|
| Data layer | herdr unix socket, newline-delimited JSON. One-shot dial per call. One persistent `events.subscribe` connection per running session. `herdr session list --json` CLI only for session discovery |
| Refresh | event-driven: `pane.agent_status_changed` (per-pane subscription), `pane.created/closed`, `workspace.*`; plus a 5 s discovery poll for the session list. No CLI on the poll path |
| Bar | `ai-usage` glyph + needs-you count (blocked+done), worst-status tone; icon-only variant below 120 px; tooltip with the breakdown |
| Panel | 440×560 attached; session cards → workspace blocks → pane rows; refresh + close buttons in header |
| Panel clicks | session header → attach in terminal (`$TERMINAL -e herdr [--session NAME]`, then alacritty/foot/kitty/xfce4-terminal fallbacks); pane row → `agent.focus` over that session's socket |
| Window raise | none — sysc-shell has no compositor focus API; attach is the portable equivalent (upstream uses hyprctl) |
| Notifications | agent→blocked default on, agent→done default off; debounced per flush; informational only (protocol v1 has no notification action callbacks) |
| Output peek | per-row text button; `agent.read` last N lines (`recent`, ANSI stripped), wrapped, mono; expand state kept plugin-side |
| New session | text input + button in panel; name validated `^[A-Za-z0-9._-]{1,64}$`; spawns a terminal running `herdr --session NAME` |
| Stopped sessions | shown (toggleable); workspace names read from `session.json` (`custom_name`, else basename of `identity_cwd`) |
| Colors | sysc-shell fixed tones only: blocked→error, done→accent, working→normal, idle/unknown→subtle. No color settings (platform limit) |
| Deferred to a later version | remote machines (`show_other_machines`), status filter control, notification actions, `pane.output_matched` triggers |
| Base | origin/main @ bfa58e2; branch `feat/herdr-plugin` in worktree `.worktrees/herdr` |

## Verified facts this design rests on

- Socket paths: `~/.config/herdr/herdr.sock` (default), `~/.config/herdr/sessions/<name>/herdr.sock` (named); the mapping comes from `herdr session list --json` (`{name, default, running, session_dir, socket_path}`).
- Calls are one-shot (server closes after each reply): dial → write one line → read one line → close. A connection that sent `events.subscribe` stays open and streams `{"event": ..., "data": ...}` lines.
- `pane.agent_status_changed` subscriptions are per pane (`pane_id` required) and must be reconciled from `pane.created` / `pane.closed`. Verified live: a `report_agent` transition produced the event in real time. `workspace.updated` does not fire for agent status changes.
- Snapshot (`session.snapshot` / `herdr api snapshot`) v0.9.1: `workspaces[]`, `tabs[]`, `panes[]`, `agents[]`, `layouts[]`; agents carry `agent_status` (idle|working|blocked|done|unknown), `state_change_seq` (ordering only, no timestamp), `state_labels` (per-status custom labels), titles and cwds; workspaces carry `tokens` and `worktree {repo_name, ...}`.
- `pane.report_agent` cannot set `done` (detected, not reported) — tests must not rely on reporting it.
- `agent.read {target, source:"recent", lines, strip_ansi:true}` returns the pane's recent output as text; `agent.focus` exists on the socket; `herdr agent focus <target>` exists in the CLI.
- `session.json` (stopped sessions) carries `workspaces[].custom_name` and `identity_cwd`.
- sysc-shell icon catalogue has no robot glyph; `ai-usage` is the AI glyph. `refresh`, `close`, `stop`, `ghost`, `folder-open`, `content-paste` exist.
- No `xdg-terminal-exec` on this machine; `$TERMINAL=ghostty`; alacritty, foot, kitty, xfce4-terminal are installed.
- plugin/v1: `lockedWriter` around the client writer; `CallKind`s for state/panel/notify; `NotifyParams` has no action callback; bar lints at 240×32, panel at manifest size via `shelllint.Tree`; text measures 8 px per byte, 16 px tall; row children must fit or the row is refused.

## Components

```
plugins/herdr/            Package (library, unit-testable)
  socket.go               Call + Subscribe client (newline JSON, reconnect backoff)
  discover.go             session list + session.json parsing
  model.go                snapshot → view model: statuses, priority, sorting, titles
  settings.go             settings parsing/defaults
  service.go              poll + subscribe loop, transitions, notifications, time-in-state
  actions.go              focus/attach/stop/delete/read/new-session execs
  render_bar.go           bar tree (standard + compact)
  render_panel.go         panel tree
  *_test.go               unit + net.Pipe + lint tests
  testdata/               real snapshot + session list fixtures
cmd/sysc-plugin-herdr/    main.go wiring per cmd/sysc-plugin-cat pattern
manifest.json             id org.sysc.herdr; widget bar; panel 440×560; settings panel
```

Settings: widget — `display_mode` (icon | icon_and_count), `hide_count_when_zero`.
Plugin — `notify_on_blocked` (default true), `notify_on_done` (default false),
`show_stopped_sessions` (default true), `output_preview_lines` (10 | 20 | 40).

Packaging: Makefile `PLUGINS`, README table, `catalog.json` + `catalog-meta.json`,
`ATTRIBUTION.md` note (Hy4ri / noctalia community-plugins, MIT).

## Behavior

### Bar

`[glyph] [count]` where count is blocked+done (the needs-you number, upstream
parity). Glyph tone is the worst status across all sessions; `!`-style error
tone when herdr is missing or no session socket answers. Hidden count when
zero if `hide_count_when_zero`. Below 120 px the row drops to the glyph alone
(with a count suffix only when something needs attention, if it fits).
Tooltip: `N sessions · M agents` plus blocked/done/working counts.

### Panel

```
[glyph] Herdr                  [refresh] [close]
N sessions · M agents
──────────────────────────────────────────────
[dot] default                   3 agents · 1 blocked
     repo-alpha   2 panes · 1 agent          ← workspace header (multi-pane)
       [dot] Fix login flow  tab 2  blocked 2m  [read] [stop…]
       [dot] Run tests       tab 3  working   [read]
     notes         1 pane · 1 agent          ← pane row rendered inline
       [dot] …
[dot] work (stopped)  · attach  · delete
+ new session: [name____] [start]
```

- One card per session; header row clickable → attach.
- Multi-pane workspaces get a header row (label, `N panes · N agents`, status) with indented pane rows; single-pane workspaces are one clickable row (label bold, pane title/tab as detail).
- Pane row: status glyph, title (preference: title > terminal_title_stripped > terminal_title > display_agent > name > agent > basename(cwd) > pane_id), tab label, status label (`state_labels` when present), time-in-state, `read` button, `stop` button on running sessions.
- Blocked/done rows get the attention fill; blocked is error-toned, done accent-toned.
- Output peek expands a mono text block under the row (last `output_preview_lines`, wrapped to the panel's content width, 2000-byte cap).
- Empty state, herdr-missing state, stale-session warning, and action-error card are distinct message cards.
- Delete (stopped, non-default) is two-step: delete → confirm/cancel.

### Sorting

Sessions: default first, then name. Workspaces: priority, then label.
Agents/panes: priority (blocked < done < working < idle < unknown < none),
then `state_change_seq` desc, then pane id.

### Actions

- `attach`: spawn terminal detached: `$TERMINAL -e herdr [--session NAME]`,
  fallback list alacritty/foot/kitty/xfce4-terminal; error card if none.
- `focus`: `agent.focus {target: pane_id}` on the session socket.
- `stop`: `herdr session stop NAME --json` (running sessions).
- `delete`: `herdr session delete NAME --json` (stopped, non-default only).
- `read`: `agent.read` over the socket, cached until collapse or next status change.
- `new`: validate name, spawn terminal `herdr --session NAME`; discovery picks
  it up on the next poll.

### Notifications

Transitions observed after the initial snapshot: working→blocked and (if
enabled) →done. Debounced ~500 ms: a single transition notifies with agent
title, workspace and session; several in one flush collapse to one summary
notification with a count. Never notify on plugin start, on reconnect
re-baseline, or for stale-last-known data.

### Errors

- herdr missing → error-tone bar glyph, message card in panel; no crash.
- Session socket failure → session marked stale, last data kept and labelled,
  reconnect with 1 s → 30 s backoff, fresh snapshot + resubscribe on connect.
- Action failure → bottom action-error card (clears on next success/refresh).
- All execs get timeouts (6 s normal, 20 s stop, 10 s machine-path) and output
  caps (2 MB), following upstream limits; session names validated before use.

## Testing

- Unit: status priority/sort, title fallback, time-in-state formatting, text
  wrap/truncate, session-name validation, session.json and snapshot parsing
  (real fixture in `testdata/`).
- Socket client: `net.Pipe` fake server covering one-shot call, error reply,
  subscribe stream, reconnect.
- Lint: `shelllint.Tree` on the bar at 240×32 and compact widths, and the
  panel at 440×560, driven by the fixture.
- Servicelogic: transition detection and notification coalescing with a fake
  clock/event source.
- Manual end-to-end against a live `herdr --session probe` before merge; the
  probe session is stopped afterwards.