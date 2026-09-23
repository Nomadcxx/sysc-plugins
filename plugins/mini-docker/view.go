package minidocker

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

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
// show_count was folded into status_mode: one setting, honest labels.
func BarLabel(statusMode string, running int, available bool) string {
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
	return "docker " + strconv.Itoa(running)
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

// PanelTree preserves the original container-only call shape for the plugin
// process while the renderer consumes the complete session snapshot.
func PanelTree(available, loading bool, listErr, actErr, actingID string, containers []Container) *v1.Node {
	return PanelTreeForSession(SessionSnapshot{
		Scope:        ScopeContainers,
		Containers:   containers,
		ContainerTab: TabStatus{Available: available, Loading: loading, ListError: listErr},
		ActionError:  actErr,
		ActingID:     actingID,
	})
}

// PanelTreeForSession renders the four-tab shell and the container detail
// view from an immutable state copy. Docker I/O belongs to Session.
func PanelTreeForSession(state SessionSnapshot) *v1.Node {
	if !validScope(state.Scope) {
		state.Scope = ScopeContainers
	}
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8, Padding: 16, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Docker containers", Size: "title", Bold: true},
			{Kind: v1.KindButton, ID: "refresh", Text: "Refresh", Name: "Refresh containers", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
	}}

	col.Children = append(col.Children, scopeButtons(state.Scope))
	status := panelStatus(state)
	if len(status) != 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: status})
	}

	var rows []Container
	containerStatus := state.ContainerTab
	if state.Scope == ScopeContainers && containerStatus.Available {
		rows = sortedContainers(state.Containers)
	}
	showLoading := state.Scope == ScopeContainers && containerStatus.Loading && !containerStatus.Available
	listHeight := 400 - 20*len(status)
	selected := selectedContainer(state)
	if selected != nil {
		listHeight -= 112
	}
	if listHeight < 200 {
		listHeight = 200
	}
	list := &v1.Node{Kind: v1.KindList, Height: listHeight, Gap: 4}
	if state.Scope != ScopeContainers {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: emptyTabText(state.Scope), Tone: v1.ToneSubtle})
	} else if showLoading {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: "Loading…", Tone: v1.ToneSubtle})
	} else if !containerStatus.Available || len(rows) == 0 {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: "No containers", Tone: v1.ToneSubtle})
	}
	visibleRows := rows
	if len(rows) > maxPanelRows {
		visibleRows = rows[:maxPanelRows]
	}
	for _, c := range visibleRows {
		list.Children = append(list.Children, containerRow(c, state.SelectedID))
	}
	col.Children = append(col.Children, list)
	if len(rows) > maxPanelRows {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText,
			Text: fmt.Sprintf("+%d more", len(rows)-maxPanelRows), Tone: v1.ToneSubtle})
	}
	if selected != nil {
		col.Children = append(col.Children, containerDetail(*selected, state.ActingID))
	}
	return col
}

func scopeButtons(selected Scope) *v1.Node {
	buttons := &v1.Node{Kind: v1.KindRow, Gap: 4}
	for _, scope := range []Scope{ScopeContainers, ScopeImages, ScopeVolumes, ScopeNetworks} {
		label := strings.ToUpper(string(scope[:1])) + string(scope[1:])
		fill := "outline"
		if scope == selected {
			fill = "accent"
		}
		buttons.Children = append(buttons.Children, &v1.Node{Kind: v1.KindButton,
			ID: "tab:" + string(scope), Text: label, Name: label + " tab", Role: "button",
			Fill: fill, Height: 28, Events: []v1.EventKind{v1.EventActivate}})
	}
	return buttons
}

func panelStatus(state SessionSnapshot) []*v1.Node {
	var status []*v1.Node
	appendLine := func(text string, tone v1.Tone) {
		if text != "" && len(status) < 2 {
			status = append(status, &v1.Node{Kind: v1.KindText, Text: text, Tone: tone})
		}
	}
	active := tabStatus(state)
	appendLine(state.ActionError, v1.ToneError)
	appendLine(active.ListError, v1.ToneError)
	if len(status) < 2 {
		switch {
		case state.ActingID != "":
			appendLine("Working…", v1.ToneSubtle)
		case active.Loading && active.Available:
			appendLine("Refreshing…", v1.ToneSubtle)
		case active.SkippedLines > 0:
			appendLine(fmt.Sprintf("%d malformed lines skipped", active.SkippedLines), v1.ToneSubtle)
		}
	}
	return status
}

func tabStatus(state SessionSnapshot) TabStatus {
	switch state.Scope {
	case ScopeImages:
		return state.ImageTab
	case ScopeVolumes:
		return state.VolumeTab
	case ScopeNetworks:
		return state.NetworkTab
	default:
		return state.ContainerTab
	}
}

func emptyTabText(scope Scope) string {
	switch scope {
	case ScopeImages:
		return "No images"
	case ScopeVolumes:
		return "No volumes"
	case ScopeNetworks:
		return "No networks"
	default:
		return "No containers"
	}
}

func sortedContainers(containers []Container) []Container {
	sorted := slices.Clone(containers)
	slices.SortFunc(sorted, func(a, b Container) int {
		if a.Running() != b.Running() {
			if a.Running() {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.Names, b.Names); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return sorted
}

func selectedContainer(state SessionSnapshot) *Container {
	if state.Scope != ScopeContainers || !state.ContainerTab.Available || state.SelectedID == "" {
		return nil
	}
	for i := range state.Containers {
		if state.Containers[i].ID == state.SelectedID {
			selected := state.Containers[i]
			return &selected
		}
	}
	return nil
}

// maxPanelRows keeps the worst-case tree (6 nodes per row) inside the
// host's MaxNodes budget of 1024; the overflow is summarized in a footer.
const maxPanelRows = 150

func containerRow(c Container, selectedID string) *v1.Node {
	tone := v1.ToneNormal
	if c.Running() {
		tone = v1.ToneAccent
	}
	fill := "outline"
	if c.ID == selectedID {
		fill = "card"
	}
	return &v1.Node{Kind: v1.KindButton, ID: "select:" + c.ID, Name: "Select " + c.Names,
		Role: "button", Fill: fill, Radius: 10, Padding: 8, Height: 54,
		Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{{
			Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
				{Kind: v1.KindText, Text: c.Names, Tone: tone, Bold: true},
				{Kind: v1.KindText, Text: c.Image + " · " + c.Status, Tone: v1.ToneSubtle},
			},
		}},
	}
}

func containerDetail(c Container, actingID string) *v1.Node {
	info := c.Image + " · " + c.Status
	actions := &v1.Node{Kind: v1.KindRow, Gap: 4}
	disabled := c.ID == actingID
	if c.Running() {
		actions.Children = append(actions.Children,
			actionButton("stop:"+c.ID, "Stop", "Stop "+c.Names, disabled),
			actionButton("restart:"+c.ID, "Restart", "Restart "+c.Names, disabled),
		)
	} else {
		actions.Children = append(actions.Children, actionButton("start:"+c.ID, "Start", "Start "+c.Names, disabled))
	}
	actions.Children = append(actions.Children,
		actionButton("remove:"+c.ID, "Remove", "Remove "+c.Names, disabled || c.Running()))
	return &v1.Node{Kind: v1.KindColumn, ID: "detail", Fill: "card", Radius: 10,
		Padding: 8, Gap: 4, Height: 112, Children: []*v1.Node{
			{Kind: v1.KindText, Text: c.Names, Bold: true},
			{Kind: v1.KindText, Text: info, Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: c.ID, Tone: v1.ToneSubtle},
			actions,
		}}
}

func actionButton(id, label, name string, disabled bool) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: name, Role: "button",
		Height: 28, Disabled: disabled, Events: []v1.EventKind{v1.EventActivate}}
}

// actionPrefixes drives ParseAction; each entry pairs a node-ID prefix with
// the docker action it dispatches.
var actionPrefixes = []struct {
	prefix, action string
}{
	{"start:", "start"},
	{"stop:", "stop"},
	{"restart:", "restart"},
}

// idRE allow-lists the container ID half of an action node ID before it is
// echoed into a docker argv. Docker IDs are hex, but the widest honest
// contract is plain identifier characters.
var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ParseAction splits an action node ID ("start:<id>") into its verb and
// container ID, rejecting anything that should never reach an argv.
func ParseAction(node string) (action, id string, ok bool) {
	for _, p := range actionPrefixes {
		if strings.HasPrefix(node, p.prefix) {
			id = node[len(p.prefix):]
			if idRE.MatchString(id) {
				return p.action, id, true
			}
			return "", "", false
		}
	}
	return "", "", false
}
