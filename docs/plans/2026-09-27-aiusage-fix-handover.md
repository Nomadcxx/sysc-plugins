# AI Usage plugin — fix handover (2026-09-27)

Handover for the next agent. A prior session diagnosed the reported defects and
applied fixes to the working tree, but **nothing is verified end-to-end, nothing
is deployed, and temporary probe artifacts are still present**. This document is
the authoritative state; read it fully before touching anything.

## 1. Mission (user's original report, verbatim intent)

The AI Usage panel only partially works on the user's laptop:

1. Only `claude`, `codex`, `commandcode` load; `codex` shows **"stale"**, `claude` shows **"error"**.
2. No provider icons — user wants brand logos (Anthropic mark for claude, OpenAI for codex, etc.) for all common providers.
3. Usage bars on the right are **clipped / exceed the panel width**.
4. The **blur effect** is not visible on the panel.

Requested steps: (1) fix issues, (2) audit, (3) hallmark audit, (4) fix gaps from both audits, (5) deploy and test, verify working.

## 2. Repos and environment

- Plugin repo (workspace): `/home/nomadx/sysc-plugins`, module `github.com/Nomadcxx/sysc-plugins`, go 1.26.4.
- Shell repo: `/home/nomadx/sysc-shell` (deployed binary `~/.local/bin/sysc-shell`, service `systemctl --user restart sysc-shell.service`).
- Installed plugin dir `~/.config/sysc-shell/plugins/aiusage` is a **symlink** to `plugins/aiusage` in this repo, so source edits are live but `bin/` must be rebuilt (`make build && make install`).
- Live config `~/.config/sysc-shell/config.json` is a WIP schema — **do not "fix"/normalize it**. `theme = {preset: standard, panel-opacity: 65}`; no aiusage plugin settings saved (defaults apply).
- **Other agents edit this repo concurrently.** `git status` before every commit; stage only your own files; never `git stash`/`checkout`/`reset --hard`.
- Commit hook `~/.git-hooks/commit-msg` rejects messages containing (case-insensitive): claude, anthropic, chatgpt, openai, copilot, cursor, cody, tabnine, codex, gemini, bard, gpt-N, llm, ai assistant, bot, agent, co-authored-by, generated with/by. Substrings count ("both" contains "bot", "hallmark" contains "llm"). Rephrase commit messages.

## 3. Working-tree state (what exists right now)

### 3a. Fixes applied by the prior session (uncommitted, unverified)

| File | Change |
|---|---|
| `plugins/aiusage/oauth.go` | Parser fix: `limits[]` entries may carry `percent` (not just `utilization`); when the limits array yields no windows, fall through to the flat `five_hour`/`seven_day` pair instead of returning "not a quota body". |
| `plugins/aiusage/oauth_test.go` | `TestOAuthPercentLimitsShape` (live shape → 2 windows, 100/78) and `TestOAuthLimitsWithoutReadingsFallsBackToFlatPair` (→ 40/9). Both pass. |
| `plugins/aiusage/window.go` | `ProviderReport` gained `Snapshot bool` (reading that only moves when the tool runs; staleness judged against its window). |
| `plugins/aiusage/codex.go` | `Fetch` sets `Snapshot: true`. |
| `plugins/aiusage/view.go` | `staleFor`: snapshot providers are stale only when `Headline(windows).ResetsAt` is non-zero and passed, else `now-UpdatedAt > 24h`; non-snapshot unchanged (2×Refresh). |
| `plugins/aiusage/loop.go` | `loadCache` no longer blanket-stales everything at `CapturedAt+2*Refresh`; per-provider `staleFor` instead. |
| `plugins/aiusage/view_test.go` | `TestSnapshotStalenessFollowsTheWindowNotTheCadence`. Passes. |
| `plugins/aiusage/assets/logos/*.png` | 8 brand marks: claude, codex, copilot, minimax, ollama, opencode-go, commandcode, synthetic (64×64, commandcode/synthetic 48×48; light fills for dark theme). |
| `plugins/aiusage/view.go` | `providerLogos` (`sync.OnceValue(scanProviderLogos)`, scans `<exe>/../assets/logos`), `providerIcon(id)` → `KindImage` 26px when a logo exists, else old `monogram(id)`. Both call sites (provider row, detail header) use it. |
| `plugins/aiusage/view.go` | Truncation fixes: provider name button 112→120; status lane 64→56; Peak button 132→136; Export CSV button Width 88; fault-branch error text now its own full-width line; panel title / fleet Avg / window label wrapped in sized columns (Width 96 Padding 6 / Width 82 Padding 5 / Width 64) because **a text node in a row ignores its Width** (host measures `len(s)*8`). |

### 3b. Temporary probe artifacts — MUST be removed before commit

- `plugins/aiusage/zz_probe_test.go` — offline render probe (`TestZZProbeRenderViews`, `TestZZProbeMeasure`).
- `/home/nomadx/sysc-shell/plugin/lint/probe.go` — temporary exported `RenderPNG`/`DumpBounds`/`MeasureText`/`fillProbeImages` in the shell repo.
- `go.mod` line 14: `replace github.com/Nomadcxx/sysc-shell => /home/nomadx/sysc-shell` (remove with `go mod edit -dropreplace github.com/Nomadcxx/sysc-shell`; **leave the version bump on line 6 alone — it predates this work**).
- `tmp_probe/` at repo root (older probe, delete).

### 3c. Not mine — do not touch

`cmd/sysc-plugin-mini-docker/*`, `plugins/mini-docker/*`, `plugins/calendar/view.go`, `plugins/cat/view.go`, `tests/integration/plugin_mini_docker_gate_test.go`, `docs/plans/2026-09-27-bar-pill-fill-handover.md` are other agents' concurrent work.

## 4. Root causes (confirmed, with evidence)

1. **Claude "error"** — live `GET https://api.anthropic.com/api/oauth/usage` (token from `~/.claude/.credentials.json`, valid) returns HTTP 200 with `limits:[{kind:"session",percent:100},{kind:"weekly_all",percent:78}]` **plus** flat `five_hour.utilization`/`seven_day.utilization`. The parser only read `limits[].utilization`; non-empty limits yielding zero windows returned `false` → "usage response was not a quota body". Fixed.
2. **Codex "stale"** — `staleFor` used `now-UpdatedAt > 2*cfg.Refresh` (10 min default) but codex reads a local session JSONL that only moves when codex runs. Fixed via `Snapshot`.
3. **No icons** — old `monogram(id)` was a letter disc; in the 64-tall detail header the converter stretched it to a 26×64 empty pill. Fixed with shipped PNGs.
4. **Clipped bars** — the **deployed binary is stale**: running plugin process started 00:31, source fixes landed 01:28. Offline render of current source at 750×430 shows meters fit (list pane 290, detail pane 412). Deploy will confirm.
5. **Blur** — shell-side, not plugin-side: for `include_settings:true` panels the host wraps the plugin root in `monitorCard` (KindCapsule, `FillContainerHigh`, opaque) with settings below, so blur only shows in the padding ring. `internal/shell/panelhost.go` sets `BlurRegion` when `cfg.Theme.BlurBehind` (true for standard preset). **Needs a decision** (see §6 step 2).

## 5. Verification commands

From `~/sysc-plugins`:

```sh
GOMAXPROCS=4 go test -count=1 -p 2 ./...
GOMAXPROCS=4 go vet -p 2 ./...
GOMAXPROCS=4 go test -race -count=1 ./plugins/aiusage
```

Repo-wide manifest scan fails on the unchanged Wallpaper Depth manifest's unsupported `wallpaper` capability — known, ignore.

Offline render probe (while it still exists): from `~/sysc-plugins`:

```sh
GOMAXPROCS=4 go test -count=1 -run "TestZZProbeRenderViews|TestZZProbeMeasure" ./plugins/aiusage/
```

Renders `/tmp/panel_{claude,codex,copilot}.png` (750×430) and `/tmp/bar_render.png` (240×32). Inspect with Gemini (`/home/nomadx/opencode-cursor/tmp/gemini.sh`, model `gemini-3.5-flash-lite` works; 3.7/3.8 often 503).

## 6. Order of action

### Step 1 — Verify the last unverified edit
The column-wrapper truncation fix (panel title / fleet Avg / window label) was applied but its test run failed on a wrong cwd. Re-run the render probe from `~/sysc-plugins` (command above), inspect the PNGs, and iterate on `view.go` if anything still truncates. Known real-font widths (host measures `len*8` but paints Inter): "AI Usage" title 17 bold = 74px; "Command Code" body 15 = 115px; "Command Code" headline 21 = 164px; "Export CSV" 15 = 82px; "Session" bold 15 = 58px; "Weekly" bold 15 = 55px; "Avg 70%" bold 15 = 66px; "Peak Copilot 100%" 15 = 134px; error text caption 12 = 378px.

### Step 2 — Blur decision (needs user input)
Options: (a) accept host card design (blur visible only in the ring); (b) shell-side change so the plugin panel content area is translucent (e.g. `monitorCard` fill → `FillContainer`/transparent when the panel is a plugin panel); (c) plugin-side: drop `include_settings` and render settings itself (large change, not recommended). Ask the user before touching `~/sysc-shell`.

### Step 3 — Remove probe artifacts
```sh
cd ~/sysc-plugins
rm plugins/aiusage/zz_probe_test.go
rm -rf tmp_probe
go mod edit -dropreplace github.com/Nomadcxx/sysc-shell
rm /home/nomadx/sysc-shell/plugin/lint/probe.go
```
Then `go mod tidy` only if the build complains; do not otherwise touch go.mod/go.sum (other agents' version bump lives there).

### Step 4 — Full verification
Run the three commands in §5. All must pass.

### Step 5 — Audit + hallmark audit
- Audit: re-read `plugins/aiusage/*.go` for correctness gaps (error paths, secret scrubbing, cache/history handling, settings lifecycle).
- Hallmark audit: load the `hallmark` skill and audit the panel/bar UI against it. Fix gaps found by both.

### Step 6 — Deploy and verify live
**Announce the restart before doing it** (the bar flashes ~2s; never loop restarts).

```sh
cd ~/sysc-plugins && make build && make install
cd ~/sysc-shell && go build -o ~/.local/bin/sysc-shell ./cmd/sysc-shell
systemctl --user restart sysc-shell.service
systemctl --user is-active sysc-shell.service
ps -eo args | grep 'bin/sysc-plugin-aiusage$'
journalctl --user -u sysc-shell.service --since '-1min' | grep -i rejected   # must be empty
```

Live checks: claude shows windows (not error), codex shows Ready (not stale), brand logos visible in panel rows + detail header, no clipped meters, blur appearance per Step 2 decision.

### Step 7 — Commit (only if the user asks)
Stage only `plugins/aiusage/*` + `docs/plans/2026-09-27-aiusage-fix-handover.md`. Beware the commit hook word list (§2).

## 7. Open questions for the user

1. Blur: accept host card design, or change the shell so plugin panels are translucent?
2. Defaults: copilot and minimax both have working credentials on this machine but are opt-in (`track_* = false`). Should they default to tracked? (User's "only 3 providers load" observation.)
3. Should the panel width/height (750×430) change, or is the current size right once clipping is fixed?

## 8. Reference facts

- Panel manifest: `{"id":"panel","width":750,"height":430,"placement":"attached","include_settings":true}`.
- Host layout rules: panel root must be a column; **Height includes padding** (content = Height − 2×Padding); text in a row is measured `len(s)*8, 16` regardless of role; only a text that is a direct child of a column gets the column's content width; buttons render ~26 tall; `animate` needs `key` (minor ≥6); `absent` legal on gauge/graph/progress (minor ≥7); `stroke`/`stroke_fill` on containers/buttons (minor ≥5); `KindImage` is panel-only, needs absolute path + `ImageSize`.
- Provider readiness on this machine: claude (OAuth token valid), codex (local session), commandcode (alpha billing endpoint), copilot (gh token), minimax (API key) all live; ollama/synthetic need keys; opencode-go needs a subscription (403).
- Live probe results (2026-09-27 ~02:27): claude fault (parser bug, now fixed), codex fresh (Session 9%, Weekly 78%), commandcode fresh ($0/$14 session, $34.99/$35 weekly), copilot fresh (Free plan), minimax fresh, ollama/synthetic needs-setup, opencode-go no-data.
