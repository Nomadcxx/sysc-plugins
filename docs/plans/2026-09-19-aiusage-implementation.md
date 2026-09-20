# AI Usage (aiusage) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `org.sysc.aiusage` — six in-process collectors, a resilient fetch loop, persisted threshold alerts, and a bar/panel pair rendered at wire minor 4 — per `docs/plans/2026-09-19-aiusage-design.md` (the spec; it travels with this plan and every task argues from it).

**Architecture:** Two repos. `~/sysc-shell` grows the icon catalogue (S1) and wire minor 4 (S2: `tooltip`, `graph`, `separator`, `shape`, `absent` — all mapped onto existing renderers). `~/sysc-plugins` adds `plugins/aiusage` — a pure model layer (P1), collectors behind a registry (P3–P6), the fetch loop with cache/history (P7), alerts (P8), view-tree builders (P9), and wiring (P10).

**Tech Stack:** Go 1.26, `plugin/v1` JSONL wire, `net/http` + `encoding/json` only, `httptest` for all network tests.

**Spec:** `docs/plans/2026-09-19-aiusage-design.md` (decisions D1–D12, §1–§12). Research background: `docs/plans/2026-09-19-aiusage-research.md`.

## Global Constraints

- **Commit messages must not contain** (case-insensitive; enforced by `~/.git-hooks/commit-msg`): `claude`, `anthropic`, `chatgpt`, `openai`, `copilot`, `cursor`, `codex`, `gemini`, `bard`, `gpt-`, `llm`, `ai assistant`, `bot`, `agent`, `co-authored-by`, `generated with/by`. Use neutral wording ("oauth usage collector", "session snapshot collector").
- **Repo order:** S1+S2 land in sysc-shell and are deployed to the user's shell before the plugin's first minor-4 run (design §12). sysc-shell commits run in `~/sysc-shell`, sysc-plugins commits in `~/sysc-plugins`.
- Panel 750×430 `attached`; capabilities `notifications, panels, settings, state`; `requires.commands: []`; protocol declared `{1,4}`; version `0.1.0`; id `org.sysc.aiusage`.
- Thresholds: warn 85 (settings, min 50 max 99), critical 95 (min 51 max 99), depleted ≥ 99 fixed; hysteresis clears at `threshold − 5`.
- Refresh 300 s default (60–3600); endpoint floor 180 s is **scheduler-level**; cross-instance cache guard 150 s; backoff cap 15 min; per-fetch timeout 20 s.
- Cache/history/report files 0600 under `$XDG_CACHE_HOME/sysc-shell/plugins/aiusage/`; keys only in memory + Authorization headers; every provider-derived string entering a `Report` passes the scrub pass (§10).
- Icons: `[a-z0-9-]` names only; `ai-usage` is the new glyph; `speed` is the runtime fallback.
- Every view tree passes `v1.Validate` in tests for all three host variants (minor 4 / minor 3 / meter).

---

### Task S1: `ai-usage` icon glyph (sysc-shell)

**Files:**
- Modify: `~/sysc-shell/internal/render/icons/build.py` (glyph name list)
- Modify: `~/sysc-shell/internal/render/iconfont.go` (rune slot + `iconNames` entry)
- Test: `~/sysc-shell/internal/render/iconfont_test.go`

**Interfaces:**
- Produces: `render.IconByName("ai-usage")` resolves; `render.IconNames()` contains `"ai-usage"`. Plugin tasks P9/P10 consume the name.

- [ ] **Step 1: Write the failing test**

Append to `iconfont_test.go`:

```go
func TestIconCatalogueHasAIUsage(t *testing.T) {
	if _, ok := IconByName("ai-usage"); !ok {
		t.Fatal("ai-usage missing from the catalogue")
	}
	found := false
	for _, n := range IconNames() {
		if n == "ai-usage" {
			found = true
		}
	}
	if !found {
		t.Fatal("IconNames() does not list ai-usage")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/render/ -run AIUsage`
Expected: FAIL — catalogue has no such name.

- [ ] **Step 3: Add the glyph**

Add `"ai-usage"` to the glyph list in `build.py` (same section as `schedule`/`ghost`), regenerate the font (`python3 internal/render/icons/build.py` — commit the regenerated asset), then in `iconfont.go` add a rune constant after the night-weather block and the map entry `"ai-usage": iconAIUsage` mirroring `"schedule"`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/render/`
Expected: PASS.

- [ ] **Step 5: Commit (in ~/sysc-shell)**

```bash
git add internal/render/
git commit -m "feat(render): ai-usage catalogue glyph"
```

---

### Task S2: wire minor 4 — parity surface (sysc-shell)

**Files:**
- Modify: `~/sysc-shell/plugin/v1/node.go` (kinds + fields + validation)
- Test: `~/sysc-shell/plugin/v1/node_test.go`
- Modify: `~/sysc-shell/internal/plugin/view.go` (Convert cases)
- Test: `~/sysc-shell/internal/plugin/view_test.go`
- Modify: `~/sysc-shell/internal/plugin/supervisor.go:201` (advertise minor 4)

**Interfaces:**
- Produces (wire): kinds `graph` (fields `values`, `absent`), `separator`; fields `tooltip` (any node), `shape` (containers+buttons), `absent` (`gauge`+`graph`). Host advertises `{1,3}` and `{1,4}`.

- [ ] **Step 1: Write the failing validation tests**

Append to `plugin/v1/node_test.go`:

```go
func TestValidateAcceptsMinorFour(t *testing.T) {
	t.Parallel()

	root := &Node{Kind: KindColumn, Children: []*Node{
		{Kind: KindText, Text: "history", Tooltip: "30 snapshots"},
		{Kind: KindGraph, Values: []float64{0.1, 0.4, 0.9}, Height: 40},
		{Kind: KindGauge, Value: 0.5, ValueText: "50", Absent: true},
		{Kind: KindSeparator},
		{Kind: KindRow, Shape: "card", Children: []*Node{
			{Kind: KindColumn, Shape: "circle", Fill: "accent", Children: []*Node{
				{Kind: KindText, Text: "C"},
			}},
		}},
	}}
	if err := Validate(root, ViewPanel); err != nil {
		t.Fatalf("minor-4 tree rejected: %v", err)
	}
}

func TestValidateRejectsBadMinorFour(t *testing.T) {
	t.Parallel()

	cases := []struct{ name string; node *Node }{
		{"unknown shape", &Node{Kind: KindRow, Shape: "neon"}},
		{"shape on text", &Node{Kind: KindText, Text: "x", Shape: "card"}},
		{"tooltip over cap", &Node{Kind: KindText, Text: "x", Tooltip: strings.Repeat("a", 257)}},
		{"graph one sample", &Node{Kind: KindGraph, Values: []float64{0.5}}},
		{"graph over cap", &Node{Kind: KindGraph, Values: make([]float64, 65)}},
		{"graph nan", &Node{Kind: KindGraph, Values: []float64{math.NaN()}}},
		{"graph out of range", &Node{Kind: KindGraph, Values: []float64{1.5}}},
		{"values on text", &Node{Kind: KindText, Text: "x", Values: []float64{0.5}}},
		{"absent on text", &Node{Kind: KindText, Text: "x", Absent: true}},
		{"separator with children", &Node{Kind: KindSeparator, Children: []*Node{{Kind: KindText, Text: "x"}}}},
	}
	for _, tc := range cases {
		if err := Validate(tc.node, ViewPanel); err == nil {
			t.Errorf("%s: Validate accepted", tc.name)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./plugin/v1/ -run MinorFour`
Expected: FAIL — `Tooltip/Shape/Values/Absent/KindGraph/KindSeparator` undefined.

- [ ] **Step 3: Implement kinds, fields, validation**

In `plugin/v1/node.go` — kinds (after `KindGauge`): `KindGraph NodeKind = "graph"`, `KindSeparator NodeKind = "separator"`. `graph` joins `interactive()`-adjacent classification as **non-interactive, non-container**; `separator` likewise. `allowedEvents`: neither emits events. `ViewKind` rules: `graph` legal in bar + panel; `separator` **panel-only**.

Fields on `Node` (after `Tabular`):

```go
	// Tooltip is bounded hover text owned by the node's feature.
	Tooltip string `json:"tooltip,omitempty"`
	// Shape names a corner treatment: circle, stadium, small, medium,
	// large, card, panel.
	Shape string `json:"shape,omitempty"`
	// Values are the graph's samples, oldest first, each normalized 0..1.
	Values []float64 `json:"values,omitempty"`
	// Absent reserves the node's box and paints nothing.
	Absent bool `json:"absent,omitempty"`
```

Validation (in the node walk): `shape` ∈ {circle, stadium, small, medium, large, card, panel} and only on `row/column/list/drop_zone/button`; `tooltip` ≤ 256 bytes on any node; `values` only on `graph`, 2–64 entries, each finite and within 0..1; `absent` only on `gauge`/`graph`; `separator` takes no children and is rejected in bar views (like `list`).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./plugin/v1/`
Expected: PASS (all existing tests too).

- [ ] **Step 5: Convert mapping**

In `internal/plugin/view.go` — add cases after `KindGauge`:

```go
	case v1.KindGraph:
		out.Kind = ui.KindGraph
		out.Values = n.Values
		out.Absent = n.Absent
	case v1.KindSeparator:
		out.Kind = ui.KindSeparator
```

In the shared copy block add `Tooltip: n.Tooltip, Absent: n.Absent`, and map `Shape` via

```go
var wireShapes = map[string]ui.Shape{
	"circle": ui.ShapeCircle, "stadium": ui.ShapeStadium,
	"small": ui.ShapeSmall, "medium": ui.ShapeMedium,
	"large": ui.ShapeLarge, "card": ui.ShapeCard, "panel": ui.ShapePanel,
}
```

unknown shape ⇒ `fmt.Errorf("plugin: %s: unknown shape %q", path, n.Shape)`.

Converter test (append to `internal/plugin/view_test.go`): a tree with `KindGraph{Values: …, Absent: true}`, `KindSeparator`, `Shape: "circle"` converts to `ui.KindGraph`/`ui.KindSeparator` with `Values`, `Absent`, `ui.ShapeCircle` intact; `"neon"` shape errors.

- [ ] **Step 6: Advertise minor 4**

`internal/plugin/supervisor.go:201` → `Supported: []v1.Version{{Major: 1, Minor: 3}, {Major: 1, Minor: 4}}` (the grant is the intersection with the plugin's declaration, so minor-3 plugins keep working).

- [ ] **Step 7: Run gates + commit (in ~/sysc-shell)**

Run: `go test -race ./plugin/v1/ ./internal/plugin/`
Expected: PASS.
Commit: `git commit -m "feat(plugin): minor-4 wire surface for parity views"`.

---

### Task P1: model layer (sysc-plugins)

**Files:**
- Create: `plugins/aiusage/window.go`, `plugins/aiusage/errors.go`, `plugins/aiusage/collector.go`
- Test: `plugins/aiusage/window_test.go`

**Interfaces:**
- Produces (all later tasks consume):

```go
type State uint8 // StateFresh, StateNeedsSetup, StateFault, StateNoData

type Window struct {
	Key, Label, ShortLabel                          string
	UsedPercent                                     float64
	HasPercent                                      bool
	WindowMinutes                                   int // 0 = unbounded/unknown
	ResetsAt                                        time.Time // zero = unknown
	ResetDescription, DisplayValue                  string
}

type ProviderReport struct {
	ID, Name, Plan, Account string
	Windows                 []Window
	Credits                 *float64
	State                   State
	Stale                   bool
	UpdatedAt               time.Time
	Err                     string
}

type Report struct {
	Providers  []ProviderReport
	CapturedAt time.Time
	Loading    bool
}

type ErrSetup struct{ Tried []string }                        // implements error
type Collector interface {
	ID() string
	Fetch(ctx context.Context) (ProviderReport, error)
}
type Env struct { // test seams
	Client *http.Client        // nil ⇒ default
	Home   string              // "" ⇒ os.UserHomeDir()
	Now    func() time.Time    // nil ⇒ time.Now
	Env    func(string) string // nil ⇒ os.Getenv
}
type Registry map[string]func(Env) Collector
func LabelForMinutes(m int) (label, short string)
func FormatCountdown(resets, now time.Time) string
func ElapsedPercent(w Window, now time.Time) (float64, bool)
func Pace(w Window, now time.Time) (int, bool)
func Severity(pct float64, warn, crit int) int // 0 normal … 3 depleted
func Headline(ws []Window) *Window
func Scrub(s string) string
```

- [ ] **Step 1: Write the failing tests** (`window_test.go`)

Table-test `LabelForMinutes` (300→`"Session"/"5h"`, 10080→`"Weekly"/"Wk"`, 43200→`"Monthly"/"Mo"`, 720→`"12 hour"/"12h"`); `FormatCountdown` with a fake `now` (25 h ⇒ `"1d 1h"`, 3 h 12 m ⇒ `"3h 12m"`, 8 m ⇒ `"8m"`, past ⇒ `"now"`); `ElapsedPercent` (mid-window ⇒ 50, before start clamps 0, past end clamps 100, `WindowMinutes == 0` ⇒ `ok == false`, zero `ResetsAt` ⇒ `ok == false`); `Pace` (usage 60/elapsed 40 ⇒ `+20`, suppressed when `ElapsedPercent` not ok); `Severity` (84→0, 85→1, 94→1, 95→2, 98→2, 99→3 with warn 85 crit 95); `Headline` (two exhausted windows ⇒ the one with the **later** reset wins; one exhausted + one 90% ⇒ exhausted wins; none ⇒ highest percent; all zero-percent ⇒ first); `Scrub` (`sk-abc123…` ⇒ `[redacted]`, `Bearer xyz…` redacted, a 60-char base64 run redacted, plain prose untouched).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./plugins/aiusage/`
Expected: FAIL — package empty.

- [ ] **Step 3: Implement**

`window.go`: the structs above; `LabelForMinutes` switch (300, 10080, 43200, else `fmt.Sprintf("%d hour", m/60)` / minutes) with short = hours/`"Wk"`/`"Mo"`; `FormatCountdown` (d/h/m ladder, `"now"` past); `ElapsedPercent` clamped [0,100], guards per test; `Pace = int(UsedPercent − elapsed)`; `Severity` per thresholds; `Headline` per design §3; `Scrub` — three regexps applied in order: `` `sk-[A-Za-z0-9_\-]{8,}` ``, `` `(?i)bearer\s+\S+` ``, `` `[A-Za-z0-9+/]{40,}={0,2}` `` → `[redacted]`.

`errors.go`: `func (e *ErrSetup) Error() string { return "setup required: " + strings.Join(e.Tried, "; ") }`.

`collector.go`: `Collector`, `Env` accessors (`httpClient`, `home`, `now`, `env`), `Registry` + `func (r Registry) Build(id string, env Env) (Collector, bool)`.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./plugins/aiusage/`
Expected: PASS.

- [ ] **Step 5: Commit**

`git commit -m "feat(aiusage): window model and collector seam"`.

---

### Task P3: session-file snapshot collector

**Files:**
- Create: `plugins/aiusage/codex.go`
- Test: `plugins/aiusage/codex_test.go`
- Fixture: `plugins/aiusage/testdata/codex-session.jsonl` (provenance comment first line: `// captured 2026-09-19 from ~/.codex/sessions/... token_count rows; hand-assembled from real rows`)

**Interfaces:**
- Consumes: P1 types. Produces: `func NewSnapshot(env Env) Collector` with `ID() == "codex"` (brand words are only filtered from commit messages — code, wire ids, and settings use the real provider ids per the design's vendor select; the *commit message* for this task stays neutral).

- [ ] **Step 1: Fixture + failing tests**

Fixture rows: one `turn_context` (`{"type":"turn_context","timestamp":…,"payload":{"model":"gpt-5.1"}}`), several `token_count` rows with `payload.rate_limits.{limit_id, primary{used_percent, window_minutes, resets_at}, secondary{…}, plan_type}` — include a `limit_id:"premium"` row (must skip), a truncated last line `{"rate_limits":` (must skip), and rows across two files (test writes a second file). Tests: newest-wins **by event timestamp** (older mtime file with newer event wins), last-match-per-file, `plan_type` → `Plan`, windows normalized (300 → primary, 10080 → secondary, `resets_at` epoch-s → `ResetsAt`), no compatible row anywhere ⇒ `StateNoData`, `$CODEX_HOME` override honored via `t.Setenv`.

- [ ] **Step 2: Run to verify failure** — `go test ./plugins/aiusage/ -run Snapshot` ⇒ FAIL.

- [ ] **Step 3: Implement** (`codex.go`)

- Root: `$CODEX_HOME` or `$HOME/.codex`, then `/sessions`; `filepath.WalkDir` for `*.jsonl`, sort by mtime desc.
- Per file: open, `Seek(0, io.SeekEnd)`, walk backwards in 64 KiB chunks building lines (keep a `pending` fragment for chunk boundaries), scan lines **backwards**, byte pre-filter `bytes.Contains(line, []byte(`"rate_limits"`))`, `json.Unmarshal` into `{Timestamp string; Payload struct{ Type string; RateLimits struct{ LimitID string; Primary, Secondary *rawWindow; PlanType string }}}`; first valid row per file wins; track global newest **by parsed event timestamp**; whole-file skip once `mtime < newestEventTS`.
- Normalize: `HasPercent` true, percent clamp [0,100], `LabelForMinutes`, `ResetsAt = time.Unix(resets_at, 0).UTC()`.
- Empty result ⇒ `ProviderReport{ID:"snapshot", Name:"Codex", State: StateNoData}`; unreadable home ⇒ same.

- [ ] **Step 4: Run to verify pass** — `go test -race ./plugins/aiusage/ -run Snapshot` ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): session-file snapshot collector"`.

---

### Task P4: oauth usage collector (the `claude` provider)

**Files:**
- Create: `plugins/aiusage/oauth.go`
- Test: `plugins/aiusage/oauth_test.go`
- Fixture: `plugins/aiusage/testdata/oauth-usage-limits.json` + `oauth-usage-flat.json` (provenance comments; shapes per research §6/DankClaudeUsage fixture and AIOC `limits[]`)

**Interfaces:**
- Produces: `func NewOAuthUsage(env Env) Collector`, `ID() == "claude"`.

- [ ] **Step 1: Fixtures + failing tests**

`oauth-usage-limits.json`: `{"limits":[{"kind":"session","utilization":49.7,"resets_at":"…Z","is_active":true},{"kind":"weekly_all","utilization":9.0,"resets_at":"…Z"},{"kind":"weekly_scoped","utilization":12.0,"resets_at":"…Z"}]}`. Flat fixture: `{"five_hour":{"utilization":40.0,"resets_at":"…"},"seven_day":{"utilization":9.0,"resets_at":"…"}}`. Tests via `httptest.Server`: session→primary (floor 49), weekly_all→secondary; flat fallback used when `limits` absent; error body (no quota block) ⇒ fault, **and** a 401 ⇒ fault with no cache fallback; token read from temp `$HOME/.claude/.credentials.json` `.claudeAiOauth.accessToken` with expired-second candidate ordered after a valid one (second fixture file); request assertions: `Authorization: Bearer …`, `anthropic-beta: oauth-2025-04-20`, `User-Agent: claude-code/` prefix; `expiresAt` epoch-ms ordering.

- [ ] **Step 2: Run to verify failure** ⇒ FAIL.

- [ ] **Step 3: Implement** (`oauth.go`)

Token discovery: read `$HOME/.claude/.credentials.json`; candidates `[]tokenCand{expiresAtMS int64, tok string}`; sort non-expired first (freshest first), expired after; try in order against `GET <host>/api/oauth/usage` with the three headers; response valid iff JSON object with `limits` **or** `five_hour`; first success wins. Parse: `limits[]` — `kind=="session"`→primary(300m), `"weekly_all"`→secondary(10080m), `"weekly_scoped"`→tertiary (active-first already); else flat `five_hour`/`seven_day` (`utilization` floored). 401/403 ⇒ `StateFault` with scrubbed message ("sign-in expired — run the sign-in flow"), never cached. `Client`/`Home`/`Now` from `Env`.

- [ ] **Step 4: Run to verify pass** ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): oauth usage collector"`.

---

### Task P5: direct-API collectors (commandcode, ollama, minimax)

**Files:**
- Create: `plugins/aiusage/direct.go` (three collectors, one file — they share the bearer-GET helper)
- Test: `plugins/aiusage/direct_test.go`

**Interfaces:**
- Produces: `func NewCommandCode(env Env) Collector` (`ID()=="commandcode"`), `func NewOllama(env Env) Collector` (`"ollama"`), `func NewMinimax(env Env) Collector` (`"minimax"`). Shared: `func bearerGet(ctx, client, url, key string, into any) error` (20 s ctx respected; 401/403 ⇒ `ErrSetup`-style typed fault).

- [ ] **Step 1: Failing tests (httptest + fixtures inline)**

- commandcode: key from `Env.Env("COMMAND_CODE_API_KEY")` → temp `$HOME/.commandcode/auth.json` `.apiKey`; response `{"windowLimits":{"fiveHour":{"used":42,"cap":100,"resetsAt":1758000000000},"weekly":{…}},"credits":{"monthlyCredits":12.5}}` ⇒ primary 42% (300 m, `DisplayValue "$42 / $100"`), weekly, Credits 12.5.
- ollama: `{"limits":{"session":{"usage":0.12},"weekly":{"usage":0.34}}}` ⇒ 12%/34%; session `WindowMinutes 0` (no countdown, history-skipped); weekly reset == next Monday 00:00 UTC from `Env.Now` (table: Wed ⇒ +5 d, Sat ⇒ +2 d, Sun ⇒ +1 d, Mon ⇒ +7 d); `{"me":{"plan":"pro","email":…}}` best-effort — plan set even when `/api/me` 500s.
- minimax: `{"base_resp":{"status_code":0},"model_remains":[{"model_name":"video",…},{"model_name":"general","current_interval_remaining_percent":61,"end_time":1758000000000}]}` ⇒ 39% used, ms→s, 300/10080 windows; `status_code != 0` ⇒ fault with `base_resp` message; missing "general" ⇒ first entry (ground-truth fallback).

- [ ] **Step 2: Run to verify failure** ⇒ FAIL.

- [ ] **Step 3: Implement** (`direct.go`) — one section per collector following the design §2 rows exactly (endpoints, key chains, quirk normalization); each collector: build `Windows` sorted primary→secondary, `State` Fresh on success / Fault with scrubbed message on failure.

- [ ] **Step 4: Run to verify pass** ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): direct-api collectors"`.

---

### Task P6: synthetic quotas collector (P0 capture first)

**Files:**
- Create: `plugins/aiusage/synthetic.go`
- Test: `plugins/aiusage/synthetic_test.go`
- Fixture: `plugins/aiusage/testdata/synthetic-quotas.json` — **captured live in Step 1**

**Interfaces:**
- Produces: `func NewSynthetic(env Env) Collector` (`ID()=="synthetic"`).

- [ ] **Step 1: Capture the real payload (no code)**

With the user's key: `curl -sS -H "Authorization: Bearer $SYNTHETIC_API_KEY" https://api.synthetic.new/quotas | tee testdata/synthetic-quotas.json` — confirm the URL/headers against `dev.synthetic.new/docs/synthetic/quotas` first; prepend a provenance comment (endpoint + date). If the payload carries no per-window percents, the fixture still lands and the collector renders informational.

- [ ] **Step 2: Failing tests** — parse the captured shape into `Window`s per its real fields; no-percent payload ⇒ one informational window (`HasPercent:false`, `DisplayValue` from payload); missing key ⇒ `ErrSetup` listing setting, env, key-file paths tried.

- [ ] **Step 3: Implement** (`synthetic.go`) — bearer key: setting → `Env.Env("SYNTHETIC_API_KEY")` → `$HOME/.synthetic/api_key`; GET; parse per fixture; **nothing parsed before the fixture exists**.

- [ ] **Step 4: Run to verify pass** ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): synthetic quotas collector"`.

---

### Task P7: fetch loop, cache, history

**Files:**
- Create: `plugins/aiusage/loop.go`, `plugins/aiusage/loop_test.go`

**Interfaces:**
- Consumes: P1–P6. Produces:

```go
type Loop struct{ /* collectors, cfg, env, paths, lastGood map[string]ProviderReport */ }
func NewLoop(reg Registry, cfg Config, env Env, cachePath, historyPath string) *Loop
func (l *Loop) WarmStart() Report                               // load report.json, flag Stale if > 2× interval
func (l *Loop) Round(ctx context.Context, force bool) Report    // one serial round; force overrides floor+backoff
func (l *Loop) Force(providerID string)                         // queue a per-provider forced fetch
func (l *Loop) Snapshot() Report
type Config struct {
	Track   map[string]bool
	Keys    map[string]string // provider → pasted key
	Refresh time.Duration
	Warn, Crit int
	HostMinor int // negotiated; views consume it
}
```

- [ ] **Step 1: Failing tests (fake clock via `Env.Now`)**

Serial order (collectors record call order; overlap impossible by construction); scheduler floor — refresh 60 s + a collector with `NextDue` 180 s ⇒ that collector is skipped without error and without `retainLastGood` churn; backoff — 3 consecutive faults ⇒ next due `interval·4`, cap 15 min, success resets, `force` overrides; `retainLastGood` — fault carries previous windows with `Stale:true` only when the previous report was error-free and non-empty, NeedsSetup never retains; wake — a `Round` after a > 120 s clock gap refetches all; history — a fresh round appends one line per tracked percent-window (`HasPercent && WindowMinutes > 0`), file 0600, trims at 2× 2000; cache — `report.json` atomic (tmp+rename), 0600, warm start restores and flags stale by age.

- [ ] **Step 2: Run to verify failure** ⇒ FAIL.

- [ ] **Step 3: Implement** (`loop.go`) — serial `for` over tracked collectors; per-collector `nextDue time.Time` (floor + backoff state machine, cap 15 min, cleared on success, bypassed when `force`); scrub every provider-derived string (`Scrub`) before the `Report` is published; atomic writes; history append per §4.

- [ ] **Step 4: Run to verify pass** ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): fetch loop with floors, backoff, cache, history"`.

---

### Task P8: alerts

**Files:**
- Create: `plugins/aiusage/alerts.go`, `plugins/aiusage/alerts_test.go`

**Interfaces:**
- Produces:

```go
type Ledger map[string]AlertRec          // key "provider:window:level"
type AlertRec struct{ ResetsAt int64 }   // unix s, or 0 = unknown sentinel
type Notice struct{ Summary, Body string; Critical bool }
func CheckAlerts(r Report, warn, crit int, led Ledger, now time.Time) []Notice
func Prune(led Ledger, now time.Time)    // 60-day silence
```

- [ ] **Step 1: Failing tests (fake clock)** — fires at 85/95/99 once per `(provider, window, level)`; no fire on Fault/NeedsSetup/NoData/Stale; hysteresis — record clears only below `threshold−5`, boundary hover 84↔86 never re-fires; rollover — new `ResetsAt` re-arms; **unknown sentinel** — `ResetsAt` zero stores 0, matches any unknown-reset window (no refire), and a known `ResetsAt` later fires once then stores it; urgency normal/critical mapping; prune after 60 days.

- [ ] **Step 2: Run to verify failure** ⇒ FAIL.

- [ ] **Step 3: Implement** (`alerts.go`) — per design §7 verbatim; invalid `crit ≤ warn` nudged `min(100, warn+5)` before evaluating (caller applies the same nudge from settings).

- [ ] **Step 4: Run to verify pass** ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): threshold alerts with persisted dedupe"`.

---

### Task P9: view builders

**Files:**
- Create: `plugins/aiusage/view.go`, `plugins/aiusage/view_test.go`

**Interfaces:**
- Consumes: P1, P7 history. Produces:

```go
func BarTree(r Report, inst Instance, hostMinor int, now time.Time) *v1.Node
func PanelTree(r Report, selected string, hist []float64, hostMinor int, now time.Time) *v1.Node
type Instance struct {
	Vendor, Visualization, GlyphPosition, Extras string
	ShowValue, ShowGlyph, ShowName               bool
}
```

- [ ] **Step 1: Failing tests** — per design §5/§6, asserted through `v1.Validate(root, ViewBar/ViewPanel)` plus structural probes, across **all three `hostMinor` values (4, 3, 2)** and states Fresh/NeedsSetup/Fault/NoData/empty:
  - bar: activate button present (opens panel — main.go wires `panel.open`); minor 4 ⇒ `gauge` with `ValueText` + `absent` when no read; minor 3 ⇒ gauge without absent; minor 2 ⇒ `progress` + `"--"` text; countdown/pace per `extras`; tone ramp (85→accent, 95→error).
  - panel: list rows = keyed `row` (`fill: card`, `shape: card`) with monogram `column` (`shape: circle`, `fill: accent`), childless name `button` (activate → select), second-line text, pin-end percent (`Tabular`, fixed `Width`) over `progress` (Height 5→7 by severity, size caption→title by severity); `separator` between rows (minor 4 only); selected row `fill: chip`; detail: hero 64×64 gauge, freshness row, exhausted notice ≥ 100, history card with `graph{values: hist}` + trend caption, window cards (quota H5 + elapsed H3 when bounds known, countdown + absolute reset, pace caption clamped), setup card quoting `Tried` paths, fault row + Retry button, record card for down providers, subtle footer; NeedsSetup/NoData rows kept.
  - stability: Fresh→Fault changes **no node counts** in the bar tree (recolor-only).
  - budgets: 6 providers ⇒ ≤ 1024 nodes, depth ≤ 16.

- [ ] **Step 2: Run to verify failure** ⇒ FAIL.

- [ ] **Step 3: Implement** (`view.go`) — pure functions over `Report`; helper `monogram(id string) *v1.Node`; severity escalations; every interactive node gets ID/Name/Role/Events; `tooltip` on pill button + name buttons (minor 4 gate).

- [ ] **Step 4: Run to verify pass** — `go test -race ./plugins/aiusage/ -run Tree` ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): bar and panel builders with negotiated variants"`.

---

### Task P10: wiring — main, manifest, Makefile, validator, README

**Files:**
- Create: `cmd/sysc-plugin-aiusage/main.go`, `plugins/aiusage/manifest.json`
- Modify: `Makefile` (PLUGINS += `sysc-plugin-aiusage:aiusage`), `README.md` (table row + attribution note), `tools/validate-manifests/main.go`
- Test: `tools/validate-manifests/main_test.go` (if present; else extend the structural run)

**Interfaces:**
- Consumes: everything above. Produces: the runnable plugin.

- [ ] **Step 1: manifest.json** — exactly the design §1 JSON: schema 1, `org.sysc.aiusage`, `0.1.0`, `{1,4}`, exec `bin/sysc-plugin-aiusage`, capabilities, `requires.commands: []`, service `aiusage`, widget `bar` with instance settings (`vendor` select auto+6, `visualization` radial|meter|none, `show_value`, `show_glyph`, `glyph_position` before|after, `extras` none|countdown|pace|both, `show_name`), panel 750×430 attached, plugin settings per §8 with `visible_when {"key":…,"equals":…}` objects.

- [ ] **Step 2: Validator extensions (failing test first)** — `visible_when` must be an object `{"key", "equals"}` whose `key` names a declared setting; run the full settings check over each `widgets[].settings` row. Run `go run ./tools/validate-manifests` before/after: before ⇒ our manifest's checks pass vacuously; after ⇒ a deliberately malformed fixture (`visible_when` on an unknown key) fails.

- [ ] **Step 3: main.go** — the reference-plugin idiom (`cmd/sysc-plugin-weather`): `Handshake`; scan `hello.Supported` for the highest minor ≤ 4 → `Config.HostMinor`; build `Env`; resolve settings → `Config`; `NewLoop`; goroutine `Recv`; `select` over ticker (interval), 60 s clock (wake detection: gap > 120 s ⇒ `Round(force=false)` + re-publish), `ViewOpen` (register bar/panel, publish), `InputEvent` (bar activate → `panel.open`; name-button `sel:<id>` → set selected + republish panel; `refresh` → `Round(force=true)`; `retry:<id>` → force that collector), `SettingsChanged` → rebuild Config + force round; on each completed `Round`: `Snapshot()` → publish (Patch with Snapshot fallback), `CheckAlerts` → `notify` + persist ledger via `state.set("alerts", …)`. Countdown re-render: the 60 s clock republishes when `extras != none`.

- [ ] **Step 4: Gates** — `gofmt -l .` empty; `go vet ./...`; `go run ./tools/validate-manifests`; `go test -race ./...` — all green. README table row + attribution sentence (patterns ported from the noctalia community plugin family and the owner's v5 plugin; licenses respected).

- [ ] **Step 5: Commit** — `git commit -m "feat(aiusage): plugin wiring, manifest, validator extensions"`.

---

### Task P11: deploy + live verify

- [ ] **Step 1:** sysc-shell: build + install to the running shell (restart per the repo's shell-env flow); sysc-plugins: `make build && make install`; confirm the plugin process starts, no `plugin start failed` lines, handshake granted minor 4.
- [ ] **Step 2:** Screenshots: bar chip (gauge + percent + tooltip hover), panel master/detail fresh, stale state (suspend/resume or old cache), NeedsSetup card (fresh keyring without a pasted key), alert notification at a forced 100% fixture. Pull PNGs, view, iterate on user feedback.
- [ ] **Step 3:** User judgement on parity vs ai-usagebar/AIOC references; fold feedback into follow-ups.

---

## Self-review notes

- Spec coverage: S1↔§9-S1; S2↔§9-S2; P1↔§2/§3; P3–P6↔§2 collectors table; P7↔§4; P8↔§7; P9↔§5/§6; P10↔§1/§8/§10; P11↔§11 live-verify. §10's scrub is in P1 (`Scrub`) + P7 (applied at publish); README privacy caveats in P10.
- Type consistency: `Env`, `Registry`, `Loop.Round/WarmStart/Force/Snapshot`, `CheckAlerts(Ledger)`, `BarTree/PanelTree(…, hostMinor int, …)` are the only cross-task signatures; P9's `hostMinor` comes from `Config.HostMinor` set in P10.
- Registry keys match the design's vendor ids (`claude`, `codex`, `commandcode`, `ollama`, `minimax`, `synthetic`) — the hook filters commit messages only, so code uses real ids while every commit message above is worded neutrally.
