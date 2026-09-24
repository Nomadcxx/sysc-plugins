package minidocker

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

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
			Text: compactDisplayText(state.ContainerTab.ListError, 35), Tone: v1.ToneError})
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
				Icon: "refresh", Events: []v1.EventKind{v1.EventActivate}},
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
	start, end := pageBounds(len(entities), state.Page)
	visibleEntities := entities[start:end]
	selected := selectedEntity(state, visibleEntities)
	selectedID := state.SelectedID
	if state.PendingRemoval != nil && state.PendingRemoval.Scope == state.Scope {
		selectedID = state.PendingRemoval.ID
	}
	showLoading := statusState.Loading && !statusState.Available
	listHeight := 400 - 20*len(status)
	if selected != nil {
		listHeight -= entityDetailHeight(*selected)
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
	for _, entity := range visibleEntities {
		if entity.Scope == ScopeContainers {
			list.Children = append(list.Children, containerRow(*entity.container, selectedID))
		} else {
			list.Children = append(list.Children, entityRow(entity, selectedID))
		}
	}
	col.Children = append(col.Children, list)
	if len(entities) > maxPanelRows {
		col.Children = append(col.Children, paginationTree(start, end, len(entities), state.Page))
	}
	if selected != nil {
		col.Children = append(col.Children, entityDetail(*selected, state))
	}
	return col
}

func paginationTree(start, end, total, page int) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, ID: "pagination", Gap: 8, Children: []*v1.Node{
		actionButton("page:previous", "Previous", "Previous page", page == 0),
		{Kind: v1.KindText, Text: fmt.Sprintf("Items %d–%d of %d", start+1, end, total), Tone: v1.ToneSubtle},
		actionButton("page:next", "Next", "Next page", end == total),
	}}
}

func runFormTree(draft RunDraft, state SessionSnapshot) *v1.Node {
	form := &v1.Node{Kind: v1.KindColumn, ID: "run-form", Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindText, Text: compactImageReference(draft.ImageRef, maxPanelTextBytes), Bold: true},
		{Kind: v1.KindText, Text: "Container name (optional)", Tone: v1.ToneSubtle},
		textInput("name", "Container name", draft.Name, false, true, draft.Reseed),
		{Kind: v1.KindText, Text: "Port (host and container, optional)", Tone: v1.ToneSubtle},
		textInput("port", "Port, 1–65535", draft.Port, false, true, draft.Reseed),
		actionButton("form:publish", publishLabel(draft.Publish), publishLabel(draft.Publish), false),
		{Kind: v1.KindText, Text: "Network", Tone: v1.ToneSubtle},
		{Kind: v1.KindButton, ID: "form:network", Text: networkLabel(draft.Network), Icon: "lan",
			Name: "Cycle network", Role: "button", Height: 32, Events: []v1.EventKind{v1.EventActivate}},
		{Kind: v1.KindText, Text: "Environment (one KEY=value per line)", Tone: v1.ToneSubtle},
		textInput("env", "Environment variables", draft.Environment, true, false, draft.Reseed),
	}}
	if draft.Error != "" {
		form.Children = append(form.Children, &v1.Node{Kind: v1.KindText, Text: compactDisplayText(draft.Error, maxPanelTextBytes), Tone: v1.ToneError})
	}
	busy := entityActionInFlight(state, ScopeImages, draft.ImageID)
	run := actionButton("run-submit", "Run", "Run "+draft.ImageRef, draft.Error != "" || busy || !state.ContainerTab.Available)
	run.Fill = "accent"
	form.Children = append(form.Children, &v1.Node{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
		actionButton("form:cancel", "Cancel", "Cancel image run", false),
		run,
	}})
	return form
}

func textInput(id, name, value string, multiline, submitOnEnter bool, reseed uint64) *v1.Node {
	height := 40
	if multiline {
		height = 80
	}
	return &v1.Node{Kind: v1.KindTextInput, ID: id, Text: value, Name: name, Role: "textbox",
		Height: height, Multiline: multiline, SubmitOnEnter: submitOnEnter, Reseed: reseed,
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
	return compactDisplayText("Network: "+network, maxPanelButtonTextBytes)
}

func scopeButtons(selected Scope) *v1.Node {
	buttons := &v1.Node{Kind: v1.KindRow, Gap: 4}
	icons := map[Scope]string{
		ScopeContainers: "widgets", ScopeImages: "wallpaper",
		ScopeVolumes: "folder_open", ScopeNetworks: "lan",
	}
	for _, scope := range []Scope{ScopeContainers, ScopeImages, ScopeVolumes, ScopeNetworks} {
		label := strings.ToUpper(string(scope[:1])) + string(scope[1:])
		fill := "outline"
		if scope == selected {
			fill = "accent"
		}
		buttons.Children = append(buttons.Children, &v1.Node{Kind: v1.KindButton,
			ID: "tab:" + string(scope), Text: label, Name: label + " tab", Role: "button",
			Icon: icons[scope], Fill: fill, Height: 28, Events: []v1.EventKind{v1.EventActivate}})
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
	appendLine(compactDisplayText(state.ActionError, maxPanelTextBytes), v1.ToneError)
	if state.Scope != ScopeContainers && !state.ContainerTab.Available {
		if state.ContainerTab.Loading {
			appendLine("Checking Docker status…", v1.ToneSubtle)
		} else {
			primaryError := state.ContainerTab.ListError
			if primaryError == "" {
				primaryError = "Actions disabled until containers refresh"
			}
			appendLine(compactDisplayText(primaryError, maxPanelTextBytes), v1.ToneError)
		}
	} else {
		appendLine(compactDisplayText(active.ListError, maxPanelTextBytes), v1.ToneError)
	}
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
	Scope               Scope
	ID                  string
	Name                string
	Summary             string
	Referenced          int
	ReferenceCountKnown bool
	Builtin             bool
	container           *Container
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
		images := sortedImages(state.Images)
		entities := make([]panelEntity, 0, len(images))
		for _, image := range images {
			ref := imageReference(image)
			entities = append(entities, panelEntity{
				Scope: state.Scope, ID: ref, Name: ref,
				Summary: compactDisplayText(image.ID, 22) + " · " + image.Size, Referenced: image.Containers,
				ReferenceCountKnown: image.ContainersKnown,
			})
		}
		return entities
	case ScopeVolumes:
		if !state.VolumeTab.Available {
			return nil
		}
		volumes := sortedVolumes(state.Volumes)
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
	return &v1.Node{Kind: v1.KindButton, ID: "select:" + entityNodeKey(entity.Scope, entity.ID),
		Name: boundedAccessibleName("Select " + entity.Name + "; " + entity.Summary), Role: "button", Fill: fill, Radius: 10, Padding: 8, Height: 54,
		Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{{
			Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
				{Kind: v1.KindText, Text: compactEntityName(entity), Bold: true},
				{Kind: v1.KindText, Text: compactDisplayText(entity.Summary, maxPanelTextBytes), Tone: v1.ToneSubtle},
			},
		}},
	}
}

func entityDetail(entity panelEntity, state SessionSnapshot) *v1.Node {
	if state.PendingRemoval != nil && state.PendingRemoval.Scope == entity.Scope && state.PendingRemoval.ID == entity.ID {
		return removalConfirmation(entity, state)
	}
	if entity.Scope == ScopeContainers {
		return containerDetail(*entity.container, state)
	}
	actions := &v1.Node{Kind: v1.KindRow, Gap: 4}
	switch entity.Scope {
	case ScopeImages:
		removeDisabled := !imageReferenceCountAllowsRemoval(entity) || !state.ContainerTab.Available || entityActionInFlight(state, ScopeImages, entity.ID)
		removeName := "Remove image " + entity.Name
		if !imageReferenceCountAllowsRemoval(entity) {
			removeName = "Cannot remove image " + entity.Name + ": container use is unknown or nonzero"
		}
		actions.Children = append(actions.Children,
			actionButton("run:"+entityNodeKey(entity.Scope, entity.ID), "Run", "Run "+entity.Name,
				entityActionInFlight(state, ScopeImages, entity.ID) || !state.ContainerTab.Available),
			actionButton("rmi:"+entityNodeKey(entity.Scope, entity.ID), "Remove", removeName, removeDisabled),
		)
	case ScopeVolumes:
		actions.Children = append(actions.Children,
			actionButton("volrm:"+entityNodeKey(entity.Scope, entity.ID), "Remove", "Remove volume "+entity.Name,
				entityActionInFlight(state, ScopeVolumes, entity.ID) || !state.ContainerTab.Available),
		)
	case ScopeNetworks:
		removeName := "Remove network " + entity.Name
		if entity.Builtin {
			removeName = "Cannot remove built-in network " + entity.Name
		}
		actions.Children = append(actions.Children,
			actionButton("netrm:"+entity.ID, "Remove", removeName,
				entity.Builtin || entityActionInFlight(state, ScopeNetworks, entity.ID) || !state.ContainerTab.Available),
		)
	}
	children := []*v1.Node{
		{Kind: v1.KindText, Text: compactEntityName(entity), Bold: true},
		{Kind: v1.KindText, Text: compactDisplayText(entity.Summary, maxPanelTextBytes), Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: compactDisplayText(entity.ID, maxPanelTextBytes), Tone: v1.ToneSubtle},
	}
	if entity.Scope == ScopeImages && !imageReferenceCountAllowsRemoval(entity) {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: imageReferenceCountNote(entity), Tone: v1.ToneSubtle})
	}
	if entity.Scope == ScopeNetworks && entity.Builtin {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "Built-in network · removal disabled", Tone: v1.ToneSubtle})
	}
	children = append(children, actions)
	return &v1.Node{Kind: v1.KindColumn, ID: "detail", Fill: "card", Radius: 10,
		Padding: 8, Gap: 4, Height: entityDetailHeight(entity), Children: children}
}

func removalConfirmation(entity panelEntity, state SessionSnapshot) *v1.Node {
	busy := entityActionInFlight(state, entity.Scope, entity.ID) || !state.ContainerTab.Available
	return &v1.Node{Kind: v1.KindColumn, ID: "detail", Fill: "card", Radius: 10,
		Padding: 8, Gap: 4, Height: 112, Children: []*v1.Node{
			{Kind: v1.KindText, Text: compactMiddleText("Remove "+entity.Name+"?", maxPanelTextBytes), Bold: true},
			{Kind: v1.KindText, Text: compactDisplayText(entity.ID, maxPanelTextBytes), Tone: v1.ToneSubtle},
			{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
				confirmRemovalButton(entity.Name, busy),
				actionButton("cancel", "Cancel", "Cancel removal of "+entity.Name, false),
			}},
		}}
}

func entityActionInFlight(state SessionSnapshot, scope Scope, id string) bool {
	if state.InFlight == nil && scope == ScopeContainers && state.ActingID == id {
		return true
	}
	for key := range state.InFlight {
		if key.scope == scope && key.id == id {
			return true
		}
	}
	return false
}

func hasInFlightScope(state SessionSnapshot, scope Scope) bool {
	for key := range state.InFlight {
		if key.scope == scope {
			return true
		}
	}
	return false
}

// maxPanelTextBytes fits the host's 8-pixel byte metric inside the detail
// card's 432-pixel content width (480 panel − 32 root padding − 16 card).
const maxPanelTextBytes = 54

// maxPanelButtonTextBytes reserves width for the network icon and button inset.
const maxPanelButtonTextBytes = 44

func containerRow(c Container, selectedID string) *v1.Node {
	tone := v1.ToneNormal
	if c.Running() {
		tone = v1.ToneAccent
	}
	fill := "outline"
	if c.ID == selectedID {
		fill = "card"
	}
	return &v1.Node{Kind: v1.KindButton, ID: "select:" + c.ID,
		Name: boundedAccessibleName("Select " + c.Names + "; image " + c.Image + "; status " + c.Status),
		Role: "button", Fill: fill, Radius: 10, Padding: 8, Height: 54,
		Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{{
			Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
				{Kind: v1.KindText, Text: compactDisplayText(c.Names, maxPanelTextBytes), Tone: tone, Bold: true},
				{Kind: v1.KindText, Text: compactContainerInfo(c.Image, c.Status), Tone: v1.ToneSubtle},
			},
		}},
	}
}

func containerDetail(c Container, state SessionSnapshot) *v1.Node {
	actions := &v1.Node{Kind: v1.KindRow, Gap: 4}
	if c.Running() {
		actions.Children = append(actions.Children,
			actionButton("stop:"+c.ID, "Stop", "Stop "+c.Names, entityActionInFlight(state, ScopeContainers, c.ID)),
			actionButton("restart:"+c.ID, "Restart", "Restart "+c.Names, entityActionInFlight(state, ScopeContainers, c.ID)),
		)
	} else {
		actions.Children = append(actions.Children,
			actionButton("start:"+c.ID, "Start", "Start "+c.Names,
				entityActionInFlight(state, ScopeContainers, c.ID)),
		)
	}
	actions.Children = append(actions.Children,
		actionButton("remove:"+c.ID, "Remove", "Remove "+c.Names,
			c.Running() || entityActionInFlight(state, ScopeContainers, c.ID)))
	children := []*v1.Node{
		{Kind: v1.KindText, Text: compactDisplayText(c.Names, maxPanelTextBytes), Bold: true},
		{Kind: v1.KindText, Text: compactContainerInfo(c.Image, c.Status), Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: compactDisplayText(c.ID, maxPanelTextBytes), Tone: v1.ToneSubtle},
	}
	if c.Running() {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "Stop this container before removing it", Tone: v1.ToneSubtle})
	}
	children = append(children, actions)
	return &v1.Node{Kind: v1.KindColumn, ID: "detail", Fill: "card", Radius: 10,
		Padding: 8, Gap: 4, Height: entityDetailHeight(panelEntity{Scope: ScopeContainers, container: &c}), Children: children}
}

func actionButton(id, label, name string, disabled bool) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: boundedAccessibleName(name), Role: "button",
		Icon: actionIcon(id), Height: 28, Disabled: disabled, Events: []v1.EventKind{v1.EventActivate}}
}

func confirmRemovalButton(name string, disabled bool) *v1.Node {
	button := actionButton("confirm", "Confirm remove", "Confirm remove "+name, disabled)
	button.Fill = "error-container"
	return button
}

func boundedAccessibleName(name string) string {
	return compactMiddleText(strings.Join(strings.Fields(name), " "), v1.MaxIdentBytes)
}

func compactContainerInfo(image, status string) string {
	suffix := " · " + compactDisplayText(status, 24)
	return compactDisplayText(image, maxPanelTextBytes-len(suffix)) + suffix
}

func compactEntityName(entity panelEntity) string {
	if entity.Scope == ScopeImages {
		return compactImageReference(entity.Name, maxPanelTextBytes)
	}
	return compactDisplayText(entity.Name, maxPanelTextBytes)
}

func actionIcon(id string) string {
	switch {
	case id == "refresh":
		return "refresh"
	case id == "confirm":
		return "check"
	case id == "cancel", id == "form:cancel":
		return "close"
	case strings.HasPrefix(id, "start:") || strings.HasPrefix(id, "run:") || id == "run-submit":
		return "play_arrow"
	case strings.HasPrefix(id, "stop:"):
		return "pause"
	case strings.HasPrefix(id, "restart:"):
		return "restart_alt"
	case strings.HasPrefix(id, "remove:") || strings.HasPrefix(id, "rmi:") || strings.HasPrefix(id, "volrm:") || strings.HasPrefix(id, "netrm:"):
		return "delete"
	default:
		return ""
	}
}

func imageReferenceCountAllowsRemoval(entity panelEntity) bool {
	return entity.ReferenceCountKnown && entity.Referenced == 0
}

func imageReferenceCountNote(entity panelEntity) string {
	if !entity.ReferenceCountKnown || entity.Referenced < 0 {
		return "Container use unknown · removal disabled"
	}
	count := "containers"
	if entity.Referenced == 1 {
		count = "container"
	}
	return fmt.Sprintf("Used by %d %s · removal disabled", entity.Referenced, count)
}

func entityDetailHeight(entity panelEntity) int {
	switch entity.Scope {
	case ScopeImages:
		if !imageReferenceCountAllowsRemoval(entity) {
			return 128
		}
	case ScopeNetworks:
		if entity.Builtin {
			return 128
		}
	case ScopeContainers:
		if entity.container != nil && entity.container.Running() {
			return 128
		}
	}
	return 112
}

func compactDisplayText(text string, maxBytes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= maxBytes {
		return text
	}
	const ellipsis = "…"
	budget := maxBytes - len(ellipsis)
	var compact strings.Builder
	for _, r := range text {
		if compact.Len()+utf8.RuneLen(r) > budget {
			break
		}
		compact.WriteRune(r)
	}
	return compact.String() + ellipsis
}

func compactImageReference(ref string, maxBytes int) string {
	return compactMiddleText(ref, maxBytes)
}

func compactMiddleText(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	const ellipsis = "…"
	budget := maxBytes - len(ellipsis)
	prefixBytes := budget / 2
	for prefixBytes > 0 && !utf8.RuneStart(text[prefixBytes]) {
		prefixBytes--
	}
	suffixStart := len(text) - (budget - prefixBytes)
	for suffixStart < len(text) && !utf8.RuneStart(text[suffixStart]) {
		suffixStart++
	}
	return text[:prefixBytes] + ellipsis + text[suffixStart:]
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
