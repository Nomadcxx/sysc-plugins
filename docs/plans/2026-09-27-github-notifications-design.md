# GitHub Notifications Design

Date: 2026-09-27 · Status: approved.

Research: [pinned prior-art report](2026-09-27-github-notifications-research.md).

## Goal

Extend `org.sysc.github-notifications` into a reliable GitHub panel with three distinct views: unread notification threads, open work items that need the user's attention, and the user's contribution activity.

## Scope decision

The plugin keeps the GitHub notification inbox and adds a read-only work queue and contribution calendar. Each view stays separate because notification urgency, current work, and contribution history have different meanings.

| Area | Included behavior |
|---|---|
| Inbox | Unread threads, local search, individual read/open actions, mark all read, paging, unread bar count |
| Work | Review-requested PRs, authored open PRs, assigned open issues; local search, paging, links |
| Activity | Trailing-year contribution heatmap, total contributions and active days, per-day hover details |
| Reliability | Stale cache, per-feed errors, request timeouts, coalesced refresh, read/refresh race protection |
| Excluded | Following feed, repository/Actions dashboard, multi-forge support, write actions on work items, persisted triage state |

The contribution calendar is useful profile context, but it is not notification urgency and will not affect the bar count or work ordering. The broader Kanban surface would add unrelated feeds and API traffic. Git Companion and DMS provide the useful work categories. The DMS source has no license metadata in the inspected snapshot, so this design uses behavior as reference and copies no source or artwork.

## Visual design

### Overall shape

Keep the current 420×640 panel. Use a compact, native shell panel with clear hierarchy: scrollable lists for Inbox and Work, and a compact calendar for Activity. The panel should feel like a quiet triage surface: the selected view and unread state are easy to spot, while timestamps and repository context stay secondary.

```text
┌ GitHub · Updated 2m ago                         ↻ ┐
│  [ Inbox · n ]       [ Work ]       [ Activity ] │
│  [ Search notifications…       ] [Mark all read] │
│  Showing cached inbox · refresh failed          │
│                                                  │
│  PR  Restore retry handling              4m   ✓  │
│      acme/api · #248 · Review requested          │
│  ──────────────────────────────────────────────  │
│  Issue  Document migration flags          1h   ✓ │
│         acme/cli · #913 · Mentioned              │
│                                                  │
│                        100 shown · Load older    │
└──────────────────────────────────────────────────┘
```

The example rows are illustrative. The panel uses host `KindColumn`, `KindRow`, `KindSegmented`, `KindTextInput`, `KindList`, `KindButton`, `KindText`, and `KindIcon` nodes. It inherits the shell's light or dark palette and uses its existing `card`, `soft`, `accent`, `subtle`, and `error` treatments. No custom CSS, image assets, or new icon dependency is needed.

### Hierarchy and row shape

1. **Header:** “GitHub” at title weight; a compact “Updated …” freshness label; a refresh icon button with an accessible name.
2. **Primary switch:** three equal segments, Inbox, Work, and Activity. Inbox shows the unread count. Work has no aggregate count because a PR can appear in more than one category. Activity has no urgency count. Inbox is the default.
3. **Work category switch:** when Work is selected, show Review, My PRs, and Issues segments with category counts.
4. **Search/action row:** in Inbox and Work, search filters loaded rows locally by title, repository, number, and reason. Inbox also shows “Mark all read”; Work has no mutating action. Activity replaces this row with a contribution total and active-day count.
5. **Feed status:** loading, stale-cache, or error text sits below controls and above the results. Errors name the affected feed and never replace useful cached rows.
6. **Selected view:** only the selected view is rendered. Inbox and Work use two-line cards. Activity uses a compact, dated heatmap with weeks as columns and weekdays as rows; hovering a day shows its date and contribution count.
7. **Paging:** a footer says how many rows are loaded and offers “Load older” when another page may exist.

Use `PR` and `Issue` text labels for work row types. The current host icon catalogue provides `notifications`, `notifications-off`, and `refresh` but no PR/issue glyphs. Avoid avatars and decorative artwork; they add requests and visual weight without improving triage.

The shell's `KindGraph` is a sparkline limited to 64 samples, and `KindScheduleGrid` is a timed event layout limited to seven days. Neither represents a year calendar. Render the heatmap as about 53×7 small native row/column cells, within the host's 1,024-node view limit. Map contribution levels to semantic theme fills (`surface`, `container`, `card`, `chip`, `accent`) and ignore GitHub's raw color values. Keep a text legend so intensity does not rely on color alone. Verify the full Activity tree against the 420×640 panel geometry.

Use bold title text and regular body text for the row hierarchy; repository, reason, and time use the host's subtle/caption treatment. The selected segment uses the host accent. Unread state uses title weight and the notification count, not color alone. Security alerts and known failures receive the error tone plus a visible text label. Controls keep accessible names and at least the host's normal 32-pixel button target.

### Work view wireframe

```text
┌ GitHub · Updated 2m ago                         ↻ ┐
│  [ Inbox · n ]       [ Work ]       [ Activity ] │
│  [Review n] [My PRs n] [Issues n]                │
│  [ Search title, repository, or number…       ]  │
│                                                  │
│  PR  Add retry budget                     3h     │
│      acme/api · #248 · Review requested          │
│  PR  Split parser tests                   1d     │
│      acme/cli · #918 · Authored                 │
│                                                  │
│                        100 shown · Load older    │
└──────────────────────────────────────────────────┘
```

### Activity view wireframe

```text
┌ GitHub · Updated 2m ago                         ↻ ┐
│  [ Inbox · n ]       [ Work ]       [ Activity ] │
│  Last 12 months · 842 contributions · 96 days    │
│       Jan       Apr       Jul       Oct           │
│  M   ░ ░ ▒ ░ ▓ ░ ···························   │
│  T   ░ ▒ ░ ░ ░ ░ ···························   │
│  W   ░ ░ ░ ▓ ░ ░ ···························   │
│  T   ▒ ░ ░ ░ ░ ░ ···························   │
│  F   ░ ░ ░ ░ ▒ ░ ···························   │
│  S   ░ ░ ░ ░ ░ ░ ···························   │
│  S   ░ ░ ░ ░ ░ ░ ···························   │
│  Less  ░  ▒  ▓  █  More                         │
│  Showing cached activity · refresh failed        │
└──────────────────────────────────────────────────┘
```

The cells use GitHub's contribution levels but the shell's theme fills. The diagram is schematic; the rendered grid uses the dates and week boundaries returned by GitHub. Activity has no search or mark-read controls.

### States

| State | Presentation |
|---|---|
| Initial load without cache | “Loading inbox…” or “Loading reviews…” in the status area; list area shows a brief text state, not fabricated skeleton cards |
| Refresh with cached data | Keep rows visible and show “Refreshing…” beside freshness |
| Feed failure with cached data | Error-tone line such as “Couldn’t refresh Reviews · showing saved results”; retry remains available |
| Feed failure without cache | Error message and retry, with an empty list state |
| Empty inbox | “All caught up” |
| Empty work category | “No review requests”, “No open PRs”, or “No assigned issues” |
| Activity fetch with no cache | “Loading activity…” followed by the graph or a retryable error |
| Activity fetch failure with cache | Keep the graph visible and report that cached activity is being shown |
| Empty contribution range | Render an all-zero grid with “No contributions in this period” |
| Search with no matches | “No matches in the loaded results”; clearing search restores the rows |
| Additional pages possible | Show “N+” as a lower bound and a “Load older” control; the count becomes exact once the final, short page is loaded |
| Mark-one failure | Restore the row and show an inline action error |
| Mark-all | Accessible action name identifies that every GitHub notification thread in the account is affected, including unloaded pages |

## Information and actions

### Inbox

Fetch unread notification threads from GitHub's notifications endpoint, ordered as GitHub returns them. Display title, repository, subject type, reason, and update age. The bar count is derived only from this feed. If a loaded page is full and more may exist, display a lower-bound count (`100+` at the default page size, or `N+` for a saved custom size) rather than presenting the loaded page length as an exact total.

Selecting a notification opens its validated canonical GitHub URL. Mark it read after `xdg-open` starts successfully. A separate “Read” action marks it read without opening. If opening fails, keep it unread and show an action error. “Mark all read” calls GitHub's account-wide endpoint; the action's name/accessible description must make that scope clear.

Load pages on demand. A successful mark-read action invalidates later page offsets because removing a thread can shift the server's page boundaries. Before loading more after a mutation, refresh the inbox from page one. On a failed mark-read, restore the item's prior position and leave the valid cached pages intact.

### Work

Use GitHub Search API queries with `sort:updated-desc` for:

- open PRs with `review-requested:@me`;
- open PRs with `author:@me`;
- open issues with `assignee:@me` and `is:issue`.

Fetch only the fields displayed: title, repository, number, canonical HTML URL, and update time. Show the search result total in the category segment and load result pages on demand. GitHub Search API's 1,000-result retrieval ceiling is an explicit limit; if reached, explain that the user must narrow the results. The first pass targets GitHub.com, matching the references and current URL normalizer; GitHub Enterprise host support is out of scope.

Work rows open their canonical GitHub link. They do not mark notification threads read and do not mutate GitHub state. A work item may also appear as a notification; the feeds remain separate and are not deduplicated.

### Activity

Use the authenticated `gh api graphql` command to fetch `viewer.contributionsCollection(...).contributionCalendar` for the trailing year. Normalize only dates, contribution counts and levels, the total, and active-day count. Render the weeks GitHub returns; do not interpolate API-provided colors. Day hover text gives the date and count. Activity is read-only and has its own loading, freshness, cache, and error state. If GitHub rejects the GraphQL request, report that for Activity and retain its cache; do not change `gh` authentication or block Inbox/Work.

## Refresh, cache, and concurrency

- Continue using the installed `gh` CLI for authentication. Do not store a GitHub token in plugin state.
- Refresh inbox, all three work categories, and Activity on startup and on the configured interval. A manual refresh coalesces with an active refresh into at most one follow-up cycle. Keep Activity's cache, freshness, and error state independent so a GraphQL failure cannot blank the inbox or work views.
- Run all `gh` operations through a single coordinator so refreshes, paging requests, and read actions have a defined order and the plugin's input loop stays responsive.
- Give each `gh` command its own timeout. Context cancellation on plugin shutdown terminates the child process.
- Keep a separate snapshot and update time for the inbox, each work category, and Activity. Keep per-feed loading/error state and paging state where applicable. A failed feed retains its previous data; other successful feeds update normally.
- Keep the selected panel mode, work category, and local search draft per open panel view; share the fetched feed snapshots across views.
- Persist normalized data in the existing host state cache. Extend the existing JSON shape so version 0.2.0's `items` and `updated_at` fields still restore as a stale inbox cache. Mark every restored feed stale until refreshed successfully.
- Keep notification mark-one and mark-all updates optimistic with rollback on failure. An in-flight read marker prevents an older refresh result from reintroducing the thread. A mark-all pending state suppresses an older inbox result until the action resolves.
- Store normalized Activity data in the existing host state cache; do not persist GitHub's palette colors or credentials. Preserve the old `{items, updated_at}` cache as a stale inbox snapshot when adding Activity.
- Pass subprocess arguments as an argv array; never interpolate the query into a shell command. Validate numeric notification thread IDs before REST paths and accept only canonical `https://github.com/` targets for opening.

## Settings and compatibility

Keep the plugin ID, panel/widget IDs, refresh interval, bar display mode, and hide-zero setting. Keep `per_page` as the inbox page-size setting, default 100, and raise its maximum from 50 to GitHub's 100-item page limit. This preserves the existing setting key for installations with a saved value. Work pages use 100 items per request. Add no new settings or dependencies. Bump the plugin minor version when implementing the work queue.

## Verification gates

- Unit tests cover CLI arguments, work/activity normalization, canonical URL validation, page merging, honest count states, legacy cache restore, partial failures, optimistic rollback, and refresh/read races.
- View tests run the host's `plugin/lint.Tree` on bar, tooltip, and panel views at the manifest's 420×640 panel size, including inbox/work/activity modes and loading/error/empty/page-full states. Check the activity grid's node count and all five fill levels.
- A plugin protocol test covers mode/category selection, local search, refresh/paging routing, and notification open/read behavior without a live GitHub account or desktop opener.
- Focused Go package tests and manifest validation are the final local gate. A real `gh` account is only needed for manual end-to-end acceptance.
