# Automated plugin panel captures

Status: design approved 2026-10-09; spec awaiting review.

## Goal

Produce every plugin's `screenshot.png` (the input to its catalog thumbnail)
with one command, from fixture data, through the shell's real layout and
painter, so the 16 existing plugins can be backfilled and any later UI change
refreshes its capture instead of leaving a stale picture.

This is the capture half of the thumbnail requirement in
`2026-10-08-plugin-thumbnails-design.md` (merged as sysc-plugins#142). The
backfill that spec describes assumed hand captures on the laptop; this replaces
them.

## Decisions already made

| Question | Decision |
|---|---|
| How a panel becomes pixels | The shell's real pipeline (convert, layout, paint, system fonts, shell theme) behind a new **public** package in sysc-shell, `plugin/preview`, following the precedent of `plugin/lint` |
| Where scenes live | One `capture_test.go` beside each plugin's code, so scenes can reach unexported state and `cmd/` packages (Games), nothing ships in plugin binaries, and CI skips them |
| When a capture runs | Only when `CAPTURE=1`; a normal `go test` skips it |
| Data | Fictional, always. The repository is public: no real notifications, notes, containers, devices or accounts |
| Scenes per plugin | One representative panel state each |
| Capture size | The panel size the plugin's own `manifest.json` declares, at the shell's display scale (default 150%) |
| Where it runs | This desktop. No pointer events are involved, so the laptop's missing mouse device does not matter |
| Plugin binaries | Not run. Scenes call each plugin's view builder with a fixture state |

## Verified facts this design rests on

- Every one of the 16 plugins builds its panel as a pure function from a state
  value to a `*v1.Node`, e.g. `github-notifications.PanelTreeForState`,
  `mini-docker.PanelTreeForSession`, `world-clock.Panel`, `timer.PanelTree`.
  Games' builder lives in `cmd/sysc-plugin-games/view.go` (package `main`),
  which is why scenes are tests inside their own packages.
- All 16 manifests declare at least one panel with a width and height, from
  360×480 (timer) to 760×540 (notes).
- The shell already exposes a public wrapper over its internals: `plugin/lint`
  imports `internal/plugin` and `internal/ui` and applies the host's converter
  and layout to a `*v1.Node`. `plugin/preview` is the same kind of package.
- The host paints a panel in `internal/shell/panelhost.go` (`render`):
  `render.NewCanvas` → `ui.Layout` → `render.Paint(canvas, root, textRenderer,
  style)`, with a style from the theme (`rootStyle`), a scale
  (`ui.Scale120`) and the surface body rect. The Moonbit audit harness
  (`docs/plans/moonbit-audit-2026-10-02/render-harness.go.txt`) proved the
  pipeline produces faithful PNGs, using theme `eldritch` dark at scale 150.
- A tree the host cannot lay out is refused (`"text_field does not fit in
  392x40"`), while the plugin's own fit test, which measures text with an
  estimate, can still pass. Rendering through the real layout therefore also
  catches views that would fail on a real display.
- sysc-plugins pins sysc-shell as a normal Go module dependency
  (`github.com/Nomadcxx/sysc-shell`, currently a pseudo-version from
  2026-10-05), so a new shell package needs a dependency bump to be usable.

## Components

### 1. sysc-shell: `plugin/preview`

```go
// Render lays out and paints a plugin panel tree exactly as the host would,
// and returns the pixels.
func Render(root *v1.Node, width, height int, opts Options) (*image.NRGBA, error)

type Options struct {
    Scale120 int    // display scale in 120ths; 0 means 180 (150%)
    Theme    string // named palette; empty means the shell's default dark theme
    Font     string // font family; empty means the shell default
}
```

- It validates (`v1.Validate`), converts (`plugin.Convert(root, v1.ViewPanel)`),
  lays out at `width`×`height` logical pixels and paints at `Scale120`, using
  system fonts through the shell's own font map. Layout refusals are returned
  as errors in the host's wording.
- It draws the panel surface (root fill and corner radius) so the result looks
  like a shell panel, not a bare tree on transparency. The surface treatment
  mirrors the host's detached (non-attached) panel.
- It is stateless: no animations, no hover or focus state, no running plugin.
- The package comment says it is for tooling and documentation images, and that
  output depends on installed fonts.
- Tests: a small fixed tree renders at the requested size; the same tree is
  refused when it does not fit (reusing a `plugin/lint` fixture); a text-heavy
  tree is not blank.

This is its own sysc-shell PR. It does not touch any host behaviour.

### 2. sysc-plugins: the capture helper

`internal/capture` (tests only import it) provides:

```go
// Panel renders tree at the size plugins/<dir>/manifest.json declares for its
// first panel and writes plugins/<dir>/screenshot.png. It does nothing unless
// CAPTURE=1.
func Panel(t testing.TB, dir string, tree *v1.Node)
```

- It finds the repository root from the test's working directory, reads the
  manifest for the panel size, calls `preview.Render`, and writes a PNG.
- Without `CAPTURE=1` it calls `t.Skip`, so the scene tests never run by
  accident.
- It also checks the tree with `plugin/lint` first, so a scene that cannot lay
  out fails with the host's own wording and the offending node path.
- A scene that needs images (cover art, device photos, note art) builds its
  image files in a temp directory; `internal/capture` offers a small helper to
  write a synthetic PNG there. No third-party artwork is committed.

### 3. Scenes: `capture_test.go` per plugin

Each of the 16 plugins gets one test, `TestCapturePanel`, that builds a
believable fictional state and calls `capture.Panel`. Content rules:

- Invented names, titles and numbers only; no real accounts, hosts, paths,
  device names, repository names or message text.
- A busy, representative state: a list with several rows, one selected or
  active item where the panel has one, values in the middle of their range. Not
  empty, not loading, not an error.
- Use the plugin's real builder and real state types; never hand-built wire
  nodes, so the capture changes when the UI changes.
- The Games scene lives in `cmd/sysc-plugin-games`; Moonbit's scene feeds the
  review-phase state; Phone Connect's feeds a paired-phone state.

### 4. Command

`make captures` runs `CAPTURE=1 go test -count=1 -p 2 -run TestCapturePanel` over
the plugin and `cmd` packages, one package at a time (this machine caps Go
parallelism), then `go run ./tools/thumbnail` for every plugin. A single plugin
can be refreshed with `make capture PLUGIN=<dir>`.

## Delivery order

1. **sysc-shell PR:** `plugin/preview` with tests.
2. **sysc-plugins PR (tooling + scenes):** bump the sysc-shell dependency to the
   merged revision, add `internal/capture`, the 16 scenes and the Makefile
   targets, and document the workflow in `docs/publishing.md`. No generated
   images are committed here, so this PR is reviewable as code.
3. **sysc-plugins PR (backfill):** run `make captures`, commit each plugin's
   `screenshot.png` and `thumbnail.webp`, remove each plugin's line from
   `tools/thumbnail/grandfathered.txt`, then pin the rows with
   `go run ./tools/catalog thumbnails -ref <commit>` as a follow-up once the
   backfill is on `main`. Because the validator forbids a grandfathered plugin
   from having the files, files and list removal land together in this PR.

## Testing

- `plugin/preview`: size, refusal, non-blank, and a determinism test on one
  machine (two renders of one tree are identical).
- `internal/capture`: skip without `CAPTURE=1`; with it, writes a PNG of the
  manifest's declared size times the scale; fails on a tree `plugin/lint`
  rejects, with the node path in the message. Tested against a fixture manifest
  and a trivial tree.
- Each scene: the existing `go test` run skips it. Running `make captures` is
  the test; a scene that fails to lay out fails the run. After the backfill PR,
  the thumbnail `-check` in CI covers the screenshots' downstream cards.

## Out of scope

- Animated or interactive captures, hover or focus states, second scenes for
  plugins with distinct modes (the thumbnail uses one image; add scenes later if
  wanted).
- Bar-widget captures: all 16 plugins have a panel, and the thumbnail design
  prefers the panel.
- Reproducible bytes across machines. Captures depend on installed fonts;
  they are committed inputs, and only the thumbnails generated from them are
  byte-checked.
- Capturing from a running plugin process.

## Open risks

- **Wide panels shrink.** The thumbnail shows the capture 316 px wide, so
  Notes (760) and Moonbit (758) appear at about 0.4× and small text is not
  legible there. This is inherent to the approved card layout; if it reads
  poorly the fix is a tighter scene or a different crop, decided when the cards
  are seen.
- **Image nodes.** Plugins whose panels show pictures (Games covers, Phone
  Connect device mockups and photos, Notes art, Wallpaper Depth previews) need
  decodable files at the paths their state names; the helper builds synthetic
  ones. If a builder reads a path the scene cannot control, that scene needs a
  small state seam; found during the work, not designed here.
- **Theme and surface fidelity.** The preview draws a detached panel surface
  with the default dark theme; if the shell's real panels differ visibly (edge
  joints, backdrop), the capture will not match a screenshot of a live shell.
  Acceptable for a catalog card; noted so nobody expects pixel identity.
- **Shell coupling.** `plugin/preview` reaches several internal packages. A
  future shell refactor that moves them must keep it working; its own tests will
  fail loudly if it breaks.
