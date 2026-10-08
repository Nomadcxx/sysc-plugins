# Plugin catalog thumbnails

Status: design approved 2026-10-08; spec awaiting review.

## Goal

Every plugin in this repository ships a uniform, sysc-branded 960×540
thumbnail that shows as its preview in the sysc-shell plugin catalog. A Go
tool generates it from data the plugin already carries plus one real
screenshot. CI refuses a plugin without a valid, current thumbnail, and a
release refuses to publish one.

The reference is Noctalia's community-plugins repo: a hosted generator page
exports a 960×540 `thumbnail.webp`, and their CI rejects a plugin without
one. We keep the idea (one fixed size, one look, enforced) with a sysc look
and a Go-only, reproducible generator instead of a hosted page.

## Decisions already made

| Question | Decision |
|---|---|
| Format | WebP, 960×540, lossless |
| Language | Go only. No headless browser, no Python, no ImageMagick at build or CI time |
| Visual design | "B1": terminal frame with ASCII details (mock below) |
| Version in the image | None. The plugin id is shown instead, so a version bump never makes a thumbnail stale |
| Raw capture | Committed beside each plugin as `screenshot.png`, so the thumbnail is reproducible |
| Encoder | `github.com/HugoSmits86/nativewebp` v1.3.0 (MIT, depends only on `x/image`) |
| Shell | PR Nomadcxx/sysc-shell#152 registers the WebP decoder in the icons worker. No other shell change |

Design mock: https://claude.ai/artifact/Mgoa76RLuFJjhpTB9AuSG6 (artboard
"B1 · Frame + ASCII details"; private to the author).

## Verified facts this design rests on

- The shell reads `entry.screenshot` (`url`, `sha256`) from `catalog.json`,
  fetches it, checks the sha256, caps it at 2 MiB (`maxPluginScreenshotBytes`),
  and draws it on store cards and the detail view at exactly 16:9. A 960×540
  image fills that box with no crop.
- The shell's image worker (`internal/icons/worker.go`) registered PNG and
  JPEG only. WebP was registered in `internal/wallpaper/depthmask.go`, so a
  WebP preview worked only because that package was linked in. #152 fixes it
  where the decoding happens and adds a golden test.
- The media cache is named by sha256 with no extension and the decoder sniffs
  content, so nothing else gates on file type.
- `tools/catalog update` does not fill `screenshot` today. It copies it from
  `catalog-meta.json`, and no plugin has one. It does pin each plugin README by
  tag URL and sha256 (`readPluginReadme`).
- `catalog validate -community` already requires a screenshot on every row.
- Neither the standard library nor `golang.org/x/image` can encode WebP.
  `nativewebp` round-trips: its output for a B-direction mock was 94 KB and
  decoded correctly with `x/image/webp` and ImageMagick.
- Plugin manifests carry `id`, `name`, `description`, and `widgets` and
  `panels` arrays. `moonbit` has no `services`; all sixteen have `panels`.

## Components

### 1. `tools/thumbnail` — the generator

```
go run ./tools/thumbnail -plugin plugins/notes          # write thumbnail.webp
go run ./tools/thumbnail -check                         # every plugin, no writes
go run ./tools/thumbnail -check -plugin plugins/notes
```

Inputs, per plugin directory `plugins/<dir>/`:

| Input | Use |
|---|---|
| `manifest.json` | `id` (title bar), `name` (title), `description`, and whether `widgets` / `panels` are non-empty (chips) |
| `catalog-meta.json` (repo root, keyed by `id`) | `category` (chip) |
| `screenshot.png` | the real captured panel or bar crop |

Output: `plugins/<dir>/thumbnail.webp`.

Rendering rules (B1), all measurements in output pixels:

- Canvas 960×540. Background `#0d1019` with a bottom radial blue glow and a
  `+` crosshair grid (24 px cells, 12 px mono, `rgba(31,139,255,0.20)`).
- Frame inset 24 px, radius 18, 2 px `#2c354f` border, fill
  `rgba(9,11,18,0.82)`.
- Title bar 58 px high with a 2 px rule beneath: left
  `┌─ sysc://plugins/<dir>` (`┌─ plugins/<dir>` when the directory name is too
  long to clear the wordmark, as `github-notifications` is), centre the blue
  sysc wordmark (116×16) flanked by `//////`, right `<plugin id> ─┐`.
- Left column: `// PLUGIN` in mono blue; the plugin name in Inter ExtraBold
  54 px (46 px on two lines) with a blue `█` cursor; a `│` gutter beside the
  description in Inter Regular 21/29; chips `[ <CATEGORY> ]`, `[ PANEL ]`,
  `[ BAR ]` (the last two only when the manifest has panels / widgets).
- Right column: the screenshot, 320 px wide, in a 2 px blue border with a
  blue glow and `┌ ┐` corner marks above it.
- Footer, bottom left: `░▒▓█▓▒░  sysc-shell · plugin catalog`.
- The slashes are drawn as strokes, not italic glyphs, so no italic face is
  embedded.

Fit rules:

- Name: wrap to at most two lines; fail with a clear error if it does not fit.
- Description: at most three lines; a longer one ends in `…` and the tool
  prints a warning naming the plugin.
- Screenshot at 320 px wide: when the scaled height reaches the space under
  the title bar it bleeds off the bottom with the last 22% faded. When it is
  shorter it is centred vertically with a full border and no fade. The source is
  never upscaled past 2×.

Determinism: the same inputs always yield the same bytes. No timestamps or
random state; lossless output; fixed encoder version. This is what makes
`-check` meaningful.

`-check` renders in memory and compares bytes with the committed
`thumbnail.webp`, reporting the plugin and which file differs. It exits
non-zero on any mismatch or missing file.

Embedded assets (under `tools/thumbnail/assets/`, via `go:embed`):

- the sysc wordmark mask (copied from sysc-shell's `sysc-mark.png`, 930×128
  grayscale, white = shape), tinted at render time;
- Inter ExtraBold and Inter Regular, and JetBrains Mono Bold, as static TTFs
  subset to the glyphs used (ASCII plus the box-drawing and block characters
  in the layout). Both families are SIL OFL 1.1; their notices go in
  `ATTRIBUTION.md`.

The layout code draws with `image`, `x/image/draw` and `x/image/font/opentype`
only: signed-distance rounded rects, blurred glows, and masked gradients. The
throwaway mock used the same approach and proved it covers the effects needed.

### 2. Screenshot capture

`plugins/<dir>/screenshot.png` is a real capture: the panel at its normal
size, or for a plugin that only has a bar widget, a crop of the bar. Captures
are made on the laptop through the shell Registry harness, not by clicking,
because synthetic clicks are not available there. The documented procedure
lives in `docs/publishing.md`. A representative state is required: real-looking
data, not an empty or loading view.

`tools/thumbnail` rejects a `screenshot.png` that is not a PNG, is under
320 px wide, or is over 4 MiB.

### 3. Catalog wiring (`tools/catalog`)

- `update` pins `plugins/<dir>/thumbnail.webp` at the release tag by raw URL and
  sha256, exactly as `readPluginReadme` does for the README, and writes it to
  the row's `screenshot`. A tagged tree without a thumbnail fails the update;
  no row is written without one. The exception is a tag older than the row's
  newest release (a backport): it only joins the row's `releases` and never
  supplies the screenshot, so it needs no thumbnail.
- `catalog-meta.json`'s `screenshot` field stays accepted as an override but is
  no longer how a thumbnail is normally recorded.
- A new subcommand, `thumbnails -ref <commit>`, pins `screenshot` on existing
  rows to a thumbnail at an immutable commit URL. It is used once to backfill
  the plugins whose releases were tagged before thumbnails existed.
- `validate -community` is unchanged and still requires a screenshot on every
  row. `validate -fetch` already downloads and hashes it.
- `catalog package` (`collectEntries`) ships everything in a plugin directory
  except `*.go`, `testdata/` and `bin/`, so it would put `screenshot.png` and
  `thumbnail.webp` into every install archive. Both root files are excluded:
  they are catalog media, not part of the install.

### 4. Enforcement in CI

`tools/validate-manifests` (run by `ci.yml`) gains a thumbnail check per
`plugins/<dir>`:

- `thumbnail.webp` exists;
- it is a WebP (RIFF/WEBP header) of exactly 960×540 and at most 512 KB;
- `screenshot.png` exists;

and `ci.yml` runs `go run ./tools/thumbnail -check`, so a thumbnail that no
longer matches its manifest, category or screenshot fails the build. The
failure message names the command that regenerates it.

The release workflow already runs `catalog validate -fetch` against the freshly
published release, which covers the pinned URL and hash.

### 5. Documentation

- `docs/publishing.md`: replace the "screenshot is optional for this
  repository's own catalog" paragraph with the new requirement, the capture
  steps, and the `thumbnail` command; remove the now-wrong "any aspect ratio,
  1920×1080" guidance for this repo's own screenshots.
- `docs/writing-plugins.md`: add `screenshot.png` and `thumbnail.webp` to the
  required files and the new-plugin checklist.
- `README.md`: note the thumbnail in the contribution steps.
- `ATTRIBUTION.md`: Inter, JetBrains Mono, the wordmark and `nativewebp`.

### 6. Backfill

The sixteen existing plugins (`aiusage`, `calendar`, `cat`, `faith`, `games`,
`github-notifications`, `kdeconnect`, `mini-docker`, `moonbit`, `notes`,
`protonvpn`, `screen-recorder`, `screenshot`, `timer`, `wallpaper-depth`,
`world-clock`) each get a captured `screenshot.png`, a generated
`thumbnail.webp`, and their catalog row pinned with
`catalog thumbnails -ref <commit>`. `moonbit` is source-only (no catalog
release) so it gets the files but no row.

## Delivery order

1. sysc-shell #152 (WebP decode) merged and deployed to the laptop, so a
   WebP preview renders in the store.
2. `tools/thumbnail` with its golden tests, then the CI check, then the
   catalog changes and docs. One PR, because the CI check would otherwise fail
   without the files it requires.
3. Capture and backfill on the laptop; then the `catalog thumbnails` row pin.
   A separate PR, because it needs the laptop and real captures.

To keep CI green between the two PRs, `tools/validate-manifests` exempts the
plugin directories named in `tools/thumbnail/grandfathered.txt`, one directory
name per line. The first PR creates that file listing the sixteen existing
plugins. Every plugin not in it, which means every new one, must have a valid
`screenshot.png` and `thumbnail.webp` from its first PR. The backfill PR removes
each plugin's line as it adds that plugin's files, and the validator fails if a
listed plugin already has a thumbnail, so the list can only shrink and ends
empty (then the file and the exemption code are deleted). Until a plugin leaves
the list, `catalog update` still refuses to release it, so a grandfathered
plugin cannot ship a new version without a thumbnail.

Alternative considered and rejected: make the first PR carry all sixteen
captures. It couples tooling review to capture work that needs a physical
device.

## Testing

- `tools/thumbnail`: golden-image test with a fixed manifest, meta and
  screenshot fixture; a determinism test (two renders are byte-identical);
  fit tests (two-line name, over-long description, tall and short screenshots);
  a round-trip test that the output decodes as 960×540 with `x/image/webp` (the
  shell's decoder); a size test under the 512 KB cap with the heaviest fixture.
- `tools/validate-manifests`: table tests for missing, wrong-size, non-WebP and
  oversized thumbnails.
- `tools/catalog`: tests that `update` pins the tagged thumbnail and fails
  without one, and that `thumbnails -ref` rewrites only `screenshot`.

## Out of scope

- Per-category accent colours (one blue for the whole catalog).
- Animated or multi-image previews.
- Changing the shell's store layout or its 2 MiB cap.
- Third-party sources' thumbnails (they keep the existing optional
  `screenshot` field).

## Open risks

- **Fonts.** The subset TTFs must cover every glyph the layout uses; the golden
  test fails if one renders as a missing-glyph box. The shape of Inter's glyphs
  at 54 px in Go's rasteriser (no hinting) differs slightly from a browser, so
  the output will look close to the mock, not identical.
- **Encoder.** `nativewebp` is a single-maintainer library. The output is plain
  lossless VP8L and decodes in three independent decoders, and if it ever
  stalls the generator can switch encoders without changing the file format.
- **Staleness churn.** `-check` makes any change to a plugin's `name`,
  `description`, category or screenshot require a regenerated thumbnail. That is
  the intent, and the failure message gives the one command to run.
