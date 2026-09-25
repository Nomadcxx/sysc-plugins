//go:build calendar_gate

package main

import (
	"context"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/calendar"
)

func init() { defaultCalendarQuery = calendarGateFixture }

func calendarGateFixture(ctx context.Context, start, end time.Time) (calendar.EDSResult, error) {
	if err := ctx.Err(); err != nil {
		return calendar.EDSResult{}, err
	}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	eventDay := day.AddDate(0, 0, 1)
	events := []calendar.Event{
		{
			ID: "product-review", CalendarID: "work", Calendar: "Work", Summary: "Product review",
			Description: "Review the current release and agree on next steps.", Location: "Room 2",
			URL: "https://meet.example/room", Start: eventDay.Add(10*time.Hour + 45*time.Minute), End: eventDay.Add(11*time.Hour + 45*time.Minute), Marker: "#0a7bde",
		},
		{
			ID: "design-planning", CalendarID: "work", Calendar: "Work", Summary: "Design planning",
			Location: "Studio", Start: eventDay.Add(11 * time.Hour), End: eventDay.Add(12 * time.Hour), Marker: "#0a7bde",
		},
		{
			ID: "leave", CalendarID: "personal", Calendar: "Personal", Summary: "Leave",
			AllDay: true, StartDate: day.Format("2006-01-02"), EndDate: day.AddDate(0, 0, 2).Format("2006-01-02"), Marker: "secondary",
		},
		{
			ID: "team-lunch", CalendarID: "personal", Calendar: "Personal", Summary: "Team lunch",
			Start: eventDay.Add(12 * time.Hour), End: eventDay.Add(13 * time.Hour), Marker: "secondary",
		},
	}
	var visible []calendar.Event
	for _, event := range events {
		if event.OccursOnRange(start, end) {
			visible = append(visible, event)
		}
	}
	return calendar.EDSResult{
		Available: true,
		Calendars: []calendar.CalendarSource{
			{ID: "work", Name: "Work", Color: "#0a7bde"},
			{ID: "personal", Name: "Personal", Color: "#a56de2"},
		},
		Events: visible,
	}, nil
}
