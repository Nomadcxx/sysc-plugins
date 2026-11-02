package lidguard

import (
	"strings"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	PanelWidth  = 560
	PanelHeight = 340

	panelInset = 12
	panelGap   = 10
	cardPad    = 12

	nodeToggle      = "toggle"
	nodePanelAction = "panel-action"
	nodeRefresh     = "refresh"
	busyDetail      = "Waiting for systemd to confirm the change."
)

func BarTree(s Snapshot) *v1.Node { return BarTreeAtWidth(s, barwidth.StandardWidth) }

// BarTreeAtWidth keeps the bar control icon-first when the available slot is
// narrow; the tooltip and panel retain the full state and recovery details.
func BarTreeAtWidth(s Snapshot, width int) *v1.Node {
	label, name, tone := barPresentation(s)
	// ponytail: At the shared 120px breakpoint only the long label fails; update this case if the breakpoint changes.
	if barwidth.Compact(width) || (width == 120 && label == "Unavailable") {
		label = ""
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: nodeToggle, Icon: "laptop", Text: label,
		Name: name, Role: "button", Tone: tone, Disabled: s.Busy,
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}}}
}

func barPresentation(s Snapshot) (label, name string, tone v1.Tone) {
	switch {
	case s.Busy:
		return "Working", "Lid Guard is changing", v1.ToneAccent
	case !s.Available || s.State == v1.ServiceStateUnavailable:
		return "Unavailable", "Lid Guard unavailable; open details", v1.ToneError
	case s.State == v1.ServiceStateActive:
		return "On", "Disable Lid Guard", v1.ToneAccent
	case s.State == v1.ServiceStateError:
		return "Error", "Lid Guard needs attention; open details", v1.ToneError
	case s.State == v1.ServiceStateUnknown:
		return "Checking", "Lid Guard status is unknown; open details", v1.ToneSubtle
	case s.State == v1.ServiceStateInactive:
		return "Off", "Enable Lid Guard", v1.ToneSubtle
	default:
		return "Checking", "Checking Lid Guard status", v1.ToneSubtle
	}
}

func TooltipTree(s Snapshot) *v1.Node {
	label, _, tone := barPresentation(s)
	if tone == v1.ToneError {
		tone = v1.ToneSubtle
	}
	children := []*v1.Node{
		{Kind: v1.KindText, Text: "Lid Guard", Bold: true},
		{Kind: v1.KindText, Text: label, Tone: tone},
	}
	if s.Busy {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: busyDetail, Tone: v1.ToneSubtle})
	} else if s.Message != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: s.Message, Tone: v1.ToneSubtle})
	}
	primaryHint := "Click: toggle"
	if !s.Available || s.State == v1.ServiceStateUnavailable || s.State == v1.ServiceStateUnknown || s.State == v1.ServiceStateError {
		primaryHint = "Click: details"
	}
	children = append(children,
		&v1.Node{Kind: v1.KindText, Text: primaryHint, Size: "caption", Tone: v1.ToneSubtle},
		&v1.Node{Kind: v1.KindText, Text: "Right-click: refresh", Size: "caption", Tone: v1.ToneSubtle},
		&v1.Node{Kind: v1.KindText, Text: "Middle-click: details", Size: "caption", Tone: v1.ToneSubtle},
	)
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Padding: 8, Children: children}
}

func PanelTree(s Snapshot) *v1.Node {
	state, tone := panelPresentation(s)
	detail := s.Message
	if s.Busy {
		detail = busyDetail
	} else if detail == "" {
		detail = panelDescription(s.State)
	}
	statusFill, statusTone, detailTone := "card", tone, v1.ToneSubtle
	if !s.Busy && (!s.Available || s.State == v1.ServiceStateUnavailable || s.State == v1.ServiceStateError) {
		statusFill, statusTone, detailTone = "error-container", v1.ToneNormal, v1.ToneNormal
	}
	recoverableError := s.State == v1.ServiceStateError && isRecoverableFailedUnit(s.State, s.Message)
	canAct := s.Available && !s.Busy && s.State != v1.ServiceStateUnavailable && s.State != v1.ServiceStateUnknown && (s.State != v1.ServiceStateError || recoverableError)
	refreshEnabled := !s.Busy && !s.Refreshing
	buttonWidth := (PanelWidth - 2*panelInset - 12) / 2
	active := s.State == v1.ServiceStateActive
	toggleText, toggleName := "Enable", "Enable Lid Guard"
	if s.Busy {
		toggleText, toggleName = "Working…", "Lid Guard is changing"
	} else if !s.Available || s.State == v1.ServiceStateUnavailable {
		toggleText, toggleName = "Unavailable", "Lid Guard is unavailable"
	} else if active {
		toggleText, toggleName = "Disable", "Disable Lid Guard"
	} else if s.State == v1.ServiceStateUnknown {
		toggleText, toggleName = "Checking", "Lid Guard status is unknown"
	} else if s.State == v1.ServiceStateError {
		if recoverableError {
			toggleText, toggleName = "Enable", "Enable Lid Guard to recover"
		} else {
			toggleText, toggleName = "Needs attention", "Lid Guard needs attention"
		}
	}
	toggleFill := "accent"
	if !canAct {
		toggleFill = "surface"
	}
	refreshFill := "soft"
	if !refreshEnabled {
		refreshFill = "surface"
	}
	statusChildren := []*v1.Node{{Kind: v1.KindText, Text: state, Size: "label", Bold: true, Tone: statusTone}}
	for _, line := range panelDetailLines(detail) {
		statusChildren = append(statusChildren, &v1.Node{
			Kind: v1.KindText, Text: line, Tooltip: detail, Size: "caption", Tone: detailTone,
			MaxWidth: PanelWidth - 2*panelInset - 2*cardPad,
		})
	}
	return &v1.Node{
		Kind: v1.KindColumn, Width: PanelWidth, Height: PanelHeight,
		Padding: panelInset, Gap: panelGap,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Height: 32, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: "laptop", IconSize: 24, Name: "Lid Guard"},
				{Kind: v1.KindText, Text: "Lid Guard", Size: "title", Bold: true},
			}},
			{Kind: v1.KindColumn, Fill: statusFill, Shape: "card", Height: 96, Padding: cardPad, Gap: 6, Children: statusChildren},
			{Kind: v1.KindRow, Height: 40, Gap: 12, Children: []*v1.Node{
				{Kind: v1.KindButton, ID: nodePanelAction, Text: toggleText, Name: toggleName,
					Role: "button", Fill: toggleFill, Shape: "stadium", Width: buttonWidth, Height: 40,
					Padding: 8, Disabled: !canAct, Events: []v1.EventKind{v1.EventActivate}},
				{Kind: v1.KindButton, ID: nodeRefresh, Icon: "restart_alt", Text: "Refresh", Name: "Refresh Lid Guard status",
					Role: "button", Fill: refreshFill, Shape: "stadium", Width: buttonWidth, Height: 40,
					Padding: 8, Disabled: !refreshEnabled, Events: []v1.EventKind{v1.EventActivate}},
			}},
			{Kind: v1.KindColumn, Fill: "card", Shape: "card", Height: 116, Padding: cardPad, Gap: 4, Children: []*v1.Node{
				{Kind: v1.KindText, Text: "Lid Guard blocks lid-close handling only.", Size: "caption"},
				{Kind: v1.KindText, Text: "Idle timeout and manual suspend can still suspend the laptop.", Size: "caption"},
				{Kind: v1.KindText, Text: "Recovery command", Size: "caption", Tone: v1.ToneSubtle},
				{Kind: v1.KindText, Text: "systemctl --user stop " + unitName, Size: "mono"},
			}},
		},
	}
}

func panelPresentation(s Snapshot) (string, v1.Tone) {
	switch {
	case s.Busy:
		return "Changing state", v1.ToneAccent
	case !s.Available || s.State == v1.ServiceStateUnavailable:
		return "Unavailable", v1.ToneError
	case s.State == v1.ServiceStateActive:
		return "Enabled", v1.ToneAccent
	case s.State == v1.ServiceStateInactive:
		return "Disabled", v1.ToneSubtle
	case s.State == v1.ServiceStateError:
		return "Service error", v1.ToneError
	default:
		return "Checking status", v1.ToneSubtle
	}
}

func panelDescription(state v1.ServiceState) string {
	switch state {
	case v1.ServiceStateActive:
		return "Lid close is inhibited while the transient user service runs."
	case v1.ServiceStateInactive:
		return "The system's normal lid-close policy is in effect."
	case v1.ServiceStateUnavailable:
		return "Systemd commands or the user service manager are unavailable."
	case v1.ServiceStateError:
		return "Systemd could not confirm the Lid Guard service state."
	default:
		return "Checking the systemd user service."
	}
}

func panelDetailLines(detail string) []string {
	const missingCommands = "Missing required commands: "
	if strings.HasPrefix(detail, missingCommands) {
		return []string{"Missing required commands:", strings.TrimPrefix(detail, missingCommands)}
	}
	return []string{visibleMessage(detail, 96)}
}

func visibleMessage(message string, maxBytes int) string {
	if len(message) <= maxBytes {
		return message
	}
	message = message[:maxBytes-3]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message + "..."
}

// HandleInput maps the upstream bar gestures to actions and keeps the detail
// panel reachable without changing the primary toggle or refresh gestures.
func HandleInput(ev *v1.InputEvent, s Snapshot) (action string, openPanel bool) {
	if ev == nil {
		return "", false
	}
	if ev.Node == nodeRefresh && ev.Event == v1.EventActivate && !s.Busy && !s.Refreshing {
		return "refresh", false
	}
	if ev.Node == nodePanelAction && ev.Event == v1.EventActivate && !s.Busy {
		if s.Refreshing {
			switch s.State {
			case v1.ServiceStateActive, v1.ServiceStateInactive:
				return "toggle", false
			case v1.ServiceStateError:
				if isRecoverableFailedUnit(s.State, s.Message) {
					return "toggle", false
				}
			}
			return "", false
		}
		switch s.State {
		case v1.ServiceStateActive:
			return "disable", false
		case v1.ServiceStateInactive:
			return "enable", false
		case v1.ServiceStateError:
			if isRecoverableFailedUnit(s.State, s.Message) {
				return "enable", false
			}
			return "", false
		default:
			return "", false
		}
	}
	if ev.Node != nodeToggle {
		return "", false
	}
	if ev.Event == v1.EventPointer {
		switch ev.Button {
		case v1.ButtonSecondary:
			if !s.Busy && !s.Refreshing {
				return "refresh", false
			}
		case v1.ButtonMiddle:
			return "", true
		}
		return "", false
	}
	if ev.Event == v1.EventActivate && !s.Busy {
		if !s.Available || s.State == v1.ServiceStateUnavailable || s.State == v1.ServiceStateUnknown || s.State == v1.ServiceStateError {
			return "", true
		}
		if s.Refreshing {
			return "toggle", false
		}
		if s.State == v1.ServiceStateActive {
			return "disable", false
		}
		return "enable", false
	}
	return "", false
}
