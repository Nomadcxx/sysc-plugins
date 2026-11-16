# Hermes Agent acceptance

Results recorded here are summarized in the pull request description.

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

What ships in Task 7: the transcript is a `KindList` with key `transcript`
and a stable `msg:<id>` key per message. It shows the newest 24 messages,
with the caption "Showing the last 24 messages" when older ones exist. The
list starts at its top, so in a long conversation the newest message can
sit below the fold. The plan's fallback (a paged transcript of the last 12
messages with Older/Newer) is not built. That choice should follow a live
run of this spike, not this static reading.

To run it: install a development build into a throwaway sysc-shell
session, point it at a Hermes bridge, stream a reply of 40+ lines with each
option, and record here:

- whether the scroll position survives each update
- whether the newest line can be reached
- what the frame cost is

## Approval check

## Parity matrix

## Bundled bridge

`scripts/hermes_bridge.py` is the local v5 port's bridge, including the
unpushed SessionDB `/sessions` and resume-history fixes.

| | Path | SHA-256 |
|---|---|---|
| Source | archPC `~/projects/noctalia-hermes-agent/v5/hermes-agent/scripts/hermes_bridge.py` | `892e27f75ec9f771d2a4dc7178ca5afcf19b9198beed0c558e2d25064cc15178` |
| Bundled | `plugins/hermes/scripts/hermes_bridge.py` | `0be2bc3cffbc01a8573fa573673642e58a0fa3934b7de685af914c5606ae8e99` |

The only difference is a three-line comment after the shebang pointing at
`ATTRIBUTION.md`; every other byte matches the source. The authorized local
changes (Task 3a) land in a later commit.

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

**Result: the deployed resolver can resolve a request by ID.** The bundled
bridge publishes it as `approval.request_id` and forwards `request_id` when
`resolve_gateway_approval` takes one (checked with `inspect.signature`). For
an older resolver without it, the bridge resolves only when the given ID is
the one it shows, using the stock oldest-first call. The bridge cannot read
the Hermes queue itself, so that fallback relies on the shown request being
the oldest one.

On `b200f5b`, Hermes sends approvals to clients as an `approval` server
request (`server_requests.send_async`) rather than an `approval.request`
event. The bundled bridge still handles only the event. That is unchanged
here and is noted for the Task 6 approval check.
