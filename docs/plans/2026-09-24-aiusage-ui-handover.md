# HANDOVER — AI Usage pre-deploy review

Status below reflects the 2026-09-24 verification pass. Later sections retain
historical RCA and incident notes; verify them against the live workspace
before reuse.

## Current status — 2026-09-24

- Plugin checkout `/home/nomadx/sysc-plugins` is on `main`, ahead of origin by
  34 commits, with existing dirty changes. Preserve the whole worktree.
- sysc-shell checkout `/home/nomadx/sysc-shell` is on `main`, behind origin by
  14 commits, and already dirty. The approved settings integration now also
  masks `_api_key` fields by default and adds a keyboard-focusable Show/Hide
  control in `internal/shell/popout_plugins.go`. OpenCode Go's provider toggle
  and key are included in the panel allowlist; focused composition and masking
  tests pass. Preserve its unrelated Beads, KDE Connect, and plugin-failure
  changes.
- Plugin tests (`GOMAXPROCS=4 go test -count=1 -p 2 ./...`), vet, and the
  AI Usage race test and binary build pass. Copilot now reads OpenCode's OAuth `access`
  field after existing credential sources; a live collector probe returned a
  fresh report without using the refresh token or writing the auth store.
  sysc-shell focused AI Usage settings
  tests and `go vet ./internal/shell` pass. Its full `internal/shell` package
  still has four unrelated network-section and battery-widget failures.
- The initial hierarchy pass had **0 critical · 0 major · 0 minor** findings.
  A later text-fit review found and fixed AIU-17: `Command Code` and several
  provider states were clipped in the master list. Names now have a 112px
  button; concise state labels fit their lane, with full status and error
  detail in the focusable tooltip, including explanations for `NoData` states.
  The focused regression was observed failing
  before the fix and passing after it; the full plugin tests, vet, and build
  pass. A temporary overlay exercised sysc-shell `Convert`, `ui.Layout`, and
  `ui.LayoutColumn` for API minors 2–7 and fresh, stale-fault, setup-required,
  no-data, and empty states before this small row adjustment. The current
  provider row also passes the plugin's host layout-fit matrix. Focused
  sysc-shell settings traversal and plugin-panel focus-routing tests pass;
  a staged 750×430 `PanelHost.render` screenshot was captured and inspected.
  It exposed setup guidance clipped at the detail-pane edge; AIU-22 now replaces
  that raw line with concise next steps while keeping the full credential
  sources in the focusable provider tooltip. Plugin and staged shell checks pass
  after the fix. Actual compositor rhythm and scroll impression remain
  unverified.
  The rebuilt current binary also passed the staged Supervisor run against a
  disposable copy of the pinned sysc-shell source, with all providers disabled
  and cache isolated under `/tmp`; no active installation or live endpoint was
  touched by that staged run.
- Safe live probes returned fresh reports for Codex, Command Code, Copilot,
  MiniMax, and Synthetic. Copilot used OpenCode's access token; the stored
  expiry metadata was stale but the quota request succeeded. Claude's earlier
  response did not match the parser; the local CLI token is now expired and
  there is no OpenCode Anthropic token for a safe follow-up. Ollama returned
  HTTP 200 with a monthly shape that is now surfaced as `NoData` because its
  units and denominator are undocumented. Smolbot's local MiniMax profile is
  OAuth, not the API key the coding-plan adapter expects.
- A scheduler-backed Claude regression exposed back-to-back requests across
  multiple credential candidates within one round. `Fetch` now tries one
  candidate per round and advances only after failure; the loop enforces the
  180-second floor even on forced refresh. The regression was observed failing
  before the fix and passing after. The earlier live response/parser mismatch
  remains unverified; missing Ollama/OpenCode Go subscriptions do not block
  fixture-backed work or other providers.
- OpenCode Go is now registered as an opt-in provider. It reads the plugin
  setting, `OPENCODE_GO_API_KEY`, or OpenCode's `opencode` API-key entry in
  read-only order; it never refreshes or rewrites that store. Fixture tests
  cover the official three-window response, key precedence, invalid quota
  fields, and the subscription-required 403. The live usage check is skipped
  because no Go subscription is available; no quota values are inferred.
- The running sysc-shell process and installed plugin were left untouched; the
  installed plugin resolves to a separate `feature/wallpaper-depth` worktree
  whose `view.go` differs from this one. No replacement, restart, or deploy was
  done. The Niri socket was recovered from the live shell process, but no AI
  Usage panel was open. The staged runtime, focused host keyboard tests, and an
  off-screen rendered screenshot pass; a real compositor screenshot and scroll
  impression remain unverified.
- Active worklist: [AI Usage workplan](docs/plans/2026-09-23-aiusage-workplan.md).
  Audit and defect dispositions: [AI Usage defect ledger](docs/plans/2026-09-23-aiusage-defect-ledger.md).
- Verification after OpenCode Go: `GOMAXPROCS=4 go test -count=1 -p 2 ./...`,
  `GOMAXPROCS=4 go vet -p 2 ./...`, `GOMAXPROCS=4 go test -race -count=1
  ./plugins/aiusage`, the plugin build, focused sysc-shell settings/masking
  tests, and `go vet ./internal/shell` pass. AI Usage's manifest passed the
  manifest scan; the repository-wide scan still fails on the unchanged
  Wallpaper Depth manifest's unsupported `wallpaper` capability.
- The actual compositor screenshot and scroll impression remain unverified:
  the running install resolves to a separate plugin worktree, and that install
  was deliberately left untouched. The current source was reviewed through an
  off-screen PNG rendered by the staged sysc-shell host. No replacement, deploy,
  or shell restart was performed.

The deploy recipe below is historical; it is not authorization to deploy or
restart the running shell.

## Repos and running state (historical — verify before use)

| What | Where | State |
|---|---|---|
| Plugin repo (your main workspace) | `/home/nomadx/sysc-plugins` | branch `main`, latest `ceb150a` + uncommitted view.go changes (see Pending fix) |
| Shell repo | `/home/nomadx/sysc-shell` | branch `feat/plugin-material-icons` at `a04ce1a`; **main** is at `e49d1bc` (merge + minor-5/6/7 + journal logging) |
| Deployed shell binary | `~/.local/bin/sysc-shell` | built from merged main **before** the log commit — rebuild it (Task 4) |
| Deployed plugin binary | `~/.config/sysc-shell/plugins/aiusage/bin/` (symlink into the repo) | current plugin build |
| Service | `systemctl --user restart sysc-shell.service` | restarting flashes the bar; the user loses the bar/launcher for ~2 s — always announce a restart, never loop restarts |
| Config (LIVE, WIP schema — DO NOT "fix" it) | `~/.config/sysc-shell/config.json` | contains `plugins.enabled` array, `tray.enabled:false`, per-plugin `enabled` flags — these are the user's WIP schema; stripping them broke the desktop once already (see Incident log) |

**Commit hook** (`~/.git-hooks/commit-msg`) rejects messages containing,
case-insensitively: `claude, anthropic, chatgpt, openai, copilot, cursor,
cody, tabnine, codex, gemini, bard, gpt-N, llm, ai assistant, bot, agent,
co-authored-by, generated with/by`. Beware sneaky substrings: **"both"
contains "bot"**, **"hallmark" contains "llm"**. Rephrase (e.g. "each is
kept", "ui audit fixes").

**Repos are the user's live workspace.** Other agents/the user edit them
concurrently. `git status` before every commit; stage only files you changed.
Never `stash`, `checkout .`, or `reset --hard` in `~/sysc-shell` — the user's
WIP lives in the working tree and in stashes (`stash@{0}` is theirs).

---

## Historical layout bug (fixed; exact root cause)

User-reported error on clicking the AI Usage bar pill:

```
ui: child 0: ui: child 0: child 0 of kind 1 does not fit in 274x12
```

followed by the host failure surface (Close / Retry / Disable buttons).

**Decode:**
- ui kinds: `KindRow=0, KindText=1, KindMeter=2, KindButton=3, KindGraph=4, KindColumn=5, KindSeparator=6` (`~/sysc-shell/internal/ui/tree.go` ~line 10).
- So: a **Text** node (kind 1) was given a box **274 wide × 12 tall** and its measured height exceeds 12.
- Chain: panel root (column) → child 0 = master/detail **row** → row child 0 = the **list scroll** → scroll child 0's content = the header/row area. 274 = 290 (list width) − 16 (provider row Padding 8×2). 12 = **provider row `Height: 28` − 2×8 padding**.
- Root cause: **`Height` includes padding.** The provider row declares `Height: 28` but has `Padding: 8`, leaving a 12px content box for 26px-tall controls (monogram disc 26, name button ~26). The same bug is pending on the fleet rollup row (`Padding 8, Height 28`).
- The measure function for plugin views is `pluginMeasure` in `~/sysc-shell/internal/shell/pluginhost.go:76`: `return len(s)*8, 16` — every text is 16 tall regardless of role. Rows containing only text measure 16.

## Fix applied in current view.go

1. In `/home/nomadx/sysc-plugins/plugins/aiusage/view.go`, provider and
   fleet rows now use `Height: 42` with `Padding: 8`: the 26px content box fits
   the monogram/button and the row controls. Do not reapply the old 44px value
   without recalculating the current content.
2. The UI pass also removed the repeated card fills, placed the identity,
   freshness, and headline gauge in one summary row, and uses dividers between
   quota sections.
3. A temporary overlay test exercised the current plugin through the shell's
   `Convert` and layout implementation. It passed across API minors 2–7 and
   fresh, stale-fault, setup-required, no-data, and empty states. The current
   shell checkout and installed binaries were not modified.

## Historical in-place test recipe (superseded; avoid editing the shell checkout)

The old recipe below proposed editing sysc-shell's `go.mod` and adding a test
file directly to that dirty checkout. The current verification used a copied
modfile and Go overlay under `/tmp` instead; do not repeat the in-place steps.

The host silently swallowed layout errors until commit `e49d1bc` (journal
logging is now live in the *deployed* binary only after Task 4's restart).
But you can and must verify **without deploying or clicking**:

**Layout probe (throwaway, in the shell repo):**

1. `cd ~/sysc-shell && go mod edit -require=github.com/Nomadcxx/sysc-plugins@v0.0.0 -replace=github.com/Nomadcxx/sysc-plugins=/home/nomadx/sysc-plugins && go mod tidy`
2. Create `internal/plugin/aiusage_layout_probe_test.go` (package `plugin`):

```go
package plugin

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/aiusage"
	ui "github.com/Nomadcxx/sysc-shell/internal/ui"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestAiusageLayoutProbe(t *testing.T) {
	measure := func(s string, _ ui.TextAttrs) (int, int) { return len(s) * 8, 16 }
	cfg := aiusage.Config{Warn: 85, Crit: 95, Refresh: time.Minute, HostMinor: 7}
	rep := aiusage.Report{Providers: []aiusage.ProviderReport{
		{ID: "claude", Name: "Claude", Plan: "Pro", State: aiusage.StateFresh,
			UpdatedAt: time.Now(), Windows: []aiusage.Window{{
				Key: "primary", Label: "Session", HasPercent: true,
				UsedPercent: 99, WindowMinutes: 300, ResetsAt: time.Now().Add(2 * time.Hour),
			}}},
		{ID: "codex", Name: "Codex", State: aiusage.StateFault, Stale: true,
			Err: "boom", UpdatedAt: time.Now(),
			Windows: []aiusage.Window{{Key: "primary", Label: "Session",
				HasPercent: true, UsedPercent: 40, WindowMinutes: 300}}},
	}}
	inst := aiusage.DefaultInstance()
	now := time.Now()

	bar := aiusage.BarTree(rep, inst, cfg, 7, now)
	if err := ui.Layout(bar, ui.Rect{W: 240, H: 32}, measure); err != nil {
		t.Errorf("bar layout: %v", err)
	}
	panel := aiusage.PanelTree(rep, "claude", nil, cfg, 7, now)
	if err := ui.LayoutColumn(panel, ui.Rect{W: 750, H: 430}, measure); err != nil {
		t.Errorf("panel layout: %v", err)
	}
}
```

3. `go test ./internal/plugin/ -run AiusageLayoutProbe -v` — iterate on
   `view.go` until **both layouts return nil**, across
   `HostMinor` ∈ {7, 6, 5, 4, 3, 2} and the states Fresh / Fault(stale) /
   NeedsSetup / NoData / empty (add subtests).
4. **Then remove the probe test file and revert the go.mod replace/require**
   (`go mod edit -dropreplace=github.com/Nomadcxx/sysc-plugins` and drop the
   require) — the shell's pin must stay a real pinned version. Do not commit
   the replace.
5. The plugin's own tests must still pass: `go test -race ./plugins/aiusage/`
   from `/home/nomadx/sysc-plugins`.

Why the probe works: the host's real pipeline is exactly
`v1.Validate → Convert → ui.Layout/LayoutColumn` with `pluginMeasure`
(`internal/plugin/prepare.go:198-206`, `pluginhost.go:76`), and the probe
duplicates it with the same measure. Root-kind rule: bar root must be
`row`, panel/tooltip root must be `column` (`internal/plugin/view.go:31-41`)
— this rule is NOT in `v1.Validate`, so structural assertions in the plugin
tests pin it (`TestViewRootKindsMatchTheHostConverter`).

## Historical deployment recipe (do not run without an explicit deploy request)

```bash
cd ~/sysc-plugins && make build && make install
cd ~/sysc-shell && go build -o ~/.local/bin/sysc-shell ./cmd/sysc-shell
systemctl --user restart sysc-shell.service   # announce to the user first
systemctl --user is-active sysc-shell.service
ps -eo args | grep 'bin/sysc-plugin-aiusage$' | grep -v grep   # must list 1
journalctl --user -u sysc-shell.service --since '-1min' | grep -i 'rejected'  # must be empty
```

Then ask the user to click the pill. If it still fails, the journal now logs
the full rejection (`"plugin view rejected"` — commit `e49d1bc`): read it with
`journalctl --user -u sysc-shell.service -n 20` and fix against the real error.

## Layout rules of this shell (learned the hard way — cite-checked)

- Bar root = `row`; panel/tooltip root = `column` (converter refuses otherwise; plugin tests cannot catch it — `v1.Validate` is root-kind-blind).
- **`Height` includes padding.** Content box = Height − 2×Padding. A 28 row with Padding 8 has 12 of content.
- Scroll (`list`) children get `content.W` × natural height; declared child Height is honored by the row/column measure cases (`columnChildHeight`), so rows in scrolls CAN declare height — but padding still eats it.
- Convert copies `Width` for `list` only after commit `dfd915b` (older shells drop it → scrolls default to 400 wide).
- Text measures via `pluginMeasure` = `len×8, 16` — real glyph metrics differ; the bar band is 32 tall (`pluginBarViewHeight`), plugin bar width 240 (`pluginBarViewWidth`).
- Buttons render ~26 tall (text 16 + padding); any row holding one needs Height ≥ 26 + its padding.
- `animate: true` requires `key` and only on `progress`/`gauge` (minor 6); `absent` is legal on gauge/graph/progress (minor 7); `stroke`/`stroke_fill` on containers+buttons (minor 5).
- Icons: project font glyphs convert to `text`; the catalogue is `[a-z0-9-]` names (`ai-usage` exists; `speed` is the fallback).
- Failure surface: the host shows Close/Retry/Disable and keeps the last error string in `v.Label` — it persists until a successful publish, so an old error can outlive its cause after a respawn (a full service restart clears it).

## Incident log (do not repeat)

1. Restarting the session shell without announcing it locked the user out of the bar/launcher during an urgent moment. Always announce restarts; batch them.
2. A recursive config "fix" stripped every `"enabled"` key from the live config — including `plugins.enabled` (the WIP schema's enable list) and `tray.enabled:false` — which killed plugin startup and re-enabled the tray. The live config is WIP-schema (`plugins.enabled` array, `tray.enabled`, per-plugin `enabled` flags) and the deployed shell binary must be built from the tree that understands it. **Never strip or "normalize" the live config again.**
3. A binary/config schema mismatch caused a crash-loop and a second lockout. Check `go list -m` + the deployed binary's mtime before assuming which schema is live.
4. Killing the plugin process to swap its binary leaves the host's failure badge up until a service restart. Prefer a full `systemctl --user restart sysc-shell.service` when the plugin binary changes.

## Current fleet (as of handover)

weather, timer, world-clock, aiusage, kdeconnect — all running. The bar pill
renders (animated dial at 99, error tone). Only the panel is broken.
