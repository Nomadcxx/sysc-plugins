# ProtonVPN plugin — panel layout handover

Status: fixed. The bar click opens the panel, the connections tab stays inside
the node budget, and the layout-engine rejection described below is resolved —
the panel view now lays out cleanly at 460×580 and at the standalone tab width.
This document keeps the analysis for the record.

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
- `go.mod` — pins sysc-shell
  `v0.0.0-20260927162619-c4eaac21be31`, the merged `main` commit that adds
  the Proton glyph (U+E075).

Tests: `go test -count=1 ./plugins/protonvpn/ ./cmd/sysc-plugin-protonvpn/`
passes, including `TestConnectionsStaysUnderNodeLimit` (149 countries × 40
servers), `TestConnectionsPagerPagesTheList`, and
`TestPinEndRowsHonorTheTwoChildContract`.

## Shell-side dependency

sysc-shell merged the Proton glyph to `main` as `c4eaac2` (U+E075). The
commit adds `svg/proton.svg`, registers the icon in `build.py` and
`iconfont.go`, routes the rune through `fontmap.go`, updates the generated
font, and tests it with `TestProtonGlyphIsTheMark`.

## The failure that was fixed

The panel view passed validation but the shell's layout engine rejected it, so
no panel opened. From `journalctl --user -u sysc-shell.service`:

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

## The fix applied

`countryRow` and `serverRow` were restructured to the documented two-child
`PinEnd` shape, the way the shell's own `bluetoothbody.go:195` does it:

- `countryRow`: `[leading, trailing]` where `leading` is a row holding the
  `flagGlyph` plus the name/meta column, and `trailing` is a row holding
  `expandButton` + `connectButton`.
- `serverRow`: `[leading, connectButton]` where `leading` holds the `dns`
  icon plus the name/meta column.
- `pagerRow`: previously a three-child row whose `PinEnd` was inert; it is
  now `[prev + count, next]` so the page stepper really right-pins.

Node IDs are unchanged (`country:XX`, `expand:XX`, `connect:XX`,
`server:XX`, `server-connect:XX`, `page:prev`, `page:next`), so the event
handlers in `cmd/sysc-plugin-protonvpn/main.go` still match.

`TestPinEndRowsHonorTheTwoChildContract` walks every view the plugin can
publish (all three tabs plus bar and tooltip in every phase) and fails on any
`PinEnd` row that is not exactly two children — the lint mirror cannot catch
this class, so the contract is asserted plugin-side.

Verified against the real engine, not just lint: a scratch test in a
`feat/proton-glyph` worktree drives the shell's internal `ui.LayoutColumn`
over `Panel()` at 460×580 and every tab body standalone at the 436px content
width, with worst-case long names and full pagination. Pre-fix it reproduced
the journal error above verbatim; post-fix all cases lay out clean.

## Verifying on the target machine

Deploy the shell and plugin through the documented workflow. Activate the
Proton bar pill and confirm the panel opens without a `plugin view rejected`
journal entry.

## Still open from the previous handover

- Daemon-side connect failure: `protonvpn connect` logs `CONN.CONNECT:START`
  then reverts to Disconnected ~1.5 s later; no ERROR in the CLI log. Needs
  `sudo journalctl -u proton.VPN.service` around a connect.
- `org.sysc.weather` fails with `read plugin.hello: EOF` (pre-existing).
