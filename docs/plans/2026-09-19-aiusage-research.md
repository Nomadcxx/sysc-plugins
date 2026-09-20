# AIUsage Prior-Art Research

Research for the `org.sysc.aiusage` plugin: what five prior-art AI-usage widgets do, which
patterns transfer to a sysc-shell `plugin/v1` Go plugin, and which do not. Compiled from
six parallel deep-dives over shallow clones of each repo (agent reports, all sources read
directly). The clones lived in a session scratchpad; the pinned SHAs below are the
authoritative record of what was analyzed.

**Repos analyzed:**

| Repo | SHA (pinned) | Last commit |
|---|---|---|
| bernardopg/AiOverviewControl | `1732b60420ed5ff9cea1ac779c3a7d1aeb354b58` | 2026-09-15 |
| wsmajt/OpenTrackerBar | `6b74a4f0af6f22fb7fb407a7eae0dffa64bcf820` | 2026-05-24 |
| wsmajt/opentracker (the CLI behind it) | `5dcd36bf7ed3bc560ec568d8037bc3a8d4cd752b` | 2026-08-19 |
| mir4zul/codex-usage | `2bf3469f91bf08b6b2e7d6dfa74e6294542f69fa` | 2026-09-13 |
| bogdan-velicu/DankClaudeUsage | `38e67a4017698c4bbaf84d1c703a32d250d39265` | 2026-06-24 |
| noctalia-dev/community-plugins (ai-usagebar) | `e19f5b122685cd6631dd46cc3d83e64e9f5ccf82` | 2026-09-18 |

`ai-usagebar` (our own noctalia v5 plugin) is the primary prior art; the others are
cross-checked lineage. All runtimes are Quickshell/QML shells (DMS, noctalia) except
opentracker (Go CLI). None of them runs inside a Go plugin host — the transport differs,
but the data acquisition, math, state machines, and information architecture are what we
are porting.

**Lineage:** codex-usage ← Feiko Wielsma's Antigravity Usage ← titeya/dms-claudecode.
OpenTrackerBar ← zakstam/dms-codexbar ← steipete/CodexBar (macOS). DankClaudeUsage ← same
dms-claudecode family (antigravity leftovers visible in its i18n). AiOverviewControl is
its own thing but covers the same data sources with the widest provider surface. ai-usagebar
outsources everything to its own Rust CLI (`ai-usagebar usage --json`) and is a pure renderer.

---

## 1. Primary prior art: ai-usagebar (noctalia v5, Luau)

**Shape:** bar capsule + master/detail panel (750×430). Zero network calls, zero credential
reads — spawns one `ai-usagebar usage --json` CLI (30 s timeout) and renders its stdout. A
headless `poller` service owns the spawn; bar and panel are pure subscribers over
`noctalia.state` keys (`report`/`error`/`polling`/`refresh_queued`).

**CLI report contract** (all of it consumed):
- `entries[]`, `primary` (provider id or `provider@account`).
- Entry: `id`, `name`, `display_name`, `plan`, `status` (`ok`|`error`), `stale`, `fetched_at`,
  `metrics[]`, `sections[]`, `error`.
- Metric: `label`, `percent`, `value` (prose, e.g. "62% of monthly limit consumed"),
  `detail` (prose: "Resets in 1h 58m · 60% elapsed · 30pts ahead"), `severity`
  (`critical|high|mid|low`), `reset_at` (ISO).
- Section types: `metric`, `text` (headings have empty `value`), `block` (Credits),
  `title`, `spacer`.

**Data model:** no local math — percentages/countdowns arrive computed; severity ranks live
in the CLI ("copying its thresholds here would be a second source of truth"). Local logic:
`ratio()` clamp for progress bars; countdown formatting (`%dd %dh` / `%dh %m` / `%dm`);
DST-safe ISO parsing; `blockingMetric` — only `percent >= 100` blocks, and among several
exhausted windows the **latest reset** wins; headline selection = blocking window →
highest-percent active-session metric (multi-model plans) → CLI's first metric. Elapsed %
and pace (usage% − elapsed%, rendered `↑3`/`↓3`) are parsed out of CLI prose.

**Refresh:** interval (`refresh_minutes`, ≥1 min) + IPC + panel-open + right-click binding.
Coalescing pinned by tests: in-flight guard + `pendingRefresh` flag + 2 s minimum spawn gap;
a queued request pumps a 250 ms interval until drained. Failure taxonomy:
`spawn_failed / timed_out (stdout discarded) / not_installed (exit 127) / failed / no_data`.
Whole-report failure **keeps the last report** and flags it; a bar failure recolors the
existing glyph instead of adding nodes — there is a test asserting node-count stability
across the failure transition.

**Error semantics worth stealing:** terminal-auth errors (HTTP 401/403 + rejection text)
**remove the provider** from bar and panel; schema-mismatch errors **keep it visible** with
`—` and the diagnostic ("a response-format failure does not establish that the account is
unavailable"); unconfigured providers are dropped as never set up.

**Bar:** per provider — glyph (identity color; `error` only when the *read* failed), optional
name, 30px quota gauge (5px, radius 3) **over** a thinner elapsed-time bar (3px), percentage,
countdown, pace, stale glyph (`clock-exclamation`). `auto` mode picks up to `provider_limit`
(1–4) by severity → percent → CLI primary → lexical id; `+N` overflow marker. Empty capsule
when the pinned vendor is down ("leaves the capsule empty rather than holding a number
nothing is refreshing"). Bar re-renders on a cheap 30 s tick; countdowns recomputed from
`reset_at`, never re-spawning the CLI for time.

**Panel:** master/detail. List pane (290 px): header (glyph, title, refresh button that
*becomes* the spinner), scrollable provider rows — glyph, name, plan/error line,
right-aligned **fixed-width percentage columns** (34/48/56/76 px per layout variant), dual
2–4px progress bars, stale glyph, selection tint; rows carry stable `key`s so click handlers
survive re-renders. Sorted by headline severity → percent; terminal-auth and unavailable
rows removed. Detail pane: provider header + plan badge, chips ("stale", "Updated N min
ago · 12:40"), stale banner + retry, exhausted-quota notices (≥100%), then cards built from
`sections[]`: per-subject stack cards (multi-model plans get one card per model), reading
rows (label, severity word, raw `value`, bold right-aligned percent in a fixed column,
quota bar + elapsed bar, clock row, context row, remaining detail), Credits block cards,
note cards for warnings. A down provider gets a **record card** — last numbers as dated
text, not gauges ("a bar reads as a live reading, and nothing is reading") — unless a plan
change superseded them. Zero-balance credit blocks are hidden. Unknown section types render
as fallback text "instead of silently losing report data".

**Settings:** plugin-level `refresh_minutes` (5, 1–120). Per widget instance: `vendor`
(auto + 21 providers), `account`, `visualization` (none/gauge), `show_value`, `show_glyph`,
`glyph_position`, `provider_limit` (1–4), `extras` (countdown/pace/both/none), `show_name`,
`color_by_usage`. Deliberately mirrors the core sysmon widget's setting vocabulary.

**Unique ideas:** bottleneck headline (a 100% long window replaces the session number;
countdown targets the blocking window), pace indicator + dual-bar gauge, per-model
sub-headlines, CPU-budgeted **secret scrubbing of the entire report before publishing**
(33 secret shapes redacted, 200-char cap, byte-metered against the regex engine),
parser-failure ≠ unavailable, plan-change guard, record-not-gauge for down providers,
node-count-stability test invariant, vendor dropdown read from plugin.toml so every offered
provider must have a glyph, right-click as a *declared rebindable* action.

## 2. AiOverviewControl — provider/data half

**Shape:** bash + jq + curl. One dispatcher (`providers/get-provider-usage`, 3,368 lines)
owns all logic; each `get-<id>-usage` is a 5-line shim `exec get-provider-wrapper <id>`.
Per run: `set -uo pipefail` (not `-e` — "provider failures are data, not dispatcher
failures"), per-provider background subshells, per-curl `--max-time 8`, 45 s overall
timeout, then `record_history` and one JSON array on stdout.

**Success contract:** `{provider, source, usage{identity{providerID, accountEmail,
loginMethod}, primary|secondary|tertiary{usedPercent, windowMinutes, resetsAt,
resetDescription, displayValue?}, updatedAt, accounts[], modelWindows[], analytics},
credits{remaining}}`. **Error contract:** `{provider, source, error{code, kind, message}}`
with kind ∈ `provider|runtime|authentication|network`. Informational providers (no quota
upstream) return a *valid* usage object with `usedPercent: 0` and a dashboard URL in
`credits.remaining` — errors are data, and absence of data is never fabricated.

**Canonical windows:** 300 min (5h) → primary, 10080 (weekly) → secondary, 43200 (monthly)
→ tertiary. Window labels derive from `windowDurationMins`, never from slot position
(handles weekly-only payloads where `primary` *is* the weekly window).

**Claude collector:** token from `${CLAUDE_CONFIG_DIR:-~/.claude}/.credentials.json` (also
reads `subscriptionType`, `rateLimitTier`); `GET https://api.anthropic.com/api/oauth/usage`
with `Authorization: Bearer`, `User-Agent: claude-code/<version>`,
`anthropic-beta: oauth-2025-04-20`. Prefers the newer canonical `limits[]` array (kinds
`session`, `weekly_all`, `weekly_scoped` — model-scoped windows, active first), falls back
to flat `five_hour`/`seven_day.utilization`; also surfaces `extra_usage`
(used_credits/monthly_limit/currency). Cache tiering: 401/403 → hard authentication error
("stale data would mislead"); transient failure → last good snapshot *even past TTL*.

**Codex collector (richest path):** JSON-RPC stdio bridge to `codex app-server` —
`initialize` → `initialized` → `account/read` + `account/rateLimits/read`, reads until both
ids arrive or 8 s deadline. Launch discipline: probes `codex app-server daemon start`,
prefers proxy mode so steady-state polls never spawn a backend; `flock -n` single-flights
launches (`~/.cache/AiOverviewControl/codex-usage.lock`, 8 s wait); freshness gate answers
bursts from cache with zero backend (60 s); 900 s stale cache reused on transient
rate-limit failure — **auth failures never fall back to cache**. Teardown: close stdin →
wait 1.5 s → TERM → KILL. Response shape (fixture): `rateLimits.primary{usedPercent,
windowDurationMins, resetsAt epoch}`, `rateLimitResetCredits.availableCount`.

**OpenCode:** dual-mode. Local (default) when `opencode.db` exists → delegates to local
analytics; API (Zen Go) with key from `OPENCODE_API_KEY` or `~/.local/share/opencode/auth.json`
→ `GET https://opencode.ai/zen/go/v1/usage` → `rollingUsage/weeklyUsage/monthlyUsage
{status, resetInSec, usagePercent}` — percentages server-computed; accepted only if **all
three** windows validate, else degrades to an auth-only note ("it never fabricates a
percentage").

**Gemini:** pure auth probe — `x-goog-api-key` **header** against
`generativelanguage.googleapis.com/v1beta/models` ("URLs leak through process listings and
proxy logs"), note card "Quota visible in AI Studio". No quota exists upstream.

**CommandCode collector:** key from `COMMAND_CODE_API_KEY` or CLI-owned
`~/.commandcode/auth.json` `.apiKey` (0600, string-checked); `GET
https://api.commandcode.ai/alpha/billing/credits` (Bearer) → `windowLimits.fiveHour/weekly`
→ primary/secondary (300/10080 min), `usedPercent = clamp(used/cap*100)`,
`displayValue: "$used / $cap"`, `resetsAt` from Unix-ms; `credits` → `monthlyCredits`;
best-effort `/alpha/whoami` + `/alpha/billing/subscriptions`; alpha failure degrades to a
documented auth-only note.

**Local analytics (`get-local-analytics` + `local-analytics.jq`, shared by
codex/opencode):** rows `{session, cwd, model, date, tokens, input, output, cacheRead,
reasoning, calls, cost}`. **Dedup:** Codex `token_count` events re-emit *cumulative*
`total_token_usage` unchanged — the reducer takes **positive deltas** and drops the row on
counter reset (never negative). "Unknown ≠ $0" cost integrity: `cost` is null unless every
row in the window has a cost — "never present a partial sum as the total cost". Cache file
embeds the **source path as cache identity** (`local-analytics-$provider-cache.json` with
`.source`), TTL 120 s. Claude's cost engine prices per assistant message
(in/out/cache-read/cache-write) from the LiteLLM pricing URL, family-normalized by
`^claude-[a-z]+-[0-9]+(-[0-9]+)?$`, cached with `schema: 2` versioning.

**Alerts (`send-quota-alert`):** state in `~/.cache/AiOverviewControl/notify-state.json`
under `flock -w 10` — shared across widget instances. Dedupe key
`provider:window-kind:duration:window-bucket` (a `--migrate` mode removed pre-1.7 keys that
embedded reset timestamps, which ms-jitter turned into one notification per poll). Fire at
threshold (global 85%, 75/85/95 options, per-provider `id:percent` overrides); re-arm only
after a meaningful fall (`percent < threshold − 5`, sends `--clear`); `≥100` escalates to
critical and **updates the notification in place** (`notify-send -p -r <prev_id>`) instead
of stacking. Entries silent >60 days pruned. Window scope: displayed|all|primary.

**History:** `record_history` appends `{ts, provider, pct}` JSONL to
`~/.cache/AiOverviewControl/usage-history.jsonl` **only when `usedPercent > 0`** — 0%
informational placeholders would pollute sparklines. Lazy retention: `tail -n $max` only
when the file exceeds 2× limit (default 2000). Reads return the last 30 snapshots per
provider, oldest first. Export CSV/JSONL is loss-resilient (bad lines drop one record, not
the export), writes 0600 into `XDG_DOWNLOAD_DIR`.

## 3. AiOverviewControl — widget/UI half

**Bar pill:** ring (canvas arc) + `logo + name + "N%"` per displayed provider, joined by
`" · "`; tri-state `…` / `ERR` / `N/A`. Pill modes: `auto` (providers with `usedPercent >
0`, else all successful), `custom` (strict subset — "never widen the pill by silently
falling back"), `top` (single highest). Per-provider window overrides
(`provider:slot`, slot ∈ primary|secondary|tertiary|highest; `highest` = max usedPercent,
falls back to primary). No click handling in the widget (the shell owns the popout); hover
shows a lazy tooltip naming provider, window, and reset. Threshold:
`>=80 → error, >=60 → warning, else success`.

**Dashboard:** header (stale-aware subtitle "Updated/Stale since …", prominent Refresh),
hero card for the primary provider, **fleet rollup** (avg load across *timed quota windows
only* — `windowMinutes != null` keeps balance/analytics 0% placeholders out of the average;
peak provider as a click-to-jump link; "at risk" count; next reset), provider list with
filter chips (All/Live/Issues with counts), expand/collapse all, per-provider cards sorted
pinned-first → most-used → errors last. Each card: status (Live/Partial/Error/Waiting),
per-window usage bars, credits, account/login, **sparkline** from local history with
up/down/flat trend, stale badge, per-provider Retry. Expanded: multi-account blocks,
Claude details (windows, "Extra usage on", Today/Week/Month token & cost tiles, projected
month, top models, top projects, all-time), local-analytics telemetry (models, projects,
sessions) — always footed with the honesty note: *"Local costs may be estimates, not
invoices. — means unavailable or incomplete; $0 is not proof of free usage."*

**Refresh orchestration:** one dispatcher + parallel sidecars (claude analytics, pi,
9router, hermes); single in-flight guard per process; per-process 45 s timeouts; a
**request-id handshake** so a late exit after timeout discards its buffers; argv snapshotted
imperatively before spawn (in-flight settings changes can't mutate a running process's
command); per-provider errors inside the payload keep healthy cards; a clean exit with no
JSON is an explicit empty state, not stale data; stale = older than 2× interval,
re-evaluated on a 10 s clock.

**Settings (~17):** provider selection CSV (default `codex,claude,copilot`), pinned CSV,
density, error-provider visibility, pill mode/subset/window-override/tooltip,
`refreshInterval` (60s–30min, default 120s), history retention (500/2000/10000),
notifications on/off, global threshold, per-provider threshold CSV with inline validation,
window scope, cooldown (0/1h/6h/24h), logo color, language. Settings page groups providers
Telemetry vs Informational with an async health check (Ready/Missing/Informational),
diagnostics command list with copy buttons, history export, two-step reset.

## 4. opentracker / OpenTrackerBar

**Shape:** the widget (QML, 2 files) is a thin renderer; all logic lives in the **Go CLI**
`opentracker` v1.3.3 (`opentracker fetch <provider>` → JSON on stdout, diagnostics on
stderr). This is the prior art written in our language — its types are directly mirrorable.

**Codex provider:** auth priority env `OPENTRACKER_CODEX_ACCESS_TOKEN` (+ `_ACCOUNT_ID`) →
`$CODEX_HOME/auth.json` → `~/.codex/auth.json`, reading `tokens.access_token` /
`tokens.account_id`; `GET https://chatgpt.com/backend-api/wham/usage` with `Authorization:
Bearer`, `ChatGPT-Account-Id` (omitted when unknown), `Accept: application/json`,
`Accept-Language: en-US`, `User-Agent: codex-cli`, 30 s client timeout. 401/403 →
"authentication expired … run 'opentracker login codex'"; 429 → "rate limited … retry
later". Response (`FlexibleFloat`/`FlexibleInt64` accept number-or-string;
`clampPercent` 0–100): `plan_type`, `rate_limit.primary_window`/`secondary_window`
`{used_percent, reset_at unix, limit_window_seconds}`, `credits{has_credits, unlimited,
balance}`.

**Normalized Go types (the ones worth mirroring):**
```go
type RateWindow struct {
    UsedPercent      int     `json:"usedPercent"`
    RemainingPercent int     `json:"remainingPercent"`
    WindowSeconds    *int64  `json:"windowSeconds,omitempty"`
    ResetsAt         *string `json:"resetsAt,omitempty"` // RFC3339
}
type UsageSnapshot struct {
    AccountEmail     string      `json:"accountEmail,omitempty"`
    Plan             string      `json:"plan,omitempty"`
    Primary          *RateWindow `json:"primary,omitempty"`
    Secondary        *RateWindow `json:"secondary,omitempty"`
    CreditsRemaining *float64    `json:"creditsRemaining,omitempty"`
    UpdatedAt        string      `json:"updatedAt"`
    Source           string      `json:"source"`
}
type UsageWindow struct {
    UsedPercent   int    `json:"usedPercent"`
    ResetsAt      string `json:"resetsAt"`
    WindowMinutes int    `json:"windowMinutes"`
}
```

**Cache:** one file per provider under `~/.cache/opentracker/`, `{data, expires_at}`, 90 s
TTL checked only on read (lazy self-delete), **not atomic** (torn file → unmarshal miss →
safe degradation), no locking/single-flight (the widget's `if (proc.running) return;` guard
is the dedup). Cache read deliberately happens **after** the provider-configured check "so
auth-sensitive providers do not return data for a stale or different account." `--force`
bypasses (the widget's manual-refresh button appends it).

**HTTP:** no retries/backoff anywhere; every client a bare `http.Client{Timeout: N}`; whole
fetch capped by a 30 s context; proxy = implicit `ProxyFromEnvironment` only. Opencode
(when used) is a browser-cookie HTML scrape — fragile, bilingual-label, Polish-only
reset-time parsing; the design lesson is to avoid scraping, not to copy it.

**Widget:** default poll 120 s (1/2/5/15/30 min options), skip-if-in-flight, per-provider
error isolation, "Updated hh:mm:ss" stamp, `auto|opencode|openai|both` pill filter,
threshold 60/80, shape-normalizing accessors (`rolling||primary`, `weekly||secondary`) so
heterogeneous providers render through one template, onboarding empty-state quoting the
exact fix command.

## 5. codex-usage

**Shape:** QML widget + stdlib-only Python helper (`get-codex-usage.py`, 185 lines), bridge
= flat `KEY=value` stdout lines. Fully local: "Read quota snapshots only; never read
credentials or contact a service." No cost/billing anywhere ("figures are not billing
amounts").

**Quota snapshots:** reads `$CODEX_HOME` (default `~/.codex`) `/sessions/**/*.jsonl`;
keeps rows with `payload.type == "token_count"` and `rate_limits.limit_id in (None,
"codex")` (skips `"premium"`); consumes `primary`/`secondary`
`{used_percent, window_minutes, resets_at epoch}` + `plan_type`. **Selection:** files
sorted by mtime desc, each read **backwards in 64 KB chunks** with a byte pre-filter
(`b'"rate_limits"' in line`), only the last matching row per file, newest-wins **by event
timestamp** across files, mtime early-exit for whole files. Truncated tails tolerated.

**Window labels:** `{300: 'Five Hour Limit', 10080: 'Weekly Limit'}` else `"N minute
limit"` / key-title — future-proof against new window sizes.

**Token activity (local estimates):** `payload.info.total_token_usage.{total_tokens,
input_tokens, output_tokens, cached_input_tokens}` — only **positive deltas** of the
cumulative counters count (unchanged repeats add zero; cache miss restarts the baseline, a
known estimate error class); model attribution from `turn_context` rows (top-level `type`,
`payload.model`); local-tz day buckets; today / rolling 7d / rolling 30d windows;
`previous_week = daily[-14:-7]` trend; sessions = distinct files with activity in 7d; top-4
models. Cache: `$XDG_CACHE_HOME/codexUsage/activity-v2.json` (**schema version in the
filename**), keyed by path, signature `(st_size, st_mtime_ns)`, atomic tmp+replace.

**State machine (no error state exists):** loading ("Reading local usage…") / not-logged-in
("No quota snapshot yet. Use Codex to record your limits.") / pending bucket ("Not
recorded", ring dimmed) / stale (>30 min: amber dot, "Stale · N min ago") /
waiting-for-fresh-data (past `RESET`: keep last percentages, say "Waiting for fresh data" —
never show 0% on a rolled-over window). Parse failure silently resets to zeros. Bar pills
show `"—"` for unrecorded buckets.

**Alerts:** opt-in (default off), 80% then 90%, per bucket, **at most once per quota window
per level** — `alertHistory: {bucket: {reset, level}}` persisted in settings; 80→90
escalates; suppressed when snapshot stale or window expired ("they are not background
account monitoring").

**Refresh:** timer 15/30/60/300 s (default 30) with overlap guard; second 60 s timer
maintains countdowns and detects **wake-from-sleep** (>120 s tick gap → immediate refresh);
manual refresh sets loading. Settings: `showIcon`, `refreshSeconds`, `chartDays` (7/30),
`detailsExpanded`, `alertsEnabled` — dashboard chips write through to the same keys
(`savePreference`).

**Honesty footer:** "Local estimates · N sessions / 7 days · Quota from last local
snapshot."

## 6. DankClaudeUsage

**Shape:** QML widget + POSIX sh fetcher (`jq`+`curl`). One job: server-computed
subscription-limit percentages. Token/cost accounting an explicit non-goal ("ccusage
territory").

**Data path:** token discovery is **expiry-ordered multi-source** — candidates from
`~/.claude/.credentials.json` (`.claudeAiOauth.accessToken`, `expiresAt` epoch-ms) and
`$XDG_DATA_HOME/opencode/auth.json` (`.anthropic.access`); ordered non-expired first,
freshest expiry first, expired kept as best-effort fallback; tried until one works.
`GET https://api.anthropic.com/api/oauth/usage` with `Authorization: Bearer`,
`anthropic-beta: oauth-2025-04-20`, `User-Agent: claude-code/<version>` (from
`claude --version`, fallback literal `2.1.0`), `-m 10`. Validity = `type == "object" and
has("five_hour")` — error bodies aren't.

**Raw response schema (fixture):**
```json
{"five_hour":{"utilization":40.0,"resets_at":"2026-06-10T11:30:00.789395+00:00"},
 "seven_day":{"utilization":9.0,"resets_at":"…"},
 "seven_day_oauth_apps":null,"seven_day_opus":null,
 "seven_day_sonnet":{"utilization":0.0,"resets_at":"…"},
 "extra_usage":{"is_enabled":true,"monthly_limit":5000,"used_credits":0.0,
                "utilization":null,"currency":"EUR"}}
```
(The widget drops `seven_day_sonnet`/`seven_day_opus`/`seven_day_oauth_apps`/`extra_usage`
— a richer port doesn't have to.)

**Cache contract:** `{captured_at, five_hour:{used_percentage, resets_at epoch},
seven_day:{…}}` — utilization **floored**, ISO→epoch stripped of fractional seconds;
atomic tmp+rename; **150 s cross-instance freshness guard** because the endpoint "is
aggressively 429-throttled (safe only ≥180s)" — design floor 180 s, implemented guard 150 s,
widget poll 300 s. Exit codes `0 ok / 1 no-credentials / 2 network-parse` drive distinct
UI messages ("Couldn't read Claude usage. Is Claude Code signed in? Run `claude` then
`/login`."). Test seams: `CLAUDE_USAGE_MOCK=<file>` and `CLAUDE_USAGE_LIST_SOURCES=1` keep
all tests off the network.

**UI:** thresholds as named constants (warn 70 / crit 90 — deliberately not settings, "to
keep the settings UI small"); pill `✳ --` (no data) / rings / numbers `"✳ 15% · 4%"`; popout
rows "N% used · resets in 5d 14h" (countdowns recomputed client-side from `resets_at`, 1 s
tick); footer "updated Xm ago" → "stale (Nm) — is Claude Code signed in?" beyond 60 min.
Settings: exactly three (`displayStyle`, `showFiveHour`, `showWeekly`).

---

## 7. Cross-cutting synthesis

### 7.1 The convergent normalized model

Every project independently converged on the same record — this is the core of our data
model:

```
window   {label, usedPercent 0–100, windowMinutes (300|10080|43200|…), resetsAt, source}
provider {id, displayName, plan, account/identity, windows[2–3], credits?, updatedAt,
          stale bool, error{kind, message} | ok}
report   {providers[], capturedAt, primary?}
```

- Labels from `window_minutes` (`≤300 → "Session"/"5-hour"`, `≤10080 → "Weekly"`, else
  "Monthly"), never from slot position.
- `displayValue` / raw `value` prose as the escape hatch for data that isn't a percent
  (cost, tokens, balance) — never fake a percentage.
- Errors are **data inside the report**, with a kind taxonomy
  (`authentication|network|runtime|provider`), never process failure.

### 7.2 Data-source strategy per tool

| Tool | Server-authoritative | Local snapshots | Notes |
|---|---|---|---|
| Claude Code | `api.anthropic.com/api/oauth/usage` (OAuth token from `~/.claude/.credentials.json`; headers `anthropic-beta: oauth-2025-04-20`, UA `claude-code/<ver>`); 429-hard → ≥180 s poll floor | project JSONL + pricing tables (ccusage-style; AIOC) | endpoint returns `utilization` floats identical to `/usage`, incl. `seven_day_*` and `extra_usage` credits |
| Codex | (a) `codex app-server` JSON-RPC bridge (AIOC); (b) `chatgpt.com/backend-api/wham/usage` REST with `auth.json` token (opentracker) | `~/.codex/sessions/**/*.jsonl` `token_count.rate_limits` (codex-usage) — zero credentials, zero network | session-file route is "last recorded value", needs the stale-state machine |
| OpenCode | `opencode.ai/zen/go/v1/usage` (key from auth.json) | local db / session analytics | HTML scrape exists but is fragile — avoid |
| Gemini | auth probe only (no quota upstream) | — | note card, never a fabricated percentage |
| CommandCode | `api.commandcode.ai/alpha/billing/credits` (key from `~/.commandcode/auth.json`) | — | native ecosystem fit for sysc |

### 7.3 State machines & error semantics

- **Stale-keep everywhere:** a failed refresh never blanks the UI; last good data stays,
  flagged ("Stale · Nm", amber dot, stale banner + retry). DankClaudeUsage: dimmed `✳ --`
  only when there has *never* been data.
- **Never show 0% after a window rolls over** — "Waiting for fresh data" with old numbers
  (codex-usage).
- **Error taxonomy drives removal vs retention** (ai-usagebar): terminal auth (401/403 +
  rejection) → remove the provider; schema mismatch → keep visible with `—`; unconfigured →
  drop; AIOC adds: auth failures never fall back to stale cache.
- **Down-provider rendering:** record card (dated text, not gauges) + plan-change guard
  (ai-usagebar); explicit empty state ≠ stale data (AIOC).
- **Node/layout stability:** a failure transition must not add/remove bar nodes — recolor
  instead (ai-usagebar test invariant).

### 7.4 Refresh, caching, wake

- Poll + **skip-if-in-flight** everywhere; manual refresh = force flag.
- Coalescing with a minimum spawn gap and a queued-request pump (ai-usagebar).
- Cache with **fresh TTL** (60–120 s) → stale-cache fallback for *transient* failures only;
  cache identity includes the data source (AIOC); atomic writes (tmp+rename) or safe
  degradation to cache-miss (opentracker).
- Cross-instance guard (150 s) for rate-limited endpoints (DankClaudeUsage).
- **Wake-from-suspend detection** via tick-gap (>120 s → immediate refresh) (codex-usage).
- Staleness = older than N× interval (2×, AIOC) or fixed 30/60 min (codex-usage /
  DankClaudeUsage).
- Countdowns tick **locally** from stored `resets_at`; never wake the data source for time.

### 7.5 Alerts

- Thresholds 60/70/80/85/90 vary by project; the useful invariants are: fire once per
  window per level, escalation 80→90, dedupe key excludes jittery timestamps, re-arm with
  hysteresis (threshold − 5) + explicit clear, critical updates the notification in place,
  suppress when data is stale or window expired, prune old state.

### 7.6 UI patterns

- Bar: percent text + one glyph; tri-state placeholders (`…` / `--` / `ERR` / `N/A`);
  auto = highest percent (or severity-ranked pick up to a limit); `+N` overflow; optional
  dual-bar gauge (quota over window-elapsed); pace `↑n`/`↓n`.
- Panel: master/detail (ai-usagebar) or scroll of cards (AIOC); per-window rows = label +
  percent (fixed-width column) + progress + countdown + elapsed context; "Updated Xm ago ·
  HH:MM" chips; refresh button that becomes the spinner; exhausted-quota notice (≥100%
  with renew time); onboarding empty-state quoting the exact fix command; record cards;
  hidden zero-credit blocks; honesty footer.
- Sorting: severity → percent → primary; pinned first; errors last (or removed).

### 7.7 Security & honesty invariants

- Tokens only ever sent to the tool's own API; never logged, never in cache files
  (DankClaudeUsage README states it; AIOC sends keys in headers, not URLs).
- **Scrub external text before publishing** (ai-usagebar's 33-shape scrubber) — relevant if
  we ever surface raw provider errors.
- "Last recorded value, not a live account query" semantics stated to the user
  (codex-usage README) + "Local costs may be estimates, not invoices" (AIOC).
- Cache/cookie files 0600 (opentracker's 0644 cookie file is called out as a hardening
  miss).

---

## 8. sysc-shell mapping (verified against the pinned plugin/v1)

Verified against `github.com/Nomadcxx/sysc-shell@v0.0.0-20260916001919-a402dac61a4d`
(`plugin/v1/{node,message,client,framing,ident}.go`, host `internal/plugin/manifest.go`,
`internal/render/iconfont.go`):

**What we can express directly:**

| Prior-art pattern | plugin/v1 mechanism |
|---|---|
| Quota / elapsed bars | `progress` node (`Value` 0–1, host renders a meter) |
| Percent column, countdowns, labels | `text` with `tabular` for numbers; `tone: subtle` for context lines |
| Threshold coloring | `tone`: normal → `accent` (warm band) → `error` (≥ crit) — **no warning tone** |
| Provider cards / rows | `row`/`column` trees; fixed-width numeric columns via `width` |
| Master/detail list | `list` node (panel-only) or row/columns |
| Refresh button, per-provider retry, record card | `button` (`id`/`name`/`role`/`events: [activate]`) |
| Stale glyph | `icon` node with `tone: error`/`accent` |
| Duration input (none needed) | `text_input` is bar-forbidden, panel-legal |
| "Updated Xm ago", honesty footer | `text`, `tone: subtle`, `size`-less caption |
| Force refresh | `Call(ctx, CallPanelOpen/…)` unaffected; plugin-side force flag on activate event |

**Client-side machinery available:** `Snapshot`/`Patch` (patch with snapshot fallback),
generic `Call` for `state.get/set/list` (needs `state` capability),
`panel.open`/`panel.close` (`panels`), `notify` with urgency + actions (`notifications`),
`plugin.status` (ok/busy/error — surfaces "signed out" without breaking the bar), settings
pushed via `SettingsChanged` after manifest-schema validation by the host. Limits:
MaxNodes 1024, MaxDepth 16, MaxTextBytes 64 KiB, UpdatesPerSecond 60 (burst 120) — a 30 s
bar tick and 1 s panel tick are cheap; 4 MiB total state.

**Manifest:** schema 1, `org.sysc.aiusage`, capabilities `panels/settings/state`
(+`notifications` only if we adopt notify alerts), `widgets: [{id: bar}]`, `panels:
[{id: panel, width 420–750, height 430–640, placement attached}]`, typed settings
(`bool|int|float|string|select` cover everything the prior art needs).

**Two gaps that force design decisions:**

1. **Protocol minor.** The pinned module has **no minor-2 fields** (no `fill`/`radius`/
   `bold`/`size`/`disabled`/`center_x`/`pin_end` — the node doc comment says sizes are
   "deliberately absent until the shell has a measure path"; `docs/plans/2026-09-19-…`
   records minor 2 as landed in sysc-shell `cc684ee`, but our go.mod pin predates it).
   Design against tone+tabular only (current pin), or bump the pin and use minor 2 (the
   kdeconnect plan does exactly this move). The card look (fill `card` + radius, `title`
   sizes) wants minor 2.
2. **Icon catalogue.** 29 names (weather, camera/record, notifications, close, schedule,
   ghost, sysmon gauges…) — **no AI/brain/sparkle glyph**. Closest today: `ghost`, `speed`,
   `schedule`. Prior art uses `brain` (ai-usagebar) and `monitoring` (AIOC). Adding glyphs
   is a sysc-shell font update (the README documents the process; kdeconnect did the same).
   Choose: ship with an existing glyph, or extend the catalogue as part of this work.

**What has no equivalent (accepted losses):** rings/canvas/sparklines/animations (→
`progress` meters + text trends `↑/↓`), hover tooltips (→ panel detail pane), per-second
countdown blinking, alpha tints/borders, in-popout scroll-to-provider, QML settings
components (→ host-rendered settings from the manifest schema).

---

## 9. Open questions carried into design

1. **Tool scope v1** — Claude Code + Codex (both have proven paths) or Claude only?
   CommandCode is a native extra (endpoint + `~/.commandcode/auth.json` documented above).
2. **Data-source strategy per tool** — OAuth endpoint (server-authoritative, 180 s floor)
   vs Codex session-file snapshots (local-only) vs both; recommend endpoint for Claude,
   session files for Codex (that's where its quota data lives; zero credentials).
3. **Token/cost analytics** — in or out? DankClaudeUsage explicitly delegates to ccusage;
   AIOC/codex-usage do local estimates with the "unknown ≠ $0" integrity rules. Recommend
   out of v1.
4. **Protocol pin** — design against the current pin or bump to minor 2 (`cc684ee`).
5. **Icon** — existing glyph vs catalogue extension.
6. **Bar pill content** — icon+percent of auto-selected window (DankClaudeUsage/AIOC style)
   vs ai-usagebar's provider_limit chips with dual gauges; what `auto` means.
7. **Panel layout** — master/detail (ai-usagebar, 750×430) vs single scroll of cards
   (AIOC) vs compact two-window list (codex-usage/DankClaudeUsage, ~420×300).
8. **Settings surface** — small constants-led set (DankClaudeUsage: 3) vs richer
   (ai-usagebar: 11; AIOC: ~17). What defaults.
9. **Alerts** — in-panel accent badge (v1) vs `notify` capability with dedupe keys +
   hysteresis (AIOC pattern) vs none.
10. **History/sparklines** — record `{ts, provider, pct}` JSONL for future trends
    (>0 filter) even if v1 renders no chart?
11. **Subprocess vs in-process collectors** — every prior art renders a foreign-language
    collector; we ARE Go. Recommend in-process collectors + the opentracker-shaped
    snapshot types, with the collector→renderer seam kept as a package boundary (the
    codex-usage KEY=value contract lesson: define the seam even when transport collapses).
