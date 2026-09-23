# Wallpaper Depth design

Date: 2026-09-23 · Baseline: plugin v0.1.0 stub on `main` (`dd221a0`), shell pin
`v0.0.0-20260923111103-dd221a064353` · Prior art: Noctalia `wallpaper_depth` 1.0.4 and
Noctalia's desktop-widget wallpaper-mask renderer.

## Goal

Turn `org.sysc.wallpaper-depth` into a visible feature. The plugin generates a foreground
mask for each active image wallpaper. The shell renders one fixed clock on each matching
output and removes the clock pixels covered by foreground scenery.

## Decisions

1. **One shell-owned clock.** The first release has a centred digital clock on every output
   with a valid mask. Movable widgets, additional widget types, and plugin-owned desktop
   surfaces remain separate work.
2. **Mask the clock pixels.** The shell draws the clock on a compact `Bottom` layer surface,
   then applies the mask as destination-out alpha. It does not copy the wallpaper into a
   second surface.
3. **Two wallpaper host calls.** The plugin polls the shell for wallpaper assignments and
   registers generated masks through a narrow protocol capability. No desktop-surface API is
   added.
4. **Image wallpapers only.** Video content changes after inference and cannot stay aligned
   with a mask generated from one frame.
5. **Fixed presentation.** The clock uses `15:04` above `Mon 2 Jan`, a themed translucent
   card, and the shell's existing clock and text renderer. The release adds no clock settings.
6. **Every mask is provisional.** A wallpaper transition, path mismatch, output removal,
   plugin exit, or foreign wallpaper surface removes the mask and clock at once.

## Prior art

The upstream plugin uses Depth Anything V2 Small through ONNX Runtime. It keeps depth
predictions separate from thresholded masks, processes outputs one at a time, polls output
wallpapers once per second, and rejects work that became stale while inference ran. The
existing stub already vendors its helper and cache format.

Noctalia does not repaint the wallpaper foreground over a desktop widget. Its desktop host
draws each widget on a tightly sized `Bottom` layer surface. A wallpaper-mask shader maps
that surface into wallpaper coordinates and draws the foreground alpha with
`DestinationOut`, punching holes in the widget. The wallpaper remains visible through those
holes. This design keeps that composition and implements the mapping in sysc-shell's CPU
renderer.

Noctalia also validates that a mask and wallpaper texture have the same dimensions and ties
the descriptor to the wallpaper path. Those checks prevent a stale mask from clipping a new
wallpaper.

## System ownership

### Plugin

The plugin owns model setup, inference, mask and depth caches, threshold and feather
settings, the per-output work queue, and the operator-facing status. It learns wallpaper
assignments from the shell. The manual `wallpaper_path` setting is deleted.

### Shell

The shell owns wallpaper assignment truth, wallpaper geometry, output scale, mask decoding,
clock surfaces, clock rendering, and mask application. It removes surfaces when the owning
plugin runtime goes away.

The split keeps model code out of the shell and Wayland code out of the plugin process.

## Protocol contract

The next additive protocol minor adds the `wallpaper` capability and two calls. Plugin
capabilities remain an API negotiation mechanism rather than a security boundary; plugin
processes already run with the user's privileges.

### `wallpaper.snapshot`

The call takes no parameters and returns:

```json
{
  "revision": 12,
  "scale": "fill",
  "outputs": [
    {
      "output": "DP-1",
      "state": "image",
      "path": "/home/me/Pictures/wallpaper.jpg"
    }
  ]
}
```

`state` is one of `image`, `video`, `none`, `transitioning`, or `covered`. The host derives
it from the wallpaper service's assignments, runtime, live connector set, and coverage map.
The plugin polls once per second, matching upstream's update interval. A revision changes
whenever a field in this result changes.

### `wallpaper.mask.set`

Registration carries the connector, the wallpaper path used for inference, and the mask
path:

```json
{
  "output": "DP-1",
  "wallpaper_path": "/home/me/Pictures/wallpaper.jpg",
  "mask_path": "/home/me/.local/state/sysc-shell/plugins/org.sysc.wallpaper-depth/cache/masks/abc.png"
}
```

An empty `mask_path` clears that output. The host associates accepted masks with the calling
runtime. It rejects a registration unless the output is connected, the current assignment
is the stated image, no transition or foreign coverage is active, and the decoded PNG has
the same dimensions as the source wallpaper. Existing image byte and pixel limits bound the
decode.

The host clears every descriptor owned by a runtime when the runtime stops. One plugin
cannot clear another runtime's descriptor.

## Plugin service

The session becomes one state owner with:

- setup state and setup error;
- current wallpaper snapshot and parameters;
- one status row per output;
- a FIFO queue with one key per output;
- one active helper job;
- the masks registered with the host.

Each queue key combines output, wallpaper path, threshold, and feather. Enqueue coalesces an
identical pending or active key. One helper job runs at a time so two outputs cannot load two
ONNX sessions concurrently.

On every poll the plugin:

1. clears masks for outputs that disappeared or no longer report `image`;
2. notices image or parameter changes;
3. queues changed images when `auto_generate` is enabled;
4. publishes rows for image, video, missing, transitioning, covered, processing, ready, and
   error states;
5. starts the next queued job when setup is ready.

After generation, the plugin polls `wallpaper.snapshot` again. It discards the result when
the output path or parameters changed, then queues the current job when automatic generation
is enabled. A current result is registered through `wallpaper.mask.set`. Registration failure
becomes an output error instead of a ready row.

Manual **Generate masks** clears and queues every current image output. **Clear cache** clears
registered masks first, empties pending work, runs the helper, and queues fresh work when
automatic generation is enabled.

Changing threshold or feather reuses the helper's cached depth prediction. Turning automatic
generation on queues every waiting image. Turning it off leaves a valid registered mask in
place until its wallpaper or parameters change.

## Clock surface

The shell creates one compact auxiliary surface per accepted mask:

- layer: `Bottom`;
- anchor: centred;
- exclusive zone: `-1`;
- keyboard: none;
- input region: empty;
- namespace: `sysc-wallpaper-depth-clock`;
- body: a large `15:04` line and smaller `Mon 2 Jan` line on a translucent themed card.

The surface uses the existing fractional-scale buffer lifecycle and text renderer. It
subscribes to the shared minute-boundary clock service only while at least one depth clock
exists. The host redraws all live depth clocks on a minute change and after a theme or scale
change.

The surface stays tightly sized. A full-output transparent buffer would consume about 20 MB
at 3440×1440 before double buffering and would redraw millions of transparent pixels every
minute.

## Mask geometry

The renderer first paints the clock. It then walks the painted buffer and multiplies every
premultiplied BGRA channel by `255 - coverage`, which is the CPU equivalent of Noctalia's
destination-out blend.

For each clock pixel it computes:

1. the pixel's logical position inside the clock surface;
2. the centred surface offset inside the output;
3. the output-relative coordinate;
4. the source-mask coordinate for the configured wallpaper scale;
5. bilinear mask coverage.

The mapping covers `fill`, `stretch`, `original`, and `panscan`. Coordinates outside an
uncropped source have zero coverage. The function takes values only and has table tests for
landscape, portrait, 1×, 1.25×, and 2× outputs.

gSlapper defines the visible result, including `panscan=1.0`. Bare-metal calibration compares
the calculated mask edge with gSlapper for all four modes before the feature ships.

## Views

### Bar and tooltip

The bar renders one `layers-subtract` icon. It carries no text. Its tone changes only for an
error. Left click opens the panel; right click opens the plugin's settings when the host
supports that existing action. The tooltip reports one of ready, processing, setup required,
or the latest error.

### Panel

The panel follows the upstream 500-pixel-wide structure:

1. title and one-line description;
2. setup card with Python state, model state, and the 99 MB local-model disclosure;
3. threshold, feather, and automatic-generation summary;
4. scrolling output rows;
5. **Generate masks** and **Clear cache** actions.

Each output row names the connector and reports processing, ready, waiting, no wallpaper,
image required, foreign coverage, or a concrete error. A ready row includes generation time
and cache-hit state when available. The panel removes the `(stub)` label and generic `Done`
notification.

The implementation keeps the panel at 500×560 unless the real layout lint rejects the
approved state matrix. A size change requires a manifest edit and matching gate update.

## Lifecycle and failure rules

- A wallpaper entering `transitioning` loses its mask and clock before the new image appears.
- A failed wallpaper apply that leaves the old assigned image visible may keep its matching
  mask.
- A covered output has no depth clock because the shell cannot prove which wallpaper is
  visible.
- A video output reports `Image wallpaper required` and has no helper job.
- A missing or unreadable wallpaper clears the mask and reports the path failure.
- A helper error affects one output. The queue proceeds to the next output.
- Output removal drops queued work and closes the surface.
- Plugin stop, disable, crash, or protocol failure clears all owned masks and surfaces.
- Shell shutdown cancels helper work through the plugin process and closes Wayland surfaces
  through the normal auxiliary-surface teardown.

## Verification contract

### Shell

- Protocol framing and strict payload decoding for both calls.
- Capability denial when a manifest omits `wallpaper`.
- Snapshot projection for image, video, transition, coverage, and no assignment.
- Mask registration accepts only the current image and rejects stale paths and bad
  dimensions.
- Runtime cleanup removes only that runtime's masks.
- Geometry tables cover every wallpaper mode and fractional scale.
- Pixel tests pin destination-out alpha and premultiplied channels.
- Surface tests pin `Bottom`, centre anchor, `-1` exclusive zone, empty input region, and
  minute redraws.

### Plugin

- Queue tests pin serialization, coalescing, parameter changes, and stale-result rejection.
- Snapshot tests pin state transitions and mask clearing.
- Helper tests keep the exact threshold and feather arguments.
- View tests validate and lint the bar, tooltip, setup states, every output state, busy state,
  and errors against their declared slots.
- The integration gate answers `wallpaper.snapshot`, observes `wallpaper.mask.set`, exercises
  open/input/settings/resync, and verifies the process survives malformed and stale replies.

### Bare metal

Use an image with a clear foreground edge crossing the centred clock. Verify setup, initial
generation, threshold and feather changes, a rapid wallpaper change during inference,
output hotplug where available, plugin termination, and `fill`, `stretch`, `original`, and
`panscan` alignment. The shell must remain responsive throughout.

## Deferred work

- movable and resizable desktop widgets;
- additional clock styles or formats;
- plugin-defined desktop views;
- masks for video wallpapers;
- GPU mask composition;
- push wallpaper events.

The measured CPU cost of the compact surface decides whether GPU composition is ever needed.
Polling remains the upgrade point for a push event if one-second detection becomes visible in
normal use.
