# Mini Docker audit workplan

**Date:** 2026-09-22
**Status:** complete (tranche landed 2026-09-22; live gate green)
**Driver:** The mini-docker plugin (v0.2.0) was built quickly, has never been tested live by the user, and is suspected incomplete. Four audits produce the findings that become the roadmap for the next development tranche.

## Workflow contract

- **Worktree:** all audit and development work happens in `.worktrees/feat/mini-docker-audit` (branch `feat/mini-docker-audit`, cut from `405c0f9`). The main checkout's uncommitted kdeconnect WIP is untouched.
- **Todo list:** tracked in the checklist below (no separate todo tool in this session); updated as phases complete.
- **TDD:** the development tranche that follows the audits is test-first: failing test → smallest change → green, per the repo's existing gate-test conventions.
- **Ponytail:** recommendations and subsequent implementation follow the lazy-senior-dev ladder — deletion over addition, no unrequested abstractions, no new dependencies, fewest files, stdlib first, `ponytail:` comments name any intentional shortcut's ceiling and upgrade path. Never simplify away input validation at trust boundaries, error handling that prevents data loss, or security.
- **Audits are read-only** for code; each writes only its own findings doc.

## Baseline

- `go test ./plugins/mini-docker/... ./cmd/sysc-plugin-mini-docker/...` → mini-docker `ok`; **cmd entry has no test files** (finding, pre-audit).
- Worktree clean at `405c0f9`.

## Audit targets

- `plugins/mini-docker/{manifest.json,docker.go,service.go,view.go,mini_docker_test.go}` (~419 lines Go + manifest)
- `cmd/sysc-plugin-mini-docker/main.go` (142 lines)
- Protocol: `plugin/v1` consumed from `github.com/Nomadcxx/sysc-shell` (locate via `go list -m -f '{{.Dir}}' github.com/Nomadcxx/sysc-shell`); minor 1 adds `subtle`/`accent` text tones and host honors node Height
- Prior art the plugin must measure against: `plugins/timer` (first rebuilt widget, pattern-setter), `plugins/aiusage` (newest, just audit-hardened)

## Phase 1 — three parallel audits

| # | Audit | Agent | Deliverable |
|---|-------|-------|-------------|
| 1 | UI (structure, layout, tones, icons, visual parity with rebuilt plugins) | general | `docs/plans/2026-09-22-mini-docker-ui-audit.md` |
| 2 | UX (journey, states, actions, settings, failure communication) | general | `docs/plans/2026-09-22-mini-docker-ux-audit.md` |
| 3 | Functionality/backend (docker exec correctness, lifecycle, protocol wiring, tests, security, ponytail shrink pass) | review | `docs/plans/2026-09-22-mini-docker-backend-audit.md` |

Findings format: severity (P1 blocker → P4 polish), file:line evidence, concrete fix direction, ponytail-compliant scope. No bd issues filed by auditors; triage happens in Phase 3.

## Phase 2 — hallmark audit (4th)

After Phase 1 findings land, run the hallmark skill (anti-slop design audit) over the bar widget and panel design, producing `docs/plans/2026-09-22-mini-docker-hallmark-audit.md`.

## Phase 3 — consolidation and roadmap

- Merge all four audits into `docs/plans/2026-09-22-mini-docker-roadmap.md`: prioritized tranche of work items, each with acceptance evidence and TDD entry point.
- User reviews the roadmap before any implementation starts.
- Optional: file sysc- issues into the sysc-shell bd tracker per repo convention.

## Checklist

- [x] Worktree created, baseline tests green
- [x] Audit 1: UI → `2026-09-22-mini-docker-ui-audit.md` (15 findings; verdict: first-sweep quality, panel unreachable)
- [x] Audit 2: UX → `2026-09-22-mini-docker-ux-audit.md` (verdict: not usable today; panel cannot open)
- [x] Audit 3: Functionality/backend → `2026-09-22-mini-docker-backend-audit.md` (verdict: crash-risk data race + unopenable panel; ≈−16 line shrink)
- [x] Audit 4: Hallmark → `2026-09-22-mini-docker-hallmark-audit.md` (0 critical · 4 major · 5 minor; no template fingerprint, absence-of-craft majors)
- [x] Consolidated roadmap written → `2026-09-22-mini-docker-roadmap.md` (T0–T4, 3 decisions, net ≈ +80–110 lines)
- [x] User sign-off on roadmap (2026-09-22: start T0; D1 = delete show_count, honest status_mode labels; D3 = file sysc-496/497/498)
- [x] Development tranche (TDD + ponytail):
  - [x] T0 gates → 1a9bf9d (reachable panel, mutex-serialized state with -race storm test, refresh off main loop)
  - [x] T1 truthfulness → 4e1fa78 (diagnosed stderr, persistent actErr, 15s action timeout, error tone on pill)
  - [x] T2 surface → 293fe10 (KindList scroll + 150-row cap + "+N more", read-only tooltip column, running-first + accent, padding/title/PinEnd/Tabular)
  - [x] T3 shrink → 77242ed (show_count deleted, honest labels, notifications capability dropped, dead instance field gone, ParseAction allow-list before argv, fallbackVersion pinned by test)
  - [x] T4 verification: live gate `go test -tags live ./plugins/mini-docker/` passes against the real daemon (11 containers, 6 running, all trees validate); manifest 0.3.0, panel width 480 (live-named decision), fallback synced, binaries rebuilt
