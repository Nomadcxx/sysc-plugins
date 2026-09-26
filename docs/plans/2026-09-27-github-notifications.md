# GitHub Notifications Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extend the existing GitHub notification plugin into a reliable panel with a paged inbox, a paged review/work queue, and a contribution activity calendar.

**Architecture:** Keep the existing Go plugin process, `gh` CLI adapter, host state cache, and declarative `plugin/v1` UI. Add work and activity models beside the notification model, keep independent snapshots in the session, and route GitHub operations through one timeout-bounded coordinator. The panel renders one selected view at a time using host-native nodes.

**Tech Stack:** Go 1.26.4, standard library, installed `gh` CLI, `github.com/Nomadcxx/sysc-shell/plugin/v1` and `plugin/lint`.

---

This plan implements [the current design](2026-09-27-github-notifications-design.md) and uses the current plugin baseline in `plugins/github-notifications/`.

## Task 1: Add work and activity models with safe GitHub queries

**Files:**

- Modify: `plugins/github-notifications/gh.go`
- Modify: `plugins/github-notifications/notifications.go`
- Create: `plugins/github-notifications/work.go`
- Create: `plugins/github-notifications/activity.go`
- Create: `plugins/github-notifications/gh_test.go`
- Create: `plugins/github-notifications/work_test.go`
- Create: `plugins/github-notifications/activity_test.go`

**Step 1: Write failing query and normalization tests**

Test the exact argv for notification pages, three work queries, and `gh api graphql`. Normalize the contribution calendar into valid dates, nonnegative counts, known contribution levels, a total, and active-day count; ignore GitHub color strings. Reject malformed or duplicate days. Test that work responses preserve `total_count`, reject unsafe URLs, and use `is:issue` so pull requests cannot leak into Assigned Issues.

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Build|Normalize|Search|Activity)'`

Expected: FAIL because the page builders, work normalizer, and Activity query do not exist yet.

**Step 2: Implement the query builders and models**

Add the three work kinds and an Activity snapshot model. Use `gh api` argv arrays with constant search qualifiers and numeric page fields. Fetch `viewer.contributionsCollection(...).contributionCalendar` with `gh api graphql`; retain only the dates, counts, contribution levels, total, and active-day count. Do not build a shell command string or persist credentials. Keep notification thread IDs numeric before forming the PATCH path.

**Step 3: Run the focused tests**

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Build|Normalize|Search|Activity)'`

Expected: PASS; malformed items and unsafe links are rejected, activity dates/levels normalize, and all query arguments match the test table.

**Step 4: Commit the adapter slice**

Run: `git add plugins/github-notifications/gh.go plugins/github-notifications/notifications.go plugins/github-notifications/work.go plugins/github-notifications/activity.go plugins/github-notifications/gh_test.go plugins/github-notifications/work_test.go plugins/github-notifications/activity_test.go && git commit -m "feat: add GitHub work and activity queries"`

## Task 2: Model independent feed snapshots, paging, and cache migration

**Files:**

- Modify: `plugins/github-notifications/service.go`
- Modify: `plugins/github-notifications/service_test.go`
- Modify: `plugins/github-notifications/gh.go`

**Step 1: Add failing session tests**

Cover separate inbox/review/PR/issue snapshots and an Activity snapshot; page append and deduplication; exact totals versus lower-bound counts; a short final page; per-feed failure preserving that feed's prior data; and restoring the existing `{items, updated_at}` cache as stale inbox data.

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Session|Cache|Page|Feed)'`

Expected: FAIL for missing work/activity snapshots and paging state.

**Step 2: Implement session-owned feed state**

Extend `Session` with one inbox snapshot, three work snapshots, and one Activity snapshot. Feed snapshots own their rows and paging state where relevant, plus last successful update, stale flag, loading flag, status, and error. Keep the legacy cache field names for inbox data and add work/activity fields. Refresh success replaces that feed's data; failure preserves its previous data and freshness timestamp.

**Step 3: Add page operations and count semantics**

Fetch additional pages only on request. Show a lower bound while another page may exist and an exact count after a short page. Dedupe by stable thread ID or GitHub item URL when appending. Marking an inbox thread read invalidates later page offsets; require a page-one refresh before the next load-more. Do not apply notification paging rules to Search API totals.

**Step 4: Run the focused tests**

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Session|Cache|Page|Feed)'`

Expected: PASS, including old-cache restore, stale Activity restore, and independent stale/error behavior.

**Step 5: Commit the state slice**

Run: `git add plugins/github-notifications/service.go plugins/github-notifications/service_test.go plugins/github-notifications/gh.go && git commit -m "feat: cache GitHub work and activity feeds"`

## Task 3: Serialize refresh/actions and protect read state

**Files:**

- Modify: `plugins/github-notifications/service.go`
- Modify: `plugins/github-notifications/service_test.go`
- Modify: `plugins/github-notifications/gh.go`
- Create: `plugins/github-notifications/coordinator.go`
- Create: `plugins/github-notifications/coordinator_test.go`

**Step 1: Write failing ordering tests**

Use blocking fake `GH` methods to prove that one active refresh plus repeated triggers produces at most one follow-up; an in-flight refresh cannot restore a thread being marked read; a mark-all result cannot be overwritten by an older inbox response; Activity failure retains its previous snapshot; and failed actions restore affected rows.

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Coordinator|Refresh|Mark)'`

Expected: FAIL on overlapping refresh/read state with the current session.

**Step 2: Add the coordinator and pending-action guards**

Route refresh, load-more, mark-one, and mark-all through one worker. Keep the UI/event loop free while commands run. Coalesce manual and timer refresh requests to one follow-up. Track pending thread IDs and a mark-all pending flag so older results cannot reintroduce read items. Keep optimistic removal and rollback semantics.

**Step 3: Bound each CLI operation**

Use a fixed per-command timeout with `context.WithTimeout`; pass its child context to `exec.CommandContext`. Shutdown cancellation must stop an active child. Do not add a timeout setting.

**Step 4: Run the focused tests**

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Coordinator|Refresh|Mark)'`

Expected: PASS without sleeps used as synchronization; tests coordinate with channels and deadlines.

**Step 5: Commit the concurrency slice**

Run: `git add plugins/github-notifications/service.go plugins/github-notifications/service_test.go plugins/github-notifications/gh.go plugins/github-notifications/coordinator.go plugins/github-notifications/coordinator_test.go && git commit -m "fix: serialize GitHub feed operations"`

## Task 4: Build the native inbox/work/activity panel and geometry checks

**Files:**

- Modify: `plugins/github-notifications/view.go`
- Modify: `plugins/github-notifications/view_test.go`
- Reference: `docs/plugin-ui-rules.md`

**Step 1: Add failing tree and layout tests**

Assert the header, primary Inbox/Work/Activity switch, work category switch, search input, separate mark-read control, account-wide mark-all name, row type labels, lower-bound/page footer, Activity summary, 53-week heatmap, day tooltips, and all empty/loading/stale/error variants. Run `shelllint.Tree` for bar, tooltip, and representative 420×640 panels; include page-full lists at the maximum page size and check the heatmap stays below `v1.MaxNodes`.

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Trees|Panel|ViewsFit)'`

Expected: FAIL because the current panel has only a notification column.

**Step 2: Render from explicit view state**

Use the shell's native row/column, segmented, text input, list, button, and text nodes. Render the contribution calendar as 53 week columns × 7 day cells, grouped with month labels. Map GitHub contribution levels to `surface`, `container`, `card`, `chip`, and `accent` fills; do not use GitHub's literal colors. Keep the current panel size and palette. Use supported `notifications`, `notifications-off`, and `refresh` icons; use `PR` and `Issue` text markers for work rows. Render only the selected view/category so the tree remains bounded.

**Step 3: Run view validation and geometry tests**

Run: `go test -count=1 -p 1 ./plugins/github-notifications -run 'Test(Trees|Panel|ViewsFit)'`

Expected: PASS with no findings at 420×640, valid panel/bar/tooltip roots, and the full Activity heatmap present.

**Step 4: Commit the view slice**

Run: `git add plugins/github-notifications/view.go plugins/github-notifications/view_test.go && git commit -m "feat: add GitHub triage and activity views"`

## Task 5: Route panel events, URLs, cache, and settings

**Files:**

- Modify: `cmd/sysc-plugin-github-notifications/main.go`
- Create: `cmd/sysc-plugin-github-notifications/main_test.go`
- Modify: `plugins/github-notifications/manifest.json`

**Step 1: Add failing protocol-routing tests**

Drive host messages through the plugin harness. Cover Inbox/Work/Activity mode and category changes, local search, load-more, manual refresh coalescing, mark-one/mark-all routing, and independent state for two panel view IDs. Test that notification open resolves an item ID to its normalized URL; a failed/missing opener must leave the thread unread.

Run: `go test -count=1 -p 1 ./cmd/sysc-plugin-github-notifications -run 'Test(Run|Routes|Open)'`

Expected: FAIL because the current event handler passes the notification ID to `xdg-open` and has no Work or Activity routes.

**Step 2: Connect the session and coordinator to the host loop**

Make the receive loop handle UI messages while the coordinator runs `gh`. Cache snapshots after successful feed updates and actions. Keep panel mode/search state per `ViewID`; share feed data and refresh status across views. Open only URLs returned by validated normalized records. Mark a notification read only after the opener starts successfully. Show Activity from its own snapshot and preserve its prior cache if GraphQL fails.

**Step 3: Keep settings compatible and update manifest metadata**

Keep existing setting keys. Set `per_page`'s default and maximum to 100, preserving any previously saved value, and relabel it “Rows per inbox page”. Keep the other defaults. Update the description and bump the manifest version to 0.3.0. Do not add settings or capabilities.

**Step 4: Run protocol and focused package tests**

Run: `go test -count=1 -p 1 ./cmd/sysc-plugin-github-notifications -run 'Test(Run|Routes|Open)'`

Expected: PASS; messages update the expected view and opener receives a canonical URL, not a thread ID.

**Step 5: Commit host wiring**

Run: `git add cmd/sysc-plugin-github-notifications/main.go cmd/sysc-plugin-github-notifications/main_test.go plugins/github-notifications/manifest.json && git commit -m "feat: wire GitHub activity panel"`

## Task 6: Run the focused final gate

**Files:**

- Verify: `plugins/github-notifications/`
- Verify: `cmd/sysc-plugin-github-notifications/`
- Verify: `plugins/github-notifications/manifest.json`

**Step 1: Run focused Go tests**

Run: `go test -count=1 -p 2 ./plugins/github-notifications ./cmd/sysc-plugin-github-notifications`

Expected: PASS for both packages. Keep package parallelism capped at 2 on this machine.

**Step 2: Validate the manifest and build the plugin command**

Run `go run -p 1 ./tools/validate-manifests`, then `go build -p 2 -o /tmp/sysc-plugin-github-notifications ./cmd/sysc-plugin-github-notifications`.

Expected: valid manifest ID/version/settings and a successful command build.

**Step 3: Review the final diff**

Confirm that bar counts remain inbox-only; work and Activity are read-only; mark-all is visibly account-wide; old inbox cache restores; feed errors preserve cached data; the Activity heatmap uses semantic theme fills; and no auth token or unrelated files were added.

**Step 4: Stop at the focused gates**

Report the focused test, manifest, and build outcomes. Do not broaden the run to a repo-wide unconstrained Go test command.
