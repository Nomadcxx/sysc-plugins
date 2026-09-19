package kdeconnect

import (
	"fmt"
	"strconv"

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
// unreachable, the phone glyph with the battery percent once a device is
// known. The whole control opens the panel.
func BarTree(snap Snapshot) *v1.Node {
	icon, label := "phonelink-off", "N/A"
	if snap.Available {
		icon, label = "smartphone", ""
		if dev := selectedDevice(snap); dev != nil && dev.BatteryKnown && dev.BatteryCharge >= 0 {
			label = fmt.Sprintf("%d%%", dev.BatteryCharge)
		}
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Icon: icon, Text: label,
		Name: "Open phone connect", Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
	}}}
}

// TooltipTree names the backend and the selected device's state.
func TooltipTree(snap Snapshot) *v1.Node {
	detail := "unavailable"
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
	}
	return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Phone Connect"},
		{Kind: v1.KindText, Text: detail, Size: "caption", Tone: v1.ToneSubtle},
	}}
}

// Composer names the composer card the panel shows under the actions. The
// entry point owns the state; the view only renders it.
type Composer uint8

const (
	ComposerNone Composer = iota
	ComposerShare
	ComposerSMS
)

// PanelTree is the phone-connect panel: the daemon header over the state,
// pairing, switcher, device, action, info, and composer sections.
func PanelTree(snap Snapshot, settings Settings, composer Composer) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 10, Children: []*v1.Node{headerTree(snap)}}
	if !snap.Available {
		col.Children = append(col.Children, stateCard(
			"KDE Connect daemon unreachable",
			"Install and start kdeconnectd, then refresh."))
		return col
	}
	if len(snap.Devices) == 0 {
		col.Children = append(col.Children, stateCard(
			"No devices",
			"Pair this desktop from the KDE Connect app on your phone."))
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
			col.Children = append(col.Children, shareComposerTree())
		case ComposerSMS:
			col.Children = append(col.Children, smsComposerTree())
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

// shareComposerTree is the share card: one URL-or-text field with its two
// sends, and one file-path field, the DMS ShareDialog's contents.
func shareComposerTree() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 8,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Share", Bold: true},
			{Kind: v1.KindTextInput, ID: "share-text", Name: "URL or text to share", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindButton, ID: "share-url-send", Text: "Send URL", Fill: "accent",
					Name: "Share the URL with the device", Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
				{Kind: v1.KindButton, ID: "share-text-send", Text: "Send text",
					Name: "Share the text with the device", Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
			}},
			{Kind: v1.KindTextInput, ID: "share-file", Name: "File path to send", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindButton, ID: "share-file-send", Text: "Send file",
				Name: "Send the file to the device", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}}
}

// smsComposerTree is the SMS card: number, multiline body, send, and the
// launch-app escape hatch, the DMS SmsDialog's contents.
func smsComposerTree() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Padding: 14, Gap: 8,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: "New message", Bold: true},
			{Kind: v1.KindTextInput, ID: "sms-number", Name: "Phone number", Role: "textbox",
				Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindTextInput, ID: "sms-body", Name: "Message", Role: "textbox",
				Multiline: true,
				Events:    []v1.EventKind{v1.EventChange}},
			{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindButton, ID: "sms-send", Text: "Send", Fill: "accent",
					Name: "Send the message", Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
				{Kind: v1.KindButton, ID: "sms-app", Text: "Open app",
					Name: "Open the SMS app on the device", Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
			}},
		}}
}

// pairingCard covers an incoming pairing request (verification key plus
// accept and reject) and an outgoing one (waiting plus cancel).
func pairingCard(dev *Device) *v1.Node {
	hint := "Pairing request sent. Accept it on the device."
	actions := []*v1.Node{{
		Kind: v1.KindButton, ID: "pair-cancel", Text: "Cancel", Fill: "error-container",
		Name: "Cancel the pairing request", Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
	}}
	if dev.PairRequestedByPeer {
		hint = "Accept the pairing request."
		if dev.VerificationKey != "" {
			hint = "Verification: " + dev.VerificationKey
		}
		actions = []*v1.Node{
			{Kind: v1.KindButton, ID: "pair-accept", Text: "Accept", Fill: "accent",
				Name: "Accept the pairing request", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "pair-reject", Text: "Reject", Fill: "error-container",
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
			{Kind: v1.KindButton, ID: "pair", Text: "Request pairing", Fill: "accent",
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
		col.Children = append(col.Children, &v1.Node{
			Kind: v1.KindRow, Fill: "card", Radius: 10, Padding: 10, Gap: 10,
			PinEnd: true, Children: []*v1.Node{
				{Kind: v1.KindColumn, Children: []*v1.Node{
					{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
						{Kind: v1.KindIcon, Icon: deviceIcon(dev)},
						{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
							{Kind: v1.KindText, Text: dev.Name, Bold: true},
							{Kind: v1.KindText, Text: deviceStatus(dev), Size: "caption", Tone: v1.ToneSubtle},
						}},
					}},
				}},
				{Kind: v1.KindButton, ID: "select-" + dev.ID, Text: "Use",
					Name: "Switch to " + dev.Name, Role: "button",
					Events: []v1.EventKind{v1.EventActivate}},
			}})
	}
	return col
}

// deviceCardTree is the main device card: type icon, name, status, and the
// battery meter. The wire has no image kind, so the DMS phone mockup is a
// card with the device glyph — the honest approximation.
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
			infoRow("info-signal", "network", "Signal Strength", strengthLabel(dev.NetworkStrength), v1.ToneNormal))
	}
	if dev.NetworkKnown && dev.NetworkType != "" {
		col.Children = append(col.Children,
			infoRow("info-network", "network", "Network Type", dev.NetworkType, v1.ToneNormal))
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
	if !dev.NotificationsKnown {
		return nil
	}
	return infoRow("info-notifications", "notifications", "Notifications",
		strconv.Itoa(dev.NotificationCount), v1.ToneNormal)
}

// infoRow is one reading row: the icon and label leading, the value pinned
// right, keyed so a reading delta can patch it.
func infoRow(key, icon, label, value string, tone v1.Tone) *v1.Node {
	return &v1.Node{Key: key, Kind: v1.KindRow, Gap: 10, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon},
			{Kind: v1.KindText, Text: label},
		}},
		{Kind: v1.KindText, Text: value, Tone: tone},
	}}
}

// batteryIconName picks the level glyph, the charging variant when the
// battery is charging, and the critical glyph at the bottom of the range.
func batteryIconName(dev *Device) string {
	charge := dev.BatteryCharge
	if charge < 0 {
		charge = 0
	}
	if charge > 100 {
		charge = 100
	}
	if charge <= 5 && !dev.BatteryCharging {
		return "battery-critical"
	}
	level := charge * 7 / 100
	if level > 6 {
		level = 6
	}
	if dev.BatteryCharging {
		return "battery-charging-" + strconv.Itoa(level)
	}
	return "battery-" + strconv.Itoa(level)
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
	return "None"
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
