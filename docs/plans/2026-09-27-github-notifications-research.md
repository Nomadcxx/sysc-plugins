# GitHub notifications research: prior art and design inputs

Research for the next design pass on `org.sysc.github-notifications`. I read
the four Noctalia plugins at community-plugins commit
[`8954b95`](https://github.com/noctalia-dev/community-plugins/commit/8954b95ed2819fea924f662464e7b9c3a981e59c)
and the DMS plugin at
[`6a02487`](https://github.com/psyreactor/dms-githubNotifier/commit/6a0248740cf1bd5b94d15be237dc65e12fc4c2fa),
then compared them with this repository's Go port. The Noctalia manifests
identify their plugins as MIT. The DMS repository has no SPDX license metadata
or license file in the inspected tree, so this report treats it as behavioral
reference only.

## Findings at a glance

| Reference | What it actually does | Best design input | Main limit |
|---|---|---|---|
| [Noctalia GitHub Notifications](https://github.com/noctalia-dev/community-plugins/tree/8954b95ed2819fea924f662464e7b9c3a981e59c/github-notifications), v0.1.0 | Unread notification-thread inbox | The closest product match: reason and subject metadata, cached state, safe links, individual and bulk read actions | Refreshes are dropped while another refresh runs; it fetches at most 50 threads |
| [Noctalia GitHub Activity](https://github.com/noctalia-dev/community-plugins/tree/8954b95ed2819fea924f662464e7b9c3a981e59c/github-activity), v1.4.0 | Contribution calendar and streaks | A useful, separate Activity view with stale-cache handling and explicit freshness | Contribution volume is not notification urgency |
| [Noctalia Git Companion](https://github.com/noctalia-dev/community-plugins/tree/8954b95ed2819fea924f662464e7b9c3a981e59c/git_companion), v1.3.0 | Authored PRs, assigned issues, and requested reviews | Review-requested work queue; per-list errors and scope filtering | It searches work items rather than reading notifications; GitHub target branches are unavailable to its query |
| [Noctalia GitHub Kanban](https://github.com/noctalia-dev/community-plugins/tree/8954b95ed2819fea924f662464e7b9c3a981e59c/github-kanban), v0.1.3 | Read-only GitHub dashboard spanning profile, notifications, work, following, repositories, and Actions | Independent section failures, stale sections, field projection, local filters | 70 settings and many dependent requests; its badge count comes from a capped 20-notification slice |
| [DMS GitHub Notifier](https://github.com/psyreactor/dms-githubNotifier/tree/6a0248740cf1bd5b94d15be237dc65e12fc4c2fa), v1.2.1 | Counts and lists authored PRs plus assigned issues | Serialized refresh, coalescing, watchdog, bounded CLI calls | No notification inbox; DMS source has no declared license |

The first reference is the only notification inbox. Activity, Companion,
Kanban, and DMS each solve a different problem. Combining every surface would
make this plugin a general GitHub client.

## Reference details

### Noctalia GitHub Notifications

The service calls `gh api notifications?all=false&per_page=N`, with `N` limited to 25 or 50 and 50 by default. It uses GitHub CLI's existing sign-in and stores no token. The panel shows a compact count, subject, repository, reason, and relative time. The normalizer maps common reasons and subject types to labels, icons, and tones; it builds canonical GitHub URLs from recognized API URLs and validates links before opening them. The panel offers refresh, open-and-read, read-without-opening, and mark-all actions. The source and behavior are in the [README](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-notifications/README.md), [service](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-notifications/service.luau#L83-L246), [normalizer](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-notifications/lib/notifications.luau#L7-L47), and [panel actions](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-notifications/panel.luau#L31-L92).

The service persists a normalized snapshot under Noctalia's plugin data directory and marks restored data stale. A failed mark-read restores the thread. It tracks thread IDs being marked read so a concurrent refresh cannot put those threads back, and it drops a refresh result while mark-all is pending. It does not queue refresh requests: `refresh()` returns when `inFlight` is true. Opening a notification starts `xdg-open` and marks the thread read after checking that the executable exists; it does not wait for the browser command to succeed.

### Noctalia GitHub Activity

This plugin uses GraphQL's `contributionCalendar` to render the account's yearly
contribution heatmap, today's count, and current and best streaks. It has no
notification data. Its [query](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-activity/sync.luau#L6-L24)
and [cache and refresh flow](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-activity/sync.luau#L104-L189)
show compact response projection, cache validation, stale/fresh state, refresh
errors, and settings-driven intervals. The bar metric can vary per widget
instance. The calendar is useful profile context, but it must stay separate
from unread counts and work urgency.

### Noctalia Git Companion

Git Companion reads three kinds of work: PRs authored by the user, issues
assigned to the user, and PRs with review requested. GitHub uses
`gh search prs/issues`; the other adapters use `glab` and `tea`. Its panel uses
separate tabs and lets users scope results by repository or owner. It keeps
prior rows visible when a fetch fails and shows the CLI error beside that list.
See the [README](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/git_companion/README.md),
[query construction](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/git_companion/service.luau#L81-L149),
and [fetch handling](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/git_companion/service.luau#L227-L245).

If the product expands beyond notifications, a focused review/work queue is the
useful extension. Search results omit notification reasons and thread state, so
the two feeds answer different questions. Multi-forge support, Gitea's
synthetic working directory, GitLab-only branch badges, and 14 settings add
costs to a focused inbox. GitHub search also omits the target branch needed for
Companion's branch badge.

### Noctalia GitHub Kanban

Kanban is a read-only dashboard with no workflow columns or drag actions. It
combines profile and contribution data, notifications, PRs, review requests,
issues, Actions, followed-user public events, releases from starred
repositories, and recent repositories. Its
[README](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-kanban/README.md)
describes the sections. The
[GitHub helper](https://github.com/noctalia-dev/community-plugins/blob/8954b95ed2819fea924f662464e7b9c3a981e59c/github-kanban/lib/github.luau#L27-L50)
shows the endpoints and field projections.

The service keeps sections independent. It preserves cached rows when an
endpoint fails, publishes partial status, and projects only fields the panel
uses. These patterns can help if a future design adds more data sources. The
manifest declares 70 settings, refresh work grows with followed users and
repositories, and the panel is 860×620. The service fetches at most 20
notifications with `all=true` and computes the unread badge from that list
after applying the history window. The badge counts this dashboard slice, not
all unread threads. Keep the dedicated unread endpoint as the badge authority.

The Following view approximates GitHub's personalized feed with followed users'
public events and recent releases in starred repositories. The README says
GitHub does not expose the web feed as an API. Label this as a public-events
approximation, not a complete account feed.

### DMS GitHub Notifier

DMS GitHub Notifier checks `gh --version` and `gh auth status`, fetches the
profile, then runs two capped searches: open PRs authored by the user and open
issues assigned to them. It supports an organization filter and displays
result cards and a total badge. It reads no GitHub notification threads. The
[README](https://github.com/psyreactor/dms-githubNotifier/blob/6a0248740cf1bd5b94d15be237dc65e12fc4c2fa/README.md)
documents those queries.

Its refresh flow serializes calls, queues one refresh if another arrives,
rejects callbacks from an old generation, and clears a stuck refresh with a
watchdog. Each widget instance owns its own queries, so multiple placements
duplicate API work. The
[widget source](https://github.com/psyreactor/dms-githubNotifier/blob/6a0248740cf1bd5b94d15be237dc65e12fc4c2fa/GitHubNotifierWidget.qml#L165-L294)
uses argument arrays for process calls. These are useful reliability
patterns; the source itself should not be copied without a license.

## Current sysc-plugins baseline

The repository already has `org.sysc.github-notifications` v0.2.0. It fetches
unread threads through `gh`, persists a cache in host state, marks individual
and all threads read with optimistic rollback, normalizes canonical GitHub
URLs, and sends a desktop toast when the unread count grows. The current panel
event handler passes the thread ID to `xdg-open` instead of the normalized URL,
so opening a notification is currently broken. Its defaults are a
120-second refresh and 50 items. See [manifest](../../plugins/github-notifications/manifest.json),
[CLI adapter](../../plugins/github-notifications/gh.go),
[session state](../../plugins/github-notifications/service.go),
[normalization](../../plugins/github-notifications/notifications.go),
[view tree](../../plugins/github-notifications/view.go), and
[plugin entry point](../../cmd/sysc-plugin-github-notifications/main.go).

The panel renders title, repository, reason, and time as plain text controls.
The model keeps subject type and an `IsFailure` flag; the view uses neither.
It has no search, reason filter, or repository filter. The Go port also lacks
the source service's `pendingThreads` and `markAllPending` guards. Mutexes
protect in-memory fields, but overlapping refresh and mark operations can
apply results in the wrong order. A manual refresh can overlap the timer
refresh, and CLI calls have no per-request timeout.

## Design inputs

### Keep the inbox authoritative

Use GitHub's unread notification threads as the source of the bar count. Keep the `gh` CLI as the authentication boundary, pass arguments directly, and keep credentials outside plugin state. Preserve a stale cache and report authentication, rate-limit, and network failures without replacing useful cached rows.

Build triage around data already present in each notification: repository, subject type, reason, title, and updated time. Reason and repository filters, useful type icons, security emphasis, and a clear empty/stale/error state can improve triage without adding unrelated APIs. Keep open, mark-one-read, and mark-all-read as distinct actions. State plainly that mark-all calls the account-wide endpoint even when the panel only displays a capped page.

Carry the source's read-race protection into the Go state machine, then add DMS's single-flight/coalesced refresh and watchdog behavior. Use the host's declarative row/column/list controls and accessibility names. The Noctalia panel, QML layouts, animations, and widget-instance APIs do not port directly. The current sysc-shell UI rules require geometry checks against the host's fixed panel size; see [`plugin-ui-rules.md`](../plugin-ui-rules.md).

### Scope choice

1. **Focused inbox:** unread count, cached inbox, reason/repository filters,
   type-aware cards, open/read actions, and reliable refresh/action ordering.
2. **Inbox plus work queue:** add distinct review-requested, authored-PR, and
   assigned-issue views. Keep notification threads and current work in
   separate panel modes because their identities, counts, and actions differ.
3. **Triage plus Activity (recommended):** retain those two modes and add the
   contribution calendar as a separate Activity mode. The user raised the
   missing graph after reviewing the design. It uses one GraphQL query, has its
   own cache and freshness, and does not affect notification counts or work
   ordering.
4. **Personal dashboard:** add Kanban's following feed, repositories, Actions,
   releases, and profile sections. That broadens the goal, grows request and
   layout costs, and inherits the approximate Following feed and capped unread
   count.

Option 3 incorporates the contribution graph without turning the plugin into a
general dashboard. It reuses `gh` for authentication and keeps each of the
three information types visibly separate.

### Contribution graph fit in sysc-shell

The shell's `KindGraph` renders up to 64 normalized sparkline samples; it does
not encode dates or weekday rows. `KindScheduleGrid` lays out timed events over
one to seven days, so it is not a year calendar. The panel can still render the
GitHub heatmap with small `KindRow`/`KindColumn` cells: about 53 weeks × 7 days
plus labels, comfortably below the 1,024-node view limit. Map contribution
levels to semantic shell fills (`surface`, `container`, `card`, `chip`,
`accent`) and ignore GitHub's literal color values so the view follows the
active light or dark theme. Geometry-check the Activity tree at the existing
420×640 panel size.

## Source record

- Noctalia community plugin manifests declare MIT: `github-notifications/plugin.toml`, `github-activity/plugin.toml`, `git_companion/plugin.toml`, and `github-kanban/plugin.toml`, all at commit `8954b95ed2819fea924f662464e7b9c3a981e59c`.
- DMS repo snapshot `6a0248740cf1bd5b94d15be237dc65e12fc4c2fa` has no repository license field or `LICENSE` file in its root tree. Treat implementation and artwork as unavailable for copying unless licensing is clarified.
- No code was copied during this research.
