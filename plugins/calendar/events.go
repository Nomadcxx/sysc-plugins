package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type ViewMode string

const (
	ViewMonth    ViewMode = "month"
	ViewWeek     ViewMode = "week"
	ViewFourDays ViewMode = "four-days"
	ViewDay      ViewMode = "day"
	ViewAgenda   ViewMode = "agenda"
)

func (v ViewMode) Valid() bool {
	switch v {
	case ViewMonth, ViewWeek, ViewFourDays, ViewDay, ViewAgenda:
		return true
	default:
		return false
	}
}

type PanelState struct {
	Date            time.Time
	View            ViewMode
	SelectedEventID string
	Details         bool
}

func stableID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

// Event is one EDS occurrence. All-day end dates use RFC3339's exclusive-end
// convention and stay date-only so DST never changes their displayed length.
type Event struct {
	ID          string
	CalendarID  string
	Calendar    string
	Summary     string
	Description string
	Location    string
	URL         string
	Start       time.Time
	End         time.Time
	AllDay      bool
	StartDate   string
	EndDate     string
	Marker      string
}

const (
	maxEvents           = 2048
	maxEventSummary     = 512
	maxEventDescription = 8192
	maxEventLocation    = 512
	maxEventURL         = 2048
)

func ValidateEvents(events []Event) error {
	if len(events) > maxEvents {
		return fmt.Errorf("calendar: %d events exceeds %d", len(events), maxEvents)
	}
	seen := make(map[string]bool, len(events))
	for i, event := range events {
		if event.ID == "" || len(event.ID) > 256 {
			return fmt.Errorf("calendar: event %d has invalid ID", i)
		}
		if seen[event.ID] {
			return fmt.Errorf("calendar: duplicate event ID %q", event.ID)
		}
		seen[event.ID] = true
		if event.Summary == "" || len(event.Summary) > maxEventSummary || len(event.Description) > maxEventDescription || len(event.Location) > maxEventLocation {
			return fmt.Errorf("calendar: event %q has invalid text bounds", event.ID)
		}
		if len(event.URL) > maxEventURL || event.URL != "" && !SafeHTTPURL(event.URL) {
			return fmt.Errorf("calendar: event %q has an unsafe meeting URL", event.ID)
		}
		if event.AllDay {
			start, errStart := time.Parse("2006-01-02", event.StartDate)
			end, errEnd := time.Parse("2006-01-02", event.EndDate)
			if errStart != nil || errEnd != nil || !start.Before(end) {
				return fmt.Errorf("calendar: event %q has invalid all-day dates", event.ID)
			}
		} else if event.Start.IsZero() || event.End.IsZero() || !event.Start.Before(event.End) {
			return fmt.Errorf("calendar: event %q has invalid time interval", event.ID)
		}
	}
	return nil
}

func SafeHTTPURL(raw string) bool {
	if raw == "" || len(raw) > maxEventURL {
		return false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, "https") || strings.EqualFold(u.Scheme, "http")
}

func sortEvents(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := eventSortStart(events[i]), eventSortStart(events[j])
		if a.Equal(b) {
			return events[i].ID < events[j].ID
		}
		return a.Before(b)
	})
}

func eventSortStart(event Event) time.Time {
	if event.AllDay {
		if day, err := time.Parse("2006-01-02", event.StartDate); err == nil {
			return day
		}
	}
	return event.Start
}

// NextTimedEvent chooses the currently active item, preferring the one that
// started most recently when calendars overlap, then the next future item.
func NextTimedEvent(events []Event, now time.Time) (event Event, active bool, ok bool) {
	for _, candidate := range events {
		if candidate.AllDay {
			continue
		}
		if !candidate.Start.After(now) && candidate.End.After(now) && (!ok || !active || candidate.Start.After(event.Start)) {
			event, active, ok = candidate, true, true
		}
	}
	if active {
		return event, active, true
	}
	for _, candidate := range events {
		if !candidate.AllDay && candidate.Start.After(now) && (!ok || candidate.Start.Before(event.Start)) {
			event, ok = candidate, true
		}
	}
	return event, false, ok
}

func (event Event) OccursOn(day time.Time) bool {
	day = localDay(day)
	if event.AllDay {
		key := day.Format("2006-01-02")
		return event.StartDate <= key && key < event.EndDate
	}
	start := event.Start.In(day.Location())
	end := event.End.In(day.Location())
	return start.Before(day.AddDate(0, 0, 1)) && end.After(day)
}

func (event Event) OccursOnRange(start, end time.Time) bool {
	if event.AllDay {
		first := start.Format("2006-01-02")
		last := end.Format("2006-01-02")
		return event.StartDate < last && event.EndDate > first
	}
	return event.Start.Before(end) && event.End.After(start)
}

func addMonthsClamped(date time.Time, delta int) time.Time {
	first := time.Date(date.Year(), date.Month(), 1, date.Hour(), date.Minute(), date.Second(), date.Nanosecond(), date.Location()).AddDate(0, delta, 0)
	day := date.Day()
	lastDay := time.Date(first.Year(), first.Month()+1, 0, date.Hour(), date.Minute(), date.Second(), date.Nanosecond(), date.Location()).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, date.Hour(), date.Minute(), date.Second(), date.Nanosecond(), date.Location())
}

func ViewRange(state PanelState, weekStart string) (time.Time, time.Time) {
	day := localDay(state.Date)
	switch state.View {
	case ViewMonth:
		start := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location())
		return start, start.AddDate(0, 1, 0)
	case ViewWeek:
		weekday := int(day.Weekday())
		if weekStart == "monday" {
			weekday = (weekday + 6) % 7
		}
		start := day.AddDate(0, 0, -weekday)
		return start, start.AddDate(0, 0, 7)
	case ViewFourDays:
		return day, day.AddDate(0, 0, 4)
	case ViewDay:
		return day, day.AddDate(0, 0, 1)
	default:
		return day, day.AddDate(0, 0, 7)
	}
}

func localDay(date time.Time) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, date.Location())
}
