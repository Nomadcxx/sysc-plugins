# Notes Plugin Redesign

**Status:** Approved 2026-09-24

## Goal

Replace the first-sweep Notes panel with a useful Markdown quick-capture and editing workspace that shares ordinary files with an Obsidian vault.

## Decisions

- Keep the existing sysc-shell panel surface and the configured `notes_dir`. Users can point it at a folder inside an Obsidian vault. Notes remain ordinary files with the configured extension (default `.md`); the plugin does not parse and rewrite frontmatter or wikilinks.
- Rebuild the panel around a persistent Scratchpad, one-line capture to a new dated note, title-and-body search, short previews, modified times, pinning, and a visible sort control. Keep blank-note creation, rename, delete confirmation, and a plain Markdown source editor.
- Keep scanning and managing direct children of the configured folder. This limits rename and delete to the folder the user selected; recursive vault traversal is a separate scope if needed.
- Preserve atomic saves, filename/path validation, symlink rejection, idle autosave, and external-edit detection. A clean buffer reloads external changes; a dirty buffer presents Reload or Keep Local. A save failure must retain the editor buffer and current store; Back and settings changes must not discard it.
- Keep the existing `.pinned.json` sidecar so pin state stays alongside the notes and syncs with the folder.
- Do not add floating desktop windows or launch Obsidian. The current plugin protocol exposes attached panels and text inputs; ordinary Markdown files are the integration boundary.
- Do not add dependencies or a Markdown renderer. Obsidian renders the shared files; the shell editor edits their source.

## Why this design

The existing store/session already own filesystem safety, autosave, and external-change behavior, so the redesign extends those owners instead of replacing them. The current view has no search, previews, or reachable sort control. `Session.Back` and `Session.SetStore` ignore flush failures and clear state, which can lose unsaved text; the redesign makes both transitions fail safely.

The folder-level design has a deliberate ceiling: search and note actions cover files directly inside the configured folder, not every Markdown file below the vault root. A recursive browser needs explicit nested-path and delete-scope decisions.

## Acceptance

- Create a blank note and capture text into a dated note; browse and open the Scratchpad.
- Search note titles and bodies; see a useful excerpt and modified time; switch between name and recent ordering; pin and unpin notes.
- Edit Markdown, rename, and delete only after confirmation; changes are visible to Obsidian as normal file updates.
- Clean external changes reload. Dirty external changes keep the local buffer and offer Reload or Keep Local.
- A failed save keeps the text, dirty state, and old store when navigating or applying a folder change.
- The redesigned panel fits the declared 420×800 panel and keeps accessible names on interactive controls.
