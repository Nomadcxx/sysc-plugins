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
| Screen Recorder | `org.sysc.screen-recorder` | stable | extracted from sysc-shell |
| Notes | `org.sysc.notes` | stable | extracted from sysc-shell |
| Timer | `org.sysc.timer` | stable | extracted from sysc-shell + noctalia deltas |
| World Clock | `org.sysc.world-clock` | stable | extracted from sysc-shell + noctalia deltas |
| Calendar | `org.sysc.calendar` | 0.1.0 | port of the built-in clock-panel calendar |
| GitHub Notifications | `org.sysc.github-notifications` | 0.1.0 | port of noctalia community plugin |
| Mini Docker | `org.sysc.mini-docker` | 0.1.0 | port of noctalia community plugin |
| Wallpaper Depth | `org.sysc.wallpaper-depth` | 0.1.0 stub | port of noctalia official plugin |

## Adding a plugin

1. `mkdir plugins/<name>` and write its `manifest.json` (see any existing
   plugin; `go run ./tools/validate-manifests` enforces the schema).
2. Implement the plugin as a library package in `plugins/<name>/` and a
   `main.go` under `cmd/sysc-plugin-<name>/` that handshakes via
   `internal/wire`'s `Client` and serves its views.
3. Add the plugin to `PLUGINS` in the `Makefile` and to the table above.

## Attribution

Plugins marked "port of noctalia ..." are Go rewrites of behavior originally
implemented by [noctalia-dev](https://github.com/noctalia-dev) for Noctalia v5
(`plugin.toml` + Luau), under the MIT license. sysc-shell does not claim
runtime compatibility with Noctalia; only behavior is ported.
