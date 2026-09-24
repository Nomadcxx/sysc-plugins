# Design and Plan Register

Last updated: 2026-09-24.

Every design, plan, and handover this project has produced, with where it lives and what it is for.
Add a row here in the same commit that adds a document. A document that is not in this register is
one the project will lose.

## This file indexes documents. bd tracks state.

bd (the sysc-shell tracker — this repo has no `.beads` database) is authoritative for what is done,
in flight, or gated. This register is authoritative for what documents exist and what each one is
for. Do not duplicate status between them.

## Document kinds

Naming is `YYYY-MM-DD-<topic>[-<kind>].md`. A topic with no kind suffix is the implementation plan.

| Kind | Purpose |
|---|---|
| *(none)* | The executable implementation plan. Carries the `superpowers:executing-plans` header, exact files, TDD steps, and commit boundaries. |
| `-design` | The approved design. Fixes contracts and decisions before any plan is written. |
| `-research` | Background gathered before a design. Kept for provenance, not maintained. |
| `-audit` | A point-in-time comparison against a reference. Evidence for a ledger, not a plan. |
| `-handover` | In-flight only. When the work lands, or remaining items are in bd, delete the file and drop its register row. |

## Documents

| Document | Kind | What it is |
|---|---|---|
| `2026-09-16-timer-world-clock-parity.md` | plan | Timer and world-clock parity implementation plan. |
| `2026-09-19-aiusage-research.md` | research | AI usage plugin background research. |
| `2026-09-19-aiusage-design.md` | design | AI usage plugin design, grown to visual parity on wire minor 4. |
| `2026-09-19-aiusage-implementation.md` | plan | AI usage plugin implementation plan. |
| `2026-09-19-kdeconnect-phone-connect.md` | design + plan | KDE Connect (Phone Connect) plugin design and P1–P8 implementation plan. |
| `2026-09-19-kdeconnect-parity-audit-report.md` | audit | The DMS parity audit and UI walk (sysc-431–478 evidence) that drove Phase 0 and the gap-fill plan. |
| `2026-09-20-pomodoro-audit.md` | audit | Pomodoro plugin audit against the reference. |
| `2026-09-20-pomodoro-handover.md` | handover | Pomodoro work handover. |
| `2026-09-21-kdeconnect-gap-fill-plan.md` | plan | Plan D — the post-wire-minor-5/6 gap fill for the KDE Connect plugin (icons, tap-to-ping, recent images, mockup, resize, focus, charging fill, live gate). Audited 2026-09-21; amendments applied. |
| `2026-09-21-kdeconnect-gap-fill-audit-report.md` | audit | Independent audit of Plan D: verdict, claims table, primed-finding dispositions, diff-level amendments, merge-order go/no-go. |
| `2026-09-21-kdeconnect-device-mockups.md` | plan | The artwork commission plan the device-mockup PNGs were generated from (assets live in `plugins/kdeconnect/assets/`). |
| `2026-09-22-kdeconnect-live-test-round.md` | plan | Plan D Task 8 runbook — build, deploy, and manually exercise the plugin on the laptop against a real phone; matrix, rollback, bd closes. |
| `2026-09-22-kdeconnect-live-failure-audit-handover.md` | handover | Commission for the live-failure audit: root-cause the three deployed symptoms, commission gap-analysis and hallmark sub-audits, then write the redesign plan. |
| `2026-09-23-plugin-view-geometry-lint.md` | plan | Host-side node identity in layout rejections, a public `plugin/lint` built on the shell's own pipeline, and the plugin-repo tests and rules doc that use it — so a view that validates but cannot be drawn fails in `go test`, not on the desktop. |
| `2026-09-23-wallpaper-depth-design.md` | design | Approved design for a shell-owned desktop clock with per-output depth masks and wallpaper-aware placement. |
| `2026-09-23-wallpaper-depth.md` | plan | Implementation sequence for wallpaper snapshots, mask registration, the depth clock, inference, integration gate, and bare-metal calibration. |
| `2026-09-24-wallpaper-depth-gap-closure.md` | plan | Reuse host settings controls, add queued mask generation and setup details, then finish the Wallpaper Depth panel. |

## Documents on branches

| Branch | Holds |
|---|---|
| *(none — every document is on `main`)* | |
