# Wallpaper Depth Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Generate a depth mask for each active image wallpaper and use it to occlude one centred shell-owned desktop clock behind foreground scenery.

**Architecture:** The plugin polls two capability-gated wallpaper host calls, serializes the existing Python helper across outputs, and registers only current masks. sysc-shell validates each mask, renders a compact click-through clock on the Wayland Bottom layer, and applies the mask to its premultiplied pixels with the same geometry as the configured wallpaper mode.

**Tech Stack:** Go 1.26, plugin/v1 additive wire minor 7, wlroots layer-shell auxiliary surfaces, sysc-shell CPU renderer, Python 3.11–3.14, ONNX Runtime, Depth Anything V2 Small, Pillow.

---

Design: `docs/plans/2026-09-23-wallpaper-depth-design.md`.

## Working rules

- Use separate worktrees for `sysc-shell` and `sysc-plugins`; invoke `superpowers:using-git-worktrees` before Task 1.
- Preserve the existing untracked shell tests and `.tmp-thumbcheck/`, and the unrelated untracked plugin documents.
- Run only the named package tests below. This machine cannot sustain `go test ./...` or repository-wide `-race`.
- Complete and push the shell commits before moving the plugin module pin. Do not add a local `replace` directive.
- Keep the protocol surface to `wallpaper.snapshot` and `wallpaper.mask.set`.
- Use the current `plugins/wallpaper-depth/depth_helper.py`; do not rewrite the model pipeline.

## Baseline repair

### Task 0: Close idle weather HTTP connections

The unchanged shell baseline currently fails
`TestClosingTheRegistryStopsTheWeatherGoroutine`. A goroutine dump after
`Registry.Close` shows the weather worker has stopped, while the standard
library HTTP transport retains one idle TLS connection through its
`persistConn.readLoop` and `persistConn.writeLoop`. This is tracked as
`sysc-500` and must be repaired before Task 1.

**Files:**
- Modify: `/home/nomadx/sysc-shell/internal/services/weather.go`
- Modify: `/home/nomadx/sysc-shell/internal/services/weather_test.go`

**Step 1: Write the failing transport cleanup test**

Install a small test `http.RoundTripper` on the weather client's
`Transport`. Give it a `CloseIdleConnections` method and assert that one
call to `Weather.Close` invokes that method. Keep this at the service
boundary; do not make the shell test inspect process-wide goroutine stacks.

**Step 2: Run the failing test**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/services -run 'WeatherClose'
```

Expected: FAIL because `Weather.Close` stops its worker but does not close
idle connections owned by its HTTP client.

**Step 3: Close the installed client's idle connections**

After the weather worker has stopped, call the standard library
`http.Client.CloseIdleConnections`. Keep `Close` idempotent and do not
replace the transport or disable keep-alives globally.

**Step 4: Run the focused proof**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/services -run 'WeatherClose'
timeout 300s env GOMAXPROCS=2 go test -count=3 ./internal/shell -run '^TestClosingTheRegistryStopsTheWeatherGoroutine$'
```

Expected: PASS; the registry test no longer leaves the transport's read and
write loops behind.

**Step 5: Commit**

```bash
git add .beads/issues.jsonl internal/services/weather.go internal/services/weather_test.go
git commit -m "fix(weather): close idle HTTP connections"
```

## Phase A: sysc-shell

### Task 1: Declare wire minor 7 and the wallpaper capability

**Files:**
- Modify: `/home/nomadx/sysc-shell/plugin/v1/message.go`
- Modify: `/home/nomadx/sysc-shell/plugin/v1/framing_test.go`
- Modify: `/home/nomadx/sysc-shell/internal/plugin/manifest.go`
- Modify: `/home/nomadx/sysc-shell/internal/plugin/manifest_test.go`
- Modify: `/home/nomadx/sysc-shell/internal/plugin/supervisor.go`
- Modify: `/home/nomadx/sysc-shell/internal/plugin/supervisor_test.go`

**Step 1: Write the failing protocol and manifest tests**

Add coverage that:

- a manifest may declare `"wallpaper"`;
- protocol minor 7 passes the supervisor's manifest and handshake bounds;
- minor 8 remains incompatible;
- both call payloads round-trip through JSON.

Use these wire shapes in `plugin/v1/message.go`:

```go
const (
	CallWallpaperSnapshot CallKind = "wallpaper.snapshot"
	CallWallpaperMaskSet  CallKind = "wallpaper.mask.set"
)

type WallpaperOutputState string

const (
	WallpaperImage         WallpaperOutputState = "image"
	WallpaperVideo         WallpaperOutputState = "video"
	WallpaperNone          WallpaperOutputState = "none"
	WallpaperTransitioning WallpaperOutputState = "transitioning"
	WallpaperCovered       WallpaperOutputState = "covered"
)

type WallpaperOutput struct {
	Output string               `json:"output"`
	State  WallpaperOutputState `json:"state"`
	Path   string               `json:"path,omitempty"`
}

type WallpaperSnapshotResult struct {
	Revision uint64            `json:"revision"`
	Scale    string            `json:"scale"`
	Outputs  []WallpaperOutput `json:"outputs"`
}

type WallpaperMaskSetParams struct {
	Output        string `json:"output"`
	WallpaperPath string `json:"wallpaper_path"`
	MaskPath      string `json:"mask_path,omitempty"`
}
```

**Step 2: Run the tests and confirm the current bounds fail**

Run:

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugin/v1 ./internal/plugin -run 'Wallpaper|MinorSeven|Capabilities'
```

Expected: FAIL because `wallpaper` is unknown and the supervisor caps the protocol at minor 6.

**Step 3: Add the minimum declarations**

Add `CapWallpaper Capability = "wallpaper"` to `knownCapabilities`. Change both supervisor minor bounds from 6 to 7. Keep the existing supported-version list, which already includes `{Major: 1, Minor: 7}`.

**Step 4: Run the focused tests**

Run the command from Step 2.

Expected: PASS.

**Step 5: Commit**

```bash
git add plugin/v1/message.go plugin/v1/framing_test.go internal/plugin/manifest.go internal/plugin/manifest_test.go internal/plugin/supervisor.go internal/plugin/supervisor_test.go
git commit -m "feat(plugin): declare wallpaper calls"
```

### Task 2: Dispatch the two capability-gated calls

**Files:**
- Modify: `/home/nomadx/sysc-shell/internal/plugin/hostcall.go`
- Modify: `/home/nomadx/sysc-shell/internal/plugin/hostcall_test.go`

**Step 1: Write failing dispatcher tests**

Cover:

- both calls fail without `CapWallpaper`;
- `wallpaper.snapshot` returns the callback result;
- `wallpaper.mask.set` passes the decoded descriptor to its callback;
- missing callbacks return named errors;
- extra JSON fields and empty output names fail before a callback runs.

Extend `CallEnv` with:

```go
WallpaperSnapshot func(context.Context) (v1.WallpaperSnapshotResult, error)
WallpaperMaskSet  func(context.Context, v1.WallpaperMaskSetParams) error
```

**Step 2: Run the failing test**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/plugin -run 'HostCallWallpaper'
```

Expected: FAIL because the dispatcher reports both call names as unknown.

**Step 3: Implement the narrow dispatcher branch**

Check `CapWallpaper` before decoding either request. Decode these two payloads with a local strict decoder using `json.Decoder.DisallowUnknownFields`; keep the behavior of existing calls unchanged. Require `Output` for mask set. Require `WallpaperPath` when `MaskPath` is non-empty.

**Step 4: Run the test**

Run the command from Step 2.

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/plugin/hostcall.go internal/plugin/hostcall_test.go
git commit -m "feat(plugin): dispatch wallpaper calls"
```

### Task 3: Project shell wallpaper state for plugins

**Files:**
- Create: `/home/nomadx/sysc-shell/internal/shell/pluginwallpaper.go`
- Create: `/home/nomadx/sysc-shell/internal/shell/pluginwallpaper_test.go`
- Modify: `/home/nomadx/sysc-shell/internal/shell/pluginhost.go`

**Step 1: Write the failing projection tests**

Build table cases from `wallpaper.Snapshot`:

- connected image assignment becomes `image` with its path;
- connected video becomes `video`;
- no assignment becomes `none`;
- `Runtime.State == wallpaper.StateStarting` becomes `transitioning`;
- an entry in `Covered` becomes `covered` even when an assignment exists;
- outputs sort by connector;
- an unchanged projection keeps its revision;
- any output state, path, or scale change increments the revision once.

Use a small state holder:

```go
type pluginWallpaperProjection struct {
	mu       sync.Mutex
	revision uint64
	last     string
}
```

The fingerprint may be a deterministic string over scale and sorted outputs. Do not introduce a general hashing service.

**Step 2: Run the failing test**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/shell -run 'PluginWallpaperSnapshot'
```

Expected: FAIL because the projector does not exist.

**Step 3: Implement the projection and callback**

Add one projector to `pluginHost`. Its snapshot callback reads `Registry.wallpaperSvc` under `Registry.mu`, releases that lock, then calls `Service.Snapshot()`. Read `cfg.Wallpaper.Scale` with the service pointer. Return a named unavailable error when tests or startup have no wallpaper service.

Wire `WallpaperSnapshot` into each runtime's `CallEnv`. Add `CapWallpaper` to `hostPluginCaps`.

**Step 4: Run the tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/shell ./internal/plugin -run 'PluginWallpaperSnapshot|GrantedCapabilities'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/shell/pluginwallpaper.go internal/shell/pluginwallpaper_test.go internal/shell/pluginhost.go
git commit -m "feat(shell): expose wallpaper snapshots"
```

### Task 4: Decode masks and apply wallpaper geometry

**Files:**
- Create: `/home/nomadx/sysc-shell/internal/wallpaper/depthmask.go`
- Create: `/home/nomadx/sysc-shell/internal/wallpaper/depthmask_test.go`

**Step 1: Write failing decode, geometry, and pixel tests**

Pin these bounds in the test and implementation:

```go
const (
	MaxDepthMaskFileBytes = 64 << 20
	MaxDepthMaskPixels    = 64 << 20
)
```

Cover:

- a grayscale PNG with the wallpaper's dimensions loads as `*image.Alpha`;
- mismatched dimensions, oversized headers, non-PNG masks, missing files, and non-regular files fail;
- JPEG, PNG, GIF, and WebP wallpaper headers supply dimensions;
- `fill` centre-crops, `stretch` maps the full source, `original` centres native pixels, and `panscan` follows gSlapper's `panscan=1.0` mapping;
- landscape and portrait sources map correctly at output scales 120, 150, and 240;
- pixels outside an `original` source receive zero coverage;
- coverage 0 preserves BGRA, 255 clears every channel, and 128 scales all four premultiplied channels by 127/255.

Define values rather than an interface:

```go
type DepthGeometry struct {
	Mode                         string
	Scale120                     int
	SurfaceX, SurfaceY           int
	SurfaceWidth, SurfaceHeight  int
	OutputWidth, OutputHeight    int
	ImageWidth, ImageHeight      int
}

func LoadDepthMask(maskPath, wallpaperPath string) (*image.Alpha, error)
func ApplyDepthMask(pix []byte, width, height, stride int, mask *image.Alpha, g DepthGeometry) error
```

**Step 2: Run the failing test**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/wallpaper -run 'DepthMask'
```

Expected: FAIL because the loader and compositor do not exist.

**Step 3: Implement with installed packages**

Use `image.DecodeConfig`, `png.Decode`, `image.Alpha`, and the already-installed `golang.org/x/image/webp` decoder. Read masks through `io.LimitReader`. Convert any decoded grayscale representation into one packed alpha plane.

Sample mask coverage bilinearly. Use the destination pixel centre, convert physical buffer coordinates back to logical coordinates with `Scale120`, add the centred surface offset, then apply the selected wallpaper transform. Multiply every BGRA channel by `255-coverage`; the buffer is premultiplied, so the same factor applies to all four bytes.

Add a `ponytail:` comment to the per-pixel CPU pass naming its ceiling and upgrade path: compact clock surfaces keep the pass small; move it to the renderer backend if measured frame time becomes material.

**Step 4: Run the tests**

Run the command from Step 2.

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/wallpaper/depthmask.go internal/wallpaper/depthmask_test.go
git commit -m "feat(wallpaper): apply depth masks"
```

### Task 5: Host the centred Bottom-layer clock

**Files:**
- Create: `/home/nomadx/sysc-shell/internal/shell/depthclock.go`
- Create: `/home/nomadx/sysc-shell/internal/shell/depthclock_test.go`
- Modify: `/home/nomadx/sysc-shell/internal/shell/registry.go`

**Step 1: Write failing clock-host tests**

Test the requested `wayland.AuxSpec` and lifecycle:

- `LayerBottom`, centre anchor, `ExclusiveZone: -1`, keyboard none;
- a fixed 560×176 logical box, clamped down only when an output is smaller;
- `AuxUpdate{SetInputRegion: true}` with no rectangles after configure;
- namespace and ID include the connector-safe surface identity;
- one accepted descriptor opens one surface;
- replacement redraws in place;
- clear and output removal close it;
- the first clock acquires one minute clock lease and the last close releases it;
- `UpdateClock` rebuilds time and date and publishes each depth-clock surface;
- render paints non-transparent clock/card pixels before applying the mask.

**Step 2: Run the failing tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/shell -run 'DepthClock'
```

Expected: FAIL because `depthClockHost` does not exist.

**Step 3: Implement the host**

Model it on `toastHost`, using a map from connector to a small surface state. Build a `ui.KindColumn` containing a title-role time and caption-role date inside one card. Resolve colors from the active theme and call the existing `render.Paint` path. After painting, call `wallpaper.ApplyDepthMask` with the surface's logical centre offset, output size, configured scale mode, and callback scale.

Keep all surface mutation under `Registry.mu`; send `AuxRequest`s after releasing it. Store the depth clock lease on `Registry` and release it outside the mutex.

**Step 4: Run the tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/shell -run 'DepthClock|TwoBarsShareOneClockService'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/shell/depthclock.go internal/shell/depthclock_test.go internal/shell/registry.go
git commit -m "feat(shell): host the depth clock"
```

### Task 6: Validate mask registration and bind runtime cleanup

**Files:**
- Modify: `/home/nomadx/sysc-shell/internal/shell/pluginwallpaper.go`
- Modify: `/home/nomadx/sysc-shell/internal/shell/pluginwallpaper_test.go`
- Modify: `/home/nomadx/sysc-shell/internal/shell/pluginhost.go`
- Modify: `/home/nomadx/sysc-shell/internal/plugin/runtime.go`
- Modify: `/home/nomadx/sysc-shell/internal/plugin/runtime_test.go`
- Modify: `/home/nomadx/sysc-shell/internal/shell/registry.go`

**Step 1: Write failing registration and cleanup tests**

Cover:

- current image path accepts a dimension-matched mask;
- a stale path, video, transition, covered output, unknown connector, and bad mask fail without replacing the current descriptor;
- empty mask path clears only the caller's matching output;
- a second plugin cannot replace or clear another plugin's descriptor;
- disable, orderly exit, protocol failure, crash before restart, and host close clear that runtime's descriptors;
- automatic restart may register a new descriptor after cleanup.

Add one runtime lifecycle callback to `RuntimeOptions`:

```go
SessionEnded func()
```

Call it once after a live session ends and before any restart attempt. `Stop` also calls it when it closes a live session. Do not call it for a stale supervisor generation.

**Step 2: Run the failing tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/plugin ./internal/shell -run 'SessionEnded|WallpaperMaskRegistration|WallpaperMaskCleanup'
```

Expected: FAIL because registrations and the lifecycle callback are unwired.

**Step 3: Implement registration at the shell boundary**

Decode the mask before taking `Registry.mu`. Under the lock, re-read the wallpaper service snapshot and active owner. Accept only the current, uncovered, non-transitioning image assignment. Hand the decoded immutable mask to `depthClockHost`, then send its requests outside the lock.

Wire `WallpaperMaskSet` into `CallEnv`. Set `RuntimeOptions.SessionEnded` to clear the plugin ID's masks. Also clear masks from `stopPlugin` and `pluginHost.Close`; cleanup must be idempotent.

Update output-drop and config-reload paths to clear or redraw depth clocks. A scale or theme change keeps the mask and rebuilds the surface. An output removal clears it.

**Step 4: Run focused shell proof**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/plugin ./internal/wallpaper ./internal/shell -run 'SessionEnded|Wallpaper|DepthClock|PluginHost'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/plugin/runtime.go internal/plugin/runtime_test.go internal/shell/pluginwallpaper.go internal/shell/pluginwallpaper_test.go internal/shell/pluginhost.go internal/shell/registry.go
git commit -m "feat(shell): bind depth masks to runtimes"
```

### Task 7: Prove the shell slice and publish the module commit

**Files:**
- Modify only files required by failures from the scoped checks above.

**Step 1: Run format checks**

```bash
gofmt -w plugin/v1/message.go plugin/v1/framing_test.go internal/plugin/manifest.go internal/plugin/manifest_test.go internal/plugin/supervisor.go internal/plugin/supervisor_test.go internal/plugin/hostcall.go internal/plugin/hostcall_test.go internal/plugin/runtime.go internal/plugin/runtime_test.go internal/wallpaper/depthmask.go internal/wallpaper/depthmask_test.go internal/shell/pluginwallpaper.go internal/shell/pluginwallpaper_test.go internal/shell/depthclock.go internal/shell/depthclock_test.go internal/shell/pluginhost.go internal/shell/registry.go
gofmt -l plugin/v1 internal/plugin internal/wallpaper internal/shell
```

Expected: the second command prints nothing for changed files.

**Step 2: Run package tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugin/v1
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/plugin
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/wallpaper
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/shell -run 'Wallpaper|DepthClock|PluginHost|Registry|Clock'
```

Expected: PASS for all four commands.

**Step 3: Run a race check only on the new state owners**

```bash
timeout 300s env GOMAXPROCS=2 go test -race -count=1 ./internal/plugin ./internal/wallpaper -run 'SessionEnded|DepthMask|HostCallWallpaper'
```

Expected: PASS with no race report.

**Step 4: Review and push the shell branch**

```bash
git diff --check
git status --short
git log --oneline --decorate -8
git push origin HEAD:main
```

Expected: only Wallpaper Depth commits leave the worktree; push reports the new shell main SHA. Record that SHA for Task 8.

## Phase B: sysc-plugins

### Task 8: Pin shell minor 7 and update the manifest

**Files:**
- Modify: `/home/nomadx/sysc-plugins/go.mod`
- Modify: `/home/nomadx/sysc-plugins/go.sum`
- Modify: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/manifest.json`
- Create: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/manifest_test.go`

**Step 1: Write a failing manifest contract test**

Add a test that reads `manifest.json` and asserts:

- version `1.0.0`;
- protocol `{major: 1, minor: 7}`;
- `wallpaper` is declared;
- `notifications` is absent;
- `wallpaper_path` is absent;
- `auto_generate`, `threshold`, and `feather` keep their upstream defaults and bounds;
- panel remains 500×560.

**Step 2: Run the failing test**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugins/wallpaper-depth -run 'Manifest'
```

Expected: FAIL against the v0.1.0 minor-0 stub.

**Step 3: Pin and update**

```bash
go get github.com/Nomadcxx/sysc-shell@<shell-main-sha-from-task-7>
```

Edit the manifest to version 1.0.0 and minor 7, add `wallpaper`, remove `notifications` and `wallpaper_path`, and retain the three upstream settings.

**Step 4: Run the test**

Run the command from Step 2.

Expected: PASS.

**Step 5: Commit**

```bash
git add go.mod go.sum plugins/wallpaper-depth/manifest.json plugins/wallpaper-depth/manifest_test.go
git commit -m "build(wallpaper-depth): pin wallpaper API"
```

### Task 9: Replace the single-wallpaper session with a serialized output queue

**Files:**
- Modify: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/service.go`
- Modify: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/service_test.go`

**Step 1: Write failing controller tests**

Introduce domain values mirroring the wire without coupling the service tests to transport:

```go
type Settings struct {
	AutoGenerate bool
	Threshold    int
	Feather      int
}

type Output struct {
	Name, State, WallpaperPath string
}

type Job struct {
	Output, WallpaperPath, Key string
	Threshold, Feather         int
}
```

Tests must prove:

- two outputs run one helper job at a time;
- identical polls do not queue duplicates;
- a wallpaper change clears the registered mask and queues the new image;
- video, none, transitioning, and covered states clear without running inference;
- automatic generation off leaves a waiting row and manual generation queues it;
- threshold or feather changes clear current masks and reuse the helper's depth cache;
- a job completed after path or parameter change is discarded;
- one output error does not stop the next job;
- clear-cache empties pending jobs, clears masks, invokes the helper once, then requeues current images when automatic generation is on.

Use a blocking fake runner and a fake registrar. Keep one concrete controller; do not add an interface for the controller itself.

**Step 2: Run the failing tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugins/wallpaper-depth -run 'Controller|Queue|Stale|ClearCache'
```

Expected: FAIL because `Session` can hold only one wallpaper and drops work while busy.

**Step 3: Implement the state owner**

Retain `Runner`. Add a `Registrar` with only `SetMask(ctx, output, wallpaperPath, maskPath string) error`. Let one controller goroutine own setup state, output rows, queue, active job, and registered paths. Helper work runs in one worker goroutine and returns a typed result to the controller.

Queue keys must include wallpaper contents indirectly through path plus parameters; the helper's own cache verifies file contents. Before registration, compare the job against the controller's current path and parameter key.

Expose immutable `Snapshot()` data for views. Publish a changed signal only when view-visible state changes.

**Step 4: Run unit and race tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugins/wallpaper-depth -run 'Controller|Queue|Stale|ClearCache|Session'
timeout 300s env GOMAXPROCS=2 go test -race -count=1 ./plugins/wallpaper-depth -run 'Controller|Queue|Stale'
```

Expected: PASS with no race report.

**Step 5: Commit**

```bash
git add plugins/wallpaper-depth/service.go plugins/wallpaper-depth/service_test.go
git commit -m "feat(wallpaper-depth): queue output masks"
```

### Task 10: Build the final bar, tooltip, and panel trees

**Files:**
- Modify: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/view.go`
- Modify: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/service_test.go`
- Create: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/view_fit_test.go`

**Step 1: Write failing view and fit tests**

Cover these states at their real slots:

- bar at `lint.BarWidth × lint.BarHeight`: glyph-only ready, processing, setup-required, and error;
- tooltip at `lint.TooltipWidth × lint.TooltipHeight`;
- panel at 500×560: checking, setup missing, setup running, ready with no outputs, ready with image/processing/ready/video/covered rows, helper error, cache hit, and busy buttons;
- every tree passes `v1.Validate` and `lint.Tree`;
- the bar contains no percentage, readiness text, or other label;
- all interactive controls have stable IDs and accessible names.

**Step 2: Run the failing tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugins/wallpaper-depth -run 'Trees|ViewsFit|Bar|Panel|Tooltip'
```

Expected: FAIL because the panel still says `(stub)`, the bar uses text, and output rows do not exist.

**Step 3: Implement the approved trees**

Use the existing v1 vocabulary. Keep the panel root shallow:

- title and description;
- one setup card;
- one three-value parameter row;
- one bounded scroll for output rows;
- one final action row.

Use the catalogue's existing `wallpaper` glyph. Upstream's `layers-subtract` glyph is absent
from both shell icon catalogues; do not add an icon asset for one glyph.

Omit a right-click settings action because minor 7 has no settings-open host call. Left activation opens the panel through `panel.open`.

**Step 4: Run the tests**

Run the command from Step 2.

Expected: PASS with zero lint findings.

**Step 5: Commit**

```bash
git add plugins/wallpaper-depth/view.go plugins/wallpaper-depth/service_test.go plugins/wallpaper-depth/view_fit_test.go
git commit -m "feat(wallpaper-depth): render output status"
```

### Task 11: Wire polling, host calls, and single-owner publishing

**Files:**
- Modify: `/home/nomadx/sysc-plugins/cmd/sysc-plugin-wallpaper-depth/main.go`
- Create: `/home/nomadx/sysc-plugins/cmd/sysc-plugin-wallpaper-depth/main_test.go`

**Step 1: Write failing command-loop tests**

Use `io.Pipe` or the repository's existing command harness style to prove:

- handshake requests the manifest identity and accepts `wallpaper`;
- the first wallpaper poll happens without opening the panel;
- polls never overlap;
- snapshot replies reach the controller;
- mask registration sends exact output, wallpaper path, and mask path;
- default settings are `auto_generate=true`, threshold 30, feather 8 before the first settings message;
- helper completion, polling, view open, input, settings, and resync all serialize through one publisher;
- `ViewResync` resets or advances a fresh snapshot;
- host shutdown cancels the poller and helper context;
- no generic completion notification is sent.

**Step 2: Run the failing tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./cmd/sysc-plugin-wallpaper-depth -run 'Run|Poll|Resync|Shutdown'
```

Expected: FAIL because the stub neither polls wallpaper calls nor handles resync.

**Step 3: Implement the event loop**

Keep the main goroutine as the only owner of `views` and the only caller of `publish`. The receive goroutine, one-second poller, and controller worker send typed events into a bounded channel. Do not call `publish` from helper goroutines.

Implement a small adapter around `v1.Client.Call`:

```go
func fetchWallpapers(ctx context.Context, c *v1.Client) (v1.WallpaperSnapshotResult, error)
func setWallpaperMask(ctx context.Context, c *v1.Client, p v1.WallpaperMaskSetParams) error
```

Decode successful reply results strictly. Treat `reply.OK == false` as an error. Use a per-call timeout shorter than the next poll. Coalesce a poll tick while one poll is active.

Handle `ViewResync` by publishing that view from current state. Bar activation calls `panel.open`. Panel action IDs send controller commands.

**Step 4: Run unit and race tests**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./cmd/sysc-plugin-wallpaper-depth ./plugins/wallpaper-depth
timeout 300s env GOMAXPROCS=2 go test -race -count=1 ./cmd/sysc-plugin-wallpaper-depth ./plugins/wallpaper-depth -run 'Run|Poll|Controller'
```

Expected: PASS with no concurrent map or encoder race.

**Step 5: Commit**

```bash
git add cmd/sysc-plugin-wallpaper-depth/main.go cmd/sysc-plugin-wallpaper-depth/main_test.go
git commit -m "feat(wallpaper-depth): follow shell wallpapers"
```

### Task 12: Add the process integration gate

**Files:**
- Create: `/home/nomadx/sysc-plugins/tests/integration/plugin_wallpaper_depth_gate_test.go`
- Modify: `/home/nomadx/sysc-plugins/tests/integration/harness_test.go` only if the fake Python entry point must be registered in `TestMain`; do not duplicate `viewSlot`, `recordSlot`, or `checkFits`.

**Step 1: Write the failing gate**

Build the plugin binary in a temporary plugin directory and copy `depth_helper.py`. Put a fake `python3` on PATH using the integration test binary's existing re-exec pattern. The fake must answer:

- `status` with ready runtime/model JSON;
- `generate` with a deterministic mask path, wallpaper path, cache hit, and elapsed time;
- `clear-cache` with ready JSON.

The scripted host must:

- grant `panels`, `settings`, `state`, and `wallpaper`;
- answer `wallpaper.snapshot` with one DP-1 image;
- record `wallpaper.mask.set`;
- record bar 240×32, tooltip 280×200, and panel 500×560 before each `ViewOpen`;
- lint every captured snapshot;
- drive bar activation, panel open, generate, settings change, clear cache, and resync;
- change the snapshot path while the fake helper is blocked and assert the stale mask is never registered;
- send shutdown and assert clean exit.

**Step 2: Run the failing gate**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./tests/integration -run 'WallpaperDepthGate'
```

Expected: FAIL until the process wiring and fake helper contract agree.

**Step 3: Finish the narrow harness support**

Add only the re-exec switch and fake command handler to `harness_test.go` when required. Keep all Wallpaper Depth host state in the new gate file.

**Step 4: Run the gate twice**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=2 ./tests/integration -run 'WallpaperDepthGate'
```

Expected: PASS twice, proving cleanup leaves no process or file collision.

**Step 5: Commit**

```bash
git add tests/integration/plugin_wallpaper_depth_gate_test.go tests/integration/harness_test.go
git commit -m "test(wallpaper-depth): gate the live process"
```

### Task 13: Replace stub documentation and run repository proof

**Files:**
- Modify: `/home/nomadx/sysc-plugins/plugins/wallpaper-depth/README.md`
- Modify: `/home/nomadx/sysc-plugins/README.md`

**Step 1: Update operator documentation**

Document:

- model size, local inference, and state directory;
- automatic per-output image discovery;
- the centred fixed clock and mask behavior;
- image-only support;
- setup, generate, and clear-cache actions;
- threshold and feather meanings;
- foreign wallpaper coverage behavior;
- removal of the manual wallpaper path;
- bare-metal verification commands.

Change the top-level plugin table from `0.1.0 stub` to `1.0.0`. Remove the statement that Wallpaper Depth is blocked on a shell wallpaper API.

**Step 2: Run documentation and source checks**

```bash
rg -n 'stub|wallpaper_path|blocked on a shell wallpaper API|Done' plugins/wallpaper-depth README.md cmd/sysc-plugin-wallpaper-depth
git diff --check
```

Expected: no stale stub claim, manual setting, or generic completion notification; `git diff --check` exits 0.

**Step 3: Build and test the plugin slice**

```bash
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugins/wallpaper-depth
timeout 300s env GOMAXPROCS=2 go test -count=1 ./cmd/sysc-plugin-wallpaper-depth
timeout 300s env GOMAXPROCS=2 go test -count=1 ./tests/integration -run 'WallpaperDepthGate'
timeout 300s env GOMAXPROCS=2 go build ./cmd/sysc-plugin-wallpaper-depth
```

Expected: PASS and a successful build.

**Step 4: Commit**

```bash
git add plugins/wallpaper-depth/README.md README.md
git commit -m "docs(wallpaper-depth): document depth clock"
```

### Task 14: Bare-metal calibration and acceptance

**Files:**
- Modify code or tests only when the measured result contradicts an invariant above.

**Step 1: Build and install the shell and plugin using the repository's normal scripts**

```bash
cd /home/nomadx/sysc-shell
go build -trimpath -o /tmp/sysc-shell ./cmd/sysc-shell
install -m755 /tmp/sysc-shell "$HOME/.local/bin/sysc-shell"
systemctl --user restart sysc-shell.service
systemctl --user is-active sysc-shell.service

cd /home/nomadx/sysc-plugins
make install
```

Expected: both builds succeed and the user shell service reports `active`. Reapply the
documented `CAP_PERFMON` file capability after replacing the shell binary when this machine's
GPU meter uses it.

**Step 2: Exercise setup and first generation**

Choose an image with a foreground edge crossing the screen centre. Open Wallpaper Depth, run setup, and wait for the model card and DP-1 row to report ready.

Expected: the clock appears centred; foreground scenery removes the matching clock pixels; the bar remains glyph-only.

**Step 3: Calibrate all wallpaper modes**

For `fill`, `stretch`, `original`, and `panscan`, compare a distinct mask edge against the visible gSlapper image at the output centre and near both axes.

Expected: the cutout follows the visible source edge within one physical pixel. If `panscan` differs, capture gSlapper's observed mapping in a failing table case before changing `DepthGeometry`.

**Step 4: Exercise stale and failure paths**

- change wallpaper while generation is running;
- change threshold and feather;
- clear cache;
- apply a video wallpaper;
- cover the output with a foreign background surface if available;
- terminate the plugin process while its panel is open;
- reopen or retry the plugin.

Expected: no stale cutout appears, unsupported states have explicit rows, all clock surfaces disappear on plugin exit, and sysc-shell remains responsive.

**Step 5: Record evidence**

Append a short dated **Bare-metal acceptance** section to `plugins/wallpaper-depth/README.md` with the tested output, scale, wallpaper modes, commands, and observed result. Commit only evidence or fixes produced by this task.

```bash
git add plugins/wallpaper-depth/README.md
git commit -m "test(wallpaper-depth): record bare-metal gate"
```

Skip the commit when no tracked file changes.

### Task 15: Final review and integration

**Files:**
- No planned source changes.

**Step 1: Run the final scoped proof in both repositories**

```bash
cd /home/nomadx/sysc-shell
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugin/v1 ./internal/plugin ./internal/wallpaper
timeout 300s env GOMAXPROCS=2 go test -count=1 ./internal/shell -run 'Wallpaper|DepthClock|PluginHost|Registry|Clock'

cd /home/nomadx/sysc-plugins
timeout 300s env GOMAXPROCS=2 go test -count=1 ./plugins/wallpaper-depth ./cmd/sysc-plugin-wallpaper-depth
timeout 300s env GOMAXPROCS=2 go test -count=1 ./tests/integration -run 'WallpaperDepthGate'
```

Expected: PASS for every command.

**Step 2: Inspect the final diffs**

```bash
git -C /home/nomadx/sysc-shell diff --check main...HEAD
git -C /home/nomadx/sysc-plugins diff --check main...HEAD
git -C /home/nomadx/sysc-shell status --short
git -C /home/nomadx/sysc-plugins status --short
```

Expected: only planned files differ in the implementation worktrees. The pre-existing untracked files remain untouched.

**Step 3: Review manually**

Check:

- no helper or Wayland work runs under `Registry.mu`;
- every goroutine stops through context cancellation;
- helper jobs remain serialized;
- stale paths cannot reach a clock surface;
- mask buffers stay immutable after publication;
- cleanup is idempotent across crash, restart, disable, and shell shutdown;
- panel snapshots fit 500×560 in every tested state;
- no new dependency or general desktop widget API entered the diff.

**Step 4: Merge shell first, then plugins**

Use `superpowers:finishing-a-development-branch`. Merge or fast-forward sysc-shell, confirm the plugin pin names that merged commit, then merge sysc-plugins. Do not rewrite the pin after plugin verification.

**Step 5: Stop**

The slice is complete when the scoped tests pass, bare-metal calibration is recorded, the plugin pin names merged shell main, and both repositories have clean planned diffs. File movable widgets or extra clock styles separately if requested.
