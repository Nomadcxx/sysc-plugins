#!/usr/bin/env python3
"""Draws plugins/notes/assets/empty-notes.png from the shell's sticky-note
paper palette.

It lives outside the plugin directory so the release archive, which ships
that directory, carries only the PNG. The PNG is committed so builds stay
offline; rerun this only when the art changes.
"""
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

OUT = Path(__file__).resolve().parents[2] / "plugins" / "notes" / "assets" / "empty-notes.png"

S = 4  # supersample, then downscale for smooth edges
W, H = 400 * S, 280 * S
PAPER = {"sun": (0xF7, 0xE8, 0xA4), "mint": (0xCF, 0xEB, 0xD5), "sky": (0xD2, 0xE4, 0xF7)}
INK = (0x3A, 0x33, 0x20)


def note(color, angle, cx, cy, lines=False):
    w, h, r = 150 * S, 150 * S, 14 * S
    layer = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    shadow = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    box = (cx - w // 2, cy - h // 2, cx + w // 2, cy + h // 2)
    ImageDraw.Draw(shadow).rounded_rectangle((box[0], box[1] + 8 * S, box[2], box[3] + 8 * S), r, fill=(0, 0, 0, 90))
    shadow = shadow.filter(ImageFilter.GaussianBlur(10 * S))
    d = ImageDraw.Draw(layer)
    d.rounded_rectangle(box, r, fill=PAPER[color] + (255,))
    # The darker band is the sticky strip along the top edge.
    band = tuple(int(c * 0.93) for c in PAPER[color]) + (255,)
    d.rounded_rectangle((box[0], box[1], box[2], box[1] + 26 * S), r, fill=band)
    d.rectangle((box[0], box[1] + 14 * S, box[2], box[1] + 26 * S), fill=band)
    if lines:
        for i, frac in enumerate((0.78, 0.62, 0.70)):
            y = box[1] + (50 + i * 26) * S
            d.rounded_rectangle((box[0] + 20 * S, y, box[0] + 20 * S + int((w - 40 * S) * frac), y + 8 * S), 4 * S, fill=INK + (150,))
    merged = Image.alpha_composite(shadow, layer)
    return merged.rotate(angle, resample=Image.BICUBIC, center=(cx, cy))


canvas = Image.new("RGBA", (W, H), (0, 0, 0, 0))
for color, angle, dx, lines in (("sky", 8, -70, False), ("mint", -5, 60, False), ("sun", 0, 0, True)):
    canvas = Image.alpha_composite(canvas, note(color, angle, W // 2 + dx * S, H // 2, lines))
canvas.resize((W // S, H // S), Image.LANCZOS).save(OUT, optimize=True)
