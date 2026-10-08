# Plugin catalog thumbnails Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every plugin in this repo ships a generated, sysc-branded 960×540 WebP thumbnail that CI requires and the catalog pins, so it shows in the sysc-shell plugin store.

**Architecture:** A pure-Go renderer (`internal/thumbnail`) draws the approved "B1" terminal-frame layout from a plugin's manifest, its catalog category and a committed `screenshot.png`, and encodes it as lossless WebP. A CLI (`tools/thumbnail`) writes or checks the files; `tools/validate-manifests` enforces them in CI; `tools/catalog` pins the tagged thumbnail into each release's catalog row and backfills existing rows.

**Tech Stack:** Go 1.26, `golang.org/x/image` (draw, font/opentype, webp), `github.com/HugoSmits86/nativewebp` v1.3.0 (MIT, lossless WebP encoder), embedded Inter and JetBrains Mono subsets.

**Spec:** `docs/superpowers/specs/2026-10-08-plugin-thumbnails-design.md`. Visual reference: the "B1 · Frame + ASCII details" artboard at https://claude.ai/artifact/Mgoa76RLuFJjhpTB9AuSG6.

**Scope of this plan:** delivery step 2 of the spec (tooling, CI, catalog, docs) as one PR. Step 1 (sysc-shell WebP decoding) is merged (Nomadcxx/sysc-shell#152, `ee5049b5`). Step 3 (laptop captures and backfill) is a separate PR and a separate plan.

## Global Constraints

- Thumbnail: WebP, lossless, exactly **960×540**, at most **512 KB** (`512 << 10` bytes), file name `thumbnail.webp` in `plugins/<dir>/`.
- Raw capture: `plugins/<dir>/screenshot.png`, a PNG, at least **320 px wide**, at most **4 MiB**.
- **Go only.** No headless browser, no Python, no ImageMagick in any tool, test or CI step. (`pyftsubset` is used once, by hand, to make the committed font subsets; it is not part of any build.)
- No version number in the image. The title bar shows the plugin id. Image content depends only on `id`, `name`, `description`, whether `widgets`/`panels` are non-empty, the category, the directory name and the screenshot.
- Output is deterministic: same inputs, same bytes. No timestamps or randomness.
- One dependency added: `github.com/HugoSmits86/nativewebp` v1.3.0. Nothing else new in `go.mod` besides what `go mod tidy` pulls for it.
- Go commands must be capped on this machine: use `go test -count=1 -p 2 <package>` one package at a time; never `go test ./...` or `go vet ./...` uncapped (a hook blocks it and the machine has locked up twice).
- Commit messages must contain **no** AI/assistant attribution (the repo's commit-msg hook rejects it). The bd pre-commit hook fails in fresh worktrees; commit with a scratch DB: `cp /home/nomadx/sysc-plugins/.beads/beads.db "$SCRATCH/beads.db" && sqlite3 "$SCRATCH/beads.db" 'delete from dirty_issues;'` then `BEADS_DB="$SCRATCH/beads.db" git commit ...`.
- Work in a worktree off the spec branch: `git worktree add -b feat/plugin-thumbnails ~/worktrees/sysc-plugins-thumb-impl docs/plugin-thumbnails-spec`. All paths below are relative to that worktree.
- `gofmt -l .` must print nothing before each commit.

## Review Focus

The spec says what must exist, not every input it will meet. These are the inputs most likely to bite, each pinned by a test in the task that owns the code:

1. **Text the embedded fonts cannot draw** (a name or description with CJK, Arabic or emoji): must fail with a clear error, never render empty boxes. Task 2 (`TestTextTheFontCannotDrawIsAnError`); Task 4 renders every shipped manifest.
2. **A very wide or very short capture** (a 1920×80 bar crop, 3000×4): must render a centred, fully framed panel, never panic on a zero-height image. Task 2 (`TestRenderHandlesAWideBarCrop`).
3. **A transparent PNG screenshot**: must composite on the frame colour, not on black. Task 2 (`TestRenderCompositesATransparentScreenshotOnTheFrame`).
4. **A manifest with no description, no panels and no widgets**: must render with just the category chip. Task 2 (`TestRenderWithoutDescriptionOrFeatureChips`).
5. **A thumbnail that is present but stale** (description or category changed, screenshot replaced): `-check` must fail and name the regenerating command. Task 4 (`TestCheckFailsWhenTheManifestChanges`).

Also covered where they arise: the grandfather list naming a directory that does not exist (Task 5), a grandfathered plugin that already has a thumbnail (Task 5), a tagged thumbnail of the wrong size or missing (Task 6), and a backport tag that predates thumbnails (Task 6 note).

## File Structure

| Path | Responsibility |
|---|---|
| `internal/thumbnail/assets/*.ttf`, `sysc-mark.png` | Embedded typefaces (subset) and the wordmark mask |
| `internal/thumbnail/assets.go` | Parse embedded fonts, build faces, glyph-coverage check, decode the wordmark |
| `internal/thumbnail/draw.go` | Raster primitives: rounded rects, blur/glow, strokes, text with tracking, wrapping |
| `internal/thumbnail/render.go` | `Input`, `Render`, the B1 layout, `panelGeometry` |
| `internal/thumbnail/encode.go` | `Encode` (lossless WebP), `Validate` (file checks), `MaxBytes` |
| `internal/thumbnail/grandfather.go` | `LoadGrandfathered`: the exempt-plugin list parser |
| `tools/thumbnail/main.go` | CLI: write or `-check` thumbnails for plugins |
| `tools/thumbnail/grandfathered.txt` | The 16 existing plugins exempt until backfilled |
| `tools/validate-manifests/main.go` | Adds the per-plugin thumbnail rule |
| `tools/catalog/update.go`, `thumbnails.go`, `main.go`, `package.go` | Pin the tagged thumbnail, backfill subcommand, exclude catalog-only media from archives |
| `.github/workflows/ci.yml`, `Makefile` | Run `-check` in CI and locally |
| `docs/publishing.md`, `docs/writing-plugins.md`, `README.md`, `ATTRIBUTION.md` | Document the requirement and credit the assets |

`internal/thumbnail` is the only package with rendering logic; both tools and the catalog import it for `Validate`/`Render`, so there is one definition of "a valid thumbnail".

---

### Task 1: Dependency, embedded assets, and thumbnail file validation

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/thumbnail/assets/Inter-Regular.ttf`, `internal/thumbnail/assets/Inter-ExtraBold.ttf`, `internal/thumbnail/assets/JetBrainsMono-Bold.ttf`, `internal/thumbnail/assets/sysc-mark.png`, `internal/thumbnail/assets/README.md`
- Create: `internal/thumbnail/assets.go`, `internal/thumbnail/encode.go`
- Test: `internal/thumbnail/assets_test.go`, `internal/thumbnail/encode_test.go`

**Interfaces:**
- Produces: `thumbnail.Width = 960`, `thumbnail.Height = 540`, `thumbnail.MaxBytes = 512 << 10`; `func Encode(img image.Image) ([]byte, error)`; `func Validate(data []byte) error` (error text begins with a verb phrase such as `is 100x100; it must be 960x540`, so callers write `"thumbnail.webp " + err.Error()`); unexported `loadFonts() (fonts, error)`, `face(*opentype.Font, float64) (font.Face, error)`, `missingRune(*opentype.Font, string) (rune, bool)`, `wordmark() (image.Image, error)`.
- Note: `Width`/`Height` are declared in `render.go` in Task 2; Task 1's `encode.go` needs them, so Task 1 declares them in a small `internal/thumbnail/size.go` that Task 2 does **not** redeclare (Task 2 below omits them).

- [ ] **Step 1: Create the worktree and add the dependency**

```bash
git worktree add -b feat/plugin-thumbnails ~/worktrees/sysc-plugins-thumb-impl docs/plugin-thumbnails-spec
cd ~/worktrees/sysc-plugins-thumb-impl
go get github.com/HugoSmits86/nativewebp@v1.3.0
```

Expected: `go.mod` gains a `github.com/HugoSmits86/nativewebp v1.3.0` require line. (`go mod tidy` runs at Step 4 once code imports it.)

- [ ] **Step 2: Create the font subsets and the wordmark (one-off, by hand)**

```bash
mkdir -p internal/thumbnail/assets
LAT="U+0020-007E,U+00A0-00FF,U+2013,U+2014,U+2018,U+2019,U+201C,U+201D,U+2026,U+2022"
pyftsubset /usr/share/fonts/inter/Inter.ttc --font-number=0  --unicodes="$LAT" --layout-features='kern' --output-file=internal/thumbnail/assets/Inter-Regular.ttf
pyftsubset /usr/share/fonts/inter/Inter.ttc --font-number=16 --unicodes="$LAT" --layout-features='kern' --output-file=internal/thumbnail/assets/Inter-ExtraBold.ttf
pyftsubset /usr/share/fonts/TTF/JetBrainsMono-Bold.ttf \
  --unicodes="U+0020-007E,U+00B7,U+2026,U+2500-257F,U+2580-259F,U+25A0-25A1,U+258C" \
  --layout-features='' --output-file=internal/thumbnail/assets/JetBrainsMono-Bold.ttf
cp ~/sysc-shell/internal/render/icons/wordmark/sysc-mark.png internal/thumbnail/assets/sysc-mark.png
ls -l internal/thumbnail/assets
```

Expected: three `.ttf` files of roughly 35–43 KB each and `sysc-mark.png` (930×128 grayscale). If `~/sysc-shell` is on another branch the file is the same; it is committed in sysc-shell at `internal/render/icons/wordmark/sysc-mark.png`.

Write `internal/thumbnail/assets/README.md` (not embedded; the embed directive names files explicitly):

```markdown
# Embedded assets

Regenerate the font subsets with the commands in the plugin-thumbnails plan
(`docs/superpowers/plans/2026-10-08-plugin-thumbnails.md`, Task 1, Step 2).

| File | Source | Licence |
|---|---|---|
| `Inter-Regular.ttf`, `Inter-ExtraBold.ttf` | Inter 4.x (`Inter.ttc` faces 0 and 16), Basic Latin, Latin-1 and common punctuation | SIL OFL 1.1 |
| `JetBrainsMono-Bold.ttf` | JetBrains Mono Bold, ASCII plus box-drawing, block elements and a few geometric shapes | SIL OFL 1.1 |
| `sysc-mark.png` | sysc-shell `internal/render/icons/wordmark/sysc-mark.png`; a grayscale mask, white where the letterforms are | same project |
```

- [ ] **Step 3: Write the failing tests**

`internal/thumbnail/encode_test.go`:

```go
package thumbnail

import (
	"image"
	"strings"
	"testing"
)

func TestValidateRejectsWhatIsNotAThumbnail(t *testing.T) {
	small, err := Encode(image.NewNRGBA(image.Rect(0, 0, 100, 100)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "not a WebP"},
		{"png header", []byte("\x89PNG\r\n\x1a\n0000000000"), "not a WebP"},
		{"wrong size", small, "100x100"},
		{"too big", append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, MaxBytes)...), "keep it under"},
		{"truncated", []byte("RIFF\x00\x00\x00\x00WEBPVP8L"), "cannot be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsAFullSizeWebP(t *testing.T) {
	data, err := Encode(image.NewNRGBA(image.Rect(0, 0, Width, Height)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(data); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
```

`internal/thumbnail/assets_test.go`:

```go
package thumbnail

import (
	"strings"
	"testing"

	"golang.org/x/image/font/opentype"
)

func TestEmbeddedFontsCoverTheLayout(t *testing.T) {
	fs, err := loadFonts()
	if err != nil {
		t.Fatal(err)
	}
	// Every literal the layout draws in the monospace face.
	const mono = "┌─┐│█░▒▓·[]/+ ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789:._-"
	check := func(name string, f *opentype.Font, text string) {
		t.Helper()
		if r, ok := missingRune(f, text); !ok {
			t.Errorf("%s has no glyph for %q", name, r)
		}
	}
	check("JetBrains Mono Bold", fs.monoBold, mono)
	var ascii strings.Builder
	for r := rune(0x20); r <= 0x7e; r++ {
		ascii.WriteRune(r)
	}
	ascii.WriteString("…’“”–—")
	check("Inter Regular", fs.interRegular, ascii.String())
	check("Inter ExtraBold", fs.interExtraBold, ascii.String())
}

func TestWordmarkDecodesAsTheExpectedMask(t *testing.T) {
	img, err := wordmark()
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 930 || b.Dy() != 128 {
		t.Fatalf("wordmark is %dx%d, want 930x128", b.Dx(), b.Dy())
	}
}
```

- [ ] **Step 4: Run to verify they fail**

Run: `go test -count=1 -p 2 ./internal/thumbnail/`
Expected: FAIL to build, `undefined: Encode` / `undefined: loadFonts`.

- [ ] **Step 5: Implement**

`internal/thumbnail/size.go`:

```go
package thumbnail

// Width and Height are the thumbnail's pixel size: the sysc-shell plugin
// store lays previews out at 16:9.
const (
	Width  = 960
	Height = 540
)
```

`internal/thumbnail/encode.go`:

```go
package thumbnail

import (
	"bytes"
	"errors"
	"fmt"
	"image"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/webp"
)

// MaxBytes is the largest thumbnail file the repository accepts. The shell
// would take 2 MiB; this keeps catalog cards cheap to fetch.
const MaxBytes = 512 << 10

// Encode writes img as a lossless WebP.
func Encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := nativewebp.Encode(&b, img, nil); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Validate reports why data is not an acceptable thumbnail file: larger than
// MaxBytes, not a WebP, or not exactly Width by Height. The messages read as
// a continuation of "thumbnail.webp ".
func Validate(data []byte) error {
	if len(data) > MaxBytes {
		return fmt.Errorf("is %d bytes; keep it under %d", len(data), MaxBytes)
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return errors.New("is not a WebP image")
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("cannot be read as a WebP: %w", err)
	}
	if cfg.Width != Width || cfg.Height != Height {
		return fmt.Errorf("is %dx%d; it must be %dx%d", cfg.Width, cfg.Height, Width, Height)
	}
	return nil
}
```

`internal/thumbnail/assets.go`:

```go
package thumbnail

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

//go:embed assets/*.ttf assets/sysc-mark.png
var assetFS embed.FS

// fonts holds the parsed typefaces.
type fonts struct {
	interRegular, interExtraBold, monoBold *opentype.Font
}

func loadFonts() (fonts, error) {
	var f fonts
	for _, e := range []struct {
		name string
		dst  **opentype.Font
	}{
		{"assets/Inter-Regular.ttf", &f.interRegular},
		{"assets/Inter-ExtraBold.ttf", &f.interExtraBold},
		{"assets/JetBrainsMono-Bold.ttf", &f.monoBold},
	} {
		data, err := assetFS.ReadFile(e.name)
		if err != nil {
			return fonts{}, err
		}
		if *e.dst, err = opentype.Parse(data); err != nil {
			return fonts{}, fmt.Errorf("%s: %w", e.name, err)
		}
	}
	return f, nil
}

// face returns f at px pixels. Hinting is off so output does not depend on
// a hinting engine.
func face(f *opentype.Font, px float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingNone})
}

// missingRune reports the first rune of text that f has no glyph for. Text
// the font cannot draw must fail loudly, since an uncovered rune renders as
// an empty box.
func missingRune(f *opentype.Font, text string) (rune, bool) {
	var buf sfnt.Buffer
	for _, r := range text {
		if idx, err := f.GlyphIndex(&buf, r); err != nil || idx == 0 {
			return r, false
		}
	}
	return 0, true
}

// wordmark decodes the sysc wordmark mask: grayscale, white where the
// letterforms are.
func wordmark() (image.Image, error) {
	data, err := assetFS.ReadFile("assets/sysc-mark.png")
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(data))
}
```

Then: `go mod tidy`.

- [ ] **Step 6: Run to verify they pass**

Run: `gofmt -l internal/thumbnail && go vet -p 2 ./internal/thumbnail/ && go test -count=1 -p 2 ./internal/thumbnail/`
Expected: no gofmt output; `ok  github.com/Nomadcxx/sysc-plugins/internal/thumbnail`.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/thumbnail
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(thumbnail): embedded fonts, wordmark and WebP validation"
```

---

### Task 2: The renderer

**Files:**
- Create: `internal/thumbnail/draw.go`, `internal/thumbnail/render.go`
- Test: `internal/thumbnail/render_test.go`

**Interfaces:**
- Consumes (Task 1): `Width`, `Height`, `loadFonts`, `face`, `missingRune`, `wordmark`, `Encode`, `Validate`, `MaxBytes`.
- Produces: `type Input struct { Dir, ID, Name, Description, Category string; HasPanel, HasBar bool; Screenshot image.Image }`; `func Render(in Input) (img *image.NRGBA, warnings []string, err error)`; unexported `panelGeometry(h int) (outer, inner image.Rectangle, bleeds bool)`. Later tasks use `Input`, `Render`, `Encode`.

- [ ] **Step 1: Write the failing tests**

`internal/thumbnail/render_test.go`:

```go
package thumbnail

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"
)

// panelShot is a stand-in capture w by h pixels: a grey panel with a blue
// header strip, so a render has real structure to scale.
func panelShot(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{0x3a, 0x40, 0x4c, 0xff}
			if y < h/10 {
				c = color.NRGBA{0x1f, 0x8b, 0xff, 0xff}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func notes() Input {
	return Input{
		Dir: "notes", ID: "org.sysc.notes", Name: "Notes",
		Description: "Markdown notes and pastel sticky notes for your Obsidian folder, with one box to search or start a note.",
		Category:    "productivity", HasPanel: true, HasBar: true,
		Screenshot: panelShot(560, 700),
	}
}

func TestRenderIsOpaqueAndTheRightSize(t *testing.T) {
	img, warnings, err := Render(notes())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if got := img.Bounds(); got != image.Rect(0, 0, Width, Height) {
		t.Fatalf("bounds = %v", got)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			t.Fatalf("pixel %d is not opaque", i/4)
		}
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	encode := func() []byte {
		img, _, err := Render(notes())
		if err != nil {
			t.Fatal(err)
		}
		b, err := Encode(img)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := encode(), encode(); !bytes.Equal(a, b) {
		t.Fatal("two renders of the same input differ")
	}
}

func TestEncodedThumbnailValidates(t *testing.T) {
	img, _, err := Render(notes())
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(data); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(data) > MaxBytes/2 {
		t.Fatalf("thumbnail is %d bytes; expected well under the %d cap", len(data), MaxBytes)
	}
}

func TestLongNameWrapsToTwoLines(t *testing.T) {
	in := notes()
	in.Name = "GitHub Notifications"
	if _, _, err := Render(in); err != nil {
		t.Fatalf("two-line name: %v", err)
	}
}

func TestNameThatCannotFitIsAnError(t *testing.T) {
	for _, name := range []string{
		"Wallpaper Depth Mask Studio Professional Edition Deluxe", // wraps to more than two lines
		"Supercalifragilisticexpialidocious",                      // one word wider than the column
		"   ",
	} {
		in := notes()
		in.Name = name
		if _, _, err := Render(in); err == nil {
			t.Errorf("Name %q rendered; want an error", name)
		}
	}
}

func TestLongDescriptionIsCutWithAWarning(t *testing.T) {
	in := notes()
	in.Description = strings.Repeat("A description that keeps going and going and going. ", 6)
	_, warnings, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "notes") {
		t.Fatalf("warnings = %v, want one naming the plugin", warnings)
	}
}

func TestTextTheFontCannotDrawIsAnError(t *testing.T) {
	in := notes()
	in.Name = "メモ"
	if _, _, err := Render(in); err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("Render = %v, want a font coverage error", err)
	}
}

func TestPanelFitsTallAndShortCaptures(t *testing.T) {
	tall, _, bleeds := panelGeometry(400)
	if !bleeds || tall.Max.Y <= Height-frameInset-frameBorder {
		t.Errorf("a 400px capture should bleed past the frame bottom, got %v bleeds=%v", tall, bleeds)
	}
	short, inner, bleeds := panelGeometry(150)
	if bleeds {
		t.Fatal("a 150px capture should be framed, not bleed")
	}
	mid := (short.Min.Y + short.Max.Y) / 2
	bodyMid := ((frameInset + frameBorder + titleBarH + 2) + (Height - frameInset - frameBorder)) / 2
	if d := mid - bodyMid; d < -1 || d > 1 {
		t.Errorf("short capture centre y=%d, body centre %d", mid, bodyMid)
	}
	if inner.Dy() != 150 {
		t.Errorf("inner height = %d, want 150", inner.Dy())
	}
}

func TestRenderHandlesAWideBarCrop(t *testing.T) {
	for _, size := range [][2]int{{1920, 80}, {3000, 4}, {320, 10}} {
		in := notes()
		in.Screenshot = panelShot(size[0], size[1])
		if _, _, err := Render(in); err != nil {
			t.Errorf("%dx%d capture: %v", size[0], size[1], err)
		}
	}
}

func TestRenderCompositesATransparentScreenshotOnTheFrame(t *testing.T) {
	in := notes()
	in.Screenshot = image.NewNRGBA(image.Rect(0, 0, 560, 200)) // fully transparent
	img, _, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	// 560x200 scales to 113 rows at the panel's 316 px inner width.
	_, inner, _ := panelGeometry(113)
	c := img.NRGBAAt((inner.Min.X+inner.Max.X)/2, (inner.Min.Y+inner.Max.Y)/2)
	if c != colFrameFill {
		t.Fatalf("centre of a transparent capture = %v, want the frame fill %v", c, colFrameFill)
	}
}

func TestRenderWithoutDescriptionOrFeatureChips(t *testing.T) {
	in := notes()
	in.Description = ""
	in.HasPanel, in.HasBar = false, false
	if _, warnings, err := Render(in); err != nil || len(warnings) != 0 {
		t.Fatalf("Render = %v, %v", warnings, err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -count=1 -p 2 ./internal/thumbnail/`
Expected: FAIL to build, `undefined: Render`, `undefined: panelGeometry`, `undefined: colFrameFill`.

- [ ] **Step 3: Write `internal/thumbnail/draw.go`**

```go
package thumbnail

import (
	"image"
	"image/color"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Palette, taken from the sysc-shell theme the approved mock used.
var (
	colBackground = color.NRGBA{0x0d, 0x10, 0x19, 0xff}
	colFrameFill  = color.NRGBA{0x09, 0x0b, 0x12, 0xff}
	colBorder     = color.NRGBA{0x2c, 0x35, 0x4f, 0xff}
	colBlue       = color.NRGBA{0x1f, 0x8b, 0xff, 0xff}
	colInk        = color.NRGBA{0xee, 0xf2, 0xfa, 0xff}
	colMuted      = color.NRGBA{0x8f, 0x9a, 0xb4, 0xff}
	colChipText   = color.NRGBA{0xa9, 0xcf, 0xff, 0xff}
	colFooter     = color.NRGBA{0x4a, 0x56, 0x76, 0xff}
)

// over returns p composited on the opaque colour bg.
func over(p, bg color.NRGBA) color.NRGBA {
	a := float64(p.A) / 255
	f := func(x, y uint8) uint8 { return uint8(float64(x)*a + float64(y)*(1-a) + 0.5) }
	return color.NRGBA{f(p.R, bg.R), f(p.G, bg.G), f(p.B, bg.B), 0xff}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = clamp(t, 0, 1)
	f := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.NRGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 0xff}
}

// blend composites colour c with coverage a over the opaque pixel at (x, y).
func blend(img *image.NRGBA, x, y int, c color.NRGBA, a float64) {
	b := img.Bounds()
	if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y || a <= 0 {
		return
	}
	a = clamp(a, 0, 1)
	i := img.PixOffset(x, y)
	for k, v := range [3]uint8{c.R, c.G, c.B} {
		img.Pix[i+k] = uint8(float64(img.Pix[i+k])*(1-a) + float64(v)*a + 0.5)
	}
	img.Pix[i+3] = 0xff
}

// rrCover is the anti-aliased coverage of the rounded rectangle r at pixel
// (x, y). Radii are per half: top radius for the upper two corners, bottom
// for the lower two, so a panel can be rounded on top and square where it
// bleeds off the frame.
func rrCover(r image.Rectangle, top, bottom float64, x, y int) float64 {
	cy := float64(r.Min.Y+r.Max.Y) / 2
	rad := top
	if float64(y)+0.5 > cy {
		rad = bottom
	}
	cx := float64(r.Min.X+r.Max.X) / 2
	hx, hy := float64(r.Dx())/2-rad, float64(r.Dy())/2-rad
	px, py := math.Abs(float64(x)+0.5-cx)-hx, math.Abs(float64(y)+0.5-cy)-hy
	d := math.Hypot(math.Max(px, 0), math.Max(py, 0)) + math.Min(math.Max(px, py), 0) - rad
	return clamp(0.5-d, 0, 1)
}

func fillRR(img *image.NRGBA, r image.Rectangle, rad float64, c color.NRGBA, alpha float64) {
	for y := r.Min.Y - 1; y <= r.Max.Y; y++ {
		for x := r.Min.X - 1; x <= r.Max.X; x++ {
			blend(img, x, y, c, rrCover(r, rad, rad, x, y)*alpha)
		}
	}
}

// strokeRR draws a w-pixel border just inside r.
func strokeRR(img *image.NRGBA, r image.Rectangle, rad float64, w int, c color.NRGBA, alpha float64) {
	in := r.Inset(w)
	inner := math.Max(rad-float64(w), 0)
	for y := r.Min.Y - 1; y <= r.Max.Y; y++ {
		for x := r.Min.X - 1; x <= r.Max.X; x++ {
			ring := rrCover(r, rad, rad, x, y) - rrCover(in, inner, inner, x, y)
			blend(img, x, y, c, ring*alpha)
		}
	}
}

func boxBlur(a []float32, w, h, r int) {
	tmp := make([]float32, len(a))
	n := float32(2*r + 1)
	at := func(buf []float32, x, y int) float32 { return buf[min(max(y, 0), h-1)*w+min(max(x, 0), w-1)] }
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < h; y++ {
			var sum float32
			for x := -r; x <= r; x++ {
				sum += at(a, x, y)
			}
			for x := 0; x < w; x++ {
				tmp[y*w+x] = sum / n
				sum += at(a, x+r+1, y) - at(a, x-r, y)
			}
		}
		for x := 0; x < w; x++ {
			var sum float32
			for y := -r; y <= r; y++ {
				sum += at(tmp, x, y)
			}
			for y := 0; y < h; y++ {
				a[y*w+x] = sum / n
				sum += at(tmp, x, y+r+1) - at(tmp, x, y-r)
			}
		}
	}
}

// glow paints a blurred halo of the rounded rectangle r around it, limited to
// where clip is non-zero (the frame, so the halo never leaks past it).
func glow(img *image.NRGBA, r image.Rectangle, rad float64, blurRadius int, c color.NRGBA, alpha float64, clip func(x, y int) float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	m := make([]float32, w*h)
	for y := max(r.Min.Y, 0); y < min(r.Max.Y, h); y++ {
		for x := max(r.Min.X, 0); x < min(r.Max.X, w); x++ {
			m[y*w+x] = float32(rrCover(r, rad, rad, x, y))
		}
	}
	boxBlur(m, w, h, blurRadius)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if v := float64(m[y*w+x]); v > 0.002 {
				blend(img, x, y, c, v*alpha*clip(x, y))
			}
		}
	}
}

// strokeLine draws an anti-aliased segment of the given thickness.
func strokeLine(img *image.NRGBA, x0, y0, x1, y1, thick float64, c color.NRGBA, alpha float64) {
	minX, maxX := int(math.Floor(math.Min(x0, x1)-thick)), int(math.Ceil(math.Max(x0, x1)+thick))
	minY, maxY := int(math.Floor(math.Min(y0, y1)-thick)), int(math.Ceil(math.Max(y0, y1)+thick))
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float64(x)+0.5-x0, float64(y)+0.5-y0
			t := 0.0
			if l2 > 0 {
				t = clamp((px*dx+py*dy)/l2, 0, 1)
			}
			d := math.Hypot(px-t*dx, py-t*dy)
			blend(img, x, y, c, clamp(thick/2+0.5-d, 0, 1)*alpha)
		}
	}
}

// scaleTo resamples src to w by h pixels.
func scaleTo(src image.Image, w, h int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

// ---- text ----

func advance(f font.Face, s string, track float64) float64 {
	return float64(font.MeasureString(f, s))/64 + track*float64(len([]rune(s)))
}

// drawText draws s with its baseline-left at (x, baseline), one glyph at a
// time so tracking applies, and returns the x position after the last glyph.
func drawText(img *image.NRGBA, f font.Face, s string, x, baseline float64, c color.NRGBA, track float64) float64 {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f}
	for _, r := range s {
		d.Dot = fixed.Point26_6{X: fixed.Int26_6(math.Round(x * 64)), Y: fixed.Int26_6(math.Round(baseline * 64))}
		d.DrawString(string(r))
		x += float64(font.MeasureString(f, string(r)))/64 + track
	}
	return x
}

// wrapLines greedily breaks s into lines no wider than maxW. fits is false
// when a single word is wider than maxW and so cannot be broken.
func wrapLines(f font.Face, s string, maxW, track float64) (lines []string, fits bool) {
	fits = true
	cur := ""
	for _, w := range strings.Fields(s) {
		t := strings.TrimSpace(cur + " " + w)
		if advance(f, t, track) > maxW && cur != "" {
			lines = append(lines, cur)
			cur = w
		} else {
			cur = t
		}
		if advance(f, cur, track) > maxW {
			fits = false
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines, fits
}
```

- [ ] **Step 4: Write `internal/thumbnail/render.go`**

```go
// Package thumbnail renders the 960x540 catalog thumbnail every sysc plugin
// ships, and checks that a file on disk is one.
package thumbnail

import (
	"errors"
	"fmt"
	"image"
	"math"
	"strings"
)

// Input is everything a thumbnail shows. Dir is the plugin directory name
// (plugins/<Dir>); ID, Name and Description come from its manifest, Category
// from catalog-meta.json, and Screenshot is the captured panel.
type Input struct {
	Dir         string
	ID          string
	Name        string
	Description string
	Category    string
	HasPanel    bool
	HasBar      bool
	Screenshot  image.Image
}

// Layout constants, in output pixels. They reproduce the approved "B1" mock.
const (
	frameInset   = 24
	frameRadius  = 18
	frameBorder  = 2
	titleBarH    = 58
	columnX      = frameInset + frameBorder + 40
	columnW      = 410
	panelInnerW  = 316
	panelRight   = Width - frameInset - frameBorder - 44
	panelTopGap  = 40
	maxDescLines = 3
	maxNameLines = 2

	// breadcrumbMaxW is the room left of the centred wordmark group.
	breadcrumbMaxW = 277
)

// Render draws the thumbnail. The returned warnings are non-fatal: today only
// a description cut to fit.
func Render(in Input) (img *image.NRGBA, warnings []string, err error) {
	if in.Screenshot == nil {
		return nil, nil, errors.New("thumbnail: no screenshot")
	}
	if b := in.Screenshot.Bounds(); b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, nil, errors.New("thumbnail: screenshot is empty")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, nil, errors.New("thumbnail: plugin has no name")
	}
	fs, err := loadFonts()
	if err != nil {
		return nil, nil, err
	}
	for _, field := range []struct{ label, text string }{
		{"name", in.Name}, {"description", in.Description}, {"category", in.Category},
	} {
		if r, ok := missingRune(fs.interExtraBold, field.text); !ok {
			return nil, nil, fmt.Errorf("thumbnail: %s %q has %q, which the embedded font does not cover", field.label, field.text, r)
		}
	}
	mark, err := wordmark()
	if err != nil {
		return nil, nil, err
	}

	img = image.NewNRGBA(image.Rect(0, 0, Width, Height))
	paintBackground(img)

	frame := image.Rect(frameInset, frameInset, Width-frameInset, Height-frameInset)
	fillRR(img, frame, frameRadius, colFrameFill, 0.82)
	strokeRR(img, frame, frameRadius, frameBorder, colBorder, 1)

	if err := paintTitleBar(img, fs, mark, in); err != nil {
		return nil, nil, err
	}
	warnings, err = paintColumn(img, fs, in)
	if err != nil {
		return nil, nil, err
	}
	if err := paintPanel(img, fs, in, frame); err != nil {
		return nil, nil, err
	}
	if err := paintFooter(img, fs); err != nil {
		return nil, nil, err
	}
	return img, warnings, nil
}

func paintBackground(img *image.NRGBA) {
	// Bottom-centre blue glow over the base colour.
	cx, cy, rx, ry := float64(Width)*0.5, float64(Height)*1.12, 700.0, 420.0
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			d := math.Hypot((float64(x)-cx)/rx, (float64(y)-cy)/ry)
			c := mix(colBackground, colBlue, clamp(1-d, 0, 1)*0.3)
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 0xff
		}
	}
	// "+" crosshairs on a 24 px grid.
	for gy := 12; gy < Height; gy += 24 {
		for gx := 16; gx < Width; gx += 24 {
			for k := -3; k <= 3; k++ {
				blend(img, gx+k, gy, colBlue, 0.2)
				if k != 0 {
					blend(img, gx, gy+k, colBlue, 0.2)
				}
			}
		}
	}
}

func paintTitleBar(img *image.NRGBA, fs fonts, mark image.Image, in Input) error {
	mono, err := face(fs.monoBold, 13)
	if err != nil {
		return err
	}
	const baseline = frameInset + frameBorder + titleBarH/2 + 5
	// The breadcrumb must stay clear of the wordmark group, which starts at
	// x = 343; a long directory name drops the "sysc://" scheme to fit.
	prefix := " sysc://plugins/"
	if advance(mono, "┌─"+prefix+in.Dir, 0) > breadcrumbMaxW {
		prefix = " plugins/"
	}
	x := float64(columnX - 12)
	x = drawText(img, mono, "┌─", x, baseline, colBlue, 0)
	x = drawText(img, mono, prefix, x, baseline, colMuted, 0)
	drawText(img, mono, in.Dir, x, baseline, colInk, 0)

	right := fmt.Sprintf("%s ─┐", in.ID)
	rx := float64(Width-frameInset-frameBorder-28) - advance(mono, right, 0)
	rx = drawText(img, mono, in.ID, rx, baseline, colMuted, 0)
	drawText(img, mono, " ─┐", rx, baseline, colBlue, 0)

	// Wordmark flanked by slashes, centred on the bar.
	const slashCell, slashes, gap, markH = 10.8, 6, 14.0, 16
	markW := int(math.Round(float64(mark.Bounds().Dx()) * markH / float64(mark.Bounds().Dy())))
	total := 2*slashCell*slashes + 2*gap + float64(markW)
	sx := (Width - total) / 2
	midY := float64(frameInset + frameBorder + titleBarH/2)
	paintSlashes(img, sx, midY)
	paintMark(img, mark, int(math.Round(sx+slashCell*slashes+gap)), int(midY)-markH/2, markW, markH)
	paintSlashes(img, sx+slashCell*slashes+gap+float64(markW)+gap, midY)

	// Rule under the bar.
	ruleY := frameInset + frameBorder + titleBarH
	for rx := frameInset + frameBorder; rx < Width-frameInset-frameBorder; rx++ {
		for k := 0; k < 2; k++ {
			blend(img, rx, ruleY+k, colBorder, 1)
		}
	}
	return nil
}

func paintSlashes(img *image.NRGBA, x, midY float64) {
	for i := 0; i < 6; i++ {
		cx := x + 10.8*float64(i) + 5.4
		strokeLine(img, cx-3.6, midY+6.5, cx+3.6, midY-6.5, 2, colBlue, 0.95)
	}
}

func paintMark(img *image.NRGBA, mask image.Image, x, y, w, h int) {
	scaled := scaleTo(mask, w, h)
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			// The mask is grayscale: white (R=255) is the letterform.
			blend(img, x+px, y+py, colBlue, float64(scaled.NRGBAAt(px, py).R)/255)
		}
	}
}

// paintColumn draws the left text column, vertically centred in the body,
// and returns any warnings.
func paintColumn(img *image.NRGBA, fs fonts, in Input) (warnings []string, err error) {
	// Name: one line at 54 px, else two at 46 px.
	nameSize := 54.0
	nameFace, err := face(fs.interExtraBold, nameSize)
	if err != nil {
		return nil, err
	}
	const nameTrack = -0.8
	nameLines, fits := wrapLines(nameFace, in.Name, columnW-40, nameTrack)
	if len(nameLines) > 1 || !fits {
		nameSize = 46
		if nameFace, err = face(fs.interExtraBold, nameSize); err != nil {
			return nil, err
		}
		nameLines, fits = wrapLines(nameFace, in.Name, columnW-40, nameTrack)
	}
	if !fits || len(nameLines) > maxNameLines {
		return nil, fmt.Errorf("thumbnail: name %q does not fit in %d lines", in.Name, maxNameLines)
	}

	descFace, err := face(fs.interRegular, 21)
	if err != nil {
		return nil, err
	}
	const gutterW = 24.8 // gutter glyph advance plus the gap to the text
	descLines, _ := wrapLines(descFace, in.Description, columnW-gutterW, 0)
	if len(descLines) > maxDescLines {
		descLines = descLines[:maxDescLines]
		last := descLines[maxDescLines-1]
		for advance(descFace, last+"…", 0) > columnW-gutterW && len(last) > 0 {
			last = strings.TrimRight(last[:len(last)-1], " ")
		}
		descLines[maxDescLines-1] = strings.TrimRight(last, " .,;:") + "…"
		warnings = append(warnings, fmt.Sprintf("description of %s is longer than %d lines and was cut with an ellipsis", in.Dir, maxDescLines))
	}

	const labelH, chipH, gap = 16.0, 28.0, 12.0
	nameH := float64(len(nameLines)) * nameSize * 1.02
	descH := float64(len(descLines)) * 29
	blockH := labelH + gap + nameH + gap + descH + gap + 6 + chipH
	bodyTop := float64(frameInset + frameBorder + titleBarH + 2)
	bodyH := float64(Height-frameInset-frameBorder) - bodyTop
	top := bodyTop + (bodyH-blockH)/2

	mono12, err := face(fs.monoBold, 12)
	if err != nil {
		return nil, err
	}
	y := top
	drawText(img, mono12, "// PLUGIN", columnX, y+12, colBlue, 2)
	y += labelH + gap

	var lastX, lastBase float64
	for i, line := range nameLines {
		lastBase = y + float64(i)*nameSize*1.02 + nameSize*0.86
		lastX = drawText(img, nameFace, line, columnX, lastBase, colInk, nameTrack)
	}
	// Terminal cursor: a block as wide as half the type and as tall as its caps.
	cur := image.Rect(int(lastX+8), int(lastBase-nameSize*0.74), int(lastX+8+nameSize*0.5), int(lastBase))
	fillRR(img, cur, 1, colBlue, 1)
	y += nameH + gap

	gutter, err := face(fs.monoBold, 18)
	if err != nil {
		return nil, err
	}
	for i, line := range descLines {
		base := y + float64(i)*29 + 22
		drawText(img, gutter, "│", columnX, base-1, colBlue, 0)
		drawText(img, descFace, line, columnX+gutterW, base, colMuted, 0)
	}
	y += descH + gap + 6

	chips := []string{"[ " + strings.ToUpper(in.Category) + " ]"}
	if in.HasPanel {
		chips = append(chips, "[ PANEL ]")
	}
	if in.HasBar {
		chips = append(chips, "[ BAR ]")
	}
	cx := float64(columnX)
	for i, label := range chips {
		w := advance(mono12, label, 1.4) + 20
		r := image.Rect(int(cx), int(y), int(cx+w), int(y+chipH))
		if i == 0 {
			fillRR(img, r, 6, colBlue, 0.14)
			strokeRR(img, r, 6, 1, colBlue, 0.55)
			drawText(img, mono12, label, cx+10, y+18, colChipText, 1.4)
		} else {
			strokeRR(img, r, 6, 1, colBorder, 1)
			drawText(img, mono12, label, cx+10, y+18, colMuted, 1.4)
		}
		cx += w + 8
	}
	return warnings, nil
}

// panelGeometry places the screenshot panel for a screenshot scaled to h
// pixels tall at panelInnerW wide. outer is the bordered box and inner the
// pixels inside the border; bleeds reports a tall capture that runs off the
// bottom of the frame (no bottom border, faded) instead of sitting fully
// framed and centred.
func panelGeometry(h int) (outer, inner image.Rectangle, bleeds bool) {
	left := panelRight - (panelInnerW + 2*frameBorder)
	bodyTop := frameInset + frameBorder + titleBarH + 2
	bodyBottom := Height - frameInset - frameBorder
	avail := bodyBottom - bodyTop - panelTopGap

	bleeds = h+frameBorder >= avail
	if bleeds {
		outer = image.Rect(left, bodyTop+panelTopGap, panelRight, bodyTop+panelTopGap+h+frameBorder)
		inner = image.Rect(left+frameBorder, outer.Min.Y+frameBorder, panelRight-frameBorder, outer.Max.Y)
		return outer, inner, true
	}
	elemH := h + 2*frameBorder
	top := bodyTop + (bodyBottom-bodyTop-elemH)/2
	outer = image.Rect(left, top, panelRight, top+elemH)
	inner = image.Rect(left+frameBorder, top+frameBorder, panelRight-frameBorder, top+frameBorder+h)
	return outer, inner, false
}

// paintPanel draws the captured screenshot in its glowing frame. A tall
// capture bleeds off the bottom of the frame and fades over its last 22%; a
// short one is centred with a full border.
func paintPanel(img *image.NRGBA, fs fonts, in Input, frame image.Rectangle) error {
	b := in.Screenshot.Bounds()
	// A very wide crop would round to zero rows; keep at least one.
	h := max(int(math.Round(float64(b.Dy())*panelInnerW/float64(b.Dx()))), 1)
	outer, inner, bleeds := panelGeometry(h)
	top, left := outer.Min.Y, outer.Min.X
	elemH := outer.Dy()
	bodyBottom := Height - frameInset - frameBorder
	bottomRad := 12.0
	if bleeds {
		bottomRad = 0
	}
	innerBottomRad := math.Max(bottomRad-float64(frameBorder), 0)

	inFrame := func(x, y int) float64 { return rrCover(frame, frameRadius, frameRadius, x, y) }
	glow(img, outer, 12, 34, colBlue, 0.42, inFrame)

	shot := scaleTo(in.Screenshot, panelInnerW, h)
	for y := outer.Min.Y; y < min(outer.Max.Y, bodyBottom); y++ {
		fade := 1.0
		if bleeds {
			fade = clamp(float64(outer.Max.Y-y)/float64(elemH)/0.22, 0, 1)
		}
		for x := outer.Min.X; x < outer.Max.X; x++ {
			a := rrCover(outer, 12, bottomRad, x, y) * fade
			c := colFrameFill
			if image.Pt(x, y).In(image.Rect(inner.Min.X, inner.Min.Y, inner.Max.X, inner.Min.Y+h)) {
				c = over(shot.NRGBAAt(x-inner.Min.X, y-inner.Min.Y), colFrameFill)
			}
			blend(img, x, y, c, a)
			ring := rrCover(outer, 12, bottomRad, x, y) - rrCover(inner, 10, innerBottomRad, x, y)
			blend(img, x, y, colBlue, ring*0.7*fade)
		}
	}

	corner, err := face(fs.monoBold, 22)
	if err != nil {
		return err
	}
	drawText(img, corner, "┌", float64(left-14), float64(top-20)+18, colBlue, 0)
	drawText(img, corner, "┐", float64(panelRight+14)-13.2, float64(top-20)+18, colBlue, 0)
	return nil
}

func paintFooter(img *image.NRGBA, fs fonts) error {
	mono, err := face(fs.monoBold, 12)
	if err != nil {
		return err
	}
	const base = Height - frameInset - frameBorder - 17
	x := drawText(img, mono, "░▒▓█▓▒░", columnX, base, colBlue, 0)
	drawText(img, mono, "  sysc-shell · plugin catalog", x, base, colFooter, 0)
	return nil
}
```

- [ ] **Step 5: Run to verify they pass**

Run: `gofmt -w internal/thumbnail && gofmt -l internal/thumbnail; go vet -p 2 ./internal/thumbnail/ && go test -count=1 -p 2 -v ./internal/thumbnail/ 2>&1 | grep -E "^(--- |ok|FAIL|PASS)"`
Expected: every test `--- PASS`, then `ok`. (This code was prototyped and all of these tests passed against it.)

- [ ] **Step 6: Look at it**

Confirm by eye with a throwaway test that writes one render to the scratch directory. Add it, run it, then delete it; it is never committed:

```go
// internal/thumbnail/zz_try_test.go  (DELETE AFTER LOOKING)
package thumbnail

import ("os"; "testing")

func TestZZWriteSample(t *testing.T) {
	img, _, err := Render(notes())
	if err != nil { t.Fatal(err) }
	b, _ := Encode(img)
	os.WriteFile(os.Getenv("SAMPLE_OUT"), b, 0o644)
}
```

Run: `SAMPLE_OUT=$SCRATCH/sample.webp go test -count=1 -p 2 -run ZZWriteSample ./internal/thumbnail/ && magick $SCRATCH/sample.webp $SCRATCH/sample.png` then open `sample.png`. It should show the B1 layout: `+` grid, framed window, breadcrumb and id in the title bar, `Notes` with a blue block cursor, gutter-marked description, `[ PRODUCTIVITY ] [ PANEL ] [ BAR ]` chips, the panel at right with a glow, and the `░▒▓█▓▒░` footer. Then `rm internal/thumbnail/zz_try_test.go`.

- [ ] **Step 7: Commit**

```bash
git add internal/thumbnail
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(thumbnail): render the sysc catalog thumbnail in pure Go"
```

---

### Task 3: The grandfathered list

**Files:**
- Create: `internal/thumbnail/grandfather.go`, `tools/thumbnail/grandfathered.txt`
- Test: `internal/thumbnail/grandfather_test.go`

**Interfaces:**
- Produces: `func LoadGrandfathered(path string) (map[string]bool, error)`; the constant file path `tools/thumbnail/grandfathered.txt` is used by Tasks 4 and 5.

- [ ] **Step 1: Write the failing test**

```go
package thumbnail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeList(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "grandfathered.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadGrandfatheredParsesNamesAndSkipsNoise(t *testing.T) {
	got, err := LoadGrandfathered(writeList(t, "# exempt until backfilled\nnotes\n\n  timer  \nworld-clock # trailing note\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notes", "timer", "world-clock"} {
		if !got[name] {
			t.Errorf("%s missing from %v", name, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %v, want exactly 3 names", got)
	}
}

func TestLoadGrandfatheredMissingFileIsEmpty(t *testing.T) {
	got, err := LoadGrandfathered(filepath.Join(t.TempDir(), "nope.txt"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want an empty list and no error", got, err)
	}
}

func TestLoadGrandfatheredRejectsBadLines(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"duplicate", "notes\nnotes\n", "listed twice"},
		{"path", "plugins/notes\n", "plugin directory name"},
		{"parent", "..\n", "plugin directory name"},
		{"space", "two words\n", "plugin directory name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadGrandfathered(writeList(t, tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -count=1 -p 2 -run Grandfathered ./internal/thumbnail/`
Expected: FAIL to build, `undefined: LoadGrandfathered`.

- [ ] **Step 3: Implement**

`internal/thumbnail/grandfather.go`:

```go
package thumbnail

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

var dirNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// LoadGrandfathered reads the list of plugin directories exempt from the
// thumbnail requirement until their backfill lands: one directory name per
// line, with blank lines and #-comments ignored. A missing file is an empty
// list, so deleting the file once the list is empty needs no other change.
func LoadGrandfathered(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if !dirNamePattern.MatchString(name) {
			return nil, fmt.Errorf("%s:%d: %q is not a plugin directory name", path, n, name)
		}
		if out[name] {
			return nil, fmt.Errorf("%s:%d: %q is listed twice", path, n, name)
		}
		out[name] = true
	}
	return out, sc.Err()
}
```

Create `tools/thumbnail/grandfathered.txt`:

```
# Plugins that predate the thumbnail requirement. Each one is removed from
# this list by the PR that adds its screenshot.png and thumbnail.webp; the
# validator fails if a listed plugin already has them. New plugins are never
# added here. Delete this file when it is empty.
aiusage
calendar
cat
faith
games
github-notifications
kdeconnect
mini-docker
moonbit
notes
protonvpn
screen-recorder
screenshot
timer
wallpaper-depth
world-clock
```

- [ ] **Step 4: Run to verify it passes**

Run: `gofmt -l internal/thumbnail; go test -count=1 -p 2 ./internal/thumbnail/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/thumbnail/grandfather.go internal/thumbnail/grandfather_test.go tools/thumbnail/grandfathered.txt
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(thumbnail): list the plugins exempt until their backfill"
```

---

### Task 4: The `tools/thumbnail` command

**Files:**
- Create: `tools/thumbnail/main.go`
- Test: `tools/thumbnail/main_test.go`

**Interfaces:**
- Consumes: `thumbnail.Input`, `Render`, `Encode`, `LoadGrandfathered` (Tasks 1–3).
- Produces: CLI `go run ./tools/thumbnail [-plugin plugins/<dir>] [-check]`; `run(root string, args []string, stdout, stderr io.Writer) error`. The regenerate command text used in failure messages everywhere is exactly `go run ./tools/thumbnail -plugin plugins/<dir>`.

- [ ] **Step 1: Write the failing tests**

```go
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{0x3a, 0x40, 0x4c, 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const notesManifest = `{"schema":1,"id":"org.sysc.notes","name":"Notes","description":"Markdown notes.","version":"1.0.0","exec":"bin/x","widgets":[{"id":"bar"}],"panels":[{"id":"panel"}]}`

// newRepo is a minimal repository: one plugin with a valid capture.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "plugins/notes/manifest.json"), []byte(notesManifest))
	write(t, filepath.Join(root, "plugins/notes/screenshot.png"), pngBytes(t, 560, 700))
	write(t, filepath.Join(root, "catalog-meta.json"), []byte(`{"org.sysc.notes":{"category":"productivity","author":"x"}}`))
	return root
}

func runIn(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(root, args, &out, &errOut)
	return out.String() + errOut.String(), err
}

func TestWriteThenCheckPasses(t *testing.T) {
	root := newRepo(t)
	if out, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatalf("write: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(root, "plugins/notes/thumbnail.webp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := thumbnail.Validate(data); err != nil {
		t.Fatalf("written file is not a valid thumbnail: %v", err)
	}
	if out, err := runIn(t, root, "-check"); err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
}

func TestCheckFailsWhenTheManifestChanges(t *testing.T) {
	root := newRepo(t)
	if _, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(notesManifest, "Markdown notes.", "Markdown notes, now with more.", 1)
	write(t, filepath.Join(root, "plugins/notes/manifest.json"), []byte(changed))
	_, err := runIn(t, root, "-check")
	if err == nil || !strings.Contains(err.Error(), "out of date") ||
		!strings.Contains(err.Error(), "go run ./tools/thumbnail -plugin plugins/notes") {
		t.Fatalf("err = %v, want an out-of-date failure naming the regenerate command", err)
	}
}

func TestCheckFailsWhenTheCategoryChanges(t *testing.T) {
	root := newRepo(t)
	if _, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "catalog-meta.json"), []byte(`{"org.sysc.notes":{"category":"utilities","author":"x"}}`))
	if _, err := runIn(t, root, "-check"); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("err = %v, want out of date", err)
	}
}

func TestCheckFailsWhenTheThumbnailIsMissing(t *testing.T) {
	root := newRepo(t)
	_, err := runIn(t, root, "-check")
	if err == nil || !strings.Contains(err.Error(), "missing thumbnail.webp") {
		t.Fatalf("err = %v, want a missing-thumbnail failure", err)
	}
}

func TestCheckSkipsGrandfatheredPlugins(t *testing.T) {
	root := newRepo(t)
	write(t, filepath.Join(root, "plugins/old/manifest.json"), []byte(strings.ReplaceAll(notesManifest, "notes", "old")))
	write(t, filepath.Join(root, "tools/thumbnail/grandfathered.txt"), []byte("old\n"))
	if _, err := runIn(t, root, "-plugin", "plugins/notes"); err != nil {
		t.Fatal(err)
	}
	if out, err := runIn(t, root, "-check"); err != nil {
		t.Fatalf("a grandfathered plugin with no files must not fail -check: %v\n%s", err, out)
	}
}

func TestBadScreenshotsAreRejected(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 400, 400)), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", jpg.Bytes(), "must be a PNG"},
		{"narrow", pngBytes(t, 200, 300), "at least 320"},
		{"not an image", []byte("hello"), "screenshot.png"},
		{"oversized", append(pngBytes(t, 400, 400), make([]byte, maxShotBytes)...), "4 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRepo(t)
			write(t, filepath.Join(root, "plugins/notes/screenshot.png"), tc.data)
			_, err := runIn(t, root, "-plugin", "plugins/notes")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestMissingScreenshotNamesTheFile(t *testing.T) {
	root := newRepo(t)
	if err := os.Remove(filepath.Join(root, "plugins/notes/screenshot.png")); err != nil {
		t.Fatal(err)
	}
	_, err := runIn(t, root, "-plugin", "plugins/notes")
	if err == nil || !strings.Contains(err.Error(), "missing screenshot.png") {
		t.Fatalf("err = %v, want a missing-screenshot failure", err)
	}
}

// Every manifest the repo ships must be drawable: a name that cannot fit in
// two lines, or a character the embedded fonts lack, fails here instead of
// on the day someone adds that plugin's capture.
func TestShippedManifestsRender(t *testing.T) {
	root := "../.."
	meta, err := readCategories(filepath.Join(root, "catalog-meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := filepath.Glob(filepath.Join(root, "plugins", "*", "manifest.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no manifests: %v", err)
	}
	for _, path := range dirs {
		dir := filepath.Base(filepath.Dir(path))
		in, err := loadInput(root, dir, meta)
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		in.Screenshot = image.NewNRGBA(image.Rect(0, 0, 560, 700))
		if _, _, err := thumbnail.Render(in); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -count=1 -p 2 ./tools/thumbnail/`
Expected: FAIL to build, `undefined: run`, `undefined: readCategories`, `undefined: loadInput`, `undefined: maxShotBytes`.

- [ ] **Step 3: Implement `tools/thumbnail/main.go`**

```go
// Command thumbnail writes, or checks, the catalog thumbnail every plugin
// ships.
//
//	thumbnail -plugin plugins/<dir>   write plugins/<dir>/thumbnail.webp
//	thumbnail                         write it for every plugin not grandfathered
//	thumbnail -check [-plugin ...]    fail if a committed thumbnail differs
//
// It renders from plugins/<dir>/manifest.json, the plugin's category in
// catalog-meta.json and plugins/<dir>/screenshot.png, so the same inputs
// always produce the same bytes. It runs from the repository root.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

const (
	grandfatheredFile = "tools/thumbnail/grandfathered.txt"
	minShotWidth      = 320
	maxShotBytes      = 4 << 20
)

func main() {
	if err := run(".", os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "thumbnail:", err)
		os.Exit(1)
	}
}

func run(root string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("thumbnail", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plugin := flags.String("plugin", "", "plugin directory, plugins/<dir> (default: every plugin not in "+grandfatheredFile+")")
	check := flags.Bool("check", false, "compare with the committed thumbnail.webp instead of writing it")
	if err := flags.Parse(args); err != nil {
		return err
	}

	meta, err := readCategories(filepath.Join(root, "catalog-meta.json"))
	if err != nil {
		return err
	}
	dirs, err := selectDirs(root, *plugin)
	if err != nil {
		return err
	}

	var failures []string
	for _, dir := range dirs {
		data, warnings, err := build(root, dir, meta)
		for _, w := range warnings {
			fmt.Fprintln(stderr, "warning:", w)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("plugins/%s: %v", dir, err))
			continue
		}
		path := filepath.Join(root, "plugins", dir, "thumbnail.webp")
		regenerate := "go run ./tools/thumbnail -plugin plugins/" + dir
		if *check {
			have, err := os.ReadFile(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				failures = append(failures, fmt.Sprintf("plugins/%s: missing thumbnail.webp; run: %s", dir, regenerate))
			case err != nil:
				failures = append(failures, fmt.Sprintf("plugins/%s: %v", dir, err))
			case !bytes.Equal(have, data):
				failures = append(failures, fmt.Sprintf("plugins/%s: thumbnail.webp is out of date; regenerate with: %s", dir, regenerate))
			}
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			failures = append(failures, fmt.Sprintf("plugins/%s: %v", dir, err))
			continue
		}
		fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", path, len(data))
	}
	if len(failures) > 0 {
		return errors.New("\n" + strings.Join(failures, "\n"))
	}
	return nil
}

// selectDirs returns the plugin directory names to process: the one named by
// -plugin, or every directory under plugins/ not on the grandfathered list.
func selectDirs(root, plugin string) ([]string, error) {
	if plugin != "" {
		dir := filepath.Base(filepath.Clean(plugin))
		if _, err := os.Stat(filepath.Join(root, "plugins", dir, "manifest.json")); err != nil {
			return nil, fmt.Errorf("-plugin %q: no manifest.json under plugins/%s", plugin, dir)
		}
		return []string{dir}, nil
	}
	grand, err := thumbnail.LoadGrandfathered(filepath.Join(root, grandfatheredFile))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !grand[e.Name()] {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func build(root, dir string, meta map[string]string) ([]byte, []string, error) {
	in, err := loadInput(root, dir, meta)
	if err != nil {
		return nil, nil, err
	}
	if in.Screenshot, err = loadScreenshot(filepath.Join(root, "plugins", dir, "screenshot.png")); err != nil {
		return nil, nil, err
	}
	img, warnings, err := thumbnail.Render(in)
	if err != nil {
		return nil, warnings, err
	}
	data, err := thumbnail.Encode(img)
	if err != nil {
		return nil, warnings, err
	}
	if err := thumbnail.Validate(data); err != nil {
		return nil, warnings, fmt.Errorf("rendered thumbnail %w", err)
	}
	return data, warnings, nil
}

type manifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Widgets     []json.RawMessage `json:"widgets"`
	Panels      []json.RawMessage `json:"panels"`
}

// loadInput gathers everything a thumbnail shows except the screenshot.
func loadInput(root, dir string, meta map[string]string) (thumbnail.Input, error) {
	data, err := os.ReadFile(filepath.Join(root, "plugins", dir, "manifest.json"))
	if err != nil {
		return thumbnail.Input{}, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return thumbnail.Input{}, fmt.Errorf("manifest.json: %w", err)
	}
	category, ok := meta[m.ID]
	if !ok || category == "" {
		return thumbnail.Input{}, fmt.Errorf("catalog-meta.json has no category for %q", m.ID)
	}
	return thumbnail.Input{
		Dir: dir, ID: m.ID, Name: m.Name, Description: m.Description,
		Category: category, HasPanel: len(m.Panels) > 0, HasBar: len(m.Widgets) > 0,
	}, nil
}

// readCategories maps plugin id to category from catalog-meta.json.
func readCategories(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make(map[string]string, len(raw))
	for id, row := range raw {
		out[id] = row.Category
	}
	return out, nil
}

// loadScreenshot reads a plugin's capture, which must be a PNG at least
// minShotWidth wide and at most maxShotBytes.
func loadScreenshot(path string) (image.Image, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("missing screenshot.png; capture the plugin's panel (docs/publishing.md, \"Thumbnails\")")
	}
	if err != nil {
		return nil, err
	}
	if info.Size() > maxShotBytes {
		return nil, fmt.Errorf("screenshot.png is %d bytes; keep it under 4 MiB", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("screenshot.png cannot be read: %w", err)
	}
	if format != "png" {
		return nil, fmt.Errorf("screenshot.png is a %s image; it must be a PNG", format)
	}
	if cfg.Width < minShotWidth {
		return nil, fmt.Errorf("screenshot.png is %d px wide; it must be at least %d", cfg.Width, minShotWidth)
	}
	return png.Decode(bytes.NewReader(data))
}
```

Note on the "oversized" test: it appends padding after a valid PNG, so the size check (before any decode) is what rejects it; the `4 MiB` wording is asserted.

- [ ] **Step 4: Run to verify they pass**

Run: `gofmt -l tools/thumbnail internal/thumbnail; go vet -p 2 ./tools/thumbnail/ && go test -count=1 -p 2 ./tools/thumbnail/`
Expected: `ok`. If `TestShippedManifestsRender` fails for a shipped plugin, the failure names it: shorten that name or description in its manifest (and tell the user) or, if it is a missing glyph, add that range to the Inter subset in Task 1 Step 2 and regenerate. Do not weaken the test.

- [ ] **Step 5: Commit**

```bash
git add tools/thumbnail/main.go tools/thumbnail/main_test.go
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(thumbnail): add the thumbnail command"
```

---

### Task 5: Enforce thumbnails in `validate-manifests`

**Files:**
- Modify: `tools/validate-manifests/main.go` (the `main` loop, imports, one new function)
- Test: `tools/validate-manifests/thumbnail_test.go`

**Interfaces:**
- Consumes: `thumbnail.Validate`, `thumbnail.LoadGrandfathered`, `thumbnail.Encode`, `thumbnail.Width/Height`.
- Produces: `func checkThumbnail(pluginDir string, grandfathered bool) error`.

- [ ] **Step 1: Write the failing tests**

`tools/validate-manifests/thumbnail_test.go`:

```go
package main

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

func validThumbnail(t *testing.T) []byte {
	t.Helper()
	data, err := thumbnail.Encode(image.NewNRGBA(image.Rect(0, 0, thumbnail.Width, thumbnail.Height)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func plugin(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCheckThumbnail(t *testing.T) {
	good := validThumbnail(t)
	small, err := thumbnail.Encode(image.NewNRGBA(image.Rect(0, 0, 100, 100)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		files map[string][]byte
		grand bool
		want  string // substring of the error; empty means no error
	}{
		{"complete", map[string][]byte{"screenshot.png": []byte("x"), "thumbnail.webp": good}, false, ""},
		{"no thumbnail", map[string][]byte{"screenshot.png": []byte("x")}, false, "missing thumbnail.webp"},
		{"no screenshot", map[string][]byte{"thumbnail.webp": good}, false, "missing screenshot.png"},
		{"wrong size", map[string][]byte{"screenshot.png": []byte("x"), "thumbnail.webp": small}, false, "100x100"},
		{"not webp", map[string][]byte{"screenshot.png": []byte("x"), "thumbnail.webp": []byte("nope")}, false, "not a WebP"},
		{"grandfathered and empty", map[string][]byte{}, true, ""},
		{"grandfathered but has thumbnail", map[string][]byte{"thumbnail.webp": good}, true, "remove it from tools/thumbnail/grandfathered.txt"},
		{"grandfathered but has screenshot", map[string][]byte{"screenshot.png": []byte("x")}, true, "remove it from tools/thumbnail/grandfathered.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkThumbnail(plugin(t, tc.files), tc.grand)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestGrandfatheredNamesMustBePlugins(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plugins", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := checkGrandfatheredExist(root, map[string]bool{"notes": true, "ghost": true})
	if err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("err = %v, want one naming ghost", err)
	}
	if err := checkGrandfatheredExist(root, map[string]bool{"notes": true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The shipped repository must satisfy its own rule.
func TestShippedPluginsMeetTheThumbnailRule(t *testing.T) {
	root := "../.."
	grand, err := thumbnail.LoadGrandfathered(filepath.Join(root, "tools/thumbnail/grandfathered.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkGrandfatheredExist(root, grand); err != nil {
		t.Fatal(err)
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "plugins", "*", "manifest.json"))
	for _, m := range dirs {
		d := filepath.Dir(m)
		if err := checkThumbnail(d, grand[filepath.Base(d)]); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -count=1 -p 2 -run 'Thumbnail|Grandfathered' ./tools/validate-manifests/`
Expected: FAIL to build, `undefined: checkThumbnail`, `undefined: checkGrandfatheredExist`.

- [ ] **Step 3: Implement**

In `tools/validate-manifests/main.go`, add to the import block:

```go
	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
```

Replace the plugin loop in `main()` (the block from `seenIDs := map[string]string{}` through its closing `}` of the `for`) with:

```go
	grand, err := thumbnail.LoadGrandfathered("tools/thumbnail/grandfathered.txt")
	if err != nil {
		fatal(err)
	}
	failures := 0
	if err := checkGrandfatheredExist(".", grand); err != nil {
		fmt.Printf("FAIL tools/thumbnail/grandfathered.txt: %v\n", err)
		failures++
	}

	seenIDs := map[string]string{}
	for _, dir := range roots {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		path := filepath.Join(dir, "manifest.json")
		if err := validate(path, seenIDs); err != nil {
			fmt.Printf("FAIL %s: %v\n", path, err)
			failures++
		} else {
			fmt.Printf("ok   %s\n", path)
		}
		if err := checkThumbnail(dir, grand[filepath.Base(dir)]); err != nil {
			fmt.Printf("FAIL %s: %v\n", dir, err)
			failures++
		}
	}
```

(The original declared `failures := 0` above the loop; the replacement declares it once, before `checkGrandfatheredExist`. Remove the original declaration so it is not declared twice.)

Add to the end of the file:

```go
// checkThumbnail enforces the catalog-thumbnail rule for one plugin
// directory. A grandfathered plugin must have neither file yet, so the list
// can only shrink; any other plugin must have a capture and a valid
// thumbnail.
func checkThumbnail(pluginDir string, grandfathered bool) error {
	shot := filepath.Join(pluginDir, "screenshot.png")
	thumb := filepath.Join(pluginDir, "thumbnail.webp")
	regenerate := "go run ./tools/thumbnail -plugin plugins/" + filepath.Base(pluginDir)

	if grandfathered {
		for _, p := range []string{shot, thumb} {
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("has %s but is still listed; remove it from tools/thumbnail/grandfathered.txt", filepath.Base(p))
			}
		}
		return nil
	}
	if _, err := os.Stat(shot); err != nil {
		return fmt.Errorf("missing screenshot.png; capture the plugin's panel (docs/publishing.md, \"Thumbnails\")")
	}
	data, err := os.ReadFile(thumb)
	if err != nil {
		return fmt.Errorf("missing thumbnail.webp; run: %s", regenerate)
	}
	if err := thumbnail.Validate(data); err != nil {
		return fmt.Errorf("thumbnail.webp %w; regenerate with: %s", err, regenerate)
	}
	return nil
}

// checkGrandfatheredExist rejects a list entry that names no plugin, so a
// typo cannot silently exempt nothing (or a plugin added later).
func checkGrandfatheredExist(root string, grand map[string]bool) error {
	names := make([]string, 0, len(grand))
	for name := range grand {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(root, "plugins", name, "manifest.json")); err != nil {
			return fmt.Errorf("lists %q, which is not a plugin directory", name)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `gofmt -l tools/validate-manifests; go vet -p 2 ./tools/validate-manifests/ && go test -count=1 -p 2 ./tools/validate-manifests/`
Expected: `ok` (the shipped-repo test passes because all 16 plugins are grandfathered and have no files yet).

Then run the command itself: `go run ./tools/validate-manifests | tail -5`
Expected: only `ok   plugins/...` lines, exit status 0.

- [ ] **Step 5: Commit**

```bash
git add tools/validate-manifests
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(validate): require a screenshot and thumbnail for every non-grandfathered plugin"
```

---

### Task 6: Pin the tagged thumbnail in `catalog update`, and keep it out of archives

**Files:**
- Modify: `tools/catalog/update.go` (add `fetchThumbnail`; call it in `updateCatalog`; add a `screenshot` parameter to `mergeRelease`)
- Modify: `tools/catalog/package.go` (`collectEntries`)
- Modify: `tools/catalog/update_test.go` (`withTaggedContentServer`, new tests)
- Modify: `tools/catalog/package_test.go` (one table case)
- Create: `tools/catalog/main_test.go`

**Interfaces:**
- Consumes: `thumbnail.Validate`, `thumbnail.MaxBytes`, `thumbnail.Encode`.
- Produces: `func fetchThumbnail(repo, ref, pluginDir string) (shot *catalog.Screenshot, found bool, err error)` (Task 7 reuses it); test helpers `stubThumbnail(*http.Request) (*http.Response, bool)` and `withThumbnailResponse(t, status int, body []byte)`.

Design note: an existing `readPluginReadme` is left untouched to avoid changing its error text. `fetchThumbnail` is a sibling with the same shape.

- [ ] **Step 1: Make the existing tests hermetic about thumbnails**

Existing `updateCatalog` tests fetch the tagged README from a stubbed or real server and have no thumbnail. Create `tools/catalog/main_test.go`:

```go
package main

import (
	"bytes"
	"image"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

// stubThumbnailBytes is a valid blank thumbnail every test release "ships".
var stubThumbnailBytes = func() []byte {
	data, err := thumbnail.Encode(image.NewNRGBA(image.Rect(0, 0, thumbnail.Width, thumbnail.Height)))
	if err != nil {
		panic(err)
	}
	return data
}()

// stubThumbnail answers a request for a tagged thumbnail.webp with the valid
// stub; any other request is not its concern.
func stubThumbnail(r *http.Request) (*http.Response, bool) {
	if !strings.HasSuffix(r.URL.Path, "/thumbnail.webp") {
		return nil, false
	}
	return &http.Response{
		StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
		Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(stubThumbnailBytes)), Request: r,
	}, true
}

type thumbnailStub struct{ next http.RoundTripper }

func (s thumbnailStub) RoundTrip(r *http.Request) (*http.Response, error) {
	if resp, ok := stubThumbnail(r); ok {
		return resp, nil
	}
	return s.next.RoundTrip(r)
}

// TestMain makes every test release carry a thumbnail unless a test says
// otherwise. Other requests behave exactly as before.
func TestMain(m *testing.M) {
	catalogHTTPClient.Transport = thumbnailStub{next: http.DefaultTransport}
	os.Exit(m.Run())
}

// withThumbnailResponse makes the tagged thumbnail.webp answer with status
// and body for the rest of the test.
func withThumbnailResponse(t *testing.T, status int, body []byte) {
	t.Helper()
	prev := catalogHTTPClient.Transport
	catalogHTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/thumbnail.webp") {
			return &http.Response{
				StatusCode: status, Status: http.StatusText(status),
				Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r,
			}, nil
		}
		return prev.RoundTrip(r)
	})
	t.Cleanup(func() { catalogHTTPClient.Transport = prev })
}
```

In `tools/catalog/update_test.go`, make `withTaggedContentServer` answer thumbnails too: inside its `roundTripFunc`, directly after the `if r.URL.Host != "catalog-test.invalid" { ... }` block, add:

```go
		if resp, ok := stubThumbnail(r); ok {
			return resp, nil
		}
```

- [ ] **Step 2: Write the failing tests**

Append to `tools/catalog/update_test.go` (add `"net/http"`, `"strings"` etc. only if not already imported — they are):

```go
func TestUpdatePinsTheTaggedThumbnail(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	base := withReadmeServer(t, http.StatusNotFound, nil)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Screenshot == nil {
		t.Fatal("row has no screenshot")
	}
	if want := base + "/Nomadcxx/sysc-plugins/timer-v1.0.0/plugins/timer/thumbnail.webp"; e.Screenshot.URL != want {
		t.Errorf("screenshot URL = %q, want %q", e.Screenshot.URL, want)
	}
	sum := sha256.Sum256(stubThumbnailBytes)
	if e.Screenshot.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("screenshot sha256 = %q, want %x", e.Screenshot.SHA256, sum)
	}
}

func TestUpdateFailsWhenTheTagHasNoThumbnail(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	withReadmeServer(t, http.StatusNotFound, nil)
	withThumbnailResponse(t, http.StatusNotFound, nil)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "plugins/timer/thumbnail.webp") {
		t.Fatalf("err = %v, want one naming plugins/timer/thumbnail.webp", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "catalog.json")); statErr == nil {
		t.Error("catalog.json was written for a release with no thumbnail")
	}
}

func TestUpdateRejectsATaggedThumbnailOfTheWrongSize(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	withReadmeServer(t, http.StatusNotFound, nil)
	small, err := thumbnail.Encode(image.NewNRGBA(image.Rect(0, 0, 100, 100)))
	if err != nil {
		t.Fatal(err)
	}
	withThumbnailResponse(t, http.StatusOK, small)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	err = updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "100x100") {
		t.Fatalf("err = %v, want one naming the 100x100 size", err)
	}
}

func TestMetaScreenshotStillOverridesTheTaggedThumbnail(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	writeFile(t, filepath.Join(root, catalogMetaFile), `{
		"org.sysc.timer": {"category": "productivity", "author": "Nomadcxx", "license": "MIT",
			"screenshot": {"url": "https://example.com/timer.png", "sha256": "`+strings.Repeat("a", 64)+`"}}
	}`)
	withReadmeServer(t, http.StatusNotFound, nil)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Screenshot == nil || e.Screenshot.URL != "https://example.com/timer.png" {
		t.Fatalf("screenshot = %+v, want the catalog-meta.json override", e.Screenshot)
	}
}
```

Add to the file's imports if missing: `"crypto/sha256"`, `"encoding/hex"`, `"image"`, and `"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"`.

In `tools/catalog/package_test.go`, add a case to the `cases` slice in `TestCollectEntries` (after the `wallpaper-depth-like` case):

```go
		{
			name: "catalog-only media",
			id:   "org.sysc.notes",
			files: []string{
				"manifest.json",
				"README.md",
				"screenshot.png",
				"thumbnail.webp",
				"assets/screenshot.png",
			},
			wantIn: []string{
				"org.sysc.notes/manifest.json",
				"org.sysc.notes/README.md",
				// Only the plugin root's two catalog files are excluded.
				"org.sysc.notes/assets/screenshot.png",
			},
			wantOut: []string{
				"org.sysc.notes/screenshot.png",
				"org.sysc.notes/thumbnail.webp",
			},
		},
```

- [ ] **Step 2b: Run to verify they fail**

Run: `go test -count=1 -p 2 ./tools/catalog/`
Expected: FAIL: `TestUpdatePinsTheTaggedThumbnail` (nil screenshot), `TestUpdateFailsWhenTheTagHasNoThumbnail` (no error), `TestUpdateRejectsATaggedThumbnailOfTheWrongSize`, `TestMetaScreenshotStillOverrides...` may already pass, `TestCollectEntries/catalog-only_media` (entries present).

- [ ] **Step 3: Implement**

In `tools/catalog/update.go`, add `"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"` to the imports, then add after `readPluginReadme`:

```go
// fetchThumbnail returns a pin (URL and sha256) for the plugin's
// thumbnail.webp at ref, after checking the bytes really are a thumbnail. ref
// is a release tag for a release and a commit for a backfill. found is false
// when ref has no thumbnail, which each caller decides how to treat.
func fetchThumbnail(repo, ref, pluginDir string) (shot *catalog.Screenshot, found bool, err error) {
	url := fmt.Sprintf("%s/%s/%s/plugins/%s/thumbnail.webp", taggedFileBaseURL, repo, ref, pluginDir)
	resp, err := catalogHTTPClient.Get(url)
	if err != nil {
		return nil, false, fmt.Errorf("thumbnail: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("thumbnail %s: status %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, thumbnail.MaxBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("thumbnail %s: %w", url, err)
	}
	if err := thumbnail.Validate(data); err != nil {
		return nil, false, fmt.Errorf("thumbnail %s %w", url, err)
	}
	sum := sha256.Sum256(data)
	return &catalog.Screenshot{URL: url, SHA256: hex.EncodeToString(sum[:])}, true, nil
}
```

In `updateCatalog`, directly after the `readme, err := readPluginReadme(...)` block, add:

```go
	thumb, found, err := fetchThumbnail(repo, tag, pluginDir)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if !found {
		return fmt.Errorf("update: %s has no plugins/%[2]s/thumbnail.webp; generate one with `go run ./tools/thumbnail -plugin plugins/%[2]s` and tag again (a backport tag from before thumbnails needs one too)", tag, pluginDir)
	}
```

After `meta, ok := metaAll[manifest.ID]` and its `!ok` guard, add:

```go
	// catalog-meta.json can still pin a screenshot by hand; otherwise the
	// tagged thumbnail is the row's screenshot.
	screenshot := thumb
	if meta.Screenshot != nil {
		screenshot = meta.Screenshot.toCatalog()
	}
```

Change the call and signature:

```go
	entries = append(entries, mergeRelease(existing, newRelease, now, meta, manifest, readme, screenshot))
```

```go
func mergeRelease(existing *catalog.Entry, newRelease catalog.Release, now time.Time, meta catalogMeta, manifest pluginManifest, readme, screenshot *catalog.Screenshot) catalog.Entry {
```

In `mergeRelease`: in the fresh-row literal replace `Screenshot:      meta.Screenshot.toCatalog(),` with `Screenshot:      screenshot,`. In the existing-row branch, move the screenshot into the `!older` block and delete the later `if meta.Screenshot != nil { e.Screenshot = ... }`:

```go
	if !older {
		e.Release = newRelease
		e.Name = manifest.Name
		e.Description = manifest.Description
		e.Readme = readme
		e.Screenshot = screenshot
	}
```

and remove:

```go
	if meta.Screenshot != nil {
		e.Screenshot = meta.Screenshot.toCatalog()
	}
```

In `tools/catalog/package.go`, in `collectEntries`, immediately after the `if filepath.Ext(name) == ".go" { return nil }` check add:

```go
		// Catalog media is for the store, not the install: the screenshot and
		// thumbnail at the plugin root stay out of the archive.
		if rel == "screenshot.png" || rel == "thumbnail.webp" {
			return nil
		}
```

and extend the doc comment on `collectEntries` to mention them ("minus *.go source, testdata/, bin/, and the plugin root's screenshot.png and thumbnail.webp").

- [ ] **Step 4: Run to verify they pass**

Run: `gofmt -l tools/catalog; go vet -p 2 ./tools/catalog/ && go test -count=1 -p 2 ./tools/catalog/`
Expected: `ok`. If an existing update test now fails because it asserts a `screenshot` value, update that assertion to the new rule (tagged thumbnail wins unless `catalog-meta.json` overrides); do not weaken other assertions.

- [ ] **Step 5: Commit**

```bash
git add tools/catalog
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(catalog): pin the tagged thumbnail in release rows and keep catalog media out of archives"
```

---

### Task 7: `catalog thumbnails -ref <commit>` backfill

**Files:**
- Create: `tools/catalog/thumbnails.go`
- Modify: `tools/catalog/main.go` (verb dispatch, doc comment, usage)
- Test: `tools/catalog/thumbnails_test.go`

**Interfaces:**
- Consumes: `fetchThumbnail` (Task 6), `decodeCatalogFile`, `writeCatalog`, `pluginDirsByID`, `resolveRepo`, `defaultRepo`.
- Produces: verb `catalog thumbnails -ref <40-hex commit> [-repo owner/name] [-plugin <dir>]`; `func pinThumbnails(repoRoot, repo, ref, only string, log io.Writer) error`.

- [ ] **Step 1: Write the failing tests**

```go
package main

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeCommit = "0123456789abcdef0123456789abcdef01234567"

// releasedTimerRepo is a repository whose catalog already has a timer row.
func releasedTimerRepo(t *testing.T) string {
	t.Helper()
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	withReadmeServer(t, http.StatusNotFound, nil)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}
	return root
}

func TestThumbnailsPinsToTheCommit(t *testing.T) {
	root := releasedTimerRepo(t)
	var log bytes.Buffer
	if err := pinThumbnails(root, defaultRepo, fakeCommit, "", &log); err != nil {
		t.Fatalf("pinThumbnails: %v", err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Screenshot == nil || !strings.Contains(e.Screenshot.URL, "/"+fakeCommit+"/plugins/timer/thumbnail.webp") {
		t.Fatalf("screenshot = %+v, want a URL at commit %s", e.Screenshot, fakeCommit)
	}
	if e.Version != "1.0.0" {
		t.Errorf("version changed to %q; only the screenshot may change", e.Version)
	}
}

func TestThumbnailsSkipsRowsWithNoThumbnailAtTheCommit(t *testing.T) {
	root := releasedTimerRepo(t)
	before := readCatalogFile(t, filepath.Join(root, "catalog.json"))
	withThumbnailResponse(t, http.StatusNotFound, nil)
	var log bytes.Buffer
	if err := pinThumbnails(root, defaultRepo, fakeCommit, "", &log); err != nil {
		t.Fatalf("pinThumbnails: %v", err)
	}
	if !strings.Contains(log.String(), "skip org.sysc.timer") {
		t.Errorf("log = %q, want a skip line", log.String())
	}
	after := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if got, want := after.Screenshot, entryByID(t, before, "org.sysc.timer").Screenshot; (got == nil) != (want == nil) {
		t.Errorf("row changed: %+v vs %+v", got, want)
	}
}

func TestThumbnailsNamedPluginMustHaveOne(t *testing.T) {
	root := releasedTimerRepo(t)
	withThumbnailResponse(t, http.StatusNotFound, nil)
	err := pinThumbnails(root, defaultRepo, fakeCommit, "timer", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "plugins/timer/thumbnail.webp") {
		t.Fatalf("err = %v, want a missing-thumbnail failure naming the file", err)
	}
}

func TestThumbnailsRefMustBeAFullCommit(t *testing.T) {
	root := releasedTimerRepo(t)
	for _, ref := range []string{"main", "v1.0.0", "0123abc", ""} {
		err := pinThumbnails(root, defaultRepo, ref, "", &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "40-character commit") {
			t.Errorf("ref %q: err = %v, want a full-commit requirement", ref, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -count=1 -p 2 -run Thumbnails ./tools/catalog/`
Expected: FAIL to build, `undefined: pinThumbnails`.

- [ ] **Step 3: Implement**

`tools/catalog/thumbnails.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// commitPattern is a full commit hash. A branch or tag name would move, and a
// pinned thumbnail must never change under its sha256.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func runThumbnails(args []string) error {
	fs := flag.NewFlagSet("thumbnails", flag.ContinueOnError)
	ref := fs.String("ref", "", "full 40-character commit hash that holds the thumbnails")
	repoFlag := fs.String("repo", "", "repository as owner/name (default $GITHUB_REPOSITORY, then "+defaultRepo+")")
	only := fs.String("plugin", "", "limit to one plugin directory; it must have a thumbnail at the commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := resolveRepo(*repoFlag)
	if err != nil {
		return fmt.Errorf("thumbnails: %w", err)
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	return pinThumbnails(repoRoot, repo, *ref, *only, os.Stderr)
}

// pinThumbnails points each catalog row's screenshot at plugins/<dir>/
// thumbnail.webp as it is at the commit ref. It is how rows released before
// thumbnails existed get one without a new version. Only screenshot changes.
// A row whose plugin has no thumbnail at ref is skipped with a note, unless
// only names it.
func pinThumbnails(repoRoot, repo, ref, only string, log io.Writer) error {
	if !commitPattern.MatchString(ref) {
		return fmt.Errorf("thumbnails: -ref must be a full 40-character commit hash, got %q", ref)
	}
	catalogPath := filepath.Join(repoRoot, "catalog.json")
	cat, err := decodeCatalogFile(catalogPath)
	if err != nil {
		return fmt.Errorf("thumbnails: %w", err)
	}
	dirs, err := pluginDirsByID(repoRoot)
	if err != nil {
		return fmt.Errorf("thumbnails: %w", err)
	}

	for i := range cat.Entries {
		e := &cat.Entries[i]
		dir, ok := dirs[e.ID]
		if !ok {
			return fmt.Errorf("thumbnails: no plugins/<dir> declares id %q", e.ID)
		}
		if only != "" && dir != only {
			continue
		}
		shot, found, err := fetchThumbnail(repo, ref, dir)
		if err != nil {
			return fmt.Errorf("thumbnails: %w", err)
		}
		if !found {
			if only != "" {
				return fmt.Errorf("thumbnails: plugins/%s/thumbnail.webp does not exist at %s", dir, ref)
			}
			fmt.Fprintf(log, "skip %s: no thumbnail at %s\n", e.ID, ref)
			continue
		}
		e.Screenshot = shot
	}
	return writeCatalog(catalogPath, cat.Entries)
}
```

In `tools/catalog/main.go`: add to the doc comment verbs block (change "three verbs" to "four verbs") the line `//	catalog thumbnails -ref <commit> [-plugin <dir>]`; add to the switch:

```go
	case "thumbnails":
		err = runThumbnails(args)
```

and to `usage()`:

```
  thumbnails -ref <40-hex commit> [-repo owner/name] [-plugin <dir>]
```

- [ ] **Step 4: Run to verify they pass**

Run: `gofmt -l tools/catalog; go vet -p 2 ./tools/catalog/ && go test -count=1 -p 2 ./tools/catalog/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add tools/catalog
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(catalog): add the thumbnails verb to backfill existing rows"
```

---

### Task 8: CI, Makefile and documentation

**Files:**
- Modify: `.github/workflows/ci.yml`, `Makefile`, `docs/publishing.md`, `docs/writing-plugins.md`, `README.md`, `ATTRIBUTION.md`

**Interfaces:** none (configuration and prose). The documented commands must be exactly the ones the tools print: `go run ./tools/thumbnail -plugin plugins/<dir>` and `go run ./tools/thumbnail -check`.

- [ ] **Step 1: CI and Makefile**

In `.github/workflows/ci.yml`, after the `validate manifests` step add:

```yaml
      - name: check thumbnails
        run: go run ./tools/thumbnail -check
```

In `Makefile`, change `.PHONY` to include `thumbnails` and add after `validate:`:

```make
thumbnails:
	go run ./tools/thumbnail -check
```

- [ ] **Step 2: Verify both commands against the real repo**

Run: `go run ./tools/thumbnail -check && go run ./tools/validate-manifests | tail -3`
Expected: `-check` prints nothing and exits 0 (all 16 plugins are grandfathered, so it checks none); validate-manifests prints only `ok` lines.

- [ ] **Step 3: `docs/publishing.md`**

Replace the whole `## Screenshot guidance` section (from that heading through the paragraph ending "required for the community catalog.") with:

```markdown
## Thumbnails

Every plugin in this repository ships two files next to its `manifest.json`:

- `screenshot.png`, a real capture of the plugin at work, and
- `thumbnail.webp`, the 960×540 catalog card the shell shows in its plugin
  store, generated from the manifest, the plugin's category and the capture.

Capture the panel in a representative state: a widget with real-looking data,
a panel open, not an empty or loading view. A plugin with only a bar widget
uses a crop of the bar. The capture must be a PNG, at least 320 px wide and at
most 4 MiB; a tall panel bleeds off the card with a fade, a short or wide one
is centred whole.

Then generate the thumbnail:

```sh
go run ./tools/thumbnail -plugin plugins/<dir>
```

It writes `plugins/<dir>/thumbnail.webp` (lossless, under 512 KB). Commit both
files. The image shows only the plugin's id, name, description, category,
whether it has a panel and a bar widget, and the capture, never a version, so a
release does not make it stale. Changing any of those does: CI runs
`go run ./tools/thumbnail -check` and fails with the command above until you
regenerate. `make thumbnails` runs the same check locally.

`validate-manifests` fails a plugin that lacks either file, and `catalog
update` refuses a release whose tagged tree has no valid thumbnail. Both files
stay out of the install archive.

Plugins listed in `tools/thumbnail/grandfathered.txt` predate this rule. The
list only shrinks: a plugin leaves it in the PR that adds its two files, and a
new plugin is never added to it.

For rows released before thumbnails existed, `go run ./tools/catalog
thumbnails -ref <full commit hash>` points each row's `screenshot` at the
thumbnail at that commit without a new version.

A thumbnail named by `catalog-meta.json`'s `screenshot` field still overrides
the tagged one, for catalogs built from other repositories. The community
catalog requires a screenshot on every row.
```

Also in the "What goes in an asset" list, change "everything else in the plugin's directory except `*.go` files, `testdata/`, and `bin/`" to "everything else in the plugin's directory except `*.go` files, `testdata/`, `bin/`, and the root `screenshot.png` and `thumbnail.webp`".

In the `catalog-meta.json` fields table, change the `screenshot` row's second cell to `no` and its Notes to ``Overrides the tagged `thumbnail.webp`; normally omit it.``

- [ ] **Step 4: `docs/writing-plugins.md`**

In "Adding a plugin", after item 3 insert:

```markdown
4. Capture the plugin at work as `plugins/<name>/screenshot.png` and generate
   its catalog card with `go run ./tools/thumbnail -plugin plugins/<name>`;
   commit both. CI fails a new plugin without them. See "Thumbnails" in
   [publishing.md](publishing.md).
```

and renumber the old item 4 to 5.

- [ ] **Step 5: `README.md` and `ATTRIBUTION.md`**

In `README.md`, in the "Writing plugins" bullet list, change the Publishing bullet to:

```markdown
- [Publishing](docs/publishing.md): per-plugin release tags, the catalog, the thumbnail every plugin ships, and running
  your own plugin source
```

Append to `ATTRIBUTION.md`:

```markdown

## Thumbnail assets

The catalog thumbnails are rendered by `internal/thumbnail` with these embedded
assets:

| Asset | Origin | Licence |
|---|---|---|
| Inter (Regular, ExtraBold), subset | [rsms/inter](https://github.com/rsms/inter) | SIL OFL 1.1 |
| JetBrains Mono (Bold), subset | [JetBrains/JetBrainsMono](https://github.com/JetBrains/JetBrainsMono) | SIL OFL 1.1 |
| sysc wordmark | sysc-shell (`internal/render/icons/wordmark/sysc-mark.png`) | same project |
| `github.com/HugoSmits86/nativewebp` | pure-Go WebP encoder | MIT |

The card layout follows the idea of Noctalia's community-plugins
`thumbnail.webp` requirement (one fixed size, enforced in CI); the design is
sysc's own.
```

- [ ] **Step 6: Final verification**

Run each, one at a time:

```bash
gofmt -l .
go vet -p 2 ./internal/thumbnail/ ./tools/thumbnail/ ./tools/validate-manifests/ ./tools/catalog/
go test -count=1 -p 2 ./internal/thumbnail/
go test -count=1 -p 2 ./tools/thumbnail/
go test -count=1 -p 2 ./tools/validate-manifests/
go test -count=1 -p 2 ./tools/catalog/
go run ./tools/validate-manifests | tail -3
go run ./tools/thumbnail -check
go run ./tools/catalog validate
```

Expected: no gofmt output, every test package `ok`, validate-manifests prints only `ok` lines, `-check` is silent, `catalog validate` passes. `go run ./tools/catalog validate` needs network (it fetches tagged manifests); if it is offline, say so rather than skipping silently.

Also render one real card end to end and look at it, using a scratch copy of the repo layout so nothing in the worktree changes. The tool runs from the repository root, so build it once and run it from the scratch root:

```bash
go build -o "$SCRATCH/thumbnail" ./tools/thumbnail
rm -rf "$SCRATCH/e2e" && mkdir -p "$SCRATCH/e2e/plugins"
cp -r plugins/notes "$SCRATCH/e2e/plugins/notes"
cp catalog-meta.json "$SCRATCH/e2e/"
cp ~/sysc-shell-shots/final-browse.png "$SCRATCH/e2e/plugins/notes/screenshot.png"
(cd "$SCRATCH/e2e" && "$SCRATCH/thumbnail" -plugin plugins/notes)
magick "$SCRATCH/e2e/plugins/notes/thumbnail.webp" "$SCRATCH/e2e.png"
```

Open `$SCRATCH/e2e.png`. It should be the real Notes card: `org.sysc.notes` in the title bar, the manifest's name and description, `[ PRODUCTIVITY ] [ PANEL ] [ BAR ]` chips, and the stand-in panel. (`final-browse.png` is a launcher capture, used only as a stand-in.)

- [ ] **Step 7: Commit and open the PR**

```bash
git add .github Makefile docs README.md ATTRIBUTION.md
BEADS_DB="$SCRATCH/beads.db" git commit -m "docs,ci: require plugin thumbnails and document the capture workflow"
git push -u origin feat/plugin-thumbnails
gh pr create --base main --title "feat: generated catalog thumbnails for every plugin" --body "..."
```

PR body: summarise the spec (link `docs/superpowers/specs/2026-10-08-plugin-thumbnails-design.md`), state that the 16 existing plugins are grandfathered and the backfill follows in a separate PR needing laptop captures, and list what was run. End with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`. The PR includes the spec and this plan; if the spec PR (`docs/plugin-thumbnails-spec`) is open, close it in favour of this one or merge it first.

---

## Self-Review

**Spec coverage**

| Spec item | Task |
|---|---|
| Go-only generator, B1 look, 960×540 lossless WebP, deterministic | 1, 2, 4 |
| Inputs: manifest id/name/description, widgets/panels chips, category, screenshot | 4 (`loadInput`), 2 |
| Version dropped, id shown | 2 (`paintTitleBar`) |
| Fit rules: name ≤2 lines (error), description ≤3 lines (ellipsis + warning), screenshot bleed vs centred | 2 |
| Fonts, wordmark embedded; OFL notices | 1, 8 (`ATTRIBUTION.md`) |
| `-check` regenerate-and-compare | 4 |
| Screenshot validation (PNG, ≥320 px, ≤4 MiB) | 4 |
| `validate-manifests` rule, grandfather ratchet (shrink-only, nonexistent names) | 3, 5 |
| `catalog update` pins tagged thumbnail, fails without; meta override stays | 6 |
| `catalog thumbnails -ref` backfill | 7 |
| CI runs `-check`; Makefile target | 8 |
| Docs (publishing, writing-plugins, README, ATTRIBUTION) | 8 |
| Backfill of the 16 plugins | out of scope here (separate PR/plan) |
| **Not in the spec, found while planning:** the archive packer would ship `screenshot.png` and `thumbnail.webp` inside every install | 6 (excluded from `collectEntries`, tested) |

**Spec deviations to carry into review:** (1) Task 6 adds the archive exclusion above. (2) Task 6's `update` requires a thumbnail for every tag, including a backport tag cut from before thumbnails existed; that is deliberate (a row without a screenshot would break `-community`) and the error says so. (3) The breadcrumb drops `sysc://` when the directory name is long (`plugins/github-notifications`), a fit rule the HTML mock did not need.

**Placeholder scan:** none. The PR body in Task 8 Step 7 is described rather than quoted because it depends on what was run; every code step contains its code.

**Type consistency:** `Input`/`Render`/`Encode`/`Validate`/`MaxBytes`/`Width`/`Height` are defined in Tasks 1–2 and used unchanged in 3–7. `LoadGrandfathered(path) (map[string]bool, error)` (Task 3) is used by Tasks 4 and 5. `fetchThumbnail(repo, ref, pluginDir) (*catalog.Screenshot, bool, error)` (Task 6) is used by Task 7. `mergeRelease` gains a trailing `screenshot` parameter in Task 6 only; no other caller exists outside `updateCatalog`. The regenerate command string is identical in Tasks 4, 5, 8.

**Review Focus:** all five lines have a named test (Tasks 2 and 4); the extras are covered in Tasks 5 and 6.

**Risks the executor should know**
- *Float determinism across architectures:* rendering uses `float64` math; Go may fuse multiply-add on arm64, so the same inputs could differ by a pixel there. Thumbnails are generated and checked on amd64 (CI is `ubuntu-latest`, dev machines are amd64). If a check ever fails only on another architecture, that is the cause.
- *Fonts:* Inter at 54 px rendered by Go's rasteriser (hinting off) is close to, not identical to, the browser mock. The prototype was compared against the mock and matches the approved layout.
- *Encoder:* `nativewebp` is a single-maintainer library; its lossless output decodes in `x/image/webp` and ImageMagick (verified).
