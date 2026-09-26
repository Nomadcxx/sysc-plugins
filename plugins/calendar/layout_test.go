package calendar

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The shell gave this panel about 810x758 on a 1536x864 laptop while the
// manifest asked for 1040x760 (sysc-578). Until the host reports the granted
// size, the panel is designed for a box both machines honour.
func TestManifestPanelMatchesDesignedBox(t *testing.T) {
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Panels []struct{ Width, Height int } `json:"panels"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Panels) != 1 {
		t.Fatalf("manifest panels: %v %+v", err, m)
	}
	if m.Panels[0].Width != PanelWidth || m.Panels[0].Height != PanelHeight {
		t.Fatalf("manifest %dx%d, code %dx%d", m.Panels[0].Width, m.Panels[0].Height, PanelWidth, PanelHeight)
	}
	if PanelWidth > 800 || PanelHeight > 720 {
		t.Fatalf("panel %dx%d exceeds the box the laptop grants", PanelWidth, PanelHeight)
	}
}

func layoutFixtures() (time.Time, []CalendarSource) {
	now := time.Date(2026, 9, 26, 13, 0, 0, 0, time.UTC)
	return now, []CalendarSource{{ID: "birthdays", Name: "Birthdays & Anniversaries"}, {ID: "personal", Name: "Personal"}}
}

func TestEveryViewAndStatusFitsThePanel(t *testing.T) {
	now, sources := layoutFixtures()
	many := make([]CalendarSource, 12)
	for i := range many {
		many[i] = CalendarSource{ID: "src-" + string(rune('a'+i)), Name: "Team calendar " + string(rune('A'+i))}
	}
	statuses := map[string]LoadStatus{
		"loaded":       {Available: true, CalendarCount: 2},
		"loading":      {Loading: true},
		"unavailable":  {Error: "EDS is not running"},
		"no calendars": {Available: true},
		"read error":   {Available: true, CalendarCount: 1, Error: "connection timed out after a long wait for the calendar factory"},
	}
	for _, mode := range []ViewMode{ViewMonth, ViewWeek, ViewFourDays, ViewDay, ViewAgenda} {
		for name, status := range statuses {
			for label, srcs := range map[string][]CalendarSource{"two": sources, "twelve": many} {
				for _, expanded := range []bool{false, true} {
					root := PanelTree(PanelState{Date: now, View: mode}, nil, "monday", status, now, srcs, nil, expanded)
					for _, f := range lint.Tree(root, v1.ViewPanel, PanelWidth, PanelHeight) {
						t.Errorf("%s/%s/%s/expanded=%v: %s", mode, name, label, expanded, f)
					}
				}
			}
		}
	}
}

func monthGridParts(t *testing.T) (weekdays []*v1.Node, dates []*v1.Node) {
	t.Helper()
	now, sources := layoutFixtures()
	root := PanelTree(PanelState{Date: now, View: ViewMonth}, nil, "monday", LoadStatus{Available: true, CalendarCount: 2}, now, sources, nil, false)
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if strings.HasPrefix(n.ID, "cal-date-") {
			dates = append(dates, n)
		}
		if strings.HasPrefix(n.Key, "weekday-") {
			weekdays = append(weekdays, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return weekdays, dates
}

// On hardware the weekday letters bunched at the left, unaligned with the
// day circles: text measures its own width, so each label is now a column
// cell exactly as wide as a date button.
func TestWeekdayHeaderCellsMatchDateColumns(t *testing.T) {
	weekdays, dates := monthGridParts(t)
	if len(weekdays) != 7 || len(dates) < 28 || len(dates)%7 != 0 {
		t.Fatalf("weekdays=%d dates=%d", len(weekdays), len(dates))
	}
	for _, w := range weekdays {
		if w.Kind != v1.KindColumn || w.Width != dates[0].Width || len(w.Children) != 1 || !w.Children[0].CenterX {
			t.Fatalf("weekday cell %+v does not match date width %d", w, dates[0].Width)
		}
	}
}

// The grid filled a quarter of the panel; the cells now use the room.
func TestMonthGridUsesThePanel(t *testing.T) {
	_, dates := monthGridParts(t)
	cell := dates[0].Width
	if cell < 52 || dates[0].Height != cell {
		t.Fatalf("date cell %dx%d is not a square of at least 52", cell, dates[0].Height)
	}
	if grid := 7*cell + 6*monthCellGap; grid > PanelWidth/2+80 {
		t.Fatalf("grid %d px leaves the day list no room", grid)
	}
}

func TestHeaderStepButtonsAndShortcutHint(t *testing.T) {
	now, sources := layoutFixtures()
	root := PanelTree(PanelState{Date: now, View: ViewMonth}, nil, "monday", LoadStatus{Available: true, CalendarCount: 2}, now, sources, nil, false)
	for _, id := range []string{"cal-prev", "cal-next"} {
		if n := findNode(root, id); n == nil || n.Width < 32 {
			t.Fatalf("%s must keep a fixed width so it is never squeezed to a sliver: %+v", id, n)
		}
	}
	if treeText(root, "J / K  Previous or next event     T  Today     C  Copy details     Ctrl + R  Refresh") {
		t.Fatal("shortcut hint still takes a body row")
	}
	hint := findNode(root, "cal-shortcuts")
	if hint == nil || hint.Icon != "keyboard" || !strings.Contains(hint.Tooltip, "J / K") {
		t.Fatalf("shortcut hint button = %+v", hint)
	}
}

func TestCommonCalendarNamesAreNotTruncated(t *testing.T) {
	now, sources := layoutFixtures()
	root := PanelTree(PanelState{Date: now, View: ViewMonth}, nil, "monday", LoadStatus{Available: true, CalendarCount: 2}, now, sources, nil, false)
	if n := findNode(root, CalendarToggleNodeID("birthdays")); n == nil || n.Text != "Birthdays & Anniversaries" {
		t.Fatalf("birthdays chip = %+v", n)
	}
}
