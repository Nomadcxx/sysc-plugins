# KDE Connect live failure redesign implementation plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the KDE Connect pill and panel usable on the live laptop, then redesign the paired-device surface against the DMS reference with live acceptance evidence.

**Architecture:** Fix the three proven gates first: paired-device selection in the plugin service, single activation delivery for the bar pill, and host routing for the merged icon font range. Re-observe the real laptop before changing paired layout. Then make the paired panel readable at its base width, resize only for large device mockups, and exercise the untested live matrix.

**Tech Stack:** Go, sysc-shell plugin v1 wire, KDE Connect DBus, `kdeconnect-cli`, targeted Go tests, SSH live acceptance.

**Reference:** Audit report `docs/plans/2026-09-22-kdeconnect-live-failure-audit-report.md`; DMS parity `docs/plans/2026-09-19-kdeconnect-parity-audit-report.md`.

---

### Task 1: Prefer a paired reachable device

**Files:**
- Modify: `plugins/kdeconnect/service.go:1093-1111`
- Test: `plugins/kdeconnect/service_test.go` selection table near `:422-468`
- Test: `plugins/kdeconnect/service_test.go` snapshot selection coverage near `:250-298`

**Step 1: Write the failing test**

Add a case where `archPC` is reachable and unpaired before a reachable paired phone. Expect the paired phone ID. Keep cases for saved paired reachable, saved offline, first reachable fallback, and first device fallback.

**Step 2: Run the focused test**

Run: `go test ./plugins/kdeconnect -run 'TestResolveSelectionOrder|TestConnectDiscoversDevicesAndReadings' -count=1`

Expected: the new case fails because `resolveSelection` returns the first reachable unpaired ID.

**Step 3: Implement the smallest policy change**

Keep a valid saved choice first. Add a pass for the first `Paired && Reachable` device, then retain the existing first reachable and first device passes. This intentionally changes DMS's raw first-reachable order to keep the live panel usable; record the deviation. Do not save an automatic choice; `saveSelection` remains user-driven.

**Step 4: Run the focused test**

Run: `go test ./plugins/kdeconnect -run 'TestResolveSelectionOrder|TestConnectDiscoversDevicesAndReadings' -count=1`

Expected: PASS.

**Step 5: Run service proof**

Run: `go test ./plugins/kdeconnect -run 'Test(SavedSelection|DeviceRemoved|ConnectDiscovers)' -count=1`

Expected: PASS, with saved selection preserved and fallback re-evaluated after device removal.

**Step 6: Commit boundary**

Commit in sysc-plugins: `fix(kdeconnect): prefer paired reachable selection`.

---

### Task 2: Make the bar open once on primary click

**Files:**
- Modify: `cmd/sysc-plugin-kdeconnect/main.go:157-166`
- Test: `cmd/sysc-plugin-kdeconnect/main_test.go`
- Test: `/home/nomadx/sysc-shell/internal/shell/pluginhost_test.go` if the host delivery contract changes
- Modify: `/home/nomadx/sysc-shell/internal/shell/pluginhost.go:1009-1032` only if choosing the host-side contract fix

**Step 1: Write the failing plugin test**

Call `handleInput` with node `open` and `EventPointer`; assert it makes no panel call. Call it with node `open` and `EventActivate`; assert one `CallPanelOpen` with entry `panel`.

**Step 2: Run the focused test**

Run: `go test ./cmd/sysc-plugin-kdeconnect -run 'TestHandleInput.*Open' -count=1`

Expected: FAIL because the current handler ignores `m.Event`.

**Step 3: Implement the narrow fix**

Ignore `open` pointer events in the plugin and handle `EventActivate` once. Preserve panel button activation and text input submit behavior. If host maintainers choose to stop sending primary press events for activate-only bar controls instead, update the host test and keep the plugin event check as a defense at the trust boundary.

**Step 4: Run plugin and host proofs**

Run: `go test ./cmd/sysc-plugin-kdeconnect -run 'TestHandleInput.*Open' -count=1`

Expected: PASS with one open call.

Run in sysc-shell: `go test ./internal/shell -run 'TestPluginPrimaryMiddleSecondaryButtons|TestPluginHost.*Panel' -count=1`

Expected: PASS; primary click opens once, secondary behavior is explicit, and no test relies on a release toggling the panel closed.

**Step 5: Commit boundary**

Commit plugin and any coordinated host change separately:

- `fix(kdeconnect): ignore non-activation bar opens`
- `fix(shell): deliver plugin bar activation once` (only if host code changes)

---

### Task 3: Route the device icon range through the embedded face

**Files:**
- Modify: `/home/nomadx/sysc-shell/internal/render/fontmap.go:172-188`
- Test: `/home/nomadx/sysc-shell/internal/render/iconfont_test.go`
- Optional modify: `/home/nomadx/sysc-shell/internal/render/iconfont.go` to export one range bound if the font map should not duplicate constants

**Step 1: Write the failing routing test**

Resolve `IconByName("smartphone")`, build a system font map, and assert `FontMap.Face(rune, ...)` returns the project icon face rather than the primary text face. Check representative device and cellular glyphs, plus a normal letter.

**Step 2: Run the focused test**

Run in sysc-shell: `go test ./internal/render -run 'Test.*Device.*(Face|Glyph)|TestIcon.*' -count=1`

Expected: FAIL for the device rune because the old range router excludes `U+E031+`.

**Step 3: Implement the range ownership**

Add the device and cellular private-use range to `iconFaceFor` (or extend a single authoritative last bound). Keep the existing weather, battery, metric, recorder, notification, gauge, night, and detail ranges intact.

**Step 4: Run raster and font proof**

Run: `go test ./internal/render -run 'Test.*Device|Test.*Cellular|Test.*Font' -count=1`

Expected: PASS, including nonzero ink for every device and cellular glyph.

**Step 5: Commit boundary**

Commit in sysc-shell: `fix(render): route kdeconnect glyphs through icon face`.

---

### Task 4: Re-observe the live laptop before redesign

**Files:**
- No repository files
- Evidence goes into the review notes for the follow-up live gate; do not edit the owner’s laptop without approval.

**Step 1: Build from the fixed commits**

Run the existing build gates in each repository. Expected: clean build, tests, vet, format, and manifest validation.

**Step 2: Deploy only with owner approval**

Use the runbook’s backup, symlink, and restart procedure. Keep the laptop read-only for diagnostics until the owner authorizes deployment.

**Step 3: Record the four blocking checks**

On the laptop, record:

- `kdeconnect-cli -l` order, paired/reachable flags, plugin stderr, and saved state key.
- The pill glyph after the shell restart.
- One left click and one right click, including whether the panel remains open.
- The paired panel width and whether the device card, action row, and info rows clip.

Expected: the paired phone becomes the default, the pill reads as a phone, one left click opens the panel, and the paired UI becomes observable.

**Stop condition:** If selection still lands on an unpaired device, stop the visual redesign and fix the selection evidence first.

---

### Task 5: Redesign the paired panel hierarchy

**Files:**
- Modify: `plugins/kdeconnect/view.go:190-231,234-279,425-679`
- Test: `plugins/kdeconnect/view_test.go`
- Modify: `plugins/kdeconnect/manifest.json:12` only if live measurement proves the base width needs a new default

**Step 1: Write failing tree tests from the observed paired state**

Cover these invariants:

- A paired snapshot renders header, current-device card, grouped actions, info readings, and recent images in that order.
- A stale or empty `SelectedID` renders a recovery state with a device chooser instead of a blank body.
- A multi-device snapshot shows a compact current-device selector; opening the selector exposes every device and pairing action.
- The current device has a visible selected state.
- Action controls have a consistent host-sized hit target and a visible label or explicit tooltip.
- The device card’s ping action has a visible cue or tooltip; the large card target remains intact.
- Empty and unavailable states carry an icon and a direct retry or pairing action.
- Composer fields have visible labels or placeholder semantics while retaining accessible names.
- The tree validates for panel view.

**Step 2: Run the failing tree tests**

Run: `go test ./plugins/kdeconnect -run 'TestPanelTree|Test.*Switcher|Test.*State|Test.*Composer' -count=1`

Expected: FAIL on the current always-open switcher, icon-only action row, and text-only state cards.

**Step 3: Implement the smallest coherent layout**

Keep existing service and action IDs. In `view.go`:

- Add panel inset and a compact header anchored to the selected device type.
- Collapse the switcher behind one explicit control and retain all device cards when expanded.
- Group the selected device card and action row so the main action stays near the device mockup.
- Use a two-up info row only when the measured width permits it; keep a one-column fallback.
- Give uncommon actions visible labels; use the wire’s icon plus text conversion rather than inventing a new host abstraction.
- Add state icons and direct actions without duplicating refresh logic.
- Preserve the host-rendered `Name` placeholders for composer fields; add persistent labels only if the live paired review shows that typing removes necessary context.
- Keep recent images behind a bounded section and preserve the existing open/share IDs.

Do not add MPRIS, Valent, drag-out, portal sharing, or i18n in this slice. Record those parity decisions in the deviation ledger.

**Step 4: Run tree and validation proofs**

Run: `go test ./plugins/kdeconnect -run 'TestPanelTree|Test.*Switcher|Test.*State|Test.*Composer' -count=1`

Expected: PASS.

Run: `go test ./plugins/kdeconnect -run 'Test.*Validate|TestPanelDelta' -count=1`

Expected: PASS with no duplicate keys and keyed deltas preserved.

**Step 5: Commit boundary**

Commit: `feat(kdeconnect): clarify paired device panel hierarchy`.

---

### Task 6: Size the panel for the content

**Files:**
- Modify: `cmd/sysc-plugin-kdeconnect/main.go` near the publish loop
- Test: `cmd/sysc-plugin-kdeconnect/main_test.go`
- Modify: `plugins/kdeconnect/manifest.json` only if the base default changes

**Step 1: Write the failing width test**

Test a width policy with 400px for phone or no mockup and 525px for tablet, desktop, and laptop when the device card is shown. Test that disabling the device card returns to 400px.

**Step 2: Run the focused test**

Run: `go test ./cmd/sysc-plugin-kdeconnect -run 'TestPanelWidth' -count=1`

Expected: FAIL because no resize call or width policy exists.

**Step 3: Implement best-effort resize**

After publishing an open panel, send `CallPanelResize` only when the desired width changes. Use 400px as the base and 525px for large mockups. Ignore a resize failure caused by a panel close race; the next open will publish the correct size. Keep host bounds 64–4096 as the validation boundary.

**Step 4: Run the focused test**

Run: `go test ./cmd/sysc-plugin-kdeconnect -run 'TestPanelWidth' -count=1`

Expected: PASS, with no repeated resize calls for an unchanged width.

**Step 5: Commit boundary**

Commit: `feat(kdeconnect): resize panel for large device mockups`.

---

### Task 7: Re-test the full live matrix and close only proven work

**Files:**
- No production files unless a live failure maps to a named task above.
- Evidence: the live completion handover in the sysc-shell tracker workflow.

**Step 1: Run local gates**

Run in sysc-plugins: `make build test vet fmt validate`.

Expected: all targets pass with a clean diff apart from the intended commits.

Run in sysc-shell: `make build test vet fmt validate`.

Expected: all targets pass without touching the owner’s WIP files.

**Step 2: Exercise the laptop matrix**

After the owner approves deployment, run the existing live round and record one evidence line per row:

1. pill and panel open;
2. paired device card and mockup;
3. tap-to-ping and ring;
4. action gating with and without the device card;
5. clipboard, URL, text, file, and SMS sends;
6. settings round-trip across restart;
7. live battery and notification delta while the panel stays open;
8. SFTP mount, recent grid, open, and share;
9. offline transition and recovery;
10. journal classification.

Expected: every row is pass or has a named, accepted deviation. A row skipped because the phone or mount was unavailable remains unverified.

**Step 3: Reconcile deviations and tracker dependencies**

Keep the report’s explicit deferrals for Valent, MPRIS, drag/portal sharing, webp, keyboard shortcuts, and i18n. Resolve or explicitly defer `sysc-469` and `sysc-470` before closing the KDE Connect epic; do not close an issue from static evidence alone.

**Step 4: Final review checkpoint**

Review the audit report, this plan, local gate output, and live evidence. The commissioning session may then land these two documents and delete the handover according to its register rule.
