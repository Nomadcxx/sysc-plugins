package protonvpn

import (
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// PanelState is everything the panel renders from.
type PanelState struct {
	Snap    Snapshot
	Tab     string // connections | protection | account
	HasCLI  bool
	Traffic bool // traffic_monitoring setting
}

// tabWidth is one of three equal shares of the 460 panel minus its padding
// and the two nav gaps.
const tabWidth = (460 - 2*12 - 2*8) / 3

// actionButton is the card's one control, morphing with phase: Connect,
// Cancel mid-flight, Disconnect once up — disabled while disconnecting.
func actionButton(p Phase) *v1.Node {
	text, fill := "Connect", "accent"
	switch p {
	case PhaseConnecting:
		text, fill = "Cancel", "soft"
	case PhaseConnected, PhaseDisconnecting:
		text, fill = "Disconnect", "error"
	}
	return &v1.Node{
		Kind:     v1.KindButton,
		ID:       "action",
		Name:     text,
		Role:     "button",
		Text:     text,
		Fill:     fill,
		Width:    92,
		Height:   40,
		Disabled: p == PhaseDisconnecting,
		Events:   []v1.EventKind{v1.EventActivate},
	}
}

// ipProtocol is the endpoint line: IP and protocol, either alone when the
// other is unknown.
func ipProtocol(snap Snapshot) string {
	switch {
	case snap.IP != "" && snap.Status.Protocol != "":
		return snap.IP + " · " + snap.Status.Protocol
	case snap.IP != "":
		return snap.IP
	default:
		return snap.Status.Protocol
	}
}

// detailRows are the card lines under the server: where the tunnel lands,
// plus the live traffic line when connected and the setting allows.
func detailRows(s PanelState) []*v1.Node {
	var rows []*v1.Node
	if line := ipProtocol(s.Snap); line != "" {
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: line, Tone: v1.ToneSubtle})
	}
	if s.Snap.Phase == PhaseConnected && s.Traffic {
		rows = append(rows, &v1.Node{Kind: v1.KindRow, Key: "traffic", Gap: 4, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "download", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: formatRate(s.Snap.RxRate), Tabular: true},
			{Kind: v1.KindIcon, Icon: "upload", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: formatRate(s.Snap.TxRate), Tabular: true},
		}})
	}
	return rows
}

// connectionCard is the panel's hero: status word, server, and the one
// action. The leading column takes the width the pinned button leaves.
func connectionCard(s PanelState) *v1.Node {
	icon, tone := phaseIcon(s.Snap.Phase)
	lead := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon, Tone: tone},
			{Kind: v1.KindText, Text: statusWord(s.Snap.Phase), Bold: true},
		}},
	}}
	if s.Snap.Phase == PhaseDisconnected {
		lead.Children = append(lead.Children,
			&v1.Node{Kind: v1.KindText, Text: "Fastest country", Bold: true},
			&v1.Node{Kind: v1.KindText, Text: "Auto-selected on connect", Tone: v1.ToneSubtle},
		)
	} else {
		if st := s.Snap.Status; st.Server != "" {
			mid := &v1.Node{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
				{Kind: v1.KindText, Text: st.Server, Bold: true},
			}}
			if st.Location != "" {
				mid.Children = append(mid.Children, &v1.Node{Kind: v1.KindText, Text: st.Location, Tone: v1.ToneSubtle})
			}
			if st.Country != "" {
				mid.Children = append(mid.Children, &v1.Node{
					Kind: v1.KindRow, Fill: "chip", Shape: "stadium", Padding: 4,
					Children: []*v1.Node{{Kind: v1.KindText, Text: strings.ToUpper(st.Country), Size: "caption"}},
				})
			}
			lead.Children = append(lead.Children, mid)
		}
		lead.Children = append(lead.Children, detailRows(s)...)
	}
	return &v1.Node{
		Kind: v1.KindRow, Fill: "card", Radius: 10, Padding: 8, Height: 96, Gap: 8, PinEnd: true,
		Children: []*v1.Node{lead, actionButton(s.Snap.Phase)},
	}
}

// connectionsStub is the tab body placeholder; the tasks building the
// connections, protection and account trees replace each call site.
func connectionsStub(label string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: label, Tone: v1.ToneSubtle},
	}}
}

// Panel is the 460×580 panel: optional CLI banner, the connection card, the
// error detail line, tab navigation, and the active tab's content.
func Panel(s PanelState) *v1.Node {
	root := &v1.Node{Kind: v1.KindColumn, Padding: 12, Gap: 8}
	if !s.HasCLI {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: "protonvpn CLI not found", Tone: v1.ToneError})
	}
	root.Children = append(root.Children, connectionCard(s))
	if s.Snap.Err != "" {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Key: "err", Text: s.Snap.Err, Tone: v1.ToneError})
	}
	nav := &v1.Node{Kind: v1.KindRow, Gap: 8}
	for _, tab := range []struct{ id, label string }{
		{"connections", "Connections"},
		{"protection", "Protection"},
		{"account", "Account"},
	} {
		fill := "soft"
		if s.Tab == tab.id {
			fill = "accent"
		}
		nav.Children = append(nav.Children, &v1.Node{
			Kind: v1.KindButton, ID: "tab:" + tab.id, Name: tab.label, Role: "button",
			Text: tab.label, Fill: fill, Width: tabWidth, Height: 36,
			Events: []v1.EventKind{v1.EventActivate},
		})
	}
	root.Children = append(root.Children, nav, connectionsStub(s.Tab))
	return root
}
