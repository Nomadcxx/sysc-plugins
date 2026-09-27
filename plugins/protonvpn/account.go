package protonvpn

import (
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// AccountState is everything the Account tab renders from.
type AccountState struct {
	Snap      Snapshot
	SignedIn  bool
	UserDraft string
	// Settings holds the shell-managed option values keyed by manifest key;
	// ponytail: the plan's AccountState carried no settings, so the Options
	// section could not show them — a plain map keeps the read-only rows
	// honest without a new type. Missing keys render "—".
	Settings   map[string]string
	UserReseed uint64
	Err        string
}

// settingOrder is the manifest's key order with the labels it ships; the
// Options section renders these read-only.
var settingOrder = []struct{ key, label string }{
	{"refresh_seconds", "Status refresh (seconds)"},
	{"traffic_monitoring", "Show live traffic"},
	{"notify_on_connect", "Notify on connect"},
	{"bar_mode", "Bar shows"},
	{"quick_connect", "Right-click connects to"},
}

func dashed(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

// infoRow is a label/value line: subtle label, tabular value pinned end.
func infoRow(label, value string) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Text: label, Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: value, Tabular: true},
	}}
}

func accountButton(id, text, fill string, disabled bool) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Name: text, Role: "button",
		Text: text, Fill: fill, Width: 92, Height: 40, Disabled: disabled,
		Events: []v1.EventKind{v1.EventActivate},
	}
}

// signedInCard is the identity header: person glyph over the account name.
func signedInCard(s AccountState) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Fill: "card", Radius: 10, Padding: 8, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: "person", Tone: v1.ToneAccent},
		{Kind: v1.KindText, Text: dashed(s.Snap.Info.Username), Bold: true},
	}}
}

// signInCard is the signed-out state: the terminal hand-off is the only
// route in, so the card says so above the username field.
func signInCard(s AccountState) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 10, Padding: 8, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Complete sign-in in the terminal (password + 2FA)", Tone: v1.ToneSubtle},
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindTextInput, ID: "signin-user", Key: "signin-user", Name: "Username", Role: "textbox",
				Text: s.UserDraft, Placeholder: "Username", Reseed: s.UserReseed,
				Width: 320, Height: 40,
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			accountButton("signin", "Sign in", "accent", s.UserDraft == ""),
		}},
		{Kind: v1.KindText, Text: "A terminal opens with protonvpn signin — finish it there", Tone: v1.ToneSubtle},
	}}
}

func optionsSection(s AccountState) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Options", Bold: true},
	}}
	for _, o := range settingOrder {
		col.Children = append(col.Children, infoRow(o.label, dashed(s.Settings[o.key])))
	}
	col.Children = append(col.Children, &v1.Node{
		Kind: v1.KindText, Text: "Change in shell settings", Tone: v1.ToneSubtle,
	})
	return col
}

// AccountTree is the Account tab: identity, the sign-out/refresh controls,
// connection facts, the read-only options mirror, and any foot error.
func AccountTree(s AccountState) *v1.Node {
	root := &v1.Node{Kind: v1.KindList, Height: 404, Gap: 4}
	if s.SignedIn {
		root.Children = append(root.Children,
			signedInCard(s),
			&v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				accountButton("signout", "Sign out", "soft", false),
				{Kind: v1.KindButton, ID: "refresh", Name: "Refresh", Role: "button",
					Icon: "restart_alt", Width: 40, Height: 40,
					Events: []v1.EventKind{v1.EventActivate}},
			}},
			infoRow("Account", dashed(s.Snap.Info.Username)),
			infoRow("Protocol", dashed(s.Snap.Status.Protocol)),
			infoRow("Interface", dashed(s.Snap.Interface)),
		)
	} else {
		root.Children = append(root.Children, signInCard(s))
	}
	root.Children = append(root.Children, optionsSection(s))
	if s.Err != "" {
		root.Children = append(root.Children, &v1.Node{
			Kind: v1.KindText, Text: s.Err, Tone: v1.ToneError,
		})
	}
	return root
}
