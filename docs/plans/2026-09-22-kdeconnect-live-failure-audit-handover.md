# Handover: kdeconnect live-failure audit commission

Date: 2026-09-22
From: the agent that planned and executed the kdeconnect live test round
To: the auditing agent taking over
Repos: `/home/nomadx/sysc-plugins` (main @ `3d05d35`, clean), `/home/nomadx/sysc-shell` (main @ `70f495e`, pushed; main checkout sits on `feat/plugin-material-icons` with the owner's uncommitted WIP — leave it alone)
Live target: laptop `archThink`, `ssh -p 7777 nomadx@192.168.0.64`

You are commissioned to audit the **failed live test round** of the KDE Connect plugin, to
commission two parallel sub-audits while you work, and then to write the plan that fixes and
redesigns the plugin. The owner deployed the plugin to their laptop, looked at it, and called it
an extremely unusable first pass. Everything below is evidence gathered by the commissioning
session — re-verify every claim against the code and the live machine yourself. Do not trust
this document; verify it.

**Your deliverables are two files, neither committed:**

1. `docs/plans/2026-09-22-kdeconnect-live-failure-audit-report.md` — the audit report.
2. `docs/plans/2026-09-22-kdeconnect-redesign-plan.md` — the fix-and-redesign plan.

The commissioning session reviews and lands both (and deletes this handover per the register's
handover rule). Change no other file.

---

## 1. What happened

The runbook is `docs/plans/2026-09-22-kdeconnect-live-test-round.md`. Tasks T1–T3 (sync, build,
deploy, configure, restart) were executed over ssh; T0 and T4 were dropped because the owner
confirmed the kdeconnect daemon is already installed and the phone already paired. The T5 visual
matrix was then abandoned after the first three observations:

| # | Observation (owner's words, cleaned) | Verdict |
|---|---|---|
| O1 | The bar pill glyph "looks like some kind of lowercase a" — should look like a phone | FAIL |
| O2 | The panel opens only on right-click | FAIL |
| O3 | The panel shows almost no styling, no features, no PNGs — just a "Request pairing" option | FAIL |

Owner's overall verdict: an extremely and largely unusable first pass.

Deployed state at time of failure:

| Artifact | State |
|---|---|
| Shell binary | `~/.local/bin/sysc-shell` rebuilt from sysc-shell `70f495e` (wire minors 4/5/6); old binary backed up as `~/.local/bin/sysc-shell.bak-kdeconnect-test` |
| Plugins | `~/sysc-plugins` @ `3d05d35` (Plan D Tasks 0–4); `make build` refreshed all nine binaries; `~/.config/sysc-shell/plugins/kdeconnect` is a symlink into the repo (dir exposes `manifest.json`, `bin/`, `assets/`) |
| Config | backup `~/.config/sysc-shell/config.json.before-kdeconnect-20260921-144521`; `plugins.enabled` gained `org.sysc.kdeconnect`; bar right gained the pill `{"id":"plugin","plugin":"org.sysc.kdeconnect","entry":"bar","instance":"kdeconnect-1"}` |
| Processes | `sysc-plugin-kdeconnect` pid 241753 and `sysc-plugin-weather` pid 241747 running from the symlinked plugin dirs |
| Daemon | `kdeconnect-cli -l`: `archPC` (reachable, **unpaired**) and `Pixel 8 Pro` (paired and reachable, 192.168.0.191) |
| Journal | after the 14:45:38 restart: fontscan/fontconfig noise only; one repeated error from **before** the restart (see F4) |

## 2. Ground truth the commissioning session established (verify, then judge)

- **F1 — the plugin is probably showing the wrong device.** `resolveSelection`
  (`plugins/kdeconnect/service.go:1093-1101`) keeps the saved choice while paired+reachable, then
  falls back to the **first reachable** device, then the first at all. `archPC` is reachable but
  unpaired; if it precedes the Pixel in the daemon's device order, the plugin auto-selects an
  unpaired device. `PanelTree` renders `unpairedCard` for exactly that case
  (`plugins/kdeconnect/view.go:210`), and `unpairedCard`'s button is literally
  `Text: "Request pairing"` (`view.go:419`) — matching O3 word for word. A preference for
  *paired-and-reachable* over merely-reachable is the obvious fix, but first establish what the
  plugin actually selected (plugin stderr, state keys, or a live DBus read) and what order the
  daemon reports devices in.
- **F2 — the pill glyph is a fontconfig fallback signature.** `BarTree` uses icon `smartphone`
  (`view.go:113-117`). The merged iconfont maps `"smartphone"` to `iconSmartphone`
  (`internal/render/iconfont.go:451` on `70f495e`), which sits at PUA codepoint 0xE031 after the
  codepoint-collision resolution in the kdeconnect-icons merge. The font is `go:embed`ed
  (`iconfont.go:15`), so the deployed binary carries the rebuilt TTF — staleness is ruled out.
  A private-use codepoint rendering as an ordinary letter is the classic signature of the
  renderer failing to select the icon font and fontconfig substituting a text font. Verify:
  does the embedded TTF actually contain a glyph at 0xE031 (inspect it with fontTools), and how
  does the render path choose the icon font for icon nodes versus falling back?
- **F3 — left-click activation is lost somewhere between the bar and the plugin.** The pill
  button carries `Events: [EventActivate]` (`view.go:128-131`) and `main.go` routes the `open`
  node to `CallPanelOpen`. `internal/shell/bar.go` contains no `EventActivate` references, so the
  click→activation routing lives elsewhere (plugin widget input handling, hit-testing, or a
  bar-item activate/context-menu split). Establish where a left-click on a plugin bar pill goes
  and why only right-click opens the panel.
- **F4 — the journal error predates the deploy.** `sysc-shell: closing surface tray-menu: ui:
  child 0 of kind 3 does not fit in 22x28` appears at 14:40:09, before the 14:45:38 restart.
  Kind 3 is `KindButton` (`internal/ui/tree.go:11-14`); the surface is the tray menu
  (`internal/shell/traymenuhost.go`). Classify it as pre-existing noise or a real bug — but do
  not attribute it to kdeconnect on the current evidence.
- **F5 — the mockups were never reached.** O3's "no PNGs" is downstream of F1: the unpaired card
  has no mockup path. The assets are visible through the symlink (`assets/` sits beside
  `bin/`), and `deviceMockup` resolves `<exe dir>/../assets/<kind>.png`. Verify the paired-card
  path would resolve them under the symlinked install (the `os.Executable` → symlink → real path
  chain), so the redesign plan doesn't chase a phantom.
- **F6 — most of the T5 matrix is unobserved, not passed.** Settings round-trip, the
  recent-images grid (including the runbook's recorded risk that `startBrowsing` then
  `mountPoint()` may yield an empty mount point), live `PanelDelta`, and the offline state were
  never exercised because the panel never got past the unpaired card. The audit report must not
  let these be claimed as tested.

One nuance the plan must carry: **the owner has not seen the paired-device UI at all.** The
unpaired card is all that rendered. Separate "broken selection hid everything" from "the paired
UI is itself weak" — the gap analysis and hallmark audit should judge the paired UI from code,
trees, and rendered output, and the plan should sequence fix-selection → re-observe live →
redesign.

## 3. Your commission — three workstreams, then the plan

**Workstream 1 (yours): root-cause the three symptoms.** Confirm or refute F1–F6 with
path:line evidence and live-machine reads. Anything you cannot verify is UNVERIFIED, never
guessed — say what evidence would settle it.

**Workstream 2 (sub-agent, dispatch while you work): gap analysis.** Commission an agent to
compare the plugin as-deployed against the reference: the DMS KDE Connect behavior and the
parity audit report (`docs/plans/2026-09-19-kdeconnect-parity-audit-report.md`, now on main).
Deliverable: a gap table — area, reference behavior, plugin behavior today, severity, and fix
locus (plugin / host / protocol). The owner's three symptoms are the top rows; the paired-device
surface (device card, action row, composers, info grid, recent images) is the body of the table.

**Workstream 3 (sub-agent, dispatch while you work): hallmark audit.** Commission an agent to
run the `hallmark` skill (audit mode) against the plugin's panel UI. Inputs: the node trees in
`plugins/kdeconnect/view.go`, the paint/render tests, and — where feasible — rendered output
(the render harness and paint tests can produce images). Judge visual hierarchy, spacing,
typography, density, and the anti-slop criteria the skill defines. Deliverable: findings with
severity and concrete direction, not vibes.

**Then: the redesign plan.** Write
`docs/plans/2026-09-22-kdeconnect-redesign-plan.md` in the house implementation-plan style
(`superpowers:writing-plans`): exact files, TDD steps, run commands with expected results,
commit boundaries. It must cite the audit report, sequence fix-selection before redesign, and
treat the live laptop as the acceptance environment. Scope is yours to propose from the gap
table — but the three symptoms are blocking and land first.

## 4. Constraints

- Your files are the report and the plan. Nothing else. Do not commit — the commissioning
  session reviews and lands both.
- No bd mutations (the tracker lives in `/home/nomadx/sysc-shell`; sysc-plugins has no `.beads`).
- Do not touch the owner's uncommitted work in the sysc-shell main checkout
  (`.commandcode/taste/*`, `.tmp-thumbcheck/`).
- The laptop is live and is the owner's daily machine. Read-only diagnostics over ssh are fine
  (`journalctl`, `pgrep`, `busctl`, `kdeconnect-cli`, reading files). Do NOT restart services,
  edit config, or deploy anything without the owner's say-so.
- Design docs on sysc-shell `main` are reachable via `git show main:docs/plans/...` — the main
  checkout itself sits on `feat/plugin-material-icons` with the owner's WIP.
- If you commit anything at all (you shouldn't), the commit-msg hook rejects case-insensitive
  substrings `bot`, `agent`, `claude`, `generated` — beware innocent words like "both" and
  "regenerated".

## 5. Report format

1. **Verdict** — root causes for O1/O2/O3, and go/no-go for the redesign plan.
2. **Root-cause table** — per symptom: mechanism, path:line evidence, fix locus, confidence.
3. **F1–F6 dispositions** — confirmed / refuted / evolved, each with evidence.
4. **Gap table** — from workstream 2.
5. **Hallmark findings** — from workstream 3.
6. **Plan handoff notes** — what the redesign plan must respect (sequencing, live acceptance,
   bd alignment for the closes that follow).
