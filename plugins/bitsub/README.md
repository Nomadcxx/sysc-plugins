# bitsub

Subscriptions, renewals and trials: keep or cancel before you're charged.

bitsub has three parts:

- **`bitsubd`**, an always-on service on archPC. It holds the list (SQLite), issues reminders, mirrors renewals to a
  dedicated Google Calendar and sends mail through [`gog`](https://github.com/steipete/gogcli), fetches daily exchange
  rates, and serves MCP tools (`/mcp`) and the plugin's JSON API (`/api/v1`).
- **`sysc-plugin-bitsub`**, the sysc-shell bar item and panel. It is a thin client of `bitsubd` on archPC and on the
  laptop.
- **[The skill](../../skills/bitsub/SKILL.md)**, which teaches MCP clients how to record subscriptions.

bitsub never cancels anything with a vendor. It records what you intend and reminds you until you confirm you've
cancelled.

## Setup

### 1. Install the service (archPC)

```sh
make bitsubd
install -m 755 plugins/bitsub/bin/bitsubd ~/.local/bin/bitsubd
install -Dm 644 packaging/systemd/bitsubd.service ~/.config/systemd/user/bitsubd.service
```

Write `~/.config/bitsub/bitsubd.json` with mode `0600` (bitsubd refuses a file group or others can read):

```json
{
  "listen": ["127.0.0.1:7733", "100.x.y.z:7733"],
  "gog_account": "you@gmail.com",
  "email_to": "",
  "reminder_hour": 9,
  "digest_weekday": "Monday",
  "calendar_name": "Subscriptions"
}
```

- `listen` takes loopback and Tailscale (`100.64.0.0/10`) addresses. Add `"allow_lan": true` to bind a LAN address,
  but the LAN would carry bearer tokens in plain text. `0.0.0.0` is refused.
- An empty `gog_account` turns calendar and mail off; the panel footer then says "Google not configured".
- An empty `email_to` mails the gog account itself.
- Data lives in `~/.local/share/bitsub/` (`bitsub.db` plus 14 daily backups in `backups/`).

### 2. Tokens

Each client gets its own named token. The name is recorded as the actor in the audit log.

```sh
bitsubd token add archpc-plugin
bitsubd token add laptop-plugin
bitsubd token add claude-archpc     # one per MCP client
bitsubd token list
bitsubd token revoke codex-laptop
```

`token add` prints the token once; only its hash is stored. Restart `bitsubd` after adding or revoking tokens.

### 3. Google Calendar scope

gog needs the calendar scope once (it opens a browser):

```sh
gog auth add you@gmail.com --services drive,gmail,calendar --force-consent
```

On first sync bitsubd creates a calendar named "Subscriptions" and only ever writes there. Edits made to its events in
Google Calendar are overwritten.

### 4. Start it

```sh
systemctl --user daemon-reload
systemctl --user enable --now bitsubd
curl -s -H "Authorization: Bearer <archpc-plugin token>" 127.0.0.1:7733/api/v1/snapshot
```

### 5. The plugin (archPC and laptop)

Write `~/.config/bitsub/client.json` with mode `0600`:

```json
{ "url": "http://127.0.0.1:7733", "token": "<archpc-plugin token>" }
```

On the laptop, use archPC's Tailscale name in `url` and the `laptop-plugin` token. Then install the plugin as usual
and add `bitsub` to `plugins.enabled`.

### 6. MCP clients

Claude Code:

```sh
claude mcp add --transport http bitsub http://<host>:7733/mcp --header "Authorization: Bearer <token>"
```

See [the skill](../../skills/bitsub/SKILL.md) for Codex and opencode, and for how clients should record
subscriptions.
