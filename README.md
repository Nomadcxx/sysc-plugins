# sysc-plugins

Official plugin repository for [sysc-shell](https://github.com/Nomadcxx/sysc-shell),
the Go-first native Wayland desktop shell for Niri.

Each top-level directory under `plugins/` is a complete sysc-shell plugin:
a `manifest.json` plus the source of a `bin/` executable that speaks the
shell's plugin wire protocol. The protocol types come from
[sysc-shell](https://github.com/Nomadcxx/sysc-shell)'s `plugin/v1` package,
consumed as a normal Go module dependency (the shell's tree carries no
Windows-reserved paths since the `aux_surface.go` rename, so the proxy
serves it).

## Building

```sh
make build      # builds every plugin binary into plugins/<dir>/bin/
make test       # go test -race ./...
make validate   # structural check of every manifest.json
make install    # build + symlink each plugin into $XDG_CONFIG_HOME/sysc-shell/plugins
```

After `make install`, enable plugins from sysc-shell's Plugins manager panel
(open the panel popout, or `sysc-shell ipc panel plugins`).

## Plugins

| Plugin | ID | Status | Origin |
|---|---|---|---|
| Screen Recorder | `org.sysc.screen-recorder` | first sweep | extracted from sysc-shell |
| Notes | `org.sysc.notes` | first sweep | extracted from sysc-shell |
| Timer | `org.sysc.timer` | UI rebuild started | extracted from sysc-shell + noctalia deltas |
| World Clock | `org.sysc.world-clock` | 2.0.0 redesign | city search, labels, bar modes; requires sysc-shell commit `f77226a` or later; see docs/plans/2026-09-25-world-clock-design.md |
| Calendar | `org.sysc.calendar` | first sweep | port of the built-in clock-panel calendar |
| GitHub Notifications | `org.sysc.github-notifications` | 0.2.0 | port of noctalia community plugin |
| Mini Docker | `org.sysc.mini-docker` | 0.2.0 | port of noctalia community plugin |
| Wallpaper Depth | `org.sysc.wallpaper-depth` | 1.0.0 | port of noctalia official plugin |
| Phone Connect | `org.sysc.kdeconnect` | 0.1.0 skeleton | port of DMS DankKDEConnect |
| AI Usage | `org.sysc.aiusage` | 0.1.0 | new; patterns ported from noctalia ai-usagebar + DMS usage widgets |

"First sweep" means working but early: these plugins were generated before
much of the shell's plugin infrastructure existed, and their views are
visually extremely basic. The plan is to refactor them and rebuild their UIs
against the noctalia plugins as prior art (layout, density, interaction
patterns), now that iteration happens in this repo. The 0.1.0 ports are
skeletons by design. Wallpaper Depth 1.0.0 tracks active image wallpapers and
supplies depth masks for the shell's centred clock.

World Clock 2.0.0 stores zones as objects. Version 1.2.0 cannot read that state; its next save overwrites the zone list.

The protocol grew for this work: plugin/v1 minor 1 adds `subtle` and
`accent` text tones (muted foreground and theme accent) and the host now
honours node Height, so views can carry text hierarchy and fixed-height
rows. The timer is the first rebuild; the rest follow its patterns.

## Adding a plugin

1. `mkdir plugins/<name>` and write its `manifest.json` (see any existing
   plugin; `go run ./tools/validate-manifests` enforces the schema).
2. Implement the plugin as a library package in `plugins/<name>/` and a
   `main.go` under `cmd/sysc-plugin-<name>/` that handshakes via
   `plugin/v1`'s `Client` and serves its views.
3. Add the plugin to `PLUGINS` in the `Makefile` and to the table above.
4. Validate every view tree with `v1.Validate(...)` in a test — the host
   rejects invalid trees at render time. Validation is geometry-blind, so lay
   every view out with `plugin/lint` at the sizes the host uses: see
   [docs/plugin-ui-rules.md](docs/plugin-ui-rules.md).

Notes the hard way taught us:

- Icon names must come from the shell's catalogue (`render.IconNames()` in
  sysc-shell: weather, battery, camera, record, notifications, close,
  schedule, ghost, sysmon gauges). They are `[a-z0-9-]` identifiers — no
  underscores — and an unknown name fails the host's conversion. Additions
  need a sysc-shell font update.
- Only `normal` and `error` tones exist; interactive nodes need
  `ID`, `Name`, `Role`, and a non-empty `Events` list; buttons cannot carry
  children (put the icon on the button itself).
- The wire vocabulary has no grid or desktop-widget view kind; compose grids
  from rows/columns, and note that noctalia desktop widgets are not portable.

## Attribution

Plugins marked "port of noctalia ..." are Go rewrites of behavior originally
implemented by [noctalia-dev](https://github.com/noctalia-dev) for Noctalia v5
(`plugin.toml` + Luau), under the MIT license. sysc-shell does not claim
runtime compatibility with Noctalia; only behavior is ported. Plugins marked
"port of DMS ..." are Go rewrites of behavior originally implemented by
Avenge Media for DankMaterialShell's DankKDEConnect plugin
(dms-plugin-registry #386), under the MIT license; runtime compatibility
with DMS is not claimed or preserved.

AI Usage is a new plugin built from the patterns in its prior-art research
(docs/plans/2026-09-19-aiusage-research.md), including the owner's own
noctalia ai-usagebar plugin. Its codex session-file collector reads session
logs only — never authentication files, never the network — and pasted API
keys live in the host's own settings store and are sent only to their
provider's API.
