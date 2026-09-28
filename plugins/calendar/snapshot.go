package calendar

import "time"

// ControlCenterSnapshot is the compact, bounded summary the sysc-shell control
// center reads from this plugin's persistent state under key "control_center".
type ControlCenterSnapshot struct {
	Generated string             `json:"generated"`
	Sources   int                `json:"sources"`
	Days      map[string]int     `json:"days"`
	Upcoming  []ControlCenterOcc `json:"upcoming"`
}

type ControlCenterOcc struct {
	Start    string `json:"start"`
	Summary  string `json:"summary"`
	Calendar string `json:"calendar"`
	AllDay   bool   `json:"all_day,omitempty"`
}

const (
	maxSnapshotDays     = 400
	maxSnapshotUpcoming = 8
	maxSnapshotSummary  = 80
	maxAllDaySpanDays   = 31
)

// BuildControlCenterSnapshot summarises already-sorted events for the shell.
// ponytail: bounded 400 day-counters + 8 upcoming occurrences; the full
// agenda lives in the plugin panel, the control center only needs markers.
func BuildControlCenterSnapshot(events []Event, sources int, now time.Time) ControlCenterSnapshot {
	snapshot := ControlCenterSnapshot{
		Generated: now.UTC().Format(time.RFC3339),
		Sources:   sources,
		Days:      make(map[string]int, 64),
		Upcoming:  make([]ControlCenterOcc, 0, maxSnapshotUpcoming),
	}
	today := now.Format("2006-01-02")
	for _, event := range events {
		if event.AllDay {
			start, errStart := time.Parse("2006-01-02", event.StartDate)
			end, errEnd := time.Parse("2006-01-02", event.EndDate)
			if errStart != nil || errEnd != nil || !end.After(start) {
				continue
			}
			for day, span := start, 0; day.Before(end) && span < maxAllDaySpanDays; day, span = day.AddDate(0, 0, 1), span+1 {
				if len(snapshot.Days) < maxSnapshotDays {
					snapshot.Days[day.Format("2006-01-02")]++
				}
			}
			if event.EndDate > today && len(snapshot.Upcoming) < maxSnapshotUpcoming {
				snapshot.Upcoming = append(snapshot.Upcoming, snapshotOcc(event, event.StartDate, true))
			}
		} else {
			if len(snapshot.Days) < maxSnapshotDays {
				snapshot.Days[event.Start.Format("2006-01-02")]++
			}
			if event.End.After(now) && len(snapshot.Upcoming) < maxSnapshotUpcoming {
				snapshot.Upcoming = append(snapshot.Upcoming, snapshotOcc(event, event.Start.Format(time.RFC3339), false))
			}
		}
	}
	return snapshot
}

func snapshotOcc(event Event, start string, allDay bool) ControlCenterOcc {
	summary := event.Summary
	if len(summary) > maxSnapshotSummary {
		summary = summary[:maxSnapshotSummary]
	}
	return ControlCenterOcc{Start: start, Summary: summary, Calendar: event.Calendar, AllDay: allDay}
}
