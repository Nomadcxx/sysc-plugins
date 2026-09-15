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
	Day     int
	InMonth bool
	Today   bool
}

// Model holds the plugin's navigation state: which month is shown, where the
// week starts, and how to reach the clock.
type Model struct {
	mu          sync.Mutex
	now         func() time.Time
	monthOffset int
	weekStart   string
}

func New(now func() time.Time) *Model {
	if now == nil {
		now = time.Now
	}
	return &Model{now: now, weekStart: "monday"}
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

// PrevMonth and NextMonth move the panel's view one month at a time.
func (m *Model) PrevMonth() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monthOffset--
}

func (m *Model) NextMonth() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monthOffset++
}

// Today snaps the panel back to the current month.
func (m *Model) Today() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monthOffset = 0
}

// View returns the month the panel should draw: the viewed time, the grid
// weeks, and the header label. Today is marked only when the current month is
// on screen.
func (m *Model) View() (view time.Time, weeks [][]Cell, header string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	view = now.AddDate(0, m.monthOffset, 0)
	return view, grid(view, m.weekStart, m.monthOffset == 0), view.Format("January 2006")
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
	first := time.Date(view.Year(), view.Month(), 1, 0, 0, 0, 0, view.Location())
	days := time.Date(view.Year(), view.Month()+1, 0, 0, 0, 0, 0, view.Location()).Day()
	prevDays := time.Date(view.Year(), view.Month(), 0, 0, 0, 0, 0, view.Location()).Day()

	lead := int(first.Weekday())
	if weekStart == "monday" {
		lead = (lead + 6) % 7
	}

	var cells []Cell
	for i := lead; i > 0; i-- {
		cells = append(cells, Cell{Day: prevDays - i + 1})
	}
	for d := 1; d <= days; d++ {
		cells = append(cells, Cell{
			Day:     d,
			InMonth: true,
			Today:   isCurrent && d == view.Day(),
		})
	}
	for len(cells)%7 != 0 {
		cells = append(cells, Cell{Day: len(cells) - lead - days + 1})
	}
	weeks := make([][]Cell, 0, len(cells)/7)
	for i := 0; i < len(cells); i += 7 {
		weeks = append(weeks, cells[i:i+7])
	}
	return weeks
}
