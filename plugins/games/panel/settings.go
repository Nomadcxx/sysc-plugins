package panel

import v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"

// SettingsHeader is the plugin-owned header above the shell's settings form:
// the manifest declares the "settings" panel with include_settings, and the
// host renders the form from the manifest's settings array. This tree only
// supplies the way back, the title, and the one line of context.
func SettingsHeader() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: 12, Children: []*v1.Node{{
		Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: 12, Gap: 8,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: 8, Height: 44, Children: []*v1.Node{
				{Kind: v1.KindButton, ID: "back", Icon: "chevron_left", Width: 40, Height: 36, Padding: 8,
					Name: "Back to games", Role: "button", Tooltip: "Back to the game deck",
					Events: []v1.EventKind{v1.EventActivate}},
				{Kind: v1.KindIcon, Icon: "settings", IconSize: 20},
				{Kind: v1.KindColumn, Width: 260, Children: []*v1.Node{
					{Kind: v1.KindText, Text: "Game Library Settings", Bold: true, Size: "title"},
				}},
			}},
			{Kind: v1.KindText, Text: "Settings save automatically. A game listed by two libraries appears once, launching through the default launcher.",
				Tone: v1.ToneSubtle, Size: "caption", MaxWidth: 620},
		},
	}}}
}

// LibraryName renders a backend name for the deck: the pill, cards and the
// detail pane say "Steam", not "steam".
func LibraryName(source string) string {
	switch source {
	case "steam":
		return "Steam"
	case "lutris":
		return "Lutris"
	case "heroic":
		return "Heroic"
	case "faugus":
		return "Faugus"
	}
	if source == "" {
		return "library"
	}
	return capitalize(source)
}
