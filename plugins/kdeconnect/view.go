package kdeconnect

import (
	"fmt"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarTree is the bar pill: the offline glyph with N/A while the daemon is
// unreachable, the phone glyph alone once it answers. The whole control
// opens the panel, the noctalia bar-widget shape.
func BarTree(snap Snapshot) *v1.Node {
	icon, label := "phonelink-off", "N/A"
	if snap.Available {
		icon, label = "smartphone", ""
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Icon: icon, Text: label,
		Name: "Open phone connect", Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
	}}}
}

// TooltipTree names the backend and its reach in one line.
func TooltipTree(snap Snapshot) *v1.Node {
	line := "Phone Connect"
	switch {
	case !snap.Available:
		line += " · unavailable"
	case len(snap.Devices) == 0:
		line += " · no devices"
	}
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: line},
	}}
}

// PanelTree is the phone-connect panel: the daemon header over the state
// cards. The skeleton renders the unavailable and empty states; the device
// cards, actions, and composers arrive with the view work.
func PanelTree(snap Snapshot) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 10, Children: []*v1.Node{headerTree(snap)}}
	if !snap.Available {
		col.Children = append(col.Children, stateCard(
			"KDE Connect daemon unreachable",
			"Install and start kdeconnectd, then refresh."))
		return col
	}
	if len(snap.Devices) == 0 {
		col.Children = append(col.Children, stateCard(
			"No devices",
			"Pair this desktop from the KDE Connect app on your phone."))
	}
	return col
}

// headerTree is the daemon header card: the backend name and its counts
// beside a right-pinned refresh control, the DMS popout header's shape.
func headerTree(snap Snapshot) *v1.Node {
	title := snap.BackendName
	if title == "" {
		title = "Phone Connect"
	}
	detail := "unavailable"
	if snap.Available {
		connected, paired := 0, 0
		for i := range snap.Devices {
			if snap.Devices[i].Reachable {
				connected++
			}
			if snap.Devices[i].Paired {
				paired++
			}
		}
		detail = fmt.Sprintf("%d connected • %d paired", connected, paired)
	}
	return &v1.Node{Kind: v1.KindRow, Fill: "card", Radius: 12, Padding: 12, Gap: 10,
		PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindColumn, Children: []*v1.Node{
				{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
					{Kind: v1.KindIcon, Icon: "devices"},
					{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
						{Kind: v1.KindText, Text: title, Bold: true, Size: "title"},
						{Kind: v1.KindText, Text: detail, Size: "caption", Tone: v1.ToneAccent},
					}},
				}},
			}},
			{Kind: v1.KindButton, ID: "refresh", Icon: "refresh",
				Name: "Refresh devices", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}}
}

// stateCard is one full-width message card with a bold headline over a
// muted hint.
func stateCard(headline, hint string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 4,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: headline, Bold: true},
			{Kind: v1.KindText, Text: hint, Tone: v1.ToneSubtle},
		}}
}
