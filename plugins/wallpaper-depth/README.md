# Wallpaper Depth (stub port)

Port of [noctalia-dev/official-plugins `wallpaper_depth`](https://github.com/noctalia-dev/official-plugins/tree/main/wallpaper_depth)
(MIT, author noctalia). The original generates a foreground depth mask for the
wallpaper with Depth Anything V2 Small (ONNX) and then composites desktop
widgets *behind* the wallpaper's foreground.

## What works here

The helper is vendored unchanged (`depth_helper.py`, attribution header at the
top). This plugin drives it: environment setup (venv + numpy/onnxruntime/
Pillow + 99 MB checksum-verified model download), status, mask generation for
a configured wallpaper with the original's threshold/feather settings, and
cache clearing. Requires `python3` (3.11–3.14) and network access for the
first setup; helper data lives under
`$XDG_STATE_HOME/sysc-shell/plugins/org.sysc.wallpaper-depth`.

## What does not work yet, and why

sysc-shell's plugin protocol (`plugin/v1`) currently offers only
`state`, `panel.open/close`, `notify`, and `output.context` host calls. There
is no way for a plugin to draw a desktop surface or to learn about wallpaper
changes, so the compositing half of the original is not implementable. The
panel's buttons therefore manage the helper only.

What a future shell API would enable:

1. a wallpaper-changed signal (or file watch over the compositor wallpaper);
2. an output-surface hook to stack a desktop widget between wallpaper and
   mask, honoring the generated alpha mask for parallax/occlusion.

Until then this plugin is versioned 0.1.0 and the panel labels itself a stub.
