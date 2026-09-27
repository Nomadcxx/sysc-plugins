# ProtonVPN plugin — session handover

Status: **all 16 tasks shipped** (see "Acceptance record" at the bottom).
The sections below preserve the handover context as written when Tasks 1–11
had just landed; the "What remains" section is now historical.

## Where the work lives

| Repo | Worktree | Branch | HEAD | State |
|---|---|---|---|---|
| sysc-plugins | `/home/nomadx/sysc-plugins/.worktrees/feat/protonvpn` | `feat/protonvpn` | `4953933` | clean |
| sysc-shell | `/home/nomadx/sysc-shell/.worktrees/feat/protonvpn-glyphs` | `feat/protonvpn-glyphs` | `85606e0` | clean, pushed to origin |

- Base commits: sysc-plugins main `6c467a1`; sysc-shell main `4d4aa9e`.
- Plan: `docs/plans/2026-09-26-protonvpn.md` (16 TDD tasks, audited and
  corrected in main-repo commit `3f7ea8b`).
- Design: `docs/plans/2026-09-26-protonvpn-design.md` (approved; flag-emoji
  spike outcome recorded in `a10970d`).
- All work happens in the sysc-plugins worktree above. Do not work on `main`.

## Process rules (user instruction)

The user has instructed: skip the per-task spec/QA subagent reviews. Implement
each task with TDD, verify the tests yourself, commit, and move on. When the
plugin is ready, generate a `.md` asking another agent to review the work
(this replaces the per-task review gates). Verbatim:

> "Ok lets avoid the QA and spec please generate a .md seeking another agent
> review your work the continue on implementing / session continuation."

Commit messages must carry no AI attribution (a repo hook rejects it).

## What is done (Tasks 1–11)

| Task | Commit(s) | What landed |
|---|---|---|
| 1. Shell glyphs | sysc-shell `85606e0` (pushed) | 14 VPN glyphs (`shield verified_user vpn_key vpn_key_off bolt dns location_on security flag download upload swap_vert language gpp_bad`) added to the material subset; SOURCE.md now 107 names / 138 glyphs / 32,740 bytes / SHA-256 `5f23319b458af6be3b087cdb0c0679f5e12da1c54d446d71c902e6bd7cb6320e`; `location_on`→`place` alias documented (upstream renamed the glyph, ligature retained). |
| 2. Pin bump | `b6b39b9` | `go.mod` pins `github.com/Nomadcxx/sysc-shell v0.0.0-20260926114359-85606e0ce8f2`; go.sum updated; build/tests/validate green. |
| 3. Flag spike | `a10970d` | Spike PASS: Noto Color Emoji paints the US flag through the runtime render path (`Mask.Color` non-nil). **Country rows lead with a flag glyph**; the code-badge capsule is only the fallback. |
| 4. Manifest + skeleton | `3fb6179` | `plugins/protonvpn/manifest.json` (id `org.sysc.protonvpn`, protocol 1.8, exec `bin/sysc-plugin-protonvpn`, 5 labelled settings), `cmd/sysc-plugin-protonvpn/main.go` skeleton (world-clock structure), Makefile `PLUGINS` entry, README row. |
| 5. CLI parsers | `3f93bad`, `5644f33` | `cli.go`: `Phase`, `Status{Phase,Server,Location,Country,Load,Protocol}`, `Info{Username}`, `Config{KillSwitch,NetShield,PortForwarding}`, `CLI` with `Run/Status/Info/Config/Connect/Disconnect`, parsers `ParseStatus/ParseInfo/ParseConfig/ParseConnectIP/ErrorDetail`, `connectArgs`, `wrapErr` (first stderr line). 6 fixtures in `testdata/`. |
| 6. State machine | `c48876e`, `eeadff5`, `e8544f9` | `state.go`: `Snapshot` (incl. `IP`, `Interface`, rates/totals, `Port`), `Machine` with 20s transition deadline, `SetStatus` clears stale `Err`/`IP` on phase change, traffic sampler with vanish + counter-reset guards. |
| 7. Server list | `9725a17`, `b7f37bf` | `servers.go`: feature bitmask, `Server`/`Country`, `LoadServers` (wrapper + bare array; status_known guard: all-zero `Status` means unknown, not down), `Aggregate` (mean load, maintenance, feature union, stable sorts, free-tier first), `FallbackCountries` (12). Fixture 5 servers / 3 countries. |
| 8. Bar + tooltip | `f29501e`, `beda889`, `4f3dc95` | `bar.go`: `BarState`, `Bar` (one button ID `bar`, icon per phase, text per `bar_mode`), `Tooltip` (status, server, location, IP, rates, protocol; disconnected hint), `formatRate` (`1.2 MB/s`, `340 KB/s`, `12 B/s`). |
| 9. Panel skeleton | `9347b2f` | `panel.go`: `PanelState`, `Panel` (root column padding 12 gap 8; CLI banner; connection card with status icon/word, server + location + country chip, IP·protocol, keyed `traffic` row, morphing `action` button; keyed `err` line; 3-button tab nav; tab dispatch). |
| 10. Connections tab | `c47ad89` | `connections.go`: `ConnectionsState`, `ConnectionsTree` (quick-connect row `qc:*`, search row `search`/`clear-search`, notice, country rows `country:<CC>` with flag/code badge, load progress `load:<CC>`, expand `expand:<CC>`, connect `connect:<CC>`; expanded server rows `server:<name>`/`server-connect:<name>`; maintenance dimming; search filtering + auto-expand). `panel.go` `tabBody` dispatches connections. |
| 11. Protection tab | `4953933` | `protection.go`: `ProtectionState`, `ProtectionTree` (kill switch `ks` locked while connected, NetShield `ns:*`, port forwarding `pf` + `Active port: N` + `copy-port` + `Negotiating port…`, split tunneling `st` + `del-app:*` + `app-query` + `app-suggest:*`, foot `Err`). `splittunnel.go`: `ReadSplitTunnel`/`WriteSplitTunnel` preserving unknown keys, failing loud on malformed files. |

## What remains (historical — shipped in Tasks 12–16, see acceptance record)

### Task 12 — Account tab (`account.go`)

Files: create `plugins/protonvpn/account.go`, `account_test.go`.

```go
type AccountState struct {
	Snap       Snapshot
	SignedIn   bool
	UserDraft  string
	UserReseed uint64
	Err        string
}

func AccountTree(s AccountState) *v1.Node
```

Tests (use the package helpers `findNode`, `lookupNode`, `assertTextContains`,
`assertTextLacks`; use `v1.ViewPanel`, not the string `"panel"`):

- `TestAccountSignedIn`: `SignedIn: true`, `Info{Username: "jane@example.com"}`,
  `Status{Protocol: "wireguard"}`, `Interface: "proton0"` → text contains all
  three; nodes `signout` and `refresh` exist.
- `TestAccountSignedOut`: `UserReseed: 1` → nodes `signin-user`, `signin`;
  text `Complete sign-in in the terminal (password + 2FA)`.
- `TestAccountOptionsSection`: text `Options` and `Change in shell settings`.
- `TestAccountLint`: signedIn × {PhaseDisconnected, PhaseConnected, PhaseError}
  at `lint.Tree(root, v1.ViewPanel, 460, 404)` → no findings.

Implementation: `KindList` Height 404 Gap 4.

- Signed-in card: row Fill `card` Radius 10 Padding 8 — `person` icon,
  username bold.
- Buttons row: `signout` (`Sign out`, Fill soft) + `refresh` (icon
  `restart_alt` — the `refresh` glyph does not exist in the shell subset;
  node ID stays `refresh`, 40×40).
- Info rows (label subtle + value tabular PinEnd): `Account`
  (`Info.Username`), `Protocol` (`Status.Protocol`), `Interface`
  (`Snapshot.Interface`; `—` when empty).
- Signed-out instead: hint card (`Complete sign-in in the terminal
  (password + 2FA)`, subtle), sign-in row (text_input `signin-user`,
  Placeholder `Username`, Reseed `UserReseed`, change+submit; button `signin`
  `Sign in`, Fill accent, Disabled when `UserDraft` empty), terminal-handoff
  hint line.
- Options section: title `Options` + read-only rows for the five settings
  values + subtle `Change in shell settings`. **Spec gap:** `AccountState`
  carries no settings values — add a minimal `Settings map[string]string`
  (manifest order: `refresh_seconds`, `traffic_monitoring`,
  `notify_on_connect`, `bar_mode`, `quick_connect`; missing → `—`) and
  disclose the deviation.
- `Err` as an error-tone line at the foot.

Commit: `feat(protonvpn): account tab` (only the two files).

### Task 13 — App scanner (`apps.go`)

`ScanApps(dataDirs []string, pathEnv []string) []App`; fixtures under
`testdata/applications/*.desktop`; tests create executables in
`t.TempDir()/bin` (setuid via `os.Chmod(..., 0o4755)`); port of Noctalia's
`apps.py` (skip NoDisplay/Hidden/non-Application, strip field codes and
`VAR=value`/`env`, exclude flatpak/snap/flatpak-spawn, shells
sh/bash/zsh/fish/dash/ksh/tcsh/env/gtk-launch/xdg-open, dispatchers
hyprctl/uwsm/xdg-terminal-exec/dbus-launch, `omarchy-launch-`/
`omarchy-webapp-handler-` prefixes, setuid/setgid; dedupe by path keeping the
shortest label; sort by label then path). Commit:
`feat(protonvpn): split-tunnel app scanner`.

### Task 14 — NAT-PMP client (`natpmp.go`)

`NATPMP{Gateway default "10.2.0.1:5351", Now}`; `RequestPort(ctx) (int, error)`;
`BuildMapRequest(op uint8, internalPort, externalPort, lifetime uint32) []byte`;
`ParseMapResponse(b []byte) (port int, epoch uint32, err error)`. Golden bytes:
`BuildMapRequest(2,0,0,60)` == `{0,2,0,0,0,0,0,0,0,0,0,60}`;
`ParseMapResponse({0,2,0,0,0,0,0,100,0x14,0x3C,0x14,0x3C,0,0,0,60})` → port
5180, epoch 100; `{0,2,0,6}` errors; failure against `127.0.0.1:1` returns an
error without hanging. Retry 250ms→2s backoff; renewal every 45s (loop-side).
Commit: `feat(protonvpn): NAT-PMP client`.

### Task 15 — Event loop (`cmd/sysc-plugin-protonvpn/main.go`)

Session additions: machine, cli, countries, apps, tab, expanded, query,
appQuery, queryReseed, appReseed, hasCLI, hasCopyTool, flags. Wire
`panel.go` `tabBody` to `ProtectionTree`/`AccountTree` (connections is already
wired). Tests use the world-clock pipe harness (`io.Pipe`, `v1.HostCall`/
`v1.HostReply`). Input dispatch by node ID: `bar` (activate → open panel;
`v1.ButtonSecondary` → quick connect/disconnect per phase + `quick_connect`
setting), `action`, `qc:*`, `search`/`clear-search`, `expand:<CC>`,
`connect:<CC>` → `Connect --country CC`, `server-connect:<name>` →
`Connect <name>`, `ks`, `ns:*`, `pf`, `copy-port` (wl-copy/xclip), `st`,
`del-app:<path>`, `app-query`/`app-suggest:<path>`, `signin`/`signin-user`,
`signout`, `refresh`, `tab:*`. Polling: status every `refresh_seconds` (1s
while transitioning; `TransitionExpired` → `Fail("Timeout")`), nmcli link
watch every 2s while connected (`nmcli -t -f NAME,TYPE,DEVICE,STATE connection
show --active`), traffic 1s from `/sys/class/net/proton0/statistics/`,
NAT-PMP renewal 45s while connected + port forwarding on. On connect success
`ParseConnectIP(stdout)` → `machine.SetIP`; on disconnect `SetIP("")`.
Sign-in handoff: `xdg-terminal-exec` → `x-terminal-emulator` → kitty/alacritty/
foot/gnome-terminal/konsole/xterm running `protonvpn signin <user>`; spawn
failure sets `Err` with the manual command. Notifications: connected
`Connected to <server>`, disconnected `Disconnected`, split-tunnel enable
`Split tunneling enabled. Remember to restart affected apps.`. Keyed patches
for stable shapes (traffic, err, bar); snapshots on structural change.
Commit: `feat(protonvpn): event loop, polling, and notifications`.

### Task 16 — Sweep, docs, acceptance

`make build && make test && make validate`; lint sweep; live acceptance
(`make install`, reload the shell, 7 numbered checks: bar, panel, connections,
protection, account, screenshots, kill the tunnel externally); handover doc
(this file); commit `docs(protonvpn): handover and acceptance record`.

### Review-request document

Per the user's instruction, when the plugin is ready generate a `.md` asking
another agent to review the work (replacing the per-task spec/QA reviews).
Suggested location: `docs/plans/2026-09-26-protonvpn-review-request.md`.
Include: what was built, the commit range, how to verify (commands), the
known risks below, and the files most worth scrutiny (`cli.go` parsers,
`state.go` deadline/traffic, `splittunnel.go` round-trip, `natpmp.go` wire
format, `main.go` dispatch).

## Key facts a continuation needs

### Official CLI surface (verified from `ProtonVPN/proton-vpn-cli` `stable`)

- `status` (connected): `Status: Connected` / `Server: {name} in {location}` /
  `Load: N%` / `Protocol: wireguard`; disconnected: `Status: Disconnected`
  only. No country/city/IP lines.
- `info`: `Account: '{name}'` only. No plan, no CLI version, no interface.
- `config list`: a tabulate table (`Setting`/`Value` columns, two-space
  separation) with rows `netshield`, `kill-switch`, `port-forwarding`,
  `custom-dns`, `vpn-accelerator`, `moderate-nat`, `ipv6`,
  `anonymous-crash-reports`; free-tier rows show `Upgrade to enable`.
- `connect` stdout: `Connected to {name} in {location}.` then
  `Your new IP address is {ip}.` (the only source of the IP).
- `connect` flags: `--country` (code or full name), `--city`, `--p2p`,
  `-sc/--securecore`, `--tor`, `--random`, positional server name. Free tier
  rejects all targets.
- Kill switch cannot be changed while connected (CLI refuses) — the UI locks
  the toggle.
- Split tunneling is **not** a CLI feature yet; the `settings.json` write is
  forward-compatible Noctalia/GTK parity and currently inert.
- The CLI is not installed on this machine, so fixtures are the verified
  formats, not live captures. Task 5 Step 1 re-captures if it ever is.

### Host manifest schema

`exec` must be `bin/<binary>` (a regular executable inside the plugin dir);
every setting needs a `label`; select `options` are `{"value","label"}`
objects. `tools/validate-manifests` is laxer than the host's
`internal/plugin/manifest.go` — the host is the arbiter.

### Render API (sysc-shell)

`NewTextRenderer(face)`, `NewTextRendererWithFontMap(*FontMap)`,
`NewSystemFontMap(family, cacheDir)`, `Raster(text, TextSpec, tabular)
(Mask, error)`; `Mask.Color` non-nil proves a CBDT bitmap blit. No `Paint`
method. `v1.Node` fields used: `Kind`, `ID`, `Key`, `Text`, `Icon`, `Tone`,
`Fill`, `Size`, `Bold`, `Disabled`, `Selected`, `PinEnd`, `Tabular`,
`Tooltip`, `Shape`, `Width`, `Height`, `MaxWidth`, `Padding`, `Gap`, `Name`,
`Role`, `Events`, `Children`, `Radius`, `Value`, `Reseed`, `Placeholder`.
Tones: `ToneNormal`/`ToneError`/`ToneSubtle`/`ToneAccent`. Fills: `surface`,
`accent`, `container`, `error`, `soft`, `card`, `outline`, `chip`. Pointer
buttons: `v1.ButtonPrimary`/`ButtonMiddle`/`ButtonSecondary`. `lint.Tree(root,
view, width, height)`; `lint.BarWidth` 240, `lint.BarHeight` 32,
`lint.TooltipWidth` 280, `lint.TooltipHeight` 200.

### Commands

- Package tests: `go test ./plugins/protonvpn/ -v` (from the worktree).
- Full module: `GOTMPDIR=/home/nomadx/.cache/go-tmp go build ./...` — plain
  `go build ./...` can hit a `/tmp` disk-quota error on `tools/catalog`.
- `make validate` (13 manifests), `make build`, `make install`.
- Pre-existing unrelated failure: `tests/integration`
  `TestPluginCalendarGateAllViews` (calendar bar view does not fit 240×32).
  Reproduced at base `6c467a1`; ignore it.

### Known concerns carried forward

- Panel column is ~16px over its 556 budget when `HasCLI: true` (banner 16 +
  card 96 + nav 36 + tree 400 + gaps); column overflow is silent in lint and
  clips the list bottom. Task 15/16 should reconcile the budget.
- `Panel` passes no `Countries` yet — the Connections tab shows an empty list
  until Task 15 feeds `ConnectionsState.Countries`.
- Split tunneling is inert until the CLI supports it.
- `BarState.Quick` is declared but unused (spec-mandated interface).
- `formatRate(9999)` renders `10.0 KB/s` while `10000` renders `10 KB/s`
  (cosmetic, pinned by test).
- `servers.go` `Score` and `FeatSecureCore`/`FeatStreaming` are currently
  unused (spec-mandated for Task 10; Task 10 did not consume them).
- `protection.go` renders the split-tunnel editor even when the kill switch
  blocks it (required by the plan's test 6, which uses a zero-value Config).

## How to continue (superseded — see acceptance record)

1. `cd /home/nomadx/sysc-plugins/.worktrees/feat/protonvpn` (branch
   `feat/protonvpn`, HEAD `4953933`).
2. Read the task text from `docs/plans/2026-09-26-protonvpn.md` (Tasks 12–16).
3. Implement with TDD: tests first, RED, implement, GREEN, `go vet`,
   `gofmt -l`, commit with the plan's message and no AI attribution.
4. No spec/QA subagent reviews — verify the tests yourself.
5. After Task 16, generate the review-request `.md` and hand the branch over.

## Acceptance record (2026-09-27, Tasks 12–16)

Worktree `/home/nomadx/sysc-plugins/.worktrees/feat/protonvpn`, branch
`feat/protonvpn`, range `6c467a1..c2371f5` (22 commits).

| Task | Commit | What landed |
|---|---|---|
| 12. Account tab | `246f709` | `account.go`: `AccountState` (+ `Settings map[string]string`, a spec gap: Options rows render real values, `—` when missing), signed-in card/buttons/info rows, signed-out hint + username field + terminal-handoff line, error foot. `signin-user` width 320 to fit the embedded 420-wide card. |
| 13. App scanner | `a6b1be2` | `apps.go`: `ScanApps` port of Noctalia `apps.py` (Exec field codes + `VAR=`/`env` stripping, PATH resolution, NoDisplay/Hidden/non-Application skips, runner/shell/dispatcher/setuid exclusions, shortest-label dedupe, label-then-path sort). Fixtures in `testdata/applications/`. Tests run executables in an in-package temp dir because `/tmp` is mounted `nosuid`. |
| 14. NAT-PMP | `15755d8` | `natpmp.go`: `BuildMapRequest`/`ParseMapResponse` golden-byte wire format, `RequestPort` UDP+TCP lifetime 60, per-attempt 1s read timeout, 250ms→2s retry until ctx end; refusals/socket errors fail fast (non-fatal to the plugin). Tests include a fake-gateway round trip. |
| 15. Event loop | `dcab335` | `main.go`: startup probes (CLI/clipboard lookPath), state-restored tab, initial status/info/config polls, serverlist `LoadServers`→`Aggregate` with `FallbackCountries` + notice, app scan, 1s tick driving status poll (`refresh_seconds`, 1s while transitioning, 20s `Timeout` fail), nmcli link watch (2s), traffic sampler (1s, `/sys/class/net/proton0`), NAT-PMP renewal (45s), phase notifications, full node-ID input dispatch, terminal sign-in handoff. `panel.go` `tabBody` wired to all three trees. Pipe-harness tests (`main_test.go`, fake sh CLI — builtins only because the harness empties `PATH`): tab persistence, action→connect wiring + IP, right-click quick connect `connect --p2p`, settings re-arm (`bar_mode`), connect notification, split-tunnel enable writes settings.json + notifies. |
| 16. Sweep + docs | `c2371f5` + this | Panel column overflow reconciled: `tabBody` caps the tab list to the panel's remaining height (lint is silent on column overflow; 404 only fit the no-banner no-error case). Lint sweep extended: panel 460×580 × 3 tabs × 5 phases × {plain, search+notice, expanded, maintenance, app-picker, error line} × hasCLI both; tooltip now every phase. |

### Verified

- `go test -count=1 ./plugins/protonvpn/ ./cmd/sysc-plugin-protonvpn/` — ok,
  ok (83 tests incl. the 6 required harness tests and the full lint sweep).
- `GOTMPDIR=... make build` — all plugins build; `make validate` — 13/13
  manifests ok.
- `make test` — only failures: pre-existing
  `TestPluginCalendarGateAllViews` (reproduced at base `6c467a1`) and a
  `TestPluginMiniDockerGate` timing flake that passes on re-run
  (`go test -count=1 ./tests/integration/` → calendar only).

### Deferred (cannot run here)

The `protonvpn` CLI is not installed, so the plan's 7 live acceptance checks
(`make install`, reload, bar states, panel tabs, protection toggles against
the real CLI, account sign-in handoff, external tunnel kill) are **not
verified**. Fixtures were built from the verified official-CLI output formats;
first live contact should re-check `status`/`config list` parsing.

### Deviations disclosed

1. `copy-port` uses the host's native `clipboard.write` call, not
   `wl-copy`/`xclip` shellout; the copy-tool probe remains only to gate the
   button as the spec requires.
2. Updates use whole-view snapshots on change instead of keyed patches
   (stable-shape patching was not needed at this view size; keys remain on
   `traffic`/`err` nodes).
3. `AccountState.Settings` added (spec gap: Options section had no data).
4. Harness seeds `settings.json` because `WriteSplitTunnel` deliberately
   refuses to clobber missing/malformed files.
