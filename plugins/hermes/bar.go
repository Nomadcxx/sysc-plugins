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

// Status is the bar's reading of the bridge and Hermes, the seven values
// noctalia-hermes-agent shows.
type Status string

const (
	StatusOffline   Status = "offline"
	StatusIdle      Status = "idle"
	StatusBusy      Status = "busy"
	StatusAttention Status = "attention"
	StatusDegraded  Status = "degraded"
	StatusError     Status = "error"
	StatusUnknown   Status = "unknown"
)

// Statuses lists every status in noctalia's order.
var Statuses = []Status{
	StatusOffline, StatusIdle, StatusBusy, StatusAttention,
	StatusDegraded, StatusError, StatusUnknown,
}

type statusLook struct {
	label, glyph string
	tone         v1.Tone
}

// statusLooks carries noctalia's labels (translations/en.json) and colours.
// Glyphs come from the pinned sysc-shell catalogue; the two noctalia alert
// glyphs have no warning-free match, so degraded and error use link_off and
// cancel.
var statusLooks = map[Status]statusLook{
	StatusOffline:   {"Offline", "power_settings_new", v1.ToneError},
	StatusIdle:      {"Online", "check", v1.ToneAccent},
	StatusBusy:      {"Working", "bolt", v1.ToneAccent},
	StatusAttention: {"Needs You", "notifications-active", v1.ToneWarning},
	StatusDegraded:  {"Degraded", "link_off", v1.ToneWarning},
	StatusError:     {"Error", "cancel", v1.ToneError},
	StatusUnknown:   {"Unknown", "ai-usage", v1.ToneNormal},
}

func (s Status) look() statusLook {
	if l, ok := statusLooks[s]; ok {
		return l
	}
	return statusLooks[StatusUnknown]
}

// Label is the status as the user reads it.
func (s Status) Label() string { return s.look().label }

// Glyph is the status icon name.
func (s Status) Glyph() string { return s.look().glyph }

// Tone is the status colour role.
func (s Status) Tone() v1.Tone { return s.look().tone }

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
}

// DeriveStatus follows noctalia's deriveStatus: a bridge that is not online
// is offline; otherwise Hermes's own status wins, and while Hermes has not
// reported one a running gateway reads as idle.
func DeriveStatus(s BarState) Status {
	if s.BridgeStatus != "online" {
		return StatusOffline
	}
	if s.HermesStatus != "" && s.HermesStatus != string(StatusUnknown) {
		if _, ok := statusLooks[Status(s.HermesStatus)]; ok {
			return Status(s.HermesStatus)
		}
		return StatusUnknown
	}
	if s.GatewayStatus == "running" {
		return StatusIdle
	}
	return StatusUnknown
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
	status := DeriveStatus(state)
	if hideWhenIdle && status == StatusIdle {
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

func barText(s Status) string {
	switch s {
	case StatusIdle, StatusUnknown:
		return ""
	case StatusAttention:
		return "!"
	}
	return s.Label()
}

// TooltipTree renders noctalia's tooltip: "Hermes: <status> · <model>",
// then the bridge error on its own line.
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
	return root
}

func headline(state BarState, prefix string) string {
	line := prefix + DeriveStatus(state).Label()
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
	status := DeriveStatus(state)
	service := v1.ServiceStateActive
	switch status {
	case StatusOffline:
		service = v1.ServiceStateInactive
	case StatusError:
		service = v1.ServiceStateError
	case StatusUnknown:
		service = v1.ServiceStateUnknown
	}
	msg := headline(state, "")
	if e := strings.TrimSpace(state.BridgeError); e != "" && (status == StatusOffline || status == StatusError) {
		msg = status.Label() + " · " + e
	}
	return &v1.ServiceStatus{
		ServiceID: serviceID, State: service,
		Message: clip(msg, v1.MaxServiceStatusMessageBytes), Revision: revision,
	}
}

// CanStartGateway follows noctalia's menu: Start gateway is offered only
// outside client-only mode, while Hermes is offline or unknown.
func CanStartGateway(s Status, clientOnly bool) bool {
	return !clientOnly && (s == StatusOffline || s == StatusUnknown)
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
