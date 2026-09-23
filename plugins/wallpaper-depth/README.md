# Wallpaper Depth

Wallpaper Depth generates a depth mask for each image wallpaper and uses the
mask to place foreground scenery over a centred desktop clock. The plugin runs
Depth Anything V2 Small locally through ONNX Runtime. The vendored
`depth_helper.py` retains its upstream MIT attribution.

## Requirements and local data

The helper needs `python3` 3.11–3.14. Setup creates a Python environment,
installs NumPy, ONNX Runtime, and Pillow, then downloads the 99 MB ONNX model
and verifies its checksum. Setup needs network access; wallpaper inference
runs on the local CPU.

The helper stores its environment, model, depth cache, and mask cache in
`$XDG_STATE_HOME/sysc-shell/plugins/org.sysc.wallpaper-depth`. When
`XDG_STATE_HOME` is unset, it uses `~/.local/state/sysc-shell/plugins/org.sysc.wallpaper-depth`.

## Behavior

The plugin polls the shell's wallpaper assignments once per second. It
generates masks for active image wallpapers on each output. Video, empty,
transitioning, and covered outputs appear in the panel without inference. A
wallpaper change clears the old mask; a result for a wallpaper that changed
while inference ran is discarded.

For each accepted mask, sysc-shell draws a centred 560×176 logical clock on a
Bottom-layer surface and clears the clock pixels covered by foreground scenery.
The shell maps the mask with the configured wallpaper mode (`fill`, `stretch`,
`original`, or `panscan`) and output scale. The clock shrinks to fit a smaller
output. If another wallpaper or background surface covers an output, the shell
clears its mask and hides its clock until that coverage ends.

The plugin reads the current wallpaper path from sysc-shell. You do not enter
or maintain a wallpaper path in plugin settings.

## Use

Run `make install` in the sysc-plugins repository, then enable Wallpaper Depth
from sysc-shell's Plugins panel. Open its panel from the wallpaper glyph in
the bar.

The panel provides these actions:

- **Check** reads helper and model readiness.
- **Run setup** installs the local runtime and model.
- **Generate** runs inference for one image output.
- **Clear cache** removes cached depth predictions and masks.

Automatic generation starts enabled and runs after image wallpaper changes.
Turn it off to generate masks from an output row on demand. **Threshold** sets
the relative depth cutoff from 0 to 100. **Feather** sets the transition width
around that cutoff from 0 to 50; higher values soften the mask edge.

## Bare-metal calibration

Build and install the shell API, then install the plugin:

```sh
cd ../sysc-shell
go build -trimpath -o /tmp/sysc-shell ./cmd/sysc-shell
install -m755 /tmp/sysc-shell "$HOME/.local/bin/sysc-shell"
systemctl --user restart sysc-shell.service
systemctl --user is-active sysc-shell.service

cd ../sysc-plugins
make install
```

Choose an image with scenery crossing the screen centre. Check the clock cutout
at the output centre and near both axes with `fill`, `stretch`, `original`, and
`panscan`, at each scale used by the output. The mask edge should follow the
visible wallpaper edge within one physical pixel. Also change wallpapers
during generation, change threshold and feather, clear the cache, apply a video
wallpaper, cover the output with a foreign background surface, and stop the
plugin while its panel is open. Confirm that stale clocks disappear and the
shell stays responsive.
