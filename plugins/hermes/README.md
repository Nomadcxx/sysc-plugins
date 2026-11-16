# Hermes Agent

Hermes Agent puts Hermes status, chat, approvals, sessions and models in the
sysc-shell bar. It talks to a Hermes bridge over HTTP, either one it starts on
this machine or one running somewhere else.

![Hermes Agent panel](screenshot.png)

> **Status:** in progress. The plugin connects in both modes, and the bar,
> tooltip, summary, menu, chat panel, pinned panel and settings views are
> drawn from the live bridge.

## Local mode

The default. The plugin starts the bundled bridge script with **Python
command** and **Hermes command**, reads the bridge state file, and can start
the Hermes gateway when it is not running. It stops only the bridge process it
started.

Needs `python3` and `hermes` on this machine.

## Client-only mode

Turn on **Client-only mode (remote bridge)** to connect to a bridge on another
machine. Set **Bridge host** and **Bridge port** to the forwarded address and
paste the token that `hermes-bridge-serve.sh` printed on the server, or point
**Bridge token file** at a file holding it (mode `0600`). In this mode the
plugin never starts or signals a process.

Bridge traffic is plain HTTP. Across an untrusted network, forward the port
over SSH; **SSH target** fills in the command for the **Copy SSH command**
button. The token is never logged or shown.

## Approvals

A pending approval shows Deny, Allow and Allow Session. Each button carries a
signature of the request that was on screen when the card was drawn. On a
click the plugin compares that signature with the approval it has cached,
fetches `/state` once more and compares it again before it posts the choice.
If either comparison fails the click is dropped and the card says
`The approval changed; check the card again`.

The plugin always sends the clicked approval's `request_id` when the state
carried one. The bundled bridge answers a POST that does not match its
`request_id` with 409 `approval_mismatch`; the plugin shows the same message
and refetches the state. With the bundled bridge no approval race remains.

A stock bridge without `request_id` does not give that guarantee. Between the
plugin's last `/state` and its POST, Hermes can queue a second approval, and
the stock bridge resolves the oldest queued one, which may not be the one on
screen. The window is one round trip. Run the bundled bridge on the remote
side too when approvals matter.

## One-shot questions

Cancelling a one-shot question ends only the plugin's request. The bridge
keeps running that `hermes` process until it finishes or hits the bridge's own
600 s timeout.

## Launcher

The `/hermes` rows are a user-config launcher provider, not a plugin
surface: add this to `~/.config/sysc-shell/config.json` (keeping the rest of
your config). The shell applies the file when you save it; if it refuses the
file the bar stays as it was and Settings shows the reason.

```json
{
  "launcher": {
    "providers": [
      {
        "name": "Hermes",
        "prefix": "/hermes",
        "glyph": "ai-usage",
        "description": "Ask Hermes, open the panel or start a session",
        "command": ["sysc-plugin-hermes", "launcher-list", "{query}"],
        "on_select": ["sysc-plugin-hermes", "launcher-select", "{selection}", "{query}"]
      }
    ]
  }
}
```

Type `/hermes` and a prompt, then pick **Ask Hermes**: the plugin runs a
one-shot and opens the chat panel. The other rows open the panel, start a new
session, and resume the latest session. **Resume latest** needs a session the
plugin has already seen.

`sysc-plugin-hermes` must be on the shell's `PATH`; otherwise use the full
path to the plugin's `bin/sysc-plugin-hermes` in both argv lists. The prompt
travels to the plugin in `$XDG_RUNTIME_DIR/sysc-hermes/ask` (mode `0600`, one
pending item, at most 1 MB), because a service action carries no payload. The
plugin reads and deletes it when it runs `launcher-ask`.

## Attribution

See [ATTRIBUTION.md](../../ATTRIBUTION.md).
