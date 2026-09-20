# KDE Connect gap-fill plan (Plan D) — independent audit report

Audit of `docs/plans/2026-09-21-kdeconnect-gap-fill-plan.md` before execution, commissioned by
`docs/plans/2026-09-21-kdeconnect-gap-fill-audit-handover.md`. Read-only: no code, commits, pushes,
or tracker mutations. This report is the only file written.

Audited revisions:

- sysc-plugins `main` @ `01d9657`; plugin source in worktree `.worktrees/feat/kdeconnect` @ `2d7b0d2`.
- sysc-shell `main` @ `42d4736`; wire implementation in worktree `.worktrees/feature/wire-minor-5` @ `68c0f8d`;
  glyph branch `feature/kdeconnect-icons` @ `3c9dd81`.

---

## 1. Verdict

**Executable after named amendments.** The plan's architecture, task decomposition, and host-contract
assumptions are sound and match the wire implementation on `feature/wire-minor-5` almost everywhere.
Three amendments are blocking, not cosmetic:

1. **Task 0 must also merge `feature/kdeconnect-icons` into sysc-shell main.** The kdeconnect glyph
   catalogue (`smartphone`, `battery-*`, `phone-in-talk`, `folder-open`, `content-paste`, `share`,
   `sms`, `notifications-active`, `refresh`, network glyphs) exists only on that branch. `iconNode`
   (`internal/plugin/view.go:232-241`) errors on unknown names, so with only `feature/wire-minor-5`
   merged **every kdeconnect view conversion fails host-side** — including the Phase 0 views already
   on `feat/kdeconnect`. Task 0's gates would still pass (plugin tests do not host-convert), producing
   a green-but-broken state.
2. **Task 7's tint mapping cannot pass the wire validator.** `KindProgress` cannot carry `Fill`
   (`fillAllowed` = container/button), and `warning`/`success` are not in `knownFills`. The fallback
   (tinting the button's own `Fill`) fails for the same reason. The wire offers `Tone` error/accent
   only; the three-tint DMS mapping must collapse to two and be recorded as a deviation.
3. **Task 2's routing premise is false.** `handleInput` has no `ping` (or `ring`/`browse`/`clipboard`)
   case; the action-row ping button is dead today. Task 2 must add routing for both `device-ping` and
   `ping`, not "exactly as existing `ping` maps".

Everything else is precision work: wrong file:line citations in Task 3, a misattributed rationale in
Task 2, an unstated failure-tolerance in Task 5, and an incomplete bd close list in Task 8. No task is
blocked in principle; none needs redesign.

---

## 2. Per-task findings

### Task 0 — prereqs: **AMEND (blocking)**

- Merge order (feat/kdeconnect → sysc-plugins main; feature/wire-minor-5 → sysc-shell main; pin bump;
  manifest minor 6) is correct and matches the verified branch state.
- `go.mod` pin is `v0.0.0-20260916042624-cc684ee8c131` (minor 2) as claimed; `golang.org/x/image` is
  absent and Task 3 adds it.
- Manifest is `{"major":1,"minor":2}` as claimed; minor is decorative (`internal/plugin/manifest.go:379-383`
  rejects only `Major != 1` and negative minor; pomodoro audit §2), so Step 3 is hygiene, not a
  compatibility requirement. No task treats minor as load-bearing (F10 confirmed).
- **Missing prerequisite:** `feature/kdeconnect-icons` @ `3c9dd81` is not an ancestor of `main`,
  `feature/wire-minor-5`, or `0b4b238`. `git show <ref>:internal/render/iconfont.go | grep -c
  'phone-in-talk\|folder-open\|battery-charging\|notifications-active'` returns 0 for main, `0b4b238`,
  and `68c0f8d`, and 10 for `3c9dd81`. The plugin uses those names throughout `view.go`
  (`deviceIcon`, `actionRowTree`, `batteryIconName`, `networkStrengthIcon`, `networkTypeIcon`,
  `BarTree`, `composerHeader`). Without the merge, `iconNode` returns
  `no icon named %q; this shell has %v and %v` and the whole view fails.
- Handover claim "All commits below `0b4b238` are on sysc-shell main history" is **refuted**:
  `git merge-base main feature/wire-minor-5` = `ec98abf`; `f9f91ce` and `348fd12` are branch-only.
  Harmless for the plan (the merge brings them), but the handover's state-of-world is wrong.
- "Nothing anywhere is pushed" verified (no remote branch for `feature/wire-minor-5`).

### Task 1 (sysc-478) icon-led buttons: **SOUND (simplify)**

- F1 confirmed: the converter's button case (`internal/plugin/view.go`, button branch) synthesizes
  `[KindIcon, KindText]` children and clears `out.Text` when both `Icon` and `Text` are set. The
  plan's Step 2 conditional ("first check whether host co-renders…") is already answered: set the
  `Icon` field; no explicit children needed. `TestHitRoutesButtonChildrenToTheButton`
  (`internal/ui/layout_test.go:746`) exists as cited.
- Icons are available: `check`, `close`, `link` in the material catalogue on main; `send` added by
  `feature/wire-minor-5` (`materialfont.go`). No dependency on the glyph branch.
- `gatedButton(id, text, name string, enabled bool)` has **four call sites** (`share-url-send`,
  `share-text-send`, `share-file-send`, `sms-send`); the signature change must update all four.
- `pairingCard` buttons (`pair-cancel`, `pair-accept`, `pair-reject`) and `unpairedCard` (`pair`) are
  built inline, not via `gatedButton`; the plan's file list is right.

### Task 2 (sysc-468) tap-to-ping: **AMEND (blocking)**

- `deviceCardTree` (`view.go:456`) is a `KindColumn` with `Fill:"card"`, `Radius:12`, `Padding:14`,
  children icon/name/status/battery-progress — wrapping it in a button is structurally valid; button
  children may be non-interactive leaves, and the implementation is looser than Design A D7 (only
  interactive kinds are rejected, `plugin/v1/node.go:648-655`).
- `actionRowTree` (`view.go:482`) currently always includes ping; the `!settings.ShowDeviceCard`
  condition is correct inside `PanelTree`'s default branch (card shown iff `ShowDeviceCard`).
- **Routing does not exist.** `cmd/sysc-plugin-kdeconnect/main.go` `handleInput` (:159-247) has cases
  for open/refresh/pair/accept/reject/share/sms/composers only — no `ping`, `ring`, `browse`, or
  `clipboard`. `git log -S ActionPing` / `-S ActionRing` on that file is empty. Step 3's "exactly as
  existing `ping` button ID maps" is false; add routing for `device-ping` **and** `ping`.
- **PanelDelta rationale misattributed.** `PanelDelta` (`view.go:687-713`) patches only
  `battery-progress`, `info-battery`, `info-notifications`; host `ApplyPatch`
  (`internal/plugin/view.go:80-130`) matches replacements by `Key` only (`hasKey`/`replaceKey`/
  `firstDupKey`), never by ID. The distinct-ID argument is still right, but for input routing: the
  host stamps `plugin:<view>:<nodeID>` from `Action = ID`, so duplicate IDs make input events
  indistinguishable.
- F3 **refuted**: the test exists. `TestActionRowGating` (`view_test.go:312`) asserts the action row
  has exactly 6 children with `Children[0].ID == "ring"` (four occurrences). Removing ping makes it 5
  and the predicate fails; the plan's wording "asserted ping always visible" is imprecise but the
  update is real and correctly ordered.

### Task 3 (sysc-445) recent images grid: **AMEND (minor)**

- Settings/Snapshot additions are consistent with `service.go` (`Snapshot:61`, `Settings:71`,
  `DefaultSettings:78`); `ActionKind` consts exist for the share path.
- Scan filter `png/jpg/jpeg` is a subset of `decodableExtensions` (`internal/icons/theme.go:29` =
  png/xpm/jpg/jpeg/gif/bmp); `.webp` exclusion is correct and already in the deviation ledger (F4
  confirmed).
- Thumbnail plan (stdlib png/jpeg decode, `x/image/draw.CatmullRom`, sha1(src|mtime) cache) is sound;
  `x/image` is not yet in `go.mod`, as the plan says.
- **Citation wrong (F5).** `daemon.go:37` is the `sftpIface` constant, not `startBrowsing`;
  `startBrowsing` is dispatched at `service.go:585` (`ActionBrowse`). The mount point **is**
  obtainable, but as a **method call** `mountPoint()` on `…device.sftp`, not a property
  (`docs/plans/2026-09-19-kdeconnect-phone-connect.md:61`, `:258`). No `.go` file calls it today.
- **Internal inconsistency:** `scanRecentImages(root string, max int, sub bool)` has no mount-point
  parameter, yet Step 2 says the test "rejects root not under SFTP mount point". Either add the mount
  point to the signature or move containment validation to the service layer (Step 6).
- Step 7's icons `folder-open` and `share` exist only on `feature/kdeconnect-icons`; covered by the
  Task 0 amendment. `folder_open` (underscore) is the material name and is a different glyph.
- `KindImage` with `ImageSize:96` and an absolute cache path satisfies the minor-5 validator; image
  nodes may carry `ID` (no validator restriction).

### Task 4 (sysc-445) device mockup: **SOUND**

- `Background:true` requires explicit `ImageW`+`ImageH` (`plugin/v1/node.go` minorFive) — the plan
  states this. `Shape:"card"` is in `knownShapes` (`node.go:317-320`); shape validation
  (`node.go:569-571`) does not restrict image nodes.
- Asset resolution `os.Executable() → dir → ../assets/` matches the manifest exec path
  (`bin/sysc-plugin-kdeconnect` → `plugins/kdeconnect/assets/`); precedent
  `plugins/wallpaper-depth/service.go:94`. F6 is addressed: the plan explicitly prescribes a package
  var and says `os.Executable()`-relative resolution does not work under `go test`.
- Mockup is a non-interactive leaf, so it composes inside Task 2's ping button; battery progress is
  also non-interactive. Valid.
- Fallback `deviceIcon` requires the glyph branch (Task 0 amendment).

### Task 5 dynamic panel width: **AMEND (minor)**

- `panelWidth` 400/525 matches the manifest panel width (400) and host bounds 64–4096
  (`internal/plugin/hostcall.go:291-300`); reply semantics ("requested", compositor completes) match
  Design B D3 and `resizePanel` (`internal/shell/pluginhost.go:897-917`).
- The publish loop only reaches views in the open-views map, so "send after panel publish" naturally
  fires only while the panel is open. A close race remains: `resizePanel` errors with
  `no open panel to resize` / `panel surface is not open`. The plan does not say to tolerate that
  (F9 confirmed as a gap). Add best-effort wording.

### Task 6 composer focus-on-open: **SOUND**

- `share-text` and `sms-number` exist as text-input IDs (`view.go` composer trees); the converter
  stamps `Action` from the ID and `Focusable=true`; disabled inputs clear `Action` (F7 confirmed) but
  these inputs are never disabled.
- `view.focus` host contract matches Design B D6/D7 (`focusPanelView`, `pluginhost.go:924-960`;
  stamped action `plugin:<view>:<nodeID>`, `pluginwidget.go:15`). Best-effort failure handling is
  stated in the plan.

### Task 7 (sysc-447) charging fill + animated progress: **AMEND (blocking)**

- `batteryProgressNode` (`view.go:469`) already carries `Key:"battery-progress"`; adding
  `Animate:true` satisfies minor 6 (`node.go` minorSix requires a key when animate is set).
- **The tint mapping is invalid.** `node.go` minorTwo (`:536-543`) requires `Fill ∈ knownFills` and
  `fillAllowed(kind)`; `fillAllowed` = container/button, so `KindProgress{Fill:…}` is rejected
  ("progress cannot carry a fill"). `knownFills` (`:306-310`) has no `warning`/`success`, so the
  fallback (tinting the button's own `Fill`) is rejected too; only `error` is valid. `paint.go`
  `KindMeter` (`:284-296`) ignores `Fill` and uses `style.Track` + accent/error by `Tone`.
- Valid amendment: `Tone: error` when level < 20, else `Tone: accent` (or `normal`); record the
  warning/success collapse as a deviation. The structural composition (progress child + icon/label
  row inside the pill button) is fine — `paintChrome` paints children instead of the label (F2
  confirmed), and containers are legal button children.
- Pill icons require the glyph branch (Task 0 amendment).

### Task 8 (sysc-430) live gate + completion handover: **AMEND (minor)**

- `SYSC_KDECONNECT_LIVE=1` precedent exists (`e3edef9`); gates and handover shape are sound.
- **Close list is incomplete.** `sysc-469` (pairing toast dedupe vs DMS refire) and `sysc-470`
  (strength-3 icon mapping) are open and are dependencies of epic `sysc-420`. Plan D does not address
  either. Closing `sysc-420` while they are open is premature; resolve, defer explicitly, or record
  as deviations first.
- `sysc-446` (image-node prerequisite) is closed, so Task 3's stated dependency is satisfied.

---

## 3. Claims table

| # | Claim (source) | Status | Evidence / settling command |
|---|---|---|---|
| 1 | feat/kdeconnect carries Phase 0 (plan Task 0) | **verified** | `view.go` headerTree:171, switcherTree:359, actionRowTree:482, infoRowsTree:510, stateCard:208, unavailableCard:218; tests view_test.go:103/127/139/312/357; commits 7ae8624/1fe8493/85adf0d |
| 2 | Handover: Phase 0 "Not implemented" | **refuted** | same as #1; only `IsRefreshing` view feature absent (grep empty); busy via `PluginStatus StatusBusy` in main.go |
| 3 | feature/wire-minor-5 carries minors 5+6, panel.resize, view.focus | **verified** | `plugin/v1/node.go` minorFive:603-661, minorSix:666-676; `message.go:247-248,382,390`; `supervisor.go:201` = {1,3},{1,4},{1,5},{1,6} |
| 4 | Pin is minor 2 `v0.0.0-20260916042624-cc684ee8c131` | **verified** | `go.mod` |
| 5 | Manifest protocol {major:1,minor:2}; minor decorative | **verified** | `manifest.json`; `internal/plugin/manifest.go:379-383`; pomodoro audit §2 |
| 6 | KindImage fields + validator rules | **verified** | `node.go` minorFive:603-661 (absolute path, one box form, background needs explicit box) |
| 7 | `Animate` requires Key, progress/gauge only | **verified** | `node.go` minorSix:666-676 |
| 8 | Button children rule | **verified (looser than Design A D7)** | `node.go:648-655` rejects interactive children only; containers allowed |
| 9 | paintChrome paints children instead of label | **verified** | `internal/render/paint.go` ~1126-1140 |
| 10 | Converter synthesizes button icon+text children, clears Text | **verified** | `internal/plugin/view.go` button branch |
| 11 | `TestHitRoutesButtonChildrenToTheButton` exists | **verified** | `internal/ui/layout_test.go:746` |
| 12 | FileResolver decodes png/xpm/jpg/jpeg/gif/bmp; no webp | **verified** | `internal/icons/theme.go:29,80-88` |
| 13 | Text input Action stamped from ID; disabled clears | **verified** | `internal/plugin/view.go` text-input branch |
| 14 | panel.resize bounds 64–4096, reply "requested" | **verified** | `hostcall.go:291-300`; `pluginhost.go:897-917`; Design B D2/D3 |
| 15 | Stamped action `plugin:<viewID>:<nodeID>` | **verified** | `pluginwidget.go:15`; `pluginhost.go:376`; `focusPanelView:924-960` |
| 16 | Reduced motion collapses animation | **verified** | `internal/shell/animation.go:183-190` |
| 17 | Task 3: "daemon.go sftp seam beside startBrowsing :37" | **refuted** | `daemon.go:37` is `sftpIface`; `startBrowsing` at `service.go:585` |
| 18 | Task 3: "mount-point property" | **refuted (wording)** | `mountPoint()` is a method (`2026-09-19-kdeconnect-phone-connect.md:61,258`) |
| 19 | Task 2: "exactly as existing ping button ID maps" | **refuted** | `main.go` handleInput:159-247 has no ping/ring/browse/clipboard; `git log -S ActionPing` empty |
| 20 | Task 2: "Phase 0 view test asserted ping always visible" | **partially refuted** | `TestActionRowGating` view_test.go:312 asserts 6 children, `Children[0].ID=="ring"` |
| 21 | Task 2: PanelDelta ID-collision argument | **refuted** | `ApplyPatch` matches `Key` only (`internal/plugin/view.go:80-130,149-202`); PanelDelta patches battery-progress/info-battery/info-notifications |
| 22 | Task 7: Key "battery-progress" already present | **verified** | `view.go:469` |
| 23 | Task 7: tint error/warning/success | **refuted** | `node.go:306-310,322-324,536-543`; `paint.go:284-296` |
| 24 | Task 3: icons folder-open, share | **refuted as written** | only on `feature/kdeconnect-icons`; material name is `folder_open`; no `share` elsewhere |
| 25 | Task 3: scan filter png/jpg/jpeg, webp excluded | **verified** | subset of `theme.go:29` |
| 26 | Task 4: Background needs explicit box; Shape card valid | **verified** | `node.go` minorFive; `knownShapes:317-320` |
| 27 | Task 4: package var for asset dir under go test | **verified (plan prescribes)** | plan Task 4 Step 2; precedent `plugins/wallpaper-depth/service.go:94` |
| 28 | Task 5: 400/525; manifest panel 400 | **verified** | `manifest.json`; plan Task 5 |
| 29 | Task 6: share-text / sms-number IDs | **verified** | `view.go` shareComposerTree:229, smsComposerTree:252 |
| 30 | Task 8: close list 478/468/445/447/430/420 | **partially refuted** | all exist/open, but 469/470 open and epic-420 dependencies |
| 31 | Makefile targets build/test/vet/fmt/validate | **verified** | Makefile:15,30,33,36,39 |
| 32 | bd sysc-478/468/445/447/430 + 420 exist and open | **verified** | `bd show` in sysc-shell |
| 33 | sysc-446 closed (Task 3 dependency) | **verified** | `bd show sysc-446` |
| 34 | Task 0 merge order | **amend** | icon branch missing (see §5.1) |
| 35 | Task 1 icons check/close/link/send | **verified** | material catalogue + wire-minor-5 `send` |
| 36 | Task 3 scanRecentImages rejects root outside mount | **unverified / inconsistent** | signature has no mount point; evidence would be the amended signature or service-layer check |
| 37 | feature/kdeconnect-icons gates green | **unverified** | run `make build test vet fmt validate` on `3c9dd81` |
| 38 | Handover: commits below 0b4b238 on sysc-shell main | **refuted** | `git merge-base main feature/wire-minor-5` = `ec98abf` |
| 39 | Nothing pushed | **verified** | no remote branch for `feature/wire-minor-5` |
| 40 | Designs A/B/C owner-approved on sysc-shell main | **verified** | `42d4736` and parents |

---

## 4. Primed-findings dispositions (F1–F10)

| # | Disposition | Note |
|---|---|---|
| F1 | **Confirmed** | Converter synthesizes `[KindIcon,KindText]` and clears `Text`; Task 1 collapses to setting `Icon`. |
| F2 | **Confirmed** | `paintChrome` paints children instead of the label; Task 7's composition is structurally fine, its tint is not. |
| F3 | **Refuted** | The test exists (`TestActionRowGating`, view_test.go:312); it must be updated, but there is no ordering problem. |
| F4 | **Confirmed** | `decodableExtensions` has no webp; the plan's filter is a correct subset. |
| F5 | **Partially refuted** | A mount point is obtainable, but via `mountPoint()` (method), and the `daemon.go:37` citation is wrong (`service.go:585`). |
| F6 | **Addressed** | Plan prescribes a package var and says `os.Executable()` fails under `go test`. |
| F7 | **Confirmed** | Disabled inputs clear `Action`; the composer targets are not disabled. |
| F8 | **Confirmed** | `supervisor.go:201` advertises `{1,5}` and `{1,6}`. |
| F9 | **Confirmed (gap)** | `resizePanel` errors when no panel is open; the plan does not state failure tolerance. |
| F10 | **Confirmed** | Manifest minor is decorative; no task treats it as load-bearing. |

---

## 5. New findings, worst first

1. **BLOCKING — Task 0 omits the glyph branch.** `feature/kdeconnect-icons` @ `3c9dd81` is the only
   ref carrying the kdeconnect glyphs; main, `0b4b238`, and `68c0f8d` have none. The plugin uses them
   in every view. Merging only `feature/wire-minor-5` leaves `iconNode` rejecting every kdeconnect
   view. Task 0's gates would still pass, masking the breakage.
2. **BLOCKING — Task 7's tints are invalid wire values.** Progress cannot carry `Fill`; `warning` and
   `success` are not known fills; the fallback is equally invalid. Use `Tone` error/accent and record
   the deviation.
3. **BLOCKING — Task 2's routing does not exist.** No `ping`/`ring`/`browse`/`clipboard` cases in
   `handleInput`; add `device-ping` and `ping`.
4. **Task 8's bd close list is incomplete.** `sysc-469` and `sysc-470` are open epic-420
   dependencies and are outside Plan D's scope; closing `sysc-420` is premature.
5. **Task 3's SFTP seam citation and wording are wrong.** `daemon.go:37` is the interface constant;
   `startBrowsing` is `service.go:585`; the mount point is a `mountPoint()` method call.
6. **Task 3's scan signature cannot enforce mount containment.** Add the mount point to the signature
   or validate in the service layer.
7. **Task 2's PanelDelta rationale is misattributed.** Patches match `Key`, not `ID`; the distinct-ID
   reason is input-event routing.
8. **Task 5 lacks best-effort failure tolerance.** A close race can make `panel.resize` fail.
9. **Handover's Phase 0 claim is wrong** (handover document, not Plan D): Phase 0 is implemented on
   `feat/kdeconnect`; only the `IsRefreshing` view feature is absent.
10. **Handover's branch-history claim is wrong:** `f9f91ce`/`348fd12` are not on sysc-shell main.
11. **Task 1's conditional is already answered** (F1); simplify to setting `Icon`.
12. **Task 3/4/7 icon dependencies** are subsumed by finding 1.

---

## 6. Recommended amendments (diff-level wording; Plan D itself is not edited)

**Task 0, Step 2** — after the wire-minor-5 merge, add:

> Also merge `feature/kdeconnect-icons` (`3c9dd81`) into sysc-shell main. It carries the kdeconnect
> glyph catalogue (`smartphone`, `phonelink-off`, `tablet`, `laptop`, `desktop-windows`, `tv`,
> `devices`, `battery-0..6`, `battery-charging-0..6`, `battery-critical`, `phone-in-talk`,
> `folder-open`, `content-paste`, `share`, `sms`, `notifications-active`, `refresh`, `network`,
> `5g`/`4g`/`3g`/`g-mobiledata`, `signal-cellular-*`). Without it `iconNode` rejects every kdeconnect
> view, including the Phase 0 views already on `feat/kdeconnect`.

**Task 1, Step 2** — replace the conditional with:

> Set `Icon` on the button alongside `Text`. The converter already synthesizes `[KindIcon, KindText]`
> children and clears `Text` when both are present (`internal/plugin/view.go`, button branch), so no
> explicit children are needed. Extend `gatedButton` to `(id, icon, text, name string, enabled bool)`
> and update its four call sites.

**Task 2, Step 3** — replace "exactly as existing `ping` button ID maps" with:

> Add routing for `device-ping` **and** `ping` (no ping routing exists today; `handleInput` has no
> ping/ring/browse/clipboard cases). Map both to `kdeconnect.Action{Kind: ActionPing}`.

**Task 2, Step 3 rationale** — replace the PanelDelta sentence with:

> The distinct ID keeps input events distinguishable: the host stamps `plugin:<view>:<nodeID>` from
> `Action = ID`, so duplicate IDs would make the two buttons indistinguishable. (`PanelDelta`
> replacements match `Key`, not `ID`.)

**Task 2, Step 4** — replace "Update the Phase 0 view test that asserted ping always visible" with:

> Update `TestActionRowGating` (`view_test.go:312`), which asserts the action row has exactly six
> children with `Children[0].ID == "ring"`; with ping hidden the row has five.

**Task 3, Step 6** — replace "extend daemon.go sftp seam beside startBrowsing :37 — mount call +
mount-point property" with:

> Extend the SFTP seam (`service.go:585`, `ActionBrowse` → `sftpIface.startBrowsing`) with a
> `mountPoint()` call on `…device.sftp` (a method, not a property;
> `docs/plans/2026-09-19-kdeconnect-phone-connect.md:61,258`).

**Task 3, Step 2** — either change the signature to
`scanRecentImages(root, mountPoint string, max int, sub bool)` or move the "root under mount point"
check into the service layer; the current signature cannot perform it.

**Task 3, Step 7** — note that `folder-open` and `share` require the Task 0 glyph merge; the material
`folder_open` is a different glyph.

**Task 5, Step 2** — add:

> Treat the resize call as best-effort: ignore a failure reply (the panel can close between publish
> and call; `resizePanel` errors when no panel is open).

**Task 7, Step 2** — replace the tint mapping with:

> Set `Tone: error` when level < 20, else `Tone: accent` (the wire has no warning/success fills or
> tones, and progress cannot carry `Fill`). If the child layout fights the pill, fall back to tinting
> the button's own `Fill` with `error`/`accent` only. Record the warning/success collapse as a
> deviation.

**Task 8, Step 4** — add:

> Resolve or explicitly defer `sysc-469` and `sysc-470` (open epic-420 dependencies) before closing
> `sysc-420`.

---

## 7. bd alignment and deviation-ledger review

**bd alignment.** All issues named by the plan exist and are open: `sysc-478`, `sysc-468`,
`sysc-445`, `sysc-447`, `sysc-430`, epic `sysc-420`. `sysc-446` (image-node prerequisite) is closed,
satisfying Task 3's dependency. `sysc-464`–`467` and `471` are closed. **Gap:** `sysc-469` (pairing
toast dedupe) and `sysc-470` (strength-3 icon) remain open and are epic-420 dependencies; Plan D
neither addresses nor defers them, and Task 8's close list omits them. The Phase 0 issues
`sysc-472`–`477` are open although the work is implemented on `feat/kdeconnect` — tracker hygiene,
not a plan defect.

**Deviation-ledger review.** The ledger covers the parity report's gaps 13–18: webp exclusion,
drag-out/portal share, per-device settings, offline dimming, warning tone, host-owned chrome
(header border, hover, dialog animation), and the out-of-scope items (MPRIS, Valent, shortcuts,
i18n). Gap 12 (InfoRow/state-card copy) is not listed but was fixed by Phase 0 (`1fe8493`). Two
additions are needed: the Task 7 warning/success tint collapse (new, forced by the wire), and an
explicit disposition for `sysc-469`/`sysc-470`. The `.webp` entry is accurate and matches
`decodableExtensions`.

---

## 8. Go/no-go for Task 0's merge order

**GO, with the icon-branch amendment.** Recommended order:

1. Merge `feat/kdeconnect` into sysc-plugins main; cut `feat/kdeconnect-gap-fill`. (No shell
   dependency; plugin tests pass against the current pin.)
2. Merge `feature/wire-minor-5` **and** `feature/kdeconnect-icons` into sysc-shell main. The icon
   merge is a hard prerequisite for the plugin to function at all; without it Task 0's green gates
   are misleading.
3. Bump the sysc-plugins pin to the sysc-shell merge commit; `go mod tidy`.
4. Set the manifest protocol to `{"major":1,"minor":6}` (hygiene; minor is decorative).
5. Run `make build test vet fmt validate` with no source changes.

No-go only if the icon branch is excluded. With it, Task 0 is safe to execute and Tasks 1–8 follow
in order; the Task 7 and Task 2 amendments should land before their respective commits.

---

## Files read

**sysc-plugins** (`/home/nomadx/sysc-plugins`, worktree `.worktrees/feat/kdeconnect`):

- `docs/plans/2026-09-21-kdeconnect-gap-fill-audit-handover.md`
- `docs/plans/2026-09-21-kdeconnect-gap-fill-plan.md`
- `docs/plans/2026-09-19-kdeconnect-parity-audit-report.md`
- `docs/plans/2026-09-19-kdeconnect-phone-connect.md`
- `docs/plans/2026-09-20-pomodoro-audit.md`
- `Makefile`
- `go.mod`
- `plugins/kdeconnect/manifest.json`
- `plugins/kdeconnect/view.go`
- `plugins/kdeconnect/view_test.go`
- `plugins/kdeconnect/service.go`
- `plugins/kdeconnect/service_test.go`
- `plugins/kdeconnect/service_live_test.go`
- `plugins/kdeconnect/daemon.go`
- `cmd/sysc-plugin-kdeconnect/main.go`
- `cmd/sysc-plugin-kdeconnect/main_test.go`
- `plugins/wallpaper-depth/service.go` (grep)

**sysc-shell** (`/home/nomadx/sysc-shell`, worktree `.worktrees/feature/wire-minor-5`, refs `main`,
`0b4b238`, `3c9dd81`):

- `docs/plans/2026-09-21-wire-minor-5-presentation-design.md`
- `docs/plans/2026-09-21-panel-capabilities-design.md`
- `docs/plans/2026-09-21-declarative-value-animation-design.md`
- `plugin/v1/node.go`
- `plugin/v1/message.go`
- `internal/plugin/supervisor.go`
- `internal/plugin/hostcall.go`
- `internal/plugin/view.go`
- `internal/plugin/manifest.go` (grep)
- `internal/render/paint.go`
- `internal/render/materialfont.go`
- `internal/render/materialfont_test.go`
- `internal/render/iconfont.go`
- `internal/render/iconfont_test.go`
- `internal/icons/theme.go`
- `internal/shell/pluginhost.go`
- `internal/shell/pluginwidget.go`
- `internal/shell/animation.go`
- `internal/shell/bar.go` (grep)
- `internal/ui/animkey.go`
- `internal/ui/tree.go`
- `internal/ui/layout_test.go` (grep)
