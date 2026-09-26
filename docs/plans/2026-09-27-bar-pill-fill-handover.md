# Handover — bar pill fills (calendar, cat) + mini-docker wrap-up

Date: 2026-09-27. Repo: `/home/nomadx/sysc-plugins` (branch `main`, HEAD `d5ebd7c`).
Shell repo: `/home/nomadx/sysc-shell`. Laptop: `ssh -p 7777 nomadx@192.168.0.64`.

## What the user asked for (verbatim)

1. m0414: *"Let's remove this fill:'soft" stadoum issue from the calander element on the bar too. and from the cat plugin."*
2. Earlier, still open: *"when complete lets run a hallmark audit on final product"* (skill: `/home/nomadx/.config/opencode/skills/hallmark/SKILL.md`, verb `audit` = read target, score against anti-patterns, ranked punch list, **do not edit**).
3. Earlier: mini-docker *"should be ready to merge"* — commit not yet made (no explicit commit ask yet).

## Root cause (already diagnosed — do not re-investigate)

Every bar widget is wrapped by the shell in a capsule (`internal/shell/widget.go` `capsuled()` → `ui.KindCapsule`, fill = `style.Capsule`, sampled `srgb(38,43,46)`). Built-in bar items (clipboard, wifi, sound, bell, phone) are **non-button** nodes, so the capsule is the only fill.

Plugin bar elements are `KindButton`s (required: only `KindButton` may declare `EventActivate`). `paintButton` (`internal/render/paint.go:1418`) paints base `style.containerHighest()` (`srgb(49,53,57)`) when the button has no fill, or the named fill otherwise. So every plugin bar button paints a second, lighter pill on top of the capsule:

- docker whale: 17px inner (subtle, user accepted)
- cat: 50px inner (very visible — user's complaint)
- calendar: `Fill:"soft"` = `wash(Accent, Capsule)` — a clearly lighter pill (user's complaint)

**Fix:** set `Fill: "card"` on the bar button. `internal/plugin/view.go:256` maps `"card"` → `ui.FillContainerHigh` → `style.Capsule` (`internal/render/paint.go:1116`), i.e. the button paints the exact capsule colour and the fill disappears. `"card"` is the only v1 fill name that resolves to the capsule level (`surface` → `FillNone` → still `containerHighest`; `outline` → transparent but paints a 1px boundary stroke; `chip` → `containerHighest`). `wireFillKinds` (`internal/plugin/view.go:215`) allows fills on `KindButton`, and `knownFills` (`plugin/v1/node.go:381`) accepts `"card"`.

## Change 1 — calendar bar element

File: `plugins/calendar/view.go`, `BarTree` (lines 25–43). Both branches carry `Fill: "soft", Shape: "stadium", Padding: 5`:

- no-event branch (line ~31)
- event branch (line ~41)

Replace `Fill: "soft", Shape: "stadium", Padding: 5` with `Fill: "card"` in both. Keep `Padding: 5` if the pill width should stay as-is (padding is not part of the complaint); `Shape` is a no-op for buttons (`chromeRadius(0)` already returns a stadium). Add a short comment: the fill resolves to the capsule level so the pill matches the bar's bare glyph items.

No calendar test asserts the fill/shape/padding (checked `plugins/calendar/*_test.go`).

## Change 2 — cat bar element

File: `plugins/cat/view.go`, `BarTree` (lines ~85–101). The button has **no** fill today; its visible pill is the `containerHighest` inner. Add `Fill: "card"` to the `open` button (keep `Height`, `Padding: barInset`, `Gap`, `Children`). No cat test asserts the fill.

## Change 3 (optional, ask the user) — docker whale

`plugins/mini-docker/view.go` `BarTree` has the same subtle 17px inner. The user accepted it, so leave it unless they ask; `Fill: "card"` would make it fully uniform with the built-ins.

## Verify + deploy

```
cd /home/nomadx/sysc-plugins
gofmt -l plugins/calendar plugins/cat
go test -count=1 ./plugins/calendar/... ./plugins/cat/...
go vet ./plugins/calendar/... ./plugins/cat/...
```

Deploy to the laptop (plugin dirs are symlinked from `~/.config/sysc-shell/plugins/` to `/home/nomadx/sysc-main-test/sysc-plugins`):

```
scp -P 7777 plugins/calendar/view.go nomadx@192.168.0.64:/home/nomadx/sysc-main-test/sysc-plugins/plugins/calendar/view.go
scp -P 7777 plugins/cat/view.go      nomadx@192.168.0.64:/home/nomadx/sysc-main-test/sysc-plugins/plugins/cat/view.go
ssh -p 7777 nomadx@192.168.0.64 'cd /home/nomadx/sysc-main-test/sysc-plugins && go build -trimpath -o plugins/calendar/bin/sysc-plugin-calendar ./cmd/sysc-plugin-calendar && go build -trimpath -o plugins/cat/bin/sysc-plugin-cat ./cmd/sysc-plugin-cat && systemctl --user restart sysc-shell'
```

Screenshot check (grim needs the Wayland env over SSH):

```
ssh -p 7777 nomadx@192.168.0.64 'export XDG_RUNTIME_DIR=/run/user/1000; export WAYLAND_DISPLAY=wayland-1; grim /tmp/bar5.png'
```

Sample pixels with `magick /tmp/bar5.png -crop 1x1+X+22 +repage txt:- | tail -1` (the `-format "%[pixel:p{...}]"` form returns bogus values). Expected: calendar and cat chips uniform `srgb(38,43,46)` like clipboard/wifi.

## Still open (not part of the two edits)

- **Hallmark audit** on the finished mini-docker UI (bar pill + panel). Load the skill, run the `audit` verb, produce a ranked punch list, do not edit.
- **Commit** the uncommitted mini-docker work in `sysc-plugins`: gate-test fix (`tests/integration/plugin_mini_docker_gate_test.go`), icon-only bar pill (`plugins/mini-docker/view.go`, `manifest.json`, `mini_docker_test.go`, `live_test.go`, `cmd/sysc-plugin-mini-docker/main.go`, `main_test.go`), SDK pin (`go.mod`/`go.sum` → `github.com/Nomadcxx/sysc-shell v0.0.0-20260926155753-7b5d65f93755`). Wait for an explicit ask.
- **Real-docker live gate** deferred: the laptop's docker daemon is stopped (user will reboot). `plugins/mini-docker/live_test.go` (build tag `live`) is read-only and safe to run once docker is up.
- **Do not touch** the unrelated in-flight `github-notifications` changes in the working tree (`cmd/sysc-plugin-github-notifications/`, `plugins/github-notifications/`) — not ours.

## Reference state

- Shell glyph: commit `7b5d65f` "feat(icons): add the Docker whale to the project font" on branch `feat/docker-glyph` (pushed). Laptop shell binary is a main+glyph build (`~/.local/bin/sysc-shell`, backup `sysc-shell.before-docker-glyph-*`).
- Laptop config: `~/.config/sysc-shell/config.json` has the mini-docker bar item after calendar; plugin process runs; journal clean.
- Mini-docker bar pill is icon-only (`Icon:"docker"`, no Text/Fill/Shape/Padding) and verified pixel-identical to the built-in glyph items.
