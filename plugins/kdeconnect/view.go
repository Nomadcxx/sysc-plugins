package kdeconnect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// deviceIcon maps a KDE Connect device type onto a catalogue name, the
// reference shell's type table.
func deviceIcon(dev *Device) string {
	if dev == nil {
		return "devices"
	}
	switch dev.Type {
	case "phone", "smartphone":
		return "smartphone"
	case "tablet":
		return "tablet"
	case "desktop", "computer":
		return "desktop-windows"
	case "laptop":
		return "laptop"
	case "tv":
		return "tv"
	}
	return "devices"
}

// selectedDevice returns the snapshot's selected device, or nil when the
// selection names nothing in the list.
func selectedDevice(snap Snapshot) *Device {
	if snap.SelectedID == "" {
		return nil
	}
	for i := range snap.Devices {
		if snap.Devices[i].ID == snap.SelectedID {
			return &snap.Devices[i]
		}
	}
	return nil
}

// BarTree is the bar pill: the offline glyph with N/A while the daemon is
// unreachable, and — matching the reference pill — the offline glyph again
// whenever the selected device is not reachable, with the percent only for
// a connected, reporting device. The whole control opens the panel.
func BarTree(snap Snapshot) *v1.Node {
	icon, label := "phonelink-off", "N/A"
	if snap.Available {
		icon, label = "smartphone", ""
		if dev := selectedDevice(snap); dev != nil {
			if dev.Reachable {
				if dev.BatteryKnown && dev.BatteryCharge >= 0 {
					label = fmt.Sprintf("%d%%", dev.BatteryCharge)
				}
			} else {
				icon = "phonelink-off"
			}
		}
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Icon: icon, Text: label,
		Name: "Open phone connect", Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
	}}}
}

// TooltipTree names the backend, the selected device's state, and — when
// the daemon announced one — the daemon's identity, the DMS settings
// status card's information on the plugin's own surface.
func TooltipTree(snap Snapshot) *v1.Node {
	detail := "unavailable"
	announced := ""
	if snap.Available {
		if dev := selectedDevice(snap); dev != nil {
			detail = deviceStatus(dev)
			if dev.BatteryKnown && dev.BatteryCharge >= 0 {
				detail += fmt.Sprintf(" · %d%%", dev.BatteryCharge)
				if dev.BatteryCharging {
					detail += " · charging"
				}
			}
		} else if len(snap.Devices) == 0 {
			detail = "no devices"
		} else {
			detail = "no device selected"
		}
		if snap.AnnouncedName != "" || snap.SelfID != "" {
			announced = fmt.Sprintf("announced as %s (%s)", snap.AnnouncedName, snap.SelfID)
		}
	}
	children := []*v1.Node{
		{Kind: v1.KindText, Text: "Phone Connect"},
		{Kind: v1.KindText, Text: detail, Size: "caption", Tone: v1.ToneSubtle},
	}
	if announced != "" {
		children = append(children, &v1.Node{
			Kind: v1.KindText, Text: announced, Size: "caption", Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Children: children}
}

// Composer names the composer card the panel shows under the actions. The
// entry point owns the state; the view only renders it.
type Composer uint8

const (
	ComposerNone Composer = iota
	ComposerShare
	ComposerSMS
)

// Drafts carries the composer field values the entry point tracks, so the
// view can gate the send buttons the way the DMS dialogs do.
type Drafts struct {
	ShareText string
	ShareFile string
	SmsNumber string
	SmsBody   string
}

// PanelTree is the phone-connect panel: the daemon header over the state,
// pairing, switcher, device, action, info, and composer sections.
func PanelTree(snap Snapshot, settings Settings, composer Composer, drafts Drafts) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 10, Children: []*v1.Node{headerTree(snap)}}
	if !snap.Available {
		col.Children = append(col.Children, unavailableCard())
		return col
	}
	if len(snap.Devices) == 0 {
		col.Children = append(col.Children, stateCard(
			"No devices found",
			"Make sure the KDE Connect app is running and the devices are paired."))
		return col
	}
	selected := selectedDevice(snap)
	if selected == nil {
		return col
	}
	switch {
	case selected.PairRequestedByPeer || selected.PairRequested:
		col.Children = append(col.Children, pairingCard(selected))
	case !selected.Paired:
		col.Children = append(col.Children, unpairedCard(selected))
	default:
		if len(snap.Devices) > 1 {
			col.Children = append(col.Children, switcherTree(snap, selected))
		}
		if settings.ShowDeviceCard {
			col.Children = append(col.Children, deviceCardTree(selected))
		}
		col.Children = append(col.Children,
			actionRowTree(selected, settings),
			infoRowsTree(selected))
		switch composer {
		case ComposerShare:
			col.Children = append(col.Children, shareComposerTree(drafts))
		case ComposerSMS:
			col.Children = append(col.Children, smsComposerTree(drafts))
		}
	}
	return col
}

// headerTree is the daemon header card: the backend name and its counts
// beside a right-pinned refresh control, the DMS popout header's shape.
func headerTree(snap Snapshot) *v1.Node {
	title := snap.BackendName
	if title == "" {
		title = "Phone Connect"
	}
	detail := "unavailable"
	if snap.Available {
		connected, paired := 0, 0
		for i := range snap.Devices {
			if snap.Devices[i].Reachable {
				connected++
			}
			if snap.Devices[i].Paired {
				paired++
			}
		}
		detail = fmt.Sprintf("%d connected • %d paired", connected, paired)
	}
	return &v1.Node{Kind: v1.KindRow, Fill: "card", Radius: 12, Padding: 12, Gap: 10,
		PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindColumn, Children: []*v1.Node{
				{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
					{Kind: v1.KindIcon, Icon: "devices"},
					{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
						{Kind: v1.KindText, Text: title, Bold: true, Size: "title"},
						{Kind: v1.KindText, Text: detail, Size: "caption", Tone: v1.ToneAccent},
					}},
				}},
			}},
			{Kind: v1.KindButton, ID: "refresh", Icon: "refresh",
				Name: "Refresh devices", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}}
}

// stateCard is one full-width message card with a bold headline over a
// muted hint.
func stateCard(headline, hint string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 4,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: headline, Bold: true},
			{Kind: v1.KindText, Text: hint, Tone: v1.ToneSubtle},
		}}
}

// unavailableCard is the DMS UnavailableMessage: an error-styled card that
// names the problem and the fix.
func unavailableCard() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "error-container", Radius: 12, Padding: 14, Gap: 4,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Phone Connect Not Available", Bold: true, Tone: v1.ToneError},
			{Kind: v1.KindText, Text: "Start kdeconnectd to use this plugin.", Tone: v1.ToneError},
		}}
}

// shareComposerTree is the share card: one URL-or-text field with its two
// sends and one file-path field, the DMS ShareDialog's contents, with the
// same send gating — URI only for a valid URI, text only when non-empty.
func shareComposerTree(drafts Drafts) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 8,
		Children: []*v1.Node{
			composerHeader("Share", "share", "share-close"),
			{Kind: v1.KindTextInput, ID: "share-text", Name: "URL or text to share", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				gatedButton("share-url-send", "send", "Send URL", "Share the URL with the device",
					isURILike(drafts.ShareText)),
				gatedButton("share-text-send", "send", "Send text", "Share the text with the device",
					strings.TrimSpace(drafts.ShareText) != ""),
			}},
			{Kind: v1.KindTextInput, ID: "share-file", Name: "File path to send", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				gatedButton("share-file-send", "send", "Send file", "Send the file to the device",
					drafts.ShareFile != ""),
			}},
		}}
}

// smsComposerTree is the SMS card: number and single-line body with send
// gated on both, plus the launch-app escape hatch, the DMS SmsDialog.
func smsComposerTree(drafts Drafts) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 8,
		Children: []*v1.Node{
			composerHeader("New message", "sms", "sms-close"),
			{Kind: v1.KindTextInput, ID: "sms-number", Name: "Phone number", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindTextInput, ID: "sms-body", Name: "Message", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				gatedButton("sms-send", "send", "Send", "Send the message",
					drafts.SmsNumber != "" && drafts.SmsBody != ""),
				{Kind: v1.KindButton, ID: "sms-app", Text: "Open app",
					Name: "Open the SMS app on the device", Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
			}},
		}}
}

// composerHeader is a composer title row with a right-pinned close button.
func composerHeader(title, icon, closeID string) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon},
			{Kind: v1.KindText, Text: title, Bold: true},
		}},
		{Kind: v1.KindButton, ID: closeID, Icon: "close",
			Name: "Close the composer", Role: "button",
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

// gatedButton is a send button that sits disabled while its draft is not
// sendable, the DMS dialogs' enablement. Setting Icon and Text together
// relies on the host converter synthesising [icon, text] children for the
// button (sysc-shell internal/plugin/view.go, the KindButton branch).
func gatedButton(id, icon, text, name string, enabled bool) *v1.Node {
	b := &v1.Node{Kind: v1.KindButton, ID: id, Icon: icon, Text: text, Fill: "accent",
		Name: name, Role: "button",
		Events: []v1.EventKind{v1.EventActivate}}
	if !enabled {
		b.Disabled = true
	}
	return b
}

// isURILike reports whether the text carries a URI scheme and no spaces,
// the reference dialog's URI gate in reduced form.
func isURILike(text string) bool {
	value := strings.TrimSpace(text)
	if value == "" || !uriScheme.MatchString(value) {
		return false
	}
	return !strings.ContainsAny(value, " \t")
}

var uriScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// pairingCard covers an incoming pairing request (verification key plus
// accept and reject) and an outgoing one (waiting plus cancel).
func pairingCard(dev *Device) *v1.Node {
	hint := "Pairing request sent. Accept it on the device."
	actions := []*v1.Node{{
		Kind: v1.KindButton, ID: "pair-cancel", Icon: "close", Text: "Cancel", Fill: "error-container",
		Name: "Cancel the pairing request", Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
	}}
	if dev.PairRequestedByPeer {
		hint = "Accept the pairing request."
		if dev.VerificationKey != "" {
			hint = "Verification: " + dev.VerificationKey
		}
		actions = []*v1.Node{
			{Kind: v1.KindButton, ID: "pair-accept", Icon: "check", Text: "Accept", Fill: "accent",
				Name: "Accept the pairing request", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "pair-reject", Icon: "close", Text: "Reject", Fill: "error-container",
				Name: "Reject the pairing request", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}
	}
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 6,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: deviceIcon(dev)},
				{Kind: v1.KindText, Text: dev.Name, Bold: true, Size: "title"},
			}},
			{Kind: v1.KindText, Text: hint, Tone: v1.ToneSubtle},
			{Kind: v1.KindRow, Gap: 8, Children: actions},
		}}
}

// unpairedCard offers the pairing request for a known but unpaired device.
func unpairedCard(dev *Device) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 6,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: deviceIcon(dev)},
				{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
					{Kind: v1.KindText, Text: dev.Name, Bold: true, Size: "title"},
					{Kind: v1.KindText, Text: "Not paired", Size: "caption", Tone: v1.ToneSubtle},
				}},
			}},
			{Kind: v1.KindButton, ID: "pair", Icon: "link", Text: "Request pairing", Fill: "accent",
				Name: "Request pairing with the device", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}}
}

// switcherTree lists every other device, the DMS device switcher.
func switcherTree(snap Snapshot, selected *Device) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8}
	for i := range snap.Devices {
		dev := &snap.Devices[i]
		if dev.ID == selected.ID {
			continue
		}
		col.Children = append(col.Children, switcherCardTree(dev))
	}
	return col
}

// switcherCardTree renders one switcher card, the DMS DeviceCard: the type
// icon, name, status line, battery and network chips, and — always, not
// only for the selected device — the pairing actions.
func switcherCardTree(dev *Device) *v1.Node {
	header := &v1.Node{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: deviceIcon(dev)},
		{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
			{Kind: v1.KindText, Text: dev.Name, Bold: true},
			{Kind: v1.KindText, Text: cardStatus(dev), Size: "caption",
				Tone: cardStatusTone(dev)},
		}},
	}}

	chips := []*v1.Node{}
	if dev.BatteryKnown && dev.BatteryCharge >= 0 {
		chips = append(chips,
			&v1.Node{Kind: v1.KindIcon, Icon: batteryIconName(dev)},
			&v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d%%", dev.BatteryCharge), Size: "caption"})
	}
	if dev.NetworkKnown && dev.NetworkStrength >= 0 {
		chips = append(chips, &v1.Node{Kind: v1.KindIcon, Icon: networkStrengthIcon(dev.NetworkStrength)})
	}
	if len(chips) > 0 {
		// Two children make the header a pin-end row: the chips column sits
		// at the card's right edge, the DMS card's status row.
		header = &v1.Node{Kind: v1.KindRow, Gap: 10, PinEnd: true, Children: []*v1.Node{
			header, {Kind: v1.KindColumn, Gap: 2, Children: chips}}}
	}

	// The select affordance leads the action row; pairing actions follow on
	// the cards they apply to.
	actions := []*v1.Node{{
		Kind: v1.KindButton, ID: "select-" + dev.ID, Text: "Use",
		Name: "Switch to " + dev.Name, Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
	}}
	switch {
	case dev.PairRequestedByPeer:
		actions = append(actions,
			&v1.Node{Kind: v1.KindButton, ID: "accept-" + dev.ID, Text: "Accept", Fill: "accent",
				Name: "Accept pairing with " + dev.Name, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			&v1.Node{Kind: v1.KindButton, ID: "reject-" + dev.ID, Text: "Reject", Fill: "error-container",
				Name: "Reject pairing with " + dev.Name, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}})
	case dev.Reachable && !dev.Paired:
		actions = append(actions,
			&v1.Node{Kind: v1.KindButton, ID: "pair-" + dev.ID, Text: "Request pairing", Fill: "accent",
				Name: "Request pairing with " + dev.Name, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}})
	}

	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 10, Padding: 10, Gap: 8,
		Children: []*v1.Node{
			header,
			{Kind: v1.KindRow, Gap: 8, Children: actions},
		}}
}

// cardStatus is the DMS DeviceCard's status line: pairing states first, the
// empty string when the device is simply connected.
func cardStatus(dev *Device) string {
	switch {
	case dev.PairRequestedByPeer:
		return "Pairing requested"
	case dev.PairRequested:
		return "Pairing..."
	case !dev.Paired:
		return "Not paired"
	case !dev.Reachable:
		return "Offline"
	}
	return ""
}

func cardStatusTone(dev *Device) v1.Tone {
	if dev.PairRequestedByPeer || dev.PairRequested {
		return v1.ToneAccent
	}
	return v1.ToneSubtle
}

// deviceCardTree is the main device card: type icon, name, status, and the
// battery meter. Task 4 swaps the icon for the wire's image kind once the
// mockup assets land.
func deviceCardTree(dev *Device) *v1.Node {
	children := []*v1.Node{
		{Kind: v1.KindIcon, Icon: deviceIcon(dev), CenterX: true},
		{Kind: v1.KindText, Text: dev.Name, Size: "headline", Bold: true, CenterX: true},
		{Kind: v1.KindText, Text: deviceStatus(dev), Size: "caption", Tone: v1.ToneSubtle, CenterX: true},
	}
	if progress := batteryProgressNode(dev); progress != nil {
		children = append(children, progress)
	}
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 6,
		Children: children}
}

func batteryProgressNode(dev *Device) *v1.Node {
	if !dev.BatteryKnown || dev.BatteryCharge < 0 {
		return nil
	}
	value := float64(dev.BatteryCharge) / 100
	if value > 1 {
		value = 1
	}
	return &v1.Node{Key: "battery-progress", Kind: v1.KindProgress, Value: value,
		Width: 180, CenterX: true}
}

// actionRowTree is one row of capability-gated action buttons.
func actionRowTree(dev *Device, settings Settings) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
		actionButton("ring", "phone-in-talk", "Ring the device",
			dev.Reachable && hasPlugin(dev, "findmyphone")),
		actionButton("ping", "notifications-active", "Ping the device",
			dev.Reachable && hasPlugin(dev, "ping")),
		actionButton("browse", "folder-open", "Browse the device files",
			dev.Reachable && hasPlugin(dev, "sftp")),
		actionButton("clipboard", "content-paste", "Send the clipboard",
			dev.Reachable && hasPlugin(dev, "clipboard") && settings.EnableClipboard),
		actionButton("share", "share", "Share with the device",
			dev.Reachable && hasPlugin(dev, "share")),
		actionButton("sms", "sms", "Send a text message",
			dev.Reachable && hasPlugin(dev, "sms")),
	}}
}

func actionButton(id, icon, name string, enabled bool) *v1.Node {
	b := &v1.Node{Kind: v1.KindButton, ID: id, Icon: icon, Name: name, Role: "button",
		Events: []v1.EventKind{v1.EventActivate}}
	if !enabled {
		b.Disabled = true
	}
	return b
}

// infoRowsTree is the selected device's reading rows: battery, signal
// strength, network type, and the notification count.
func infoRowsTree(dev *Device) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8}
	if row := batteryRowNode(dev); row != nil {
		col.Children = append(col.Children, row)
	}
	if dev.NetworkKnown && dev.NetworkStrength >= 0 {
		col.Children = append(col.Children,
			infoRow("info-signal", networkStrengthIcon(dev.NetworkStrength), "Signal Strength", strengthLabel(dev.NetworkStrength), v1.ToneNormal))
	}
	if dev.NetworkKnown && dev.NetworkType != "" {
		col.Children = append(col.Children,
			infoRow("info-network", networkTypeIcon(dev.NetworkType), "Network Type", networkTypeLabel(dev.NetworkType), v1.ToneNormal))
	}
	if row := notificationRowNode(dev); row != nil {
		col.Children = append(col.Children, row)
	}
	return col
}

func batteryRowNode(dev *Device) *v1.Node {
	if !dev.BatteryKnown || dev.BatteryCharge < 0 {
		return nil
	}
	tone := v1.ToneNormal
	if dev.BatteryCharging {
		tone = v1.ToneAccent
	}
	return infoRow("info-battery", batteryIconName(dev), "Battery",
		fmt.Sprintf("%d%%", dev.BatteryCharge), tone)
}

func notificationRowNode(dev *Device) *v1.Node {
	// The reference row renders unconditionally with a zero default, even
	// when the count is unknown.
	return infoRow("info-notifications", "notifications", "Notifications",
		strconv.Itoa(dev.NotificationCount), v1.ToneNormal)
}

// infoRow is one reading row: the leading icon beside a stacked label over
// value, the DMS InfoRow shape, keyed so a reading delta can patch it.
func infoRow(key, icon, label, value string, tone v1.Tone) *v1.Node {
	return &v1.Node{Key: key, Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: icon},
		{Kind: v1.KindColumn, Gap: 1, Children: []*v1.Node{
			{Kind: v1.KindText, Text: label, Size: "caption", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: value, Tone: tone},
		}},
	}}
}

// batteryIconName follows the reference shell's breakpoints: charging icons
// at 90/60/40/20, level icons at 95/80/65/50/35/20/10, and the critical
// glyph below ten.
func batteryIconName(dev *Device) string {
	charge := dev.BatteryCharge
	if charge < 0 {
		charge = 0
	}
	if charge > 100 {
		charge = 100
	}
	if dev.BatteryCharging {
		switch {
		case charge >= 90:
			return "battery-charging-6"
		case charge >= 60:
			return "battery-charging-4"
		case charge >= 40:
			return "battery-charging-3"
		case charge >= 20:
			return "battery-charging-2"
		}
		return "battery-charging-1"
	}
	switch {
	case charge < 10:
		return "battery-critical"
	case charge >= 95:
		return "battery-6"
	case charge >= 80:
		return "battery-5"
	case charge >= 65:
		return "battery-4"
	case charge >= 50:
		return "battery-3"
	case charge >= 35:
		return "battery-2"
	case charge >= 20:
		return "battery-1"
	}
	return "battery-0"
}

// networkStrengthIcon picks the signal bar for the reported strength.
func networkStrengthIcon(strength int) string {
	switch {
	case strength >= 4:
		return "signal-cellular-4-bar"
	case strength == 3:
		return "signal-cellular-3-bar"
	case strength == 2:
		return "signal-cellular-2-bar"
	case strength == 1:
		return "signal-cellular-1-bar"
	}
	return "signal-cellular-null"
}

// networkTypeIcon picks the generation glyph for the raw network type; an
// empty type reads as no signal, the reference shell's nodata fallback.
func networkTypeIcon(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "signal-cellular-null"
	}
	switch networkTypeLabel(raw) {
	case "5G":
		return "5g"
	case "LTE", "LTE+":
		return "4g-mobiledata"
	case "3G":
		return "3g-mobiledata"
	case "2G":
		return "g-mobiledata"
	}
	return "signal-cellular-4-bar"
}

// strengthLabel reads like the reference shell's connectivity labels.
func strengthLabel(strength int) string {
	switch {
	case strength >= 4:
		return "Excellent"
	case strength == 3:
		return "Good"
	case strength == 2:
		return "Fair"
	case strength == 1:
		return "Weak"
	}
	return "No Signal"
}

// networkTypeLabel maps the daemon's raw cellular network type onto the
// reference shell's friendly labels, capitalising whatever is unknown.
func networkTypeLabel(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "N/A"
	}
	switch strings.ToUpper(trimmed) {
	case "NR", "5G", "5G_NR", "5G NR":
		return "5G"
	case "LTE", "4G":
		return "LTE"
	case "LTE_CA", "LTE+", "4G+", "4G_CA":
		return "LTE+"
	case "HSPA", "HSDPA", "HSUPA", "HSPAP", "UMTS", "WCDMA", "3G":
		return "3G"
	case "EDGE", "GPRS", "GSM", "2G":
		return "2G"
	}
	return strings.ToUpper(trimmed[:1]) + trimmed[1:]
}

func deviceStatus(dev *Device) string {
	switch {
	case !dev.Paired:
		return "not paired"
	case dev.Reachable:
		return "connected"
	}
	return "offline"
}

// PanelDelta reports the keyed subtree replacements that bring an open
// panel up to date when only the selected device's live readings moved. A
// nil result means the panel needs a full snapshot instead.
func PanelDelta(prev, next Snapshot) []v1.Replacement {
	if !samePanelStructure(prev, next) {
		return nil
	}
	was, now := selectedDevice(prev), selectedDevice(next)
	if was == nil || now == nil {
		return nil
	}
	var out []v1.Replacement
	if was.BatteryCharge != now.BatteryCharge || was.BatteryCharging != now.BatteryCharging {
		if node := batteryProgressNode(now); node != nil {
			out = append(out, v1.Replacement{Key: "battery-progress", Node: node})
		}
		if node := batteryRowNode(now); node != nil {
			out = append(out, v1.Replacement{Key: "info-battery", Node: node})
		}
	}
	if was.NotificationCount != now.NotificationCount {
		if node := notificationRowNode(now); node != nil {
			out = append(out, v1.Replacement{Key: "info-notifications", Node: node})
		}
	}
	return out
}

// samePanelStructure reports whether both snapshots render the same panel
// shape: same availability, selection, device order, and pairing state,
// with every reading flag unchanged.
func samePanelStructure(prev, next Snapshot) bool {
	if prev.Available != next.Available || prev.AnnouncedName != next.AnnouncedName ||
		prev.SelectedID != next.SelectedID || len(prev.Devices) != len(next.Devices) {
		return false
	}
	for i := range prev.Devices {
		a, b := &prev.Devices[i], &next.Devices[i]
		if a.ID != b.ID || a.Name != b.Name || a.Type != b.Type ||
			a.Reachable != b.Reachable || a.Paired != b.Paired ||
			a.PairRequested != b.PairRequested || a.PairRequestedByPeer != b.PairRequestedByPeer ||
			a.VerificationKey != b.VerificationKey ||
			a.BatteryKnown != b.BatteryKnown || a.NetworkKnown != b.NetworkKnown ||
			a.NetworkType != b.NetworkType || a.NetworkStrength != b.NetworkStrength ||
			a.NotificationsKnown != b.NotificationsKnown ||
			!stringSlicesEqual(a.SupportedPlugins, b.SupportedPlugins) {
			return false
		}
	}
	return true
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
