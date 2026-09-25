# ProtonVPN plugin

Status: approved in brainstorm, 2026-09-26.

## Goal

A ProtonVPN plugin for sysc-shell that matches or exceeds three references:

- Noctalia `riversyx/noctaproton-vpn` v1.0.0 (panel structure, protection
  controls, NAT-PMP, split tunneling).
- DMS `JDKamalakar/DMS-Proton_VPN` v1.0.2 (quick-connect modes, speed monitor,
  settings surface).
- The official GTK app (`ProtonVPN/proton-vpn-gtk-app`, branch `stable`) for
  interaction design: one state-morphing action button, explicit status
  vocabulary, server-load colour thresholds, maintenance dimming, free-tier
  sorting.

Backend is the official `protonvpn` CLI (`connect`, `disconnect`, `status`,
`info`, `config list`, `config set`, `signin`, `signout`). Noctalia runs the
same binary but parses output the official CLI does not print (country/city/IP
lines in `status`, `key: value` lines in `config list`), so its parsers are
not a contract to copy: the formats below are taken from the CLI's own source
(`ProtonVPN/proton-vpn-cli`, `stable`). All CLI interaction is isolated in one
file so an adapter for a future CLI revision touches parsers only.

## Prior-art audit

Taken from Noctalia: the 3-tab panel (connections / protection / account),
kill switch, NetShield 3-way, port forwarding with NAT-PMP renewal, split
tunneling via `~/.config/Proton/VPN/settings.json`, the `.desktop` app scanner,
country aggregation from `serverlist.json`, bar right-click quick connect.

Noctalia's CLI parsers are not copied: they expect `status` fields (`Country`,
`City`, `IP`, `Kill Switch`) and `config list` `key: value` lines that the
official CLI does not emit. The command names and the settings.json
split-tunnel shape are copied; the parsing is written against the real output.

Taken from DMS: quick-connect modes as a setting, protocol awareness, the
speed container, paid-server awareness.

Taken from the GTK app (the part both plugins miss):

- One action button that morphs `Connect` → `Cancel` → `Disconnect` instead of
  separate buttons that can race.
- Status words `Unprotected` / `Connecting…` / `Protected` / `Disconnecting…` /
  `Connection error`, plus a one-line error detail from CLI stderr.
- Server load colour: >90% error tone, >75% accent, else normal.
- `Active port: N` row visible only when connected and port forwarding is on.
- Maintenance rows dimmed with a tooltip; free accounts see free locations
  first.
- The split-tunneling notification "Remember to restart affected apps".

Rejected from the references: DMS's corner animations and glassmorphism (no
animation primitives beyond progress/gauge); the GTK app's tray menu and
browser links ("Upgrade") — sysc has no tray surface and no URL opener.

## Scope

In: bar + tooltip; 460×580 attached panel with 3 tabs; connection card with
live rx/tx; quick connect (fastest / random / P2P / Tor); search + country
list with expandable per-server rows; kill switch; NetShield; port forwarding
with NAT-PMP; split tunneling with app picker; account (sign-in via terminal
handoff, sign-out, account name, protocol, interface); host settings;
connect/disconnect notifications.

Out: pinned servers, custom DNS, early access, WireGuard config import,
in-panel 2FA (terminal handoff instead), an "Upgrade" upsell, protocol
override (the CLI's `--protocol` flag exists but smart default is what both
references ship as default; revisit on request).

## Architecture

One plugin process, which is also the service.

| File | Owns |
|---|---|
| `plugins/protonvpn/manifest.json` | Schema 1, id `org.sysc.protonvpn`, version 1.0.0, protocol minor 8, capabilities `panels, settings, state, notifications`, `requires.commands: ["protonvpn"]`, panel 460×580 attached, `include_settings` false. |
| `plugins/protonvpn/cli.go` | The only file that spawns the CLI: connect (no arg / `--random` / `--p2p` / `--tor` / `--country CC` / server name), disconnect, `status`, `info`, `config list`, `config set …`, `signin`, `signout`. Parsers for the CLI's real formats (status lines, the `Account:` line, the `config list` table), 15s connect timeout, stderr capture, IP from connect stdout. |
| `plugins/protonvpn/state.go` | The state machine (disconnected → connecting → connected → disconnecting, error capture with 20s transition deadline), poll scheduling, traffic sampling from `/sys/class/net/proton0/statistics/`, nmcli link watch, NAT-PMP renewal cadence. |
| `plugins/protonvpn/servers.go` | `~/.cache/Proton/VPN/serverlist.json` parser (~18k LogicalServers), country aggregation (count, average load, feature bitmask SECURE_CORE=1 TOR=2 P2P=4 STREAMING=8, maintenance), per-country server lists, free-tier sort, built-in fallback country list. |
| `plugins/protonvpn/apps.go` | `.desktop` scan across XDG data dirs for split-tunnel candidates: Exec reduction, PATH resolution, exclusion of flatpak/snap runners, shells, dispatchers, setuid binaries. |
| `plugins/protonvpn/natpmp.go` | RFC 6886 client: gateway 10.2.0.1:5351, UDP+TCP mappings, 60s lifetime, 250ms→2s backoff, renewal every 45s. |
| `plugins/protonvpn/bar.go` | Bar pill and tooltip tree. |
| `plugins/protonvpn/panel.go` | Panel skeleton: connection card, tab nav, tab dispatch, keyed patching. |
| `plugins/protonvpn/connections.go` | Quick-connect row, search, country/server list. |
| `plugins/protonvpn/protection.go` | Kill switch, NetShield, port forwarding, split tunneling. |
| `plugins/protonvpn/account.go` | Account card, sign-in/out, info rows, plugin options render. |
| `cmd/sysc-plugin-protonvpn/main.go` | Event loop, settings, host calls, tick scheduling. |

No wire-protocol change beyond the host's current minor (8).

### sysc-shell change (lands first)

The icon catalogue has no shield, key, bolt, dns, location, or flag glyphs.
Extend the material subset in sysc-shell (the repo's own convention, see
`materialfont.go`): append to `ICONS` in `internal/render/icons/material/build.py`,
rebuild `material-symbols-rounded.ttf` from the pinned upstream, mirror the
names in `internal/render/materialfont.go`, extend `materialfont_test.go`,
record the artefact hash in `SOURCE.md`. `public` and `edit` already landed
for the world clock; the names to add (each verified against the pinned
upstream before cutting):

`shield`, `verified_user`, `vpn_key`, `vpn_key_off`, `bolt`, `dns`,
`location_on`, `security`, `flag`, `download`, `upload`, `swap_vert`,
`language`, `gpp_bad`.

Then bump the sysc-plugins pin and `go mod tidy`. The plugin uses the new
glyphs: `vpn_key_off` (unprotected), `bolt` (connecting), `shield` (connected),
`gpp_bad` (error), `dns`/`location_on`/`security` (detail rows), `download`/
`upload` (traffic), `public` (country fallback), `flag` (feature tag).

Repo hygiene rides along in sysc-plugins: the pin (20260925112547) predates
the shell commit that added `public`/`edit`, so world-clock's tests fail at
HEAD, and commit `228113c` dropped sysc-shell's go.sum lines again. Bumping
the pin to the new shell commit and running `go mod tidy` fixes both — no
icon renames needed.

### Flag emoji spike (acceptance gate)

The render pipeline paints CBDT colour emoji (`SplitRuns` groups the two
regional-indicator runes into one run; `blitGlyphBitmap` blits Noto Color
Emoji PNGs untinted). Before the Connections tab is implemented, a spike
shapes `🇺🇸` through `lint`/render with Noto Color Emoji installed. Flags paint
→ country rows lead with a flag glyph; anything degrades → the code-badge
capsule (2-letter, chip fill) is the shipped default. The spike result is
recorded here before country-row work starts.

## Data model and persistence

- Host state key `ui`: `{"tab":"connections"}` — the last open tab, restored
  on panel open. Nothing else is persisted; VPN state lives in the CLI and
  its config files, and the plugin never caches credentials.
- Undecodable state → defaults in memory, no write until a user change.
- Manifest settings (host-managed, edited in the shell settings pane):

| Key | Type | Default | Notes |
|---|---|---|---|
| `refresh_seconds` | int | 5 | 2–60; stable-state status poll. |
| `traffic_monitoring` | bool | true | rx/tx sampling and display. |
| `notify_on_connect` | bool | true | Host notify on connect/disconnect. |
| `bar_mode` | select | `code` | `icon`, `code`, `status`. |
| `quick_connect` | select | `fastest` | `fastest`, `random`, `p2p`, `tor`; bar right-click action. |

## CLI integration

Commands (the official `protonvpn` CLI, verified against
`ProtonVPN/proton-vpn-cli` `stable`): `protonvpn connect` (no arg = fastest;
`--random`, `--p2p`, `--tor`, `--country CC|name`, `--city`, or a server
name), `disconnect`, `status`, `info`, `config list`,
`config set kill-switch standard|off`,
`config set netshield off|malware-only|malware-ads-trackers`,
`config set port-forwarding on|off`, `signin <user>`, `signout`.

Parsing contract, from the CLI's real output:

- `status` (connected): `Status: Connected`, `Server: {name} in {location}`,
  `Load: {N}%`, `Protocol: {protocol}`. Disconnected: `Status: Disconnected`
  alone. Location is `City, Country`, `Country`, or `City, via EntryCountry`
  (Secure Core). There is no IP line; the country code is parsed from the
  server-name prefix (`US-NY#1` → `US`).
- `info`: `Account: '{name}'` only — no plan, no CLI version, no interface.
- `config list`: a tabulate table (`Setting` / `Value` columns, two-space
  separation) with rows `kill-switch`, `netshield`, `port-forwarding`,
  `custom-dns`, `vpn-accelerator`, `moderate-nat`, `ipv6`,
  `anonymous-crash-reports`; free-tier rows read `Upgrade to enable` and map
  to `off`.
- `connect` stdout: `Connected to {name} in {location}.` then `Your new IP
  address is {ip}.` — the only place the IP appears, so it is captured on
  connect and cleared on disconnect.

Parsers are table-driven against fixtures in the CLI's real formats; a live
CLI capture replaces them when one is installed. Unparseable output is an
error, never a zero value.

- Kill switch cannot be changed while connected (the CLI refuses); the toggle
  is disabled while connected.
- Port forwarding toggle is a CLI command; the forwarded port number is not —
  it comes from our NAT-PMP client only.
- Split tunneling is not a CLI feature yet (the CLI README says so): writing
  `~/.config/Proton/VPN/settings.json`
  `features.split_tunneling.{enabled,apps}` matches Noctalia and the GTK app
  and is forward-compatible, but the CLI does not apply it today. Mutually
  exclusive with kill switch; enabling shows the GTK app's "Remember to
  restart affected apps" notification.
- Sign-in needs a TTY (password + 2FA): spawn the user's terminal via
  `xdg-terminal-exec`, falling back to `x-terminal-emulator` then a short
  common list, running `protonvpn signin <user>`; the panel shows a hint line
  while the terminal is out.

## Server list

`~/.cache/Proton/VPN/serverlist.json`, populated by the CLI on first connect.
Aggregation per country: server count, average load, feature bits, maintenance
(a country is under maintenance when all its servers are down; `Status==1` is
only trusted when at least one server anywhere is up). Server rows carry name,
city, load, features, score. The CLI does not expose the plan, so free
locations (tier 0) sort first unconditionally — safe for free accounts, and
paid users can still pick any country. Missing or stale file → a built-in
12-country fallback list with a subtle "Server list unavailable" notice.

## Panel (460 × 580, attached)

Root column, padding 12, gap 8. Budget: 24 padding + 96 connection card + 8 +
36 tab nav + 8 + 404 tab content.

**Connection card** (card fill, radius 10, padding 8, three rows):

1. Status icon (`vpn_key_off` subtle / `bolt` accent / `shield` accent /
   `gpp_bad` error) + status word (bold) + the morphing action button PinEnd:
   `Connect` (accent) → `Cancel` (soft, while connecting) → `Disconnect`
   (error fill) → `Connect`. Disabled while disconnecting.
2. Server name (bold) + country-code badge; city, country subtle. When
   disconnected: "Fastest country" over "Auto-selected on connect".
3. IP · protocol (subtle; IP only when known — it comes from the connect
   output, so a plugin started while already connected shows protocol alone)
   and, when `traffic_monitoring` and connected, the keyed rx/tx line
   (`download`/`upload` icons, tabular text, session totals).

Error detail (first stderr line, error tone, key `err`) renders under the
card when present and clears on the next state change.

**Tab nav**: three equal buttons — Connections / Protection / Account. Active
fill accent, inactive soft.

**Connections tab** (content 404): quick-connect row of four buttons
(Fastest / Random / P2P / Tor — Secure Core is reached through country rows
tagged with the feature); search row (text input, placeholder "Search country
or server", `Reseed`, change + submit, plus a disabled-until-non-empty clear
button); scrolling `list` (~316 tall). Country row: code badge capsule (or
flag per spike), name (bold), `N servers · L%` subtle (`1 server` singular)
with a 48px load progress coloured by the GTK thresholds, chevron expand,
`Connect` button
PinEnd. Rows are rows, never buttons-with-children. Expand inserts server
rows inline (name, city, load, feature tags `lan` P2P / `visibility_off` Tor /
`security` Secure Core / `play_arrow` streaming, Connect). Connected country
row uses the `container` fill; maintenance rows render subtle with tooltip
"{name} is under maintenance" and a disabled Connect. Search filters countries
by name/code and surfaces server-name matches grouped under their country,
auto-expanded.

**Protection tab** (list, 404): kill switch row (label, description, On/Off
button — accent when on); NetShield row with a 3-way segmented control
(Off / Malware / Malware+Ads, active accent); port forwarding row plus, when
on and connected, the `Active port: N` row with a copy button that shells to
`wl-copy`/`xclip` and is hidden when neither exists (host clipboard is
read-only); split tunneling row (disabled with an error-tone warning while
kill switch is on), excluded-app rows (name, path subtle, delete), and an
add-app input with up to 5 suggestion chips from the `.desktop` scan — the
world-clock suggestion pattern.

**Account tab** (list, 404): signed-in card (`person` icon, username bold) or
the sign-in row (username input + Sign in button + terminal handoff hint);
sign-out and refresh buttons; info rows (account name from `info`, protocol
from `status`, tunnel interface from the link watch); an Options section
rendering the current settings values as read-only rows with a pointer to the
shell settings pane.

## Bar and tooltip

Bar 240×32, root row, one button (whole control opens the panel):

- Icon: `vpn_key_off` subtle (disconnected) / `bolt` accent (connecting) /
  `shield` accent (connected) / `gpp_bad` error.
- Text by `bar_mode`: `icon` none; `code` country code uppercase ("…" while
  connecting); `status` the status word.
- Left click opens the panel; right click quick-connects (per `quick_connect`
  setting) when disconnected, disconnects when connected, does nothing while
  transitioning.

Tooltip (280×200, root column): status word, server, location, IP (when
known), rx/tx rates, protocol. Disconnected: "Unprotected" and "Right-click
to quick connect".

## Polling

- Status poll every `refresh_seconds` when stable; every 1s while connecting
  or disconnecting; a transition older than 20s becomes `Connection error`.
- nmcli link watch every 2s while connected (`nmcli -t -f NAME,TYPE,DEVICE,STATE
  connection show --active`, ProtonVPN name / proton0 / wireguard match): a
  vanished tunnel triggers an immediate status refresh. nmcli is optional —
  without it the status poll alone drives the bar.
- Traffic sampled every 1s from `/sys/class/net/proton0/statistics/` when
  connected: rates plus session totals.
- NAT-PMP renewal every 45s while connected with port forwarding on.
- Keyed patches for stable shapes (bar, rx/tx line, load values); snapshots
  on structural change (tab switch, expand, search, state transitions).

## Error handling summary

| Case | Behavior |
|---|---|
| CLI missing at runtime | Bar renders disabled; panel banner "protonvpn CLI not found". |
| Connect timeout (20s) | `Connection error` + stderr detail line. |
| Auth/session errors | stderr mapped to "Authentication denied" / "Session limit reached" style detail. |
| Kill switch changed while connected | Toggle disabled while connected (the CLI refuses the change). |
| Free-tier connect target | CLI stderr ("not available on the free plan") shown as the error detail. |
| serverlist.json missing | Built-in 12-country fallback + subtle notice. |
| settings.json write fails | Error line in the Protection tab; toggle reverts. |
| NAT-PMP failure | Port row shows `—`; retried next cycle. |
| No clipboard tool | Copy button hidden. |
| Terminal spawn fails | Error line with the manual `protonvpn signin` command. |
| Undecodable stored state | Defaults in memory, no write until a user change. |

## Testing

Unit (plugin package): CLI parsers against captured fixtures (status, info,
config list, error stderr); serverlist aggregation against a fixture JSON
(counts, loads, features, maintenance, free-first sort, fallback); `.desktop`
scanner against a fixture tree (exclusions, PATH resolution); NAT-PMP wire
format against golden bytes; state-machine transitions and the 20s deadline;
settings.json read/write round-trip.

Views: `lint.Tree` for the panel at 460×580 across 3 tabs ×
disconnected/connecting/connected/error, plus search, expanded, app-picker,
and maintenance states; the bar at 240×32 in every mode and state; the
tooltip at 280×200.

Loop (cmd package, fake client): tab switch persists; the action button
morphs across states; settings changes re-arm polls; right-click quick
connect fires the right command; notifications fire per `notify_on_connect`.

Acceptance: `make install`, reload the shell, screenshot the panel (all tabs,
connected and not), the bar in each mode, and the tooltip; verify the flag
spike outcome; view the PNGs.
