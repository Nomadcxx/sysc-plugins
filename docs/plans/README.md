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
| `2026-09-19-aiusage-design.md` | design | AI usage plugin design, updated for OpenCode Go and its opt-in credentials. |
| `2026-09-19-aiusage-implementation.md` | plan | AI usage plugin implementation plan, including the corrected Synthetic endpoint. |
| `2026-09-19-kdeconnect-phone-connect.md` | design + plan | KDE Connect (Phone Connect) plugin design and P1–P8 implementation plan. |
| `2026-09-19-kdeconnect-parity-audit-report.md` | audit | The DMS parity audit and UI walk (sysc-431–478 evidence) that drove Phase 0 and the gap-fill plan. |
| `2026-09-20-pomodoro-audit.md` | audit | Pomodoro plugin audit against the reference. |
| `2026-09-20-pomodoro-handover.md` | handover | Pomodoro work handover. |
| `2026-09-21-kdeconnect-gap-fill-plan.md` | plan | Plan D — the post-wire-minor-5/6 gap fill for the KDE Connect plugin (icons, tap-to-ping, recent images, mockup, resize, focus, charging fill, live gate). Audited 2026-09-21; amendments applied. |
| `2026-09-21-kdeconnect-gap-fill-audit-report.md` | audit | Independent audit of Plan D: verdict, claims table, primed-finding dispositions, diff-level amendments, merge-order go/no-go. |
| `2026-09-21-kdeconnect-device-mockups.md` | plan | The artwork commission plan the device-mockup PNGs were generated from (assets live in `plugins/kdeconnect/assets/`). |
| `2026-09-22-kdeconnect-live-test-round.md` | plan | Plan D Task 8 runbook — build, deploy, and manually exercise the plugin on the laptop against a real phone; matrix, rollback, bd closes. |
| `2026-09-22-kdeconnect-live-failure-audit-handover.md` | handover | Commission for the live-failure audit: root-cause the three deployed symptoms, commission gap-analysis and hallmark sub-audits, then write the redesign plan. |
| `2026-09-22-kdeconnect-live-failure-audit-report.md` | audit | Evidence-ranked root causes and design constraints for the KDE Connect live failures. |
| `2026-09-22-kdeconnect-redesign-plan.md` | plan | Ordered live fix and acceptance plan for the KDE Connect pill and paired-device panel. |
| `2026-09-22-mini-docker-audit-workplan.md` | plan | The four-audit commission for the mini-docker tranche and its checklist; the T0–T4 tranche landed on `feat/mini-docker-audit` and is merged. |
| `2026-09-22-mini-docker-ui-audit.md` | audit | UI audit of the shipped v0.2.0: 15 findings, verdict first-sweep quality with an unreachable panel. |
| `2026-09-22-mini-docker-ux-audit.md` | audit | UX audit: not usable as shipped — the panel could not open, and action errors were erased before display. |
| `2026-09-22-mini-docker-backend-audit.md` | audit | Backend audit: a crash-risk data race, the argv and validation review, and a shrink ledger. |
| `2026-09-22-mini-docker-hallmark-audit.md` | audit | Craft audit: 0 critical · 4 major · 5 minor, all absence-of-craft rather than template slop. |
| `2026-09-22-mini-docker-roadmap.md` | plan | The consolidated T0–T4 tranche plan and its signed-off decisions (settings reduction, panel width, shell issue filing). |
| `2026-09-22-mini-docker-tranche-review.md` | audit | Independent read of the tranche: verdict fix-first; its three items landed in `5991b68`. |
| `2026-09-23-plugin-view-geometry-lint.md` | plan | Host-side node identity in layout rejections, a public `plugin/lint` built on the shell's own pipeline, and the plugin-repo tests and rules doc that use it — so a view that validates but cannot be drawn fails in `go test`, not on the desktop. |
| `2026-09-23-aiusage-defect-ledger.md` | audit | AI Usage provider, settings, scheduling, privacy, and UI findings with evidence and current dispositions. |
| `2026-09-23-aiusage-workplan.md` | plan | Remediation checklist and focused verification record for the AI Usage audit. |
| `2026-09-24-aiusage-ui-handover.md` | handover | Pre-deploy AI Usage UI and host integration status; compositor-level scroll review remains. |
| `2026-09-23-mini-docker-research.md` | research | Prior-art research for the mini-docker redesign: the Noctalia v4/v5 and DMS managers read in full (pinned SHAs), the pinned shell's render vocabulary, surfaces, limits and icon catalogue, the v0.3.0 tranche's open findings, and the feature-disposition table the design will decide from. |
| `2026-09-23-mini-docker-design.md` | design | Approved mini-docker parity design: four tabs, per-tab refresh and error behavior, safe actions, run-image form, protocol minor 4, and the signed deletion of `show_count`. |
| `2026-09-23-mini-docker-parity-pass.md` | plan | Approved TDD implementation sequence for mini-docker parity, with exact files, focused verification, and task-boundary commits. |
| `2026-09-23-wallpaper-depth-design.md` | design | The approved Wallpaper Depth design: a fixed shell-owned clock on the Bottom layer, per-output Depth Anything masks, destination-out composition, and two narrow wallpaper host calls. |
| `2026-09-23-wallpaper-depth.md` | plan | The TDD implementation sequence for wire minor 7, wallpaper snapshot and mask calls, the Bottom-layer depth clock, serialized per-output inference, process gate, and bare-metal calibration. |
| `2026-09-24-wallpaper-depth-gap-closure.md` | plan | Reuse the host settings renderer, add queued bulk mask generation and setup/result detail, then polish the Wallpaper Depth panel. |
| `2026-09-24-notes-design.md` | design | Approved Notes manager plus shell-owned floating sticky surfaces for a configured Obsidian vault folder, including the bounded plugin surface contract, pin layers, and save-failure safety. |
