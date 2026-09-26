# Publishing a plugin release

This is the author-facing guide to releasing a plugin from this repository
and to the shape any third-party source repository needs to be readable by
sysc-shell's built-in plugin store. The mechanics are also described, from
the tooling side, in
[docs/plans/2026-09-25-plugin-release-pipeline.md](plans/2026-09-25-plugin-release-pipeline.md).

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
`arm64`, built with `CGO_ENABLED=0`. Inside is a single top-level directory
named for the plugin's `id` (for example `org.sysc.timer/`), holding:

- `manifest.json`, unchanged from the plugin's own;
- everything else in the plugin's directory except `*.go` files,
  `testdata/`, and `bin/` (the tool replaces `bin/` with the binary it just
  built);
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
| `screenshot` | conditionally | `{"url": "...", "sha256": "..."}`. Required for the community catalog (see below). |

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
`capabilities` and `requires` agree with its plugin's `manifest.json`. It
does not download anything.

`go run ./tools/catalog validate -fetch` additionally downloads every asset
and screenshot the catalog names and checks its size and sha256 — the same
check the release workflow runs with `-fetch` right before it opens the
catalog PR, against the freshly published release.

## Screenshot guidance

A screenshot should show the plugin in a representative state — a widget
with real-looking data, a panel open, not an empty or loading view. Any
aspect ratio is fine. It must be at most 2 MiB and 1920×1080, and its
`sha256` in `catalog-meta.json` (or, for the community catalog, in that
repository's own metadata) must match the file exactly. A screenshot is
optional for this repository's own catalog and required for the community
catalog.

## Publishing your own source

Any git repository can be a plugin source: sysc-shell's plugin store reads a
`catalog.json` at the root of the default branch. To publish your own:

1. Copy `tools/catalog` and `.github/workflows/release.yml` into your
   repository.
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
