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
