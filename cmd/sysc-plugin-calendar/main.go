// Command sysc-plugin-calendar is a read-only EDS calendar with a countdown
// widget and month, week, four-day, day, and agenda views.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/calendar"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type calendarQuery func(context.Context, time.Time, time.Time) (calendar.EDSResult, error)

var defaultCalendarQuery calendarQuery = calendar.QueryEDS

type openedView struct {
	kind       v1.ViewKind
	revision   uint64
	instance   string
	output     string
	generation uint32
}

type calendarRange struct{ start, end time.Time }

type refreshRequest struct{ ranges []calendarRange }

type refreshResult struct {
	data   calendar.EDSResult
	ranges []calendarRange
	err    error
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	return runPlugin(in, out, time.Now, defaultCalendarQuery)
}

func runPlugin(in io.Reader, out io.Writer, now func() time.Time, query calendarQuery) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.calendar", Name: "Calendar", Version: "0.3.0"})); err != nil {
		return err
	}

	model := calendar.New(now)
	views := make(map[string]openedView)
	calendarListsOpen := make(map[string]bool)
	hidden := make(map[string]bool)
	var sources []calendar.CalendarSource
	status := calendar.LoadStatus{Loading: true}
	var coverage []calendarRange

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := make(chan v1.Message, 16)
	receiveError := make(chan error, 1)
	go func() {
		for {
			message, err := c.Recv()
			if err != nil {
				select {
				case receiveError <- err:
				default:
				}
				cancel()
				return
			}
			select {
			case incoming <- message:
			case <-ctx.Done():
				return
			}
		}
	}()

	refreshRequests := make(chan refreshRequest, 1)
	refreshResults := make(chan refreshResult, 1)
	go func() {
		for {
			var request refreshRequest
			select {
			case <-ctx.Done():
				return
			case request = <-refreshRequests:
			}
			result := refreshResult{ranges: request.ranges}
			for _, queryRange := range request.ranges {
				queryCtx, queryCancel := context.WithTimeout(ctx, 30*time.Second)
				part, err := query(queryCtx, queryRange.start, queryRange.end)
				queryCancel()
				if err != nil {
					result.err = err
					break
				}
				mergeEDSResult(&result.data, part)
			}
			select {
			case refreshResults <- result:
			case <-ctx.Done():
				return
			}
		}
	}()

	prefs := make(chan struct {
		ids []string
		err error
	}, 1)
	go func() {
		ids, err := loadHiddenCalendars(ctx, c)
		select {
		case prefs <- struct {
			ids []string
			err error
		}{ids, err}:
		case <-ctx.Done():
		}
	}()

	var lastClockKey, lastMinuteKey string
	var publishOne func(string) error
	publishOne = func(id string) error {
		view, ok := views[id]
		if !ok {
			return nil
		}
		view.revision++
		views[id] = view
		var tree *v1.Node
		events := visibleEvents(model.Events(), hidden)
		at := now()
		switch view.kind {
		case v1.ViewBar:
			tree = calendar.BarTree(events, at)
		case v1.ViewTooltip:
			tree = calendar.TooltipTree(events, at)
		default:
			tree = calendar.PanelTree(model.Panel(id), events, model.WeekStart(), status, at, sources, hidden, calendarListsOpen[id])
		}
		return c.Snapshot(id, view.revision, tree)
	}
	publishAll := func() error {
		for id := range views {
			if err := publishOne(id); err != nil {
				return err
			}
		}
		lastClockKey = clockKey(visibleEvents(model.Events(), hidden), now())
		lastMinuteKey = now().Format("200601021504")
		return nil
	}
	publishClocks := func() error {
		for id, view := range views {
			if view.kind == v1.ViewBar || view.kind == v1.ViewTooltip {
				if err := publishOne(id); err != nil {
					return err
				}
			}
		}
		lastClockKey = clockKey(visibleEvents(model.Events(), hidden), now())
		return nil
	}
	publishPanels := func() error {
		for id, view := range views {
			if view.kind == v1.ViewPanel {
				if err := publishOne(id); err != nil {
					return err
				}
			}
		}
		lastMinuteKey = now().Format("200601021504")
		return nil
	}

	requestRefresh := func(force bool) {
		panelStates := make([]calendar.PanelState, 0, len(views))
		for id, view := range views {
			if view.kind == v1.ViewPanel {
				panelStates = append(panelStates, model.Panel(id))
			}
		}
		ranges := queryRanges(now(), panelStates, model.WeekStart(), coverage, force)
		if len(ranges) == 0 {
			return
		}
		status.Loading = true
		request := refreshRequest{ranges: ranges}
		select {
		case refreshRequests <- request:
		default:
			select {
			case <-refreshRequests:
			default:
			}
			select {
			case refreshRequests <- request:
			default:
			}
		}
	}

	if err := publishAll(); err != nil {
		return err
	}
	requestRefresh(true)
	refreshTicker := time.NewTicker(10 * time.Minute)
	defer refreshTicker.Stop()
	clockTicker := time.NewTicker(5 * time.Second)
	defer clockTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-receiveError:
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			default:
				return nil
			}
		case <-refreshTicker.C:
			requestRefresh(true)
		case <-clockTicker.C:
			at := now()
			if key := clockKey(visibleEvents(model.Events(), hidden), at); key != lastClockKey {
				if err := publishClocks(); err != nil {
					return err
				}
			}
			if key := at.Format("200601021504"); key != lastMinuteKey {
				if err := publishPanels(); err != nil {
					return err
				}
			}
		case result := <-refreshResults:
			status.Loading = false
			status.ActionError, status.Notice = "", ""
			if len(result.data.Calendars) > 0 || result.data.Available {
				sources = boundedSources(result.data.Calendars)
				status.CalendarCount = len(sources)
			}
			status.Available = result.data.Available
			status.Truncated = result.data.Truncated
			errorText := result.err
			if errorText == nil && len(result.data.Errors) > 0 {
				errorText = errors.New(strings.Join(result.data.Errors, "; "))
			}
			if errorText != nil || !result.data.Available {
				status.Error = ""
				if errorText != nil {
					status.Error = truncateText(errorText.Error(), 1024)
				}
				status.Stale = len(model.Events()) > 0
			} else {
				status.Error, status.Stale = "", false
				coverage = addCoverage(coverage, result.ranges)
				updated := replaceEventsInRanges(model.Events(), result.data.Events, result.ranges)
				if err := model.SetEvents(updated); err != nil {
					status.Error, status.Stale = truncateText(err.Error(), 1024), len(model.Events()) > 0
				} else {
					status.Truncated = status.Truncated || len(updated) >= 2048
				}
			}
			if err := publishAll(); err != nil {
				return err
			}
		case pref := <-prefs:
			if pref.err != nil {
				status.ActionError = "Calendar preferences could not be loaded: " + truncateText(pref.err.Error(), 256)
				if err := publishAll(); err != nil {
					return err
				}
			} else if len(pref.ids) > 0 {
				hidden = make(map[string]bool, len(pref.ids))
				for _, id := range pref.ids {
					hidden[id] = true
				}
				if err := publishAll(); err != nil {
					return err
				}
			}
		case message := <-incoming:
			switch message := message.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				views[message.ViewID] = openedView{kind: message.View, instance: message.Instance, output: message.Output, generation: message.Generation}
				if message.View == v1.ViewPanel {
					requestRefresh(false)
				}
				if err := publishOne(message.ViewID); err != nil {
					return err
				}
			case *v1.ViewClose:
				delete(views, message.ViewID)
				delete(calendarListsOpen, message.ViewID)
				model.ClosePanel(message.ViewID)
			case *v1.ViewResync:
				if err := publishOne(message.ViewID); err != nil {
					return err
				}
			case *v1.InputEvent:
				view, ok := views[message.ViewID]
				if !ok || view.revision != message.Revision {
					continue
				}
				if view.kind == v1.ViewBar {
					if message.Event != v1.EventActivate {
						continue
					}
					if message.Node == "calendar-open" {
						if _, err := hostCall(ctx, c, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Instance: view.instance, Output: view.output, Generation: view.generation}); err != nil {
							status.ActionError = "Calendar panel could not be opened: " + truncateText(err.Error(), 256)
							_ = c.Send(&v1.PluginStatus{State: v1.StatusError, Message: status.ActionError})
						}
					}
					continue
				}
				if view.kind != v1.ViewPanel || message.Event != v1.EventActivate && message.Event != v1.EventShortcut {
					continue
				}
				refresh := false
				switch {
				case message.Node == "calendar-next-event":
					model.AdjacentEventIn(message.ViewID, 1, visibleEvents(model.Events(), hidden))
				case message.Node == "calendar-previous-event":
					model.AdjacentEventIn(message.ViewID, -1, visibleEvents(model.Events(), hidden))
				case message.Node == "cal-prev":
					model.Step(message.ViewID, -1)
					refresh = true
				case message.Node == "cal-next":
					model.Step(message.ViewID, 1)
					refresh = true
				case message.Node == "cal-today":
					model.TodayFor(message.ViewID)
					refresh = true
				case strings.HasPrefix(message.Node, "cal-date-"):
					if date, err := time.ParseInLocation("20060102", strings.TrimPrefix(message.Node, "cal-date-"), model.Panel(message.ViewID).Date.Location()); err == nil {
						model.SelectDate(message.ViewID, date)
						refresh = true
					}
				case strings.HasPrefix(message.Node, "cal-view-"):
					mode := calendar.ViewMode(strings.TrimPrefix(message.Node, "cal-view-"))
					if model.SetPanelView(message.ViewID, mode) {
						refresh = true
					}
				case strings.HasPrefix(message.Node, "calendar-toggle-"):
					for _, source := range sources {
						if calendar.CalendarToggleNodeID(source.ID) == message.Node {
							hidden[source.ID] = !hidden[source.ID]
							if !hidden[source.ID] {
								delete(hidden, source.ID)
							}
							if stateErr := saveHiddenCalendars(ctx, c, hidden); stateErr != nil {
								status.ActionError = "Calendar filter could not be saved: " + truncateText(stateErr.Error(), 256)
							}
							break
						}
					}
				case message.Node == "calendar-more":
					calendarListsOpen[message.ViewID] = !calendarListsOpen[message.ViewID]
				case isEventNode(message.Node):
					eventID := eventIDFromNode(message.Node)
					if event, ok := findEvent(model.Events(), eventID); ok && !hidden[event.CalendarID] {
						model.OpenEvent(message.ViewID, event.ID)
					}
				case message.Node == "cal-back":
					model.CloseEvent(message.ViewID)
				case message.Node == "cal-retry":
					requestRefresh(true)
				case message.Node == "action-copy":
					panel := model.Panel(message.ViewID)
					if event, ok := findEvent(model.Events(), panel.SelectedEventID); ok {
						text := copyEventSummary(event, panel.Date.Location())
						if _, callErr := hostCall(ctx, c, v1.CallClipboardWrite, v1.ClipboardWriteParams{Text: text}); callErr != nil {
							status.ActionError = "Clipboard write failed: " + truncateText(callErr.Error(), 256)
						} else {
							status.ActionError, status.Notice = "", "Event details copied"
						}
					}
				case message.Node == "action-join":
					panel := model.Panel(message.ViewID)
					if event, ok := findEvent(model.Events(), panel.SelectedEventID); ok && calendar.SafeHTTPURL(event.URL) {
						if _, callErr := hostCall(ctx, c, v1.CallOpenURL, v1.OpenURLParams{URL: event.URL}); callErr != nil {
							status.ActionError = "Meeting link could not be opened: " + truncateText(callErr.Error(), 256)
						} else {
							status.ActionError, status.Notice = "", "Opening meeting link…"
						}
					}
				}
				if refresh {
					requestRefresh(false)
				}
				if err := publishAll(); err != nil {
					return err
				}
			case *v1.SettingsChanged:
				if value, ok := message.Values["week_start"].(string); ok {
					model.SetWeekStart(value)
					if err := publishAll(); err != nil {
						return err
					}
					requestRefresh(false)
				}
			}
		}
	}
}

func hostCall(ctx context.Context, client *v1.Client, kind v1.CallKind, params any) (v1.HostReply, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	reply, err := client.Call(callCtx, kind, params)
	if err != nil {
		return reply, err
	}
	if !reply.OK {
		return reply, errors.New(reply.Error)
	}
	return reply, nil
}

func loadHiddenCalendars(ctx context.Context, client *v1.Client) ([]string, error) {
	reply, err := hostCall(ctx, client, v1.CallStateGet, v1.StateGetParams{Key: "hidden_calendars"})
	if err != nil {
		return nil, err
	}
	var result v1.StateGetResult
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		return nil, err
	}
	if !result.Found {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal(result.Value, &ids); err != nil {
		return nil, err
	}
	if len(ids) > 128 {
		ids = ids[:128]
	}
	return ids, nil
}

func saveHiddenCalendars(ctx context.Context, client *v1.Client, hidden map[string]bool) error {
	ids := make([]string, 0, len(hidden))
	for id, value := range hidden {
		if value && len(id) > 0 && len(id) <= 256 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > 128 {
		ids = ids[:128]
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = hostCall(ctx, client, v1.CallStateSet, v1.StateSetParams{Key: "hidden_calendars", Value: raw})
	return err
}

func queryRanges(now time.Time, panels []calendar.PanelState, weekStart string, coverage []calendarRange, force bool) []calendarRange {
	var ranges []calendarRange
	base := calendarRange{start: now.Add(-31 * 24 * time.Hour)}
	base.end = base.start.Add(370 * 24 * time.Hour)
	includeBase := force || len(coverage) == 0
	if includeBase {
		ranges = append(ranges, base)
	}
	for _, panel := range panels {
		start, end := calendar.ViewRange(panel, weekStart)
		candidate := calendarRange{start: start, end: end}
		covered := rangeCovered(base, candidate) && includeBase
		if !includeBase {
			for _, existing := range coverage {
				if rangeCovered(existing, candidate) {
					covered = true
					break
				}
			}
		}
		if !covered {
			ranges = append(ranges, candidate)
		}
	}
	return ranges
}

func rangeCovered(outer, inner calendarRange) bool {
	return !inner.start.Before(outer.start) && !inner.end.After(outer.end)
}

func addCoverage(existing, additions []calendarRange) []calendarRange {
	for _, added := range additions {
		covered := false
		for _, current := range existing {
			if rangeCovered(current, added) {
				covered = true
				break
			}
		}
		if !covered {
			existing = append(existing, added)
		}
	}
	if len(existing) > 64 {
		existing = append([]calendarRange(nil), existing[len(existing)-64:]...)
	}
	return existing
}

func mergeEDSResult(target *calendar.EDSResult, part calendar.EDSResult) {
	if target.Calendars == nil && target.Events == nil && len(target.Errors) == 0 {
		target.Available = part.Available
	} else {
		target.Available = target.Available && part.Available
	}
	calendars := make(map[string]bool, len(target.Calendars))
	for _, source := range target.Calendars {
		calendars[source.ID] = true
	}
	for _, source := range part.Calendars {
		if !calendars[source.ID] {
			target.Calendars = append(target.Calendars, source)
			calendars[source.ID] = true
		}
	}
	byID := make(map[string]int, len(target.Events)+len(part.Events))
	for index, event := range target.Events {
		byID[event.ID] = index
	}
	for _, event := range part.Events {
		if index, ok := byID[event.ID]; ok {
			target.Events[index] = event
		} else if len(target.Events) < 2048 {
			byID[event.ID] = len(target.Events)
			target.Events = append(target.Events, event)
		} else {
			target.Truncated = true
		}
	}
	target.Errors = append(target.Errors, part.Errors...)
	target.Truncated = target.Truncated || part.Truncated
}

func replaceEventsInRanges(existing, incoming []calendar.Event, ranges []calendarRange) []calendar.Event {
	result := make([]calendar.Event, 0, len(existing)+len(incoming))
	for _, event := range existing {
		remove := false
		for _, queryRange := range ranges {
			if event.OccursOnRange(queryRange.start, queryRange.end) {
				remove = true
				break
			}
		}
		if !remove {
			result = append(result, event)
		}
	}
	byID := make(map[string]int, len(result)+len(incoming))
	for index, event := range result {
		byID[event.ID] = index
	}
	for _, event := range incoming {
		if index, ok := byID[event.ID]; ok {
			result[index] = event
		} else {
			byID[event.ID] = len(result)
			result = append(result, event)
		}
	}
	return result
}

func boundedSources(input []calendar.CalendarSource) []calendar.CalendarSource {
	seen := make(map[string]bool, len(input))
	result := make([]calendar.CalendarSource, 0, min(len(input), 128))
	for _, source := range input {
		if source.ID == "" || len(source.ID) > 256 || seen[source.ID] {
			continue
		}
		seen[source.ID] = true
		source.Name = truncateText(source.Name, 180)
		if source.Name == "" {
			source.Name = "Calendar"
		}
		source.Color = strings.ToLower(source.Color)
		if len(source.Color) > 7 || (source.Color != "" && !strings.HasPrefix(source.Color, "#")) {
			source.Color = ""
		}
		result = append(result, source)
		if len(result) == 128 {
			break
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func visibleEvents(events []calendar.Event, hidden map[string]bool) []calendar.Event {
	result := make([]calendar.Event, 0, len(events))
	for _, event := range events {
		if !hidden[event.CalendarID] {
			result = append(result, event)
		}
	}
	return result
}

func clockKey(events []calendar.Event, now time.Time) string {
	event, active, ok := calendar.NextTimedEvent(events, now)
	if !ok {
		return "date:" + now.Format("2006-01-02")
	}
	state := countdownLabel(event.Start.Sub(now))
	if active {
		state = "now"
	}
	return event.ID + ":" + state
}

func countdownLabel(remaining time.Duration) string {
	if remaining <= 0 {
		return "now"
	}
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm", max(minutes, 1))
	}
	if minutes < 24*60 {
		return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
	}
	return fmt.Sprintf("%dd", (minutes+24*60-1)/(24*60))
}

func findEvent(events []calendar.Event, id string) (calendar.Event, bool) {
	for _, event := range events {
		if event.ID == id {
			return event, true
		}
	}
	return calendar.Event{}, false
}

func isEventNode(node string) bool {
	if strings.HasPrefix(node, "event-") {
		return true
	}
	if len(node) != 32 {
		return false
	}
	for _, char := range node {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func eventIDFromNode(node string) string {
	id := strings.TrimPrefix(node, "event-")
	if index := strings.LastIndex(id, "-on-"); index >= 0 {
		id = id[:index]
	}
	return id
}

func copyEventSummary(event calendar.Event, location *time.Location) string {
	when := ""
	if event.AllDay {
		start, _ := time.Parse("2006-01-02", event.StartDate)
		end, _ := time.Parse("2006-01-02", event.EndDate)
		when = start.Format("Monday, 2 January 2006")
		if end.After(start.AddDate(0, 0, 1)) {
			when += " – " + end.AddDate(0, 0, -1).Format("Monday, 2 January 2006")
		}
		when += " · All day"
	} else {
		start, end := event.Start.In(location), event.End.In(location)
		when = start.Format("Monday, 2 January 2006 15:04") + " – " + end.Format("Monday, 2 January 2006 15:04")
	}
	parts := []string{event.Summary, when}
	if event.Calendar != "" {
		parts = append(parts, "Calendar: "+event.Calendar)
	}
	if event.Location != "" {
		parts = append(parts, "Location: "+event.Location)
	}
	if event.Description != "" {
		parts = append(parts, event.Description)
	}
	return truncateText(strings.Join(parts, "\n"), 8192)
}

func truncateText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
