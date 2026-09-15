# sysc-plugins

Official plugin repository for [sysc-shell](https://github.com/Nomadcxx/sysc-shell),
the Go-first native Wayland desktop shell for Niri.

Each top-level directory under `plugins/` is a complete sysc-shell plugin:
a `manifest.json` plus the source of a `bin/` executable that speaks the
shell's plugin wire protocol (protocol v1). The protocol types are vendored
under `internal/wire/` — see `internal/wire/README.md` for why.

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
| Timer | `org.sysc.timer` | first sweep | extracted from sysc-shell + noctalia deltas |
| World Clock | `org.sysc.world-clock` | first sweep | extracted from sysc-shell + noctalia deltas |
| Calendar | `org.sysc.calendar` | first sweep | port of the built-in clock-panel calendar |
| GitHub Notifications | `org.sysc.github-notifications` | 0.1.0 | port of noctalia community plugin |
| Mini Docker | `org.sysc.mini-docker` | 0.1.0 | port of noctalia community plugin |
| Wallpaper Depth | `org.sysc.wallpaper-depth` | 0.1.0 stub | port of noctalia official plugin |

"First sweep" means working but early: these plugins were generated before
much of the shell's plugin infrastructure existed, and their views are
visually extremely basic. The plan is to refactor them and rebuild their UIs
against the noctalia plugins as prior art (layout, density, interaction
patterns), now that iteration happens in this repo. The 0.1.0 ports are
skeletons by design; wallpaper-depth is blocked on a shell wallpaper API.

## Adding a plugin

1. `mkdir plugins/<name>` and write its `manifest.json` (see any existing
   plugin; `go run ./tools/validate-manifests` enforces the schema).
2. Implement the plugin as a library package in `plugins/<name>/` and a
   `main.go` under `cmd/sysc-plugin-<name>/` that handshakes via
   `internal/wire`'s `Client` and serves its views.
3. Add the plugin to `PLUGINS` in the `Makefile` and to the table above.
4. Validate every view tree with `wire.Validate(...)` in a test — the host
   rejects invalid trees at render time.

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
runtime compatibility with Noctalia; only behavior is ported.
