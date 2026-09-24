# Calendar plugin redesign

Date: 2026-09-24 · Baseline: `org.sysc.calendar` 0.2.0, month-grid-only plugin · Host: sysc-shell plugin protocol 1.7

## Goal

Replace the date-only stub with a read-only EDS calendar that combines a useful bar countdown, a complete multi-view event browser, and the visual hierarchy and interactions demonstrated by Noctalia and DMS.

## Evidence from the current system

- `plugins/calendar/calendar.go` owns only month offset, week start, a date grid, and today's date text. `view.go` draws the date in the bar and a month grid in the panel. `main.go` handles previous/next/today and the `week_start` setting. There is no calendar data source, selected date, event model, or event action.
- `calendar_test.go` covers the month grid, navigation, settings, and tree validity. `TestPluginCalendarGateMonthNavigation` checks panel navigation and the bar date. Neither exercises calendar data or event interaction.
- The panel is 320×420. It cannot show the dense month-plus-agenda and five-view layouts in the references at useful size.
- sysc-shell's internal UI already renders segmented controls and scroll lists, but plugin/v1 does not expose segmented controls, time-positioned event layouts, panel shortcut registration, or a clipboard-write call. The shell also owns a clipboard client that plugins cannot currently use.

## Decisions

1. **Use Evolution Data Server (EDS) as the only provider.** Discover calendars and read expanded event instances through EDS's public session service. EDS owns account setup, credentials, synchronization, recurrence expansion, and its offline cache. The plugin does not inspect EDS's private files or add another cache. The current machine has EDS 3.60.2 installed; an EDS-free machine gets a clear setup state.
2. **Keep the calendar read-only.** Opening an event or meeting link is allowed; create, edit, and delete are out of scope. The plugin must never write to a user's calendars.
3. **Ship five views:** Month, Week, 4 Days, Day, and Agenda. Month pairs the date grid with the selected day's events. Week, 4 Days, and Day show a time-scaled schedule with all-day events, overlapping lanes, and a current-time marker. Agenda groups event cards by day. Navigation follows the active range, and Today returns to the current local date.
4. **Combine the useful bar behaviors.** Show the next event's truncated title and live countdown in a compact bar pill; show `Now` while an event is active. With no upcoming timed event, show today's date. Click opens the calendar panel; hover reveals the full title, schedule, location, and meeting-link availability.
5. **Use EDS calendar colors as decoration.** Event blocks keep theme-owned text and surfaces for contrast; the source color appears as a bounded marker/edge. Calendar visibility can be toggled in the panel and persisted through the existing plugin-state service.
6. **Extend the host at the responsible layer.** Expose the existing native segmented control. Add a small, theme-aware schedule-grid UI node for the proportional Week/4 Days/Day layout because row/column flow cannot place timed events or resolve overlaps. Add declared panel shortcuts, a write-only clipboard host call, and a restricted HTTP(S) URL-opening call for `j/k`, `t`/Home, `c`, and Ctrl+R. These are general plugin UI capabilities, not calendar-specific host actions. Shortcuts are active only while that plugin panel has focus and no text editor owns input; Escape remains host-owned. Clipboard access is declared in the manifest and supports writes only.
7. **Preserve protocol compatibility.** The new UI node and host calls use additive plugin protocol fields at a new minor. Older hosts reject the required minor with the existing compatibility error; the plugin does not silently render a materially degraded view. Resolve the existing host protocol-ceiling issue (`sysc-511`, blocked by plugin-source design `sysc-506`) before selecting the final minor number.
8. **Keep view state per panel instance.** Each panel tracks its selected date, active view, and selected event independently. EDS event snapshots are shared. Reject input events whose revision is stale. The existing Monday/Sunday week-start setting remains.

## UI and parity target

The pass is reference-led, not a backend-first screen wrapped around the current month grid. It uses the shell's theme, type, focus, and motion rules while matching the references' information order and density.

| Surface | Reference behavior to match |
|---|---|
| Bar + tooltip | dms-dcal next-event/countdown pill; full event details on hover |
| Month | Noctalia's month grid beside the selected-day event card; dim adjacent dates and emphasize today/selection |
| Week / 4 Days / Day | DMS's proportional time grid, all-day row, overlapping events, date headers, and current-time indicator |
| Agenda | DankCalendar's day grouping, colored event rail, today affordance, `Now`/`Next` status, and selected-event details |
| Event actions | Open an in-panel detail view, join only validated HTTP(S) meeting links, and copy a concise event summary |
| Empty/error states | Distinguish loading, no configured EDS calendars, no events, unavailable EDS, and read errors; provide a retry where useful |

Selecting an event opens its detail view inside the calendar panel; event creation, editing, and deletion are excluded. Meeting links use the host's HTTP(S)-only URL opener. Event names, times, locations, and descriptions remain text content and are bounded before crossing the plugin wire. Calendar event actions use accessible names and focusable controls. Custom shortcuts supplement normal focus navigation; they do not replace it.

## Ownership and data flow

The Go plugin owns EDS discovery/querying, conversion to a bounded event model, per-instance navigation, selected-calendar preferences, refresh state, and event actions. It uses the existing `godbus` dependency for EDS and a standards-capable iCalendar parser if the public EDS interface returns iCalendar components; recurrence and source synchronization stay with EDS.

The shell owns the segmented control, the schedule-grid layout and overlap geometry, clipboard writes, shortcut routing, theme colors, focus order, clipping, and panel sizing. The schedule grid receives event data and semantic colors; it does not connect to EDS or own calendar state. No CalDAV credentials or event bodies are duplicated into shell state.

The plugin queries only the visible range plus the next-event lookahead window. A single provider worker publishes immutable snapshots; UI navigation never waits on D-Bus. EDS change notifications trigger a new snapshot, with a bounded refresh interval for countdown accuracy and recovery. Timestamps are displayed in the user's local time zone; all-day dates remain date-only.

## Failure and privacy behavior

- EDS unavailable or no configured sources: show one actionable setup state, not a blank calendar.
- Source/query error: keep the last successful EDS snapshot visible when available, label it stale, and offer refresh. Never present a failed refresh as an empty calendar.
- Calendar changes are read-only. Meeting URLs accept only `http` or `https` and are opened by the shell without invoking a command shell.
- Calendar filters and view preference use namespaced plugin state. Event contents and credentials are not copied into that state.
- Every interactive node has a stable ID/name, respects the current view revision, and fits the declared panel dimensions. Text remains readable at host theme sizes; color is never the only event distinction.

## Proof and visual acceptance

The implementation plan will add focused checks for event conversion, recurrence instances and time-zone display, model navigation, stale-input rejection, host protocol conversion/geometry, and the EDS failure states. The host's real geometry linter must accept the bar, tooltip, month, schedule, agenda, and error trees at declared sizes.

Visual review is a required gate. Capture and inspect the bar/tooltip, Month, Week, 4 Days, Day, Agenda, selected-event details, meeting action, empty state, and EDS failure state in the running shell. Compare event density, hierarchy, control spacing, contrast, selected/today treatment, and scrolling against the supplied references; iterate the UI until those views are coherent. Backend tests passing alone do not meet acceptance.

## Prior art

- [Noctalia calendar service](https://docs.noctalia.dev/noctalia/services/calendar/): multiple EDS-compatible provider concepts, a month-plus-day agenda, local-time handling, and read-only event behavior.
- [dms-dcal](https://github.com/leoamaro01/dms-dcal): compact next-event title/countdown pill.
- [dms-dankcalendar](https://github.com/arqueon/dms-dankcalendar): grouped agenda, selected-event details, `Now`/`Next`, copy/join actions, and keyboard navigation.
- [dms-calendar](https://github.com/arqueon/dms-calendar): EDS-backed calendar, five views, time-grid interactions, and calendar colors.
- The shell's live Noctalia reference capture is `docs/plans/assets/2026-08-31-bar-visual-parity/refs/noctalia-calendar.png` in sysc-shell. The dms-dankcalendar and dms-calendar screenshots are linked from their READMEs.

The references inform behavior and visual hierarchy only; this design does not reuse their plugin code.
