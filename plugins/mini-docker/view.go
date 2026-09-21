package minidocker

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarTree renders the docker pill. One activatable button carrying the
// label — the click opens the panel (host only opens panels on the
// plugin's own CallPanelOpen), mirroring the world-clock bar shape. An
// unavailable docker tones the pill error: hidden must not read as
// zero-running.
func BarTree(label string, unavailable bool) *v1.Node {
	var tone v1.Tone
	if unavailable {
		tone = v1.ToneError
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Text: label, Tone: tone,
		Name: "Open mini docker", Role: "button", Tabular: true,
		Events: []v1.EventKind{v1.EventActivate},
	}}}
}

// BarLabel computes the bar text for the current settings and snapshot.
func BarLabel(showCount bool, statusMode string, running int, available bool) string {
	if !available {
		return "docker"
	}
	switch statusMode {
	case "hidden":
		return "docker"
	case "running_only":
		if running == 0 {
			return "docker"
		}
	}
	if showCount {
		return "docker " + strconv.Itoa(running)
	}
	return "docker"
}

// TooltipText computes the bar tooltip.
func TooltipText(running int, available bool) string {
	if !available {
		return "Docker unavailable"
	}
	if running == 1 {
		return "1 container running"
	}
	return strconv.Itoa(running) + " containers running"
}

// TooltipTree renders the bar tooltip. Tooltip views reject interactive
// nodes, so this is a plain column — never the bar's button tree.
func TooltipTree(text string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: text},
	}}
}

// PanelTree lists containers with lifecycle buttons; start is offered only
// for stopped containers, stop and restart only for running ones. actErr
// (the last failed action) outranks listErr: it is what the user just did,
// and the action's own refresh must not have erased it. A container with an
// action in flight gets its buttons disabled - no silent double-fires.
func PanelTree(available, loading bool, listErr, actErr, actingID string, containers []Container) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8, Padding: 16, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Docker containers", Size: "title", Bold: true},
			{Kind: v1.KindButton, ID: "refresh", Text: "Refresh", Name: "Refresh containers", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
	}}
	if actErr != "" {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: actErr, Tone: v1.ToneError})
	}
	if listErr != "" {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: listErr, Tone: v1.ToneError})
	}
	if loading {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "Loading…"})
		return col
	}
	if !available {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "Docker is not available"})
		return col
	}
	if len(containers) == 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "No containers"})
		return col
	}
	// Running containers surface first; exited ones must not bury the
	// actionable rows. Stable keeps each group in docker's own order.
	sorted := slices.Clone(containers)
	slices.SortStableFunc(sorted, func(a, b Container) int {
		if a.Running() != b.Running() {
			if a.Running() {
				return -1
			}
			return 1
		}
		return 0
	})
	rows := sorted
	list := &v1.Node{Kind: v1.KindList, Height: 400, Gap: 8}
	if len(rows) > maxPanelRows {
		rows = rows[:maxPanelRows]
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("+%d more", len(containers)-maxPanelRows), Tone: v1.ToneSubtle})
	}
	for _, c := range rows {
		list.Children = append(list.Children, containerRow(c, actingID))
	}
	col.Children = append(col.Children, list)
	return col
}

// maxPanelRows keeps the worst-case tree (6 nodes per row) inside the
// host's MaxNodes budget of 1024; the overflow is summarized in a footer.
const maxPanelRows = 150

func containerRow(c Container, actingID string) *v1.Node {
	tone := v1.ToneNormal
	if c.Running() {
		tone = v1.ToneAccent
	}
	row := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: c.Names + " · " + c.Status, Tone: tone},
		{Kind: v1.KindText, Text: c.Image, Tone: v1.ToneSubtle},
	}}
	disabled := c.ID == actingID
	actions := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if c.Running() {
		actions.Children = append(actions.Children,
			actionButton("stop:"+c.ID, "Stop", "Stop "+c.Names, disabled),
			actionButton("restart:"+c.ID, "Restart", "Restart "+c.Names, disabled),
		)
	} else {
		actions.Children = append(actions.Children,
			actionButton("start:"+c.ID, "Start", "Start "+c.Names, disabled),
		)
	}
	row.Children = append(row.Children, actions)
	return row
}

func actionButton(id, label, name string, disabled bool) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: name, Role: "button",
		Disabled: disabled, Events: []v1.EventKind{v1.EventActivate}}
}
