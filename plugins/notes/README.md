# Notes

Notes keeps your notes as ordinary Markdown files. Set **Notes directory** to a folder inside your Obsidian vault; the plugin reads and writes files in that folder without changing their Markdown source. It searches direct children and does not scan subfolders.

## Library

Open **Notes** from the bar to capture a thought, edit a file, or search note titles and text. Quick capture creates a dated note. When the shell grants clipboard access, **Paste** imports plain text into the capture field so you can review or edit it before saving. In the shell launcher, `/nt` opens Notes and `/nt <text>` saves a quick capture. **Scratchpad** opens `scratchpad.md` for text you want to keep nearby. Use the star to favorite a note in the library; favorites do not pin a desktop window.

Open a note as a sticky from its library card or editor. The manager and every sticky for that file share one edit buffer.

## Sticky notes

Drag the title bar to move a sticky and drag its lower-right corner to resize it. Choose a color in the note toolbar. **Pin** keeps the sticky above regular windows. The shell saves its position and pin layer per output.

Closing a sticky closes its window and keeps the Markdown file. Pinned stickies reopen on their saved output when Notes starts and that output is available.

## Saving and conflicts

Notes saves edits after you stop typing. A clean note reloads edits made in Obsidian. If both apps changed the same note, open it in the manager and choose **Reload file** or **Keep my text**. A failed save keeps your text and leaves the sticky open.

Stickies need layer-shell on-demand keyboard focus. If the compositor does not support it, the manager stays available and reports why it could not open the sticky.
