package calendar

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const maxDisplayEvents = 120

type LoadStatus struct {
	Loading       bool
	Available     bool
	CalendarCount int
	Error         string
	ActionError   string
	Notice        string
	Stale         bool
	Truncated     bool
}

func BarTree(events []Event, now time.Time) *v1.Node {
	event, active, ok := NextTimedEvent(events, now)
	if !ok {
		label := now.Format("Mon 2 Jan")
		return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{{
			Kind: v1.KindButton, ID: "calendar-open", Icon: "calendar_month", Text: label, Name: "Open calendar, " + now.Format("Monday, 2 January 2006"), Role: "button",
			Fill: "soft", Shape: "stadium", Padding: 5, Events: []v1.EventKind{v1.EventActivate},
		}}}
	}
	status := countdown(event.Start.Sub(now))
	if active {
		status = "Now"
	}
	label := truncateText(event.Summary, 24) + "  " + status
	return &v1.Node{Kind: v1.KindRow, Gap: 6, Tooltip: eventTooltip(event, now, active), Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "calendar-open", Icon: "calendar_month", Text: label, Name: "Open calendar. " + eventAccessibleName(event, now.Location()), Role: "button",
		Fill: "soft", Shape: "stadium", Padding: 5, Tabular: true, Events: []v1.EventKind{v1.EventActivate},
	}}}
}

func TooltipTree(events []Event, now time.Time) *v1.Node {
	date := now.Format("Monday, 2 January")
	event, active, ok := NextTimedEvent(events, now)
	if !ok {
		return &v1.Node{Kind: v1.KindColumn, Gap: 5, Children: []*v1.Node{
			{Kind: v1.KindText, Text: date, Size: "title", Bold: true},
			{Kind: v1.KindText, Text: "No upcoming events", Tone: v1.ToneSubtle},
		}}
	}
	state := countdown(event.Start.Sub(now))
	if active {
		state = "Now"
	}
	children := []*v1.Node{
		{Kind: v1.KindText, Text: date, Size: "title", Bold: true},
		{Kind: v1.KindText, Text: state + " · " + event.Summary, Tone: v1.ToneAccent, Bold: true},
		{Kind: v1.KindText, Text: eventWhen(event, now.Location()), Tone: v1.ToneSubtle},
	}
	if event.Location != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: event.Location, Tone: v1.ToneSubtle})
	}
	if SafeHTTPURL(event.URL) {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "Meeting link available", Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 5, Children: children}
}

func PanelTree(state PanelState, events []Event, weekStart string, status LoadStatus, now time.Time, sources []CalendarSource, hidden map[string]bool, calendarsExpanded bool) *v1.Node {
	if now.IsZero() {
		now = time.Now()
	}
	if state.Date.IsZero() {
		state.Date = localDay(now)
	}
	root := &v1.Node{Kind: v1.KindColumn, Gap: panelGap, Padding: panelPadding}
	root.Children = append(root.Children, panelHeader(state, weekStart), viewSwitcher(state.View))
	// body is what the chrome above leaves of the panel box; every view sizes
	// its scrolling content from it rather than from a fixed guess.
	body := PanelHeight - 2*panelPadding - headerHeight - panelGap - switcherHeight - panelGap
	filters, overflow := calendarFilterBar(sources, hidden, calendarsExpanded)
	if filters != nil {
		root.Children = append(root.Children, filters)
		body -= filterHeight + panelGap
		if calendarsExpanded && overflow {
			body -= expandedList + 6
		}
	}
	banner := statusBanner(status, len(events))
	if banner != nil {
		root.Children = append(root.Children, banner)
		body -= bannerHeight + panelGap
	}
	body = max(body, 200)
	if state.Details {
		if event, ok := eventByID(events, state.SelectedEventID); ok {
			root.Children = append(root.Children, eventDetails(event, now.Location(), body))
			return root
		}
	}
	switch state.View {
	case ViewWeek, ViewFourDays, ViewDay:
		root.Children = append(root.Children, scheduleView(state, events, weekStart, now, body))
	case ViewAgenda:
		root.Children = append(root.Children, agendaView(state, events, weekStart, now, body))
	default:
		root.Children = append(root.Children, monthView(state, events, weekStart, now, body-dayTitleHeight))
	}
	return root
}

func panelHeader(state PanelState, weekStart string) *v1.Node {
	title := rangeTitle(state, weekStart)
	step := "month"
	if state.View == ViewWeek || state.View == ViewAgenda {
		step = "week"
	} else if state.View == ViewFourDays {
		step = "four days"
	} else if state.View == ViewDay {
		step = "day"
	}
	prev := button("cal-prev", "‹", "Previous "+step, "button", v1.EventActivate)
	next := button("cal-next", "›", "Next "+step, "button", v1.EventActivate)
	prev.Width, prev.Height, next.Width, next.Height = stepButton, iconButton, stepButton, iconButton
	// Two children so PinEnd keeps the shortcut button at the right edge.
	return &v1.Node{Kind: v1.KindRow, Height: headerHeight, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: headerGap, Children: []*v1.Node{
			prev,
			{Kind: v1.KindText, Text: title, Size: "title", Bold: true, MaxWidth: titleMaxWidth},
			next,
			{Kind: v1.KindButton, ID: "cal-today", Text: "Today", Name: "Go to today", Role: "button", Fill: "soft", Width: todayButton, Height: iconButton, Events: []v1.EventKind{v1.EventActivate}},
		}},
		{Kind: v1.KindButton, ID: "cal-shortcuts", Icon: "keyboard", Name: "Keyboard shortcuts", Role: "button", Tooltip: shortcutHint,
			Width: iconButton, Height: iconButton, Events: []v1.EventKind{v1.EventActivate}},
	}}
}

func viewSwitcher(selected ViewMode) *v1.Node {
	segments := []struct {
		mode  ViewMode
		label string
	}{
		{ViewMonth, "Month"}, {ViewWeek, "Week"}, {ViewFourDays, "4 days"}, {ViewDay, "Day"}, {ViewAgenda, "Agenda"},
	}
	root := &v1.Node{Kind: v1.KindSegmented, Gap: 2, Height: switcherHeight}
	for _, item := range segments {
		root.Children = append(root.Children, &v1.Node{
			Kind: v1.KindButton, ID: "cal-view-" + string(item.mode), Text: item.label,
			Name: item.label + " view", Role: "button", Selected: item.mode == selected,
			Events: []v1.EventKind{v1.EventActivate},
		})
	}
	return root
}

func statusBanner(status LoadStatus, eventCount int) *v1.Node {
	var title, detail string
	tone := v1.ToneSubtle
	switch {
	case status.ActionError != "":
		title, tone = status.ActionError, v1.ToneError
	case status.Notice != "":
		title, tone = status.Notice, v1.ToneAccent
	case status.Loading && eventCount > 0:
		title = "Refreshing calendars…"
	case status.Loading && eventCount == 0:
		title = "Loading calendars…"
	case !status.Available && status.Error != "":
		title = "Calendar service unavailable"
		detail = status.Error
		tone = v1.ToneError
	case !status.Available:
		title = "Evolution Data Server is unavailable"
		detail = "Start Evolution Data Server to read your configured calendars."
		tone = v1.ToneError
	case status.CalendarCount == 0:
		title = "No calendars configured"
		detail = "Add a calendar in Evolution or connect an account in GNOME Online Accounts."
	case status.Error != "" && eventCount > 0:
		title = "Calendar refresh failed · showing previous events"
		detail = status.Error
		tone = v1.ToneError
	case status.Error != "":
		title = "Couldn’t read calendars"
		detail = status.Error
		tone = v1.ToneError
	case status.Truncated:
		title = "Some events were omitted because the range is unusually busy."
	}
	if title == "" {
		return nil
	}
	text := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{{Kind: v1.KindText, Text: boundedText(title, 512), Tone: tone, Bold: true}}}
	if detail != "" {
		text.Children = append(text.Children, &v1.Node{Kind: v1.KindText, Text: boundedText(detail, 1024), Tone: tone})
	}
	banner := &v1.Node{Kind: v1.KindRow, Gap: 8, Fill: "soft", Shape: "medium", Padding: 10, Height: bannerHeight, Children: []*v1.Node{text}}
	if status.Error != "" || !status.Available || status.CalendarCount == 0 {
		// The text column clips; PinEnd reserves the button so a long error
		// can never push it out of the row.
		retry := button("cal-retry", "Refresh", "Retry calendar refresh", "button", v1.EventActivate)
		retry.Height = iconButton
		banner.PinEnd = true
		banner.Children = append(banner.Children, retry)
	}
	return banner
}

// calendarFilterBar shows as many calendar chips as fit on one row and folds
// the rest behind "+N". It reports whether any were folded.
func calendarFilterBar(sources []CalendarSource, hidden map[string]bool, expanded bool) (*v1.Node, bool) {
	if len(sources) == 0 {
		return nil, false
	}
	row := &v1.Node{Kind: v1.KindRow, Gap: 6, Height: filterHeight, Children: []*v1.Node{{Kind: v1.KindText, Text: "Calendars", Tone: v1.ToneSubtle, Size: "label"}}}
	limit := chipsThatFit(sources)
	for _, source := range sources[:limit] {
		row.Children = append(row.Children, calendarToggleButton(source, hidden))
	}
	if len(sources) > limit {
		label, name := fmt.Sprintf("+%d", len(sources)-limit), "Show more calendars"
		if expanded {
			label, name = "Less", "Show fewer calendars"
		}
		row.Children = append(row.Children, button("calendar-more", label, name, "button", v1.EventActivate))
	}
	column := &v1.Node{Kind: v1.KindColumn, Gap: 6, Children: []*v1.Node{row}}
	if expanded && len(sources) > limit {
		list := &v1.Node{Kind: v1.KindList, Height: 88, Gap: 4, Events: []v1.EventKind{v1.EventScroll}}
		for _, source := range sources[limit:] {
			list.Children = append(list.Children, calendarToggleButton(source, hidden))
		}
		column.Children = append(column.Children, list)
	}
	return column, len(sources) > limit
}

// chipsThatFit counts the leading chips that fit beside the label, keeping
// room for the "+N" button whenever some do not. Widths use the host's text
// metric (8 px per byte) plus the chip's padding and stroke.
func chipsThatFit(sources []CalendarSource) int {
	const label, more, gap = len("Calendars") * 8, 48, 6
	width := func(s CalendarSource) int { return len(truncateText(s.Name, chipTextLimit))*8 + 2*5 + 2 + 12 }
	used := label
	for i, s := range sources {
		used += gap + width(s)
		reserve := 0
		if i < len(sources)-1 {
			reserve = gap + more
		}
		if used+reserve > contentWidth {
			return i
		}
	}
	return len(sources)
}

func calendarToggleButton(source CalendarSource, hidden map[string]bool) *v1.Node {
	visible := !hidden[source.ID]
	label, action := truncateText(source.Name, chipTextLimit), "Hide"
	fill := "accent"
	if !visible {
		action, fill = "Show", "outline"
	}
	marker := ""
	if validMarkerColor(source.Color) && strings.HasPrefix(source.Color, "#") {
		marker = source.Color
	}
	return &v1.Node{Kind: v1.KindButton, ID: CalendarToggleNodeID(source.ID), Text: label,
		Name: action + " calendar " + truncateText(source.Name, 180), Role: "button", Fill: fill, Shape: "stadium", Padding: 5,
		Stroke: 1, StrokeFill: "accent", MarkerColor: marker, Events: []v1.EventKind{v1.EventActivate}}
}

func CalendarToggleNodeID(sourceID string) string {
	return "calendar-toggle-" + stableID(sourceID)[:12]
}

func monthView(state PanelState, events []Event, weekStart string, now time.Time, listHeight int) *v1.Node {
	left := &v1.Node{Kind: v1.KindColumn, Width: monthGridWidth, Gap: monthCellGap, Children: []*v1.Node{}}
	// Each weekday label is a column cell as wide as a date button: text
	// measures its own width, so bare labels cannot line up with the grid.
	weekdays := &v1.Node{Kind: v1.KindRow, Gap: monthCellGap}
	for i, day := range weekdayLabels(weekStart) {
		weekdays.Children = append(weekdays.Children, &v1.Node{Kind: v1.KindColumn, Key: fmt.Sprintf("weekday-%d", i), Width: monthCell,
			Children: []*v1.Node{{Kind: v1.KindText, Text: day, Tone: v1.ToneSubtle, Size: "label", CenterX: true}}})
	}
	left.Children = append(left.Children, weekdays)
	for _, week := range monthGrid(state.Date, weekStart, now, state.Date) {
		row := &v1.Node{Kind: v1.KindRow, Gap: monthCellGap}
		for _, cell := range week {
			row.Children = append(row.Children, dateButton(cell, events))
		}
		left.Children = append(left.Children, row)
	}
	selectedDate := state.Date.Format("Monday, 2 January")
	right := &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: selectedDate, Size: "title", Bold: true},
			{Kind: v1.KindText, Text: fmt.Sprintf("%d events", countEvents(events, state.Date)), Tone: v1.ToneSubtle},
		}},
	}}
	dayEvents := eventsOn(events, state.Date)
	if len(dayEvents) == 0 {
		right.Children = append(right.Children, emptyEventsMessage("Nothing scheduled for this day."))
	} else {
		right.Children = append(right.Children, eventList(dayEvents, state.Date.Location(), listHeight))
	}
	return &v1.Node{Kind: v1.KindRow, Gap: monthSplitGap, Children: []*v1.Node{left, right}}
}

func dateButton(cell Cell, events []Event) *v1.Node {
	date := cell.Date.Format("20060102")
	count := countEvents(events, cell.Date)
	label := fmt.Sprintf("%s, %s", cell.Date.Format("Monday"), cell.Date.Format("2 January 2006"))
	if count > 0 {
		label += fmt.Sprintf(", %d events", count)
	}
	if cell.Selected {
		label += ", selected"
	}
	text := fmt.Sprint(cell.Day)
	if count > 0 {
		text += " ·"
	}
	node := &v1.Node{Kind: v1.KindButton, ID: "cal-date-" + date, Text: text, Name: label, Role: "button", Width: monthCell, Height: monthCell, Tabular: true,
		Events: []v1.EventKind{v1.EventActivate}}
	if cell.Selected {
		node.Fill = "accent"
	} else if !cell.InMonth {
		node.Tone = v1.ToneSubtle
	}
	if cell.Today && !cell.Selected {
		node.Stroke, node.StrokeFill = 1, "accent"
	}
	if count > 0 {
		node.Tooltip = fmt.Sprintf("%d events", count)
	}
	return node
}

func scheduleView(state PanelState, events []Event, weekStart string, now time.Time, height int) *v1.Node {
	start, end := ViewRange(state, weekStart)
	days := int(end.Sub(start).Hours()/24 + 0.5)
	if state.View == ViewDay {
		days = 1
	} else if state.View == ViewFourDays {
		days = 4
	} else {
		days = 7
	}
	schedule := &v1.ScheduleGrid{
		Start: start, Now: now, Zone: start.Location().String(), Days: days,
		Selected: state.SelectedEventID,
	}
	for _, event := range events {
		if !event.OccursOnRange(start, end) || len(schedule.Events) >= maxDisplayEvents {
			continue
		}
		item := v1.ScheduleEvent{ID: event.ID, Title: boundedText(event.Summary, 512), Name: eventAccessibleName(event, start.Location()), Marker: scheduleMarker(event.Marker)}
		if event.AllDay {
			item.AllDay, item.StartDate, item.EndDate = true, event.StartDate, event.EndDate
		} else {
			item.Start, item.End = event.Start, event.End
		}
		schedule.Events = append(schedule.Events, item)
	}
	return &v1.Node{Kind: v1.KindScheduleGrid, Height: height, Schedule: schedule}
}

func agendaView(state PanelState, events []Event, weekStart string, now time.Time, height int) *v1.Node {
	start, end := ViewRange(state, weekStart)
	list := &v1.Node{Kind: v1.KindList, Height: height, Gap: 8, Events: []v1.EventKind{v1.EventScroll}}
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		dayEvents := eventsOn(events, day)
		heading := day.Format("Monday, 2 January")
		if sameDate(day, now) {
			heading = "Today · " + heading
		} else if sameDate(day, now.AddDate(0, 0, 1)) {
			heading = "Tomorrow · " + heading
		}
		group := &v1.Node{Kind: v1.KindColumn, Gap: 6, Children: []*v1.Node{{Kind: v1.KindText, Text: heading, Size: "title", Bold: true}}}
		if len(dayEvents) == 0 {
			group.Children = append(group.Children, &v1.Node{Kind: v1.KindText, Text: "No events", Tone: v1.ToneSubtle})
		} else {
			for i, event := range dayEvents {
				if i == maxDisplayEvents {
					group.Children = append(group.Children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d more events", len(dayEvents)-i), Tone: v1.ToneSubtle})
					break
				}
				group.Children = append(group.Children, eventCardForDay(event, day.Location(), day))
			}
		}
		list.Children = append(list.Children, group)
	}
	return list
}

func eventList(events []Event, location *time.Location, height int) *v1.Node {
	list := &v1.Node{Kind: v1.KindList, Height: height, Gap: 7, Events: []v1.EventKind{v1.EventScroll}}
	for i, event := range events {
		if i == maxDisplayEvents {
			list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d more events", len(events)-i), Tone: v1.ToneSubtle})
			break
		}
		list.Children = append(list.Children, eventCard(event, location))
	}
	return list
}

func eventCard(event Event, location *time.Location) *v1.Node {
	return makeEventCard(event, location, "event-"+event.ID)
}

func eventCardForDay(event Event, location *time.Location, day time.Time) *v1.Node {
	return makeEventCard(event, location, "event-"+event.ID+"-on-"+day.Format("20060102"))
}

func makeEventCard(event Event, location *time.Location, id string) *v1.Node {
	label := event.Summary
	if !event.AllDay {
		label = event.Start.In(location).Format("15:04") + "  " + label
	} else {
		label = "All day  " + label
	}
	markerColor := ""
	markerFill := eventMarkerFill(event.Marker)
	if len(markerFill) == 7 && markerFill[0] == '#' {
		markerColor, markerFill = markerFill, "accent"
	}
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: truncateText(label, 160), Name: eventAccessibleName(event, location), Role: "button",
		Tooltip: eventTooltip(event, event.Start, false), Fill: "card", Shape: "medium", Padding: 9, Stroke: 2, StrokeFill: markerFill, MarkerColor: markerColor,
		Events: []v1.EventKind{v1.EventActivate}}
}

func eventDetails(event Event, location *time.Location, height int) *v1.Node {
	children := []*v1.Node{
		button("cal-back", "‹  Back to calendar", "Return to calendar", "button", v1.EventActivate),
		{Kind: v1.KindText, Text: event.Summary, Size: "headline", Bold: true},
		{Kind: v1.KindText, Text: eventWhen(event, location), Size: "title"},
		{Kind: v1.KindText, Text: event.Calendar, Tone: v1.ToneSubtle},
	}
	if event.Location != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: event.Location, Tone: v1.ToneSubtle})
	}
	if event.Description != "" {
		children = append(children, &v1.Node{Kind: v1.KindSeparator})
		children = append(children, &v1.Node{Kind: v1.KindText, Text: event.Description, MaxWidth: 880})
	}
	actions := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		button("action-copy", "Copy details", "Copy event details to clipboard", "button", v1.EventActivate),
	}}
	if SafeHTTPURL(event.URL) {
		actions.Children = append(actions.Children, button("action-join", "Join meeting", "Open the event meeting link", "button", v1.EventActivate))
	}
	children = append(children, actions)
	return &v1.Node{Kind: v1.KindList, Height: height, Gap: 12, Padding: 16, Fill: "card", Shape: "large", Events: []v1.EventKind{v1.EventScroll}, Children: children}
}

func eventByID(events []Event, id string) (Event, bool) {
	for _, event := range events {
		if event.ID == id {
			return event, true
		}
	}
	return Event{}, false
}

func eventsOn(events []Event, date time.Time) []Event {
	result := make([]Event, 0)
	for _, event := range events {
		if event.OccursOn(date) {
			result = append(result, event)
		}
	}
	return result
}

func countEvents(events []Event, date time.Time) int {
	count := 0
	for _, event := range events {
		if event.OccursOn(date) {
			count++
		}
	}
	return count
}

func emptyEventsMessage(message string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "soft", Shape: "medium", Padding: 16, Children: []*v1.Node{{Kind: v1.KindText, Text: message, Tone: v1.ToneSubtle}}}
}

func button(id, text, name, role string, event v1.EventKind) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: text, Name: name, Role: role, Events: []v1.EventKind{event}}
}

func weekdayLabels(weekStart string) []string {
	if weekStart == "sunday" {
		return []string{"S", "M", "T", "W", "T", "F", "S"}
	}
	return []string{"M", "T", "W", "T", "F", "S", "S"}
}

func rangeTitle(state PanelState, weekStart string) string {
	start, end := ViewRange(state, weekStart)
	switch state.View {
	case ViewMonth:
		return state.Date.Format("January 2006")
	case ViewDay:
		return state.Date.Format("Monday, 2 January 2006")
	case ViewWeek, ViewAgenda:
		return start.Format("2 Jan") + " – " + end.AddDate(0, 0, -1).Format("2 Jan 2006")
	default:
		last := end.AddDate(0, 0, -1)
		if start.Month() == last.Month() {
			return start.Format("2") + " – " + last.Format("2 January 2006")
		}
		return start.Format("2 Jan") + " – " + last.Format("2 Jan 2006")
	}
}

func eventWhen(event Event, location *time.Location) string {
	if event.AllDay {
		start, _ := time.Parse("2006-01-02", event.StartDate)
		end, _ := time.Parse("2006-01-02", event.EndDate)
		last := end.AddDate(0, 0, -1)
		if start.Equal(last) {
			return start.Format("Monday, 2 January 2006") + " · All day"
		}
		return start.Format("2 Jan") + " – " + last.Format("2 Jan 2006") + " · All day"
	}
	start, end := event.Start.In(location), event.End.In(location)
	if sameDate(start, end) {
		return start.Format("Mon, 2 Jan 2006 · 15:04") + "–" + end.Format("15:04")
	}
	return start.Format("Mon, 2 Jan 2006 15:04") + " – " + end.Format("Mon, 2 Jan 2006 15:04")
}

func eventAccessibleName(event Event, location *time.Location) string {
	parts := []string{event.Summary, eventWhen(event, location)}
	if event.Calendar != "" {
		parts = append(parts, event.Calendar)
	}
	if event.Location != "" {
		parts = append(parts, event.Location)
	}
	return truncateText(strings.Join(parts, ", "), 240)
}

func eventTooltip(event Event, now time.Time, active bool) string {
	parts := []string{event.Summary, eventWhen(event, now.Location())}
	if event.Location != "" {
		parts = append(parts, event.Location)
	}
	if SafeHTTPURL(event.URL) {
		parts = append(parts, "Meeting link available")
	}
	if active {
		parts = append([]string{"Now"}, parts...)
	}
	return truncateText(strings.Join(parts, " · "), 252)
}

func scheduleMarker(marker string) string {
	if validMarkerColor(marker) {
		return marker
	}
	return "accent"
}

func eventMarkerFill(marker string) string {
	switch marker {
	case "secondary":
		return "container"
	case "tertiary":
		return "soft"
	case "outline":
		return "outline"
	default:
		return "accent"
	}
}

func countdown(remaining time.Duration) string {
	if remaining <= 0 {
		return "Now"
	}
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("in %dm", max(minutes, 1))
	}
	if minutes < 24*60 {
		hours := minutes / 60
		if minutes%60 == 0 {
			return fmt.Sprintf("in %dh", hours)
		}
		return fmt.Sprintf("in %dh %dm", hours, minutes%60)
	}
	days := (minutes + 24*60 - 1) / (24 * 60)
	return fmt.Sprintf("in %dd", days)
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	value = value[:limit-3]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}

// PanelWidth and PanelHeight are the manifest's panel box. The shell sends the
// declared size in view.open rather than the size it grants, and a 1536x864
// laptop grants about 810 wide (sysc-578), so the panel is designed for a box
// every screen honours instead of for the largest one.
const (
	PanelWidth  = 800
	PanelHeight = 720

	panelPadding = 14
	panelGap     = 12
	contentWidth = PanelWidth - 2*panelPadding

	headerHeight   = 36
	switcherHeight = 38
	filterHeight   = 32
	expandedList   = 88
	bannerHeight   = 64

	stepButton    = 36
	todayButton   = 72
	iconButton    = 32
	headerGap     = 8
	titleMaxWidth = contentWidth - 2*stepButton - todayButton - iconButton - 4*headerGap

	monthCell      = 56
	monthCellGap   = 6
	monthGridWidth = 7*monthCell + 6*monthCellGap
	monthSplitGap  = 22
	dayTitleHeight = 32

	chipTextLimit = 28
)

const shortcutHint = "J / K  Previous or next event · T  Today · C  Copy details · Ctrl + R  Refresh"
