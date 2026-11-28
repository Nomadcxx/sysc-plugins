# bitsub: subscription tracker for sysc-shell and its agents

Date: 2026-11-29 · Status: approved in conversation, pending plan · Repos: sysc-plugins (plugin, service, skill), sysc-shell (two protocol additions)

## 1. Intent

The owner pays for a growing pile of subscriptions: streaming (Netflix, Stan, Spotify), infrastructure (VPN, VPS) and many LLM providers (Synthetic, opencode Go, Command Code, MiniMax, SuperGrok and others). Trials such as Speechify and SuperGrok keep renewing because nothing asked "keep or cancel?" in time. Today a Grok bot reads one Gmail inbox and puts events in a calendar. It misses mail from other inboxes, and it depends on a service the owner is about to cancel.

bitsub keeps the list on the owner's own machine and asks before every charge that matters, early enough to act on the answer. Most entries arrive through agents (Claude Code, Codex, opencode with open-weight models). Those agents need a stable way to add, update and query the list, plus a skill that teaches them the conventions.

The owner always does the cancelling. bitsub never cancels anything. It records the owner's intent and keeps reminding until they confirm it's done.

Success looks like this: no trial converts and no unwanted renewal goes through without the owner having been asked at least twice beforehand on desktop, phone and email. They can also see what they spend each month in AUD at a glance.

## 2. Goals, non-goals, anti-goals

**Goals**
- One source of truth for subscriptions, held on archPC.
- An intent per subscription: keep, cancel or undecided. Reminders scale with the stakes.
- Reminders through three channels: sysc-shell notifications (desktop and laptop), a mirrored Google Calendar with phone alarms, and Gmail.
- Cost tallies in AUD, with the original currency in brackets when it differs.
- Agents on archPC or the laptop can read and change the list over MCP.
- A sysc-shell plugin (bar item and panel) on both machines. Keep and cancel can be done from the panel or straight from a notification.
- A skill shipped alongside, so any agent records subscriptions the same way.

**Non-goals**
- Scanning inboxes or parsing receipts. Entries arrive because an agent or the owner adds them.
- Cancelling subscriptions, or anything that touches a vendor account.
- A web UI, multi-user accounts, budgets, savings scenarios, themes, Pushover, ntfy or Gotify.
- Two-way calendar sync. Edits made in Google Calendar are overwritten.
- Offline edits on the laptop. While archPC is unreachable the laptop is read-only.
- Search, category breakdowns and tabs in the panel (decided against in the mockups).

**Anti-goals (failures even if it "works")**
- A reminder that never arrives because a sync failed silently. Every failing channel must show in the panel footer.
- Duplicate reminders: one notification per step, never one per machine or per poll.
- Losing subscription data or charge history: deletes are soft, the database is backed up, and writes are atomic.
- Exposing the service beyond localhost and Tailscale by default.
- Drift in renewal dates (31 Jan → 3 Mar style bugs).

## 3. Architecture

```
                 archPC                                          laptop
┌──────────────────────────────────────────────┐       ┌──────────────────────────┐
│ bitsubd (systemd --user)                     │       │ sysc-shell               │
│  ├─ store: SQLite (modernc, WAL)             │       │  └─ sysc-plugin-bitsub ──┼──┐
│  ├─ schedule: renewals, reminders, digests   │       │ agents (MCP over HTTP) ──┼──┤
│  ├─ fx: Frankfurter daily rates              │       └──────────────────────────┘  │
│  ├─ google: gog → Calendar + Gmail           │                                     │
│  └─ http: /mcp (MCP)  /api/v1 (plugin JSON)  │◀──── Tailscale (bearer token) ──────┘
│        ▲            ▲                        │
│ agents (MCP)   sysc-shell → sysc-plugin-bitsub
└──────────────────────────────────────────────┘
```

Binaries are all Go, built from sysc-plugins:
- `cmd/bitsubd`: the always-on service. It's the only process that opens the database.
- `cmd/sysc-plugin-bitsub`: the sysc-shell plugin. It's a thin client of `bitsubd` on both machines.

Package layout follows the repo's convention of a library package under `plugins/<name>/` plus a `main.go` under `cmd/`:
- `plugins/bitsub/`: plugin views, API client, notification relay, header art (the `plugins/bitsub` package plus `manifest.json`).
- `plugins/bitsub/subs/`: domain types, money, renewal maths, totals. These are pure functions, shared by the service and the plugin's API types.
- `plugins/bitsub/server/`: store, scheduler, reminder engine, calendar and email adapters, FX, HTTP API, MCP tools.
- `skills/bitsub/SKILL.md`: the agent skill.

`bitsubd` runs separately from the shell so that reminders, calendar sync and email keep going while the shell is restarted, crashed or logged out.

## 4. Data model (SQLite)

Money is stored as integer minor units plus an ISO 4217 code. Floats never hold money. Dates that are calendar days (renewal, trial end, access until) are stored as `YYYY-MM-DD` civil dates and interpreted in the system time zone (Australia/Melbourne). Instants are stored as UTC RFC 3339.

**subscriptions**
| field | notes |
|---|---|
| id | UUID (google/uuid is already in go.sum) |
| name | required, 1–80 chars, unique among non-deleted rows (case-insensitive) |
| url | optional, the vendor's account or billing page |
| amount_minor, currency | price per cycle, amount > 0, currency in the supported set |
| cycle, interval | `day`/`week`/`month`/`year` × 1–36 |
| anchor_date | the first real (or post-trial) charge date; renewals derive from it |
| trial_end | optional; when set and in the future, the next event is "trial ends" and the first charge is on this date |
| intent | `keep` / `cancel` / `undecided` |
| status | `active` / `cancelled` |
| notice_days | days before renewal by which the vendor needs cancellation (default 0); the cancel-by date is renewal − notice_days |
| access_until | optional, set on confirmation of cancellation |
| category | free text, e.g. "AI and LLMs" (used in future breakdowns, not shown in v1 panel) |
| payment_label | free text such as "Visa ··21" or "PayPal"; never a card number (validated: no runs of 8+ digits) |
| notes | free text up to 2 KB |
| reminder_days | optional override list of lead days, e.g. `[14,3,1]` |
| deleted_at | soft delete |
| created_by, created_at, updated_at | created_by is the token name of the caller (see §9) |

**charges**: id, subscription_id, date, amount_minor, currency, aud_minor, fx_rate, fx_date, kind (`assumed` when the renewal date passed, `recorded` when the owner or an agent confirmed it). A "probably charged" flag is derived: an `assumed` charge on a subscription whose intent is `cancel`.

**notifications**: seq (autoincrement), subscription_id, step key, issued_at, summary, body, urgency, actions JSON, plus a unique `dedup_key` = subscription id + target date + step. That key makes issuing a reminder idempotent.

**calendar_links**: subscription_id, event_id, content_hash, synced_at.

**audit**: every mutation (actor, action, subscription id, before/after JSON). It's for debugging agent mistakes and for undo by hand.

**meta**: schema version, calendar id, last FX fetch, and the last success or error for each channel.

Migrations are numbered SQL files applied at start-up inside a transaction. The database is copied to `bitsub-YYYYMMDD.db` daily using SQLite's online backup (`VACUUM INTO`), keeping 14 copies.

## 5. Renewal maths

- Monthly, quarterly and yearly cycles keep the anchor's day of month and clamp it to each month's length: anchored on 31 Jan, the dates run 28 Feb (29 in leap years), 31 Mar, 30 Apr and so on. Each renewal is computed from the anchor as `anchor + k·interval`, never by adding to the previous renewal, so a clamp never sticks.
- Day and week cycles add days.
- `next_renewal` = the smallest anchor-derived date ≥ today. While a trial is running, the next event is `trial_end`.
- When a renewal date passes:
  - keep or undecided: an `assumed` charge is recorded at that date's FX rate and the next date is computed. Undecided items stay undecided.
  - cancel with no confirmation: an `assumed` charge is recorded, it's flagged "probably charged", the item stays in the list and the nagging continues.
- `confirm_cancelled` sets status to `cancelled` and records `access_until`. No future renewals are generated, and past charges stay in the history and the totals.
- Totals: per-month figure = Σ active, non-cancelling cost normalised to a month (year ÷ 12, week × 52 ÷ 12, day × 365 ÷ 12). The per-year figure is ×12. "Next 30 days" = Σ renewals falling in today+30 (cancelling items included, because the charge is still coming if the owner forgets). The 12-month graph = Σ `charges.aud_minor` per calendar month.

Prior art: bscott/subtrackr (AGPL-3.0) has the same anchor-clamp idea and a cancel-by date. This repo is MIT, so we reimplement it and copy no code.

## 6. Currency

- Display: AUD first, with the original in brackets when it isn't AUD, e.g. `A$31.00 (US$20.00)`, `A$9.70 (€5.49)`. Symbols: A$, US$, €, £, ¥, NZ$, C$. Anything else shows its ISO code.
- Rates: Frankfurter (`https://api.frankfurter.dev/v1/latest?base=AUD`), which publishes ECB reference rates with no key. Fetched once a day by `bitsubd`, and the last good set is cached in `meta`. If the rates are more than 7 days old, totals show `≈` and the footer says the rates are stale.
- A charge locks the rate of its date when it's recorded, so history never moves. Totals for future renewals use the current rate.
- Supported set: whatever Frankfurter returns, which includes AUD, USD, EUR, GBP, JPY, NZD and CAD. An agent passing an unsupported code gets a validation error.

## 7. Reminders

The service decides what's due on a schedule (every minute, cheap). It issues each step once via `dedup_key`, then delivers it on each channel.

Default cadence. An agent or the owner can override it per subscription with `reminder_days`.

| intent / kind | steps (days before target) | urgency |
|---|---|---|
| trial (any intent other than keep) | 7, 2, 1 | 2 and 1 are critical |
| undecided | 7, 2 | 2 is critical |
| cancel (not yet confirmed) | daily from 3 days before the cancel-by date, and daily after it until confirmed | critical from the cancel-by date |
| keep | 1 | low |

- Steps fire at 09:00 local time (a config value). After downtime, a step that was missed but whose target hasn't passed fires on start-up. A step whose target has passed is skipped, and the next relevant one fires.
- Wording example: summary "Speechify trial ends in 3 days", body "A$215.45/yr (US$139.00). Keep or cancel?".

**Channels**
- **sysc-shell notifications:** the service only queues them (the notifications table). Each plugin instance polls `/api/v1/snapshot?since=<seq>`, shows anything newer than the last seq it displayed, and saves that seq through the plugin `state` capability, so a restart doesn't repeat notifications. A plugin that was offline when a notification was issued shows it on reconnect only if it was issued in the last 12 hours and its subscription still needs attention. Both machines show the same notification, because each runs its own desktop. That's intended: the service issued it once, and no machine schedules its own. Notification actions: `keep`, `cancel` for undecided items and trials, `done` ("I've cancelled it") for items being cancelled. They need sysc-shell change A (§11). On older hosts, the notification goes out without buttons.
- **Email (Gmail through gog):** one email per critical step, plus a weekly digest every Monday at 09:00. The digest lists the next 14 days, anything awaiting a decision, anything being cancelled, the monthly and next-30-days totals, and any channel errors. Plain text. Sender and recipient are both the configured gog account unless config sets a `to`.
- **Calendar:** the alarms live on the mirrored events (§8), so the phone reminds the owner even when no desktop is on.

## 8. Google Calendar mirror (through gog)

- `bitsubd` shells out to `gog` (≥ v0.28) using `--json` and an explicit `--account`, with a 60 s timeout per call and no shell interpolation (argv only). The owner must first add the calendar scope to their gog login once; today it covers Drive and Gmail only.
- On first sync, the service creates a dedicated calendar named "Subscriptions" (`gog calendar create-calendar`) and stores its id in `meta`. It never writes to any other calendar.
- Desired state is one all-day event per active subscription, on its next renewal or trial end:
  - summary: `Speechify trial ends · A$215.45/yr (US$139.00)` or `Netflix renews · A$18.99/mo`. For items being cancelled: `Cancel Stan by Wed 10 Dec`.
  - description: intent, cancel-by date, payment label, vendor URL, and "Managed by bitsub, edits are overwritten".
  - reminders (`--reminder`, max 5): popup alarms matching the cadence steps, e.g. `popup:7d`, `popup:2d`, plus 09:00 the day before. All-day event offsets count back from midnight at the event's start; the builder confirms the exact offsets against Google's behaviour.
  - `--private-prop bitsub_id=<uuid>`.
- Reconcile loop: on every change and every 15 minutes, list the events (`--private-prop-filter` on the calendar), diff them against desired state using a content hash, then create, update or delete. Stray bitsub events (unknown id) get deleted, and events without the property are never touched. Failures are retried with backoff (1 m → 30 m cap). The last error goes into `meta` and the panel footer reads "Calendar out of date: <reason>".

## 9. Interfaces

One HTTP server in `bitsubd`, bound by default to `127.0.0.1` and archPC's Tailscale address. Binding a LAN address is an explicit config opt-in, because the LAN carries the bearer token in plain text and Tailscale encrypts it.

**Auth:** each client gets its own named bearer token (e.g. `archpc-plugin`, `laptop-plugin`, `claude-archpc`, `codex-laptop`), and only a hash of each token is kept in `~/.config/bitsub/bitsubd.json`. Tokens are compared in constant time. The token name becomes the `actor` in the audit log and `created_by`. A one-off command, `bitsubd token add <name>`, prints a new token once.

**Service layer:** a single set of operations used by both front doors, so behaviour and validation can't drift between them:
`list(filter)`, `get(id|name)`, `add`, `update`, `set_intent`, `confirm_cancelled(access_until?)`, `record_charge`, `upcoming(days)`, `totals`, `delete` (soft), `restore`.

**MCP** (`/mcp`, streamable HTTP, official Go SDK `github.com/modelcontextprotocol/go-sdk`, a new dependency justified by the MCP requirement). Tools:
| tool | purpose |
|---|---|
| `list_subscriptions` | filter by status/intent/text; returns AUD + original price, next date, intent |
| `get_subscription` | by id or exact/unique name match |
| `add_subscription` | name, amount, currency, cycle, interval, anchor_date or trial_end, intent (default undecided), optional url/category/payment_label/notes/notice_days |
| `update_subscription` | partial update; price changes are recorded in the audit log |
| `set_intent` | keep / cancel / undecided |
| `confirm_cancelled` | marks cancelled; optional access_until |
| `record_charge` | confirm an actual charge (amount may differ from list price) |
| `upcoming` | renewals and trial ends in the next N days (default 30) |
| `totals` | per month, per year, next 30 days, in AUD |
| `delete_subscription` | soft delete, for mistakes; not for cancellation |

`add_subscription` refuses a name that is a near-duplicate of an existing active subscription (case- and space-insensitive equality) unless `allow_duplicate` is set. Every tool validates its inputs at the boundary: currency code, amount range (0 < amount ≤ 100 000.00), dates within ±10 years, enum values, field lengths, and no card numbers. All tool results carry both the AUD and the original figures.

**Plugin JSON API** (`/api/v1`):
- `GET /snapshot?since=<seq>`: subscriptions in list order, totals, the 12-month series, channel status (calendar, email, FX), and notifications after `seq`.
- `POST /subscriptions/{id}/intent` `{intent}`.
- `POST /subscriptions/{id}/confirm-cancelled` `{access_until?}`.

**Client config** for the plugin and for agents' MCP entries: `~/.config/bitsub/client.json` `{ "url": "http://archpc.<tailnet>:7733", "token": "…" }`, mode 0600. The plugin reads it, so the token never lands in sysc-shell's settings. Agent MCP config example for Claude Code:
`claude mcp add --transport http bitsub http://<host>:7733/mcp --header "Authorization: Bearer <token>"`. Codex and opencode take the equivalent URL and header entries, which the skill documents.

## 10. sysc-shell plugin (`org.sysc.bitsub`)

Manifest capabilities: `notifications`, `panels`, `state`, `settings`. Its one setting is the client config path, defaulting to `~/.config/bitsub/client.json`. Panel width 758, matching Moonbit's. Height ≈ 600 (the builder fits it with `plugin/lint`). Placement attached.

**Bar** (a row, fixed node count so it doesn't jitter):
- nothing pending: icon + `A$342/mo`
- something awaiting a decision or being cancelled within 7 days: an alert dot + the most urgent item, e.g. `Speechify · 3d`. Below 120 px wide: icon + dot only.
- offline (laptop): icon in the subtle tone; the tooltip says since when.
- The icon comes from the shell's icon catalogue. If nothing fitting exists, adding one is part of change B's font work, otherwise `schedule` is the fallback.

**Panel**, design "C revised" (chosen in the mockups):
1. Header: `bitsub.ans` rendered centred in mono. With change B, it's drawn as coloured spans: ANSI 36 (cyan) → accent tone, 90 (dark grey) → subtle tone, and the 47 background on `▒▓` is ignored with the glyph drawn in accent. Without change B, every row is drawn in the accent colour (the Moonbit pattern). The `.ans` file is embedded and parsed at build or start time, and the parser is tested.
2. One scrolling list of active subscriptions, soonest target first. Each row has:
   - the name (bold) and a date line: "Trial ends Tue 2 Dec · in 3 days", "Renews Sun 14 Dec", or "Renews Wed 10 Dec · cancel before then". The date line is normal tone, turning orange-ish (accent or warning, whichever the theme offers) inside 7 days and error tone inside 2 days for undecided items and trials.
   - price `A$215.45/yr (US$139.00)`, with the bracketed part in subtle tone.
   - **Keep** and **Cancel** buttons. Undecided: both outlined. Keep: Keep filled. Cancel: Cancel filled in error tone. Clicking Keep or Cancel sets that intent, and clicking the filled one again sets undecided. Exception: clicking a filled Cancel opens an inline confirmation on the row, "Have you cancelled it? Yes · Not yet". Yes calls `confirm-cancelled`, and the row leaves the list.
   - No tags or chips.
3. Pinned at the bottom: three cards. "Per month" (with "A$x a year" under it), "Next 30 days" (with the renewal count) and "Charged per month · 12 months" as a `graph` node with the peak label.
4. Footer (subtle): calendar sync age, next digest, "served by archPC". An error on any channel replaces the relevant part in error tone.

**Offline (laptop or archPC service down):** the last snapshot is kept in plugin state and rendered with an "Offline: archPC hasn't answered since HH:MM (N min ago)" card above the list, and the Keep/Cancel buttons are rendered in the dimmed `soft` style and ignored.

**Polling:** every 30 s, at once when the panel opens, and straight after any action. Back off to 2 min while offline.

**Notifications:** relayed as described in §7. A click on an action button (change A) calls the same intent or confirm endpoints. A click on the notification body opens the panel, if change A provides a way to do that. Otherwise the click does nothing beyond the shell's default.

All views are validated with `v1.Validate` and linted with `plugin/lint` at the bar, tooltip and panel sizes in tests. That covers the offline state and an empty list, which shows "Nothing tracked yet. Ask an agent to add a subscription."

## 11. sysc-shell changes

Both are protocol additions with their own minor bumps (current `origin/main`: `ProtocolMinor = 28`), each in its own PR, following the repo's deploy rules.

**A. Plugin notification actions (minor 29)**
- Today `Registry.PluginNotify` (`internal/shell/notifications.go`) drops `NotifyParams.Actions` entirely. They're validated and then discarded, so plugin notifications never show buttons.
- Change: pass the actions through to the published toast, remember notification id → owning plugin instance (bounded, expiring with the notification), and when the shell's toast host invokes an action on a plugin-owned notification, deliver a new host→plugin message:
  `notify.action { id: uint32, key: string }`. `key` = `default` for a body click.
- Only plugins that negotiated ≥ 29 receive it. Older plugins keep today's behaviour.
- Tests: actions reach the toast; an invoke routes to the right plugin and only that plugin; ids from other producers never route to a plugin; an expired mapping is dropped.

**B. Coloured text spans (minor 30)**
- A new optional `spans` field on text nodes: `[{ text, tone, bold }]`, with tones from the existing set (normal, subtle, accent, error). When `spans` is set, `text` must be empty. The node lays out and measures exactly as if `text` were the concatenation, so lint and layout behave the same, and only the painter varies colour per run.
- Validation: bounded span count (≤ 64) and total length within the existing text bound.
- Hosts below 30 ignore the field, so plugins must send `text` instead when the negotiated minor is < 30 (bitsub does this).

## 12. The skill (`skills/bitsub/SKILL.md`)

A standard `SKILL.md`, so Claude Code, Codex and opencode can all load it. It covers:
- **When to use:** the owner mentions signing up, starting a trial, a price change, cancelling or a renewal; or an agent sees a signup or receipt while doing other work; or the owner asks what they're paying or what's renewing.
- **Setup:** how to add the MCP server in each harness (URL and header), with the token from the owner.
- **Recording rules:** search first (`list_subscriptions`) and update instead of adding. Always capture name, amount, currency, cycle, the first charge date (or trial end), and whether it's a trial. Record intent only when the owner stated it, otherwise leave it undecided and ask. Don't put card numbers in `payment_label`. Use `delete_subscription` only to fix mistakes, and `confirm_cancelled` only when the owner says they've cancelled.
- **Hard rule:** never try to cancel a subscription or log in to a vendor. The owner does that.
- **Reporting:** quote AUD with the original in brackets, matching the panel.

## 13. Deployment

- archPC: `bitsubd` installed to `~/.local/bin`, plus the systemd user unit `bitsubd.service` (`Restart=on-failure`). Config in `~/.config/bitsub/bitsubd.json` (listen addresses, gog account, email to, digest day and time, reminder hour), data in `~/.local/share/bitsub/`. First run: `bitsubd token add …` for each client, then the gog calendar re-auth (the owner runs it).
- Both machines: the plugin goes through the usual `~/sysc-plugins-deploy/<rev>` symlink procedure and is added to `plugins.enabled`, which needs a shell restart. The owner is asked first.
- The laptop also needs `client.json` pointing at archPC's Tailscale name, and MCP entries in its harnesses.
- Shell changes A and B are deployed with `scripts/deploy` before the plugin depends on them. The plugin works without them, through the fallbacks.

## 14. Testing

- `subs`: table tests for renewal maths (month-end clamps, leap years, intervals, trials), normalisation and totals, and money formatting.
- `server`: store migrations and round-trips; the reminder engine against a fake clock (cadence, idempotency, catch-up after downtime, cancel nagging, "probably charged"); calendar reconcile against a fake gog runner (create, update, delete, strays, failure backoff); email composition; FX caching and staleness; HTTP auth (missing, wrong and revoked tokens); MCP tool validation and results through the SDK's in-memory transport.
- `plugin`: view trees validated and linted at every size, for the normal, empty, offline, confirm-inline and many-items states; the notification seq and dedup logic; the ANSI parser.
- sysc-shell: the tests listed under A and B.
- The Go command caps hook applies. Use `-p 2`, and run race tests per package, not across `./...`.

## 15. Repo conventions to respect

- The commit-msg hook rejects messages containing words such as "agent", "bot", "claude", "codex", "llm" and "gpt-N", and AI co-author trailers. Commit messages must avoid them, e.g. "MCP tools" and "skill", not "agent tools". This owner rule outranks generic attribution guidance.
- Fresh worktrees need the beads `BEADS_DB` workaround to commit.
- Add the plugin to the `Makefile` `PLUGINS` list and the README table. The validation `go run ./tools/validate-manifests` must pass.

## 16. Decided by the builder (my calls, flagged for review)

- SQLite through the existing `modernc.org/sqlite` dependency, not a JSON file.
- Frankfurter as the FX source.
- Per-client named tokens, and Tailscale plus localhost as the default binding (LAN opt-in).
- The cadence table, 09:00 send time, Monday digest, and the 12-hour catch-up window for offline plugins.
- Laptop read-only while offline: changes are disabled, not queued.
- Confirming a cancellation by clicking the filled Cancel again, then answering an inline "Have you cancelled it?".
- Port 7733, the panel height and the exact file paths.

## 17. Left to the builder

The exact MCP SDK version, the module layout inside `server/`, the SQL schema details, the gog JSON parsing, the reminder offsets for all-day events, icon choice, and the visual polish within the chosen layout. All of these stay within the decisions above.

## Immediate note

The SuperGrok trial must be cancelled before 8 Dec 2026, and bitsub won't be running by then. The owner should put that one in their calendar by hand.
