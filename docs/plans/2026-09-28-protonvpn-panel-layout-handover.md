# ProtonVPN plugin — panel layout handover

Status: the bar click and the node-count rejection are fixed; the panel is
still rejected by the shell's layout engine. This document hands the remaining
work to the next session.

## What this PR contains

- `cmd/sysc-plugin-protonvpn/main.go` — the bar click now opens the panel:
  - `panel.open` passes `v1.PanelParams{Entry: "panel", Output, Generation, Instance}`
    (was `nil`, which the shell rejected with `panel "" is not declared`).
  - A primary click arrives twice (press → `EventPointer`, release →
    `EventActivate`); the panel now opens on `EventActivate` only, so the pair
    no longer toggles it straight closed. Secondary click still quick-connects.
- `plugins/protonvpn/bar.go` — the bar pill and the panel header use the
  `proton` glyph for every phase; the tone carries the phase.
- `plugins/protonvpn/connections.go` — the connections tab is paginated:
  20 countries per page, 20 servers per expanded country, at most 3
  auto-expanded countries, with a pager row. This keeps the view under
  `v1.MaxNodes` (1024).
- `go.mod` — pins sysc-shell `v0.0.0-20260927145008-3936e7da2013`, the
  `feat/proton-glyph` commit that adds the `proton` glyph (0xE075).

Tests: `go test -count=1 ./plugins/protonvpn/ ./cmd/sysc-plugin-protonvpn/`
passes, including `TestConnectionsStaysUnderNodeLimit` (149 countries × 40
servers) and `TestConnectionsPagerPagesTheList`.

## Shell-side dependency

The `proton` glyph lives on sysc-shell branch **`feat/proton-glyph`**
(`3936e7d`, pushed, one commit on top of `origin/main`). It adds
`svg/proton.svg` (both paths of the official mark, unioned), `uniE075` in
`build.py`, `iconProton = iconGitHubUnread + 1` in `iconfont.go`, the
`fontmap.go` face route, and `TestProtonGlyphIsTheMark`. **Not yet merged to
sysc-shell `main`.**

## The remaining failure

The panel view now passes validation but the shell's layout engine rejects it.
From `journalctl --user -u sysc-shell.service`:

```
WARN plugin view rejected plugin=org.sysc.protonvpn view=panel revision=1
  err="ui: child 2: ui: child 2: ui: row at root.children[2].children[2].children[0]:
  child 2 of kind 3 (button at root.children[2].children[2].children[0].children[2])
  does not fit in 420x36"
```

Path: `root.children[2]` = `tabBody` = `ConnectionsTree` root;
`.children[2]` = the country `KindList`; `.children[0]` = the first
`countryRow`; `.children[2]` = its `expandButton` (28×28).

## Root cause (confirmed by arithmetic)

`PinEnd` is documented as a **two-child** feature
(`internal/ui/tree.go:373`: "right-pins the last child of a two-child row to
the row's inner right edge"). `countryRow` and `serverRow` set `PinEnd: true`
on rows with 3–4 children.

In `internal/ui/layout.go:117`, a zero-width `KindColumn` takes the whole
remaining width whenever the row is `PinEnd`:

```go
case KindColumn:
    if child.Width <= 0 && (i == len(root.Children)-1 || root.PinEnd) {
        w = remain
    }
```

`countryRow` is `Padding: 8, Height: 52` → content box 420×36. The lead
column (child 1, `Width: 0`) takes all 388 remaining pixels, so the
`expandButton` (child 2) starts at x = content.X + 428 and overflows the
420-wide box → `fitError`. `serverRow` has the same shape (icon, lead column,
connect button) and will fail the same way once the country row is fixed.

The plugin lint does not catch this: `internal/ui/check.go` mirrors Layout's
loop but only implements the two-child `PinEnd` reservation, and its
`KindColumn` case explicitly hands the column the row's content box.

## Suggested fix

Restructure `countryRow` and `serverRow` to the documented two-child `PinEnd`
shape, the way the shell's own `bluetoothbody.go:195` does it:

- `countryRow`: `[leading, trailing]` where `leading` is a row/column holding
  `flagGlyph` + the name/meta column, and `trailing` is a row holding
  `expandButton` + `connectButton`.
- `serverRow`: `[leading, connectButton]` where `leading` holds the `dns`
  icon + the name/meta column.

Alternatively drop `PinEnd` and give the lead column an explicit `Width`, but
the two-child shape matches the shell contract and keeps the controls
right-aligned.

## Reproduction

Laptop `192.168.0.64:7777` (user `nomadx`), shell deployed from
`feat/proton-glyph` via `scripts/deploy --host laptop --force …`; plugin
binary at `~/sysc-plugins-main/plugins/protonvpn/bin/`. Click the pill at
(848, 24) with `ydotool` (`YDOTOOL_SOCKET=/tmp/.ydotool_socket`, left button
`0x00`), then read the journal. The error popup under the bar shows the same
message.

## Still open from the previous handover

- Daemon-side connect failure: `protonvpn connect` logs `CONN.CONNECT:START`
  then reverts to Disconnected ~1.5 s later; no ERROR in the CLI log. Needs
  `sudo journalctl -u proton.VPN.service` around a connect.
- `org.sysc.weather` fails with `read plugin.hello: EOF` (pre-existing).
