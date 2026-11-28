---
name: bitsub
description: Record and query the owner's subscriptions, free trials and renewals in bitsub over MCP. Use when the owner signs up for something, starts a trial, mentions a price change, wants to keep or cancel a subscription, says they have cancelled one, asks what is renewing or what they pay per month, or when you see a signup or receipt while doing other work.
---

# bitsub

bitsub is the owner's subscription tracker. It runs as `bitsubd` on archPC and exposes MCP tools at
`http://<host>:7733/mcp`. It reminds the owner before trials end and renewals charge, on the desktop, the
phone (Google Calendar) and by email. Your job is to keep its list accurate.

## Hard rule

**Never try to cancel a subscription, log in to a vendor, or change anything with a vendor.** The owner does
all cancelling. bitsub only records what they intend and what they have done. `set_intent` with `cancel` means
"remind the owner to cancel", nothing more.

## When to use

- The owner signs up, starts a free trial, or upgrades or downgrades a plan.
- The owner mentions a price change, a new renewal date, or a charge they saw.
- The owner says "keep X", "cancel X", or "I've cancelled X".
- The owner asks what is renewing soon, what a subscription costs, or what they pay in total.
- While doing other work you see a signup confirmation, trial notice or receipt. Offer to record it; don't
  record it silently.

## Setup

Each client gets its own token from the owner (`bitsubd token add <name>` on archPC prints it once). Use a name
that says which client and machine, e.g. `claude-archpc` or `codex-laptop`; it is recorded as the actor on every
change. `<host>` is `127.0.0.1` on archPC and archPC's Tailscale name from the laptop.

Claude Code (`claude mcp add --help`, Claude Code 2.1):

```sh
claude mcp add --transport http bitsub http://<host>:7733/mcp --header "Authorization: Bearer <token>"
```

Codex (`codex mcp add --help`, codex-cli 0.172; https://developers.openai.com/codex/mcp). The token is read
from an environment variable:

```sh
export BITSUB_TOKEN=<token>
codex mcp add bitsub --url http://<host>:7733/mcp --bearer-token-env-var BITSUB_TOKEN
```

or in `~/.codex/config.toml`:

```toml
[mcp_servers.bitsub]
url = "http://<host>:7733/mcp"
bearer_token_env_var = "BITSUB_TOKEN"
```

opencode (V2 config, https://opencode.ai/docs/mcp-servers/; `opencode mcp add --help`, opencode 2.3), in
`opencode.json`:

```jsonc
{
  "mcp": {
    "servers": {
      "bitsub": {
        "type": "remote",
        "url": "http://<host>:7733/mcp",
        "oauth": false,
        "headers": { "Authorization": "Bearer {env:BITSUB_TOKEN}" }
      }
    }
  }
}
```

## Recording rules

1. **Search first.** Call `list_subscriptions` (use `text` with the vendor name) before adding anything. If it
   exists, use `update_subscription` instead. `add_subscription` refuses a name that already exists; only pass
   `allow_duplicate: true` when the owner really has two separate subscriptions with the same name.
2. **Required fields:** `name`, `amount`, `currency`, `cycle`, and either `anchor_date` or `trial_end`.
   - `amount` is a decimal string in the original currency, e.g. `"20"` or `"139.00"`. Never convert it to AUD
     yourself; bitsub does that.
   - `currency` is the ISO code the vendor charges in (`USD`, `EUR`, `AUD`, ...).
   - `cycle` is `day`, `week`, `month` or `year`; `interval` 3 with `month` is quarterly.
   - `anchor_date` is the first real charge date (`YYYY-MM-DD`).
3. **Trials:** set `trial_end` to the last day of the trial and the price that applies afterwards. Leave
   `anchor_date` out; the first charge is assumed on `trial_end`.
4. **Intent:** set `intent` only when the owner said it. Otherwise leave it out (it defaults to `undecided`)
   and ask them whether they want to keep it. bitsub keeps reminding them until they decide.
5. **Never record a card number.** `payment_label` is for something like `Visa ··21` or `PayPal`. bitsub rejects
   anything with eight or more digits in a row.
6. **Notice periods:** if the vendor needs notice before renewal, set `notice_days`; reminders then target the
   cancel-by date.
7. `delete_subscription` is only for entries recorded by mistake. A cancelled subscription is recorded with
   `confirm_cancelled`, which keeps its charge history.
8. `confirm_cancelled` only after the owner says they have cancelled. Pass `access_until` if they mention when
   access ends.
9. `record_charge` when the owner reports an actual charge, especially when the amount differs from the list
   price.

## Reporting

Quote prices the way the panel shows them: AUD first, the original in brackets when it isn't AUD, with the
cycle after the AUD figure. Use the `price_display` field from tool results as it is:

- `A$31.01/mo (US$20.00)`
- `A$18.99/mo`

`totals` gives the monthly and yearly spend for subscriptions being kept, plus everything due in the next 30
days. If `approx` is true, say the AUD figures are approximate (an exchange rate is missing or stale).

## Tools

| Tool | Use |
|---|---|
| `list_subscriptions` | `status` (`active` or `cancelled`), `intent`, `text`; soonest first |
| `get_subscription` | `id_or_name` |
| `add_subscription` | see the recording rules |
| `update_subscription` | `id` plus only the fields that change; `clear_trial_end` removes a trial |
| `set_intent` | `id`, `intent` (`keep`, `cancel`, `undecided`) |
| `confirm_cancelled` | `id`, optional `access_until` |
| `record_charge` | `id`, `date`, optional `amount` and `currency` |
| `upcoming` | `days` (default 30) |
| `totals` | no arguments |
| `delete_subscription` | `id`; mistakes only |

A tool error names the field to fix, or the existing entry for a duplicate. Correct the input and retry; don't
work around it.

## Examples

**"I just started a Grok trial, it ends 7 Dec, US$30/month after."**
`list_subscriptions` with `text: "grok"` finds nothing, so:
`add_subscription {name: "SuperGrok", amount: "30", currency: "USD", cycle: "month", trial_end: "2026-12-07"}`.
Then ask: "Do you want to keep it after the trial? I've left it undecided, so bitsub will remind you before 7
Dec."

**"Cancel Speechify."**
Find it with `list_subscriptions {text: "speechify"}`, then `set_intent {id, intent: "cancel"}`. Tell the owner:
"Marked to cancel. You'll need to cancel it on Speechify's site yourself before <cancel_by>; bitsub will remind
you daily until you confirm." Do not open the vendor's site or attempt the cancellation.

**"I've cancelled Stan."**
`confirm_cancelled {id}` (with `access_until` if they said when access ends). Reminders stop and the history
stays.

**"What's renewing this week?"**
`upcoming {days: 7}`, then list each as `Name — Renews Thu 10 Dec — A$17.00/mo`, flagging any with
`intent: "undecided"` or `probably_charged: true`.
