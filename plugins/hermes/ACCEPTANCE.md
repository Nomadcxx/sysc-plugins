# Hermes Agent acceptance

Results recorded here are summarized in the pull request description.

## Scroll spike

**Status: not run. No option has been chosen.**

The plan asks for four transcript strategies to be compared in a real
sysc-shell session while a reply streams:

1. Full snapshot on every change.
2. A patch to the streaming message's text node only.
3. Fixed message slots whose text is patched in place.
4. One text node for the transcript, plus `view.focus` to the newest line.

This spike was not run for Task 7 (2026-11-11 UTC):

- The only sysc-shell on the development machine is the owner's live
  desktop session. Running the spike would mean installing a development
  build of this plugin into that live configuration and running a Hermes
  bridge behind it.
- Each option has to be judged by scrolling the transcript by hand while a
  reply streams. Rendered previews (`sysc-panel-preview`) are still images
  and cannot show scroll behaviour.

Static reading of the pinned host (sysc-shell v0.2.5). This is not a
measured result:

- `internal/shell/pluginhost.go` `onMessage`: a `view.snapshot` and a
  `view.patch` both produce a newly converted root, and `applyResult`
  replaces `v.Root`. Options 1 to 3 therefore all rebuild the panel tree.
- `internal/shell/panelhost.go` `rebuildPanel` carries scroll offsets over
  only for the host's own settings lists (`settingsRailKey`,
  `settingsBodyKey`, `settingsSearchBodyKey`). A plugin list's
  `ScrollOffset` does not appear to survive a rebuild. If that holds, the
  transcript list would jump back to the top on every update, whatever the
  option.
- `view.focus` (`focusPanelView`) matches only focusable nodes, and
  `setFocus` moves the roving focus index without scrolling. Text nodes
  are not focusable, so option 4 looks unable to bring the newest message
  into view by itself.

What ships in Task 7: the transcript is a `KindList` with key `transcript`
and a stable `msg:<id>` key per message. It shows the newest 24 messages,
with the caption "Showing the last 24 messages" when older ones exist. The
list starts at its top, so in a long conversation the newest message can
sit below the fold. The plan's fallback (a paged transcript of the last 12
messages with Older/Newer) is not built. That choice should follow a live
run of this spike, not this static reading.

To run it: install a development build into a throwaway sysc-shell
session, point it at a Hermes bridge, stream a reply of 40+ lines with each
option, and record here:

- whether the scroll position survives each update
- whether the newest line can be reached
- what the frame cost is

## Approval check

## Parity matrix
