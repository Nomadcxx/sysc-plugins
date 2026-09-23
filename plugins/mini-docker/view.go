package minidocker

import (
	"fmt"
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

// TooltipTreeForSession adds the current container-list diagnosis while
// keeping the tooltip read-only and within its two-line contract.
func TooltipTreeForSession(state SessionSnapshot, running int) *v1.Node {
	root := TooltipTree(TooltipText(running, state.ContainerTab.Available))
	if !state.ContainerTab.Available && state.ContainerTab.ListError != "" {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText,
			Text: state.ContainerTab.ListError, Tone: v1.ToneError})
	}
	return root
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
			{Kind: v1.KindText, Text: "Docker " + string(state.Scope), Size: "title", Bold: true},
			{Kind: v1.KindButton, ID: "refresh", Text: "Refresh", Name: "Refresh " + string(state.Scope), Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
	}}

	col.Children = append(col.Children, scopeButtons(state.Scope))
	status := panelStatus(state)
	if len(status) != 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: status})
	}
	if state.RunForm != nil {
		col.Children = append(col.Children, runFormTree(*state.RunForm, state))
		return col
	}

	statusState := tabStatus(state)
	entities := panelEntities(state)
	selected := selectedEntity(state, entities)
	selectedID := state.SelectedID
	if state.PendingRemoval != nil && state.PendingRemoval.Scope == state.Scope {
		selectedID = state.PendingRemoval.ID
	}
	showLoading := statusState.Loading && !statusState.Available
	listHeight := 400 - 20*len(status)
	if selected != nil {
		listHeight -= 112
	}
	if listHeight < 200 {
		listHeight = 200
	}
	list := &v1.Node{Kind: v1.KindList, Height: listHeight, Gap: 4}
	if showLoading {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: "Loading…", Tone: v1.ToneSubtle})
	} else if !statusState.Available || len(entities) == 0 {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: emptyTabText(state.Scope), Tone: v1.ToneSubtle})
	}
	visibleEntities := entities
	if len(entities) > maxPanelRows {
		visibleEntities = entities[:maxPanelRows]
	}
	for _, entity := range visibleEntities {
		if entity.Scope == ScopeContainers {
			list.Children = append(list.Children, containerRow(*entity.container, selectedID))
		} else {
			list.Children = append(list.Children, entityRow(entity, selectedID))
		}
	}
	col.Children = append(col.Children, list)
	if len(entities) > maxPanelRows {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText,
			Text: fmt.Sprintf("+%d more", len(entities)-maxPanelRows), Tone: v1.ToneSubtle})
	}
	if selected != nil {
		col.Children = append(col.Children, entityDetail(*selected, state))
	}
	return col
}

func runFormTree(draft RunDraft, state SessionSnapshot) *v1.Node {
	form := &v1.Node{Kind: v1.KindColumn, ID: "run-form", Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindText, Text: draft.ImageRef, Bold: true},
		{Kind: v1.KindText, Text: "Container name (optional)", Tone: v1.ToneSubtle},
		textInput("name", "Container name", draft.Name, false, true, draft.Reseed),
		{Kind: v1.KindText, Text: "Host port (optional)", Tone: v1.ToneSubtle},
		textInput("port", "Host port", draft.Port, false, true, draft.Reseed),
		actionButton("form:publish", publishLabel(draft.Publish), publishLabel(draft.Publish), false),
		{Kind: v1.KindText, Text: "Network", Tone: v1.ToneSubtle},
		{Kind: v1.KindButton, ID: "form:network", Text: networkLabel(draft.Network),
			Name: "Cycle network", Role: "button", Height: 32, Events: []v1.EventKind{v1.EventActivate}},
		{Kind: v1.KindText, Text: "Environment (one KEY=value per line)", Tone: v1.ToneSubtle},
		textInput("env", "Environment variables", draft.Environment, true, false, draft.Reseed),
	}}
	if draft.Error != "" {
		form.Children = append(form.Children, &v1.Node{Kind: v1.KindText, Text: draft.Error, Tone: v1.ToneError})
	}
	busy := actionInFlight(state, ScopeImages, "run", draft.ImageID)
	run := actionButton("run-submit", "Run", "Run "+draft.ImageRef, draft.Error != "" || busy)
	run.Fill = "accent"
	form.Children = append(form.Children, &v1.Node{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
		actionButton("form:cancel", "Cancel", "Cancel image run", false),
		run,
	}})
	return form
}

func textInput(id, name, value string, multiline, submitOnEnter bool, reseed uint64) *v1.Node {
	return &v1.Node{Kind: v1.KindTextInput, ID: id, Text: value, Name: name, Role: "textbox",
		Height: 40, Multiline: multiline, SubmitOnEnter: submitOnEnter, Reseed: reseed,
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}}
}

func publishLabel(publish bool) string {
	if publish {
		return "Publish port: on"
	}
	return "Publish port: off"
}

func networkLabel(network string) string {
	if network == "" {
		return "Select network"
	}
	return "Network: " + network
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
		case hasInFlightScope(state, state.Scope) || state.Scope == ScopeContainers && state.ActingID != "":
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

type panelEntity struct {
	Scope      Scope
	ID         string
	Name       string
	Summary    string
	Referenced int
	Builtin    bool
	container  *Container
}

func panelEntities(state SessionSnapshot) []panelEntity {
	switch state.Scope {
	case ScopeContainers:
		if !state.ContainerTab.Available {
			return nil
		}
		containers := sortedContainers(state.Containers)
		entities := make([]panelEntity, 0, len(containers))
		for i := range containers {
			c := containers[i]
			entities = append(entities, panelEntity{
				Scope: state.Scope, ID: c.ID, Name: c.Names, Summary: c.Image + " · " + c.Status,
				container: &c,
			})
		}
		return entities
	case ScopeImages:
		if !state.ImageTab.Available {
			return nil
		}
		images := slices.Clone(state.Images)
		slices.SortFunc(images, func(a, b Image) int {
			if c := strings.Compare(a.Repository+":"+a.Tag, b.Repository+":"+b.Tag); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		})
		entities := make([]panelEntity, 0, len(images))
		for _, image := range images {
			entities = append(entities, panelEntity{
				Scope: state.Scope, ID: image.ID, Name: image.Repository + ":" + image.Tag,
				Summary: image.ID + " · " + image.Size, Referenced: image.Containers,
			})
		}
		return entities
	case ScopeVolumes:
		if !state.VolumeTab.Available {
			return nil
		}
		volumes := slices.Clone(state.Volumes)
		slices.SortFunc(volumes, func(a, b Volume) int {
			if c := strings.Compare(a.Name, b.Name); c != 0 {
				return c
			}
			return strings.Compare(a.Driver, b.Driver)
		})
		entities := make([]panelEntity, 0, len(volumes))
		for _, volume := range volumes {
			entities = append(entities, panelEntity{
				Scope: state.Scope, ID: volume.Name, Name: volume.Name,
				Summary: volume.Driver + " · " + volume.Scope,
			})
		}
		return entities
	case ScopeNetworks:
		if !state.NetworkTab.Available {
			return nil
		}
		networks := sortedNetworks(state.Networks)
		entities := make([]panelEntity, 0, len(networks))
		for _, network := range networks {
			entities = append(entities, panelEntity{
				Scope: state.Scope, ID: network.ID, Name: network.Name,
				Summary: network.Driver + " · " + network.Scope,
				Builtin: network.Name == "bridge" || network.Name == "host" || network.Name == "none",
			})
		}
		return entities
	default:
		return nil
	}
}

func selectedEntity(state SessionSnapshot, entities []panelEntity) *panelEntity {
	id := state.SelectedID
	if state.PendingRemoval != nil && state.PendingRemoval.Scope == state.Scope {
		id = state.PendingRemoval.ID
	}
	if id == "" {
		return nil
	}
	for i := range entities {
		if entities[i].ID == id {
			return &entities[i]
		}
	}
	return nil
}

func entityRow(entity panelEntity, selectedID string) *v1.Node {
	fill := "outline"
	if entity.ID == selectedID {
		fill = "card"
	}
	return &v1.Node{Kind: v1.KindButton, ID: "select:" + entity.ID,
		Name: "Select " + entity.Name, Role: "button", Fill: fill, Radius: 10, Padding: 8, Height: 54,
		Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{{
			Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
				{Kind: v1.KindText, Text: entity.Name, Bold: true},
				{Kind: v1.KindText, Text: entity.Summary, Tone: v1.ToneSubtle},
			},
		}},
	}
}

func entityDetail(entity panelEntity, state SessionSnapshot) *v1.Node {
	if state.PendingRemoval != nil && state.PendingRemoval.Scope == entity.Scope && state.PendingRemoval.ID == entity.ID {
		return removalConfirmation(entity)
	}
	if entity.Scope == ScopeContainers {
		return containerDetail(*entity.container, state)
	}
	actions := &v1.Node{Kind: v1.KindRow, Gap: 4}
	switch entity.Scope {
	case ScopeImages:
		actions.Children = append(actions.Children,
			actionButton("run:"+entity.ID, "Run", "Run "+entity.Name, actionInFlight(state, ScopeImages, "run", entity.ID)),
			actionButton("rmi:"+entity.ID, "Remove", "Remove image "+entity.Name,
				entity.Referenced > 0 || actionInFlight(state, ScopeImages, "rmi", entity.ID)),
		)
	case ScopeVolumes:
		actions.Children = append(actions.Children,
			actionButton("volrm:"+entity.ID, "Remove", "Remove volume "+entity.Name,
				actionInFlight(state, ScopeVolumes, "volrm", entity.ID)),
		)
	case ScopeNetworks:
		actions.Children = append(actions.Children,
			actionButton("netrm:"+entity.ID, "Remove", "Remove network "+entity.Name,
				entity.Builtin || actionInFlight(state, ScopeNetworks, "netrm", entity.ID)),
		)
	}
	return &v1.Node{Kind: v1.KindColumn, ID: "detail", Fill: "card", Radius: 10,
		Padding: 8, Gap: 4, Height: 112, Children: []*v1.Node{
			{Kind: v1.KindText, Text: entity.Name, Bold: true},
			{Kind: v1.KindText, Text: entity.Summary, Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: entity.ID, Tone: v1.ToneSubtle},
			actions,
		}}
}

func removalConfirmation(entity panelEntity) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, ID: "detail", Fill: "card", Radius: 10,
		Padding: 8, Gap: 4, Height: 112, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Remove " + entity.Name + "?", Bold: true},
			{Kind: v1.KindText, Text: entity.ID, Tone: v1.ToneSubtle},
			{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
				actionButton("confirm", "Confirm remove", "Confirm remove "+entity.Name, false),
				actionButton("cancel", "Cancel", "Cancel removal of "+entity.Name, false),
			}},
		}}
}

func actionInFlight(state SessionSnapshot, scope Scope, verb, id string) bool {
	if state.InFlight == nil && scope == ScopeContainers && state.ActingID == id {
		return true
	}
	_, exists := state.InFlight[actionKey{scope: scope, verb: verb, id: id}]
	return exists
}

func hasInFlightScope(state SessionSnapshot, scope Scope) bool {
	for key := range state.InFlight {
		if key.scope == scope {
			return true
		}
	}
	return false
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

func containerDetail(c Container, state SessionSnapshot) *v1.Node {
	info := c.Image + " · " + c.Status
	actions := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if c.Running() {
		actions.Children = append(actions.Children,
			actionButton("stop:"+c.ID, "Stop", "Stop "+c.Names, actionInFlight(state, ScopeContainers, "stop", c.ID)),
			actionButton("restart:"+c.ID, "Restart", "Restart "+c.Names, actionInFlight(state, ScopeContainers, "restart", c.ID)),
		)
	} else {
		actions.Children = append(actions.Children,
			actionButton("start:"+c.ID, "Start", "Start "+c.Names,
				actionInFlight(state, ScopeContainers, "start", c.ID)),
		)
	}
	actions.Children = append(actions.Children,
		actionButton("remove:"+c.ID, "Remove", "Remove "+c.Names,
			c.Running() || actionInFlight(state, ScopeContainers, "remove", c.ID)))
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
	{"remove:", "remove"},
	{"rmi:", "rmi"},
	{"volrm:", "volrm"},
	{"netrm:", "netrm"},
}

// ParseAction splits an action node ID ("start:<id>") into its verb and
// entity ID, rejecting anything that should never reach an argv.
func ParseAction(node string) (action, id string, ok bool) {
	for _, p := range actionPrefixes {
		if strings.HasPrefix(node, p.prefix) {
			id = node[len(p.prefix):]
			if validEntityID(id) {
				return p.action, id, true
			}
			return "", "", false
		}
	}
	return "", "", false
}
