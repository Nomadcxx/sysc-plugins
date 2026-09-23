# Mini Docker hallmark audit

Date: 2026-09-22 · Scope: `plugins/mini-docker/view.go`, `manifest.json`, tooltip path in `cmd/sysc-plugin-mini-docker/main.go` · Read-only audit, no edits.

Lens note: hallmark's anti-pattern list is web-bornc. Desktop shell nodes (row/column/text/button, host-owned tones) are a narrower vocabulary — each web tell below is applied only where it honestly translates, and adapted tells are labelled `(adapted)`. Functional defects (panel unreachable, data race, stderr loss) are owned by the UI/UX/backend audits and are **not re-scored** here.

Pre-flight: no `design.md` at repo root. The de-facto system is the host shell vocabulary plus sibling-plugin precedent (timer, aiusage, world-clock, notes). This audit scores mini-docker against that system.

## Findings

### Major

```
[major] Hover-only affordance (adapted) — cmd/sysc-plugin-mini-docker/main.go:67-68
  The plugin's only hover affordance (tooltip) is declared and silently dropped:
  ViewTooltip publishes BarTree(tooltip), a row root, where tooltip roots must be
  columns; the host rejects it and the user never learns why.
  → Publish a column-root tooltip tree (3 lines; world-clock shape).
```

```
[major] Every-section-sameness inverted: zero padding (adapted) — plugins/mini-docker/view.go:50
  Content is glued to the panel chrome on all sides; every sibling plugin sets
  12–16px panel padding, so this panel reads as unsealed scaffolding.
  → Add the sibling-standard padding field to the panel root column.
```

```
[major] Uniform template rows, state as body text (adapted from icon-tile uniformity) — view.go:81
  "name · status" is merged into one plain string; running and exited containers
  scan identically, though the tone vocabulary is already imported one line below
  (ToneSubtle on the image line, view.go:82).
  → Split status into its own node and tone it (accent/subtle for running, subtle
  for exited).
```

```
[major] No scan-order hierarchy — view.go:73-75
  Rows render in `docker ps -a` order, so exited containers bury the actionable
  running ones; the list rewards reading, not scanning.
  → Order running-first (stable within group), or tone-sort as a cheaper variant.
```

### Minor

```
[minor] Tabular data without tabular-nums — view.go:31 (BarLabel count path)
  The pill count uses proportional figures; width jitters 9↔10 on every refresh.
  v1 exposes the exact analogue (Node.Tabular, "fixed-advance figures").
  → Set Tabular on the count text node.
```

```
[minor] Header hierarchy absent (adapted) — view.go:52
  "Docker containers" is a plain body node; timer/aiusage give panel headers
  title size, Bold, and PinEnd alignment.
  → Match the sibling header treatment (size + Bold + PinEnd for the refresh button).
```

```
[minor] Default-attractor width (adapted from 100vw discipline) — manifest.json:36
  720px is the default-attractor panel size; at current row anatomy ~370px per row
  is dead. Panel size should follow content, not the default.
  → Resize to ~440–480 after the row anatomy is settled (resize is free).
```

```
[minor] Dead layout noise — view.go:12
  Gap: 6 on a one-child row is decoration on nothing.
  → Drop the field when the pill becomes a button in the dev tranche.
```

```
[minor] State lines in default tone — view.go:62, view.go:66
  "Loading…" and "Docker is not available" render in body tone, so transitional
  and failure states look like content.
  → ToneSubtle for loading, ToneError for unavailable.
```

## Structural fingerprint check

Panel shape (title+action header row → flat list → per-row action buttons) is the repo's host vocabulary — the same system notes, world-clock, and aiusage render in. **No AI-template fingerprint.** The one departure from the system is the bar pill: a bare text node (view.go:11-15) where every sibling ships an interactive pill. That is the triple-confirmed functional P1 owned by the other audits — cross-referenced, not re-scored.

## Passed checks

- Root disciplines correct (bar=row, panel=column) per host vocabulary.
- Ellipsis character correct ("Loading…", U+2026); no straight quotes, no double hyphens.
- No italic headers, no invented metrics, no emoji-as-icon, no re-drawn chrome.
- Tone vocabulary present and used where it exists (ToneError on errMsg, ToneSubtle on image).
- Copy is concrete and domain-named ("Docker containers", "Refresh interval (s)") — no startup bingo.
- Settings block well-formed; refresh button shape matches aiusage precedent.

## Summary

**0 critical · 4 major · 5 minor**

Verdict — reads as AI-generated (unfinished first-sweep tier). The shape is system-coherent and there is no template fingerprint; the majors are absence-of-craft (padding, hierarchy, state tone, scan order) rather than slop. Fix the 4 majors in the dev tranche and this reads as made.
