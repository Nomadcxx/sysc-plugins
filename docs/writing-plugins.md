# Writing a plugin

How to add a plugin to this repository. Releasing it is covered in
[publishing.md](publishing.md), and the layout rules views must follow are in
[plugin-ui-rules.md](plugin-ui-rules.md).

Each directory under `plugins/` is a complete sysc-shell plugin: a
`manifest.json` plus the source of a `bin/` executable that speaks the
shell's plugin wire protocol. The protocol types come from sysc-shell's
`plugin/v1` package, consumed as a normal Go module dependency.

## Adding a plugin

1. `mkdir plugins/<name>` and write its `manifest.json` (see any existing
   plugin; `go run ./tools/validate-manifests` enforces the schema).
2. Implement the plugin as a library package in `plugins/<name>/` and a
   `main.go` under `cmd/sysc-plugin-<name>/` that handshakes via
   `plugin/v1`'s `Client` and serves its views.
3. Add the plugin to `PLUGINS` in the `Makefile` and to the table in the README.
4. Validate every view tree with `v1.Validate(...)` in a test — the host
   rejects invalid trees at render time. Validation is geometry-blind, so lay
   every view out with `plugin/lint` at the sizes the host uses: see
   [plugin-ui-rules.md](plugin-ui-rules.md).

Notes the hard way taught us:

- Icon names must come from the shell's catalogue (`render.IconNames()` in
  sysc-shell: weather, battery, camera, record, notifications, close,
  schedule, ghost, sysmon gauges). They are `[a-z0-9-]` identifiers — no
  underscores — and an unknown name fails the host's conversion. A small
  Material subset also exists under underscore names (`download`, `refresh`,
  `restart_alt`, `upload`, `lock`, ...); matching entries from
  `render.MaterialIconNames()` work too. Additions need a sysc-shell font
  update.
- Text tones are `normal`, `error`, `subtle` and `accent` (the last two
  from plugin/v1 minor 1); interactive nodes need
  `ID`, `Name`, `Role`, and a non-empty `Events` list; buttons cannot carry
  children (put the icon on the button itself).
- The wire vocabulary has no grid or desktop-widget view kind; compose grids
  from rows/columns, and note that noctalia desktop widgets are not portable.

## Protocol history

The protocol grew for this work: plugin/v1 minor 1 adds `subtle` and
`accent` text tones (muted foreground and theme accent) and the host now
honours node Height, so views can carry text hierarchy and fixed-height
rows. The timer is the first rebuild; the rest follow its patterns.
