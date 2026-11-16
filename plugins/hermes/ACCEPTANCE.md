# Hermes Agent acceptance

This record contains only results that exist. The Task 11 live matrix was run
in part on 2026-11-17 and found a crash that blocks acceptance (see *Task 11
live run*), so the plugin is **not** claimed to have passed live acceptance.
The final automated gate was run on the last commit of `feat/hermes-merge`;
its raw output is in `evidence/task12/gate.txt`.

## Task 11 live run (2026-11-17, 08:14 to 08:58 AEDT)

**Result: not accepted.** A crash in the chat panel (D1 below) blocks
acceptance. Several rows could not be run once the plugin had crashed.

**Where it ran:**

- archThink, in a separate nested session: `niri 26.04` in a window,
  `dbus-run-session`, with a temporary `HOME`, `XDG_CONFIG_HOME`,
  `XDG_CACHE_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME` and `XDG_RUNTIME_DIR`.
- The shell was the installed `sysc-shell` binary (module
  `v0.3.1-0.20261116032720-b50262d5227d`). The plugin was built from
  `1b99cac` into the temporary config only, with `polkit_agent` off.
- Hermes `835a913` (0.20.6) with a throwaway `HERMES_HOME` holding only a
  stub config.
- The model was a scripted OpenAI-compatible stub on `127.0.0.1:18877`. No
  provider credentials were copied, and the Ollama cloud models had no
  credit.
- `hermes gateway start` and `stop` went through a shim that runs a
  foreground `hermes gateway run` in the throwaway home, so no systemd unit
  was created.
- The bundled bridge ran on `127.0.0.1:19777`. Local mode fixes that port;
  nothing else uses it on archThink.
- Input came from a `zwlr_virtual_pointer_v1` client and `wtype`, both
  against the nested display only.

The daily session (`sysc-shell` pid 1092015, greetd, sysc-lock and the
compositor) was not touched. Evidence: the Task 11 evidence folder
(`shots/00`–`38`, `logs/`, `ps-before.txt`, `ps-after.txt`).

### Matrix

| Check | Result | Evidence |
|---|---|---|
| Local, no bridge | PASS: spawned one bridge, bar online, token read beside the state file | `shots/02-tooltip.png`, `logs/hermes-shim.log` |
| Local, Noctalia bridge already running | NOT RUN: the plugin was stuck after D1's restart (D2) and never adopted the hand-started bridge | `shots/38-manual-bridge.png` |
| Local, gateway stopped | PASS: `gateway start` called exactly once | `logs/hermes-shim.log` |
| Local, state-file updates | PASS (observed, not timed): the card and status followed the state file at once in Local mode | `shots/10-approval-card.png` |
| Local → client-only, client-only → local | NOT RUN (blocked by D1 and D2) | |
| Collision warning | NOT RUN | |
| Client-only: tunnel down, LAN host, token file, wrong or missing token | NOT RUN (blocked by D1 and D2; the VPS has no bridge, see Phase B) | |
| Bar | PARTIAL: Online ✓, Working, Needs You with `!`; tooltip `Hermes: Online · t11-stub` | `shots/02`, `10`, `32` |
| Left click | FAIL: opens the chat panel, not the summary; a second click closes it | `shots/03`, `04` (D3) |
| Right click | FAIL: no menu opens | `shots/05-rightclick.png` (D3) |
| Hide when idle, Control Center tile, `plugin.action` | NOT RUN | |
| Send prompt | PASS: user message shown at once, reply rendered | `shots/07b-retry.png` |
| Interrupt, Interrupt during one-shot, One-shot | NOT RUN | |
| Approval Allow | PASS: ran once; the next identical call asked again | `shots/11`, `12`, `approvals-ui.txt` |
| Approval Allow Session | PASS: ran, and the next call of the same pattern ran without asking. The queued-approvals part was NOT RUN | `shots/16`, `17`, `approvals-ui.txt` |
| Approval Deny | PASS: `BLOCKED: Command denied by user` | `shots/14`, `15` |
| Stale approval click | PARTIAL: the stale Allow was dropped and the tool did not run, but no message appeared (D4) | `stale-click.txt`, `shots/13` |
| Mismatched `request_id` | PASS: 409 `approval_mismatch`, request still pending | `409-mismatch.txt` |
| Queued second approval behind the shown one | NOT RUN: one approval per session could be queued with this setup | |
| `/model` `gpt 4 --global` | PASS: 400 `invalid_model`, config hash unchanged | `model-invalid.txt` |
| New session/Reset, Sessions/Resume, Resume latest, Model Apply/Persist, Start/Stop gateway, Pinned, Settings, Launcher | NOT RUN (blocked by D1 and D2) | |
| Mid-turn drop | FAIL: D1 crashed the plugin mid-reply and took the owned bridge with it; recovery did not happen (D2). No token value appears in any captured log or evidence file | `crash-repro2.txt` |
| Small output | NOT RUN | |
| Scroll | see the scroll spike below | |
| Approval card content | FAIL: the card shows only the title and buttons, not the command (D5) | `shots/10-approval-card.png` |

### Defects found (not fixed in this run)

**D1 (P1, blocks acceptance): panic in `wrapLines`.**

- Where: `plugins/hermes/panel.go:686-693`, reached through `bubble`
  (`panel.go:615`), `transcript` (`:566`), `PanelTree` (`:275`) and
  `main.viewTree` (`cmd/sysc-plugin-hermes/main.go:801`).
- Cause: when the text wraps to more than `maxLines` (12) lines, `cut :=
  len(last)` is kept whenever `len(last) <= maxBytes-len(" …")`, so the
  loop then reads `last[cut]` one past the end of the string.
- Minimal repro: `wrapLines("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm", 92, 12)`.
- Live: a 60-line reply in an open chat panel gave `index out of range [56]
  with length 56`. The 12th line, `Line 12 of the long streamed reply for
  the scroll spike.`, is 56 bytes.
- Every render of that transcript crashes again.

**D2 (P2): no recovery after the host restarts the plugin.**

- After the host restarted the plugin following D1, the bar and the panel
  stayed `Connecting…` and no bridge was spawned (observed for more than
  2 minutes, twice).
- The goroutine dump in `plugin-goroutines-stuck.txt` shows the event loop
  idle in `select` and the reader in `Recv`.
- The cause is not confirmed. The host's consent state may be involved:
  after a manual install the host showed "enabled before approvals were
  recorded", and a stderr wrapper produced `state.get … withheld because the
  plugin changed`. Approving in Settings → Plugins restarted it cleanly.

**D3 (P2): the bar never opens the summary or the menu.**

- `cmd/sysc-plugin-hermes/main.go:659-664` opens the chat panel for the
  bar's `open` activate.
- `input` (`main.go:631-646`) ignores `EventPointer`.
- Nothing calls `openPanelFrom(…, "summary")` or `(…, "menu")`, so neither
  panel is reachable, which contradicts the README.

**D4 (P2): action errors are never shown.**

- `View.ActionError` (`plugins/hermes/service.go:298`) is set but no tree
  reads it, so `The approval changed; check the card again` and every
  action error are invisible.

**D5 (P2): the approval card hides what is being approved.**

- `approvalCard` (`panel.go:496-537`) shows only `ApprovalTool` and
  `Summary.ApprovalMessage`.
- Hermes `835a913` sends `command` and `description` inside
  `approval.request`, with empty `message` and `tool_name`, so the card names
  nothing.

**D6 (docs):** Hermes `835a913` offers `always` in `choices`
(`allow_permanent: true`). The README says Hermes offers no "always" choice.

### Scroll spike

Not a four-option comparison. Only what ships was observed:

- The transcript list starts at its top, so the newest messages sit below the
  fold (`shots/18`).
- The mouse wheel scrolls it (`shots/19`).
- During a streamed reply the plugin crashed on D1 before the scroll
  position could be judged.

Still open.

### b200f5b approval path (read-only source check on archPC)

Confirmed by reading the source:

- In `tui_gateway/server.py`, `_emit_approval_request` sends an `approval`
  server→client request through `server_requests.send_async`, not an
  `approval.request` event.
- `write_json` routes it to the session's transport, which for the bundled
  bridge is `StateTransport`.
- `HermesState.apply_event` acts only on `params.type == "approval.request"`
  (`scripts/hermes_bridge.py:566-567`), so the request is dropped and no card
  appears.
- The bridge's transport is not a `WSTransport`, so
  `_session_client_answers_requests` counts the session as answerable and
  Hermes does not withdraw the request. The tool waits for
  `approvals.timeout`.
- `approval.respond` still resolves by `request_id` if the bridge learns the
  ID, for example by handling the `approval` request frame or polling
  `_pending_approval_request_payload`.

This needs a bridge change, which is an owner decision. Evidence:
`archpc-b200f5b-*.txt`.

### Phase B (VPS, client-only)

Skipped. The VPS (`hermes-vps`, Hermes `f0d94882e8`) runs only
`hermes-gateway` and `hermes-dashboard`. It has no bridge listening, no bridge
script and no bridge token (`vps-readonly-inventory.txt`). The bundled bridge
has exactly one token, `bridge.token`, with no way to issue or revoke a
second one.

Phase B needs the owner to decide whether to:

- install the bundled bridge on the VPS and run it, which starts a new
  process against the lead's Hermes home;
- use its single token, revoked by stopping the bridge and deleting the
  token file;
- and settle the b200f5b-style approval path first, because the VPS build
  is newer than `835a913`.

## Automated gates

| Gate | Result |
|---|---|
| `gofmt -l .` | clean |
| `go vet ./...` | pass |
| `go test -race -count=1 ./...` | pass |
| `make validate` | pass |
| `make thumbnails` (throwaway `git archive` copy) | no tracked-file changes |
| `go run ./tools/catalog validate` | not run: no `hermes` row in `catalog.json` |
| `git diff --exit-code -- go.mod go.sum catalog.json catalog-meta.json` | clean against base `e97f517` |

## QA verdicts

These are the reviews that were actually completed. Gate 3 is the live
environment gate; it is deferred everywhere, which is why the Task 11 matrix is
still outstanding.

| Commit | Task | Report | Verdict |
|---|---|---|---|
| `937ac1a` | bundled bridge: approval identity and model-name guard | `qa-3a.md` | Gate 1 PASS, Gate 2 PASS, Gate 3 DEFERRED |
| `7ffec52` | settings panel | `qa-8.md` | Gate 1 PASS, Gate 2 PASS with a process caveat, Gate 3 DEFERRED |
| `7c9335f` | approval choices | `qa-6.md` | Gate 1 PASS with defects, Gate 2 PASS, Gate 3 DEFERRED; the README race defect was fixed in `38af553` |
| `38af553` | stock-bridge approval race documented | `qa-fix6.md` | Gate 1 PASS, Gate 2 PASS, Gate 3 DEFERRED |
| `f9031c6`, `88439ae` | pinned side panel and loopback host test | `qa-9.md` | Gate 1 PASS, Gate 2 PASS, Gate 3 DEFERRED |
| `6b59d12` | bar, summary, panel and settings helpers reconciled | `qa-ui-integrate.md` | Gate 1 PASS, Gate 2 PASS |
| `86aaac5`..`0e2efd7` | runtime and UI merge | `qa-merge.md` | Gate 1 PASS, Gate 2 PASS, Gate 3 live DEFERRED |
| `8f1f697`, `119b203` | approval click routing, helper de-duplication | `qa-fixmerge.md` | PASS (hygiene, scope, gate) |
| `e97f517` | `/hermes` launcher rows | `qa-10.md` | accepted, no P1/P2; Gate 2 pass; four P3 follow-ups |
| `9661608`, `20d4104`, `bbe66f3` | Task 12 test hardening | not independently reviewed at the time of writing | mutations caught as recorded below |

`qa-10.md` recorded two surviving mutations, both closed here:

| Follow-up | Test | Mutation result |
|---|---|---|
| `runArgv` could become `sh -c` unnoticed | `TestRunArgvExecutesArgvWithoutAShell` re-executes the test binary and records its argv | `exec.Command("sh", "-c", …)` caught: the marker file appears and the spaced argument changes |
| 1 MiB boundary untested on both sides | `TestLauncherAskAcceptsExactlyOneMegabyte` (`write` and `read` subtests) | `len(text) > max` → `>=` caught; `Size() > max` → `>=` caught |
| empty-query fallback untested | `TestLauncherSelectAskFallsBackToTheRowComment` | removing the fallback caught: exit 2, `type a prompt after /hermes` |
| 2 s deadline flaked under `-race` | `TestSettingsChangedConnectsAndRepublishes` now uses a 10 s test host deadline | the Online snapshot and the active status are read from one stream, because either could arrive first and a separate wait would consume the other's message; 10 consecutive `-race` runs pass |

## Scroll spike

**Status: not run as a comparison. No option has been chosen.** The 2026-11-17
observation is under *Task 11 live run*.

The plan asks for four transcript strategies to be compared in a real
sysc-shell session while a reply streams:

1. Full snapshot on every change.
2. A patch to the streaming message's text node only.
3. Fixed message slots whose text is patched in place.
4. One text node for the transcript, plus `view.focus` to the newest line.

This spike was not run for Task 7 (2026-11-11 UTC):

- The only sysc-shell on the development machine is the owner's live
  desktop session. Running the spike would mean installing a development
  build of this plugin into that live configuration and running a Hermes
  bridge behind it.
- Each option has to be judged by scrolling the transcript by hand while a
  reply streams. Rendered previews (`sysc-panel-preview`) are still images
  and cannot show scroll behaviour.

Static reading of the pinned host (sysc-shell v0.2.5). This is not a
measured result:

- `internal/shell/pluginhost.go` `onMessage`: a `view.snapshot` and a
  `view.patch` both produce a newly converted root, and `applyResult`
  replaces `v.Root`. Options 1 to 3 therefore all rebuild the panel tree.
- `internal/shell/panelhost.go` `rebuildPanel` carries scroll offsets over
  only for the host's own settings lists (`settingsRailKey`,
  `settingsBodyKey`, `settingsSearchBodyKey`). A plugin list's
  `ScrollOffset` does not appear to survive a rebuild. If that holds, the
  transcript list would jump back to the top on every update, whatever the
  option.
- `view.focus` (`focusPanelView`) matches only focusable nodes, and
  `setFocus` moves the roving focus index without scrolling. Text nodes
  are not focusable, so option 4 looks unable to bring the newest message
  into view by itself.

What ships: the transcript is a `KindList` with key `transcript` and a stable
`msg:<id>` key per message. It shows the newest 24 messages, with the caption
"Showing the last 24 messages" when older ones exist. The list starts at its
top, so in a long conversation the newest message can sit below the fold. The
plan's fallback (a paged transcript of the last 12 messages with
Older/Newer) is not built. That choice should follow a live run of this
spike, not this static reading.

To run it: install a development build into a throwaway sysc-shell
session, point it at a Hermes bridge, stream a reply of 40+ lines with each
option, and record here:

- whether the scroll position survives each update
- whether the newest line can be reached
- what the frame cost is

## Approval check

**Status: partial (2026-11-17).** Deny, Allow and Allow Session were exercised
through the bundled bridge on Hermes `835a913`; see *Task 11 live run*. The
`b200f5b` delivery path and a stock bridge were not exercised live.

Before that run, no approval had been exercised against a live Hermes gateway. The automated tests pin the plugin's request signature, the
`request_id` payload and the 409 handling, but they use a fake bridge.

The open gap is on the bridge side: on Hermes `b200f5b`, approvals arrive as
an `approval` server request (`server_requests.send_async`) rather than an
`approval.request` event, and the bundled bridge still handles only the event.
That path is unverified. A live check must drive one real approval through both
the bundled bridge and a stock bridge, confirm that Deny, Allow and Allow
Session resolve the request on screen, and confirm that a deliberately stale
click is refused with `The approval changed; check the card again`.

## Parity matrix

**Status: partial, not accepted.** See *Task 11 live run*. No side-by-side
comparison against Noctalia was produced.
The design's surface inventory and host-gap list describe what should be
compared; they are not a substitute for the matrix.

## Bundled bridge

`scripts/hermes_bridge.py` is the local v5 port's bridge, including the
unpushed SessionDB `/sessions` and resume-history fixes.

| | Path | SHA-256 |
|---|---|---|
| Reference v5 source | archPC `~/projects/noctalia-hermes-agent/v5/hermes-agent/scripts/hermes_bridge.py` | `892e27f75ec9f771d2a4dc7178ca5afcf19b9198beed0c558e2d25064cc15178` |
| Bundled before Task 3a (commit `a8384bd`) | `plugins/hermes/scripts/hermes_bridge.py` | `0be2bc3cffbc01a8573fa573673642e58a0fa3934b7de685af914c5606ae8e99` |
| Bundled now | `plugins/hermes/scripts/hermes_bridge.py` | `c658df456138eb6cfd6113c7a94ace79683177e30ebdc2c49d8447a4c5fac183` |

The pre-Task-3a bundle differed from the reference source only by a three-line
comment after the shebang pointing at `ATTRIBUTION.md`. Commit `937ac1a` then
made the two authorized changes: `request_id` approval resolution (with 409
`approval_mismatch`) and the `/model` `invalid_model` guard. Those are the only
local changes; the reference hash was recorded by the Task 3a audit and was not
re-read for this record.

## Approval resolver (Task 3a)

Read from the deployed Hermes installs, read-only:

| Host | Hermes checkout | `approval.respond` |
|---|---|---|
| archThink | `835a913` (2026-08-29) | `tui_gateway/methods_prompt.py` |
| archPC | `b200f5b` (2026-11-03) | `tui_gateway/methods_prompt.py` |

Both pass `params["request_id"]` to `tools.approval.resolve_gateway_approval(
session_key, choice, resolve_all=…, reason=None, request_id=None)`. With a
`request_id`, it resolves only the queued entry with that ID and returns 0
when none matches; without one, it resolves the oldest entry (FIFO) or, with
`all`, every entry. Each queue entry gets `request_id` (`uuid4().hex`) when it
is created, and the approval payload sent to clients is a copy of the entry's
data, so `approval.request` carries it.

**Source-read result: the deployed resolver can resolve a request by ID.** The
bundled bridge publishes it as `approval.request_id` and forwards `request_id`
when `resolve_gateway_approval` takes one (checked with `inspect.signature`).
For an older resolver without it, the bridge resolves only when the given ID is
the one it shows, using the stock oldest-first call. The bridge cannot read the
Hermes queue itself, so that fallback relies on the shown request being the
oldest one.

This is a source read, not a live test. As noted under *Approval check*, the
`b200f5b` server-request delivery path is still open and Task 11 did not run.
