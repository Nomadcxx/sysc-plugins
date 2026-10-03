# Session handover — 2026-10-04

**Workspace (this PR):** `/home/nomadx/sysc-plugins`  
**Parallel work (not this PR):** `/home/nomadx/sysc-shell/.worktrees/feature/file-browser`  
**Date:** 2026-10-04

This is the single session record. It covers Phone Connect work in sysc-plugins, the commissioned sysc-shell file browser, the audits, and what was fixed before the plugins PR.

Do not treat the file-browser as part of the sysc-plugins PR. It lives in another repo and is **not ready to consume**.

---

## 1. What the human asked

1. Finish unfinished KDE Connect work: picture inside the phone silhouette, and the panel length that kept drifting.
2. Locked plan items **1** and **3** only: idle mockup fill from newest SFTP photo (album art still wins if `localAlbumArtUrl` exists); keep 480 width, shrink height to the idle stack (640), scroll recents/composers. No `CallPanelResize`. Empty band above the phone was investigated and **not** chosen.
3. Files does nothing (permissions / no in-shell browser). They disagreed that the shell does not need a file manager: commission a **sysc-shell** file browser as infrastructure, do not derail this session, continue kdeconnect until that agent reports back.
4. When that agent finishes, it must write a post-implementation handover for audit.
5. Then: one session handover of everything, a deep audit, and a PR to **sysc-plugins** from that outcome.

---

## 2. What landed in sysc-plugins (this PR)

Phone Connect (`org.sysc.kdeconnect`).

### Idle mockup fill

Empty `recent_images_path` (the default) mounts with `mountAndWait` (never `startBrowsing` on the ticker), scans `DCIM` and `Pictures` one subdirectory deep (`Camera`, `Screenshots`), thumbnails the newest png/jpg, and `composeMockup`s it into the phone frame. The recents grid stays empty. Album art from `fetchMedia` still wins when the daemon names a local cover. Live `kdeconnect-git` still has **no** `localAlbumArtUrl`; the precedence is for a future daemon.

### Panel size

`PanelWidth=480`, `PanelHeight=640`. Manifest matches. Root is `KindList` with `Height: PanelHeight` and `EventScroll`. No `CallPanelResize`.

### SFTP error card

Failed `mountAndWait` with a configured recents path copies `getMountError` onto `Device.SFTPError`. Failed Files (`startBrowsing` false) does the same and republishes. The panel shows **Phone files unavailable** plus the daemon reason under Actions. A successful mount or Files click clears the error. Empty-path background scans (default, recents off) do **not** write the card — permission nags stay on the Files action. Toasts still fire.

### Audit hold items that were fixed before this PR

Deep review (uncommitted kdeconnect diff) said **do not PR** until idle SFTP was off the signal path. Fixed:

| Finding | Fix |
|---|---|
| Default idle fill ran `mountAndWait` on **every** `publish()`, including battery/mpris/notification signals | `scanAndPublish` on first open, ticker, manual refresh, selection change. Signals only `publish()`. |
| Empty `mountPoint` after a true mount was silent | Sets `SFTPError` to `SFTP mounted with no mount point`. |
| `fetchMedia` `GetAll` error left stale `MockupArt` and blocked idle fill | Clears `MockupArt`. |
| `applyIdleMockup` skipped whenever `MockupArt != ""`, even if the file was gone | `os.Stat` before skip. |
| Files click never wrote `SFTPError` or republished | Browse false sets it, browse true clears it, serve loop `publish()` after `ActionBrowse`. |

Files still does **not** open an in-shell browser. Success still depends on kdeconnectd `startBrowsing` (Qt/`xdg-open`). That is deferred to the shell FM once it is actually shippable.

### Files in this PR

```
cmd/sysc-plugin-kdeconnect/main_test.go
plugins/kdeconnect/manifest.json
plugins/kdeconnect/mockup.go
plugins/kdeconnect/service.go
plugins/kdeconnect/service_test.go
plugins/kdeconnect/view.go
plugins/kdeconnect/view_fit_test.go
plugins/kdeconnect/view_test.go
docs/2026-10-04-session-handover.md
```

Not in this PR: `.codex-tmp/`, `docs/moonbit-audit-handover.md`, `docs/pr-audit-*`, the earlier `docs/2026-10-04-kdeconnect-idle-mockup-handover.md` (superseded by this file).

### Tests (sysc-plugins)

```
timeout 90s env GOMAXPROCS=2 go test -count=1 ./plugins/kdeconnect -run Test
timeout 90s env GOMAXPROCS=2 go test -count=1 ./cmd/sysc-plugin-kdeconnect -run Test
```

Never `go test ./...` or `-race`.

### Still not live-verified on the laptop panel

Idle composite, 640 fold, error card, real Pixel `getMountError` string. Rebuild/restart `sysc-plugin-kdeconnect` before judging the running shell.

### Remaining kdeconnect ceilings

- Empty-path idle scan mounts from service start (`newService` / ticker), not from panel visibility. Cost per scan: one `mountAndWait` + one `find` over existing `DCIM`/`Pictures` + one thumbnail resolve. A successful scan that finds no photos sets `idleScanEmpty` and further ticks skip until manual refresh, selection change, or settings change. Failed mounts keep retrying. A scan that found a photo still polls on the ticker so a newer screenshot can replace it.
- `newestIdleImage` maxdepth 2; dated `DCIM/Camera/2026/…` trees are missed.
- After music stops, idle fill waits until the next scan (ticker/refresh), because mpris signals publish without rescan.
- Files success with no window is still a success toast. Shell FM is the follow-up, not Thunar-from-the-plugin.

---

## 3. What the file-browser agent did (sysc-shell, not this PR)

**Agent:** commissioned as a background general-purpose task on sysc-shell only.  
**Worktree:** `/home/nomadx/sysc-shell/.worktrees/feature/file-browser`  
**Branch:** `feature/file-browser` (tracks `origin/main`, no remote feature branch)  
**HEAD:** `d5060893` — implementation is **uncommitted**.  
**Primary checkout** `/home/nomadx/sysc-shell` is on another branch. Do not audit or build there.

### Spec and audit docs (gitignored under `docs/plans/`)

- Design: `docs/plans/2026-10-03-sysc-shell-file-browser-design.md`
- Agent handover: `docs/plans/2026-10-03-sysc-shell-file-browser-handover.md`  
  `git add -f` those two if they should travel with a later shell PR.

### Locked shape (as built)

Host-owned `PanelFiles`, capability `files`, call `files.browse` (`CallFilesBrowse`). Protocol **15 → 16**. Modes: `open` | `pick-file` | `pick-directory`. Jail: absolute paths, `EvalSymlinks`, `filepath.IsLocal` (not `HasPrefix`). Pick waits without the 10s `pluginCallTimeout`. Plugins do not draw a KindList of entries. No Fyne/lf/superfile.

### How kdeconnect Files should consume it *later*

1. After sysc-plugins `validate-manifests` allow-lists `files` / `CallFilesBrowse`.
2. Negotiate protocol minor ≥ 16 (and the host must keep 15 in `Supported` or every plugin rebuilds; audit found a 15→7 handshake cliff).
3. When the SFTP mount is up: `files.browse` with `root` = mount dir, `mode` = `"open"`, `title` = device name.
4. Do not call `sftp.startBrowsing` / Dolphin. Keep kdeconnect’s own mount-jail for thumbnails; the shell jail is a second fence.
5. `pick-file` is for share-a-file, not the Files button.

### File-browser audit: **NO-GO for a sysc-shell PR, do not wire kdeconnect**

Deep review of that worktree found:

| Severity | Issue |
|---|---|
| Critical | Open mode `xdg-open` uses `CommandContext` + `Start` + immediate `cancel()` — the child is killed as soon as it spawns. Files `mode=open` likely does nothing. |
| Important | Second `files.browse` replaces the session but does not rebuild the panel (wrong listing vs hit targets). |
| Important | `ctx.Done()` can close whoever currently owns `PanelFiles`. |
| Important | Jail is not re-checked at activate / `xdg-open` / pick result (list-then-open TOCTOU). |
| Important | KindImage thumbs are never decoded on `PanelFiles`. |
| Important | Protocol `Supported` list jumps 16 then 7, skipping 15. |

Do **not** open a sysc-shell PR until those are fixed. Do **not** patch kdeconnect Files onto `files.browse` in this plugins PR.

---

## 4. Deep audit outcome (this session)

Two independent reviews of the actual diffs, plus scoped tests.

### sysc-plugins kdeconnect (this PR)

**First pass: hold.** Critical: idle SFTP on every signal `publish()`. Important: silent `mountPoint`, stale `MockupArt`, Files click not feeding the card.

**After fixes:** those hold items have tests and implementation:

- `TestBatterySignalDoesNotRescanSFTP`
- `TestRefreshRecentImagesDegradesToEmptyGrid` (empty `mountPoint` now requires `SFTPError`)
- `TestFetchMediaGetAllFailureClearsMockupArt`
- `TestApplyIdleMockupReplacesMissingArt`
- `TestClipboardBrowseAndSMSAppActions` waits for the error on the snapshot

**PR verdict after fixes:** ship the kdeconnect slice. Do not claim Files is an in-shell browser. Do not include shell FM code.

### sysc-shell file browser (not this PR)

**Verdict: no-go.** See §3.

---

## 5. Suggested audit order for a human

1. This file.
2. `git diff` of the eight kdeconnect files. Confirm: no `CallPanelResize`, no Files→`files.browse`, no empty-band padding change, signals do not call `refreshRecentImages`.
3. Read `serve`’s `publish` vs `scanAndPublish` (`service.go` ~343–396), then `refreshRecentImages` + `applyIdleMockup` + `fetchMedia`.
4. Re-run the two `go test` commands in §2.
5. Laptop: deny phone FS access → error card; allow + refresh → idle mockup; Share/SMS must scroll inside 640.
6. File-browser: read the shell handover in the worktree **only after** kdeconnect. Do not merge or wire it yet.

---

## 6. What a follow-up session should not do

- Do not wire kdeconnect Files to `files.browse` until the shell critical/important items are fixed and a shell PR exists.
- Do not open a sysc-shell file-browser PR from this plugins session.
- Do not compact the empty band unless a new visual pass asks.
- Do not scrape album art some other way; wait for `localAlbumArtUrl`.
