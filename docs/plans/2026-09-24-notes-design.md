# Notes and Sticky Notes Redesign

**Status:** Approved 2026-09-24; this revision supersedes the panel-only scope.

## Goal

Replace the stub Notes plugin with a polished Markdown vault manager and desktop sticky notes. Notes stay ordinary files Obsidian can edit and render.

## Design

### Notes manager

- Keep one configured folder inside the user's Obsidian vault, with `.md` as the default extension. Search, rename, and delete direct children only; do not rewrite frontmatter, wikilinks, or Markdown source.
- Rebuild the attached panel as a compact, searchable library: quick capture, a persistent Scratchpad, note title and body search, readable excerpts, modified times, name/recent sorting, and a clear favorite control. Opening a note exposes its full Markdown source editor and save/conflict state. From either the library or editor, the user can open the note as a sticky surface.
- A capture creates a dated Markdown file. Blank-note creation, rename, and confirmed delete remain available. Deleting a note closes its sticky surface only after the file delete succeeds.
- Keep the existing `.pinned.json` data and interpret it as library favorites, preserving current folders. Use “Favorite” in the library so it is distinct from “Always on top” on a desktop sticky.

### Sticky surfaces

- Each note may have one open sticky surface per output. The title bar supports drag, a resize grip supports resizing, and the compact toolbar provides a small fixed set of contrast-safe color choices, “Always on top”, and close. Closing a surface never deletes the Markdown file.
- The shell owns surface creation, position, size, clamping, focus, z-order, resize and drag input, and teardown. The Notes process supplies a declarative view tree and handles note input; plugins receive no Wayland handles.
- Add a bounded, capability-gated floating-surface contract to the plugin protocol: open a surface with a stable key, output, and initial geometry; receive its view ID and a `ViewOpen`; close it through the host; and update whether it is pinned. Reuse plugin view snapshots and input routing for its content. Enforce the existing per-plugin view limit and validate keys, output generations, and geometry before creating a surface.
- Implement surfaces with the shell's existing `wlr-layer-shell` path. Unpinned notes use the top layer; pinned notes use the overlay layer. Use on-demand keyboard interactivity so opening a note does not steal text focus. The shell persists geometry and layer state in its local plugin state, keyed by plugin, stable note key, and output; the plugin persists note colors and which pinned notes should reopen when the shell starts. Keep this presentation state out of Markdown files.
- Add a small set of named note fills to the shell's semantic theme vocabulary. The shell pairs each fill with readable foreground colors; the plugin chooses a named fill and cannot submit arbitrary colors.

### File safety and editing

- Preserve atomic writes, filename/path validation, symlink rejection, idle autosave, and external-change detection. A clean buffer reloads external edits; a dirty buffer offers Reload or Keep Local.
- Replace the single editor buffer with per-note editor state so the library editor and several open sticky notes cannot overwrite each other. Flush before navigation, close, rename, delete, or folder change. If a flush fails, retain the text and current vault rather than completing the transition.
- Report scan, save, rename, delete, and restore failures in the relevant view. Do not silently drop an action error.
- Sticky surfaces edit Markdown source; Obsidian remains responsible for rich Markdown rendering. Do not add an in-shell Markdown renderer, launch Obsidian, recurse through the vault, or add dependencies in this tranche.

## Approaches considered

1. **Attached manager only.** Smallest change and the earlier approved scope, but it does not provide the independent colored and resizable sticky windows in the Obsidian reference.
2. **Shell-managed layer surfaces (selected).** Adds a reusable plugin surface contract while keeping geometry, input, and Wayland lifecycle inside the shell. It supports the required drag, resize, pin layering, and on-demand keyboard behavior using the surface stack already used by sysc-shell.
3. **Normal XDG toplevel windows.** Would delegate decorations and placement to the compositor, but sysc-shell has no plugin toplevel host, and the Wayland toplevel contract does not provide portable always-on-top control for the pin behavior.

## Compatibility and limits

- Existing vault files and `.pinned.json` remain readable. No migration touches note content.
- Search, capture, rename, and delete stay within the configured folder; nested vault browsing is out of scope.
- Sticky windows use shell layer surfaces, not compositor-managed application windows. “Always on top” means the overlay layer above ordinary application windows and shell top-layer surfaces; unpinned notes remain on the top layer above ordinary application windows.
- Notes remain editable Markdown source in sysc-shell. Rich rendering, Obsidian commands, and Obsidian workspace integration are outside this design.

## Acceptance

- The 420×800 manager panel exposes quick capture, Scratchpad, title/body search, useful previews, modified times, sorting, favorites, and clear empty/loading/error states with accessible names.
- Create, open, edit, rename, favorite, and confirmed-delete operations affect the ordinary Markdown file in the configured folder. Search and destructive operations stay within that folder.
- Open several different notes as independent sticky surfaces. Each can be dragged, resized, recolored, pinned, unpinned, and closed without affecting other notes or the manager. Reopening a note focuses its existing surface rather than creating a conflicting editor.
- Pinned surfaces restore with saved geometry and color after shell/plugin restart. Output removal and geometry that no longer fits are handled without off-screen windows or stale-generation actions.
- Clean external edits reload. Dirty external edits preserve the local buffer and offer Reload or Keep Local. A failed save retains all affected buffers and refuses navigation, close, rename, delete, or folder change when completing it would lose edits.
- On a live Wayland compositor, verify drag, resize, focus, layer order, scale changes, and output removal/reconnect. If on-demand keyboard behavior is unavailable, the shell must report that floating notes are unsupported instead of taking exclusive focus silently.
