# KDE Connect panel: audit fixes handover

Status handover for `org.sysc.kdeconnect` after Plan D execution and the
visual/functional audit that followed it. All Plan D work is committed on
`main` and deployed to the laptop; the audit surfaced three real bugs and one
styling defect that are **not** fixed yet. Nothing here blocks deploying what
is already on `main`.

## What is done (on main, deployed)

| Commit | Change |
|---|---|
| `d3e65b6` | Pin + manifest protocol minor 6 |
| `1da47d0` | Glyph-catalogue icons (merged feature branch) |
| `87a2a2d` | Tap-to-ping device card (Task 2, routing `device-ping` + `ping`) |
| `4d8b84c` | Recent-images section (Task 4) |
| `2806833` `07c85e7` `3d05d35` | Device mockup artwork + `MockupKind` (Task 3) |
| `c933127` | Name the catalogue glyphs the plan specified (`send`, `folder-open`, `share`) |
| `fcd44de` | Task 5: panel resize with device type (`panelWidth` 400/525, manifest height 760) |
| `36a6d62` | Task 6: `view.focus` the composer's first field on open |
| `ca2d080` | Task 7: animated charging fill in the bar pill + card battery progress (`Animate`, Tone error/accent) |
| `d9f341f` | Mockup card moved below the Actions section |
| `d5ebd7c` | Trim transparent canvas margins off all four mockup PNGs; `mockupSizes` now phone 111×225, tablet 156×213, desktop 210×136, laptop 231×146 |

Gates green on the last batch: `make build vet fmt validate`, all
kdeconnect unit tests. Deployed to the laptop live checkout
(`~/sysc-main-test/sysc-plugins` @ `d5ebd7c`, `make install`, shell restarted).
The laptop runs `main`; no side branches (the earlier cherry-pick onto
`claude/keen-gates-pp8k68` was undone).

## Bugs found by the audit (still open)

### 1. Every action button looks dead: plugin toasts never reach the shell (SHELL bug)

`cmd/sysc-shell/main.go:89` binds plugins with
`shell.PluginHostOptions{Roots, StateDir}` — the `Notify` field
(declared `internal/shell/pluginhost.go:22-26`) is **never set**.
`pluginhost.go:257` wires the nil into the dispatcher env, so
`CallNotify` (`internal/plugin/hostcall.go:166+`) always fails with
"notifications are not available". The kdeconnect plugin discards the error
(`cmd/sysc-plugin-kdeconnect/main.go:371-378`), so ping/ring/share/sms
**execute on the phone** (D-Bus succeeds) but produce zero visible feedback.

Fix shape: implement `PluginHostOptions.Notify` on top of the existing
`notifyclient` (`main.go:184`), e.g.
`SendProducer(protocol.Command{Kind: protocol.CommandProducerPublish, Producer:
&protocol.ProducerRequest{Key, AppName, Summary, Body, Urgency, Value}})`
(module `github.com/Nomadcxx/sysc-notify/protocol`, `types.go:195`).
Ordering caveat: `BindPlugins` runs at line 89, `notifyClient` is created at
line 184 — either reorder, or pass a closure that resolves the client lazily,
or add a setter. Map v1 urgencies to `protocol.Urgency*`; return the reply id
on success so plugin-side suppression keys work.

### 2. Ring / Files / Clipboard buttons are not wired (PLUGIN bug)

`handleInput` (`cmd/sysc-plugin-kdeconnect/main.go:223-330`) has no cases for
`"ring"`, `"browse"`, `"clipboard"` — clicking them does nothing at all, even
after bug 1. The service layer is complete: `performAction` (service.go ~597)
implements `ActionRing` → `findMyPhoneIface.ring`, `ActionBrowse` →
`sftpIface.startBrowsing`, `ActionClipboard` → `clipboardIface.sendClipboard`,
with success/failure toast text, and `service_test.go` already asserts those
events.

Fix shape: three cases mirroring the ping case:
`svc.Do(kdeconnect.Action{Kind: kdeconnect.ActionRing, DeviceID: device})`
(`svc.Do` is fire-and-report, returns false, no tree change). Add a small
`main_test.go` routing check per node.

### 3. Share / SMS composers open below the fold (PLUGIN + shell gap)

`share`/`sms` toggle the composer correctly, but the composer is the panel
tree's **last** child and total content ≈ 917 px > 760 px panel height, so it
appears below the visible scroll area → looks dead. Worse, the shell's
`focusPanelView` (`internal/shell/pluginhost.go:1033-1064`) only moves the
roving-focus index + publishes; it **never scrolls**, so the Task-6
`view.focus` call cannot bring the composer into view either.

Cheapest fix (plugin-side): while a composer is open, insert it directly after
the Actions card instead of at the end, so it is on-screen and focus + scroll
agree. Shell-side scroll-to-focus (scroll node into view on `view.focus`) is
the more general fix but bigger. Alternative: grow the panel height while a
composer is open (`CallPanelResize` is already used by Task 5 and errors are
best-effort).

### 4. Action buttons render as tight "balls" (styling, plugin-side)

`actionButton` sets no `Padding`/`Height`/`Width`; `measureButton`
(`internal/ui/layout.go:235`) sizes to content and `chromeRadius`
(`internal/render/paint.go:1155`) turns any zero-radius box into a stadium, so
icon+text sit against the edge → small tight pills. There is no Grow/Flex in
the wire and `Layout` left-aligns natural widths, so uniform width needs an
explicit `Width`.

Requested outcome: uniform clickable pills, icon + text, roughly double size.
Suggested: `Padding: 10`, `Height: 40` on every action button, plus explicit
`Width` ≈ (panelWidth − 2×list pad 8 − 2×card pad 10 − 2×gap 8) / 3 ≈ 116 at
400 px / 158 at 525 px. The tree builder currently doesn't know the panel
width — either thread it through `PanelTreeForState` (12 call sites, mostly
tests) or hardcode the 400-base width and accept slight raggedness on wide
panels. Update `view_test.go` assertions accordingly.

## Unresolved from the last visual round

User still reported "empty element above the phone image + too much padding
below it" *after* the `d5ebd7c` trim deployed. The panel could not be opened
for a screenshot (see limitations), so this is unverified: it may be the
tap-to-ping card's own `Padding: 14` reading as an empty band (the whole card
is one button), or the Actions card. Needs one screenshot after bug 1–4 land
(and the composer no longer hijacks layout) before touching the card metrics.

## Environment facts / limitations

- Laptop = `ssh -p 7777 nomadx@192.168.0.64`, live plugins checkout
  `~/sysc-main-test/sysc-plugins` (all `~/.config/sysc-shell/plugins` symlinks
  point there), shell = systemd user unit `sysc-shell.service`
  (`systemctl --user restart sysc-shell`). archPC (this box) is **not** the laptop.
- **ydotool clicks do not reach the compositor** (device exists, niri holds
  it, but no panel opens and `pick-window` sees nothing; logical 1536×864
  scale 1.25 on eDP-1). No IPC path opens plugin panels either
  (`knownPanels` in `internal/ipc/server.go:21` is built-ins only). Visual
  verification requires the user to click the bar widget.
- Screenshot pipeline that works:
  `ssh -p 7777 nomadx@192.168.0.64 'XDG_RUNTIME_DIR=/run/user/1000 WAYLAND_DISPLAY=wayland-1 grim /tmp/x.png'`
  → scp → view via cloakbrowser `file://` (test-filesystem read_media_file
  truncates; Gemini vision works via
  `/home/nomadx/.cache/gemini-venv/bin/python .../analyze2.py <img> <prompt>`).
- Laptop bar config temporarily slimmed for testing
  (`world-clock` + `mini-docker` removed from `bar.items.right`; backup at
  `~/.config/sysc-shell/config.json.bak` — restore when done).
- `commit-msg` hook (`core.hooksPath=/home/nomadx/.git-hooks`) rejects
  messages matching ai/bot/agent word list — "bottom" contains "bot"; write
  "lower edge".
- Pre-existing, unrelated to kdeconnect: `TestPluginCalendarGateAllViews`
  fails deterministically since `2f1961f` (calendar header redesign added a
  second header child; `tests/integration/plugin_calendar_gate_test.go:166`
  reads `Children[0].Children[1].Text`). mini-docker race flake and a catalog
  disk-quota issue also predate this work.
- bd tracker lives in the **sysc-shell** repo: sysc-478/468/445/447/430 open
  under epic sysc-420 (plus 469/470 to resolve/defer) — close as these fixes
  land.

## Suggested execution order

1. Shell bug 1 (toasts) — every subsequent test depends on seeing feedback.
2. Plugin bug 2 (three dead cases) — trivial, mirrors ping.
3. Plugin bug 3 (composer position) — plugin-side reorder first.
4. Styling bug 4 (pill sizing) — one commit, tests updated.
5. Gates: `make build vet fmt validate` (repo) + shell tests for bug 1;
   `make install` + `systemctl --user restart sysc-shell` on the laptop;
   then ask the user for one panel screenshot and a real ping to confirm
   end-to-end.
