# Hermes Agent acceptance

Results recorded here are summarized in the pull request description.

## Scroll spike

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
