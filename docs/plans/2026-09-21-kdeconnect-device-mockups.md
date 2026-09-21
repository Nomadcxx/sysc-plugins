# KDE Connect Device Mockups Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver four coherent transparent device silhouettes at the exact canvases declared by the KDE Connect view.

**Architecture:** Generate each finished silhouette with the built-in image-generation tool. Use the accepted phone image as the style reference for the tablet, desktop, and laptop, then use ImageMagick only to fit the generated art onto exact transparent canvases and write sRGB RGBA PNGs.

**Tech Stack:** Built-in image generation, ImageMagick, PNG

---

### Task 1: Generate the device family

**Files:**
- Create: `plugins/kdeconnect/assets/phone.png`
- Create: `plugins/kdeconnect/assets/tablet.png`
- Create: `plugins/kdeconnect/assets/desktop.png`
- Create: `plugins/kdeconnect/assets/laptop.png`

**Step 1: Generate the phone**

Call the built-in image-generation tool with the approved palette and constraints from `docs/plans/2026-09-21-kdeconnect-device-mockups-design.md`. Request one centered portrait phone on a transparent background with no shadow or extra objects.

**Step 2: Inspect the phone**

Check the body shape, inset screen, transparent background, flat treatment, and edge quality. Make one targeted image edit if any required property is missing.

**Step 3: Generate the remaining devices**

Make one built-in generation call per device. Pass the accepted phone as the style reference and keep the same palette, edge treatment, transparency, and visual weight. Request a portrait tablet, straight-on monitor, and straight-on open laptop.

**Step 4: Inspect the family**

Compare the four sources together. Regenerate only an asset that breaks the shared treatment or contains text, a logo, a shadow, a heavy gradient, or extra objects.

### Task 2: Normalize and validate the PNGs

**Files:**
- Modify: `plugins/kdeconnect/assets/phone.png`
- Modify: `plugins/kdeconnect/assets/tablet.png`
- Modify: `plugins/kdeconnect/assets/desktop.png`
- Modify: `plugins/kdeconnect/assets/laptop.png`

**Step 1: Fit exact canvases**

For each accepted source, trim empty source padding, scale without distortion into a box that leaves at least 12 px on each canvas edge, center it, and write an sRGB RGBA PNG:

```bash
magick SOURCE -alpha on -trim +repage -resize '111x236>' -gravity center -background none -extent 135x260 -colorspace sRGB -define png:color-type=6 -define png:exclude-chunk=date,time plugins/kdeconnect/assets/phone.png
magick SOURCE -alpha on -trim +repage -resize '156x216>' -gravity center -background none -extent 180x240 -colorspace sRGB -define png:color-type=6 -define png:exclude-chunk=date,time plugins/kdeconnect/assets/tablet.png
magick SOURCE -alpha on -trim +repage -resize '236x136>' -gravity center -background none -extent 260x160 -colorspace sRGB -define png:color-type=6 -define png:exclude-chunk=date,time plugins/kdeconnect/assets/desktop.png
magick SOURCE -alpha on -trim +repage -resize '236x146>' -gravity center -background none -extent 260x170 -colorspace sRGB -define png:color-type=6 -define png:exclude-chunk=date,time plugins/kdeconnect/assets/laptop.png
```

Replace each `SOURCE` with the corresponding generated file path.

**Step 2: Verify technical constraints**

Run:

```bash
file plugins/kdeconnect/assets/{phone,tablet,desktop,laptop}.png
identify -format '%f %wx%h %[channels] %[colorspace] %b\n' plugins/kdeconnect/assets/{phone,tablet,desktop,laptop}.png
```

Expected: canvases `135x260`, `180x240`, `260x160`, and `260x170`; RGBA channels; sRGB colorspace; every file below 200 KiB.

**Step 3: Check native and half scale**

Create an untracked comparison sheet in `/tmp` and inspect it:

```bash
magick montage plugins/kdeconnect/assets/{phone,tablet,desktop,laptop}.png -thumbnail 50% -background '#181b1d' -geometry +24+24 /tmp/kdeconnect-device-mockups-half.png
```

Expected: each subject remains recognizable, centered, unclipped, and stylistically consistent against the approximate panel color.

**Step 4: Commit**

```bash
git add plugins/kdeconnect/assets/phone.png plugins/kdeconnect/assets/tablet.png plugins/kdeconnect/assets/desktop.png plugins/kdeconnect/assets/laptop.png
git commit -m "feat(kdeconnect): add device mockup artwork"
```
