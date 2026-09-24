# Wallpaper Depth UI Gap Closure Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close the audited Wallpaper Depth controls, bulk generation, setup disclosure, and result detail gaps, then polish the plugin views.

**Architecture:** Reuse sysc-shell's declared plugin settings renderer through `include_settings`; let the host render unmatched schema settings in a generic Settings card. Keep generation in the existing single-worker controller and add a deterministic all-image entry point. Make no wallpaper protocol change.

**Tech Stack:** Go 1.26, sysc-shell plugin manifest and view protocol, existing plugin controller and settings renderer.

---

## Tasks

### Task 1: Expose declared settings in opted-in plugin panels

Modify `sysc-shell/internal/shell/popout_plugins.go` so `pluginPanelSettings` retains its existing named groups and renders remaining visible schema settings under a generic Settings card. Set `include_settings: true` for the Wallpaper Depth panel. Keep the existing panel scroll and manifest dimensions.

### Task 2: Restore bulk mask generation and complete status context

Add an explicit `GenerateAll` controller event. Queue current image outputs in connector order through the existing serialization and stale-result checks; retain per-output Generate. Add a disabled-aware Generate masks action, show the Python/model readiness and first-setup download disclosure, and display cached elapsed time on ready output rows.

### Task 3: Polish and document the view

Keep the glyph-only bar and accessible names. Improve panel section labels and action hierarchy without adding a custom icon or changing the wallpaper picker. Update the approved design and README to match the controls now present.

### Verification

Build the shell and plugin binaries from their feature worktrees. Skip compositor calibration while `NIRI_SOCKET` is unavailable; retain it as the hardware acceptance item.
