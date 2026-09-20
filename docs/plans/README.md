# Design and Plan Register

Last updated: 2026-09-21.

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
| `2026-09-20-pomodoro-audit.md` | audit | Pomodoro plugin audit against the reference. |
| `2026-09-20-pomodoro-handover.md` | handover | Pomodoro work handover. |
| `2026-09-21-kdeconnect-gap-fill-plan.md` | plan | Plan D — the post-wire-minor-5/6 gap fill for the KDE Connect plugin (icons, tap-to-ping, recent images, mockup, resize, focus, charging fill, live gate). |
| `2026-09-21-kdeconnect-gap-fill-audit-handover.md` | handover | Commission for the independent audit of Plan D; drop it once the audit report is landed. |

## Documents on branches

| Branch | Holds |
|---|---|
| `feat/kdeconnect` | `2026-09-19-kdeconnect-parity-audit-report.md` — the DMS parity audit and UI walk (sysc-431–478 evidence). Register it here in the same commit that merges the branch. |
