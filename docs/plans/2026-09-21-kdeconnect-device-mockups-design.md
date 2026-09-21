# KDE Connect Device Mockups Design

## Goal

Create four transparent PNG device silhouettes for the KDE Connect device card. Each image must match the node's declared canvas size so the host can cover-fill it without cropping.

## Visual treatment

Generate each device as finished flat artwork. Use one shared prompt structure and feed the approved phone output back as the style reference for the remaining devices.

- Body: `#3a3f45`
- Screen: `#101214`
- Edge highlight: `#565d66`
- Background: transparent

Keep the shapes centered with at least 8 px of transparent space on every side. Use rounded, substantial geometry that matches SYSC's dark card surfaces. The laptop uses a straight-on view. Include no text, logos, brand marks, UI content, cast shadows, or saturated colors.

## Deliverables

| File | Canvas | Subject |
|---|---:|---|
| `plugins/kdeconnect/assets/phone.png` | 135 x 260 | Rounded portrait phone with inset screen |
| `plugins/kdeconnect/assets/tablet.png` | 180 x 240 | Portrait tablet with slimmer bezels |
| `plugins/kdeconnect/assets/desktop.png` | 260 x 160 | Straight-on monitor with compact stand |
| `plugins/kdeconnect/assets/laptop.png` | 260 x 170 | Straight-on open laptop |

## Validation

Inspect all four images at native size and 50% scale. Check exact dimensions, RGBA color, sRGB metadata, transparent corners, the 8 px safety margin, and a file size below 200 KiB.
