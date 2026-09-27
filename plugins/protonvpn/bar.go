package protonvpn

import (
	"fmt"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarState is everything the bar and tooltip render from.
type BarState struct {
	Snap    Snapshot
	Mode    string // icon | code | status
	Quick   string // quick_connect setting, for the tooltip hint
	Traffic bool   // traffic_monitoring setting; gates the tooltip rates line
}

// statusWord is the human phase name shown in status mode and the tooltip.
func statusWord(p Phase) string {
	switch p {
	case PhaseConnecting:
		return "Connecting…"
	case PhaseConnected:
		return "Protected"
	case PhaseDisconnecting:
		return "Disconnecting…"
	case PhaseError:
		return "Connection error"
	default:
		return "Unprotected"
	}
}

// phaseIcon pairs the bar glyph with its theme tone. Disconnecting keeps the
// bolt: the tunnel is mid-transition, like connecting.
func phaseIcon(p Phase) (string, v1.Tone) {
	switch p {
	case PhaseConnecting, PhaseDisconnecting:
		return "bolt", v1.ToneAccent
	case PhaseConnected:
		return "shield", v1.ToneAccent
	case PhaseError:
		return "gpp_bad", v1.ToneError
	default:
		return "vpn_key_off", v1.ToneSubtle
	}
}

// barLabel is the text beside the icon for the mode, "" for icon-only.
func barLabel(s BarState) string {
	switch s.Mode {
	case "code":
		// ponytail: country is stale mid-transition, so one ellipsis covers
		// connecting and disconnecting
		switch s.Snap.Phase {
		case PhaseConnecting, PhaseDisconnecting:
			return "…"
		}
		return strings.ToUpper(s.Snap.Status.Country)
	case "status":
		return statusWord(s.Snap.Phase)
	default: // icon
		return ""
	}
}

// Bar is the 240×32 bar row: one button carrying the phase icon and, per
// mode, the country code or the status word. Activate opens the panel;
// pointer carries the right click for quick connect.
func Bar(s BarState) *v1.Node {
	icon, tone := phaseIcon(s.Snap.Phase)
	btn := &v1.Node{
		Kind:   v1.KindButton,
		ID:     "bar",
		Name:   "ProtonVPN",
		Role:   "button",
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon, Tone: tone},
		},
	}
	if text := barLabel(s); text != "" {
		btn.Children = append(btn.Children, &v1.Node{Kind: v1.KindText, Text: text, Tone: tone})
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{btn}}
}

// formatRate renders bytes/s for the tooltip; the traffic panel reuses it.
// Bytes are whole; a scaled unit takes one decimal below 10, none above.
func formatRate(bps float64) string {
	var v float64
	var unit string
	switch {
	case bps >= 1e9:
		v, unit = bps/1e9, "GB/s"
	case bps >= 1e6:
		v, unit = bps/1e6, "MB/s"
	case bps >= 1e3:
		v, unit = bps/1e3, "KB/s"
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, unit)
	}
	return fmt.Sprintf("%.0f %s", v, unit)
}

// Tooltip is the 280×200 hover column: status word, server, location, IP,
// rates, protocol. Disconnected collapses to the quick-connect hint.
func Tooltip(s BarState) *v1.Node {
	lines := []*v1.Node{{Kind: v1.KindText, Text: statusWord(s.Snap.Phase), Bold: true}}
	if s.Snap.Phase == PhaseDisconnected {
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: "Right-click to quick connect", Tone: v1.ToneSubtle})
		return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: lines}
	}
	st := s.Snap.Status
	if st.Server != "" {
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: st.Server})
	}
	if st.Location != "" {
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: st.Location, Tone: v1.ToneSubtle})
	}
	if s.Snap.IP != "" {
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: s.Snap.IP, Tone: v1.ToneSubtle})
	}
	if s.Traffic {
		lines = append(lines, &v1.Node{Kind: v1.KindRow, Gap: 4, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "download", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: formatRate(s.Snap.RxRate), Tabular: true},
			{Kind: v1.KindText, Text: "·", Tone: v1.ToneSubtle},
			{Kind: v1.KindIcon, Icon: "upload", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: formatRate(s.Snap.TxRate), Tabular: true},
		}})
	}
	if st.Protocol != "" {
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: st.Protocol, Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: lines}
}
