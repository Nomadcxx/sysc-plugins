package calendar

/*
#cgo pkg-config: libecal-2.0
#include <libecal/libecal.h>
#include <json-glib/json-glib.h>
#include <time.h>

static gchar *sysc_utc_query_time(gint64 timestamp) {
	time_t value = (time_t) timestamp;
	struct tm parts;
	char buffer[32];
	gmtime_r(&value, &parts);
	if (strftime(buffer, sizeof(buffer), "%Y%m%dT%H%M%SZ", &parts) == 0)
		return NULL;
	return g_strdup(buffer);
}

static void sysc_json_string(JsonBuilder *builder, const gchar *name, const gchar *value) {
	json_builder_set_member_name(builder, name);
	json_builder_add_string_value(builder, value ? value : "");
}

static gchar *sysc_calendar_query(gint64 start, gint64 end, GCancellable *cancellable) {
	JsonBuilder *builder = json_builder_new();
	GError *error = NULL;
	ESourceRegistry *registry = e_source_registry_new_sync(cancellable, &error);
	gchar *fatal_error = NULL;
	if (!registry && error) {
		fatal_error = g_strdup(error->message);
		g_clear_error(&error);
	}
	gchar *start_text = sysc_utc_query_time(start);
	gchar *end_text = sysc_utc_query_time(end);
	gchar *sexp = start_text && end_text ? g_strdup_printf(
		"(occur-in-time-range? (make-time \"%s\") (make-time \"%s\"))", start_text, end_text) : NULL;
	json_builder_begin_object(builder);
	json_builder_set_member_name(builder, "available");
	json_builder_add_boolean_value(builder, registry != NULL);
	json_builder_set_member_name(builder, "calendars");
	json_builder_begin_array(builder);
	GList *sources = registry ? e_source_registry_list_enabled(registry, E_SOURCE_EXTENSION_CALENDAR) : NULL;
	for (GList *link = sources; link; link = link->next) {
		ESource *source = E_SOURCE(link->data);
		const gchar *uid = e_source_get_uid(source);
		const gchar *name = e_source_get_display_name(source);
		ESourceExtension *extension = e_source_get_extension(source, E_SOURCE_EXTENSION_CALENDAR);
		gchar *color = extension ? e_source_selectable_dup_color(E_SOURCE_SELECTABLE(extension)) : NULL;
		json_builder_begin_object(builder);
		sysc_json_string(builder, "id", uid);
		sysc_json_string(builder, "name", name);
		sysc_json_string(builder, "color", color);
		json_builder_end_object(builder);
		g_free(color);
	}
	json_builder_end_array(builder);
	json_builder_set_member_name(builder, "events");
	json_builder_begin_array(builder);
	GPtrArray *errors = g_ptr_array_new_with_free_func(g_free);
	if (fatal_error) {
		g_ptr_array_add(errors, g_strdup(fatal_error));
	}
	guint event_count = 0;
	gboolean truncated = FALSE;
	for (GList *link = sources; link && sexp; link = link->next) {
		ESource *source = E_SOURCE(link->data);
		GError *source_error = NULL;
		ECalClient *client = E_CAL_CLIENT(e_cal_client_connect_sync(
			source, E_CAL_CLIENT_SOURCE_TYPE_EVENTS, 8, cancellable, &source_error));
		if (!client) {
			if (source_error) {
				gchar *message = g_strdup_printf("%s: %s", e_source_get_display_name(source), source_error->message);
				g_ptr_array_add(errors, message);
				g_clear_error(&source_error);
			}
			continue;
		}
		GSList *components = NULL;
		if (!e_cal_client_get_object_list_as_comps_sync(client, sexp, &components, cancellable, &source_error)) {
			if (source_error) {
				gchar *message = g_strdup_printf("%s: %s", e_source_get_display_name(source), source_error->message);
				g_ptr_array_add(errors, message);
				g_clear_error(&source_error);
			}
			g_object_unref(client);
			continue;
		}
		ESourceExtension *extension = e_source_get_extension(source, E_SOURCE_EXTENSION_CALENDAR);
		gchar *color = extension ? e_source_selectable_dup_color(E_SOURCE_SELECTABLE(extension)) : NULL;
		for (GSList *item = components; item; item = item->next) {
			if (event_count >= 2048) {
				truncated = TRUE;
				break;
			}
			gchar *ical = e_cal_component_get_as_string(E_CAL_COMPONENT(item->data));
			if (!ical || strlen(ical) > 65536) {
				g_ptr_array_add(errors, g_strdup("An event exceeded the iCalendar response limit"));
				g_free(ical);
				continue;
			}
			json_builder_begin_object(builder);
			sysc_json_string(builder, "calendar_id", e_source_get_uid(source));
			sysc_json_string(builder, "calendar", e_source_get_display_name(source));
			sysc_json_string(builder, "color", color);
			sysc_json_string(builder, "ical", ical);
			json_builder_end_object(builder);
			g_free(ical);
			event_count++;
		}
		g_free(color);
		g_slist_free_full(components, g_object_unref);
		g_object_unref(client);
	}
	json_builder_end_array(builder);
	json_builder_set_member_name(builder, "errors");
	json_builder_begin_array(builder);
	for (guint i = 0; i < errors->len; i++) {
		json_builder_add_string_value(builder, g_ptr_array_index(errors, i));
	}
	json_builder_end_array(builder);
	json_builder_set_member_name(builder, "truncated");
	json_builder_add_boolean_value(builder, truncated);
	if (fatal_error) {
		sysc_json_string(builder, "fatal_error", fatal_error);
	}
	json_builder_end_object(builder);
	JsonGenerator *generator = json_generator_new();
	JsonNode *root = json_builder_get_root(builder);
	json_generator_set_root(generator, root);
	gchar *result = json_generator_to_data(generator, NULL);
	json_node_free(root);
	g_object_unref(generator);
	g_object_unref(builder);
	g_ptr_array_unref(errors);
	if (registry)
		g_object_unref(registry);
	g_list_free_full(sources, g_object_unref);
	g_free(start_text);
	g_free(end_text);
	g_free(sexp);
	g_free(fatal_error);
	return result;
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

type CalendarSource struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type EDSResult struct {
	Available bool
	Calendars []CalendarSource
	Events    []Event
	Errors    []string
	Truncated bool
}

type edsWireResult struct {
	Available bool             `json:"available"`
	Calendars []CalendarSource `json:"calendars"`
	Events    []struct {
		CalendarID string `json:"calendar_id"`
		Calendar   string `json:"calendar"`
		Color      string `json:"color"`
		ICal       string `json:"ical"`
	} `json:"events"`
	Errors     []string `json:"errors"`
	FatalError string   `json:"fatal_error"`
	Truncated  bool     `json:"truncated"`
}

// QueryEDS reads enabled EDS calendars and asks EDS itself to expand recurring
// occurrences for the requested half-open time range.
func QueryEDS(ctx context.Context, start, end time.Time) (EDSResult, error) {
	if !start.Before(end) || end.Sub(start) > 370*24*time.Hour {
		return EDSResult{}, fmt.Errorf("calendar: EDS query range is invalid or longer than a year")
	}
	cancellable := C.g_cancellable_new()
	C.g_object_ref(C.gpointer(unsafe.Pointer(cancellable))) // The query goroutine owns one reference.
	type nativeResult struct{ data *C.gchar }
	done := make(chan nativeResult, 1)
	var mu sync.Mutex
	abandoned := false
	go func() {
		data := C.sysc_calendar_query(C.gint64(start.Unix()), C.gint64(end.Unix()), cancellable)
		C.g_object_unref(C.gpointer(unsafe.Pointer(cancellable)))
		mu.Lock()
		if abandoned {
			C.g_free(C.gpointer(unsafe.Pointer(data)))
			mu.Unlock()
			return
		}
		done <- nativeResult{data}
		mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		C.g_cancellable_cancel(cancellable)
		mu.Lock()
		abandoned = true
		mu.Unlock()
		select {
		case result := <-done:
			C.g_free(C.gpointer(unsafe.Pointer(result.data)))
		default:
		}
		C.g_object_unref(C.gpointer(unsafe.Pointer(cancellable)))
		return EDSResult{}, ctx.Err()
	case result := <-done:
		C.g_object_unref(C.gpointer(unsafe.Pointer(cancellable)))
		if result.data == nil {
			return EDSResult{}, fmt.Errorf("calendar: EDS returned no result")
		}
		defer C.g_free(C.gpointer(unsafe.Pointer(result.data)))
		var wire edsWireResult
		if err := json.Unmarshal([]byte(C.GoString(result.data)), &wire); err != nil {
			return EDSResult{}, fmt.Errorf("calendar: decode EDS result: %w", err)
		}
		out := EDSResult{Available: wire.Available, Calendars: wire.Calendars, Errors: wire.Errors, Truncated: wire.Truncated}
		if wire.FatalError != "" && len(out.Errors) == 0 {
			out.Errors = append(out.Errors, wire.FatalError)
		}
		for _, item := range wire.Events {
			events, err := parseICalendar(item.CalendarID, item.Calendar, item.Color, item.ICal)
			if err != nil {
				out.Errors = append(out.Errors, item.Calendar+": "+err.Error())
				continue
			}
			out.Events = append(out.Events, events...)
		}
		if len(out.Events) > maxEvents {
			out.Events = out.Events[:maxEvents]
			out.Truncated = true
		}
		if err := ValidateEvents(out.Events); err != nil {
			return EDSResult{}, err
		}
		sortEvents(out.Events)
		return out, nil
	}
}

type icalProperty struct {
	params map[string]string
	value  string
}

func parseICalendar(calendarID, calendarName, color, data string) ([]Event, error) {
	lines := unfoldICalendar(data)
	var events []Event
	var fields map[string]icalProperty
	nested := 0
	for _, line := range lines {
		name, params, value, ok := splitICalendarProperty(line)
		if !ok {
			continue
		}
		switch name {
		case "BEGIN":
			if strings.EqualFold(value, "VEVENT") {
				fields, nested = make(map[string]icalProperty), 0
			} else if fields != nil {
				nested++
			}
			continue
		case "END":
			if strings.EqualFold(value, "VEVENT") {
				if fields != nil {
					event, err := eventFromICalendar(calendarID, calendarName, color, fields)
					if err != nil {
						return events, err
					}
					events = append(events, event)
				}
				fields = nil
			} else if fields != nil && nested > 0 {
				nested--
			}
			continue
		}
		if fields != nil && nested == 0 {
			if _, exists := fields[name]; !exists {
				fields[name] = icalProperty{params: params, value: value}
			}
		}
	}
	if fields != nil {
		return events, fmt.Errorf("unterminated VEVENT")
	}
	return events, nil
}

func unfoldICalendar(data string) []string {
	physical := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(physical))
	for _, line := range physical {
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
		} else {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitICalendarProperty(line string) (string, map[string]string, string, bool) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return "", nil, "", false
	}
	parts := strings.Split(line[:colon], ";")
	name := strings.ToUpper(parts[0])
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:]
	}
	params := make(map[string]string, len(parts)-1)
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			params[strings.ToUpper(key)] = strings.Trim(value, "\"")
		}
	}
	return name, params, line[colon+1:], true
}

func eventFromICalendar(calendarID, calendarName, color string, fields map[string]icalProperty) (Event, error) {
	uid := fields["UID"].value
	startProperty, ok := fields["DTSTART"]
	if uid == "" || !ok {
		return Event{}, fmt.Errorf("VEVENT needs UID and DTSTART")
	}
	start, startDate, allDay, err := parseICalendarDate(startProperty)
	if err != nil {
		return Event{}, fmt.Errorf("VEVENT %q DTSTART: %w", uid, err)
	}
	endDate, startTime, endTime := "", time.Time{}, time.Time{}
	if allDay {
		endProperty, hasEnd := fields["DTEND"]
		if hasEnd {
			if fields["DURATION"].value != "" {
				return Event{}, fmt.Errorf("VEVENT %q has both DTEND and DURATION", uid)
			}
			var endAllDay bool
			var endErr error
			_, endDate, endAllDay, endErr = parseICalendarDate(endProperty)
			if endErr != nil || !endAllDay {
				return Event{}, fmt.Errorf("VEVENT %q has invalid all-day DTEND", uid)
			}
		} else if rawDuration := fields["DURATION"].value; rawDuration != "" {
			duration, durationErr := parseICalendarDuration(rawDuration)
			if durationErr != nil || duration.elapsed != 0 || duration.days == 0 {
				return Event{}, fmt.Errorf("VEVENT %q has invalid all-day DURATION", uid)
			}
			date, _ := time.Parse("2006-01-02", startDate)
			endDate = date.AddDate(0, 0, duration.days).Format("2006-01-02")
		} else {
			date, _ := time.Parse("2006-01-02", startDate)
			endDate = date.AddDate(0, 0, 1).Format("2006-01-02")
		}
		if endDate <= startDate {
			return Event{}, fmt.Errorf("VEVENT %q has a non-positive all-day interval", uid)
		}
	} else {
		startTime = start
		if endProperty, hasEnd := fields["DTEND"]; hasEnd {
			if fields["DURATION"].value != "" {
				return Event{}, fmt.Errorf("VEVENT %q has both DTEND and DURATION", uid)
			}
			var endAllDay bool
			endTime, _, endAllDay, err = parseICalendarDate(endProperty)
			if err != nil || endAllDay {
				return Event{}, fmt.Errorf("VEVENT %q has invalid DTEND", uid)
			}
		} else if rawDuration := fields["DURATION"].value; rawDuration != "" {
			duration, durationErr := parseICalendarDuration(rawDuration)
			if durationErr != nil {
				return Event{}, fmt.Errorf("VEVENT %q has invalid DURATION: %w", uid, durationErr)
			}
			endTime = duration.Add(startTime)
		} else {
			// ponytail: Give DTSTART-only items a one-hour display interval; EDS
			// often omits DTEND for imported events and a zero-height block is unusable.
			endTime = startTime.Add(time.Hour)
		}
		if !startTime.Before(endTime) {
			return Event{}, fmt.Errorf("VEVENT %q has a non-positive timed interval", uid)
		}
	}
	key := calendarID + "\x00" + uid + "\x00" + fields["RECURRENCE-ID"].value
	summary := decodeICalendarText(fields["SUMMARY"].value)
	if summary == "" {
		summary = "Untitled event"
	}
	marker := strings.ToLower(strings.TrimSpace(color))
	if !validMarkerColor(marker) {
		marker = "accent"
	}
	meetingURL := decodeICalendarText(fields["URL"].value)
	if !SafeHTTPURL(meetingURL) {
		meetingURL = ""
	}
	return Event{
		ID: stableID(key), CalendarID: calendarID, Calendar: boundedText(calendarName, 256),
		Summary: boundedText(summary, maxEventSummary), Description: boundedText(decodeICalendarText(fields["DESCRIPTION"].value), maxEventDescription),
		Location: boundedText(decodeICalendarText(fields["LOCATION"].value), maxEventLocation), URL: meetingURL,
		Start: startTime, End: endTime, AllDay: allDay, StartDate: startDate, EndDate: endDate, Marker: marker,
	}, nil
}

func parseICalendarDate(property icalProperty) (time.Time, string, bool, error) {
	raw := strings.TrimSpace(property.value)
	if strings.EqualFold(property.params["VALUE"], "DATE") || len(raw) == 8 {
		date, err := time.Parse("20060102", raw)
		if err != nil {
			return time.Time{}, "", true, err
		}
		return date, date.Format("2006-01-02"), true, nil
	}
	if strings.HasSuffix(raw, "Z") {
		value, err := time.Parse("20060102T150405Z", raw)
		return value, "", false, err
	}
	zone := time.Local
	if tzid := property.params["TZID"]; tzid != "" {
		loaded, err := time.LoadLocation(tzid)
		if err != nil {
			return time.Time{}, "", false, fmt.Errorf("unknown time zone %q", tzid)
		}
		zone = loaded
	}
	value, err := time.ParseInLocation("20060102T150405", raw, zone)
	return value, "", false, err
}

type icalDuration struct {
	days    int
	elapsed time.Duration
}

func (d icalDuration) Add(start time.Time) time.Time {
	return start.AddDate(0, 0, d.days).Add(d.elapsed)
}

func parseICalendarDuration(raw string) (icalDuration, error) {
	var out icalDuration
	if len(raw) < 3 || raw[0] != 'P' {
		return out, fmt.Errorf("duration must start with P")
	}
	body := raw[1:]
	dateEnd := strings.IndexByte(body, 'T')
	datePart, timePart := body, ""
	if dateEnd >= 0 {
		datePart, timePart = body[:dateEnd], body[dateEnd+1:]
	}
	if dateEnd >= 0 && timePart == "" {
		return out, fmt.Errorf("empty time duration")
	}
	if datePart != "" {
		if strings.HasSuffix(datePart, "W") {
			if dateEnd >= 0 || strings.Count(datePart, "W") != 1 {
				return out, fmt.Errorf("invalid week duration")
			}
			weeks, err := strconv.Atoi(strings.TrimSuffix(datePart, "W"))
			if err != nil || weeks < 1 || weeks > 370/7 {
				return out, fmt.Errorf("invalid week duration")
			}
			out.days = weeks * 7
		} else {
			if !strings.HasSuffix(datePart, "D") || strings.Count(datePart, "D") != 1 {
				return out, fmt.Errorf("invalid day duration")
			}
			days, err := strconv.Atoi(strings.TrimSuffix(datePart, "D"))
			if err != nil || days < 0 || days > 370 {
				return out, fmt.Errorf("invalid day duration")
			}
			out.days = days
		}
	}
	if timePart != "" {
		position, order := 0, 0
		for position < len(timePart) {
			start := position
			for position < len(timePart) && timePart[position] >= '0' && timePart[position] <= '9' {
				position++
			}
			if start == position || position == len(timePart) {
				return out, fmt.Errorf("invalid time duration")
			}
			value, err := strconv.Atoi(timePart[start:position])
			if err != nil || value < 0 {
				return out, fmt.Errorf("invalid time duration")
			}
			unit := timePart[position]
			position++
			var unitOrder int
			switch unit {
			case 'H':
				unitOrder = 1
				out.elapsed += time.Duration(value) * time.Hour
			case 'M':
				unitOrder = 2
				out.elapsed += time.Duration(value) * time.Minute
			case 'S':
				unitOrder = 3
				out.elapsed += time.Duration(value) * time.Second
			default:
				return out, fmt.Errorf("unknown time duration unit %q", unit)
			}
			if unitOrder <= order {
				return out, fmt.Errorf("time duration units are repeated or out of order")
			}
			order = unitOrder
		}
	}
	if out.days == 0 && out.elapsed == 0 || time.Duration(out.days)*24*time.Hour+out.elapsed > 370*24*time.Hour {
		return icalDuration{}, fmt.Errorf("duration is empty or too long")
	}
	return out, nil
}

func decodeICalendarText(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) {
			out.WriteByte(value[i])
			continue
		}
		i++
		switch value[i] {
		case 'n', 'N':
			out.WriteByte('\n')
		case ',', ';', '\\':
			out.WriteByte(value[i])
		default:
			out.WriteByte(value[i])
		}
	}
	return out.String()
}

func boundedText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		out.WriteRune(r)
	}
	value = out.String()
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func validMarkerColor(value string) bool {
	if value == "accent" || value == "secondary" || value == "tertiary" || value == "outline" {
		return true
	}
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}
