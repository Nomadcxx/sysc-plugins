# Hermes Agent

Hermes Agent puts Hermes status, chat, approvals, sessions and models in the
sysc-shell bar. It talks to a Hermes bridge over HTTP, either one it starts on
this machine or one running somewhere else.

![Hermes Agent panel](screenshot.png)

> **Status:** scaffold. The plugin loads and every view shows
> **Connecting…**; the bridge client and the real views are not in yet.

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

## Attribution

See [ATTRIBUTION.md](../../ATTRIBUTION.md).
