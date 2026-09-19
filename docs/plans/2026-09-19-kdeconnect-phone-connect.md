# KDE Connect (Phone Connect) DMS Parity Design & Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** A sysc-shell plugin with functional and visual parity with DMS's DankKDEConnect
"Phone Connect" plugin (dms-plugin-registry issue #386, dms-plugins `DankKDEConnect` v3.0.0):
device list over the KDE Connect daemon, battery and charging status, ring, ping, send
clipboard, browse files (SFTP), share (URL/text/file), SMS, pairing flows, and a bar pill.

**Architecture:** Two repos. sysc-shell extends the plugin icon catalogue (the plugin wire
vocabulary has no image kind and the catalogue has no phone or action glyphs). sysc-plugins
adds `plugins/kdeconnect` — a standard v1 plugin process (manifest + `cmd/` entry point +
service + views) whose service speaks DBus to `org.kde.kdeconnect` on the session bus,
modeled on the `internal/services/bluetooth.go` seam style from sysc-shell.

**Tech Stack:** Go 1.26, `plugin/v1` wire protocol (JSONL, minor 2), `github.com/godbus/dbus/v5`
(same pin sysc-shell carries — v5.2.2; recorded here as the reason the pin is deliberate),
fontTools via the existing `internal/render/icons/build.py`.

**Repos:** `~/sysc-shell` (icon catalogue, Tasks S1–S2) and `~/sysc-plugins` (plugin, Tasks P1–P8).

---

## Design summary (locked against DMS DankKDEConnect v3.0.0 and the sysc-shell plugin-host design)

### Identity and manifest

- Plugin id `org.sysc.kdeconnect`, name **Phone Connect** (DMS parity), dir `plugins/kdeconnect/`,
  entry `cmd/sysc-plugin-kdeconnect`. Makefile gains `sysc-plugin-kdeconnect:kdeconnect`.
- `protocol {major:1, minor:2}` — the minor-2 presentation fields (fills, radius, bold, size
  rungs, disabled, center_x/pin_end) landed in sysc-shell `cc684ee`; the repo pin moves to
  `v0.0.0-20260916042624-cc684ee8c131` (that commit) in the plugin work, recorded here per the
  pinning rule. Icon names are strings on the wire, so the new glyphs need no compile-time pin
  — only a shell with the extended catalogue at runtime.
- Capabilities: `notifications` (pairing requests, action feedback, incoming shares),
  `panels`, `settings`, `state`.
- `requires.commands: ["kdeconnect-cli"]` — `kdeconnectd` is not on PATH on most distros
  (`/usr/lib/kdeconnect/kdeconnectd`); the CLI's presence implies the daemon package. Missing
  dependency ⇒ the plugin shows visible-but-stopped, which is the correct UX.
- Panel 400×520, `placement: "attached"` (DMS popout is 400 wide; the shell's attached panel
  is the equivalent surface).
- Settings (host-rendered schema, `visible_when` not needed):
  - `refresh_seconds` float, default 30, min 5, max 300 — daemon refresh period (DMS
    `stateUpdateInterval`).
  - `enable_clipboard_action` bool, default true — DMS `enableClipboardAction`.
  - `show_device_card` bool, default true — DMS `showDevicePlaceholder`.

### DBus surface (what the service speaks)

All on the session bus, service `org.kde.kdeconnect`, daemon path `/modules/kdeconnect`.
This mirrors `DankKDEConnect/services/KDEConnectService.qml` exactly:

| Concern | Path suffix | Interface | Reads / calls |
|---|---|---|---|
| Daemon | `/modules/kdeconnect` | `org.kde.kdeconnect.daemon` | props `announcedName`, `selfId`; `devices(isPaired b, onlyReachable b) → as`; signals `deviceAdded(s)`, `deviceRemoved(s)`, `deviceListChanged()`, `deviceVisibilityChanged(s)`, `pairingRequestsChanged()` |
| Device | `…/devices/<id>` | `org.kde.kdeconnect.device` | props `name`, `type`, `isReachable`, `isPaired`, `isPairRequested`, `isPairRequestedByPeer`, `statusIconName`, `supportedPlugins (as)`, `verificationKey`; calls `requestPairing`, `acceptPairing`, `cancelPairing`, `unpair`; signals `reachableChanged(b)`, `pairStateChanged(b)`, `nameChanged(s)`, `pluginsChanged()`, `statusIconNameChanged(s)` |
| Battery | `…/battery` | `…device.battery` | props `charge i`, `isCharging b`; signal `refreshed(b charging, i charge)` |
| Connectivity | `…/connectivity_report` | `…device.connectivity_report` | props `cellularNetworkType s`, `cellularNetworkStrength i` |
| Ring | `…/findmyphone` | `…device.findmyphone` | `ring()` |
| Ping | `…/ping` | `…device.ping` | `sendPing(s message)` |
| Share | `…/share` | `…device.share` | `shareUrl(s)`, `shareText(s)`, `shareFile(s)`; signal `shareReceived(s url)` |
| Clipboard | `…/clipboard` | `…device.clipboard` | `sendClipboard()` |
| SFTP | `…/sftp` | `…device.sftp` | `startBrowsing()`, `mountPoint() → s`, `mountAndWait() → b` |
| SMS | `…/sms` | `…device.sms` | `sendSms(...)`, `launchApp()` — marshal shapes verified against daemon introspection during P5 and pinned by the fake-bus tests |
| Notifications | `…/notifications` | `…device.notifications` | `activeNotifications()` → count; probe `org.freedesktop.DBus.Introspectable.Introspect` first — a locally disabled plugin stays in `supportedPlugins` but exports no object (upstream #3173) |

Plugin-capability gating uses DMS's rule: a feature's control is `Disabled` unless
`supportedPlugins` contains the name or `kdeconnect_<name>`. Signal subscription is one
`AddMatch` on the daemon service (DMS subscribes service-wide and dispatches on member +
path); battery deltas also arrive via `org.freedesktop.DBus.Properties.PropertiesChanged`.

### Service shape

`plugins/kdeconnect/service.go` runs one goroutine, publishes immutable `Snapshot` values on
`Updates() <-chan Snapshot` (latest-wins), `Reconfigure(Settings)`, `Close()` — the weather
plugin's contract. DBus sits behind narrow seam interfaces (`daemonObject`, `deviceObject`,
`bus`) so table tests run against a deterministic fake, the bluetooth service's pattern.
`Snapshot` carries: `Available`, `BackendName` ("KDE Connect"), `AnnouncedName`,
`Devices` (sorted by name; each: id, name, type, reachable, paired, pair-request flags,
verification key, supported plugins, battery charge/charging, network type/strength,
notification count), and `SelectedID`.

Device selection mirrors DMS `autoSelectBestDevice`: the saved selection if still paired and
reachable, else the first reachable device, else the first device; empty when the daemon has
no devices. The selection persists through `state.get/set` (`selected_device_id`), written on
every change.

### Views (wire minor 2)

Bar (DMS `horizontalBarPill`, the shell has no vertical bar): button `open` → row →
`smartphone` (or `phonelink-off` when unavailable / offline), `N/A` text when the daemon is
absent, `98%` text when battery is known. Charging renders the battery via the panel only;
the bar keeps DMS's icon + percent shape.

Tooltip: device name, battery line, status line — read-only, `ToneSubtle` captions.

Panel 400×520, top to bottom (DMS popout order):

1. Header card (row, `fill: card`, radius 12): device-type icon, column(`title` bold backend
   name, `caption` `N connected • M paired` in accent), `pin_end` icon-only refresh button.
2. State cards: daemon unavailable → error card; daemon up, no devices → subtle empty card.
3. Pairing-request card (when `isPairRequestedByPeer`): `Verification: <key>` + Accept
   (accent) / Reject (error) buttons. Unpaired device → Request pairing button.
4. Device switcher (visible when >1 device): one row per device (`fill: card`): type icon,
   column(bold name, `caption` status), `pin_end` select button — DMS `DeviceCard` with the
   switch button collapsed into the row.
5. Main device card (when `show_device_card`): `fill: card` column, `center_x`: device-type
   icon, `title` bold device name, battery `progress` + charging accent. Tap = ping (DMS
   `PhoneDisplay.onClicked`).
6. Action row: icon buttons ring (`phone-in-talk`), browse (`folder-open`), clipboard
   (`content-paste`), share (`share`), SMS (`sms`), each `Disabled` unless reachable,
   capability present, and (clipboard) enabled in settings.
7. Info rows: battery (icon `battery-N` / `battery-charging-N`, label + `pin_end` value),
   signal strength and network type (`network` icon), notifications count — DMS `InfoRow`
   grid, single column at panel width.
8. Composers (toggled by the share / SMS action buttons, one at a time — DMS
   `ShareDialog` / `SmsDialog` as inline cards): share = URL/text `text_input` +
   send-as-URL + send-as-text buttons + file path `text_input` + send-file;
   SMS = number `text_input`, multiline message `text_input`, send button, launch-app button.

Action feedback and daemon events go through `CallNotify` toasts: ringing, ping sent,
clipboard sent, pairing request sent/accepted, unpaired, file received (`shareReceived`
signal), file sent — DMS's `ToastService` calls.

### Honest visual deltas (wire vocabulary limits, recorded deliberately)

- No `image` kind on the wire: DMS's gradient `PhoneDisplay` phone mockup becomes a card
  with the device-type icon. Functional parity (tap = ping) is kept.
- Icon glyphs are the shell catalogue's to decide (the converter rejects unknown names), so
  Tasks S1–S2 extend it; DMS Material names map to hyphenated catalogue names.
- No per-node color or animation: DMS's charging wave becomes `ToneAccent` + charging glyph;
  tones/fills only, per the wire contract.

### Non-goals (deferred, not dropped)

- **Valent backend.** DMS supports `org.kde.kdeconnect` and Valent; v1 speaks to
  `kdeconnectd` only. Valent's DBus differences are isolated behind the same seams and can
  become a second backend later.
- **MPRIS remote.** The shell's first-party media widget already surfaces the
  `org.mpris.MediaPlayer2.kdeconnect.*` bridge players; a per-device MPRIS section would
  duplicate it. Revisit with a real consumer.
- **Recent images, per-device images, device type overrides** (DMS plugin-data maps): need
  file picking and image nodes the wire does not have.
- **Lock/remote-commands/photo.** DMS exposes them only partially; no parity pressure yet.

---

### Task S1: expose built glyphs in the plugin icon catalogue (sysc-shell)

The font already carries the fifteen battery glyphs and the metric set, but `iconNames`
(interior render/iconfont.go) does not name them, and the converter hard-fails on unknown
names. Map-only change, no font rebuild.

**Files:**
- Modify: `internal/render/iconfont.go` (iconNames)
- Test: `internal/render/iconfont_test.go`

**Step 1: failing test** — catalogue membership for `battery-0`…`battery-6`,
`battery-charging-0`…`battery-charging-6`, `battery-critical`, `network` via `IconByName`.

**Step 2: add the sixteen names to `iconNames`.**

**Step 3:** `gofmt`, `go vet ./...`, `go test -race -count=1 ./...` green.

### Task S2: add the device and communication glyphs (sysc-shell)

Fourteen new SVG glyphs, appended after `uniE02F` (next free codepoints `0xE030`…`0xE03D`),
so every existing codepoint stays stable:

`smartphone, phonelink-off, tablet, laptop, desktop-windows, tv, devices, phone-in-talk,
folder-open, content-paste, share, sms, notifications-active, refresh`

DMS name mapping: `phonelink_off→phonelink-off`, `desktop_windows→desktop-windows`,
`content_paste→content-paste`, `phone_in_talk→phone-in-talk`,
`notifications_active→notifications-active`; type fallback `devices_other→devices`.

**Files:**
- Add: `internal/render/icons/svg/<name>.svg` ×14 — 24×24 viewBox, single filled path,
  sourced from the pinned google/material-design-icons repo (Apache-2.0, matches
  `icons/material/SOURCE.md` provenance).
- Modify: `internal/render/icons/build.py` (GLYPHS), `internal/render/iconfont.go`
  (const block append + iconNames), `internal/render/icons/material/SOURCE.md` is **not**
  touched (that documents the material subset); the sysc font's own byte size changes are
  recorded in the commit message.
- Test: `internal/render/iconfont_test.go`

**Step 1:** failing catalogue tests for the fourteen names.
**Step 2:** SVGs + `GLYPHS` entries + constants + map entries; rebuild
(`python3 internal/render/icons/build.py`); commit the larger `sysc-icons.ttf` deliberately.
**Step 3:** the existing coverage-at-chrome-sizes catalogue test must pass for every name;
`go test -race -count=1 ./...` green; gofmt/vet gates.

### Task P1: plugin skeleton (sysc-plugins)

**Files:**
- Add: `plugins/kdeconnect/manifest.json` (as specified above)
- Add: `plugins/kdeconnect/service.go` (Service contract with an always-unavailable fake
  backend), `plugins/kdeconnect/view.go` (bar/tooltip/panel trees for the unavailable and
  empty states), `plugins/kdeconnect/service_test.go`, `plugins/kdeconnect/view_test.go`
- Add: `cmd/sysc-plugin-kdeconnect/main.go` — `v1.NewClient` → `identity.FromManifest` →
  reader goroutine + select loop (timer's shape: `ViewOpen/ViewClose/ViewResync/InputEvent/
  SettingsChanged/HostShutdown`, service `updates`, stale-data ticker)
- Modify: `Makefile` (PLUGINS), `README.md` (plugin table row)

**Step 1:** view tests calling `v1.Validate(tree, kind)` for every state the skeleton can
produce; service test pinning the unavailable snapshot.
**Step 2:** implementation. **Step 3:** `make build test vet fmt validate` green.

### Task P2: DBus service (sysc-plugins)

**Files:**
- Modify: `plugins/kdeconnect/service.go`, add `plugins/kdeconnect/daemon.go` (seam
  interfaces + godbus implementation + signal routing), `plugins/kdeconnect/service_test.go`
- Modify: `go.mod`/`go.sum` (add `github.com/godbus/dbus/v5 v5.2.2` — direct require of the
  version sysc-shell already pins; recorded per the dependency rule)

**Step 1:** fake-bus table tests: discovery diff (added/removed/reordered), device property
refresh, battery `refreshed` and `PropertiesChanged` handling, availability flicker
(daemon restart clears devices), auto-select ordering (saved reachable > first reachable >
first > none), notification-count probe (introspect-miss ⇒ count stays unknown).
**Step 2:** implementation: connect with `godbus`, `AddMatch` service-wide, member+path
dispatch, `GetAllProps` per device on relevant signals, 1s minimum refresh spacing (DMS
`refreshMinTimer`), settings-driven refresh ticker.
**Step 3:** race tests green; the plugin binary must still come up cleanly with no session
bus (CI) — degraded `Available=false`, not a crash.

### Task P3: panel and bar views (sysc-plugins)

**Files:** `plugins/kdeconnect/view.go`, `view_test.go`, `cmd/sysc-plugin-kdeconnect/main.go`
(action dispatch + publish), `plugins/kdeconnect/main_test.go`

**Step 1:** table tests over `PanelTree`/`BarTree`/`TooltipTree` for: unavailable, empty,
one device, many devices (switcher), pairing-request card, unpaired card, per-action
disabled matrix (capability × reachability × settings), composers closed/open (mutually
exclusive). Every interactive node has id/name/role/events; every tree passes `v1.Validate`.
**Step 2:** implementation per the layout section above; node ids: `open`, `refresh`,
`ring`, `ping`, `browse`, `clipboard`, `share`, `sms`, `pair`, `pair-accept`,
`pair-reject`, `unpair`, `select-<deviceID>`, `share-url`, `share-url-send`,
`share-text-send`, `share-file`, `share-file-send`, `sms-number`, `sms-body`, `sms-send`,
`sms-app`; text inputs reseeded on open.
**Step 3:** publish uses full `Snapshot` on view open and `Patch` (keyed replacements on the
device subtree) for battery/count deltas; update budget respected (60/s burst 120 — batch
per refresh tick, never per property).

### Task P4: pairing flows (sysc-plugins)

**Files:** `plugins/kdeconnect/service.go`, `view.go`, `cmd/sysc-plugin-kdeconnect/main.go`,
tests.

requestPairing / acceptPairing / cancelPairing / unpair behind the seam; pairing-request
detection from `isPairRequestedByPeer` + `verificationKey`; `CallNotify` toast on incoming
requests (DMS `onPairingRequestReceived`), success/failure feedback on every action
result. Tests: state transitions on the fake bus, toast params, stale-request clearing.

### Task P5: share and SMS composers (sysc-plugins)

**Files:** `plugins/kdeconnect/service.go`, `view.go`, `cmd/sysc-plugin-kdeconnect/main.go`,
tests.

shareUrl/shareText/shareFile, `sendClipboard`, sftp `startBrowsing` (+ `mountPoint` read for
the toast), SMS `sendSms`/`launchApp` with marshal shapes pinned against daemon
introspection (record the observed signatures here during implementation).
`shareReceived` signal → `CallNotify` "File received from <device>".
Input validation at the boundary: non-empty URL/text/path, number + body before send;
disabled send buttons otherwise.

### Task P6: settings, state, auto-select polish (sysc-plugins)

**Files:** `plugins/kdeconnect/service.go`, `cmd/sysc-plugin-kdeconnect/main.go`, tests.

`SettingsChanged` handling (refresh ticker rebuild, clipboard toggle), state
`selected_device_id` restore/save, auto-select re-run on `devicesListChanged` (DMS
`onDevicesListChanged`), stale-data ticker refresh, `plugin.status` reporting
(ok / degraded when the bus is down) so the manager shows "daemon unreachable".

### Task P7: integration gate (sysc-plugins)

**Files:** add `tests/integration/plugin_kdeconnect_gate_test.go`, harness reuse from
`harness_test.go`.

Build the binary into a temp plugin dir (Makefile convention), drive it with the scripted
host: handshake, bar + panel open with the fake unavailable service, settings change,
input round-trip, two views served from one process. The DBus seam keeps CI bus-free.

### Task P8: live Niri gate and handover

`make install`, enable `org.sysc.kdeconnect` in `~/.config/sysc-shell/config.json` with a bar
placement, run the shell against the live Niri session
(`NIRI_SOCKET`/`WAYLAND_DISPLAY`/`XDG_RUNTIME_DIR` per the shell guide), `niri msg -j layers`
assertions, a paired-device smoke test (battery, ring toast, clipboard), and the
`YYYY-MM-DD-kdeconnect-completion-handover.md` snapshot: gate output, live observations,
unresolved hardware behavior. bd holds status; the handover holds evidence.

---

## Execution rules

- Docs landed on `sysc-plugins` main before any code; work happens in
  `.worktrees/feat/kdeconnect` (sysc-plugins) and `.worktrees/feature/kdeconnect-icons`
  (sysc-shell).
- bd (sysc-shell checkout) is the only tracker; claim/close each task there.
- Code commits pass `gofmt`, `go vet ./...`, `go test -race -count=1 ./...`;
  sysc-shell commits additionally keep `git diff --exit-code -- go.mod go.sum`.
- godbus pin v5.2.2 is deliberate: identical to sysc-shell's pin, so the two modules share
  one dbus version and no transitive drift is possible.
