<!-- Hallmark · pre-emit critique: P5 H5 E5 S5 R5 V4 -->

# AI Usage plugin audit and defect ledger

Original audit: 2026-09-23. Updated 2026-09-24 with remediation, live
quota-only probes, and a sysc-shell host-layout check. Credentials, response
bodies, account identifiers, and quota amounts were not printed; saved plugin
settings were not read.

## Audit result

The original audit found collectors for Claude, Codex, Command Code, Copilot,
Ollama, MiniMax, and Synthetic; AIU-20 added OpenCode Go, so the current
registry has eight. Only Claude, Codex, and Command Code are enabled by
default; the other five have track flags defaulting to false. Claude also
needs a local OAuth credential. The requested providers are now present in
source, but they are not all active or proven live.

The strongest source-level explanation for seeing only Codex and Command Code
is the settings lifecycle defect below: the shell sends saved plugin settings
when it starts the plugin, then the plugin resets them on its first view. A
provider without a successful cached read can also disappear on a skipped
round. The precise live result depends on the saved widget instance and
credentials, which were not read.

## Defects, ordered by impact

### AIU-01 · P1 · Settings lifecycle resets the saved provider configuration

The host sends ScopePlugin settings at plugin start
(/home/nomadx/sysc-shell/internal/shell/pluginhost.go:233-262). On the first
ViewOpen, the plugin then calls ensure(nil), which resolves defaults and
reconfigures the collector loop (cmd/sysc-plugin-aiusage/main.go:51-60,
165-176). The host also sends ScopeInstance settings after ViewOpen
(pluginhost.go:586-600), but the plugin handles every SettingsChanged message
identically, ignoring Scope and Instance (main.go:216-219).

As a result, the first view can discard saved provider toggles, API keys,
thresholds, and widget preferences. Missing track flags resolve to defaults:
Claude, Codex, and Command Code on; Copilot, Ollama, MiniMax, and Synthetic
off (main.go:263-331). This can make enabled providers vanish and explains why
the configured provider set may not match what the user sees.

Fix direction: retain plugin-scope configuration independently; apply
instance-scope values only to the matching widget instance; do not replace
already received settings with defaults when a view opens.

### AIU-02 · P1 · Saved API keys are not installed into the first collector set

The initial ensure path constructs NewLoop with Config.Keys but an empty Env
(main.go:51-56). NewLoop stores the config and immediately builds collectors
(plugins/aiusage/loop.go:70-99), but only Reconfigure copies Config.Keys into
Env.Keys (loop.go:101-110). Because the shell pushes plugin settings as the
plugin starts, the first forced fetch can run before any Reconfigure and miss
keys entered in the plugin settings.

Fix direction: make NewLoop initialize the collector environment from its
Config before constructing collectors.

### AIU-03 · P1 · Setup-required providers disappear while waiting for their next read

On a setup error, Round sets nextDue and includes the NeedsSetup report
(plugins/aiusage/loop.go:219-225), but does not save it as lastGood. On a
not-due round, the loop appends only lastGood and otherwise omits that provider
(loop.go:210-215). The detail view's NeedsSetup branch also returns before
adding a Retry action (plugins/aiusage/view.go:520-533); Retry is rendered only
for StateFault (view.go:534-563).

The outcome is transient or missing setup guidance. A normal refresh can
replace a visible setup row with no row until the provider is due again.

Fix direction: preserve the latest report state across scheduled skips, and
give setup-required providers a usable retry or settings path.

### AIU-04 · P2 · Codex no-data reports are relabelled as fresh

The Codex collector deliberately returns StateNoData with no error when there
is no compatible session snapshot (plugins/aiusage/codex.go:173-184). Round's
nil-error branch unconditionally changes that state to StateFresh
(plugins/aiusage/loop.go:239-244). The panel's NoData message therefore does
not describe this normal Codex state.

Fix direction: preserve the collector's state and only mark a report fresh
when it contains a valid fresh reading.

### AIU-05 · P1 · Settings are absent from the AI Usage panel

The AI Usage panel manifest does not set include_settings
(plugins/aiusage/manifest.json:70-72). The host composes settings into a plugin
panel only when that flag is true
(/home/nomadx/sysc-shell/internal/shell/pluginhost.go:866-888). Its inline
panel settings helper currently recognizes only screen-recorder keys
(popout_plugins.go:100-130), so adding the flag alone would still render no AI
Usage settings.

The host's general Plugins settings page does render every manifest setting
(popout_plugins.go:39-97). The gap is configuration from within this
plugin's own panel, not a missing settings schema.

Fix direction: extend the host's panel composition to render the AI Usage
schema, then opt the AI Usage panel into it (or provide an explicit route to
the general plugin settings page).

### AIU-06 · P1 · The Threshold alerts switch has no effect

The manifest exposes alerts_enabled and uses it to hide the threshold fields
(plugins/aiusage/manifest.json:110-119). The setting is not read by
resolveConfig, and round always calls CheckAlerts
(cmd/sysc-plugin-aiusage/main.go:105-124). AlertConfig has no enabled field
(plugins/aiusage/alerts.go:60-78).

Turning the switch off therefore hides controls but does not stop
notifications.

Fix direction: wire the setting through configuration and gate alert
evaluation and delivery with it.

### AIU-07 · P1 · Refresh behavior conflicts with its label and provider rate limits

The panel promises “Fetch every tracked provider now”
(plugins/aiusage/view.go:305-308), but the refresh action calls round(false)
(cmd/sysc-plugin-aiusage/main.go:189-192). Round skips collectors whose
nextDue has not arrived (plugins/aiusage/loop.go:193-215), so refresh can
produce no new read. Conversely, Retry clears nextDue
(loop.go:178-184), and SettingsChanged calls round(true)
(main.go:216-219); both can bypass the Claude collector's 180-second floor
(plugins/aiusage/oauth.go:43-46; loop.go:193-210).

Fix direction: define one explicit, rate-limit-safe manual refresh contract.
Keep provider floors separate from retry backoff, and make the button's label
and result say which providers were refreshed or deferred.

### AIU-08 · P2 · Command Code cannot receive a pasted key through its declared settings

The Command Code collector looks for a plugin setting named commandcode
(plugins/aiusage/direct.go:129-145), but the manifest declares no
commandcode_api_key and resolveConfig only passes keys for Ollama, MiniMax,
and Synthetic (manifest.json:73-102; main.go:318-322). Environment and local
auth-file credentials still work. However, the collector's 401 message says
to replace the key in settings (direct.go:85-97), where no such field exists.

Fix direction: either add and pass a Command Code key setting, or make the
setup and rejected-key messages describe only the supported environment and
auth-file paths.

### AIU-09 · P2 · Widget settings are global instead of per instance

The manifest declares settings per bar widget instance
(plugins/aiusage/manifest.json:11-68), and the host sends an instance ID with
ViewOpen and ScopeInstance messages
(pluginhost.go:586-600). The plugin stores only one Instance value and its view
records contain only kind and revision (main.go:43-66); publish applies that
same Instance to every bar and tooltip (main.go:81-101).

With multiple AI Usage widgets, the last instance settings received win for
all of them.

Fix direction: retain each view's instance ID and resolve bar preferences
from that instance's settings.

### AIU-10 · P2 · The refresh busy state is never published

round publishes before setting Loading=true, then does not publish again
until after the round has finished
(cmd/sysc-plugin-aiusage/main.go:105-112). The refresh button reads
Report.Loading to disable itself (plugins/aiusage/view.go:305-308), so the
busy state is never visible.

Fix direction: set Loading before publishing the in-progress view.

### AIU-16 · P1 · Provider API keys were visible in the plugin settings panel

The host's generic plugin string field rendered saved API keys as clear text.
Anyone looking at the open settings panel could read them, even though the
native field already supports masked display.

**Status:** fixed in sysc-shell: `_api_key` settings are masked by default and
have a keyboard-focusable Show/Hide control. The field state preserves the
reveal choice across panel rebuilds; the focused regression test passes.

## Provider readiness

| Provider | Tracked by default | Source and setup path | Live probe (2026-09-24) |
|---|---:|---|---|
| Claude | Yes | OAuth token from Claude Code or OpenCode files; endpoint is undocumented and rate-limited (plugins/aiusage/oauth.go). | Earlier OAuth response did not match the quota parser. The CLI token is expired and no OpenCode Anthropic token is present; candidate cadence is fixture-covered. |
| Codex | Yes | Local session JSONL snapshots; no network credential required (plugins/aiusage/codex.go). | Fresh report. |
| Command Code | Yes | Alpha billing endpoint; settings, env, or local auth file (plugins/aiusage/direct.go). | Fresh report. |
| Copilot | No | GitHub token from `gh auth token`, environment, or OpenCode's `github-copilot` OAuth access field; uses `copilot_internal/user` (plugins/aiusage/copilot.go). | Fresh report via OpenCode access token. No refresh-token exchange or auth-store write. |
| Ollama | No | Ollama Cloud plan usage, not local inference usage; settings or `OLLAMA_API_KEY` (plugins/aiusage/direct.go). | HTTP 200 monthly shape is reported as explanatory `NoData`; units/denominator remain undocumented, so no percentage is inferred. |
| MiniMax | No | `api.minimax.io` coding-plan endpoint; settings, env, `~/.minimax/api_key`, or Codexbar's provider config (plugins/aiusage/direct.go). The local Smolbot `minimax-portal` profile is OAuth, not this API-key credential. | Fresh report. |
| OpenCode Go | No | Opt-in collector. Key source order: plugin setting, `OPENCODE_GO_API_KEY`, then the `opencode` API-key entry in OpenCode's auth store (read-only). Official usage endpoint: `GET /zen/go/v1/usage`. | Fixture-verified for all three windows and the no-subscription 403. No live usage check; no Go subscription is available. |
| Synthetic | No | Synthetic API key; settings, env, or local key file (plugins/aiusage/synthetic.go). | Fresh report. |

The collector tests use controlled HTTP servers and fixtures; they do not
prove that all provider credentials, endpoints, or response schemas work for
every account. Synthetic's live probe used the documented/tested `/v2/quotas`
route; the stale capture path noted in the original implementation plan was
corrected. Current live results are shown above and remain partial.

The source confirms implementation presence for the requested providers, not
universal live availability. The table above records the checks actually
performed; it does not prove live Claude/Ollama quota rendering.

## Hallmark audit

Target: the native plugin view tree in `plugins/aiusage/view.go` and its
sysc-shell composition. The earlier 0/0/0 conclusion was incorrect: the host
wraps included settings in `monitorCard`
(`/home/nomadx/sysc-shell/internal/shell/pluginhost.go:883`), while the plugin
added card fills to its fleet row, provider rows, history, stale record, and
each quota window. That repeated card-in-card treatment is one confirmed major
finding.

The major card-in-card finding was fixed by removing the repeated neutral card
fills, keeping active provider selection distinct, and grouping
identity/freshness beside the headline gauge. That source audit found
**0 critical · 0 major · 0 minor**; sysc-shell still owns the palette and type.
A later text-fit pass found provider name/status clipping, recorded as AIU-17
below and now fixed. The tree converted and laid out successfully through the
current shell renderer for API minors 2–7 and tested provider states. Rendered
screen rhythm, scrolling, and real keyboard focus remain unverified because
this session has no Niri socket.

## Carry into settings work

The API-key settings remain ordinary string values in host-managed config;
the panel now masks them visually by default, with an explicit Show/Hide
control. Masking is display privacy, not encrypted storage, so the host's
existing config-file permission policy still matters.

Token and cost analytics, multi-account support, Gemini, Cursor, Kimi, and
other listed providers are documented v1 non-goals
(docs/plans/2026-09-19-aiusage-design.md:55-61), so they are not counted here
as defects.

## Remediation follow-up finding (2026-09-24)

### AIU-14 · P1 · Ollama monthly usage units and denominator are undocumented

The live `/api/usage` response uses `limits.monthly.models` plus a numeric
`limits.monthly.usage`, while the current official Cloud and API Usage docs do
not define the unit or a quota denominator. Treating the value as a fraction
or percentage would invent account usage semantics.

**Status:** safe fallback implemented. The observed monthly shape becomes
`StateNoData` with an explanation; it produces no usage window/percentage and
does not call the optional identity endpoint. Fixture tests cover this path.
Actual monthly usage presentation remains unsupported until the provider
documents the unit and limit.

### AIU-11 · P2 · History retention select values were ignored

The manifest declares `history_retention` as a select with string values
(`plugins/aiusage/manifest.json:168-175`), but `resolveConfig` originally read
it through the integer helper that accepts only JSON numbers
(`cmd/sysc-plugin-aiusage/main.go:371`). Choosing 500 or 10000 therefore kept
the 2000-line default.

**Status:** fixed during remediation by accepting the three declared select
values; `TestResolveConfigParsesHistoryRetentionSelect` pins all options.

### AIU-12 · P2 · Incomplete provider quota fields silently became zero usage

Several response structs decoded required numeric values directly into
`float64`. Go's JSON decoder leaves absent numeric fields at zero, so bodies
such as a Synthetic subscription with a limit but no request count, or an
Ollama quota object with no `usage`, could be presented as a fresh 0%-used
reading. Command Code quota windows had the same issue for `used` and `cap`;
Codex session rows could be treated as valid when a window omitted
`used_percent`.

**Status:** fixed by preserving field presence for those readings and
rejecting incomplete API quota objects or skipping incomplete Codex snapshots.
Final review also found that a present Command Code `cap <= 0` still computed
as a fresh 0% window; the collector now rejects it. Regression tests cover
missing fields and zero/negative caps. Claude, Copilot, and MiniMax already
retain presence for their quota values.

### AIU-17 · P2 · Provider names and status labels clipped in the master list

The provider-name button was 88px wide, below the host's measured 96px for
`Command Code`. The adjacent status column was 64px, while labels such as
`Needs setup` and `Read failed` measure 88px and `Refresh deferred · 1m`
measures 176px under the host's `len(text) × 8` metric. The host clips text at
the row/column boundary, so these labels could not be read from the provider
list.

**Status:** fixed by giving the provider button 112px, using compact one-line
states (`Setup`, `Error`, `No data`, `Wait`, `Stale`, `Ready`), and placing the
full provider/status/error/quota detail in the provider button's tooltip. The
row uses 270px of its 274px content budget. A red/green regression test covers
six representative states and the `Command Code` name; the plugin's host-layout
fit matrix passes afterward.

### AIU-18 · P2 · Copilot ignored OpenCode's stored OAuth access token

The collector originally tried `gh auth token` and environment variables, but
not the `github-copilot` OAuth entry in OpenCode's auth store. A safe
quota-only request using its access field returned HTTP 200 and the expected
snapshot structure, even though local expiry metadata was in the past.

**Status:** fixed. The collector reads only the OAuth `access` field after
existing credential sources. It never refreshes or writes OpenCode's shared
auth store. A fake-server regression and a live collector probe pass.

### AIU-19 · P1 · Claude credential fallback bypassed the request floor

`oauthUsageCollector.Fetch` tried every credential candidate sequentially.
The 180-second `Floor()` was enforced by the loop only between `Fetch` calls,
so a failed Claude Code token could be followed immediately by an OpenCode
token in the same round.

**Status:** fixed. A round now sends one OAuth usage request and records only
the next credential source, not its token. The loop's existing floor governs
when that alternate may be tried. A regression test was observed failing with
two same-round requests, then passing with a deferred forced refresh and a
fallback after the floor.

### AIU-20 · P2 · OpenCode Go is not registered

OpenCode Go was not one of the seven original collectors. Official Go docs
describe usage windows; the project's own server source exposes
`GET /zen/go/v1/usage` with a Bearer key and
`{usage:{rolling,weekly,monthly:{status,percent,resetsAt}}}`. The endpoint
returns 403 when the account has no Go subscription.

**Status:** fixed. The opt-in collector accepts the settings key, the
`OPENCODE_GO_API_KEY` environment variable, or OpenCode's `opencode` API-key
entry read-only. Fixture tests cover usage parsing, strict quota validation,
credential precedence, and the explanatory no-subscription 403 response.
Window lengths are not inferred from reset timestamps, so pace remains
unavailable until the provider reports a duration.

### AIU-21 · P2 · Provider-row tooltips hid no-data explanations

For `StateNoData`, the row tooltip showed only “No data yet” even when the
collector supplied a provider-specific explanation. That hid why OpenCode Go
needs a subscription and why Ollama's observed usage cannot be interpreted
from the available documentation unless the user opened the detail pane.

**Status:** fixed. The provider-row tooltip now includes the report's
explanation. A focused test was observed failing on the OpenCode Go
subscription-required row before the fix and passing afterward; its tooltip and
detail pane both retain the reason.

### AIU-22 · P2 · Setup guidance overflowed in the detail pane

The `StateNeedsSetup` detail pane rendered the full credential-search error as
one unbounded text line. In the staged sysc-shell render, the source list was
clipped at the right edge, obscuring the next step. The provider-row tooltip
already carried the complete diagnostic.

**Status:** fixed. The detail pane now gives concise guidance to check the
settings below or focus the provider row for credential options; the tooltip
retains the full source list. A regression test was observed failing before
the fix and passing afterward. The staged `PanelHost.render` PNG was re-captured
and visually inspected at 750×430.

## Current disposition — 2026-09-24

| Items | Disposition |
|---|---|
| AIU-01–AIU-12 | Fixed and covered by focused plugin/configuration/provider tests; AIU-05 also required the sysc-shell settings-panel integration. |
| AIU-13, AIU-16, AIU-17, AIU-18, AIU-22 | UI hierarchy, provider-row readability, API-key privacy, OpenCode Copilot auth, and setup-message fit are implemented. Host conversion/layout, key masking, source precedence, fixture coverage, redacted live Copilot fetch, settings traversal, plugin-panel focus routing, and staged PNG review pass. Actual compositor screenshot/scroll impression remains unverified. |
| AIU-14 | Safe fallback fixed and fixture-covered: unsupported monthly usage is shown as `NoData`, without inferred units/percentages or identity POST. Actual numeric presentation remains deferred pending documented semantics. |
| AIU-15 | Deferred until a usable Claude OAuth credential is available for the earlier parser mismatch. The local CLI token is expired and no OpenCode Anthropic token is present; the production fallback path itself is covered by AIU-19. |
| AIU-19 | Fixed: one request per Claude collector round; subsequent credential candidate is only tried after the loop's 180-second floor. Scheduler-backed fake-server regression passes. |
| AIU-20, AIU-21 | Fixed and fixture-verified. OpenCode Go is registered as opt-in, its API-key auth sources are wired read-only, the documented 403 entitlement response is clear, and no-data explanations reach the provider-row tooltip. Live Go usage remains unverified without a subscription. |
| Copilot live availability | Verified: the OpenCode OAuth access token returned a fresh report through the collector. The refresh token is not used. |
| Rendered panel / real key traversal | Staged host layout, plugin-settings composition, API-key reveal, settings traversal, plugin-panel focus-routing tests, and off-screen 750×430 screenshot review pass. Actual compositor screenshot and scroll impression were skipped because the running plugin is from another worktree and no panel is open; no replacement/restart was made. |
| sysc-shell staged runtime | Passed: a temporary install completed the real host handshake and returned panel/bar snapshots that converted and laid out, with all providers disabled and no live endpoints contacted. |

The current sysc-shell process was left running. Its installed plugin is from a
separate worktree and was not replaced. Focused host settings tests and vet
pass; the full shell package still has unrelated network-section and battery
widget failures. A recent journal scan found no `plugin view rejected`
message. The Niri socket was recovered from the live shell process, but no
AI Usage panel was open for a rendered check.
