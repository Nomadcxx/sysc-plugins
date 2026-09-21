package minidocker

import (
	"strconv"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarTree renders the docker pill. One activatable button carrying the
// label — the click opens the panel (host only opens panels on the
// plugin's own CallPanelOpen), mirroring the world-clock bar shape.
func BarTree(label string) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Text: label,
		Name: "Open mini docker", Role: "button",
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

// PanelTree lists containers with lifecycle buttons; start is offered only
// for stopped containers, stop and restart only for running ones.
func PanelTree(available, loading bool, errMsg string, containers []Container) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Docker containers"},
			{Kind: v1.KindButton, ID: "refresh", Text: "Refresh", Name: "Refresh containers", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
	}}
	if errMsg != "" {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: errMsg, Tone: v1.ToneError})
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
	for _, c := range containers {
		col.Children = append(col.Children, containerRow(c))
	}
	return col
}

func containerRow(c Container) *v1.Node {
	row := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: c.Names + " · " + c.Status},
		{Kind: v1.KindText, Text: c.Image, Tone: v1.ToneSubtle},
	}}
	actions := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if c.Running() {
		actions.Children = append(actions.Children,
			actionButton("stop:"+c.ID, "Stop", "Stop "+c.Names),
			actionButton("restart:"+c.ID, "Restart", "Restart "+c.Names),
		)
	} else {
		actions.Children = append(actions.Children,
			actionButton("start:"+c.ID, "Start", "Start "+c.Names),
		)
	}
	row.Children = append(row.Children, actions)
	return row
}

func actionButton(id, label, name string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: name, Role: "button",
		Events: []v1.EventKind{v1.EventActivate}}
}
