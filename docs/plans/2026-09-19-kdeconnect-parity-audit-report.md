# KDE Connect Plugin Parity Audit Report

**Scope:** `org.sysc.kdeconnect` (feat/kdeconnect, through e3edef9) audited against
DMS DankKDEConnect v3.0.0 (dms-plugin-registry #386). Every DMS source file was read:
`DankKDEConnect.qml` (1,459 lines), `KDEConnectDetailContent.qml`, `DeviceCard.qml`,
`ShareDialog.qml`, `SmsDialog.qml`, `InfoRow.qml`, `EmptyState.qml`,
`UnavailableMessage.qml`, `PhoneDisplay.qml`, `GenericPhoneImage.qml`,
`DankKDEActionButton.qml`, `DankKDEConnectSettings.qml`, `plugin.json`, `qmldir`,
22 translation files, and the three services (`KDEConnectService.qml`,
`PhoneConnectService.qml`, `ValentService.qml`). The gap ledger lives in bd
(sysc-431–sysc-449, children of sysc-420); this report is the evidence.

**Method:** feature-by-feature diff of the DMS QML/services against the plugin's
service.go/daemon.go/view.go/main.go, plus the live headless-daemon smoke
(SYSC_KDECONNECT_LIVE=1, paired Pixel 8 Pro) from e3edef9.

**Ledger ID map:** 1→sysc-431, 2→sysc-432, 3→sysc-433, 4→sysc-434, 5→sysc-435,
6→sysc-436, 7→sysc-437, 8→sysc-438, 9→sysc-439, 10→sysc-440, 11→sysc-441,
12→sysc-442, 13→sysc-443, 14→sysc-444, 15→sysc-445 (blocked by sysc-446),
16→sysc-447, 17→sysc-448, 18→sysc-449; shell prerequisite→sysc-446.

## What matches (verified, not assumed)

| Feature | DMS evidence | This plugin |
|---|---|---|
| Daemon discovery + availability watching | KDEConnectService `checkAvailability`/`dbusListNames`, NameOwnerChanged watch in PhoneConnectService | `addMatch` on the daemon sender + `NameOwnerChanged` (arg0), reconnect cycle, degraded-unavailable snapshot |
| Device list + full property set (name, type, isReachable, isPaired, isPairRequested, isPairRequestedByPeer, verificationKey, supportedPlugins) | `fetchDeviceInfo` GetAllProps | `fetchDevice` GetAllProps, same property names |
| Battery via `refreshed` signal + `PropertiesChanged` | `updateDeviceBattery`, member dispatch | `refreshed` body[charging, charge] + PropertiesChanged on batteryIface |
| Connectivity via `connectivityUpdated` + `PropertiesChanged` | `fetchConnectivityInfo` | `fetchConnectivity` on the same signals |
| Notification count with the introspect probe (locally disabled plugins export no object, upstream #3173) | `_probeNotificationsPath`/`fetchNotificationsCount` | `fetchNotifications` probes `Introspect` for a `notifications` node, once per device |
| Plugin-capability gating incl. the `kdeconnect_` prefix spelling | `hasPlugin` | `hasPlugin` identical rule |
| Pairing: request/accept/cancel/unpair + verification key + incoming-request toast | KDEConnectService calls + `pairingRequestReceived` | `performAction` pairing kinds + rising-edge `EventPairingRequest` with key |
| ring, ping, shareUrl, shareText, sendClipboard, startBrowsing, launchApp | KDEConnectService methods | same interface/method names in `performAction` |
| Incoming `shareReceived` toast | `onShareReceived` | `EventShareReceived` |
| Auto-select order (saved reachable → first reachable → first → none) | `autoSelectBestDevice` | `resolveSelection`, identical order; selection persisted user-only |
| Per-action toasts with the same success/failure intent | ToastService calls | `CallNotify` events, critical urgency on failure |
| Unavailable / empty states | `UnavailableMessage` / `EmptyState` | `stateCard`s (copy differs — see gaps) |
| Refresh cadence + minimum refresh spacing | `stateUpdateInterval` timer + `refreshMinTimer` | settings-driven ticker + `minRefreshGap` |
| Unpair/accept/reject re-read state immediately | `refreshDevices()` after calls | `reconcileNow` after pairing actions |

## Gap ledger (each item is a bd issue; priorities here are the bd ones)

### Behavior bugs — the plugin deviates from what the daemon/DMS actually do

1. **[P1] announcedName/selfId are never read correctly.** DMS reads them as DBus
   *method calls* on `org.kde.kdeconnect.daemon` (`dbusCall(..., "announcedName", [])`);
   my `GetAllProps` returned an empty `announcedName` in the live smoke — the live
   test proves the property dictionary does not carry it. Also `selfId` is unread.
   Fix: method-call reads; surface both (DMS shows them in the settings status card).
2. **[P1] SMS send uses the wrong transport.** PhoneConnectService.sendSms on the
   KDE backend does **not** call DBus — it shells out:
   `kdeconnect-cli -d <id> --send-sms <message> --destination <number>`.
   I call `org.kde.kdeconnect.device.sms.sendSms(number, body)`. Match DMS
   (`kdeconnect-cli` is already the manifest's required command), or prove the DBus
   path works live before deviating.
3. **[P1] File sharing uses the wrong method.** PhoneConnectService.shareFile
   converts the path to a `file://` URI and calls **shareUrl**; I call
   `shareIface.shareFile`, a method DMS never uses (and kdeconnectd's share plugin
   may not export). Route file shares through shareUrl like DMS.
4. **[P3] Panel open does not refresh.** DMS refreshes devices and re-runs
   auto-select when the popout opens (`onPopoutOpenChanged`). My ViewOpen only
   publishes.

### View/interaction gaps (same feature, less state or gating)

5. **[P2] Device cards are thinner than DMS DeviceCards.** Missing per card: battery
   chip (level icon + %), network icon chip, and the pairing status states — DMS
   shows "Pairing requested" / "Pairing..." (warning colour), "Not paired", "Offline",
   and *no* status line when connected. My rows only show connected/offline/not-paired.
6. **[P2] Switcher cards can't act on pairing.** DMS DeviceCards carry per-card
   Accept/Reject rows when `isPairRequestedByPeer` and a Request Pairing row when
   reachable, unpaired, and not already requested — visible on selectable switcher
   cards too. Mine offers pairing actions only for the *selected* device.
7. **[P3] Share dialog has no URI gating.** DMS validates URI-ness with a scheme
   regex: the URI button is enabled only for a valid URI, the Text button only for
   non-empty text, plus a close button. Mine: both buttons always enabled, and my
   submit heuristic diverges (prefix check instead of the URI test).
8. **[P3] SMS dialog is looser than DMS.** DMS: single-line message field, Send
   disabled until number *and* message are non-empty. Mine: multiline body, send
   never gated.
9. **[P3] Refresh cannot be disabled, and there is no busy state.** DMS's
   stateUpdateInterval slider allows 0 = disabled and shows a spinner
   (`isRefreshing`) on the refresh control. Mine clamps to ≥5 s and never reports
   busy (the wire has StatusBusy unused).

### Label/icon fidelity

10. **[P3] Network type labels unmapped.** DMS maps NR/5G→"5G", LTE/LTE_CA→"LTE"/
    "LTE+", HSPA/UMTS→"3G", EDGE/GPRS/GSM→"2G", capitalises unknowns, "N/A" when
    empty; I pass the daemon's raw string through. Strength 0 is "No Signal" in DMS,
    "None" in mine.
11. **[P3] Battery/network icon breakpoints differ, and per-state network glyphs are
    missing.** DMS: charging icons at ≥90/≥60/≥40/<20, level icons at ≥95/≥80/≥65/
    ≥50/≥35/≥20/≥10 then alert; network icons per strength bar and per type
    (5g, 4g_mobiledata, 3g_mobiledata, 2g_mobiledata, signal_cellular_nodata). Mine:
    linear 7-band battery, one `network` glyph everywhere. Needs catalogue additions
    (sysc-shell) + band changes.
12. **[P4] InfoRow layout and state-card copy.** DMS stacks label over value with a
    28px leading icon; mine right-pins the value beside the label. Empty copy:
    "No devices found" + hint; unavailable card is error-styled with
    "Phone Connect Not Available". Cosmetic deltas.

### Larger features DMS has that this plugin does not (decision + shell dependencies)

13. **[P2 — decision] Valent backend.** DMS runs two interchangeable backends:
    kdeconnectd and `ca.andyholmes.Valent` (an ObjectManager + org.gtk.Actions API —
    a different protocol, 864 lines), auto-detected via NameOwnerChanged on both
    names. My plugin is kdeconnectd-only (documented non-goal). Full parity needs a
    second backend behind the existing seams.
14. **[P2 — decision] Ongoing media (MPRIS) section.** DMS renders a full player:
    play/pause, prev/next, replay-10/forward-10, draggable seekbar with position and
    length, track title/artist/album, player identity, artwork, `showOngoingMedia`
    setting — sourced from the host MPRIS bridge (`org.mpris.MediaPlayer2.kdeconnect.*`)
    with the device `mprisremote` DBus fallback. Mine defers to the shell's first-party
    media widget, which surfaces the same bridge players; parity requires either the
    in-panel section (mprisremote over DBus) or an explicit accepted deviation.
15. **[P3 — needs sysc-shell support] Recent images grid + device customisation.**
    DMS scans the SFTP mount (`find`, maxdepth, png/jpg/jpeg/webp), shows a thumbnail
    grid with per-image open (xdg-open), drag-out (ripdrag/xdragon/dragon), portal
    share, per-device recent-images path (validated against the SFTP mount point),
    custom device images, device type overrides, maxRecentImages (1–12) and
    scanSubdirectories settings. The plugin wire has no image node, no file-picking,
    and no OS drag — this needs sysc-shell wire/capability work first
    (image node, file dialog/drag, per-device map settings).
16. **[P4] Charging fill on the bar pill** (`enableChargingAnimation`): a level fill
    in error/warning/success tints while charging. Wire has no animation; a static
    accent approximation is possible, true parity needs shell animation support.
17. **[P4] Keyboard shortcuts** — Ctrl+Tab/Ctrl+Shift+Tab device cycling, Alt+1–9
    direct select, S to share. Shortcuts belong to the shell, not the plugin wire;
    needs a host capability before it can exist.
18. **[P4] i18n.** DMS ships 22 translation files through I18n.trFor. The shell has
    no plugin translation mechanism. Deferred.

### Verified equivalent-by-architecture (not gaps)

- **CC detail surface:** DMS renders the same content twice (popout + control-centre
  detail); the sysc attached panel is that single surface.
- **Vertical bar pill:** the shell has no vertical bar.
- **Lock/remote-commands/photo/conversations:** present in DMS's *service* layer but
  no fetched UI wires them (conversations/remote commands/lockdevice/photo are dead
  code in the plugin UI), so no parity pressure.
- **Ripples, hover scaling, device-switch animation, skeleton shimmer:** animation
  and hover polish are host-owned; the wire deliberately carries none.
- **plugin.json metadata** (`firstParty`, `requires_dms`, icon field, permissions):
  sysc manifests express the same things differently (capabilities, requires.commands).

## Live-smoke evidence (headless daemon, e3edef9)

- Device list decodes: two entries for the paired Pixel 8 Pro (one reachable, one
  stale-offline — the daemon itself lists both; DMS shows both too).
- Battery read 59% with charging flag; battery unknown on the stale entry.
- Plugin gating correct: `kdeconnect_battery`, `kdeconnect_findmyphone`, `kdeconnect_sms`
  etc. all resolve through the prefix rule.
- `announcedName` empty — the trigger for gap 1.
