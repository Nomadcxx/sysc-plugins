package hermes

import v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"

// Menu glyphs, the pinned catalogue's closest names for noctalia's
// message-circle, plus, octagon, refresh, power and settings.
const (
	menuIconOpen         = "sms"
	menuIconNewSession   = "add"
	menuIconInterrupt    = "stop"
	menuIconRefresh      = "refresh"
	menuIconStartGateway = "power_settings_new"
	menuIconSettings     = "settings"

	menuPad    = 8
	menuGap    = 4
	menuRowH   = 36
	menuRowPad = 8
)

// MenuItem is one right-click menu entry. ID is the manifest service
// action it runs.
type MenuItem struct{ ID, Label, Icon string }

// MenuItems is noctalia's right-click menu: Open, New session, Interrupt,
// Refresh, then Start gateway only in local mode while offline or unknown,
// then Settings. There is no stop entry, local or remote.
func MenuItems(s Status, clientOnly bool) []MenuItem {
	items := []MenuItem{
		{"open", "Open Hermes", menuIconOpen},
		{"new-session", "New session", menuIconNewSession},
		{"interrupt", "Interrupt", menuIconInterrupt},
		{"refresh", "Refresh", menuIconRefresh},
	}
	if CanStartGateway(s, clientOnly) {
		items = append(items, MenuItem{"start-gateway", "Start gateway", menuIconStartGateway})
	}
	return append(items, MenuItem{"settings", "Hermes Settings", menuIconSettings})
}

// MenuTree renders the menu as one row per item in the width by height
// menu panel.
func MenuTree(s Status, clientOnly bool, width, height int) *v1.Node {
	inner := width - 2*menuPad
	root := &v1.Node{Kind: v1.KindColumn, Width: width, Height: height, Padding: menuPad, Gap: menuGap}
	for _, it := range MenuItems(s, clientOnly) {
		root.Children = append(root.Children, &v1.Node{
			Kind: v1.KindButton, ID: it.ID, Key: it.ID, Icon: it.Icon, Text: it.Label,
			Name: it.Label, Role: "button", Width: inner, Height: menuRowH, Padding: menuRowPad,
			Events: []v1.EventKind{v1.EventActivate},
		})
	}
	return root
}
