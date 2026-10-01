# Notes

Notes keeps your notes as ordinary Markdown files. Set **Notes directory** to a folder inside your Obsidian vault. The plugin reads and writes files in that folder without changing their Markdown, and it only looks at files directly in the folder, not in subfolders.

## The panel

Click the Notes icon in the bar to open the panel. Your notes are listed on the left and the open note is on the right.

- **Search or create.** Type in the box at the top to filter notes by title and text. Press **Enter** to open the top match. If nothing matches, Enter creates a note named after what you typed. The **+** button always creates a new note from the box, or a blank note when the box is empty.
- **Paste.** When the shell grants clipboard access, the clipboard button creates a note from the copied text, named after its first line.
- **Scratchpad.** `scratchpad.md` is always the first row, for text you want to keep nearby.
- **Pinned and Notes.** Star a note to pin it to the top of the list. **Recent** / **A–Z** changes the order of the rest.
- **Editing.** Rename a note by editing its title. The rename happens when you press Enter or move to another note. Notes save shortly after you stop typing; the footer shows the save state, the word count and when the note last changed.

Quick captures from the shell launcher (`/nt <text>`) become notes named after their first line. Older files named like `note-2026-10-01-091233.md` are listed by their first line too.

## Sticky notes

Open the selected note as a sticky with the sticky-note button. Each sticky is a small window of pastel paper: sunshine, mint, sky, rose or lilac. Choose the colour with the dots at the bottom. The panel and every sticky for the same note share one copy of the text, so they never overwrite each other.

- Drag the title bar to move a sticky; drag the lower-right corner to resize it. The writing area grows with the window. The smallest size is 200×180.
- **Pin** keeps the sticky above regular windows. The shell remembers each sticky's position, size and pin per monitor.
- **Esc** while typing leaves the text; press Esc again to close the sticky. Closing a sticky never deletes the note.
- Pinned stickies come back when Notes next opens a view on that monitor (the bar icon or the panel). Notes cannot reopen them before then, because the shell tells it which monitor it is on only when one of its views opens.

## Saving and conflicts

A note that is not being edited reloads changes made in Obsidian. If both apps changed the same note, the editor asks whether to **Reload file** or **Keep mine**. A failed save keeps your text and shows the error in the footer or on the sticky.

Stickies need the compositor's layer-shell on-demand keyboard focus (version 4). Without it, the panel still works and shows why the sticky could not open.
