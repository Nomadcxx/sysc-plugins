// Package hermes is the sysc-shell Hermes Agent plugin.
package hermes

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	// serviceID is the manifest's one service.
	serviceID = "hermes"

	// avatarFile ships in the plugin's assets directory, one level above
	// the binary in bin/.
	avatarFile = "hermes-icon.png"

	// barOpenID is the bar's one control. Activate opens Hermes; a pointer
	// event carries the right click for the menu.
	barOpenID = "open"

	// fallbackAvatarIcon stands in for the avatar when the asset is
	// missing, the same glyph the scaffold and the quick action use.
	fallbackAvatarIcon = "ai-usage"

	// tooltipLineBytes keeps each tooltip line inside the 280-wide
	// tooltip at the host's eight pixels per byte.
	tooltipLineBytes = 34

	// avatarSize is the avatar edge inside a 24-pixel bar button with one
	// pixel of padding.
	avatarSize = 22
)

// BarState is the narrow slice of the bridge snapshot the bar, tooltip and
// summary read. Status is the one unified reading derived from the runtime
// State; the rest is display input from the same snapshot.
type BarState struct {
	Status        Status
	BridgeError   string
	GatewayStatus string
	Model         string
	// ClientOnly and Host feed the tooltip's cleartext warning.
	ClientOnly bool
	Host       string
}

// BarTree renders the bar at the standard width.
func BarTree(state BarState, hideWhenIdle bool, avatar string) *v1.Node {
	return BarTreeAtWidth(state, hideWhenIdle, avatar, barwidth.StandardWidth)
}

// BarTreeAtWidth renders the bar: the avatar button, the status glyph and,
// outside idle and unknown, the status label ("!" for attention). Below the
// compact width the button alone carries the status glyph. With
// hide_when_idle on, an idle bar is an empty row, as plugins/updates hides
// at zero.
func BarTreeAtWidth(state BarState, hideWhenIdle bool, avatar string, width int) *v1.Node {
	status := state.Status
	if hideWhenIdle && status.Key == StatusIdle {
		return &v1.Node{Kind: v1.KindRow}
	}
	button := &v1.Node{
		Kind: v1.KindButton, ID: barOpenID, Name: "Open Hermes: " + status.Text,
		Role: "button", Width: 24, Height: 24, Padding: 1,
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}
	if barwidth.Compact(width) {
		button.Icon, button.Tone = status.Icon, status.Tone
		return &v1.Node{Kind: v1.KindRow, Height: 24, Children: []*v1.Node{button}}
	}
	if avatar != "" {
		button.Children = []*v1.Node{{
			Kind: v1.KindImage, Path: avatar, Name: "Hermes", Role: "img", ImageSize: avatarSize,
		}}
	} else {
		button.Icon = fallbackAvatarIcon
	}
	row := &v1.Node{Kind: v1.KindRow, Height: 24, Gap: 6, Children: []*v1.Node{
		button,
		{Kind: v1.KindIcon, Icon: status.Icon, Tone: status.Tone, Name: status.Text},
	}}
	if text := barText(status); text != "" {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: text, Bold: true, Tone: status.Tone})
	}
	return row
}

func barText(s Status) string { return s.BarText }

// TooltipTree renders noctalia's tooltip: "Hermes: <status> · <model>",
// then the bridge error on its own line, then a cleartext warning when
// client-only mode talks to a non-loopback host.
func TooltipTree(state BarState) *v1.Node {
	root := &v1.Node{Kind: v1.KindColumn, Gap: 2}
	root.Children = append(root.Children, &v1.Node{
		Kind: v1.KindText, Text: clip(headline(state, "Hermes: "), tooltipLineBytes),
	})
	if e := strings.TrimSpace(state.BridgeError); e != "" {
		root.Children = append(root.Children, &v1.Node{
			Kind: v1.KindText, Text: clip(e, tooltipLineBytes), Tone: v1.ToneError,
		})
	}
	if w := CleartextWarning(state.ClientOnly, state.Host); w != "" {
		// len(w) bounds the line count (every line holds at least one
		// byte), so the warning is never capped or ellipsised.
		for _, line := range wrapLines(w, tooltipLineBytes, len(w)) {
			root.Children = append(root.Children, &v1.Node{
				Kind: v1.KindText, Text: line, Tone: v1.ToneWarning,
			})
		}
	}
	return root
}

func headline(state BarState, prefix string) string {
	line := prefix + state.Status.Text
	if m := strings.TrimSpace(state.Model); m != "" {
		line += " · " + m
	}
	return line
}

// AvatarPath resolves assets/hermes-icon.png next to the plugin's bin
// directory; empty means the bar falls back to a glyph.
func AvatarPath(exeDir string) string {
	if exeDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		exeDir = filepath.Dir(exe)
	}
	p := filepath.Join(exeDir, "..", "assets", avatarFile)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// ServiceStatusFor maps the bar status onto the host's service states.
// Busy stays false: it reports a running service action, which the runtime
// owns.
func ServiceStatusFor(state BarState, revision uint64) *v1.ServiceStatus {
	status := state.Status
	service := v1.ServiceStateActive
	switch status.Key {
	case StatusOffline:
		service = v1.ServiceStateInactive
	case StatusError:
		service = v1.ServiceStateError
	case StatusUnknown:
		service = v1.ServiceStateUnknown
	}
	msg := headline(state, "")
	if e := strings.TrimSpace(state.BridgeError); e != "" && (status.Key == StatusOffline || status.Key == StatusError) {
		msg = status.Text + " · " + e
	}
	return &v1.ServiceStatus{
		ServiceID: serviceID, State: service,
		Message: clip(msg, v1.MaxServiceStatusMessageBytes), Revision: revision,
	}
}

// CanStartGateway follows noctalia's menu: Start gateway is offered only
// outside client-only mode, while Hermes is offline or unknown.
func CanStartGateway(s Status, clientOnly bool) bool {
	return !clientOnly && (s.Key == StatusOffline || s.Key == StatusUnknown)
}

// clip collapses whitespace and cuts text to maxBytes on a rune boundary,
// ending in an ellipsis when it cuts.
func clip(text string, maxBytes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= maxBytes {
		return text
	}
	const ellipsis = "…"
	cut := maxBytes - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + ellipsis
}
