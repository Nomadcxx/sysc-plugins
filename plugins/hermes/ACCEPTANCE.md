# Hermes Agent acceptance

This record contains only results that exist. The live acceptance matrix in
Task 11 has **not** been run, so the plugin is not claimed to have passed live
acceptance. The final automated gate was run on the last commit of
`feat/hermes-merge`; its raw output is in `evidence/task12/gate.txt`.

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

**Status: not run. No option has been chosen.**

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

**Status: NOT RUN (Task 11 live matrix).**

No approval was exercised against a live Hermes gateway, in either delivery
path. The automated tests pin the plugin's request signature, the
`request_id` payload and the 409 handling, but they use a fake bridge.

The open gap is on the bridge side: on Hermes `b200f5b`, approvals arrive as
an `approval` server request (`server_requests.send_async`) rather than an
`approval.request` event, and the bundled bridge still handles only the event.
That path is unverified. A live check must drive one real approval through both
the bundled bridge and a stock bridge, confirm that Deny, Allow and Allow
Session resolve the request on screen, and confirm that a deliberately stale
click is refused with `The approval changed; check the card again`.

## Parity matrix

**Status: NOT RUN.** No live parity comparison against Noctalia was produced.
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
