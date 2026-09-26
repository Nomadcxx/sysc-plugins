package worldclock

import (
	"strconv"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// PanelWidth and PanelHeight are the manifest's panel box; tests lint at it.
const (
	PanelWidth  = 480
	PanelHeight = 440
)

// The panel's frame. The host adds no inner padding, so the root column
// provides it. The header and the list share one inset so their edges align,
// and the list's inset is also the gutter its 4 px scrollbar is drawn in.
const (
	panelPad     = 12
	panelGap     = 0 // the header's and list's insets already separate them
	contentInset = 10
	headerGap    = 8
	titleLine    = 24 // the title token renders taller than body text
	textLine     = 18
	searchHeight = 40
	searchPad    = 10 // the painter insets field text by Padding; clears the pill's cap
)

const (
	searchGap      = 8
	searchAddWidth = 40
)

const (
	zoneDragType = "world-clock-zone"
	clockWidth   = 96
	skyWidth     = 24
	cardGap      = 6
	iconButton   = 28
	actionGap    = 2
	actionsWidth = 3*iconButton + 2*actionGap
)

type Suggestion struct {
	ID    string
	Title string
}

// PanelState is everything the panel draws; the loop owns it.
type PanelState struct {
	Readings      []Reading
	Query         string
	QueryReseed   uint64
	Suggestions   []Suggestion
	PendingDelete string
	Renaming      string
	RenameDraft   string
	RenameReseed  uint64
	Errors        []string
	NoMatches     string
	HideNoMatches bool
	Notice        string
}

var (
	activate = []v1.EventKind{v1.EventActivate}
	editing  = []v1.EventKind{v1.EventChange, v1.EventSubmit}
)

func Panel(s PanelState) *v1.Node {
	header := []*v1.Node{
		{Kind: v1.KindText, Text: "World Clock", Size: "title", Bold: true},
		searchRow(s),
	}
	header = append(header, messageLines(s)...)
	height := PanelHeight - 2*panelPad - panelHeaderHeight(s) - panelGap
	var list *v1.Node
	if s.Query != "" {
		list = suggestionList(s.Suggestions, s.NoMatches, s.HideNoMatches, height)
	} else {
		list = zoneList(s, height)
	}
	return &v1.Node{Kind: v1.KindColumn, Padding: panelPad, Gap: panelGap, Children: []*v1.Node{
		{Kind: v1.KindColumn, Padding: contentInset, Gap: headerGap, Children: header},
		list,
	}}
}

func messageLines(s PanelState) []*v1.Node {
	var out []*v1.Node
	for _, err := range s.Errors {
		if err != "" {
			out = append(out, &v1.Node{Kind: v1.KindText, Text: err, Tone: v1.ToneError})
		}
	}
	if s.Notice != "" {
		out = append(out, &v1.Node{Kind: v1.KindText, Text: s.Notice, Tone: v1.ToneSubtle})
	}
	return out
}

// panelHeaderHeight is the vertical budget of the header column; the list
// takes what remains, so error lines shorten the list instead of pushing it
// past the panel's bottom edge.
func panelHeaderHeight(s PanelState) int {
	return 2*contentInset + titleLine + headerGap + searchHeight + len(messageLines(s))*(headerGap+textLine)
}

func searchRow(s PanelState) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: searchGap, Children: []*v1.Node{
		{Kind: v1.KindTextInput, ID: "search", Text: s.Query, Name: "Search time zones", Role: "textbox",
			Placeholder: "Search a city or country", Width: PanelWidth - 2*panelPad - 2*contentInset - searchGap - searchAddWidth,
			Height: searchHeight, Padding: searchPad, SubmitOnEnter: true, Reseed: s.QueryReseed, Events: editing},
		{Kind: v1.KindButton, ID: "add", Icon: "add", Name: "Add the top match", Role: "button", Fill: "accent",
			Width: searchAddWidth, Height: searchHeight, Disabled: strings.TrimSpace(s.Query) == "", Events: activate},
	}}
}

func suggestionList(suggestions []Suggestion, noMatches string, hideNoMatches bool, height int) *v1.Node {
	rows := make([]*v1.Node, 0, len(suggestions)+1)
	if len(suggestions) == 0 && !hideNoMatches {
		if noMatches == "" {
			noMatches = "No matching zone"
		}
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: noMatches, Tone: v1.ToneSubtle})
	}
	for _, m := range suggestions {
		rows = append(rows, &v1.Node{Kind: v1.KindButton, ID: "pick:" + m.ID, Text: m.Title, Name: "Add " + m.Title,
			Role: "button", Fill: "soft", Height: 36, Events: activate})
	}
	return &v1.Node{Kind: v1.KindList, Height: height, Padding: contentInset, Gap: 4, Children: rows}
}

func zoneList(s PanelState, height int) *v1.Node {
	rows := make([]*v1.Node, 0, 2*len(s.Readings)+1)
	if len(s.Readings) == 0 {
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: "No zones yet. Search for a city above.", Tone: v1.ToneSubtle})
	}
	for i, r := range s.Readings {
		rows = append(rows, dropGap(i), zoneCard(r, s))
	}
	if len(s.Readings) > 0 {
		rows = append(rows, dropGap(len(s.Readings)))
	}
	return &v1.Node{Kind: v1.KindList, Height: height, Padding: contentInset, Children: rows}
}

func dropGap(i int) *v1.Node {
	return &v1.Node{Kind: v1.KindDropZone, ID: "drop:" + strconv.Itoa(i), Accept: []string{zoneDragType},
		Height: 6, Events: []v1.EventKind{v1.EventDrop}}
}

func zoneCard(r Reading, s PanelState) *v1.Node {
	var name *v1.Node
	if s.Renaming == r.Zone {
		name = &v1.Node{Kind: v1.KindTextInput, ID: "label:" + r.Zone, Text: s.RenameDraft, Name: "Label for " + r.Zone,
			Role: "textbox", Placeholder: ShortLabel(r.Zone), Height: 36, Padding: 8, SubmitOnEnter: true, Reseed: s.RenameReseed, Events: editing}
	} else {
		tone := v1.ToneNormal
		if !r.OnBar {
			tone = v1.ToneSubtle
		}
		name = &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
			{Kind: v1.KindText, Text: r.Label, Bold: true, Tone: tone},
			metaText(r),
		}}
	}
	// Two children so PinEnd reserves the fixed trailing group; the name side
	// is the one that clips when a label or zone id runs long.
	return &v1.Node{Kind: v1.KindRow, Key: "row:" + r.Zone, Gap: cardGap, Fill: "card", Radius: 10, Padding: 8, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: cardGap, Children: []*v1.Node{
			{Kind: v1.KindDragSource, ID: "drag:" + r.Zone, Text: "≡", Name: "Reorder " + r.Label, Role: "button",
				DragType: zoneDragType, Payload: r.Zone, Width: 24, Events: []v1.EventKind{v1.EventPointer}},
			name,
		}},
		{Kind: v1.KindRow, Gap: cardGap, Width: clockWidth + skyWidth + actionsWidth + 2*cardGap, Children: []*v1.Node{
			clockColumn(r),
			skyIcon(r),
			actions(r, s),
		}},
	}}
}

// metaText is the card's second line. It never repeats what the label says:
// a zone with no region ("UTC") shows its offset alone, and a card still
// showing the default city name drops that city from the zone id. A custom
// label keeps the full id, the only place the actual zone is named.
func metaText(r Reading) *v1.Node {
	text := r.Zone + " · " + r.Offset
	switch i := strings.LastIndex(r.Zone, "/"); {
	case i < 0:
		text = r.Offset
	case r.Label == ShortLabel(r.Zone):
		text = r.Zone[:i] + " · " + r.Offset
	}
	return &v1.Node{Kind: v1.KindText, Key: "meta:" + r.Zone, Text: text, Tone: v1.ToneSubtle}
}

func clockColumn(r Reading) *v1.Node {
	tone := v1.ToneAccent
	if !r.OnBar {
		tone = v1.ToneSubtle
	}
	top := []*v1.Node{{Kind: v1.KindText, Text: r.Clock, Bold: true, Tabular: true, Tone: tone}}
	if m := DayMarker(r.DayShift); m != "" {
		top = append(top, &v1.Node{Kind: v1.KindText, Text: m, Tone: v1.ToneAccent})
	}
	return &v1.Node{Kind: v1.KindColumn, Key: "clock:" + r.Zone, Gap: 2, Width: clockWidth, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 4, Children: top},
		{Kind: v1.KindText, Text: r.Relative, Tone: v1.ToneSubtle},
	}}
}

func skyIcon(r Reading) *v1.Node {
	icon := "bedtime"
	if r.Daytime {
		icon = "sunny"
	}
	return &v1.Node{Kind: v1.KindIcon, Key: "sky:" + r.Zone, Icon: icon, Width: skyWidth}
}

func iconAction(id, icon, name, fill string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Icon: icon, Name: name, Role: "button", Fill: fill,
		Width: iconButton, Height: iconButton, Events: activate}
}

func actions(r Reading, s PanelState) *v1.Node {
	var buttons []*v1.Node
	switch {
	case s.Renaming == r.Zone:
		buttons = []*v1.Node{
			iconAction("rename-ok", "check", "Save label for "+r.Zone, "accent"),
			iconAction("rename-cancel", "close", "Cancel rename", ""),
		}
	case s.PendingDelete == r.Zone:
		buttons = []*v1.Node{
			iconAction("del-ok", "check", "Remove "+r.Label, "error"),
			iconAction("del-cancel", "close", "Keep "+r.Label, ""),
		}
	default:
		eye, verb := "visibility", "Hide "
		if !r.OnBar {
			eye, verb = "visibility_off", "Show "
		}
		buttons = []*v1.Node{
			iconAction("bar:"+r.Zone, eye, verb+r.Label+" on the bar", ""),
			iconAction("edit:"+r.Zone, "edit", "Rename "+r.Label, ""),
			iconAction("del:"+r.Zone, "delete", "Remove "+r.Label, ""),
		}
	}
	// An explicit width: a row of controls squeezed below its content is
	// refused, and a card's flexible column must give way instead.
	return &v1.Node{Kind: v1.KindRow, Gap: actionGap, Width: actionsWidth, Children: buttons}
}

// PanelPatch is the minute update for a panel showing zone cards. The meta
// line of the zone being renamed is absent from the tree, so it is skipped.
func PanelPatch(readings []Reading, renaming string) []v1.Replacement {
	out := make([]v1.Replacement, 0, 3*len(readings))
	for _, r := range readings {
		out = append(out,
			v1.Replacement{Key: "clock:" + r.Zone, Node: clockColumn(r)},
			v1.Replacement{Key: "sky:" + r.Zone, Node: skyIcon(r)})
		if r.Zone != renaming {
			out = append(out, v1.Replacement{Key: "meta:" + r.Zone, Node: metaText(r)})
		}
	}
	return out
}

func SuggestionTitle(m Match, relative string) string {
	parts := []string{m.City}
	if m.Country != "" {
		parts = append(parts, m.Country)
	}
	return strings.Join(append(parts, relative), " · ")
}
