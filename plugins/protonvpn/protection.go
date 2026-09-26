package protonvpn

import (
	"fmt"
	"path/filepath"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// ProtectionState is everything the Protection tab renders from.
type ProtectionState struct {
	Snap        Snapshot
	SplitTunnel bool     // settings.json features.split_tunneling.enabled
	Apps        []string // excluded app paths
	Candidates  []App    // scan results for the picker
	AppQuery    string
	AppReseed   uint64
	Port        int
	HasCopyTool bool
	Err         string
}

// App is one launcher the app scan found: Value is the executable path the
// settings file stores, Label is the display name.
type App struct{ Value, Label string }

// The tab lives inside the panel's 12px padding in Task 15, so every fixed
// control also fits the 420 a card offers there; the lint tests call the tree
// standalone at the full 460.
const (
	toggleWidth  = 64
	appQueryW    = 388
	suggestLimit = 5
)

// ProtectionTree is the Protection tab: kill switch, NetShield, port
// forwarding and split tunneling cards, plus the foot error line.
func ProtectionTree(s ProtectionState) *v1.Node {
	root := &v1.Node{Kind: v1.KindList, Height: 404, Gap: 4}
	root.Children = append(root.Children,
		killSwitchCard(s),
		netShieldCard(s),
		portForwardingCard(s),
		splitTunnelCard(s),
	)
	if s.Err != "" {
		root.Children = append(root.Children, &v1.Node{
			Kind: v1.KindText, Text: s.Err, Tone: v1.ToneError,
		})
	}
	return root
}

// toggleButton is the On/Off control every card's header pins to its end.
func toggleButton(id, name, text string, on, disabled bool) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Name: name, Role: "button",
		Text: text, Fill: fillFor(on), Width: toggleWidth, Height: 36, Disabled: disabled,
		Events: []v1.EventKind{v1.EventActivate},
	}
}

func fillFor(on bool) string {
	if on {
		return "accent"
	}
	return "soft"
}

func onText(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

// settingLead is the bold label over the subtle description a card header
// carries.
func settingLead(label, desc string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: label, Bold: true},
		{Kind: v1.KindText, Text: desc, Tone: v1.ToneSubtle},
	}}
}

// killSwitchCard is the one setting the CLI refuses to change mid-tunnel, so
// its toggle locks while connected.
func killSwitchCard(s ProtectionState) *v1.Node {
	on := s.Snap.Config.KillSwitch != "off"
	return &v1.Node{
		Kind: v1.KindRow, Fill: "card", Radius: 10, Padding: 8, Height: 52, Gap: 8, PinEnd: true,
		Children: []*v1.Node{
			settingLead("Kill Switch", "Block traffic if the tunnel drops"),
			toggleButton("ks", "Kill switch", onText(on), on, s.Snap.Phase == PhaseConnected),
		},
	}
}

// netShieldCard is the three-way filter level, the active one accented.
func netShieldCard(s ProtectionState) *v1.Node {
	card := &v1.Node{
		Kind: v1.KindColumn, Fill: "card", Radius: 10, Padding: 8, Gap: 4,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: "NetShield", Bold: true},
			{Kind: v1.KindText, Text: "Block malware, ads and trackers", Tone: v1.ToneSubtle},
		},
	}
	row := &v1.Node{Kind: v1.KindRow, Gap: 4}
	for _, ns := range []struct {
		value, label string
		width        int
	}{
		{"off", "Off", 64},
		{"malware-only", "Malware", 96},
		{"malware-ads-trackers", "Malware+Ads", 128},
	} {
		row.Children = append(row.Children, &v1.Node{
			Kind: v1.KindButton, ID: "ns:" + ns.value, Name: "NetShield " + ns.label, Role: "button",
			Text: ns.label, Fill: fillFor(s.Snap.Config.NetShield == ns.value), Width: ns.width, Height: 32,
			Events: []v1.EventKind{v1.EventActivate},
		})
	}
	card.Children = append(card.Children, row)
	return card
}

// portForwardingCard shows the negotiated port once the tunnel is up; the
// copy control only exists when the session actually owns a clipboard tool.
func portForwardingCard(s ProtectionState) *v1.Node {
	on := s.Snap.Config.PortForwarding
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 10, Padding: 8, Gap: 4}
	card.Children = append(card.Children, &v1.Node{
		Kind: v1.KindRow, Gap: 8, PinEnd: true,
		Children: []*v1.Node{
			settingLead("Port forwarding", "Forward a port for peer-to-peer traffic"),
			toggleButton("pf", "Port forwarding", onText(on), on, false),
		},
	})
	if !on {
		return card
	}
	switch {
	case s.Snap.Phase == PhaseConnected && s.Port > 0:
		row := &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: fmt.Sprintf("Active port: %d", s.Port), Tabular: true},
		}}
		if s.HasCopyTool {
			row.Children = append(row.Children, &v1.Node{
				Kind: v1.KindButton, ID: "copy-port", Name: "Copy port", Role: "button",
				Icon: "content_copy", Width: 28, Height: 28,
				Events: []v1.EventKind{v1.EventActivate},
			})
		}
		card.Children = append(card.Children, row)
	case s.Port == 0:
		card.Children = append(card.Children, &v1.Node{
			Kind: v1.KindText, Text: "Negotiating port…", Tone: v1.ToneSubtle,
		})
	}
	return card
}

// splitTunnelCard gates its editor on the kill switch: the CLI ignores split
// tunneling while the kill switch is up, so the toggle locks and says why.
func splitTunnelCard(s ProtectionState) *v1.Node {
	blocked := s.Snap.Config.KillSwitch != "off"
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 10, Padding: 8, Gap: 4}
	card.Children = append(card.Children, &v1.Node{
		Kind: v1.KindRow, Gap: 8, PinEnd: true,
		Children: []*v1.Node{
			settingLead("Split tunneling", "Exclude apps from the VPN tunnel"),
			toggleButton("st", "Split tunneling", onText(s.SplitTunnel), s.SplitTunnel, blocked),
		},
	})
	if blocked {
		card.Children = append(card.Children, &v1.Node{
			Kind: v1.KindText, Text: "Disable kill switch to use split tunneling", Tone: v1.ToneError,
		})
	}
	if s.SplitTunnel {
		card.Children = append(card.Children, appRows(s)...)
		card.Children = append(card.Children, appAddRow(s))
		card.Children = append(card.Children, appSuggestions(s)...)
	}
	return card
}

// appRows lists the excluded apps, each with a delete control pinned to the
// row's end.
func appRows(s ProtectionState) []*v1.Node {
	var rows []*v1.Node
	for _, path := range s.Apps {
		name := appName(path, s.Candidates)
		rows = append(rows, &v1.Node{
			Kind: v1.KindRow, Gap: 8, PinEnd: true,
			Children: []*v1.Node{
				{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
					{Kind: v1.KindText, Text: name},
					{Kind: v1.KindText, Text: path, Tone: v1.ToneSubtle},
				}},
				{Kind: v1.KindButton, ID: "del-app:" + path, Name: "Remove " + name, Role: "button",
					Icon: "delete", Width: 28, Height: 28,
					Events: []v1.EventKind{v1.EventActivate}},
			},
		})
	}
	return rows
}

// appName prefers the scan's display name for a path, falling back to the
// executable's base name.
func appName(path string, candidates []App) string {
	for _, c := range candidates {
		if c.Value == path && c.Label != "" {
			return c.Label
		}
	}
	return filepath.Base(path)
}

// appAddRow is the filter field the host owns under the "app-query" key;
// AppReseed is bumped only to replace it.
func appAddRow(s ProtectionState) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindTextInput, ID: "app-query", Key: "app-query", Name: "Add an app", Role: "textbox",
			Text: s.AppQuery, Placeholder: "Add an app", Reseed: s.AppReseed,
			Width: appQueryW, Height: 40,
			Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
	}}
}

// appSuggestions is the picker: scan results matching the query on label or
// path, minus what is already excluded, capped at five.
func appSuggestions(s ProtectionState) []*v1.Node {
	q := strings.ToLower(s.AppQuery)
	added := make(map[string]bool, len(s.Apps))
	for _, a := range s.Apps {
		added[a] = true
	}
	var rows []*v1.Node
	for _, c := range s.Candidates {
		if added[c.Value] {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(c.Label), q) &&
			!strings.Contains(strings.ToLower(c.Value), q) {
			continue
		}
		rows = append(rows, &v1.Node{
			Kind: v1.KindRow, Gap: 8,
			Children: []*v1.Node{
				{Kind: v1.KindButton, ID: "app-suggest:" + c.Value, Name: "Add " + c.Label, Role: "button",
					Text: c.Label, Fill: "soft", Height: 32,
					Events: []v1.EventKind{v1.EventActivate}},
				{Kind: v1.KindText, Text: c.Value, Tone: v1.ToneSubtle},
			},
		})
		if len(rows) == suggestLimit {
			break
		}
	}
	return rows
}
