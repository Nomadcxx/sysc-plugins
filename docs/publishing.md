# Publishing a plugin release

This is the author-facing guide to releasing a plugin from this repository
and to the shape any third-party source repository needs to be readable by
sysc-shell's built-in plugin store.

## Tags

Releases are per plugin, never per repository:

```
<dir>-v<version>
```

`<dir>` is the plugin's directory name under `plugins/`, and `<version>` is
its `manifest.json` version, exactly. Examples: `timer-v1.4.0`,
`github-notifications-v0.3.0`, `wallpaper-depth-v1.1.0`.

Push the tag once the plugin's `manifest.json` version has already been
bumped and committed to `main`. `.github/workflows/release.yml` triggers on
tags matching `*-v*`, builds both arches, publishes the GitHub release, and
opens a pull request against `main` carrying the regenerated `catalog.json`.
CI never pushes to `main` directly; a human merges that PR.

## What goes in an asset

Each asset is `<id>-<version>-linux-<arch>.tar.gz` for `arch` in `amd64` and
`arm64`. Pure-Go plugins build with `CGO_ENABLED=0`. The `cgoPlugins` map in
`tools/catalog/package.go` lists the exceptions. Today, `calendar` builds with
`CGO_ENABLED=1` and needs `libecal2.0-dev` on a runner that matches the target
architecture. Inside each asset is a single top-level directory named for the
plugin's `id` (for example `org.sysc.timer/`), holding:

- `manifest.json`, unchanged from the plugin's own;
- everything else in the plugin's directory except `*.go` files,
  `testdata/`, `bin/` (the tool replaces `bin/` with the binary it just
  built), and the root `screenshot.png` and `thumbnail.webp` (catalog media,
  not part of the install);
- the built executable at the manifest's `exec` path, executable.

This is exactly what `go run ./tools/catalog package -plugin <dir> -arch
<amd64|arm64> -out <dist>` produces, and it's deterministic: the same
tagged commit always produces the same archive bytes, so packaging twice
and diffing the output is a safe way to check for accidental drift before
tagging.

## `catalog-meta.json` fields

`manifest.json` supplies `name`, `description`, `version`, `protocol`,
`capabilities` and `requires` — the fields that must never disagree with
what actually shipped in the archive. Everything else a listing needs lives
in `catalog-meta.json` at the repository root, keyed by plugin id:

| Field | Required | Notes |
|---|---|---|
| `category` | yes | One of the closed set in `plugin/catalog.Categories`: `utilities`, `monitoring`, `system`, `appearance`, `productivity`, `media`, `audio`, `networking`, `weather`, `finance`, `social`. |
| `author` | yes | Display name. |
| `license` | no | SPDX identifier, matching the repository's `LICENSE`. |
| `homepage` | no | Must be `https`. |
| `long_description` | no | Plain text, no markup; shown on the detail view. |
| `screenshot` | no | `{"url": "...", "sha256": "..."}`. Overrides the tagged `thumbnail.webp`; normally omit it (see [Thumbnails](#thumbnails)). |

Add or update your plugin's row here before tagging its first release.
`tools/catalog update` refuses a release whose plugin has no
`catalog-meta.json` entry.

## Validating before you tag

Run the same check CI runs:

```sh
make catalog-validate
# or directly:
go run ./tools/catalog validate
```

This confirms `catalog.json` decodes cleanly, every row has a
`catalog-meta.json` entry, and every row's `name`, `description`, `protocol`,
`capabilities` and `requires` agree with `manifest.json` on that release
tag. The working tree is not the source of those fields: it may have moved
on after the tag. This step fetches the tagged manifests; it does not
download assets.

`go run ./tools/catalog validate -fetch` additionally downloads every asset
and screenshot the catalog names and checks its size and sha256 — the same
check the release workflow runs with `-fetch` right before it opens the
catalog PR, against the freshly published release.

## Thumbnails

Every plugin in this repository ships two files next to its `manifest.json`:

- `screenshot.png`, a real capture of the plugin at work, and
- `thumbnail.webp`, the 960×540 catalog card the shell shows in its plugin
  store, generated from the manifest, the plugin's category and the capture.

Capture the plugin in a representative state: a widget with real-looking
data, a panel open, not an empty or loading view. A plugin with only a bar
widget uses a crop of the bar. The capture must be a PNG, at least 320 px wide
and at most 4 MiB; a tall panel bleeds off the card with a fade, a short or
wide one is centred whole.

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

## Publishing your own source

Any git repository can be a plugin source: sysc-shell's plugin store reads a
`catalog.json` at the root of the default branch. To publish your own:

1. Copy `tools/catalog` and `.github/workflows/release.yml` into your
   repository. The tool writes URLs for the repository that published the
   release: in Actions it takes that from `GITHUB_REPOSITORY`, and
   `update -repo <owner>/<name>` sets it explicitly for local runs.
2. Keep `catalog.json` at the root of the default branch — that's the one
   file the store actually reads. Everything else (`catalog-meta.json`, the
   workflow, this guide) is authoring machinery.
3. Tag a release the same way: `<dir>-v<version>`, pushed once the manifest
   version is committed.
4. Add your repository's URL as a source in sysc-shell's Settings → Plugins
   → Sources.

`git archive` output — the plain tarball GitHub generates for a tag or
commit, not a `catalog`-built release asset — is accepted anywhere this
repository accepts a gzipped tar: it carries a `pax_global_header` entry
ahead of the real tree, which the shell's extractor skips.
