# AI Usage (aiusage) Plugin Design

Companion documents: `2026-09-19-aiusage-research.md` (prior-art evidence, pinned SHAs —
every pattern below cites where it was stolen from) and `2026-09-19-aiusage-implementation.md`
(the task plan this design feeds). Amended 2026-09-20 by the design audit (findings cited
inline as B/M/I from the legality audit and 1–9 from the parity/privacy audit).

**Goal:** `org.sysc.aiusage` — a bar pill + master/detail panel tracking AI-plan quota
windows (percent used, reset countdowns, pace) for Claude Code, Codex, Command Code,
Ollama, MiniMax, and Synthetic, with desktop alerts at threshold.

**Architecture:** One Go plugin process. Six in-process collectors behind a `Collector`
interface normalize every vendor's data into one `Window` model; a serial fetch loop with
backoff publishes immutable `Report` snapshots; bar and panel are pure view-tree builders
over the snapshot. Two sysc-shell side tasks: the `ai-usage` icon glyph (S1), and wire
**minor 4** (S2) — the visual-parity surface that carries what the references render:
hover tooltips, history sparklines, list rhythm separators, semantic shapes, and the
gauge's dimmed/absent state. Every minor-4 addition maps 1:1 onto a renderer the shell
already ships (`ui.Node.Tooltip`, `KindGraph`/`paintGraph`, `KindSeparator`, `ui.Shape`,
`ui.Node.Absent`) — plumbing, not new rendering; the parity plan's argument, one minor
further.

**Tech Stack:** Go 1.26, `plugin/v1` (JSONL, wire minor 4 — the go.mod pin
`v0.0.0-20260919230303-ec98abffd13e` carries minor 3; S2 extends it), `net/http` +
`encoding/json` only — no external commands (`requires.commands: []`, unlike every
prior art which shells to curl/python/CLIs).

**Repos:** `~/sysc-shell` (Tasks S1–S2: icon glyph, wire minor 4) and `~/sysc-plugins`
(Tasks P1–P8: the plugin). `Validate` rejects unknown kinds and fields (audit I6), so
the builder serves three trees keyed off `HostHello.Supported` (scanned plugin-side —
the v1 client hardcodes `{1,0}` and never inspects it, audit M1): minor 4 (full parity),
minor 3 (gauge, no tooltips/graph/separator/shape/absent), minor ≤ 2 (meter fallback).
Degradation is for old hosts only — it is never an excuse to cut a wanted visual.

---

## Locked decisions (brainstorm 2026-09-20; amended by audit)

| # | Decision | Choice | Why / source |
|---|---|---|---|
| D1 | Tool scope v1 | claude, codex, commandcode, ollama, minimax, synthetic | user; each has a proven path (research §7.2 + local repo report) |
| D2 | Data sources | Claude via OAuth usage endpoint; Codex via session-file snapshots; others via direct APIs | user; DankClaudeUsage (endpoint = server-authoritative, zero pricing tables), codex-usage (local-only honesty) |
| D3 | Token/cost analytics | **out** of v1 — non-goal | user; DankClaudeUsage delegates it to ccusage; AIOC carries it as a separate heavy engine |
| D4 | Protocol | declare wire **minor 4**: `tooltip` field, `graph` kind + `values`, `separator` kind, `shape` field, gauge `absent` — all mapped onto existing shell renderers | user directive: parity over stretching; the parity plan's minor-2 move, continued |
| D4b | Visual stance | design from what the references render, then grow the vocabulary; degrade trees exist for old hosts, not to cut features | user, 2026-09-20: "work from what we want… genuine parity… so be it" |
| D5 | Bar | one chip per widget instance (vendor pinned or auto) | user; matches timer/world-clock pills; ai-usagebar's provider_limit churn skipped |
| D6 | Panel | master/detail, 750×430 attached | user; the owner's own ai-usagebar renderer |
| D7 | Radial dials | bar (22px, sysmon-style) + panel hero, via the existing `gauge` wire kind, with `absent` dim-state (minor 4) | user; `ui.KindRadialGauge` painted by the shell and on the wire since minor 3 (audit B3); `Absent` exists on `ui.Node` and in `paintGraph`'s contract |
| D8 | Icon | extend the catalogue (`ai-usage`) | user; kdeconnect precedent (font build via `internal/render/icons/build.py`) |
| D9 | Alerts | `notify` + in-panel notices; 85/95 settings + fixed 99 depleted; 5% hysteresis; persisted dedupe | user; local plugin's levels/hysteresis, AIOC's dedupe-key lessons, and the local repo's own handover note ("alerts do not survive restart") closed via `state.set` |
| D10 | History | record-only JSONL (meaningful percents only, 2000 cap, lazy trim — shape in §4); rendered as a `graph` sparkline + trend in the detail pane | user; AIOC `record_history`, adapted per-window; codex-usage's chart, percent-only |
| D11 | Refresh | 300 s default, 60–3600 range; per-collector floors (Claude 180 s at scheduler level + 150 s cross-instance cache guard) | user; DankClaudeUsage's 429 discipline |
| D12 | Collectors | in-process Go, no subprocess; collector→renderer seam kept as a package boundary | research §9.11; opentracker proves the shape in Go |

## Non-goals (v1)

Token/cost/pricing math; sparkline or chart rendering (history data flows, visuals deferred);
multi-account (`provider@label` identities); HTML scraping (opentracker's opencode path);
desktop widgets (no wire view kind); animations; per-second countdown ticks (60 s clock);
Gemini/Cursor/Kimi/Z.AI providers (the collector seam makes each one file +
one table entry + one fixture later). OpenCode Go moved into v1 with AIU-20
(2026-09-24); see the remediation ledger for its opt-in setup and API contract.

Also accepted simplifications, named so they read as decisions rather than drift
(audit D/INFO): single chip ⇒ no `+N` overflow marker and no pinned-first sorting;
in-process collectors ⇒ no refused-start counter; the four-kind provider error taxonomy
collapses to NeedsSetup/Fault; `extras` defaults to `none`, unlike ai-usagebar's
countdown-default — deliberate restraint; no plan-change guard on record cards
(single-account v1); no `warning`/`success` wire tones — the shell theme has no such
palette tokens (verified) and the primary reference uses the theme's secondary/accent
for the mid band; mid-band = `accent`. Animation lives shell-side if ever (meter/gauge
easing is host rendering, not wire); token-quantity charts stay out with the analytics
non-goal, but the **percent-history sparkline is in** via `graph` (D10's data finally
renders).

---

## 1. Identity & manifest

```json
{
  "schema": 1,
  "id": "org.sysc.aiusage",
  "name": "AI Usage",
  "version": "0.1.0",
  "protocol": {"major": 1, "minor": 4},
  "exec": "bin/sysc-plugin-aiusage",
  "capabilities": ["notifications", "panels", "settings", "state"],
  "requires": {"commands": []},
  "services": [{"id": "aiusage"}],
  "widgets": [{"id": "bar", "settings": [ /* instance settings, §5 */ ]}],
  "panels": [{"id": "panel", "width": 750, "height": 430, "placement": "attached"}],
  "settings": [ /* plugin settings, §8 */ ]
}
```

Makefile gains `sysc-plugin-aiusage:aiusage`; README table row:
`| AI Usage | org.sysc.aiusage | 0.1.0 | new; patterns ported from noctalia ai-usagebar + DMS usage widgets |`.

## 2. Collectors

Seam (the local plugin's `backend`-string dispatch, typed):

```go
type Collector interface {
    ID() string                                     // "claude" | "codex" | ...
    Fetch(ctx context.Context) (ProviderReport, error)
}
// registry: map[id]func(cfg Config) Collector — a new provider is one file,
// one entry, one fixture. Nothing else changes.
```

Typed error split (local plugin §3: "classify on structured fields, never sniff prose"):

```go
type ErrSetup struct{ Tried []string } // no credential found; names every path tried
// any other error is a fault → retainLastGood + Stale
```

| Provider | Acquisition | Normalization quirks (confined to the parse layer) |
|---|---|---|
| claude | `GET https://api.anthropic.com/api/oauth/usage`; headers `Authorization: Bearer`, `anthropic-beta: oauth-2025-04-20`, `User-Agent: claude-code/<ver>` (`claude --version`, fallback `2.1.0`); token from `~/.claude/.credentials.json` `.claudeAiOauth.accessToken` (`expiresAt` epoch ms, expiry-ordered) | prefer canonical `limits[]` (kinds `session`, `weekly_all`, `weekly_scoped`, active-first), fall back to flat `five_hour`/`seven_day` `utilization` (floor → percent); ISO `resets_at` → RFC3339 parse; validity = object with quota block — error bodies rejected (DankClaudeUsage); 401/403 = fault **never** served from cache (AIOC) |
| codex | local only: `$CODEX_HOME` (default `~/.codex`) `/sessions/**/*.jsonl`; files mtime-desc, backwards 64 KB chunk scan, byte pre-filter `"rate_limits"`, last match per file, newest-wins **by event timestamp**; rows `payload.type=="token_count"`, `rate_limits.limit_id ∈ {nil,"codex"}` | `primary/secondary {used_percent, window_minutes, resets_at epoch-s}`; labels from `window_minutes` (300→"5h", 10080→"Wk", else "N min"); semantics = **last recorded value**, stated in the panel footer (codex-usage) |
| commandcode | `GET https://api.commandcode.ai/alpha/billing/credits`; bearer from `COMMAND_CODE_API_KEY` → `~/.commandcode/auth.json` `.apiKey` | `windowLimits.fiveHour/weekly` → primary/secondary (300/10080); `usedPercent = clamp(used/cap*100)`; `DisplayValue = "$used / $cap"`; `resetsAt` unix-ms; `monthlyCredits` → Credits (AIOC collector) |
| ollama | `GET https://ollama.com/api/usage` + best-effort `POST /api/me` (identity collapse to null on failure — "a plan name is not worth failing a refresh over"); bearer = key setting | `limits.{session,weekly}.usage` are 0–1 fractions → ×100; session `windowMinutes = 0`; weekly = 10080 with **computed** reset: next Monday 00:00 UTC (local plugin `nextWeeklyResetSeconds`; math verified against ground truth) |
| minimax | `GET https://api.minimax.io/v1/api/openplatform/coding_plan/remains`; bearer: key setting → `~/.minimax/api_key` → codexbar `config.json` borrow (`.providers[id=="minimax"].apiKey`) | `base_resp.status_code != 0` ⇒ error **even on HTTP 200**; `model_remains` entry `model_name=="general"` preferred, first entry as fallback (matches ground truth); remaining→used (`100 − current_interval_remaining_percent`); `end_time` epoch-ms → s; 300/10080 windows (local plugin `parseMiniMaxOutput`) |
| synthetic | quotas endpoint on the Synthetic API (OpenAI/Anthropic-compatible provider; docs at dev.synthetic.new); bearer: key setting → env `SYNTHETIC_API_KEY` → key file | **P0 pins the exact URL, headers, and payload shape by a live capture, committed as a fixture with a provenance comment** — nothing is parsed before it exists. Parser maps captured quota windows into `Window`s; a payload without percents renders an informational note card (`Window{HasPercent: false, DisplayValue: …}`) — never a fabricated percentage (AIOC's `displayValue` escape hatch) |

HTTP discipline: per-fetch `context.WithTimeout(ctx, 20*time.Second)` (local plugin's
`COMMAND_TIMEOUT_MS`); no retries; keys go in headers, never URLs ("URLs leak through
process listings and proxy logs" — AIOC); `http.Client{Timeout: 30 s}` ceiling.

Terminal-auth divergence, deliberate (audit finding 6): ai-usagebar **removes** a
provider on 401/403; this design keeps the row visible as Fault with its Retry button —
an expired Claude token is a chore to fix, not an absence, which is the same reasoning
its NeedsSetup rows use. Documented here so the divergence is a decision, not drift.

## 3. Data model

```go
type Window struct {
    Key              string    // "primary" | "secondary" | "tertiary"
    Label            string    // "Session" · "Weekly" · "Monthly" · provider wording
    ShortLabel       string    // "5h" · "Wk" · "Mo"
    UsedPercent      float64   // 0–100, clamped; meaningful only when HasPercent
    HasPercent       bool      // false ⇒ informational card (displayValue only)
    WindowMinutes    int       // 0 = unbounded/unknown (history filter uses this)
    ResetsAt         time.Time // zero = unknown ⇒ no countdown, no pace
    ResetDescription string    // provider wording, kept verbatim
    DisplayValue     string    // escape hatch: "$12.40 / $50", "350/500 requests"
}

type ProviderReport struct {
    ID, Name, Plan, Account string
    Windows                 []Window // normalized, display order
    Credits                 *float64
    State                   State // Fresh | NeedsSetup | Fault | NoData
    Stale                   bool
    UpdatedAt               time.Time
    Err                     string // human-readable, secret-scrubbed (§10)
}

type State uint8 // Fresh, NeedsSetup, Fault, NoData; Loading is report-level only

type Report struct {
    Providers  []ProviderReport
    CapturedAt time.Time
    Loading    bool
}
```

Severity (derived, thresholds from §8 settings): `depleted ≥ 99 > critical ≥ 95 >
warn ≥ 85 > normal`. Headline selection (ai-usagebar `blockingMetric`): an exhausted
(`≥ 100`) window with the **latest** `ResetsAt` among exhausted ones beats everything;
else highest-percent window; else first. Countdown format: `"Nd Nh"` ≥ 24 h, else
`"Nh Nm"`, else `"Nm"`, past-reset ⇒ `"now"` — and a past `ResetsAt` with fresh data
renders "Waiting for fresh data" keeping the last percents, never 0% (codex-usage).

## 4. Fetch loop, caching, staleness

- **Serial queue, one fetch in flight** across all collectors (local plugin: parallel
  fan-out drops providers silently; same hazard with hammering endpoints).
- **Cadence:** `settings.refresh_interval` ticker (default 300 s). The Claude 180 s
  floor is **scheduler-level**: each collector carries a next-due time, so a
  floor-blocked round is never scheduled at all — it cannot error, and therefore cannot
  churn `retainLastGood`/Stale on a fast setting (audit finding 3). A `report.json`
  cache with `captured_at` younger than 150 s satisfies restarts/shell-reloads without
  a request (DankClaudeUsage guard).
- **Warm start:** on service start, load `$XDG_CACHE_HOME/sysc-shell/plugins/aiusage/report.json`
  (atomic tmp+rename, 0600, normalized reports only — **never token material**); older
  than 2× interval ⇒ flagged Stale, still drawn.
- **History (record-only):** after each completed round, append one JSON line per
  tracked window carrying a meaningful percent — `{"ts":…,"provider":…,"window":…,"pct":…}`
  — to `$XDG_CACHE_HOME/sysc-shell/plugins/aiusage/history.jsonl` (0600). Skip rows
  with `HasPercent == false` or `WindowMinutes == 0` (AIOC's `pct > 0` rule, adapted
  per provider-window rather than per provider). Lazy trim only when the file exceeds
  2× the 2000-line cap; the detail pane's history card reads the last 30 headline
  percents of the selected provider (oldest first) for its `graph` sparkline.
- **Backoff:** consecutive failures per collector double its wait (`interval · 2^n`,
  cap 15 min), cleared on success. Scheduled rounds skip backing-off collectors;
  **manual refresh (panel button) and settings-change refresh override backoff**
  (local plugin: "refusing a user who explicitly asked is how a plugin earns a
  reputation for being stuck").
- **retainLastGood:** fault ⇒ previous `ProviderReport` carried forward with
  `Stale: true` — only if the retained snapshot is itself error-free and non-empty
  (service.luau guards this; "last good" has to mean it). NeedsSetup ⇒ **no**
  retention ("the instruction is the whole point of the card").
- **Wake-from-suspend:** a 60 s clock records tick gaps; gap > 2× tick ⇒ immediate
  refresh (codex-usage). The same clock re-renders countdowns while views are live;
  the data source is never woken for time.
- **Staleness display:** `UpdatedAt` age > 2× interval ⇒ stale glyph + "Stale · Nm"
  (AIOC's 2× rule).

## 5. Bar view

Per widget instance (settings scoped `instance`): `vendor` select (`auto` + the six
ids, default `auto`), `visualization` select (`radial` default · `meter` · `none`),
`show_value` bool (true), `show_glyph` bool (true), `glyph_position` select
(`before` default · `after`), `extras` select (`none` default · `countdown` · `pace` ·
`both`), `show_name` bool (false). Mirrors the sysmon/ai-usagebar vocabulary (D6 of the
local plugin's settings design).

Tree (minor-4 host, full parity): `row[button(activate — ID/Name/Role/Events, Icon =
ai-usage glyph, childless, Tooltip = "provider · window NN% · resets in Xh Ym" — the
AIOC pill tooltip) | gauge 22×22 (Value = headline fraction, ValueText = "NN",
Absent = no live read — reserves its box and paints nothing, the dimmed-ring state
codex-usage dims to 0.35)] + text percent (Tabular) + optional countdown (Tabular,
subtle) + optional pace ("↑3"/"↓3", subtle)`; beneath the row, when window bounds are
known, a `progress` strip (Height 3) showing window-elapsed — ai-usagebar's
quota-over-elapsed pairing, with the radial carrying quota. The button is what makes
the pill open the panel — both house plugins do exactly this (timer's bar button →
`panel.open` on activate; audit B2: the host owns no pill click). Meter fallback
(pre-minor-3 host): the gauge becomes `progress (Value, Height 5)`, `absent` renders
as `text "--"`. `auto` ranks providers by severity → percent → lexical id and takes
the first; a pinned vendor with no live read leaves the capsule **empty rather than
holding a number nothing is refreshing** (ai-usagebar).

Invariants: a state transition never adds/removes nodes — recolor in place
(`tone`: normal → `accent` at warn → `error` at critical/depleted; node-count-stability
test ported from ai-usagebar's `bar_test`). Every interactive node carries
ID + Name + Role + a non-empty legal `Events` list (audit I4).

Pace = `UsedPercent − elapsedPercent`, where `elapsedPercent = (now − (ResetsAt −
WindowMinutes)) / WindowMinutes`, clamped to [0, 100] with pace suppressed outside the
band (clock skew or a past reset would render nonsense like `↑47` — audit finding 9);
rendered only when `WindowMinutes > 0` and `ResetsAt` known.

## 6. Panel view (master/detail, 750×430)

**List pane** (~290 px, `column` of rows; buttons are childless, so rows follow the
world-clock pattern — audit B1): each row is a `row` container keyed `provider-<id>`
(`Key`, `fill: card`, `shape: card`) holding [monogram disc — a `column` with
`shape: circle`, `fill: accent`, one centered letter of the provider id (provider
identity without 6 new font glyphs; catalogue glyphs can replace it later) |
childless `button` carrying the provider name (activate → select, Tooltip =
"NN% · resets in Xh Ym") | `column` of second-line text (plan / "Needs setup" /
"Stale · Nm" / "No data yet") | pin-end `column`: headline percent (fixed `Width`
per digit-count, `Tabular`) over a `progress` meter (Height 5), max 2 windows].
**Structural severity** (ai-usagebar `severityOf`): the percent text escalates
`size` caption → body → title with `bold`, and its meter `Height` 5 → 7, as severity
climbs. Rows are separated by a `separator` node (the rhythm line ai-usagebar draws).
Selected row's `fill` steps up `card` → `chip` (the selection tint; the wire has no
alpha borders — AIOC's `primary/0.55` outline collapses to the stronger fill).
Sorted severity → percent; NeedsSetup rows **kept** (their instruction is the point);
NoData rows kept as "No data yet" (same philosophy); disabled/untracked providers
absent. Selecting a row is an `activate` event on its name button → plugin sets
`selected` state → detail pane re-renders (ai-usagebar's key'd rows + selection).

**Detail pane** (`column`): provider header (monogram disc, name, plan chip); freshness
row ("Updated 4m ago · 12:40", subtle; stale variant in error tone); **hero gauge**
(64×64, headline window: Value, ValueText "NN%", Absent when no live read — dim state;
`progress` + display-size text fallback on pre-minor-3 hosts) beside the headline
window's card; exhausted-quota notice when any window ≥ 100 ("Quota exhausted ·
renews Sun 4:23 PM", error tone); **history card** (`fill: card`): a `graph`
(Height 40, `values` = last 30 recorded headline percents of this provider, oldest
first — exactly what D10's history file records) with a trend caption "↑ 4pts this
week / ↓ / flat" (last-two-snapshot delta, AIOC's rule) — the sparkline AIOC renders
and codex-usage charts, fed by data we already keep; then one **window card** per
window (`fill: card`, `shape: card`):

```
row: Label (bold) ......................... percent "82%" (Tabular, tone ramp)
progress (Height 5, quota)
progress (Height 3, elapsed — only when window bounds known)
row: "resets in 3h 12m" (Tabular) · "Sat 14:20" absolute (subtle)
row: "60% of window elapsed · ↑3 pace" (subtle, caption) — when derivable
text: raw DisplayValue / provider detail (subtle)
```

State rows: NeedsSetup card quotes every path tried ("No Claude token — sign in with
`claude` / set the key in Settings (looked in ~/.claude/.credentials.json)") —
onboarding-in-the-empty-state (OpenTrackerBar); Fault = error row + **per-provider
Retry button** (activate → force-fetch that collector only); down provider = **record
card** (last numbers as dated text, not gauges — "a bar reads as a live reading, and
nothing is reading", ai-usagebar); Credits row when non-nil; footer (subtle caption):
"Quota windows · last local snapshot per provider · not billing figures" (codex-usage's
honesty footer). Refresh button in the list header becomes the busy state while a round
runs (ai-usagebar) — implemented as a `disabled` + label swap (minor 2 field; legal per
audit I5).

## 7. Alerts

- Levels: `warn` ≥ `warn_threshold` (85), `critical` ≥ `critical_threshold` (95),
  `depleted` ≥ 99 (fixed constant). Invalid `critical ≤ warn` nudged to
  `min(100, warn+5)` rather than refused (local plugin; cap kept — audit finding 4).
- Fire per `(provider, window, level)` when percent crosses up; **never** on
  Fault/NeedsSetup/NoData or Stale data ("an unreachable provider is not a full one").
- **Hysteresis:** the level's record clears only below `threshold − 5`; boundary
  hovering never re-fires.
- **Dedupe + persistence:** ledger in plugin `state` under key `alerts` —
  `{"provider:window:level": resetsAtUnix | "unknown"}`. A level fires iff no record
  for that key matches the current window; matching compares `resetsAt` only when
  **both** stored and current are known (non-zero). An `unknown` record matches any
  unknown-reset window and is replaced the moment a real `resetsAt` appears —
  `0 == 0` would pin a never-re-arming alert, and unknown→known must fire once
  (audit finding 2). Records prune when superseded or 60 days silent (AIOC's
  migration lesson: keys embed the window identity, not jittery fetch timestamps).
  Persisting across restarts closes the local plugin's known-open bug.
- Delivery: `notify` — Summary `"Claude · Session 92%"`, Body `"Above critical
  threshold · resets in 2h 14m"`; urgency `normal` for warn, `critical` for
  critical/depleted. The wire has no replace-by-id, so escalation is a second
  notification, one per level per window (documented divergence from AIOC's in-place
  update). In-panel: the exhausted/critical window card carries its tone; the list row
  sorts it up.
- Settings gate: `alerts_enabled` (default true) hides the threshold fields
  (`visible_when`) — "two number fields that do nothing are worse than no fields".

## 8. Settings (plugin scope)

| key | type | default | notes |
|---|---|---|---|
| `track_claude` / `track_codex` / `track_commandcode` | bool | true | proven paths |
| `track_ollama` / `track_minimax` / `track_opencode_go` / `track_synthetic` | bool | false | opt-in; toggled on deliberately |
| `ollama_api_key` / `minimax_api_key` / `opencode_go_api_key` / `synthetic_api_key` | string | "" | `visible_when` follows each provider's track toggle; read into memory only (§10) |
| `refresh_interval` | int | 300 | min 60, max 3600 (seconds); the Claude 180 s floor is scheduler-level within (§4) |
| `alerts_enabled` | bool | true | |
| `warn_threshold` | int | 85 | min 50 max 99, `visible_when {"key":"alerts_enabled","equals":true}` |
| `critical_threshold` | int | 95 | min 51 max 99 — 100 would collide with the fixed depleted ≥99 (audit I2); `visible_when {"key":"alerts_enabled","equals":true}` |

Key resolution order per provider: pasted setting → env var (`OLLAMA_API_KEY`,
`MINIMAX_API_KEY`, `OPENCODE_GO_API_KEY`, `SYNTHETIC_API_KEY`,
`COMMAND_CODE_API_KEY`) → well-known file (`~/.minimax/api_key`, codexbar/OpenCode
auth borrows where they exist). OpenCode Go reads only the `opencode` API-key
entry; it does not refresh or write OpenCode credentials. A pasted key wins
(local plugin). Unlike the Luau original there is **no managed key file**: the
host owns settings persistence; clearing a field deletes it with no plugin-side residue.

## 9. sysc-shell side tasks

- **S1 — icon catalogue:** add `ai-usage` glyph (brain/sparkle form factor, matching
  the stroke weight of `ghost`/`schedule`) via `internal/render/icons/build.py`;
  `IconNames()` grows accordingly. `[a-z0-9-]` identifier, no underscores.
- **S2 — wire minor 4, the visual-parity surface.** Each addition maps onto a
  renderer the shell already ships (`ui.Node.Tooltip` tree.go:264, `KindGraph` +
  `Values` tree.go:155 + `paintGraph` paint.go:777, `KindSeparator`, `ui.Shape`
  tree.go:376, `ui.Node.Absent`):
  - `tooltip` string field, legal on every node (≤ 256 bytes), → `ui.Node.Tooltip`.
    Hover affordance the references all have (AIOC pill tooltip; ai-usagebar row
    tooltips); the shell already paints tooltips for its own features.
  - `graph` node kind with `values` array (2–64 entries, finite, each 0–1 normalized,
    oldest first) and optional `absent`; non-interactive, `height` honoured, legal in
    bar and panel views → `ui.KindGraph`. Powers the history sparkline.
  - `separator` node kind, no fields, container-legal, panel-only → `ui.KindSeparator`.
  - `shape` field on containers and buttons: `circle | stadium | small | medium |
    large | card | panel` → `ui.Shape` (monogram discs, pill chips, cards with the
    shell's own corner language).
  - `absent` bool, gauge-only (and graph) → `ui.Node.Absent` ("reserves its space,
    paints nothing" — the dimmed-ring state). Replaces the builder dash-swap for
    minor-4 hosts.
  - Validation: unknown values diagnosable (`unknown shape %q`), `values` length and
    finiteness bounds, `absent` rejected on other kinds, `separator` takes no
    children; ceilings unchanged.
  - Host advertises `Supported: [{1,3}, {1,4}]` (supervisor.go:201 — the grant is the
    intersection, so old plugins keep working); Convert gains the `graph`/`separator`
    cases and copies the new fields. No new rendering anywhere.

Two small CI gaps in `~/sysc-plugins` close alongside the plugin (audit M4/M5):
`tools/validate-manifests` learns the concrete `visible_when` shape
(`{"key", "equals"}` with the key required to be a declared setting) and runs its
settings checks over `widgets[].settings` too.

## 10. Privacy, honesty, security

- Tokens/keys are sent **only** to their provider's own API host (allowlist per
  collector); never logged, never written to cache/history files. A scrub pass (port
  of ai-usagebar's, reduced to the shapes that can appear: `sk-*`, bearer values,
  long base64 runs) runs over **every provider-derived string entering a `Report`** —
  `Err`, `DisplayValue`, `ResetDescription`, `Plan`, `Account` — not just error text
  (audit finding 1: the cache invariant must not rest on providers never echoing
  secrets into display fields).
- Cache + history files 0600, under `$XDG_CACHE_HOME/sysc-shell/plugins/aiusage/`.
- Pasted API keys live in host-managed plugin settings (the shell's config file,
  written atomically 0600 — verified `internal/config/write.go`): acceptable, with a
  README caveat that they share a file with all shell config (backup/screen-share
  exposure) and render as plain strings in the settings UI.
- Codex collector reads session JSONL only — never `~/.codex/auth.json`; the README
  states it, codex-usage-style ("does not read authentication files, refresh tokens,
  contact a server, or upload session content").
- Panel footer + README state the semantics: percentages are quota windows as recorded
  by each source (server-computed for Claude/CommandCode/Ollama/MiniMax/Synthetic;
  last local snapshot for Codex); nothing here is a billing figure.

## 11. Testing

- **Fixtures with provenance** per provider (real captures, comment-header naming the
  request; synthetic captured in P0); error payloads pinned verbatim — the local repo's
  war story: an earlier test put the CLI error on stderr "so it passed while the code
  it was guarding could not fire".
- `httptest.Server` per HTTP collector (base_resp-on-200, 401-never-from-cache,
  expired-token ordering, limits[]-vs-flat Claude shapes, fraction→percent Ollama);
  no network in tests, `CLAUDE_USAGE_MOCK`-style seams replaced by injecting the
  HTTP client + home dir (`t.Setenv`, temp `$CODEX_HOME`).
- Table tests: window label math, countdown formats, pace/elapsed derivation incl.
  clamping, backoff progression (skip vs manual override), alert dedupe + hysteresis +
  rollover + the unknown-reset sentinel (fake clock), retainLastGood guards,
  codex tail-scan (truncated line, 140 KB padding, newest-by-event-timestamp,
  premium skip).
- View tests: `v1.Validate` on every report state for **all three builder variants**
  (minor-4 full tree, minor-3, meter fallback — `Validate` is minor-blind, so the
  builder triple is the axis; audit M3); node-count stability across Fresh→Fault;
  minor-4 field validation (values bounds/finiteness, shape enum, tooltip cap,
  absent placement); panel tree budgets (≤ 1024 nodes, depth ≤ 16); scrub pass over
  hostile strings.
- `go run ./tools/validate-manifests` (with the §9 validator extensions); manifest
  schema check runs over `widgets[].settings` too.
- `make build test vet validate` green; race suite.
- Deploy + live verify per house style: install, screenshot bar chip (gauge), panel
  master/detail (fresh, stale, setup), alert firing; user judgement on parity.

## 12. Risks

- **Synthetic payload unknown** — mitigated by the P0 capture task and the
  informational-card fallback; the seam guarantees a surprise degrades to "note card",
  never a wrong number.
- **Claude endpoint is undocumented and 429-throttled** — scheduler-level 180 s floor +
  150 s cross-instance guard + backoff; a schema change surfaces as Fault-with-stale,
  not a blank bar.
- **Codex snapshot drift** (field renames upstream) — byte pre-filter + strict field
  validation ⇒ drift yields NoData with a stated reason, never a stale percent.
- **Deploy ordering** — S1+S2 must reach the user's shell before the plugin declares
  minor 4; until then an unknown icon name fails host conversion, so the plugin
  probes once (first rejected snapshot) and sticks to the `speed` glyph fallback for
  the process lifetime, and the builder serves the minor-3 tree whenever
  `Supported` lacks `{1,4}`. Out-of-order deploys degrade visibly and meter, never
  blank.
