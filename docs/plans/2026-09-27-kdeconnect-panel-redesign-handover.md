# KDE Connect panel: redesign landed, follow-ups

Follow-up to `2026-09-27-kdeconnect-audit-fixes-handover.md`. The panel
redesign, the action routing fix, Files error reporting and album art are
on `main` and deployed to the laptop. This doc lists what landed and what
is still open.

## Landed on main

### sysc-plugins

| Commit | Change |
|---|---|
| `b902e83` | Actions called plugin methods on the device object; the daemon exports them on `devices/<id>/<plugin>`, so ring, ping, share, clipboard, sftp and sms-app all failed with "no such interface". They now use `pluginPath`, as the readings did. The fake bus had the bug's shape and now mirrors the live daemon. Panel is one 480 width with every fixed size derived from `kdeconnect.PanelWidth`: three 136 cells per card row (action pills, recent thumbnails), shared pill metrics, switcher chips as a pinned row, readings in their own card. The per-type 400/525 resize is gone. Bar pill is the phone glyph only (the sysc-447 charge fill made it a meter; the owner rejected it). `view_fit_test.go` lays out 12 panel states with the host rules. |
| `3b5dff6` | The integration gate opens the panel at the manifest size instead of a fixed 400x520. |
| `5d34dc8` | Pin sysc-shell to main at `b11550a`. |
| `462dda1` | `startBrowsing` answers false when the phone refuses the mount; the action now fails with the daemon's `getMountError` reason instead of "Opening the file browser". The recent-images scan mounts with `mountAndWait`; `startBrowsing` would also open a file manager on every refresh. |
| `d977e7c` | Album art: `fetchMedia` reads `mprisremote.localAlbumArtUrl` on each device fetch and on its `PropertiesChanged`, and `composeMockup` draws the cover into the mockup's screen (flood-filled dark region from the artwork centre, cover-fit, NRGBA so the bezel stays byte-exact), cached with one live composite per kind. The wire has no overlay, so the cover rides inside the mockup image. A new composite forces a full snapshot. |

### sysc-shell

| Commit | Change |
|---|---|
| `9dfcc66` | `columnChildHeight` sizes a button whose one child is a column from that column's content. The tap-to-ping card measured 28 px (padding only) and its mockup, labels and meter overlapped the readings below. |
| `a57c413` | Local main (toast delivery `bebdc93`, tracking, plans) merged with origin. |
| `b11550a` | `feat/icon-union` merged into main (Docker whale, protonvpn glyphs); Material subset rebuilt from the pinned upstream, 139 glyphs. |

## Findings worth keeping

- The earlier "panel renders ~262 px" claim was wrong: the old crops were of
  the terminal. The panel honours the manifest width (400 logical = ~500
  physical at the laptop's 1.25 scale).
- A laptop screenshot showing old behaviour usually means a stale shell
  build, not a plugin bug. Check `go version -m ~/.local/bin/sysc-shell`
  before debugging layout. Several sessions deployed stale shells on
  2026-09-27; the owner's deploy rule (build only from an origin/main
  worktree, never replace a binary whose revision you don't contain) now
  governs every shell deploy.
- Deploy plugins by copying the built binary into
  `~/sysc-main-test/sysc-plugins/plugins/kdeconnect/bin/` on the laptop.
  That checkout is behind origin and holds other sessions' uncommitted
  edits; don't pull or `make install` there.

## Open

1. **Album art, live check.** Nothing was playing when it shipped, so the
   daemon's `localAlbumArtUrl` has not been seen populated on the laptop.
   Play media on the phone, open the panel, and confirm the cover shows.
   If it doesn't, read `busctl --user get-property org.kde.kdeconnect
   /modules/kdeconnect/devices/<id>/mprisremote
   org.kde.kdeconnect.device.mprisremote playerList localAlbumArtUrl`;
   an empty player list means the phone is not publishing its players.
2. **Files.** The phone refuses the mount with "Permissions missing:
   filesystem access". Grant the KDE Connect app file access on the phone
   (Filesystem expose plugin settings), then check that Files opens Thunar
   and the error toast no longer shows.
3. **Clipboard.** The call reaches the daemon and succeeds; syncing has no
   visible effect on the phone. Owner to confirm by pasting on the phone.
4. **SMS and Share.** Work partly; deferred by the owner until the items
   above are done.
5. **Tracking.** sysc-430 (live Niri gate) and epic sysc-420 can close once
   items 1 to 3 are confirmed. Ping is confirmed working on the phone.
6. **Local checkouts.** `~/sysc-plugins` main carries the same commits
   under other hashes (diverged, blocked by uncommitted
   github-notifications work); `~/sysc-shell` main is behind origin with
   other sessions' uncommitted edits. Reset each to origin/main once those
   owners commit.
