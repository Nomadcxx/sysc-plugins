# herdr plugin — implementation handover

You are implementing a new sysc-shell plugin, `org.sysc.herdr`, a port of the Noctalia community
plugin **herdr** by Hy4ri (MIT) with an event-driven core and extra features. The design is approved
and the implementation plan is written. Your job is to execute the plan task by task.

## Read these first (absolute paths)

1. **Design spec (source of truth for behavior):**
   `/home/nomadx/sysc-plugins/.worktrees/herdr/docs/superpowers/specs/2026-10-11-herdr-design.md`
2. **Implementation plan (execute this):**
   `/home/nomadx/sysc-plugins/.worktrees/herdr/docs/superpowers/plans/2026-10-11-herdr.md`
3. Upstream reference (already downloaded):
   `/tmp/herdr-upstream/` (`bar.luau`, `panel.luau`, `panel_view.luau`, `service.luau`, `plugin.toml`)
4. Repo UI rules: `<worktree>/docs/plugin-ui-rules.md`; writing plugins: `<worktree>/docs/writing-plugins.md`.

The plan begins with `> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
… or superpowers:executing-plans … to implement this plan task-by-task.` Follow that instruction
(load the skill before starting). Use checkbox syntax as you go.

## Where the work lives

- **Worktree:** `/home/nomadx/sysc-plugins/.worktrees/herdr` — work ONLY here.
- **Branch:** `feat/herdr-plugin`, based on `origin/main` (`bfa58e2`).
- **Do not touch the primary checkout** `/home/nomadx/sysc-plugins` — it holds the user's unrelated
  `fix/kdeconnect` working tree. The primary local `main` is stale; origin/main is the truth.
- Commits so far: `92be7fe` design spec, `1e1e7f6` implementation plan.
- **bd issues** (epic `sysc-plugins-3`): tasks `sysc-plugins-4` … `sysc-plugins-11`, mapped 1:1 to
  plan Tasks 1–8. Mark `in_progress` when starting a task, `closed` when its acceptance is met, and
  commit `.beads/issues.jsonl` with the work. `bd` runs fine from this worktree.

## Environment facts

- Go 1.26.4, module `github.com/Nomadcxx/sysc-plugins`, dep `github.com/Nomadcxx/sysc-shell
  v0.0.0-20261005234519-d16632ff376f` (in module cache; `plugin/v1` + `plugin/lint`).
- **Cap all Go commands**: `go test -count=1 -p 2 <one package>`; never `go test ./...` or
  `go vet ./...` uncapped — the machine has locked up twice.
- Commit messages must contain **no** AI/assistant attribution (commit-msg hook rejects). bd
  pre-commit hook works in this worktree (commits above prove it).
- `gofmt -l .` must print nothing before each commit.
- herdr v0.9.1 is installed (`~/.local/bin/herdr`). A **live probe session `probe` is still running**
  with socket `/home/nomadx/.config/herdr/sessions/probe/herdr.sock` (one workspace `w1`, pane
  `w1:p1`, agent `claude`). Use it for tests, and at the very end: `herdr session stop probe` plus
  kill the leftover pty process (see `/tmp/herdr-probe.log`). Useful: `herdr session list --json`.
- Full herdr API JSON schema: `/tmp/herdr-schema.json`. Real captured snapshot:
  `/tmp/fixture-snapshot.json` (Task 1 sanitizes it into `plugins/herdr/testdata/`).
- Terminals available for attach: `$TERMINAL` is `ghostty`; `alacritty`, `foot`, `kitty`,
  `xfce4-terminal` installed; no `xdg-terminal-exec`.

## herdr socket protocol — critical facts (verified live)

- Unix socket, **newline-delimited JSON**. Request `{"id","method","params"}`; reply
  `{"id","result"}` or `{"id","error":{"code","message"}}`.
- **Call connections are one-shot**: dial → write one line → read one line → close.
- **A subscribed connection accepts exactly ONE `events.subscribe` request.** Any second request on
  that connection makes the server reset it. All subscriptions — global types plus one
  `pane.agent_status_changed` per known pane — go in that single request.
- Events on the subscribed connection arrive unsolicited as `{"data":{...},"event":"<type>"}`.
- Global event types (no pane_id needed): `pane.created/updated/focused/closed/exited/moved`,
  `tab.*`, `workspace.*` (created/updated/metadata_updated/renamed/moved/reordered/closed/focused),
  `worktree.*`, `layout.updated`, `pane.agent_detected`, `pane.scroll_changed`.
- `pane.agent_status_changed` **requires `pane_id`** and (optionally) `agent_status` filter. Event
  data: `{"agent","agent_status","pane_id","workspace_id"}` — no sequence number; use local time.
- Pane set changes ⇒ reconnect with fresh snapshot + new single subscribe (re-baseline silently;
  never notify on the first snapshot after (re)connect).
- Methods used: `session.snapshot` (call, `{}`), `agent.focus` (`{"target": paneID}`),
  `agent.read` (`{"target","source":"recent","lines","strip_ansi":true}` →
  `{"result":{"text","truncated"}}`), `pane.report_agent` (test fixture only; cannot set `done`).
- CLI used: `herdr session list --json`, `herdr session stop NAME --json`,
  `herdr session delete NAME --json` (never delete `default`), 6s/20s timeouts, 2 MiB output caps.
- Snapshot schema v0.9.1 (`session.snapshot`): `{version, protocol, focused_*, workspaces[], tabs[],
  panes[], layouts[], agents[]}`. AgentStatus enum: `idle|working|blocked|done|unknown`.
  `AgentInfo` has `state_change_seq` (ordering only, no timestamp), `state_labels` (map status →
  custom label), `title`, `terminal_title_stripped`, `display_agent`, `name`, `cwd`,
  `foreground_cwd`. `WorkspaceInfo.worktree.repo_name` gives repo display names (no branch).
  `PaneInfo` has `title`, `label`, `agent_status`, `terminal_title(_stripped)`. Session names match
  `^[A-Za-z0-9._-]{1,64}$`, not `.`/`..`. Stopped sessions: `session.json` in the session dir
  (`workspaces[].custom_name` else `basename(identity_cwd)`, home → `~`).

## sysc-shell SDK facts you will need

- `plugin/v1`: `NewClient`, `Handshake(identity.FromManifest(...))`, `Recv`, `Send`, `Snapshot(viewID,
  rev, root)`, `Call(ctx, CallKind, params)`. Node kinds/fields and validation limits are in the
  plan; `PluginStatus{State: error}` for fatal errors only.
- Repo patterns to copy: `cmd/sysc-plugin-cat/main.go` (event loop, views map, input dispatch,
  lockedWriter), `plugins/cat/view_test.go` (shelllint test idiom + `internal/barwidth` compact
  breakpoint at <120px), `plugins/aiusage` (settings panel head with back/close buttons, per-instance
  widget settings, `capture.Panel` test idiom).
- Icons resolve against the project dash-name set **and** the Material underscore subset. Use:
  `ai-usage` (brand/bar/panel header), statuses `notifications`/`check`/`play_arrow`/`pause`/
  `do_not_disturb_on`/`terminal`, session `dns`, workspace `folder-open`, actions `terminal`, `desktop_windows`,
  `visibility`, `delete`, `stop`, `refresh`, `close`, `add`, `send`. Anything else fails validation.
- Tones only: `blocked→error`, `done→accent`, `working→normal`, idle/unknown→`subtle`. No colors.
- Every merged plugin must ship `screenshot.png` + `thumbnail.webp` (validate-manifests enforces);
  capture via `capture.Panel` + `make capture PLUGIN=herdr` (fictional data only — public repo).
- Package layout, Makefile `PLUGINS` list, `catalog-meta.json`, `ATTRIBUTION.md` row: plan Task 7.

## Execution notes

- Tasks 1–8 in the plan are the commit boundaries; each task lists files, exact interfaces, test
  names, commands, and acceptance. Write tests first where the plan says so.
- Keep all I/O behind seams (`CallOn`, function fields on `Service`/`Actions`) so tests never touch
  real processes or sockets; the live probe session is for the manual e2e in Task 8.
- After each task: tests pass (`go test -count=1 -p 2 ./plugins/herdr`), `gofmt -l .` clean, `bd`
  issue closed, commit with the message given in the plan, include `.beads/issues.jsonl`.
- Task 8 is the definition of done: build, link into the shell, exercise against the live probe
  session (bar count/tone, panel rows, peek, focus, stop/delete, blocked notification, herdr-missing
  state), then stop the probe session and clean up.
- If you run low on context, commit what passes, update the bd issue status, and write a short
  continuation note at the top of this file before handing off.