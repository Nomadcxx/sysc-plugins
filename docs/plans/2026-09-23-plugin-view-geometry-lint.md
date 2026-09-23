# Plugin View Geometry Lint Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the "plugin view does not fit its slot" bug class visible in `go test` instead of
on the user's desktop: name the offending node in the host's rejection messages, and export a
public layout checker (`plugin/lint`) that a plugin repo can run against its own view trees.

**Why:** the aiusage panel became a failure card (Close / Retry / Disable) for days because a
padded row declared `Height: 28` for children that measure 16 tall — `Height` includes padding,
so the content box was 274×12. Nothing in the plugin repo could see it: `v1.Validate` is
geometry-blind, the host's layout stops at the **first** rejection, and `internal/ui` is not
importable from another module. The fix is a checker that runs the host's own pipeline at test
time.

**Depends on:** the shell work in the sibling repo (`sysc-shell`), then the pin move here. History
lives in `sysc-shell` on `main` (`feat(ui): name the node that does not fit, and check a view
without a host`).

**Tech stack:** Go 1.26.4, two modules: `github.com/Nomadcxx/sysc-shell` and
`github.com/Nomadcxx/sysc-plugins`.

## Global constraints

- This machine OOMs on `go test -race` and `go test ./...`. Iterate with scoped runs:
  `timeout 120s env GOMAXPROCS=2 go test -count=1 <pkg> -run '<Name>'`. CI still runs `-race`.
- Pin order: the shell repo is merged and pushed **before** this repo's pin moves.
- `commit-msg` hook rejects `agent`, `bot`, `both`, `llm`, `codex`, `cursor`, `co-authored-by`
  and friends; keep messages clean.
- The live config (`~/.config/sysc-shell/config.json`) and any deployed binary are never touched
  by this work: it moves sources only.

## The geometry contract these checks enforce

| Rule | Detail |
|---|---|
| Height includes padding | A node's content box is `Height − 2×Padding`. A row is refused when a child cannot live in it. |
| Row width | A row's children must also fit its content width. Text clips at the edge; a control (button, capsule, segmented control, menu, drag source) that overruns refuses the row. |
| PinEnd | On a two-child row, reserves the trailing child's width before the leading text is clipped. |
| Columns never refuse | A child taller or wider than its column truncates or overflows in silence. Budget column children yourself. |
| Capsule in a row | Fills the row's content height, so a fixed-size disc needs a matching content height (26 content ⇒ `Height 42` with `Padding 8`). |
| Text metric | `len(bytes)×8` wide, `16` tall, whatever the role. |
| Root kinds | Bar root is a row; panel root is a column (or a list); tooltip root is a column. |
| Slots | Bar 240×32, tooltip 280×200, panel from the manifest; a panel with `include_settings` is wrapped by the host, so its tree gets a smaller box than declared. |

## Shell tasks (`sysc-shell`)

**Task 1 — name the node that does not fit.** Files: `internal/ui/tree.go` (add `Node.Path`, a
`Kind.String()` mapper), `internal/ui/layout.go` and `internal/ui/column.go` (rejection wording),
`internal/plugin/view.go` (stamp `Path: path` in `convertNode`), `internal/ui/layout_test.go`
(`TestLayoutNamesTheNodeThatDoesNotFit`). The tail `child %d of kind %d does not fit in %dx%d`
stays byte-identical for the dated docs that quote it; identity is added around it:

```
ui: row at root.children[1]: child 0 of kind 1 (text "Avg 70%" at root.children[1].children[0]) does not fit in 274x12
```

**Task 2 — one metric, and a checker that reports every violation.** Files:
`internal/plugin/measure.go` (`Measure(text, attrs) = len×8, 16`; deletes the duplicate
`pluginMeasure` in `internal/shell/pluginhost.go` and the test's copy), `internal/ui/check.go`
(`CheckFit(root, bounds, measure) []FitProblem`, mirroring `Layout`'s placement rules — text
clipping, `PinEnd` reservation, scroll width — and reporting **all** violations), and
`internal/ui/check_test.go`, whose `TestCheckFitAgreesWithLayout` pins the mirror: a finding
exists exactly when `Layout` returns an error.

**Task 3 — publish `plugin/lint`.** Files: `plugin/lint/lint.go`, `plugin/lint/lint_test.go`,
and `internal/shell/pluginhost.go` (the bar/tooltip slot constants alias lint's).
`lint.Tree(root *v1.Node, view v1.ViewKind, width, height int) []Finding` runs
`v1.Validate → Convert → ui.CheckFit` with `plugin.Measure` and returns every finding with its
wire path. The package comment carries the rules table above; it is the godoc a plugin author
reads.

## Plugin tasks (this repo)

**Task 4 — check the aiusage views.** File: `plugins/aiusage/view_fit_test.go`. Reads the panel
box from `manifest.json`, then for every state (`fresh`, `setup`, `fault`, `nodata`, `empty`) ×
host minor (7…2) lints `BarTree` at `lint.BarWidth×BarHeight` and `PanelTree` at the manifest
box. Verified by reverting the deployed fix (`Height: 42` → `28`): the test reports
`does not fit in 274x12` per state, then goes green when restored.

**Task 5 — every gate test lints its snapshots.** Files: `tests/integration/harness_test.go`
(adds `viewSlot{v1.ViewKind, w, h}`, `recordSlot(map, id, slot)`, `checkFits(t, slot, root)`) and
the notes, calendar and recorder gates. Each host records its slot **before** sending
`ViewOpen` and reads it under its own mutex, so a snapshot can never arrive unmonitored and
`-race` stays clean:

```go
h.mu.Lock()
h.slots = recordSlot(h.slots, "panel-1", viewSlot{v1.ViewPanel, 420, 800})
h.mu.Unlock()

// the ViewSnapshot case, where the root is stored:
h.mu.Lock()
slot, monitored := h.slots[m.ViewID]
h.root = m.Root
h.mu.Unlock()
if monitored {
    checkFits(h.t, slot, m.Root)
}
```

Bars are linted at the host's real slot (`lint.BarWidth/BarHeight`), not the scripted copy's
narrower number. The kdeconnect gate is **not** in this task: its file is in flight in the KDE
Connect workstream, which applies the same pattern after this lands.

**Task 6 — the rules where plugin authors work.** Files: `docs/plugin-ui-rules.md` (the contract,
the worked `Height 28 / Padding 8 → 274×12` example, the metric, the slots, and the two-line
test) and `README.md` step 4 pointing at both it and `plugin/lint`.

## Verification

| What | Command | Expect |
|---|---|---|
| Shell suites | scoped `go test -count=1` on `./internal/ui ./internal/plugin ./plugin/lint ./internal/shell -run TestPlugin` | `ok` |
| Shell hygiene | `gofmt -l internal plugin`, scoped `go vet`, `go build -o /dev/null ./cmd/sysc-shell` | silent, clean |
| No semantic drift | the aiusage layout probe from the original audit, re-run against the branch tree | 0/36 layouts fail |
| Catches the bug | `Height: 42` → `28` in the plugin worktree, run Task 4's test, restore | red, then green |
| This repo | `go test -count=1 ./plugins/aiusage`, `./tests/integration`, `go run ./tools/validate-manifests` | `ok` |
| Pin | `grep replace go.mod` after landing | no replace |

## Landing order

1. Shell branch: commit, push, fast-forward `main`. The pin target is that commit
   (`v0.0.0-<date>-<sha>`), fetched through the module proxy.
2. This repo: drop the temporary `replace`, `go get github.com/Nomadcxx/sysc-shell@<sha>`,
   `go mod tidy`, re-run the plugin test and the wired gates against the **pinned** module, then
   commit, rebase onto `main`, and fast-forward `main`.
3. The KDE Connect workstream wires its gate (Task 5's pattern) and closes the daemon-absent run
   of that gate: the committed predicate accepts the "Phone Connect Not Available" card, so the
   gate no longer depends on `kdeconnectd` being up.

## Risks

- Other plugins may fail the new gate. That is the point of it; fix trivial declared geometry,
  and escalate anything design-shaped. None were found at the time of writing (notes, calendar
  and recorder are green; kdeconnect's own failure was its stale predicate, not geometry).
- A column that truncates silently is still invisible to these checks: `CheckFit` reports what
  the host refuses, not what it silently clips.
- Message churn: keep the tail of the rejection wording stable — dated documents quote it.
