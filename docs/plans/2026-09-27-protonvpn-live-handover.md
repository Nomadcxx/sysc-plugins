# ProtonVPN plugin — live-testing handover

Status: review fixes merged to `main`; plugin deployed on the test laptop;
live connect blocked by a ProtonVPN daemon failure. This document hands the
remaining work to the next session.

## What landed (all on `main`)

- `e9812b6` (rebased into the merge) — review fixes:
  - `settingsPath` corrected to `~/.config/Proton/VPN/settings.json` (was `~/.config/protonvpn/`).
  - Split-tunnel state restored from settings.json at startup (`ReadSplitTunnel` was never called).
  - Split-tunnel toggle and app edits revert in memory when the write fails.
  - NAT-PMP requests UDP (op 1) first and keeps the UDP mapping's port (was TCP-first, kept TCP port).
  - `ParseStatus`: unparseable `Load` is an error, not a silent 0.
  - CLI timeout 30s → 10s (design default) to bound event-loop stalls.
  - Traffic sampler marks the interface gone when sysfs reads fail.
  - Free-tier countries sort first (`Aggregate(..., true)`; design says unconditional).
  - Removed dead `patch()`; maintenance tooltip on country rows; "Negotiating port…" only while connected; tooltip rates gated on `traffic_monitoring`.
- `9430e68` — `ErrorDetail` skips the CLI wrapper's sentry/eventlet deprecation
  block on stderr (verified live: proton-vpn-cli 1.0.3 prints it on every
  invocation).

Verification at merge time: 85/85 unit tests, `make validate` 14/14,
`go test ./tests/integration/` fully green (the old `TestPluginCalendarGateAllViews`
failure was fixed on main separately).

## Shell-side dependency

The VPN glyphs (`shield`, `bolt`, `vpn_key_off`, `gpp_bad`, `dns`,
`location_on`, `security`, `flag`, `download`, `upload`, …) exist only on
sysc-shell branch **`feat/icon-union`** (`b0d4a74`, pushed) — a merge of
`feat/protonvpn-glyphs` (has VPN glyphs, no `docker`) and `feat/docker-glyph`
(has `docker`, no VPN glyphs). Neither parent alone satisfies both plugins.
The union branch is built and verified (render tests pass, SOURCE.md hash
matches the rebuilt font). **Not yet merged to sysc-shell `main`** — merging it
is an open item.

## Laptop deployment (192.168.0.64:7777, user nomadx)

- `~/.local/bin/sysc-shell` ← built from `feat/icon-union` (backup: `sysc-shell.bak-0927`).
- Plugin checkout: `~/sysc-plugins-main` (worktree of sysc-plugins `origin/main`,
  currently `3b5dff6`), binary built at `plugins/protonvpn/bin/`.
- Plugin symlink: `~/.config/sysc-shell/plugins/protonvpn` → `~/sysc-plugins-main/plugins/protonvpn`
  (was /tmp, re-pointed to survive reboot).
- `~/.config/sysc-shell/config.json`: `plugins.enabled` now includes
  `org.sysc.protonvpn` (backup: `config.json.before-protonvpn-20260927`).
  **The shell requires this explicit entry — a plugin dir alone is not enough.**
- Shell runs as systemd user unit: `systemctl --user restart sysc-shell.service`.
- Environment: proton-vpn-cli 1.0.3-1, daemon `proton.VPN.service` (system unit),
  account `lukegiles32@protonmail.com`, kill switch off, netshield malware-only,
  real 24MB `~/.cache/Proton/VPN/serverlist.json`, Niri session.

## Verified live (parsers all match the real CLI)

- `protonvpn status` (disconnected): `Status: Disconnected` alone — parser matches.
- `protonvpn config list`: tabulate table with the documented rows — parser matches.
- `protonvpn info`: `Account: 'lukegiles32@protonmail.com'` — parser matches.
- Plugin process starts, polls, and stays alive with zero errors in
  `journalctl --user -u sysc-shell.service`.

## Open issue 1 — bar pill not visible (user-confirmed)

The plugin runs but no ProtonVPN pill appears in the bar. Strong hypothesis:
`config.json` `bar.items` is an explicit list; other plugins appear via entries
like `{"id": "plugin", "plugin": "org.sysc.cat", "entry": "bar", "instance": "cat-1"}`.
No such entry exists for `org.sysc.protonvpn`. Next step: add one (back up
config.json first), restart the user service, confirm the pill renders with
`vpn_key_off` + country code. If it still doesn't render, check the shell's
plugin-bar discovery path for a manifest/registration gap.

## Open issue 2 — daemon-side connect failure (blocks connect testing)

`protonvpn connect` fails: daemon logs `CONN.CONNECT:START` (Server AU#366,
WireGuard) then silently reverts to `Disconnected (initial state)` ~1.5s later.
No ERROR lines in `~/.cache/Proton/VPN/logs/vpn-cli.log`. Network to the server
is fine (ping + UDP to 79.127.155.65:51820 OK). The daemon also spams
`split_tunneling: Clearing config for user 1000` every ~5s — unexplained, worth
correlating. Not a plugin bug: the plugin's error path (stderr detail line,
`Connection error` state) is exactly what should surface this. Next steps:
`sudo journalctl -u proton.VPN.service -n 200` around a connect attempt; check
WireGuard module availability (`modprobe wireguard`), NetworkManager state, and
whether the GTK app can connect (it shares the daemon).

## Open issue 3 — smaller items

- Pre-existing on the laptop: `org.sysc.weather` plugin fails to start
  (`read plugin.hello: EOF`) — unrelated to this work.
- Once the pill renders: exercise panel tabs, connect/disconnect through the
  panel (expect the daemon error to surface as the designed error detail),
  quick-connect right-click, NetShield/kill-switch toggles, split-tunnel
  writes (verify `~/.config/Proton/VPN/settings.json` round-trips), NAT-PMP
  port (needs a P2P server + connected state).
- `feat/icon-union` merge to sysc-shell main (see above), then re-pin in
  sysc-plugins if the pin changes.

## Verification commands

```sh
# laptop
cd ~/sysc-plugins-main && go test -count=1 ./plugins/protonvpn/ ./cmd/sysc-plugin-protonvpn/
systemctl --user restart sysc-shell.service
journalctl --user -u sysc-shell.service -f   # watch plugin lifecycle
timeout 25 protonvpn status </dev/null       # CLI sanity (daemon must be up)
```
