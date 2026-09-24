# KDE Connect live failure audit report

Date: 2026-09-22
Scope: `org.sysc.kdeconnect` at sysc-plugins `3d05d35` and sysc-shell `70f495e`.

## Verdict

The redesign is **go**, after three blocking fixes land in this order:

1. Prefer a paired, reachable device when the user has no valid saved selection.
2. Deliver one primary bar activation. The current host sends a pointer event on press and an activation on release; the plugin opens the toggle panel for both.
3. Route the merged device glyph range through the embedded icon face.

O3 follows directly from the current selection policy when an unpaired reachable device precedes a paired phone. The laptop order and selected ID could not be re-read because the audit SSH target returned `No route to host`; the mechanism is confirmed, the live instance is unverified. O1 and O2 have source and focused-test proof. O4 is a real usability failure, but its exact natural-width threshold still needs a paired live panel after the first three fixes.

The owner saw only the unpaired card. The paired surface remains an evidence-based code and visual audit, not a live pass.

## Root-cause table

| Symptom | Mechanism | Evidence | Fix locus | Confidence |
|---|---|---|---|---|
| O1: pill glyph looks like a lowercase `a` | The plugin emits `smartphone`, and the merged shell maps it to PUA `U+E031`. The embedded TTF contains `U+E031`, but `FontMap.iconFaceFor` stops at the older detail range (`U+E030`). Fontscan therefore resolves the PUA rune through a text face. | `plugins/kdeconnect/view.go:113-131`; sysc-shell `internal/render/iconfont.go:119-151,451` at `70f495e`; sysc-shell `internal/render/fontmap.go:172-188`; fontTools on `internal/render/icons/sysc-icons.ttf` from `70f495e` reports `U+E031 -> uniE031`. | Host font routing. Keep the plugin name; add device/network ranges and a coverage test. | **Confirmed, high** |
| O2: panel opens only on right-click | `handlePluginBar` sends `EventPointer` for primary press and `EventActivate` for primary release. `handleInput` opens the panel for node `open` without checking event kind. `panel.open` toggles an owned panel, so left press opens and left release closes. Secondary release sends one pointer event, which the same handler treats as open. | sysc-shell `internal/shell/bar.go:824-863`; `internal/shell/pluginhost.go:1009-1032`; `internal/shell/pluginhost.go:661-710` (toggle close); `cmd/sysc-plugin-kdeconnect/main.go:157-166`; host test comment at `internal/shell/pluginhost_test.go:924-939`. Focused host test passes and documents the release toggle. | Event contract. Narrow plugin fix: ignore pointer events for `open`, or change host delivery so activate-only bar buttons activate once. Add a real click regression test. | **Confirmed, high** |
| O3: panel shows only “Request pairing” | `resolveSelection` retains a saved paired and reachable device, then selects the first reachable device regardless of pairing. `PanelTree` sends an unpaired selection to `unpairedCard` and returns before the paired card, actions, info, grid, or composers. | `plugins/kdeconnect/service.go:1093-1111`; `plugins/kdeconnect/view.go:202-223,408-422`; daemon order is copied from the `devices()` reply at `service.go:442-461`. The reported `archPC`/Pixel order was not re-read because SSH was unreachable. | Plugin selection policy. Prefer paired and reachable, then reachable, then any device; preserve explicit saved selection rules. | **Mechanism confirmed; live instance unverified** |
| O4: panel is too narrow | The manifest fixes the panel at `400x520`. The paired tree can contain a switcher, 260px mockup, action row, info rows, recent grid, and composer. No `panel.resize` call exists in the plugin; the host capability is present but was out of scope for the run. | `plugins/kdeconnect/manifest.json:12`; `plugins/kdeconnect/view.go:190-231,520-679`; host resize bounds at sysc-shell `internal/plugin/hostcall.go:291-300`; no resize call in `cmd/sysc-plugin-kdeconnect/main.go`. | Plugin layout and width policy, with host live acceptance. Use 400px base and resize to 525px for large mockups, or collapse sections if the live measurement shows 400 is sufficient. | **Observed failure confirmed; cause split pending paired render** |

## F1-F6 dispositions

| Finding | Disposition | Evidence and limit |
|---|---|---|
| F1 selection can choose an unpaired reachable device | **Evolved: code confirmed, live selection unverified** | The fallback is exactly `first Reachable` at `service.go:1093-1111`; the selected view is exactly `unpairedCard` at `view.go:206-210`. The laptop read could not establish the daemon order or plugin state. |
| F2 glyph is a font fallback signature | **Confirmed** | `smartphone` is `U+E031` in sysc-shell `70f495e`; the embedded TTF has that glyph. `FontMap.iconFaceFor` omits the device range, so the rune falls to the system map. |
| F3 left-click activation is lost between bar and plugin | **Evolved: narrowed to double delivery** | The host delivers primary pointer plus activation; the plugin opens on both. The host test states that release would toggle the panel closed. The symptom is not a missing `EventActivate` route. |
| F4 journal tray-menu error predates this deploy | **Unverified timestamp; unrelated code path** | `KindButton` is kind 3 at sysc-shell `internal/ui/tree.go:10-15`; tray input is in `internal/shell/traymenuhost.go:407-430`; the layout error originates in `internal/ui/layout.go:90-145`. The 14:40:09 versus 14:45:38 timestamps come from the handover and could not be re-read live. Do not attribute it to KDE Connect. |
| F5 paired mockups were hidden downstream of selection | **Confirmed by code; install-path live proof unavailable** | `deviceMockup` resolves `<executable directory>/../assets/<kind>.png` at `plugins/kdeconnect/view.go:36-93`; the symlinked install exposes `bin/` beside `assets/` per the runbook. The paired branch calls it at `view.go:526-531`. The laptop filesystem could not be read during this audit. |
| F6 most of T5 was never tested | **Confirmed** | The runbook matrix marks settings, live delta, recent images, offline recovery, and journal checks after the abandoned first observations (`docs/plans/2026-09-22-kdeconnect-live-test-round.md:128-167`). No paired render or live mount evidence exists in the handover. |

## Gap table

Reference: DMS DankKDEConnect v3.0.0 and the parity audit in `docs/plans/2026-09-19-kdeconnect-parity-audit-report.md`.

| Area | Reference behavior | Plugin today | Severity | Fix locus |
|---|---|---|---|---|
| Bar glyph (O1) | Phone glyph comes from the shell icon catalogue and icon face. | Correct `smartphone` name, wrong host PUA routing. | P0 | Host |
| Bar activation (O2) | Primary click opens the attached panel once. | Primary press and release both reach `panel.open`; right release happens to open. | P0 | Host/plugin contract |
| Default device (O3) | A usable paired phone should be the default when one is available. | First reachable device can be unpaired; panel stops at pairing request. | P0 | Plugin |
| Panel width (O4) | 400px base, up to 525px for large device mockups. | Fixed 400px manifest; no resize call. | P1 | Plugin plus host acceptance |
| Selected device card | Type-sized PhoneDisplay, status, battery, network chips, tap-to-ping; connected state has no redundant status line. | PNG mockup, name, status text, progress meter, card ping; no offline dimming or selected-device chips. | P1 | Plugin; wire tone/image limits remain |
| Device switcher | All devices, selected styling, explicit swap affordance, pairing actions on each card. | Other devices only; switcher is always open; cards have actions but no selected styling. | P1 | Plugin |
| Action row | Centered action card with icon and label; ping is hidden when the display handles it. | Panel-level left row of icon-only controls; ping hiding is correct when the card is shown. | P1 | Plugin |
| Pairing controls | Icon plus label for request, accept, reject, cancel. | Actions exist; several are text-only inline buttons. | P1 | Plugin |
| Header | Active device icon in a tinted anchor, service name, counts, refresh/spinner. | Static `devices` icon, title/counts, static refresh. | P2 | Plugin; spinner needs host/wire support |
| Info grid | Leading icon, stacked label/value, two columns when space allows. | Stacked rows in one column; keyed battery/notification deltas work in code. | P2 | Plugin |
| Share composer | URI regex gate, text gate, close control. | View gates are present; submit routing only treats `http://` and `https://` as URLs, so `mailto:` and other valid schemes diverge. | P2 | Plugin |
| SMS composer | Single-line fields; send disabled until both values exist. | Single-line fields and gated send are present. | P3 | Plugin polish |
| Recent images | Mount-relative scan, thumbnails, open, drag-out, portal share, webp support. | Start browsing plus `mountPoint()`, png/jpg/jpeg scan, open/share buttons; no drag/portal/webp. The path was not observed live. | P2/P3 | Plugin diagnostics; host/protocol for richer share |
| Live delta/offline/settings | Rebinding updates open panel and settings survive restart. | Snapshot and keyed delta code exists; no live proof in this round. | P1 evidence gap | Plugin/live acceptance |
| Valent/MPRIS/i18n | DMS supports the additional backend, media section, and translations. | Explicitly deferred. | P2 decision / P4 | Product and host scope |

## Hallmark findings

The audit used the Hallmark audit criteria against `plugins/kdeconnect/view.go`, the host panel layout path, and the available tests. No paired panel image could be rendered locally; the owner saw only the unpaired state.

### Critical

1. **Content can clip below the fixed surface.** `PanelTree` builds an unpadded column containing header, always-open switcher, mockup, actions, info, recent images, and composer (`view.go:190-231,520-679`). The manifest is `400x520` (`manifest.json:12`), and the host lays the root into fixed bounds (`sysc-shell/internal/shell/panelhost.go:1074-1086`). Add a scrollable region or collapse secondary sections before adding more content.
2. **The primary icon fails as typography.** The PUA omission described in O1 makes the control read as a letter. Fix font routing before judging the visual language.

### Major

3. **Cards touch the panel edge.** The root has `Gap: 10` but no outer padding (`view.go:190-192`). Add a panel inset and retain a smaller card gap.
4. **The switcher dominates the first view.** It appears for every multi-device snapshot (`view.go:212-214`) and excludes the selected device (`view.go:425-435`). Collapse it behind an explicit device control and show the current device.
5. **Actions look like unlabeled icon tiles.** `actionButton` supplies an icon without text, size, padding, or tooltip (`view.go:559-591`). Recent-image controls repeat the pattern (`view.go:646-679`). Use consistent hit targets and visible labels for uncommon actions.
6. **The action and info sections have weak grouping.** The paired path appends an unlabeled row and a single-column info stack (`view.go:218-223,559-643`). Group them into one card and use a two-column info layout where it fits.
7. **The header does not anchor the current device.** It always paints `devices` (`view.go:234-268`). Use the selected device type icon and keep refresh separate.
8. **State cards lack a visual cue and direct next action.** `stateCard` and `unavailableCard` only contain text (`view.go:271-289`). Add an icon and a clear retry or pairing action.
9. **A stale selection can produce a blank body.** When devices exist but `SelectedID` names none, `PanelTree` returns after the header (`view.go:202-205`). Render a recovery state or guarantee a valid selection before publishing.
10. **The device card hides its primary action.** The whole card is a button named `Tap to ping ...`, but its visible children are only image, name, status, and meter (`view.go:520-545`). Add a visible ping cue or tooltip while keeping the large target.

### Minor

11. Spacing uses many unshared values across cards and rows (`view.go:191,254,274,284,295,318,410,427,597,637,655`). Consolidate to a small host-aligned scale.
12. Typography mixes `caption`, `title`, `headline`, and default body roles without a clear device-to-control ladder (`view.go:260-262,401-417,534-535`). Establish one role for device name, state, and action labels.
13. The recent grid has three fixed 96px cells and no filename context (`view.go:646-679`). Add a concise source caption or a clear open/share affordance and verify it at the chosen width.
14. Composer inputs rely on the host-rendered `Name` placeholder and have no persistent label or helper/error slot (`view.go:294-331`; sysc-shell `internal/render/paint.go:688-692`). Keep the placeholder path unless live typing shows that a persistent label is needed.

## Plan handoff notes

- Land selection, activation, and icon-routing fixes before any visual redesign.
- The selection change intentionally prioritizes a paired reachable device over DMS's raw first-reachable order; record that usability deviation in the follow-up tracker entry.
- Re-observe the laptop after those fixes. Record the selected ID, daemon order, glyph appearance, left-click behavior, and paired card before changing the paired layout.
- Treat the paired surface as unobserved until the owner confirms it on the laptop. Do not close live-gate work from static tests.
- Keep 400px as the base panel and use `panel.resize` for large mockups only if live layout measurements show the base clips or crowds content. Make resize best effort because the panel may close between publish and call.
- Exercise the abandoned matrix: settings round-trip, live `PanelDelta`, SFTP mount and image grid, open/share actions, offline recovery, and journal classification.
- The redesign plan must align follow-up closes with sysc-shell tracker work. Resolve or explicitly defer open dependencies such as `sysc-469` and `sysc-470` before closing the KDE Connect epic.
- The audit documents remain the review record; the implementation changes are in the follow-up diff above. No tracker state changed.

## Follow-up implementation status

The authorized follow-up applied the selection fallback, activation guard, and paired-panel hierarchy changes in the plugin. The panel now has a collapsed device chooser, stale-selection recovery, labelled action groups, retryable state cards, and a visible ping cue.

Bare-metal `kdeconnect-cli -l` reports `Pixel 8 Pro` as paired and reachable, followed by a second paired device. The local shell service was restarted with the rebuilt plugin and produced no new plugin startup errors. GUI click and paired-panel visual checks still need a manual desktop pass.
