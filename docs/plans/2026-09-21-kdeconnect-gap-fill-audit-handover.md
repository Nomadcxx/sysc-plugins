# Handover: Plan D audit commission

Date: 2026-09-21
From: the agent that designed and implemented the plugin wire minors and wrote Plan D
To: the auditing agent taking over
Repos: `/home/nomadx/sysc-plugins` (main @ `df4f892`), `/home/nomadx/sysc-shell` (main checkout on `feat/plugin-material-icons`; implementation branch `feature/wire-minor-5`)

You are commissioned to audit Plan D — `docs/plans/2026-09-21-kdeconnect-gap-fill-plan.md` in
sysc-plugins — before anyone executes it, and to produce a comprehensive audit report. Plan D was
written in one session against facts gathered in the same session; an author auditing their own
plan is worthless, so every claim in this handover and in Plan D is something you must re-verify
against the code yourself. Do not trust this document's claims — verify everything.

**Your only deliverable is the report.** Write it to
`/home/nomadx/sysc-plugins/docs/plans/2026-09-21-kdeconnect-gap-fill-audit-report.md`.
Do NOT commit it — the commissioning session reviews and lands it. Change no other file.

---

## 1. State of the world (what exists right now)

The overall effort is governed by `~/.commandcode/plans/kdeconnect-modern-panel-infrastructure.md`.
Its arc: give the plugin wire the vocabulary the KDE Connect parity gaps need (Designs A/B/C on
sysc-shell), then fill the plugin's gaps using that vocabulary (Plan D on sysc-plugins).

| Artifact | Where | State |
|---|---|---|
| Design A doc (wire minor 5: image/stroke/button children) | sysc-shell `main`, `docs/plans/2026-09-21-wire-minor-5-presentation-design.md` | Owner-approved |
| Design B doc (`panel.resize` + `view.focus`) | sysc-shell `main`, `docs/plans/2026-09-21-panel-capabilities-design.md` | Owner-approved |
| Design C doc (`animate` flag, minor 6) | sysc-shell `main`, `docs/plans/2026-09-21-declarative-value-animation-design.md` | Owner-approved |
| Designs A/B/C implementation | sysc-shell branch `feature/wire-minor-5` | Committed, gates green, **not merged, not pushed** |
| Plan D (this audit's subject) | sysc-plugins `main` @ `df4f892`, `docs/plans/2026-09-21-kdeconnect-gap-fill-plan.md` | Written, **not executed** |
| Phase 0 (header chip, switcher, action row, info grid, centred states, IsRefreshing) | bd sysc-472/473/475/476/477 + plugin half of 474 | **Not implemented** — Plan D builds on Phase-0-shaped views |
| KDE Connect plugin source | sysc-plugins worktree `.worktrees/feat/kdeconnect` (tip `2d7b0d2`) — `main` has **no** `plugins/kdeconnect` | Awaiting merge |
| Parity audit report (sysc-431–478 evidence) | `feat/kdeconnect` only, `docs/plans/2026-09-19-kdeconnect-parity-audit-report.md` | Awaiting merge |

Branch stack on `feature/wire-minor-5` (worktree `/home/nomadx/sysc-shell/.worktrees/feature/wire-minor-5`):

| Commit | Subject |
|---|---|
| `68c0f8d` | fix(plugin): offer minor six in the supervisor handshake |
| `35e8a35` | feat(plugin): add declarative value animation on minor six |
| `dc1cd03` | feat(plugin): add panel.resize and view.focus host calls |
| `0fd3c44` | feat(plugin): add the minor-five presentation vocabulary |
| `0b4b238` | feat(plugin): add the minor-four wire vocabulary (owner's work, committed by the session) |
| `f9f91ce` | fix(shell): synchronize the animator running flag (pre-existing race, fixed en route) |

All commits below `0b4b238` are on sysc-shell `main` history. Nothing anywhere is pushed.

## 2. What Plan D says (so you can audit intent, not just text)

Eight tasks plus prerequisites, each mapped to a bd issue in the sysc-shell tracker:

| Task | Closes | One-line intent |
|---|---|---|
| 0 | — | Merge `feat/kdeconnect` then `feature/wire-minor-5`; bump the sysc-shell pin off `v0.0.0-20260916042624-cc684ee8c131`; manifest protocol minor 2 → 6 |
| 1 | sysc-478 | Icon-led buttons: `gatedButton` gains an icon; pairing buttons get check/close/link, composer sends get `send` |
| 2 | sysc-468 | Tap-to-ping: `deviceCardTree` becomes a `KindButton` (ID `device-ping`); action row hides its ping button while the card shows |
| 3 | sysc-445 | Recent-images grid: manifest settings, `find`-based scan of the SFTP mount, x/image thumbnail cache, `KindImage` grid, open/share actions |
| 4 | sysc-445 | Device mockup: four type-sized PNG assets swap the device icon inside the ping button |
| 5 | — | Dynamic panel width via `panel.resize` (400 base, 525 for large device types) |
| 6 | — | Composer focus-on-open via `view.focus` (`share-text` / `sms-number`) |
| 7 | sysc-447 | Animated charging fill: `Animate: true` on the battery progress; charging pill gains a progress child with error/warning/success tint |
| 8 | sysc-430 | Live Niri gate with the real device; completion handover; close 478/468/445/447/430 then epic 420 |

It ends with a deviation ledger (webp, drag/portal share, per-device settings, offline dimming,
warning tone, header chrome, MPRIS/Valent/shortcuts/i18n).

## 3. Audit dimensions

1. **Wire-capability claims.** Every Plan D assumption about minor 5/6 must be checked against
   `plugin/v1/node.go` and `message.go` on `feature/wire-minor-5`: `KindImage` with
   `Path/ImageSize/ImageW/ImageH/Background`, `Stroke`/`StrokeFill` on row/column/button, the
   button-children rule (children must be non-interactive leaves), `Animate` on progress/gauge
   requiring `Key`, `CallPanelResize`/`CallViewFocus` and their params, the minor-5/6 validators,
   supervisor `Supported` containing `{1,5}` and `{1,6}`.
2. **Host-contract claims.** The behaviours Plan D leans on: `icons.FileResolver` accepts only
   absolute local paths with decodable extensions; the converter stamps a text input's `Action`
   from its `ID` (making `view.focus` able to target inputs); `panel.resize` bounds 64–4096 and
   its reply semantics (requested, not completed); the stamped action format
   `plugin:<viewID>:<nodeID>`; reduced-motion collapsing the glide.
3. **Plugin-side facts.** Every file:line citation Plan D makes into `view.go`, `service.go`,
   `daemon.go`, `main.go`, and `manifest.json` on the `feat/kdeconnect` worktree — line numbers
   drift, so verify the *content* at each citation, not just the number.
4. **Plan-internal consistency and ordering.** Task dependencies (Task 2 flips Phase 0's
   sysc-475 rule; Task 4 composes into Task 2's button; Task 7 rides Task 0's minor 6), the
   `PanelDelta` keyed-patch ID-collision argument for `device-ping` vs `ping`, and whether any
   task silently assumes state another task has not yet produced.
5. **bd alignment.** From `/home/nomadx/sysc-shell` (the tracker lives there — sysc-plugins has
   no `.beads`): confirm sysc-478, 468, 445, 447, 430 and epic 420 exist and are open, and that
   the plan's close list matches.
6. **Deviation ledger completeness.** Read the parity audit report on `feat/kdeconnect` and check
   every wire-gap it records is either closed by Designs A/B/C, handled by a Plan D task, or
   present in the ledger. Anything in neither column is a finding.
7. **Gates.** Confirm `make build test vet fmt validate` targets exist in sysc-plugins' Makefile.
8. **Risk register.** Anything you cannot verify from code is UNVERIFIED, never guessed. Say what
   evidence would settle it.

## 4. Primed findings (the author's own doubts — verify each, then judge)

These are places the author already knows the plan is soft. Confirm or refute each with evidence;
they are seeds, not the whole audit.

- **F1 — Task 1's central question is already answered, and the plan doesn't know it.** Task 1
  Step 2 says "first check whether the host co-renders a button's own Icon beside its Text". The
  converter's button case (`internal/plugin/view.go:378-399` on `feature/wire-minor-5`)
  synthesizes `[KindIcon, KindText]` children for any button carrying both, clearing `out.Text`
  ("the noctalia bar-widget shape"). If that reads as it appears, Task 1 collapses to "set the
  `Icon` field on the existing button nodes" — no view restructuring, and the pairing buttons'
  inline literals just gain `Icon`. Verify, then say so in the report.
- **F2 — children replace the label everywhere.** `paintChrome`
  (`internal/render/paint.go:1126-1140`) paints children *instead of* the node's own text label.
  Any task that adds a child to a node that also sets `Text`/`Icon` must restate the label as a
  child. Task 7's charging pill (progress child + "the existing icon+label row") is the exposed
  one — check the plan's fallback clause actually covers the failure mode.
- **F3 — Task 2 Step 4 edits a test that does not exist.** "Update the Phase 0 view test that
  asserted ping always visible" — Phase 0 is unimplemented. The plan needs an explicit ordering
  statement (Task 2 lands after Phase 0, or the step is conditional).
- **F4 — webp exclusion.** `decodableExtensions` (`internal/icons/theme.go:29`) is
  png/xpm/jpg/jpeg/gif/bmp. Confirm, and confirm the plan's scan filter matches it exactly.
- **F5 — the SFTP seam.** Task 3 asserts `daemon.go`'s sftp seam (beside `startBrowsing`, :37)
  extends to "mount call + mount-point property". Read the DBus surface the plugin already uses
  and confirm a mount point is actually obtainable; if it is not, Task 3's scan root is unfounded.
- **F6 — asset resolution under `go test`.** `os.Executable()` points into a temp build dir
  during tests; the plan's package-var escape hatch must actually be exercised by the test it
  prescribes.
- **F7 — focus targets must not be disabled.** The converter stamps `Action` from ID but clears
  it for disabled inputs; a disabled `share-text` would be unfocusable. Check Task 6's targets
  are enabled in the states where focus is sent.
- **F8 — supervisor handshake.** Confirm `68c0f8d` really added `{1,6}` (and that `{1,5}` arrived
  with Design A), since Task 0's manifest bump silently depends on it.
- **F9 — resize error tolerance.** `panel.resize` fails when no panel is open; Task 5's uiState
  loop must tolerate that. Check the plan says so.
- **F10 — manifest minor semantics.** The pomodoro audit (§2) established the manifest minor is
  decorative (the handshake checks the major only). Task 0's rationale ("the manifest should not
  lie") is consistent with that — confirm no task elsewhere treats the minor as load-bearing.

## 5. Method and constraints

- **Read-only.** No code changes, no commits, no pushes, no bd mutations. The audit report is the
  only file you create.
- **Verify by reading and by running.** Cheap verification is encouraged: `go build`/`go test`
  inside the worktrees where it settles a claim (the full `-race` suite is green as of the listed
  commits; re-running it is optional, not required). Cite only what you actually read —
  `path:line` for every claim.
- **Do not touch** the owner's uncommitted files in the sysc-shell main checkout:
  `.commandcode/taste/*` and `.tmp-thumbcheck/` are dirty by design.
- **Design docs are not in any working tree** — read them with
  `git show main:docs/plans/<file>.md` from `/home/nomadx/sysc-shell` (the main checkout sits on
  `feat/plugin-material-icons`, which predates them).
- **Long commands**: the shell tool's timeout clamps erratically; use `nohup … &` plus polling
  for anything over ~30s.
- The commit-msg hook rejects substrings like `bot`/`agent`/`claude` — irrelevant to you, since
  you commit nothing.

## 6. Report format

Write `docs/plans/2026-09-21-kdeconnect-gap-fill-audit-report.md` (sysc-plugins, uncommitted) with:

1. **Verdict** — one paragraph: is Plan D executable as written, executable after named
   amendments, or blocked?
2. **Per-task findings** — for each of Tasks 0–8: sound / amend / blocked, with evidence.
3. **Claims table** — every material claim you checked, marked verified / refuted / unverified,
   each with a `path:line` citation or the command that settled it.
4. **Primed-findings dispositions** — F1–F10, each confirmed, refuted, or upgraded.
5. **New findings** — anything the author missed, worst first.
6. **Recommended amendments** — concrete, diff-level text for each amendment (task number, step,
   replacement wording). Do not edit Plan D yourself.
7. **bd alignment** and **deviation-ledger review** as short sections.
8. **Go/no-go** for Task 0's merge order.

End the report with the exact list of files you read, so the commissioning session can gauge
coverage.
