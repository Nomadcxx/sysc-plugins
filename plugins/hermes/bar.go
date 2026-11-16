// Package hermes is the sysc-shell Hermes Agent plugin.
package hermes

import (
	"net"
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

// BarStatus is the bar's reading of the bridge and Hermes, the seven values
// noctalia-hermes-agent shows.
type BarStatus string

const (
	BarStatusOffline   BarStatus = "offline"
	BarStatusIdle      BarStatus = "idle"
	BarStatusBusy      BarStatus = "busy"
	BarStatusAttention BarStatus = "attention"
	BarStatusDegraded  BarStatus = "degraded"
	BarStatusError     BarStatus = "error"
	BarStatusUnknown   BarStatus = "unknown"
)

// BarStatuses lists every status in noctalia's order.
var BarStatuses = []BarStatus{
	BarStatusOffline, BarStatusIdle, BarStatusBusy, BarStatusAttention,
	BarStatusDegraded, BarStatusError, BarStatusUnknown,
}

type barStatusLook struct {
	label, glyph string
	tone         v1.Tone
}

// barStatusLooks carries noctalia's labels (translations/en.json) and colours.
// Glyphs follow the locked design table on the pinned v0.2.5 catalogue.
var barStatusLooks = map[BarStatus]barStatusLook{
	BarStatusOffline:   {"Offline", "power_settings_new", v1.ToneError},
	BarStatusIdle:      {"Online", "check", v1.ToneAccent},
	BarStatusBusy:      {"Working", "refresh", v1.ToneAccent},
	BarStatusAttention: {"Needs You", "notifications-active", v1.ToneWarning},
	BarStatusDegraded:  {"Degraded", "gpp_maybe", v1.ToneWarning},
	BarStatusError:     {"Error", "cancel", v1.ToneError},
	BarStatusUnknown:   {"Unknown", "ai-usage", v1.ToneNormal},
}

func (s BarStatus) look() barStatusLook {
	if l, ok := barStatusLooks[s]; ok {
		return l
	}
	return barStatusLooks[BarStatusUnknown]
}

// Label is the status as the user reads it.
func (s BarStatus) Label() string { return s.look().label }

// Glyph is the status icon name.
func (s BarStatus) Glyph() string { return s.look().glyph }

// Tone is the status colour role.
func (s BarStatus) Tone() v1.Tone { return s.look().tone }

// BarState is the narrow slice of the bridge snapshot the bar and tooltip
// read. It stands in until the runtime snapshot lands; the runtime fills it
// from bridge.status, bridge.error, hermes.status, summary.gateway.status
// and hermes.model.
type BarState struct {
	BridgeStatus  string
	BridgeError   string
	HermesStatus  string
	GatewayStatus string
	Model         string
	// ClientOnly and Host feed the tooltip's cleartext warning.
	ClientOnly bool
	Host       string
}

// deriveBarStatus follows the design's first steps on this stand-in: a bridge
// status of "error" is error; any other bridge that is not online is
// offline; otherwise Hermes's own status wins, and while Hermes has not
// reported one a running gateway reads as idle.
func deriveBarStatus(s BarState) BarStatus {
	if s.BridgeStatus == "error" {
		return BarStatusError
	}
	if s.BridgeStatus != "online" {
		return BarStatusOffline
	}
	if s.HermesStatus != "" && s.HermesStatus != string(BarStatusUnknown) {
		if _, ok := barStatusLooks[BarStatus(s.HermesStatus)]; ok {
			return BarStatus(s.HermesStatus)
		}
		return BarStatusUnknown
	}
	if s.GatewayStatus == "running" {
		return BarStatusIdle
	}
	return BarStatusUnknown
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
	status := deriveBarStatus(state)
	if hideWhenIdle && status == BarStatusIdle {
		return &v1.Node{Kind: v1.KindRow}
	}
	button := &v1.Node{
		Kind: v1.KindButton, ID: barOpenID, Name: "Open Hermes: " + status.Label(),
		Role: "button", Width: 24, Height: 24, Padding: 1,
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}
	if barwidth.Compact(width) {
		button.Icon, button.Tone = status.Glyph(), status.Tone()
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
		{Kind: v1.KindIcon, Icon: status.Glyph(), Tone: status.Tone(), Name: status.Label()},
	}}
	if text := barText(status); text != "" {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: text, Bold: true, Tone: status.Tone()})
	}
	return row
}

func barText(s BarStatus) string {
	switch s {
	case BarStatusIdle, BarStatusUnknown:
		return ""
	case BarStatusAttention:
		return "!"
	}
	return s.Label()
}

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
	if w := cleartextWarning(state.ClientOnly, state.Host); w != "" {
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

func cleartextWarning(clientOnly bool, host string) string {
	host = strings.TrimSpace(host)
	if !clientOnly || host == "" || loopbackHost(host) {
		return ""
	}
	return "Cleartext HTTP to " + host + ": the token and prompts are not encrypted."
}

func loopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func headline(state BarState, prefix string) string {
	line := prefix + deriveBarStatus(state).Label()
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
	status := deriveBarStatus(state)
	service := v1.ServiceStateActive
	switch status {
	case BarStatusOffline:
		service = v1.ServiceStateInactive
	case BarStatusError:
		service = v1.ServiceStateError
	case BarStatusUnknown:
		service = v1.ServiceStateUnknown
	}
	msg := headline(state, "")
	if e := strings.TrimSpace(state.BridgeError); e != "" && (status == BarStatusOffline || status == BarStatusError) {
		msg = status.Label() + " · " + e
	}
	return &v1.ServiceStatus{
		ServiceID: serviceID, State: service,
		Message: clip(msg, v1.MaxServiceStatusMessageBytes), Revision: revision,
	}
}

// CanStartGateway follows noctalia's menu: Start gateway is offered only
// outside client-only mode, while Hermes is offline or unknown.
func CanStartGateway(s BarStatus, clientOnly bool) bool {
	return !clientOnly && (s == BarStatusOffline || s == BarStatusUnknown)
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
