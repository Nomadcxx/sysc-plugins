# Plugin view UI rules

A plugin view is a declarative tree ([`plugin/v1`](https://pkg.go.dev/github.com/Nomadcxx/sysc-shell/plugin/v1)).
The host validates it, converts it, and lays it out — and **a tree that
validates can still be refused**, because validation is geometry-blind. A
refused view is replaced on the user's screen by a failure card (Close /
Retry / Disable) and the rejection is written to the shell's journal.

This document is the geometry contract. `plugin/lint` enforces every rule in
it, and your tests should run it.

## Check your views

```go
import (
    shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
    v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

for _, f := range shelllint.Tree(panel, v1.ViewPanel, panelW, panelH) {
    t.Errorf("panel: %s", f)
}
```

`lint.Tree(root, view, width, height)` returns one finding per violation — not
just the first, which is all the host's own layout will ever report. It runs
the host's real pipeline (`v1.Validate → Convert → layout`) with the host's
own text metric, so its verdict is the host's verdict.

Sizes to check at:

| View | Box |
|---|---|
| bar | `plugin/lint.BarWidth` × `plugin/lint.BarHeight` (240×32) |
| tooltip | `plugin/lint.TooltipWidth` × `plugin/lint.TooltipHeight` (280×200) |
| panel | the `width`/`height` your `manifest.json` declares |

A panel with `"include_settings": true` is wrapped by the host in a card
inside a scroll, so its tree is laid out in a box *smaller* than the declared
panel. Linting at the declared box is optimistic for those panels: it can miss
a rejection, never invent one.

## The rules

**Height includes padding.** A node's content box is `Height − 2×Padding`. A
row of `Height 28` with `Padding 8` has twelve pixels of content, and a text
child measures sixteen:

```
row Height 28, Padding 8  →  content 274×12   (in a 290-wide list pane)
text "Avg 70%"            →  measures 48×16   →  the row is refused
```

The same row with `Height 42` has twenty-six pixels of content and fits. The
rule of thumb: `Height ≥ 2×Padding + the tallest thing inside`.

**A row's children must fit its content width too.** Text is clipped at the
row's edge (it can be squeezed), but a control — button, capsule, segmented
control, menu, drag source — that overruns the content box refuses the whole
row. Give rows with mixed content explicit widths, or set `PinEnd` on a
two-child row to reserve the trailing child's width before the leading text is
clipped.

**A column never refuses.** A child taller or wider than its column truncates
or overflows in silence. Nothing will tell you; budget the width yourself.

**A capsule fills its row's content height.** A 26×26 monogram disc inside a
row therefore needs the row's content height to be 26 — `Height 42` with
`Padding 8` — or the disc stretches or squashes.

**Text measures `len(bytes)×8` wide and `16` tall**, whatever the role, the
weight, or the size token. That is deliberately crude — the host lays a view
out at its logical size before a real face is resolved — so do not expect
captions to measure narrower than titles.

**Root kinds are fixed per view**: a bar's root is a row; a panel's root is a
column (or a list); a tooltip's root is a column. The converter refuses
anything else, and `v1.Validate` will not warn you.

## Why it is worth a test

The host's layout stops at the first rejection, so a view with two geometry
bugs reveals them one deploy at a time. A view that is refused takes the whole
panel down, not just the offending row: the user sees an error card instead of
your UI. Both have happened here. `lint.Tree` reports every violation in one
run, in a test, with no shell and no desktop involved.
