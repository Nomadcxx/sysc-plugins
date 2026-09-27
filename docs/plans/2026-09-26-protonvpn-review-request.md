# ProtonVPN plugin — review request

Asking: a fresh-eyes review of branch `feat/protonvpn`
(worktree `/home/nomadx/sysc-plugins/.worktrees/feat/protonvpn`), range
`6c467a1..HEAD`, before merge to main. Per-session spec/QA gates were
skipped by user instruction; this document replaces them.

## What was built

Plugin `org.sysc.protonvpn`: bar + tooltip + 460×580 panel (connections /
protection / account tabs) driving the official `protonvpn` CLI — status
polling, connect/disconnect/quick-connect, country search/expand, kill
switch, NetShield, port forwarding (NAT-PMP + copy port), split-tunnel
app picker, sign-in terminal handoff, notifications, persisted tab state.

Commit log is one commit per plan task with the plan's messages; review-fix
commits are prefixed `fix(protonvpn):`/`test(protonvpn):`. The final
`docs(protonvpn): handover and acceptance record` commit contains the
task-by-task landing table and verification transcript.

## How to verify

```sh
cd /home/nomadx/sysc-plugins/.worktrees/feat/protonvpn
go test -count=1 ./plugins/protonvpn/ ./cmd/sysc-plugin-protonvpn/   # 83 tests
GOTMPDIR=/home/nomadx/.cache/go-tmp make build && make validate      # 13/13
go test -count=1 ./tests/integration/   # expect ONLY the pre-existing
                                        # TestPluginCalendarGateAllViews failure
```

## Files worth the most scrutiny

| File | Why |
|---|---|
| `plugins/protonvpn/cli.go` | Output parsers against real CLI formats; fixtures are verified official formats, **not live captures** — the CLI is not installed here. |
| `plugins/protonvpn/state.go` | 20s transition deadline, stale Err/IP clearing, traffic counter-reset + interface-vanish guards. |
| `plugins/protonvpn/splittunnel.go` | Unknown-key-preserving JSON round trip; refuses to clobber malformed files. |
| `plugins/protonvpn/natpmp.go` | RFC 6886 wire format (golden bytes), retry/fail-fast split. |
| `plugins/protonvpn/apps.go` | Exec-line parsing and the exclusion lists (Noctalia port). |
| `cmd/sysc-plugin-protonvpn/main.go` | Event loop: single-goroutine `Machine` access via `async` channel; every input handler path; polling cadences. |
| `plugins/protonvpn/panel.go` `tabBody` | Height budget math (lint cannot see column overflow). |

## Known risks / open items

- Live CLI parity unproven (formats could drift). First live run should
  re-check `status`, `config list`, and connect stdout IP parsing.
- Split tunneling writes are inert until the CLI supports the feature.
- `protection.go` renders the split-tunnel editor even when kill switch
  blocks it (required by the plan's test).
- `TestPluginMiniDockerGate` flakes under full-suite load; passes in
  isolation — timing, unrelated to this plugin.

## Deviations already disclosed

Native `clipboard.write` instead of `wl-copy` shellout (probe still gates
the button); snapshot-on-change instead of keyed patches;
`AccountState.Settings` map added (spec gap); sign-in input width 320 to
fit the embedded card; harness empties `PATH` so the fake CLI uses shell
builtins only; tests run app executables inside the package dir because
`/tmp` is `nosuid`.
