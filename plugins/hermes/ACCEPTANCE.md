# Hermes Agent acceptance

This record contains only results that exist. The Task 11 live matrix was run
on 2026-11-17, first in part (a crash, D1, blocked it) and then again on
`bc810b9` after the fixes; D1 to D8 from those runs are fixed. The rows those
runs left open, and the client-only matrix, were run live on 2026-11-18 and
2026-11-19 against a throwaway nested sysc-shell, a throwaway Hermes home and
a second throwaway bridge standing in for the VPS. Every row passes now, and
the defects those runs found are fixed (see *Live runs 2026-11-18 and
2026-11-19*). Not run: client-only mode against the VPS, an approval through
a stock bridge, and a side-by-side comparison with Noctalia. The automated
gates pass on `9f2f8c7` (see *Automated gates*). Sections below that one are
the earlier runs, kept as they were recorded.

## Live runs 2026-11-18 and 2026-11-19

**Where they ran.** archThink, in a nested `niri` with its own `HOME`, XDG
directories and `HERMES_HOME` under `/tmp/hv1`, a scripted stub model on
`127.0.0.1:18881` and Hermes `835a913`; input from a virtual pointer and
`wtype` against the nested display only. Shell: v1 the installed shell at
`3c0fe385`, v2 and v3 `bc9dc40f`. Plugin: v1 `8dbcc45` and `9c01c7e`, v2
`cf89402`, v3 `6b6258a` through `9f2f8c7`, swapped in as each fix landed.
Client-only ran against a second bundled bridge on `19778` with its own Hermes
home, state directory and 0600 token, moved to the LAN address `192.168.0.64`
for the LAN row. The b200f5b path ran on archPC (Hermes `b200f5bc60e`) over
HTTP with a throwaway `HERMES_HOME`. Evidence: archThink
`~/hermes-oc/evidence/v1`, `v2` and `v3/shots` (numbers below are v3 unless
marked), harness scripts in `~/hermes-oc/v3-live`.

| Check | Result |
|---|---|
| b200f5b approval path | PASS (archPC, 2026-11-18): the `approval` server request shows a card with `request_id` and the once/session/always/deny choices; a wrong `request_id` gets 409 and the card stays; deny blocks the command, allow-once runs it; Interrupt with a card pending clears it. Re-checked with `e8217e9` |
| Approval choices (R4) | PASS (v1 shot 07): Deny, Allow and Allow Session for once, session, always and deny |
| Stale approval click (O1) | PASS (v2 shots 07, 08): a click on a card that had been replaced shows `The approval changed; check the card again` and nothing runs |
| Transcript paging | PASS (v1 shots 01 to 05): follows the tail, Older pages back, a paged-back view stays put while a reply streams, Latest returns |
| Pinned composer | PASS (v1 shot 08): the whole composer shows under the host's sticky chrome |
| Host respawn of the plugin (D2) | PASS (v1): settings restored and the bridge respawned |
| One-shot (R11) | PASS (02 to 05): the reply is in the transcript within 9 s; the poll is 30 s |
| Interrupt during one-shot, one-shot cancel | PASS after `03177d9` (09, 10): the bridge reports no error and the one-shot finishes on the bridge (documented as client-side cancel) |
| Refused model Apply (R8) | PASS (12 to 14): `Hermes: Unknown provider 'nosuch'…` shows, the header keeps `custom / v1-stub` across a poll, `config.yaml` unchanged |
| Model Apply and Persist | PASS (31 to 34): Persist writes `config.yaml`; after a bridge kill the respawned bridge and a new session run the persisted model |
| Auto-start gateway toggle (R7) | PASS (24 to 27): off from plugin start, nothing starts over 45 s; on, the gateway starts 7 s later with the plugin and bridge pids unchanged |
| Gateway killed outside a planned stop | PASS after `bb868da`: `/state` and `/detect` say stopped; the next plugin connection starts it |
| New session and Reset | PASS (29, 30): Reset replaces the session and clears the transcript |
| Sessions and Resume, Resume latest | PASS after `bb868da`, `879e81f`, `54258a6` (57 to 60): resuming keeps the session live, the transcript grows, and the model receives the whole conversation, also after a bridge restart |
| Launcher | PASS (47 to 50): the four `/hermes` rows; Ask writes the hand-off file 0600 in a 0700 directory, it is deleted on read, the one-shot runs and the chat opens; Start session creates and opens one |
| Control Center tile | PASS (40, 41): opens the chat. The host labels it `Guard On` (host gap) |
| Open action with the panel already open | PASS after `a0304d1` (42): the chat stays open |
| Small output | PASS after `954f264` (53, 55, 56): at output scale 2 (476x516) the chat panel is accepted idle and running, and Settings fits |
| Local → client-only | PASS (64): the owned bridge exits 1 s after the switch, 19777 is free, the gateway is not stopped, and the plugin is online through the token file |
| Client-only, tunnel down | PASS (65 to 67): `Last error: Bridge offline` within one poll; back within a poll |
| Wrong or missing token | PASS (71, 73): `Wrong token`, and `No bridge token: paste it in Settings or set a token file`; the pasted field is masked and no token appears in the shell log or bridge state |
| Client-only, LAN host | PASS (74 to 76): works; the cleartext warning shows in Settings and the tooltip |
| Client-only → local | PASS (78): a local bridge is spawned in 2 s with the local token, no 403 |
| Collision | PASS after `9f2f8c7` (83, 84): the warning shows in Settings and the tooltip; the adopted bridge is left running |

Defects found and fixed in these runs:

| Defect | Commit |
|---|---|
| Local mode stopped respawning after three crashes in a session | `9c01c7e` |
| The bridge could not import Hermes on `b200f5b` (`hermes_bootstrap`), and a cold start outlasted the start probe | `e8217e9`, `cf89402` |
| A model Hermes refused was shown as live, and the refusal was not shown | `c84755f`, `aa9221b` |
| The Local-mode poll and Refresh did not re-read `/state` | `6b6258a` |
| Cancelling a one-shot showed `Hermes: session not found` | `03177d9` |
| Open, settings and launcher actions closed an already-open panel (the host's `panel.open` toggles) | `a0304d1` |
| The host refused the composer row at a small output | `954f264` |
| Resume did not continue the session | `bb868da`, `879e81f`, `54258a6` |
| A killed gateway read as running, so auto-start never fired | `bb868da` |
| Settings showed the client-only port in Local mode | `859778e` |
| The collision warning was never rendered | `9f2f8c7` |

Two approvals granted with no scripted input in the v1 run were the owner
clicking the visible nested window, not a defect.

Seen and left to the host: the 953-px nested bar with four plugins on the
right overflows when the Hermes pill carries a label (Working, Error), and the
shell paints the overflow at x=0; a hand edit of `config.json` does not reach
a running plugin; the Settings panel loses its scroll position on updates.



## Task 11 local re-run (2026-11-17, 10:08 to 10:54 AEDT)

*Kept as recorded. Every row marked NOT RUN, PARTIAL or FAIL here was run
again later; see* Live runs 2026-11-18 and 2026-11-19.

**Result: local matrix not accepted.** D1 and D3 to D8 are fixed and hold up
live (D7 and D8 were checked on 2026-11-17, 11:50 to 11:54 AEDT). One problem
remained in this run: after the host restarts the plugin on its own, it
stays on `Connecting…` (D2). D2 was later fixed plugin-side; see *Defect
status*. Client-only rows wait on the owner.

**b200f5b approval path:** Hermes `b200f5b` on archPC sends approvals as a
server request that the bundled bridge used to drop (see *b200f5b approval
path* below). Fixed in `25768b4`; checked live on 2026-11-18.

**Pending the owner:** every client-only row, including the VPS (see
*Phase B*).

**Where it ran:** the same nested setup as the first run (archThink, nested
`niri` with `dbus-run-session`, temporary `HOME`, XDG directories and
`HERMES_HOME`, stub model on `127.0.0.1:18877`, bundled bridge on
`127.0.0.1:19777`). The shell was the installed `sysc-shell` (module
`v0.3.1-0.20261116032720-b50262d5227d`) and Hermes was `835a913`. The plugin
was built from `bc810b9`. The only change in that build was extra stderr logging
(`evidence/task11b/ht11-final/debug-build.diff`). Evidence paths below are
relative to `evidence/task11b/`. The setup was torn down at 10:54 AEDT and
the daily session was not touched (see *Teardown*).

### Matrix (re-run)

| Check | Result | Evidence |
|---|---|---|
| Local, no bridge | PASS: after a nested shell restart the plugin spawned the bridge as its own child, and after a `kill -9` it spawned a new one within 5 s. The token came from `~/.cache/sysc-hermes/bridge.token` | `hide-idle.txt`, `midturn-drop.txt`, `ht11-final/logs/plugin-stderr.log` |
| Local, Noctalia bridge already running | PASS: a hand-started bridge with its state in `~/.cache/noctalia-hermes/` was adopted, not spawned | `adopt.txt`, `shots/01-adopted-bar.png` |
| Local, gateway stopped | PASS: `gateway start` ran once at start. No second start after two shell restarts and a bridge kill | `gateway.txt`, `ht11-final/logs/hermes-shim.log` |
| Local, state-file updates | PASS: a prompt posted straight to the bridge was in the open panel by the next shot, about 7 s later. The idle HTTP poll is 30 s | `statefile.txt`, `shots/36-statefile-2s.png` |
| Local → client-only | NOT RUN: client-only rows are pending the owner | |
| Client-only → local | NOT RUN: pending the owner | |
| Collision | NOT RUN: needs a tunnel on 19777 (client-only), pending the owner | |
| Client-only, tunnel down | NOT RUN: pending the owner | |
| Client-only, LAN host | NOT RUN: pending the owner. Not exposed on the LAN from the throwaway setup | |
| Wrong or missing token | PARTIAL: in Local mode with the token file replaced, the bridge answered 403 and Settings showed `Last error: Wrong token`. On `bc810b9` the bar tooltip said only `Hermes: Error · t11-stub` (D8); on `6a30c4b` the tooltip and the summary say `Wrong token` (see *D7 and D8 live check*). With the file missing the bar showed Error, but the Settings text was not captured. No token value appears in any log or evidence file. It recovered after the file was restored | `wrong-token.txt`, `shots/27`–`35` |
| Bar | PASS: Online, Needs You with `!`, Error and hidden-when-idle all render. Tooltip `Hermes: Online · t11-stub` | `shots/02b`, `14`, `27`, `25` |
| Left click | PASS (D3 fixed): the summary opens with Model, Gateway and Activity rows | `shots/07b-summary.png` |
| Right click | PASS (D3 fixed): menu with Open Hermes, New session, Interrupt, Refresh and Hermes Settings. Start gateway was hidden because the gateway was running (`menu.go:26-36`) | `shots/08b-right-menu.png` |
| Hide when idle | PARTIAL: the pill was hidden when idle, and `plugin.action open` opened the panel while hidden. The Control Center tile was not clicked. A live `config.json` edit did not reach the plugin; the setting applied after a shell restart | `hide-idle.txt`, `shots/24`–`26` |
| Send prompt | PASS | `shots/36-statefile-2s.png`, `sessions.txt` |
| Interrupt | PASS: the stub saw the stream closed, and the status returned to idle | `interrupt.txt` |
| Interrupt during one-shot | PARTIAL: the interrupt was accepted and the one-shot finished with its reply recorded. The bridge then kept `[Errno 32] Broken pipe` as its error and Hermes status `error` until the next session | `oneshot-interrupt.txt`, `sessions.txt` |
| Approval Allow | PASS: Allow on the pinned card ran the command once, and the next call asked again | `pinned-approval.txt`, `shots/12*`, `16` |
| Approval Allow Session | PASS in the first run (`910ed68`). Not re-run | first run `approvals-ui.txt` |
| Approval Deny | PASS: the denied `rm` did not run | `pinned-approval.txt`, `shots/17` |
| Stale approval click | PARTIAL: the request denied through the API never ran. In both tries, though, the card had re-rendered before the click landed, so the click resolved the next card (O1). The stale-drop message is covered by unit tests only | `stale-click.txt`, `stale-click2.txt`, `shots/13`, `15` |
| Second approval queued (bundled bridge) | PASS: a wrong `request_id` got 409 `approval_mismatch` and resolved nothing. The shown request was then resolved from the card | `approval-409.txt` |
| `/model` `gpt 4 --global` | PASS: 400 `invalid_model`, config unchanged | `model.txt` |
| New session / Reset | PARTIAL: `new-session` started a new session. Reset was not run | `sessions.txt` |
| Sessions / Resume | PASS: the list loaded 5 sessions, resume loaded the transcript, and sending worked | `sessions.txt`, `shots/22b-sessions.png` |
| Resume latest | PARTIAL: the service action resumed the newest stored session. The launcher route was not run | `sessions.txt` |
| Model Apply / Persist | PARTIAL: Persist wrote `default: t11-stub-b` to `config.yaml`, and it was reverted after. Hermes refused Apply with 5001 `Unknown provider 'custom'`, which comes from the stub provider. Whether Persist survives a bridge restart was not checked | `model.txt` |
| Start / Stop gateway | PASS for Local: stop through the bridge, then start through the service action. The client-only half is NOT RUN | `gateway.txt` |
| One-shot | PASS: `hermes -z` completed. Cancel was not run on its own | `gateway.txt`, `oneshot-interrupt.txt` |
| Pinned | PASS (D7 fixed): opens, and approvals work there. On `6a30c4b` it is restored after a shell restart. A plugin-only restart was not checked, because D2 leaves the plugin on `Connecting…` | `pinned-approval.txt`, `shots/11`, `14`–`17`, `evidence/task11c/d7-live.txt`, `evidence/task11c/shots/02`, `03` |
| Settings | PASS: Test connection result, pin toggle and connection line. The model picker shows `No models reported by the bridge` (stub) | `shots/10-test-connection.png`, `35` |
| Launcher | PARTIAL: the Ask hand-off file was 0600 in a 0700 directory, was deleted on read, and the prompt was sent. The `/hermes` provider rows were not captured | `launcher-ask.txt` |
| Mid-turn drop | PASS: the bridge was killed mid-stream and the plugin respawned it within 5 s. The panel came back Online, with no token in any log. The in-flight turn was lost and no error was shown | `midturn-drop.txt`, `shots/37`, `38` |
| Small output | NOT RUN: the nested output is 953x1033; no small output was available | |
| Scroll | PASS (D1 fixed): a 60-line streamed reply rendered without a crash, the wheel scrolled up and down, and the plugin pid was unchanged | `scroll.txt`, `shots/18`–`20` |
| Approval card content | PASS (D5 fixed): the card shows the description and the command | `shots/14-pinned-card.png` |
| Restart by the host | FAIL (D2, sysc-shell): see below | `d2-root-cause.txt`, `shots/d2-01-after-kill.png` |

### Defect status

| ID | Status | Commit |
|---|---|---|
| D1 `wrapLines` panic | fixed, holds live | `8cdecc0` |
| D2 `Connecting…` after the host restarts the plugin | fixed plugin-side: Hermes reuses its saved settings after a respawn, including an all-defaults copy. Held up in the `d2b.md` nested live check (kill -9, Manager Retry, cold start, client-only) | `9d10165`, `606c0e1`, `917e6ec` |
| D3 bar left and right click | fixed, holds live | `292d77a`, `fd7e810` |
| D4 action errors not shown | fixed. The live stale-click message was not triggered (O1); Settings `Last error` shows live | `f6e6870`, `bc810b9` |
| D5 approval card content | fixed, holds live | `803a844` |
| D6 README on `always` | fixed | `0e60f4c` |
| D7 pinned panel not restored at shell start | fixed, holds live after a shell restart | `951ffdd`, `67c9241` |
| D8 bar does not say why it is in Error | fixed, holds live for a wrong token | `6a30c4b` |

### Remediation fixes (rounds 1 and 2)

Each is unit-tested and holds live; see *Live runs 2026-11-18 and 2026-11-19*.

| ID | Status | Commit |
|---|---|---|
| b200f5b approval server request dropped by the bundled bridge | fixed, holds live (archPC, 2026-11-18) | `25768b4` |
| O1 click resolves a card that replaced the one aimed at | fixed: a click is bound to the request_id its card was rendered for, or a per-card nonce for an id-less request; holds live. The first pass `89d4093` (1 s time guard) is superseded | `1d8ece5` |
| Composer one-shot held the action queue | fixed: runs off the queue, so Interrupt and approvals are not stuck behind it; holds live | `ea33faa` |
| R4 Allow Session offered when Hermes does not allow it | fixed: the card draws only the choices Hermes offers and refuses any other; holds live | `be033ac` |
| R7 auto-start gateway toggle waited for a reconnect | fixed: read at every poll, as Noctalia does; holds live | `c95385f` |
| R8 refused model apply said nothing | fixed: shown in the action error row, and the picker keeps the live model; holds live | `5c605b1` |
| R11 one-shot result fields unused | fixed: the reply shows in the transcript at once, the dead fields are gone; holds live | `7c1ecbc` |

### Open items

| ID | Status | Reason |
|---|---|---|
| R5 bridge state has no lock across threads | open | `HermesState` is shared by the `ThreadingHTTPServer` threads; it is upstream bridge code outside the allowed bridge diff |
| R6 `/detect` is unauthenticated | open, by design | upstream design: it answers before a token exists and returns detection info only |
| R9 check-then-read race in `takeLauncherAsk` | open, negligible | `Lstat` then `ReadFile` in a 0700 directory of the same user |
| R10 shutdown flush can hang up to 5 s | open | a wedged host holds the flush; the code is outside the hermes paths |

**D2 cause (sysc-shell `b50262d`, `internal/shell/pluginhost.go`).** The host
sends the plugin-scope `SettingsChanged` only once, after the first
`rt.Start` in `ensure()` (`:519-523`, `pushSettingsLocked` `:574-575`).
When it relaunches the plugin on its own, the runtime calls only
`SessionStarted` (`:479-482`): `serviceSessionStarted` (`:685-699`) and
`reopenViews`. Only instance-scope settings are re-sent there, through
`announceView` (`:1274`). Protocol v1 has no settings read call, so the
plugin cannot ask. The plugin starts its service on the first plugin-scope
`SettingsChanged` (`cmd/sysc-plugin-hermes/main.go:574-590`), so the new
process waits forever. A settings change or a fresh `ensure()` clears it,
which is why approving in Settings fixed it in the first run. A live repro
had `kill -9` on the plugin, an instant respawn, no bridge after 70 s and
`Connecting…` on the bar.

**D2 fix (plugin-side, `9d10165`).** Hermes saves the last host settings in
its own plugin state on every plugin-scope `SettingsChanged`. The copy holds
every value, including a pasted bridge token; `state.json` is mode 0600,
the same exposure as `config.json`. After a respawn the plugin reads that
copy and applies it once a ~2 s grace has passed and the host has sent
nothing. Host settings always win, and identical settings do not reconnect.
A missing or unreadable copy leaves the bar on `Connecting…` as before.
sysc-shell is unchanged. The host change `e0becead` was not used because it
changed respawn behaviour for every plugin.

**D7 (P2): the pinned panel is not restored at shell start.** At start,
`cmd/sysc-plugin-hermes/main.go:256-272` calls `PinnedSurface.Restore`
(`plugins/hermes/pinned.go:122-150`) once. During shell start no bar output
exists yet, so `surface.open` is refused with `no output` (sysc-shell
`pluginhost.go:2272-2275`). The plugin logs the error and never tries again.
Live, `state.json` held `"pinned":true`, the nested shell was restarted, and
the log showed `restore pinned surface: surface.open: no output` with no
pinned panel. Fix: retry the restore when an output appears or after a
short backoff.

**D8 (P3): the bar does not say why it is in Error.** `publishConn` puts
`Wrong token` or `No bridge token…` in `View.Message`
(`plugins/hermes/service.go:705-711`). The bar tooltip and the summary read
only `State.Bridge.Error` (`cmd/sysc-plugin-hermes/main.go:893`,
`plugins/hermes/bar.go:103`, `summary.go:309`). So the reason shows only on
the Settings page.

**D7 and D8 live check (2026-11-17, 11:50 to 11:54 AEDT, plugin built from
`6a30c4b`; `67c9241` changes only a test).** A separate nested niri and
sysc-shell under `/tmp/ht11`, with a stub model and a throwaway Hermes home.
The build had no debug logging. QA accepted `951ffdd`, `6a30c4b` and
`67c9241` (`reports/qa-t11c.md`). The setup was torn down at 12:08 AEDT and
the daily session was not touched (`evidence/task11c/teardown.txt`,
`ps-before-daily.txt`, `ps-after-daily.txt`).
- D7: Pin from the panel saved `"pinned":true`. After the nested shell was
  restarted, the floating Hermes panel came back with Unpin showing. Evidence:
  `evidence/task11c/d7-live.txt`, `evidence/task11c/shots/02-pinned.png`,
  `03-after-shell-restart.png`.
- D8: with the token file replaced by a random value, the bridge answered 403.
  The bar showed Error, its tooltip read `Hermes: Error · t11-stub` and
  `Wrong token`, and the summary showed a `Wrong token` banner. After the file
  was restored, the bar went back to Online and the banner went away. Neither
  token value appears in any log or evidence file. The missing-token case is
  covered by tests only. Evidence: `evidence/task11c/d8-live.txt`,
  `evidence/task11c/shots/04` to `08`.

**O1 (observation, not a defect):** an approval card can be replaced while
the pointer is moving to it. The node ID carries the new signature, so the
click then resolves the next request. The stale-drop protection works only
when the click carries the old ID. Whether Noctalia behaves the same was not checked.
Since `1d8ece5` a click is bound to the request_id its card was rendered for
(see *Remediation fixes*); holds live since 2026-11-19.

### Teardown

Only processes whose environment had `HOME=/tmp/ht11/home` were stopped, plus
the stub on 18877, which was matched by its command line. Those were the
nested shell, the plugin, the bridge, the throwaway gateway, niri, its D-Bus
daemon and the stub. Ports 18877 and 19777 were free afterwards. Logs and
scripts were copied to `ht11-final/` before `/tmp/ht11` was removed. The
symlink to the Hermes checkout was removed, not followed. The daily
`sysc-shell` (pid 1092015, started 01:20 AEDT), greetd, the session worker,
the compositor and sysc-lock have the same pids and start times as in
`daily-before.txt`, and the daily shell's child processes match
`ps-before.txt` (`teardown.txt`, `daily-after.txt`).

## Task 11 first live run (2026-11-17, 08:14 to 08:58 AEDT)

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

### Defects found (fixed later; see *Defect status* in the re-run)

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

Evidence: `archpc-b200f5b-*.txt`. Fixed in `25768b4`: the bundled bridge
also handles the `approval` server request (and its `request.cancel`), while
the `approval.request` event still works. Checked live on 2026-11-18.

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
- and confirm the b200f5b-style approval path live first, because the VPS
  build is newer than `835a913` (fixed in `25768b4`, checked live on archPC's
  `b200f5b` on 2026-11-18).

## Automated gates

On `9f2f8c7` (2026-11-19). Repository-wide runs are capped at `-p 2` and the
race detector runs one package at a time, the machine's limits.

| Gate | Result |
|---|---|
| `gofmt -l .` | clean (the untracked local `zz_dump_test.go` excluded) |
| `go vet -p 2 ./...` | pass |
| `go test -p 2 -count=1 ./...` | pass |
| `go test -race -count=1` on `./plugins/hermes/` and `./cmd/sysc-plugin-hermes/` | pass |
| `python3 -m unittest test_hermes_bridge` (42 tests) | pass |
| `make validate` | pass |
| `go run ./tools/catalog validate` | not run: no `hermes` row in `catalog.json` yet |

## QA verdicts

These are the reviews that were actually completed. Gate 3, the live
environment gate, was deferred in each; the live matrix was run afterwards
(see *Live runs 2026-11-18 and 2026-11-19*).

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

**Status: superseded.** The four-way comparison was never run. The host keeps
no scroll position for a plugin list across an update and sends no wheel
events (README host gap 5), so the plan's fallback shipped instead: since
`f74206d` the transcript is a paged window over the wrapped lines. It follows
the newest line, **Older** and **Newer** move a screen at a time, **Latest** or
a new prompt returns to the tail, and a paged-back window stays put while a
reply streams. It holds live (v1 shots 01 to 05, 2026-11-17 and -18). The
static reading of sysc-shell v0.2.5 recorded for Task 7 is kept below.

- `internal/shell/pluginhost.go` `onMessage`: a `view.snapshot` and a
  `view.patch` both produce a newly converted root, and `applyResult`
  replaces `v.Root`, so every option rebuilds the panel tree.
- `internal/shell/panelhost.go` `rebuildPanel` carries scroll offsets over
  only for the host's own settings lists, so a plugin list jumps back to the
  top on every update.
- `view.focus` matches only focusable nodes and does not scroll, so focusing
  the newest line cannot bring it into view.

## Approval check

**Status: passes live for the bundled bridge.** Deny, Allow and Allow Session
resolved real approvals on Hermes `835a913` (2026-11-17); the `approval`
server request on Hermes `b200f5b` showed a card, refused a wrong
`request_id` with 409, blocked on deny and ran on allow-once (archPC,
2026-11-18); a click on a card that had just been replaced was refused with
`The approval changed; check the card again` (2026-11-19). An approval through
a **stock** bridge was not exercised; the README states what a stock bridge
does differently.

## Parity matrix

**Status: not run as a side-by-side comparison.** Every row of the design's
matrix has been run on the plugin (see *Live runs 2026-11-18 and
2026-11-19*), but no comparison against a running Noctalia was produced.
The design's surface inventory and host-gap list describe what should be
compared; they are not a substitute for the matrix.

## Bundled bridge

`scripts/hermes_bridge.py` is the local v5 port's bridge, including the
unpushed SessionDB `/sessions` and resume-history fixes.

| | Path | SHA-256 |
|---|---|---|
| Reference v5 source | archPC `~/projects/noctalia-hermes-agent/v5/hermes-agent/scripts/hermes_bridge.py` | `892e27f75ec9f771d2a4dc7178ca5afcf19b9198beed0c558e2d25064cc15178` |
| Bundled before Task 3a (commit `a8384bd`) | `plugins/hermes/scripts/hermes_bridge.py` | `0be2bc3cffbc01a8573fa573673642e58a0fa3934b7de685af914c5606ae8e99` |
| Bundled after Task 3a (`937ac1a`, unchanged through `917e6ec`) | `plugins/hermes/scripts/hermes_bridge.py` | `c658df456138eb6cfd6113c7a94ace79683177e30ebdc2c49d8447a4c5fac183` |
| After `25768b4` | `plugins/hermes/scripts/hermes_bridge.py` | `c0387bfdd1bb795ac837569c03993cc2b3ab8f358840eccaaa3ff0def25bd5cd` |
| After `54258a6` | `plugins/hermes/scripts/hermes_bridge.py` | `b2e23bbc128264c37979c353970120af2ddae24c4a43e1f0bc3bf4784d44d1d6` |

The pre-Task-3a bundle differed from the reference source only by a comment
after the shebang pointing at `ATTRIBUTION.md`. The local changes since, each
marked `sysc-shell:` in the source:

| Change | Commit |
|---|---|
| Resolve the shown approval by `request_id`, 409 `approval_mismatch` otherwise; refuse flag-like model names with 400 `invalid_model` | `937ac1a` |
| Show approvals sent as an `approval` server request; withdraw the card on `request.cancel` | `25768b4` |
| Keep a client hang-up (a cancelled one-shot) out of the bridge's error state | `9204603` |
| Restore the stock `empty_model` reply; correct the provenance header | `8dbcc45` |
| Import `hermes_bootstrap` at startup, before Hermes's modules | `e8217e9` |
| Keep a model Hermes refused out of the live model | `c84755f` |
| Bind the resumed session; read a gateway whose process is gone as stopped | `bb868da` |
| Return the reply to an RPC Hermes runs on its worker pool | `879e81f` |
| Keep the stored session id when Hermes reattaches a live session | `54258a6` |
| Preserve the active conversation when Resume fails, queue follow-ups as separate turns, isolate one-shot status, and redact bridge diagnostics | `0.1.1` (sysc-plugins-123–126) |

`937ac1a` was authorized with Task 3a. The owner approved `bb868da`, `879e81f`
and `54258a6` on 2026-11-19. `25768b4`, `9204603`, `e8217e9` and `c84755f`
were made to fix defects found in testing and are flagged here for the
owner. `DiffScopeTest` in `scripts/test_hermes_bridge.py` fails if any
function outside this list changes (methods named with their class).

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

This was a source read. The `b200f5b` server-request delivery path is fixed in
`25768b4` and was checked live on 2026-11-18 (see *Approval check*).
