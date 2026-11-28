# bitsub handover — before the notify-actions and text-spans plan

Date: 2026-11-29 · Branch `feat/bitsub` · PR [#344](https://github.com/Nomadcxx/sysc-plugins/pull/344)

This hands over from `2026-11-29-bitsub.md` (Tasks 1–13 done) to
`2026-11-29-notify-actions-and-text-spans.md` (sysc-notify A1, sysc-shell A2 and B1), and on to bitsub Task 14
after it. It lists what was not done and what must be true before that plan starts.

## 1. Done (PR #344)

| Plan task | State |
|---|---|
| 1–2 `subs` domain (dates, money, renewal maths, totals, wire types) | done |
| 3 SQLite store | done |
| 4 service layer (validation, rollover, snapshot) | done |
| 5 Frankfurter FX | done |
| 6 reminder cadence and engine | done |
| 7 Calendar mirror, Gmail and digest through gog | done, but no live sample (see §3) |
| 8 HTTP auth and `/api/v1` | done |
| 9 MCP tools (go-sdk v1.8.0) | done |
| 10 `bitsubd`, config, token commands, systemd unit | done |
| 11 plugin art and views | done; panel rendered with `sysc-panel-preview` |
| 12 plugin client, relay, session loop, manifest (protocol minor 28) | done |
| 13 skill | done; harness syntax checked against the installed CLIs |
| **14** notification buttons and two-tone header | **not started; blocked on the companion plan** |
| **15** deploy | **not done; needs the owner's go-ahead** |

All the rulings made during execution are in the git-ignored ledger
`.superpowers/sdd/2026-11-29-bitsub/progress.md` in the worktree. The ones that matter downstream are repeated in §5.

## 2. Must be true before the companion plan starts

1. **PR #344 merged to `main`.** Task 14 builds on `plugins/bitsub` and `cmd/sysc-plugin-bitsub` as merged.
2. **The protocol numbers are still free.** Checked 2026-11-29: sysc-shell `origin/main` (20e4bbfc) has
   `ProtocolMinor = 28`. A2 takes 29 and B1 takes 30. If main has moved, take the next free numbers and update
   Task 14 and `actionsMinor` in `cmd/sysc-plugin-bitsub/main.go` to match.
3. **The owner picks the sysc-notify release version.** As of 2026-11-29:
   - sysc-notify `origin/main` is 8ee2ebc. sysc-shell pins `github.com/Nomadcxx/sysc-notify v0.1.1`.
   - `git tag --sort=-v:refname` lists `v0.2` first, but `v0.2` is **not valid Go semver**: Go needs
     `vMAJOR.MINOR.PATCH`, so `go get …@v0.2` fails.
   - `v0.2` also points at an older commit (04fee0a, 7 commits behind main) than `v0.1.2` (3 behind).
   - So "use the next minor" in Task A1 is ambiguous. Agree a tag (e.g. `v0.2.0` or `v0.3.0`) with the owner
     before tagging, and don't bump past the stray `v0.2` by sort order alone.
4. **The root cause is unchanged** (checked 2026-11-29):
   - `protocol.ProducerRequest` in sysc-notify still has no `Actions` field.
   - sysc-shell `internal/shell/notifications.go:407` still has `// ponytail: v1 Actions are dropped — the
     producer protocol has no actions.`

   A1 and A2 are still needed as written.
5. **Worktrees, not the main checkouts.** `~/sysc-shell` and `~/sysc-notify` may hold other sessions' work.
   Branch from `origin/main` into `~/worktrees/<name>`, per the companion plan's Global Constraints.

## 3. Not done, and not blocking the companion plan

These are owner actions or live checks. They can run in parallel with the companion plan.

- **gog calendar scope** (the owner runs it; it opens a browser):
  `gog auth add noovie@gmail.com --services drive,gmail,calendar --force-consent`.
  Today the account has drive and gmail only.
- **Live gog JSON check.**
  - `plugins/bitsub/server/google/testdata/*.json` was built from the gog v0.28.0 source (`internal/cmd/calendar_*.go`):
    - events: `{"events":[…google calendar.Event…]}`
    - create/update: `{"event":…}`
    - create-calendar: `{"calendar":…}`
  - After the re-auth, run `gog --account noovie@gmail.com --json calendar events primary --max 1` and compare the
    `id` and `extendedProperties.private` fields. Then replace the fixture with a sample stripped of personal data.
  - A mismatch would appear as `Calendar out of date: …` in the panel footer, not as silence.
- **Deploy (Task 15).** Every step that restarts something needs the owner's OK:
  1. Build `bitsubd` from a clean checkout of main after merge.
  2. Install it with the unit, write `~/.config/bitsub/bitsubd.json` (0600, listen `127.0.0.1:7733` plus archPC's
     Tailscale IP), then `bitsubd token add archpc-plugin`, `laptop-plugin` and one token per MCP client.
  3. `systemctl --user enable --now bitsubd`.
  4. Plugin on both machines through `~/sysc-plugins-deploy/<rev>`, plus `client.json` (0600) and
     `plugins.enabled`; then a shell restart.
  5. MCP entries per `skills/bitsub/SKILL.md`.
  6. Smoke test: a "Test trial" ending in 2 days, then `delete_subscription`.

  Deploying before the companion plan is useful: the live notification flow exists to test A2 against.
- **Not verified live:**
  - The bar view: `sysc-panel-preview` renders panels only. The bar is checked by `plugin/lint` at 240, 120 and
    64 px, but has not been seen in the real shell.
  - A real Claude Code, Codex or opencode session connecting to `/mcp` (covered only by the end-to-end test,
    which uses the SDK client).
  - The laptop over Tailscale.
- **SuperGrok trial.** Cancel it by hand before **8 Dec 2026**; bitsub won't be live in time.
- **Beads.** No beads issues were filed for the follow-ups below. File them from a checkout with a working beads
  DB if the owner wants them tracked (see the beads export truncation note before committing `issues.jsonl`).

## 4. What the companion plan and Task 14 should know about the bitsub side

- **Notification flow today.**
  - bitsubd queues each reminder once in its `notifications` table.
  - Each plugin polls `/api/v1/snapshot?since=<last_seq>` and calls `CallNotify` for anything newer that was
    issued within 12 hours.
  - It stores `last_seq` in plugin state. The first run records the server's seq without replaying history.
  - Critical reminders get `TimeoutMS 0`; the others get 10000.
- **Actions are already wired, just gated.**
  - `notify()` in `cmd/sysc-plugin-bitsub/main.go` copies each `subs.Action` into `v1.NotifyAction` when
    `s.minor >= actionsMinor` (29).
  - The keys are exactly `keep:<id>`, `cancel:<id>` and `done:<id>`. `subs.ParseActionKey` parses them and rejects
    anything else.
  - The labels are "Keep", "Cancel" and "I've cancelled it".
  - The same key can reach two machines, because both plugins show the same notification. So the A2 routing
    must deliver a click only to the plugin instance that owns that notification id.
- **The negotiated minor is capped by the pin.** `v1.NegotiateProtocol` picks the highest minor ≤ the
  `ProtocolMinor` compiled into the plugin. Until Task 14 bumps the sysc-shell pin in sysc-plugins `go.mod`, the
  plugin negotiates 28 even on a 29 or 30 host, and sends no actions. That is intended. Task 14 must bump the pin
  **and** `plugins/bitsub/manifest.json` `protocol.minor` (to 30).
- **Task 14 hooks.**
  - Header: `Panel` uses `PlainRows()` today. `ArtRows()` already returns tone spans (accent and subtle, padded,
    adjacent tones merged). With `State.HostMinor >= 30`, emit one text node per row with `Spans` and an empty
    `Text`. `State.HostMinor` is already filled from the handshake.
  - Clicks: handle `v1.NotifyActionEvent` (the name the companion plan gives it) in the session loop.
    `keep` → `SetIntent(keep)`, `cancel` → `SetIntent(cancel)`, `done` → `ConfirmCancelled`, `default` →
    `CallPanelOpen`. Then poll, the way the panel's own actions do (`enqueue` then `requestPoll`).
  - Clicks from a notification carry no view revision, so the stale-revision guard does not apply to them.
- **B1 measurement check.**
  - The art rows are 73 runes and about 205 bytes each. At the host's `len(bytes)×8` text metric they measure
    about 1640 px, against a 758 px panel.
  - Columns never refuse, so this lints clean today. B1's `TestSpansMeasureLikeText` should use one of these
    rows (`plugins/bitsub/assets/bitsub.ans`) as its fixture.
- **Host quirk found while rendering.**
  - A two-child title row laid out differently from a three-child one: the trailing price landed at the row's
    far edge. bitsub works around it by always giving title rows three children.
  - If B1 touches row layout, worth knowing; it may be a host bug worth its own issue.

## 5. Rulings that change interfaces the plan named

- `remind.DueSteps(s, today, rates, charged bool)`: a fourth parameter, the service's "probably charged" flag, so
  the cancel nag keeps going after a missed renewal.
- Price format everywhere: `A$215.50/yr (US$139.00)`, with the suffix after the AUD figure (spec §8/§10), not
  `A$31.00 (US$20.00)/mo`. An approximate zero total shows `—`.
- `mcptools.NewServer(svc, version, auth)`: the actor comes from each request's bearer token through
  `httpapi.Auth.Actor`, because the SDK's auth package would add `golang.org/x/oauth2`.
- `google.DesiredEvents(list []subs.Subscription, rates, today)`, not `[]subs.Item`. Alarms are
  `popup:(N×24−9)h`, so they fire at 09:00 local. The content hash rides on the event as the private prop
  `bitsub_hash`.
- The plugin skips byte-identical repaints, so a revision only advances on a real change. Keep this if Task 14
  adds repaint triggers.

## 6. Deferred minor findings (from the final self-review)

- `Calendar.Ensure`: if a `create-calendar` call times out after Google created the calendar, the retry creates
  a second "Subscriptions" calendar.
- The plugin's 16-slot job queue drops a job when full, so a click could be lost under an extreme backlog.
- `list_subscriptions` with an unknown `status` returns an empty list instead of a validation error.
- An undecided renewal gets no "renews today" step, because the day-2 step key dedups it.
- An anchor date far in the past backfills every passed renewal as assumed charges at today's rate.
- The plugin timer is `Reset` without draining, which can cause one extra poll.
- The final review was a self-review by the implementer. An independent review of PR #344 is still open if the
  owner wants one.
