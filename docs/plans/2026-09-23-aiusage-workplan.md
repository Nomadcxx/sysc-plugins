# AI Usage defect remediation workplan

**Date:** 2026-09-23
**Updated:** 2026-09-24
**Status:** implementation and deterministic validation complete. The staged panel has been rendered and visually reviewed; the setup guidance overflow found in that pass is fixed. Live-only checks without an active subscription or documented response semantics are explicitly skipped. A real compositor screenshot/scroll impression remains a manual pre-deploy check.
**Source audit:** [AI Usage defect ledger](2026-09-23-aiusage-defect-ledger.md)

## Working constraints

- Keep plugin-scope configuration separate from widget-instance preferences.
- Preserve existing untracked work and saved settings/cache. Live credentials are used only in redacted, allowlisted probes; never print keys, response bodies, account identifiers, or quota amounts.
- Keep live writes in a temporary cache/history directory. Use quota endpoints only, respect provider floors, and block Ollama's optional identity POST during diagnostics.
- Keep Copilot, Ollama, MiniMax, and Synthetic opt-in; do not change saved tracking defaults while testing.
- Keep OpenCode Go opt-in; resolve its API key from plugin settings, `OPENCODE_GO_API_KEY`, or OpenCode's `opencode` API-auth entry read-only.
- Missing subscriptions for Ollama Cloud or OpenCode Go do not block fixture-based work or testing other providers; do not invent live usage when credentials are absent.
- Hermes `.env` keys are loaded only by the temporary probe; the shipped plugin does not currently import that file.
- The settings-panel composition code belongs to `/home/nomadx/sysc-shell`, outside this workspace's write scope; its focused patch was approved and applied.
- API-key controls reuse the host's existing text-field renderer and config storage; values are masked by default with a keyboard-focusable Show/Hide control.
- Work tracking is in sysc-shell's Beads database; the staged visual review is recorded as `sysc-502`.

## Checklist

### Configuration and provider setup

- [x] **AIU-01 · P1** Preserve plugin settings across view open; only `ScopePlugin` messages replace plugin-wide configuration.
- [x] **AIU-09 · P2** Retain each view's instance ID and use that placement's preferences without leaking them to other widgets.
- [x] **AIU-02 · P1** Initialize the first collector set with configured API keys.
- [x] **AIU-08 · P2** Declare and pass the Command Code API-key setting.
- [x] **AIU-05 · P1** Make AI Usage settings reachable from its panel; opt in via the manifest and add host panel composition for the AI Usage schema.
- [x] **AIU-11 · P2** Parse the `history_retention` select value as a string so the 500/10000 options take effect.

### Collection and interaction correctness

- [x] **AIU-03 · P1** Retain setup-required reports across skipped rounds and provide a retry action.
- [x] **AIU-04 · P2** Preserve Codex's `StateNoData` instead of relabelling it fresh.
- [x] **AIU-07 · P1** Define a manual refresh/retry contract that can override retry backoff but never a provider's hard request floor; report deferred providers honestly.
- [x] **AIU-10 · P2** Publish the busy state before collection begins and visibly label the in-progress state.
- [x] **AIU-06 · P1** Honor `alerts_enabled` when evaluating and delivering threshold notifications.

### Panel readability

- [x] **AIU-17 · P2** Keep provider names and status labels readable within the shell's measured row widths; put complete status and error detail in the focusable provider tooltip.
- [x] **AIU-21 · P2** Preserve provider-specific `NoData` explanations in the provider-row tooltip as well as the detail pane.
- [x] **AIU-22 · P2** Keep setup guidance within the detail pane; leave the full credential-source diagnostics on the focusable provider-row tooltip.

### Provider readiness

- [x] Exercise all eight currently registered providers (Claude, Codex, Command Code, Copilot, Ollama, MiniMax, OpenCode Go, Synthetic) with deterministic tests and check setup/error semantics.
- [x] Align the stale Synthetic capture URL in the implementation plan with the documented/tested `/v2/quotas` route.
- [x] **AIU-12 · P2** Reject incomplete provider quota numbers and nonpositive Command Code caps instead of presenting invalid data as fresh zero usage (Synthetic, Ollama, Command Code, Codex).
- [x] **Live provider check — partial.** Codex, Command Code, Copilot, MiniMax, and Synthetic returned fresh reports. Copilot used the OpenCode OAuth access-token fallback; its access token is marked expired locally, but the quota endpoint accepted it. No refresh-token exchange occurred. An earlier Claude OAuth response did not match the current quota parser; no follow-up was sent because the local CLI token is expired and no OpenCode Anthropic token is present. Claude fallback timing is now fixture-tested. Ollama returned HTTP 200 with a monthly shape whose units and denominator remain undocumented; no percentage was inferred.
- [x] **AIU-18 · P2** Read Copilot's OpenCode `github-copilot` OAuth access token after existing `gh auth token` and environment sources; never refresh or write the OpenCode auth store. Fake-server and one safe live collector probe pass.
- [x] **AIU-19 · P1** Keep Claude credential fallback within the collector's 180-second scheduler floor: one credential request per `Fetch`, then try the next source only on a later due round.
- [x] **AIU-13 · P2** Polish the master/detail panel hierarchy and reduce repeated card surfaces; retain sysc-shell theme ownership. Provider identity, freshness, and headline gauge now share one summary band; repeated card fills are flattened into rows/sections with dividers.
- [x] **AIU-16 · P1** Keep provider API keys private in the plugin settings panel. The host now masks `_api_key` fields by default and provides an accessible Show/Hide control that preserves its state across panel rebuilds.
- [x] **AIU-14 · P1** Handle Ollama's observed monthly response without inventing units: surface an explanatory `NoData` state and skip identity lookup instead of faulting or deriving a percentage. Unit/limit interpretation remains unsupported until documented; no active subscription is needed for this deterministic path.
- [x] **SKIPPED LIVE · AIU-15 · P1** A live Claude parser check is unavailable: the local CLI token is expired and no OpenCode Anthropic token is present. Do not change parsing from an unrecorded response; known quota fixtures and candidate/floor behavior are tested. Revisit with a usable credential or captured redacted schema.
- [x] **AIU-20 · P2** Add OpenCode Go as an opt-in provider. Its official `/zen/go/v1/usage` response is fixture-tested for rolling, weekly, and monthly percentages/resets; the settings key, environment key, and read-only OpenCode API-auth fallback are supported. A 403 without a Go subscription is reported as explanatory `NoData`; no window lengths are inferred.
- [x] **Staged rendered-panel review.** A 750×430 PNG from the staged Supervisor/PanelHost renderer was inspected; AIU-22 was fixed and re-rendered. The actual compositor screenshot and scroll impression remain a manual pre-deploy check because the active plugin is from another worktree; the running install was left untouched.
- [x] **sysc-shell host validation.** The current shell renderer's `Convert → Layout/LayoutColumn` path passed through a temporary Go overlay; focused plugin-settings composition and API-key Show/Hide tests pass, and `go vet ./internal/shell` passes.
- [x] **sysc-shell staged install.** A temporary plugin directory with the new manifest and binary was loaded through sysc-shell's real `Supervisor`; provider collection was disabled before opening views, and panel/bar snapshots passed host conversion and layout. The active plugin path was not replaced and the shell was not restarted.

## Execution order

1. Fix configuration scope, per-instance state, and key initialization; add the missing Command Code key path.
2. Correct loop report retention, `StateNoData`, hard-floor scheduling, and busy-state publication.
3. Wire the alerts switch.
4. Add the in-panel settings path across the plugin manifest and host renderer (host write approved and applied).
5. Run focused checks and record which provider behavior is fixture-verified versus live-unverified.
6. Attempt Claude/Ollama live checks only with usable credentials and documented semantics; otherwise keep fixture coverage and honest no-data behavior, then continue provider work.
7. Apply the approved UI direction in the plugin view, then validate its composition and keyboard behavior in sysc-shell.
8. Build the plugin and exercise the in-panel settings through a staged sysc-shell runtime without changing saved settings or the user's cache.

## Verification record

- Verification update (2026-09-24): after Claude floor and Ollama monthly-shape handling, `GOMAXPROCS=4 go test -count=1 -p 2 ./...`, `GOMAXPROCS=4 go vet -p 2 ./...`, `GOMAXPROCS=4 go test -race -count=1 ./plugins/aiusage`, plugin build, and `git diff --check` passed. Focused sysc-shell settings, masking, keyboard traversal, and plugin focus-routing tests passed; `go vet ./internal/shell` passed.
- `go test ./...` and `go vet ./...` pass in the plugin repository; the AI Usage binary builds to `/tmp/sysc-plugin-aiusage-verify`.
- sysc-shell's focused AI Usage panel/settings and API-key privacy tests pass, as does `go vet ./internal/shell`. The full `internal/shell` package run still reports unrelated network-section and battery-widget test failures; its design-token check passes after removing the Show/Hide button's fixed width.
- Manifest JSON parses; whitespace and gofmt checks are clean.
- Final review regression was observed failing for zero/negative Command Code caps before the fix and passing afterward; focused tests and `go vet` pass.
- Live provider probe (2026-09-24): Codex, Command Code, MiniMax, and Synthetic returned fresh reports. Copilot now also returns a fresh report through its OpenCode access-token fallback. Claude's earlier OAuth response did not match the quota parser. Ollama returned HTTP 200 JSON whose top-level keys are `activity` and `limits`, with `limits.monthly` keys `models` and `usage`; no usage values were captured. All probes avoided saved settings and persistent report/history caches.
- Live-schema follow-up (2026-09-24): Ollama's redacted shape is `limits.monthly.models=array` and `limits.monthly.usage=number`. Current official [Cloud](https://docs.ollama.com/cloud) and [API Usage](https://docs.ollama.com/api/usage) docs still do not define this hosted-plan quota response; the decoder now reports it as `NoData` with a reason, without inferring a unit/denominator or sending the optional identity POST. No active Ollama Cloud or OpenCode Go subscription is available; fixture-backed behavior and other provider work continue. Smolbot's configured `minimax-portal` auth type is OAuth, not the API key expected by the coding-plan endpoint. The local Claude CLI token is expired and no OpenCode Anthropic token is present, so no live Claude retry was sent.
- OpenCode Go (2026-09-24): implementation matches the official [Go usage docs](https://opencode.ai/docs/go/) and [`GET /zen/go/v1/usage`](https://github.com/anomalyco/opencode/blob/dev/packages/console/app/src/routes/zen/go/v1/usage.ts). Deterministic tests cover the three windows, API-key source precedence, malformed quotas, and the no-subscription 403 path. No Go subscription is available for a live usage check; no values or account data are fabricated.
- No-data tooltip proof (2026-09-24): the provider-row test first failed because a no-subscription report exposed only “No data yet”; the shared tooltip now carries the provider's explanatory `Err`. The OpenCode Go row also passes the host layout-fit matrix.
- OpenCode Go settings integration (2026-09-24): sysc-shell's focused panel test first failed because its provider-settings allowlist omitted the new toggle/key. The host allowlist and test now include OpenCode Go; focused panel composition, masked-key reveal, and `go vet ./internal/shell` pass.
- Ollama no-data UI proof (2026-09-24): a fixture matching the observed monthly shape failed first as a generic provider fault. It now yields no percent/windows and an explanatory message; the view renders provider-specific no-data explanations while preserving the existing generic Codex copy. Focused collector and view tests pass.
- UI proof (2026-09-24): the temporary host overlay exercised `Convert` plus the real `ui.Layout`/`ui.LayoutColumn` at panel/bar bounds for API minors 2–7 and Fresh, stale Fault, NeedsSetup, NoData, and empty states; all passed. The host settings tests verify API-key masking, reveal state, and focusable labeling.
- Provider-row readability proof (2026-09-24): the new focused test failed first on `Command Code` needing 96px in an 88px button and on state labels needing 88–176px in a 64px slot. It passes after widening the name button to 112px, using compact one-line statuses, and retaining full state/error context in the provider tooltip. The row uses 270px of its 274px content budget; `go test ./plugins/aiusage` passes, including the host layout-fit matrix.
- Claude request-floor proof (2026-09-24): the new scheduler-backed fallback test failed first because one round sent both available OAuth candidates. `Fetch` now sends one request and remembers the next credential source without retaining its access token; the test confirms forced refresh stays deferred until 180 seconds and then tries the alternate source. Focused AI Usage tests pass.
- Staged runtime proof (2026-09-24, refreshed after AIU-18): the rebuilt manifest and binary loaded through `internal/plugin.Supervisor` from a disposable copy of the pinned sysc-shell source, completed the protocol handshake, accepted plugin-scope settings disabling all providers, and returned panel/bar snapshots that converted and laid out. It used an isolated cache under `/tmp`; no provider endpoint was called by this staged run and no active installation was replaced.
- Setup-guidance visual proof (2026-09-24): the staged panel's initial render showed the raw credential-search error clipped in the detail pane. The view now gives three concise next steps and retains the full source list in the provider-row tooltip. The regression was observed failing before the change; plugin tests, staged host rendering, PNG decode/dimensions, and the re-render passed.
- The installed sysc-shell process was left running. Its plugin symlink resolves to `/home/nomadx/sysc-plugins/.worktrees/feature/wallpaper-depth/plugins/aiusage`, whose `view.go` differs from this worktree. No service restart or plugin replacement was performed. An off-screen staged screenshot was captured and inspected; an actual Niri compositor screenshot and real scroll/focus impression remain unverified.
