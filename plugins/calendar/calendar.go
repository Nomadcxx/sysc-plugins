// Package calendar ports the shell's built-in clock-panel month grid into a
// plugin: a bar widget with today's date, a tooltip, and a navigable month
// panel.
package calendar

import (
	"sync"
	"time"
)

// Cell is one day box in the month grid.
type Cell struct {
	Day      int
	Date     time.Time
	InMonth  bool
	Today    bool
	Selected bool
}

// Model holds the plugin's navigation state: which month is shown, where the
// week starts, and how to reach the clock.
type Model struct {
	mu        sync.Mutex
	now       func() time.Time
	weekStart string
	panels    map[string]PanelState
	events    []Event
}

func New(now func() time.Time) *Model {
	if now == nil {
		now = time.Now
	}
	return &Model{now: now, weekStart: "monday", panels: make(map[string]PanelState)}
}

// Panel returns this panel instance's independent calendar state.
func (m *Model) Panel(id string) PanelState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.panelLocked(id)
}

func (m *Model) ClosePanel(id string) {
	m.mu.Lock()
	delete(m.panels, id)
	m.mu.Unlock()
}

func (m *Model) panelLocked(id string) PanelState {
	state, ok := m.panels[id]
	if !ok {
		state = PanelState{Date: localDay(m.now()), View: ViewMonth}
		m.panels[id] = state
	}
	return state
}

func (m *Model) SetPanelView(id string, view ViewMode) bool {
	if !view.Valid() {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.panelLocked(id)
	state.View, state.Details = view, false
	state.SelectedEventID = ""
	m.panels[id] = state
	return true
}

// Step moves by the active view's range while retaining the selected local day.
func (m *Model) Step(id string, amount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.panelLocked(id)
	switch state.View {
	case ViewMonth:
		state.Date = addMonthsClamped(state.Date, amount)
	case ViewWeek:
		state.Date = state.Date.AddDate(0, 0, amount*7)
	case ViewFourDays:
		state.Date = state.Date.AddDate(0, 0, amount*4)
	case ViewDay:
		state.Date = state.Date.AddDate(0, 0, amount)
	case ViewAgenda:
		state.Date = state.Date.AddDate(0, 0, amount*7)
	}
	state.Details, state.SelectedEventID = false, ""
	m.panels[id] = state
}

func (m *Model) TodayFor(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.panelLocked(id)
	state.Date = localDay(m.now()).In(state.Date.Location())
	state.Details, state.SelectedEventID = false, ""
	m.panels[id] = state
}

func (m *Model) SelectDate(id string, date time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.panelLocked(id)
	state.Date = localDay(date).In(state.Date.Location())
	state.Details, state.SelectedEventID = false, ""
	m.panels[id] = state
}

func (m *Model) OpenEvent(id, eventID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, event := range m.events {
		if event.ID == eventID {
			state := m.panelLocked(id)
			if event.AllDay {
				if date, err := time.Parse("2006-01-02", event.StartDate); err == nil {
					state.Date = date.In(state.Date.Location())
				}
			} else {
				state.Date = localDay(event.Start).In(state.Date.Location())
			}
			state.SelectedEventID, state.Details = eventID, true
			m.panels[id] = state
			return true
		}
	}
	return false
}

func (m *Model) CloseEvent(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.panelLocked(id)
	state.SelectedEventID, state.Details = "", false
	m.panels[id] = state
}

// SetEvents replaces the shared immutable snapshot after checking its bounds.
func (m *Model) SetEvents(events []Event) error {
	if err := ValidateEvents(events); err != nil {
		return err
	}
	copyOfEvents := append([]Event(nil), events...)
	sortEvents(copyOfEvents)
	m.mu.Lock()
	m.events = copyOfEvents
	m.mu.Unlock()
	return nil
}

func (m *Model) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.events...)
}

func (m *Model) EventsForDay(date time.Time) []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var events []Event
	for _, event := range m.events {
		if event.OccursOn(date) {
			events = append(events, event)
		}
	}
	return events
}

func (m *Model) AdjacentEvent(id string, direction int) (Event, bool) {
	return m.AdjacentEventIn(id, direction, m.Events())
}

// AdjacentEventIn navigates the supplied visible event snapshot for one panel.
func (m *Model) AdjacentEventIn(id string, direction int, candidates []Event) (Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if direction == 0 || len(candidates) == 0 {
		return Event{}, false
	}
	if direction > 0 {
		direction = 1
	} else {
		direction = -1
	}
	state := m.panelLocked(id)
	start, end := ViewRange(state, m.weekStart)
	var events []Event
	for _, event := range candidates {
		if event.OccursOnRange(start, end) {
			events = append(events, event)
		}
	}
	if len(events) == 0 {
		return Event{}, false
	}
	index := -1
	for i := range events {
		if events[i].ID == state.SelectedEventID {
			index = i
			break
		}
	}
	if index < 0 {
		if direction > 0 {
			index = -1
		} else {
			index = 0
		}
	}
	index = (index + direction + len(events)) % len(events)
	state.SelectedEventID, state.Details = events[index].ID, true
	m.panels[id] = state
	return events[index], true
}

// SetWeekStart accepts "monday" or "sunday"; anything else keeps the current
// value.
func (m *Model) SetWeekStart(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s == "monday" || s == "sunday" {
		m.weekStart = s
	}
}

func (m *Model) WeekStart() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.weekStart
}

// Date renders the tooltip's long form of today.
func (m *Model) Date() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now().Format("Mon 2 Jan 2006")
}

// DayOfMonth renders the bar widget's compact label.
func (m *Model) DayOfMonth() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now().Format("2")
}

// WeekdayHeader returns the one-letter day labels for the current setting,
// left to right.
func (m *Model) WeekdayHeader() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.weekStart == "sunday" {
		return []string{"S", "M", "T", "W", "T", "F", "S"}
	}
	return []string{"M", "T", "W", "T", "F", "S", "S"}
}

// grid lays out the viewed month with leading days from the previous month
// and trailing days from the next, in whole weeks under the given week start.
// isCurrent gates the today marker so navigated months never mark one.
func grid(view time.Time, weekStart string, isCurrent bool) [][]Cell {
	today := time.Time{}
	if isCurrent {
		today = view
	}
	return monthGrid(view, weekStart, today, view)
}

func monthGrid(view time.Time, weekStart string, today, selected time.Time) [][]Cell {
	first := time.Date(view.Year(), view.Month(), 1, 0, 0, 0, 0, view.Location())
	days := time.Date(view.Year(), view.Month()+1, 0, 0, 0, 0, 0, view.Location()).Day()
	lead := int(first.Weekday())
	if weekStart == "monday" {
		lead = (lead + 6) % 7
	}

	start := first.AddDate(0, 0, -lead)
	rows := (lead + days + 6) / 7
	if rows < 5 {
		rows = 5
	}
	weeks := make([][]Cell, rows)
	for week := 0; week < rows; week++ {
		weeks[week] = make([]Cell, 7)
		for weekday := 0; weekday < 7; weekday++ {
			date := start.AddDate(0, 0, week*7+weekday)
			weeks[week][weekday] = Cell{
				Day: date.Day(), Date: date, InMonth: date.Month() == view.Month(),
				Today: sameDate(date, today), Selected: sameDate(date, selected),
			}
		}
	}
	return weeks
}

func sameDate(a, b time.Time) bool {
	return !b.IsZero() && a.Year() == b.In(a.Location()).Year() && a.Month() == b.In(a.Location()).Month() && a.Day() == b.In(a.Location()).Day()
}
