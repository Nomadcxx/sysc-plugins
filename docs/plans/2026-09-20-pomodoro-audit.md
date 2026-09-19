# Pomodoro timer — audit, gap analysis, and ledger

Date: 2026-09-20
Commissioned by: `docs/plans/2026-09-20-pomodoro-handover.md`
Repos audited: `/home/nomadx/sysc-plugins` (main @ `dd64750`), `/home/nomadx/sysc-shell` (main @ `8602bfa`)

Everything below was verified against the running system or the source. Where I could not verify
something, it says so rather than guessing.

---

## 1. Status: the timer is up

Three plugin processes run, and the bar widget renders the clock glyph with the countdown:

```
sysc-plugin-weather
sysc-plugin-timer
sysc-plugin-world-clock
```

Screenshot evidence: the bar reads `⏱ 25:00` between the weather capsule and the world clock.
Before the work below it read `⏱ 05:00`; before the rebuild it was the failed placeholder.

Gates, all green:

| Gate | Result |
|---|---|
| `gofmt -l .` (plugins) | clean |
| `go vet ./...` (plugins) | clean |
| `go run ./tools/validate-manifests` | 8/8 ok |
| `go test -count=1 ./...` (plugins) | all packages ok |
| `go test -race ./plugins/timer` | ok |
| `gofmt -l internal/ plugin/` (shell) | clean |
| `go test -race ./internal/{ui,plugin,render}` | ok |

Note on this machine: a pre-tool hook blocks uncapped `./...` Go commands (an uncapped run
hard-locked the box twice — zram-only swap). Use `GOMAXPROCS=4 … -p 2`, and race-check one
package at a time. The handover's §8 gate commands trip this hook as written.

---

## 2. The handover's stated root cause is wrong

§0 claims the shell rejected the timer because the plugin declared protocol minor 3 while the
installed shell advertised minor 2. **No such rejection exists anywhere in the shell.** Verified:

- `internal/plugin/manifest.go:379-383` rejects only `Major != 1` and a negative minor. The file
  is byte-identical between the installed commit `fed2d9f` and HEAD (`git diff` empty).
- `internal/plugin/supervisor.go:246` checks only `reply.Protocol.Major != 1`.
- `plugin/v1/client.go:44` has every plugin answer `Version{Major: 1, Minor: 0}`, hard-coded, and
  never reads `hello.Supported`.

So the manifest's `"minor"` is effectively decorative today, and mixed minors are fine
(handover §6.5 answered: yes, the shell accepts them — because it never looks).

Direct proof the plugin itself was healthy all along — driving the *installed, pre-fix* binary
with a host handshake:

```
REPLY type=plugin.hello  Protocol:{Major:1 Minor:0}
                         Plugin:{ID:org.sysc.timer Name:Pomodoro Timer Version:1.3.0}
process still alive
```

**What actually happened.** The shell process started at `05:04:13`, the same second
`make install` was rewriting `plugins/timer/bin/sysc-plugin-timer` (bin mtime `05:04`). A plugin
exec against a binary being rewritten fails (`ETXTBSY` / partial image). `internal/plugin/runtime.go`
does retry, but on a rolling budget; once spent the plugin parks in `StateFailed` and nothing
retries it — which is exactly the sticky "failed" the user saw. Weather and world-clock were not
being rewritten at that instant and came up fine.

I cannot prove `ETXTBSY` specifically: that process's captured stderr died with the old shell.
What is proven is that the minor theory is impossible, the binary handshakes cleanly on demand,
and a rebuild-then-restart fixes it. The deploy ordering in the handover's §2 playbook
(install plugins, *then* restart) is the right order; this run appears to have raced it.

---

## 3. Bug found and fixed: the work phase was five minutes

This is the one code change I made. It was not in the handover's ledger.

`NewSession` set `work: 25 * time.Minute` on the `Session` but never pushed that into the
underlying `Timer`, which carries its own constructor default of **5 minutes**
(`plugins/timer/timer.go`, `New()`). `Session.Remaining()` delegates straight to the timer.

Consequence on this desktop, where no timer settings are configured: a fresh pomodoro opened in
**Work** mode, with the footer promising "Next long break after 4 more", while counting down
**5:00**. Confirmed by dumping the plugin's real view tree, not inferred:

```
--- SNAPSHOT view=bar-1 ---
  button id="open" text="05:00" icon="schedule"
--- SNAPSHOT view=panel-1 ---
  gauge  valuetext="05:00"
  text   text="Work"
```

It self-heals on the first mode-pill click or settings change (both call `timer.SetDuration`),
which is why it was invisible in testing — and why **every** test in `session_test.go` missed it:
all five call `SetDurations(...)` as their first statement, priming the exact state that hides the
defect. That answers the handover's §6.4 "tests honest?" — they are honest, but they never
exercise the out-of-the-box path.

Fix (`plugins/timer/session.go`): `NewSession` now calls `s.timer.SetDuration(s.work)`.
Regression test `TestNewSessionOpensOnTheWorkLength` added, and verified to fail without the fix:

```
--- FAIL: TestNewSessionOpensOnTheWorkLength
    session_test.go:113: remaining = 5m0s, want 25m
```

Deployed and confirmed: the bar now reads `25:00`.

**This change is uncommitted in the working tree.** I did not commit it — say the word and I will.

---

## 4. Does the panel hard-fail? No.

The world-clock freeze came from a layout hard-failure (`child 3 of kind 3 does not fit`). I
checked the timer panel for that class directly: captured the plugin's real panel tree, ran it
through the shell's own `plugin.Convert` and `ui.LayoutColumn` at the manifest's 360×480, across
three text-measure densities (7/8/9 px per rune, bracketing the house test convention).

```
panel-1.json @7px/rune: laid out OK in 360x480
panel-1.json @8px/rune: laid out OK in 360x480
panel-1.json @9px/rune: laid out OK in 360x480
bar-1.json   @7/8/9px:  laid out OK in 400x34
```

The harness was a temporary test inside `internal/plugin/`; it has been deleted, and
`gofmt -l internal/ plugin/` is clean. This proves the panel does not hard-fail at its declared
size. It does **not** prove visual fidelity — approximate metrics, no painting.

All eight input handlers are wired: `open`, `close`, `start`, `pause`, `reset`, `mode-work`,
`mode-short`, `mode-long`.

---

## 5. Gap analysis against `pomdoro.png` and prior art

Structure matches the spec. The deltas are styling and iconography.

| Spec element | Built? | Delta |
|---|---|---|
| Header "Pomodoro Timer" + ✕ | yes | none |
| Subtext "Focus session • N completed" | yes | none |
| Circular ring, time inside | yes | arc colour is `LerpColor(Accent, Secondary, progress)` — your theme accent reads blue in the bar, spec is lavender. Theme-driven, not plugin-driven. |
| "Work" label beneath ring | yes | none |
| Centred pause/stop/reset **icons** | partial | rendered as **text** buttons "Start"/"Reset", and the row sets no `CenterX`, so they are not centred. |
| Three rounded **pills** with icons | partial | text-only, and the pill nodes set **no `Radius`** (the footer sets `Radius: 10`), so they take the default button shape rather than the spec's fully-rounded pill. Active state does work (`Fill: accent`). |
| Footer card + checkmark | yes | the ✓ is a literal text glyph, not an icon. Card styling matches (`Fill: card`, `Radius: 10`). |

**Icon gap is real and is a shell-side problem, exactly as the handover said.** Verified name by
name against the two catalogues:

| Icon | Availability |
|---|---|
| `schedule` (bar, in use) | in `iconfont` — reachable |
| `pause`, `play_arrow`, `check`, `coffee`, `restart_alt` | material subset only — **unreachable from plugins** |
| `briefcase`, `couch` | absent from both |

`internal/plugin/view.go:299-333` resolves plugin icons through `render.IconByName` (iconfont
only) and hard-errors on an unknown name. So icons cannot be fixed plugin-side; it needs shell
vocabulary work, plus two new glyphs.

Behaviour vs the noctalia plugin is correct and covered by tests: work→short, long every Nth,
both auto-start flags, `SetMode` resets, sessions clamped ≥1. Transition notifications read
"Work complete — time for a break" / "…for a long break" / "Break over — back to work".

---

## 6. Correctness findings (code audit)

**Locking is deadlock-free.** `Session.mu` → `Timer.mu` is the only nesting order; the delegating
methods (`Start`, `Remaining`, …) take `Timer.mu` alone and never re-enter `Session.mu`. No
inversion exists. The handover's §6.4 concern is unfounded.

**`Progress()` clamps to [0,1]** (`timer.go`), so `Value: 1 - progress` can never leave the
gauge's validation range. I had suspected a validation-rejection crash here; there isn't one.

**Restore loses the phase (real, user-visible).** Only the `deadline` unix stamp is persisted
(`main.go restore()`). After a shell restart mid-break: `NewSession` resets mode to Work and the
tally to 0, then `Restore` sets the countdown to the leftover. The panel then labels a break as
"Work", and when it fires, `Tick` takes the work branch — incrementing `completed` and counting a
break as a finished pomodoro, permanently skewing the long-break cadence. The handover listed
"mode not persisted" as cosmetic; the tally corruption makes it more than that.

**`Restore` also overwrites `duration`** with the remaining time, so the ring resets to full on
restart and no longer reflects the true phase fraction.

**Torn reads in `publish()`** (minor): `Remaining()`, `State()`, `Progress()`, `Mode()`,
`Completed()` are five separate lock acquisitions, so a publish racing a `Tick` transition can
mix pre- and post-transition values for one frame.

---

## 7. Over-reach check: clean

`plugins.enabled` is exactly `[org.sysc.weather, org.sysc.timer, org.sysc.world-clock]`. Bar
entries are `timer-1` and `worldclock-1` only. `grep -c "notes\|screen-recorder"` over the live
config returns **0** — notes and the GPU recorder are gone, as you asked.

Unrelated drift vs `config.json.bak-pre-plugins-20260920`, flagged not attributed: theme
templates `cava/gtk3/gtk4/kitty/niri/qt` flipped to `true`, a `bluetooth-1` widget added, and
`wallpaper` gained an instance id. None of it is plugin work; please confirm it is yours.

---

## 8. Ledger — pick what you want

| # | Item | Severity | Effort | Notes |
|---|---|---|---|---|
| 1 | Commit the 5-minute work-phase fix (§3) | — | 1 min | Already written, tested, deployed. Just needs a commit. |
| 2 | Persist session mode + completed tally (§6) | **high** | ~1 h | Corrupts the pomodoro count across restarts. Extend the state blob beyond `deadline`. |
| 3 | Centre the controls row; give pills a `Radius` | medium | ~20 min | Pure plugin-side, closes two visible spec deltas. |
| 4 | Material-icon vocabulary for plugins | medium | ~half day | Shell-side: widen the converter past `iconfont`, add `briefcase`/`couch`. Unblocks every spec icon. |
| 5 | Make `Restore` preserve phase duration | medium | ~30 min | Ring accuracy after restart. Pairs naturally with #2. |
| 6 | Snapshot `publish()` under one lock | low | ~20 min | One-frame tearing only. |
| 7 | Harden deploy ordering against the install race (§2) | medium | ~30 min | Either make `make install` write-and-rename, or have the shell surface `StateFailed` stderr in the UI so this is diagnosable next time. |
| 8 | Drop or enforce the manifest `minor` field | low | ~30 min | It currently means nothing; either gate on it or stop advertising it. |
| 9 | README status table stale (carried: B) | low | ~10 min | Timer row still says "UI rebuild started". |
| 10 | Carried items D, E, G, and the two shell suspects | — | — | Untouched this session; see handover §7. |

---

## 9. What I could not verify

- **Panel anchoring under the clicked widget** (`fed2d9f`). The code is in the running binary and
  has a test, but there is no CLI to open a panel, so I could not confirm it on screen.
- **Panel visual fidelity.** Layout is proven not to hard-fail; colours, spacing and typography
  need your eyes.
- **World-clock opened twice without freezing.** `651ede4` is in the running binary and
  `internal/ui` passes, but the plugin only emits its bar view to a synthetic host, so I could not
  drive its panel offscreen.

These three need a click. If you open the timer and the world clock and tell me what you see —
or hand me a screenshot — I will take it from there.
