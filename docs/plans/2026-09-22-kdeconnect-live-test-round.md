# KDE Connect Live Test Round (Plan D Task 8)

> **For Claude:** Execute over `ssh -p 7777 nomadx@192.168.0.64`. Steps marked **MANUAL** need the
> owner (sudo, or the phone in hand) — stop and hand them the exact command before proceeding.

**Goal:** prove the gap-filled Phone Connect plugin on real hardware — shell and plugin built from
`main`, the real `kdeconnectd`, and a real paired phone — then record the evidence and close the
bd items the round actually satisfies.

**What is under test:** Plan D Tasks 0–4 (pin bump to wire minor 6, icon-led buttons, tap-to-ping,
recent images grid, device mockup). Tasks 5–7 (panel.resize, view.focus, charging fill) are **not
implemented yet** and are out of scope; the matrix must not claim them.

---

## Verified ground truth (gathered 2026-09-22)

| Fact | Value |
|---|---|
| Dev `sysc-shell` main | `70f495e` — wire minors 4/5/6 (image/stroke, panel.resize + view.focus, animate), pushed |
| Dev `sysc-plugins` main | `3d05d35` — plugin Tasks 0–4, pushed |
| Laptop OS | Arch x86_64, niri, `sysc-shell.service` active (`Restart=on-failure`) |
| Laptop shell binary | `~/.local/bin/sysc-shell`, built Sep 19 — **pre-minor-5**, must be replaced |
| Laptop plugin installs | `~/.config/sysc-shell/plugins/*` are symlinks into `~/sysc-plugins/plugins/*`; `kdeconnect` absent |
| Laptop clones | `~/sysc-shell` @ `68eaa0c` (behind 121, FF-OK), `~/sysc-plugins` @ `e53b863` (behind 57, FF-OK), both clean |
| Laptop toolchain | go 1.27.1 (go.mod wants 1.26.4), 8 cores, 734 G free |
| KDE Connect | package **not installed** (`extra/kdeconnect 26.08.1-1`); `kio-fuse 5.1.1` already installed |
| Plugin contract | manifest id `org.sysc.kdeconnect`, protocol {1,6}, exec `bin/sysc-plugin-kdeconnect`, requires command `kdeconnect-cli`, DBus name `org.kde.kdeconnect` |
| Bar pill config | `{"id":"plugin","plugin":"org.sysc.kdeconnect","entry":"bar","instance":"kdeconnect-1"}` (`internal/config/load.go` wireItem) |
| Mockup assets | `plugins/kdeconnect/assets/{phone,tablet,desktop,laptop}.png`; resolved as `<exe dir>/../assets/<kind>.png`, so the symlinked install resolves correctly |

---

## Task 0 — install the daemon (MANUAL: sudo)

```bash
sudo pacman -S --needed kdeconnect
```

Verify (owner or Claude over ssh):

```bash
kdeconnect-cli --version          # binary on PATH — discovery's MissingCommands gate
pacman -Ql kdeconnect | grep -E 'kdeconnectd$|org.kde.kdeconnect.service'
kdeconnect-cli -l                 # first call D-Bus-activates kdeconnectd; empty list is fine
```

Expected: version prints, the D-Bus activation file exists, `-l` returns without error (device
list may be empty until the phone appears).

## Task 1 — sync and build on the laptop

```bash
git -C ~/sysc-shell pull --ff-only     # 68eaa0c -> 70f495e
git -C ~/sysc-plugins pull --ff-only   # e53b863 -> 3d05d35

cd ~/sysc-shell
go build -o ~/.local/bin/sysc-shell.new ./cmd/sysc-shell \
  && cp ~/.local/bin/sysc-shell ~/.local/bin/sysc-shell.bak-kdeconnect-test \
  && mv ~/.local/bin/sysc-shell.new ~/.local/bin/sysc-shell

cd ~/sysc-plugins && make build        # all nine plugins into plugins/<dir>/bin/
ln -sfn ~/sysc-plugins/plugins/kdeconnect ~/.config/sysc-shell/plugins/kdeconnect
```

Verify:

```bash
ls -la ~/.config/sysc-shell/plugins/kdeconnect/bin/ ~/.config/sysc-shell/plugins/kdeconnect/assets/
~/.config/sysc-shell/plugins/kdeconnect/bin/sysc-plugin-kdeconnect --help 2>&1 | head -1 || true
```

Expected: binary and four PNGs present through the symlink. `make build` refreshed every plugin
binary in place (the symlinks point at the repo), so the other eight plugins also pick up their
current main builds — that is intended; note any behaviour change the owner notices.

## Task 2 — config

```bash
cfg=~/.config/sysc-shell/config.json
cp "$cfg" "$cfg.before-kdeconnect-$(date -u +%Y%m%dT%H%M%SZ)"
jq '.plugins.enabled += ["org.sysc.kdeconnect"]
    | .bar.items.right += [{"id":"plugin","plugin":"org.sysc.kdeconnect","entry":"bar","instance":"kdeconnect-1"}]' \
    "$cfg" > "$cfg.new" && jq empty "$cfg.new" && mv "$cfg.new" "$cfg"
```

Notes:
- `plugins.settings` stays untouched — manifest defaults (refresh 30 s, clipboard on, card on)
  are the right first-round values. `recent_images_path` is set in Task 5 after the mount point
  is discovered live.
- The shell validates config in full before replacing live state; a bad edit fails startup, which
  is why the backup is taken first.

## Task 3 — restart and first boot

```bash
systemctl --user restart sysc-shell
sleep 3
systemctl --user is-active sysc-shell
journalctl --user -u sysc-shell --since -2min --no-pager | grep -iE 'kdeconnect|plugin|negotiat|minor' | head -30
```

Expected:
- service active; bar reappears with the new pill on the right lane (phone glyph or placeholder —
  the daemon may not be running yet).
- journal shows the kdeconnect candidate discovered and started, and the wire handshake settling
  on minor 6 (the supervisor offers {1,3},{1,4},{1,5},{1,6}; the plugin declares {1,6}).

Rollback at any point: restore `sysc-shell.bak-kdeconnect-test` and the config backup, remove the
symlink and the enabled entry, restart.

## Task 4 — pair the phone (MANUAL: phone in hand)

Prerequisite: KDE Connect app on the phone, same LAN, both discoverable.

```bash
kdeconnect-cli -l                    # refresh; note the device id
kdeconnect-cli --pair -d <id>        # accept the prompt on the phone
```

The plugin UI path is the one under test — prefer it: open the panel from the bar pill, use the
unpaired card's **pair** button, accept on the phone, then verify the pairing card shows the
verification key with **accept/reject** and lands on the paired device card.

Expected after pairing: device card with name, status line, battery progress, network chips, and
the type-sized mockup (a phone shows `phone.png`, 135×260).

## Task 5 — test matrix

Record pass / fail / degraded per row, with one line of evidence each.

| # | Check | How | Expected |
|---|---|---|---|
| 1 | Pill + panel open | Click the bar pill | 400×520 attached panel opens |
| 2 | Device card content | Look | Name, status, battery progress, network chips, mockup image |
| 3 | Tap-to-ping | Click the device card | Phone rings/vibrates + notification (sysc-468) |
| 4 | Ping hidden with card | Look at action row | ring, browse, clipboard, share, sms — **no ping** (Task 2 rule) |
| 5 | Ping restored | Settings → `show_device_card: false` | Action row shows six buttons, ping second |
| 6 | Ring | Action row ring button | Phone rings |
| 7 | Clipboard send | Action row clipboard | Phone clipboard/toast |
| 8 | Share URL | Share composer, type URL, send | Phone receives/opens |
| 9 | Share file | Share composer, path, send | Phone receives file |
| 10 | SMS | SMS composer, number + body, send | Message sends (uses `kdeconnect-cli`; watch journal on failure) |
| 11 | Settings round-trip | Change `refresh_seconds`, restart shell | Value survives |
| 12 | Live delta | Change phone battery/notifications | Card updates without reopening the panel |
| 13 | Recent images | Discover mount (below), set `recent_images_path`, restart | Grid of thumbnails under "Recent" |
| 14 | recent-open | Click a grid image's folder button | `xdg-open` shows the local thumbnail's source |
| 15 | recent-share | Click a grid image's share button | Phone receives the file |
| 16 | Offline state | Disable phone Wi-Fi | Unavailable card; recovers when reachable |
| 17 | Journal clean | `journalctl --user -u sysc-shell --since -30min` | No repeating error lines |

Recent-images mount discovery (run before row 13):

```bash
kdeconnect-cli --sftp -d <id> || true          # may open a file manager; also mounts
findmnt | grep -i -E 'kde|sftp'                # where the mount landed
busctl --user introspect org.kde.kdeconnect /modules/kdeconnect/devices/<id>/sftp | grep -E 'mountPoint|startBrowsing'
```

Then set `plugins.settings["org.sysc.kdeconnect"].recent_images_path` to a phone-side directory
relative to that mount (typically `DCIM/Camera` or `Pictures/Screenshots`), plus
`max_recent_images` (1–12) and `scan_subdirectories` as desired, and restart the shell.

Known risk to record honestly: the plugin calls `startBrowsing` then reads `mountPoint()`. If the
daemon only mounts on an explicit mount request, `mountPoint()` may return empty and the grid
stays silently empty — that is a finding, not a failure to hide. `kio-fuse` is installed, which
is the usual prerequisite for the mount landing.

## Task 6 — evidence, handover, bd

- Write the completion handover (`2026-09-22-kdeconnect-live-test-handover.md`): gate output from
  both repos on the laptop, the matrix table with results, live observations, and the honest
  deviation list. Write once, never edit.
- Close in bd (sysc-shell tracker, run from `/home/nomadx/sysc-shell`): `sysc-478`, `sysc-468`,
  `sysc-445` (grid + mockup), `sysc-430` (live Niri gate) — only the rows the matrix actually
  passed. `sysc-447` (charging fill) stays open: Task 7 is not implemented.
- Add the register row here in the same commit as this plan.

## Rollback

```bash
cp ~/.local/bin/sysc-shell.bak-kdeconnect-test ~/.local/bin/sysc-shell
cp ~/.config/sysc-shell/config.json.before-kdeconnect-* ~/.config/sysc-shell/config.json  # newest backup
rm ~/.config/sysc-shell/plugins/kdeconnect
systemctl --user restart sysc-shell
# optional, owner's call: sudo pacman -Rns kdeconnect
```

## Out of scope

- Plan D Tasks 5–7 (panel.resize, view.focus, charging fill) — unimplemented; not claimable.
- Multi-device selection, unpair flows, notification mirroring depth, MPRIS.
