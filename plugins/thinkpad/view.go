package thinkpad

import (
	"fmt"
	"os"
	"path/filepath"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Panel geometry. Every explicit Height here already includes the padding the
// node carries, because the host measures content as Height - 2*Padding. The
// numbers are derived, not guessed: see PanelPreferredHeight and the
// TestPanelGeometry test, which re-adds them and compares.
// PanelWidth is the panel width the manifest declares. It is exported so
// cmd/sysc-thinkpad-screenshot renders pictures at the real size instead of
// guessing at one.
const PanelWidth = 758

const (
	panelWidth = PanelWidth

	// panelHeight is what the manifest declares. PanelPreferredHeight must
	// return exactly this for the seeded three-profile layout.
	panelHeight = 564

	panelInset = 22
	panelGap   = 12

	heroHeight = 92
	heroPad    = 16
	heroGap    = 12

	// The hero's three text slots are sized from the longest sentence each one
	// can be asked to render, not from a round number. Painting the states
	// showed the headline clipping on "Fan unmanaged - 4432 RPM" at 200px: the
	// shell's own width model under-measures a label-sized run, so the columns
	// carry deliberate slack instead of sitting exactly on the estimate.
	heroLogoW  = 122
	heroLogoH  = 44
	heroTextW  = 248
	heroSideW  = 192
	heroButton = 28

	// columnWidth is (panelWidth - 2*panelInset - panelGap) / 2: the two
	// columns share the panel's content box, not the panel itself.
	columnWidth = (panelWidth - 2*panelInset - panelGap) / 2
	cardPad     = 12
	cardGap     = 8

	fanCardHeight      = 168
	keyboardCardHeight = 148
	lidCardHeight      = 96

	setupCardHeight = 96
	storeCardHeight = 96

	headerRowHeight = 28
	levelRowHeight  = 26
	fanValueW       = 64
	fanModeButtonW  = (columnWidth - 2*cardPad - cardGap) / 2
	toggleW         = 72
	keyboardRowH    = 30
	actionRowHeight = 22

	profileHeaderRow = 28
	profileRowHeight = 42
	profilePad       = 8
	profileGap       = 6
	profileInputH    = 30
	profileMaxRows   = 6
	profileNameW     = 176
	profileApplyW    = 76
	profileActionW   = 80
	profileDeleteW   = 32

	// settingsPanelHeader mirrors the aiusage settings chrome: the host owns
	// the card, the scroll and the validated form, so the plugin only draws
	// the bar above it.
	settingsHeaderRow = 36
	settingsBackW     = 32
	settingsIcon      = 20
	settingsTitleW    = 320
	settingsCloseW    = 72
)

// Fills, sizes and shapes are plain strings on the wire; the shell validates
// them against its own catalogue. Naming them once keeps the builders readable.
const (
	fillCard    = "card"
	fillSoft    = "soft"
	fillAccent  = "accent"
	fillChip    = "chip"
	fillErrored = "error-container"

	sizeLabel   = "label"
	sizeCaption = "caption"
	sizeTitle   = "title"
	sizeMono    = "mono"

	shapeCard = "card"
)

// Node ids the process loop dispatches on.
const (
	nodeSettings    = "settings"
	nodeLidToggle   = "lid"
	nodeProfileSave = "profile:save"
	nodeProfileRen  = "profile:rename"
	nodeSetupCopy   = "setup:copy"
	nodeRecheck     = "recheck"
	nodeOpen        = "open"

	prefixFan      = "fan:"
	prefixKeyboard = "kbd:"
	prefixApply    = "profile:apply:"
	prefixDelete   = "profile:delete:"
	prefixPick     = "profile:pick:"
)

// cardContent is the drawable width inside a card of the standard column width.
func cardContent() int { return columnWidth - 2*cardPad }

// LogoPath resolves the wordmark for the panel and bar: the user's override
// first, then the asset shipped next to the binary. An empty result means fall
// back to the material laptop glyph.
func LogoPath(cfg Config, exeDir string) string {
	candidates := make([]string, 0, 2)
	if cfg.LogoPath != "" {
		candidates = append(candidates, cfg.LogoPath)
	}
	if exeDir == "" {
		if exe, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(exe)
		}
	}
	if exeDir != "" {
		candidates = append(candidates, filepath.Join(exeDir, "..", "assets", "thinkpad.png"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Size() > 0 {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs
			}
			return candidate
		}
	}
	return ""
}

func activate() []v1.EventKind { return []v1.EventKind{v1.EventActivate} }

func iconNode(id, name, glyph string, size int, tone v1.Tone) *v1.Node {
	return &v1.Node{Kind: v1.KindIcon, ID: id, Name: name, Icon: glyph, IconSize: size, Tone: tone}
}

func textNode(role, value string, size string, tone v1.Tone, width int) *v1.Node {
	node := &v1.Node{Kind: v1.KindText, Key: role, Text: value, Size: size, Tone: tone, Tabular: true}
	if width > 0 {
		node.Width = width
	}
	return node
}

func caption(role, value string, width int) *v1.Node {
	return textNode(role, value, sizeCaption, v1.ToneSubtle, width)
}

func button(id, name, label string, width, height, pad int, fill string) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Name: name, Text: label,
		Width: width, Height: height, Padding: pad, Fill: fill,
		Role: "button", Events: activate(),
	}
}

// iconButton builds an icon-only control. A button must not set IconSize: the
// host sizes a button's glyph itself, and the linter rejects the combination.
func iconButton(id, name, glyph string, width, height, pad int) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Name: name, Icon: glyph,
		Width: width, Height: height, Padding: pad,
		Role: "button", Events: activate(),
	}
}

func card(height, pad, gap int, children ...*v1.Node) *v1.Node {
	return &v1.Node{
		Kind: v1.KindColumn, Fill: fillCard, Shape: shapeCard,
		Height: height, Padding: pad, Gap: gap, Children: children,
	}
}

// toneFor picks between two theme roles. Tones and fills are different
// vocabularies, so an icon needs its own picker rather than a shared one.
func toneFor(on bool, whenOn, whenOff v1.Tone) v1.Tone {
	if on {
		return whenOn
	}
	return whenOff
}

func fillFor(enabled bool, on string, off string) string {
	if enabled {
		return on
	}
	return off
}

func thermalLabel(snap Snapshot, cfg Config) string {
	if !snap.State.CPUTempValid {
		// Short on purpose: this sits in the hero's narrow side column, and
		// the longer "CPU temperature unavailable" was clipped mid-word.
		return "CPU unavailable"
	}
	label := fmt.Sprintf("CPU %.0f °C", snap.State.CPUTempC)
	if cfg.WarnThermal && snap.State.Hot(cfg.ThermalLimitC) {
		return label + " · hot"
	}
	return label
}

func accessLabel(snap Snapshot) string {
	support := snap.State.Support()
	switch {
	case !snap.State.Any():
		return "No ThinkPad hardware found"
	case !support.Writable:
		return "Setup needed for some controls"
	default:
		return "Controls ready"
	}
}

// hero is the header: the wordmark, a one-line reading of the current state and
// the entry point to the settings panel.
func hero(snap Snapshot, cfg Config, logo string) *v1.Node {
	var mark *v1.Node
	if logo != "" {
		mark = &v1.Node{
			Kind: v1.KindImage, Path: logo, Name: "ThinkPad",
			Role: "img", ImageW: heroLogoW, ImageH: heroLogoH,
		}
	} else {
		// No decodable asset: the glyph keeps the header's height rather than
		// leaving a hole, and the surrounding text still names the machine.
		mark = &v1.Node{
			Kind: v1.KindIcon, ID: "", Name: "ThinkPad", Icon: "laptop",
			IconSize: 40, Width: heroLogoH, Height: heroLogoH,
		}
	}

	state := columnNode(cardGap, 0, 0,
		textNode("hero-state", liveLine(snap), sizeLabel, "", 0),
		caption("hero-detail", detailLine(snap), 0),
	)
	side := columnNode(cardGap, 0, 0,
		textNode("hero-thermal", thermalLabel(snap, cfg), sizeLabel, v1.ToneNormal, 0),
		caption("hero-access", accessLabel(snap), 0),
	)

	return card(heroHeight, heroPad, heroGap, &v1.Node{
		Kind: v1.KindRow, Height: heroHeight - 2*heroPad, Gap: heroGap,
		Children: []*v1.Node{
			mark,
			fixedColumn(heroTextW, state),
			fixedColumn(heroSideW, side),
			iconButton(nodeSettings, "Open ThinkPad settings", "settings", heroButton, heroButton, 4),
		},
	})
}

// liveLine and detailLine are the two hero sentences.
func liveLine(snap Snapshot) string {
	if !snap.State.HaveFan {
		return "Fan unavailable"
	}
	if !snap.State.FanValid {
		return "Fan reading unavailable"
	}
	level := "unmanaged"
	if snap.State.FanLevelValid {
		level = FanLevelLabel(snap.State.FanLevel)
	}
	return fmt.Sprintf("Fan %s · %d RPM", level, snap.State.FanRPM)
}

func detailLine(snap Snapshot) string {
	return fmt.Sprintf("Backlight %s · lid light %s", backlightLabel(snap), lidLabel(snap))
}

func backlightLabel(snap Snapshot) string {
	if !snap.State.Keyboard.Present {
		return "unavailable"
	}
	if !snap.State.Keyboard.On() {
		return "off"
	}
	return fmt.Sprintf("%d of %d", snap.State.Keyboard.Brightness, snap.State.Keyboard.MaxBrightness)
}

func lidLabel(snap Snapshot) string {
	if !snap.State.Lid.Present {
		return "unavailable"
	}
	if snap.State.Lid.On() {
		return "on"
	}
	return "off"
}

func columnNode(gap, width, height int, children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: gap, Width: width, Height: height, Children: children}
}

// fixedColumn gives a text stack a hard width so the hero row cannot be pushed
// past the panel by a long state line.
func fixedColumn(width int, child *v1.Node) *v1.Node {
	return &v1.Node{
		Kind: v1.KindColumn, Width: width, Gap: cardGap,
		Height: 16 + cardGap + 16, Children: child.Children,
	}
}

func fanCard(snap Snapshot) *v1.Node {
	enabled := snap.State.HaveFan && snap.State.FanWritable && snap.State.FanLevelValid

	rows := []*v1.Node{
		fanHeader(snap, enabled),
		fanSliderRow(snap, enabled),
		fanModeRow(snap, enabled),
		&v1.Node{
			Kind: v1.KindRow, Height: actionRowHeight,
			Children: []*v1.Node{caption("fan-note", fanNote(snap, enabled), cardContent())},
		},
	}
	return card(fanCardHeight, cardPad, cardGap, rows...)
}

func fanHeader(snap Snapshot, enabled bool) *v1.Node {
	// The card is the fan's level control, so it says so. An earlier draft
	// titled it "Thermal", which only made sense when this card showed a
	// temperature reading - the CPU temperature has its own hero line.
	title := "Fan"
	if !snap.State.HaveFan {
		title = "No fan interface"
	}
	return &v1.Node{
		Kind: v1.KindRow, Height: headerRowHeight, Gap: cardGap,
		Children: []*v1.Node{
			iconNode("", "Fan", "speed", 18, fanTone(snap, enabled)),
			columnNode(1, cardContent()-(cardGap+18), headerRowHeight,
				textNode("fan-title", title, sizeLabel, "", 0),
				caption("fan-level", fanLevelCaption(snap), 0),
			),
		},
	}
}

func fanTone(snap Snapshot, enabled bool) v1.Tone {
	if !snap.State.HaveFan {
		return v1.ToneSubtle
	}
	if enabled && snap.State.FanLevel == FanLevelDisengaged {
		return v1.ToneError
	}
	if enabled && numericLow(snap.State.FanLevel) {
		return v1.ToneError
	}
	return ""
}

func numericLow(level string) bool {
	n := FanLevelNumber(level)
	return n >= 0 && n <= 2
}

func fanLevelCaption(snap Snapshot) string {
	if !snap.State.HaveFan {
		return "thinkpad_acpi is not loaded"
	}
	if snap.State.FanStatus == "disabled" {
		return "disabled"
	}
	if !snap.State.FanLevelValid {
		return "no fan_control=1, read only"
	}
	if snap.State.FanWritable {
		return "writable"
	}
	return "read only"
}

func fanNote(snap Snapshot, enabled bool) string {
	switch {
	case !snap.State.HaveFan:
		return "Load thinkpad_acpi to manage the fan."
	case snap.State.FanStatus == "disabled":
		return "Fan is disabled; levels apply after it is re-enabled."
	case !snap.State.FanLevelValid:
		return "Needs fan_control=1 in the driver options."
	case !enabled:
		return "Needs write access to change the level."
	case !snap.State.FanValid:
		return "Fan speed unavailable."
	case snap.State.FanLevel == FanLevelDisengaged:
		return fmt.Sprintf("Full speed · %d RPM", snap.State.FanRPM)
	case numericLow(snap.State.FanLevel):
		return fmt.Sprintf("Low levels can run hot · %d RPM", snap.State.FanRPM)
	default:
		return fmt.Sprintf("%d RPM · the firmware raises it.", snap.State.FanRPM)
	}
}

func fanSliderRow(snap Snapshot, enabled bool) *v1.Node {
	value := FanLevelNumber(snap.State.FanLevel)
	if value < 0 {
		value = 0 // Auto and Full are explicit modes beside the manual level slider.
	}
	return &v1.Node{
		Kind: v1.KindRow, Height: levelRowHeight, Gap: cardGap,
		Children: []*v1.Node{
			{
				Kind: v1.KindSlider, ID: "fan-slider", Name: "Fan level", Role: "slider",
				Min: 0, Max: MaxNumericFanLevel, Step: 1, Value: float64(value),
				Width: cardContent() - cardGap - fanValueW, Height: levelRowHeight,
				Disabled: !enabled, Events: []v1.EventKind{v1.EventChange},
			},
			caption("fan-slider-value", fanLevelValue(snap.State.FanLevel), fanValueW),
		},
	}
}

func fanLevelValue(level string) string {
	if n := FanLevelNumber(level); n >= 0 {
		return fmt.Sprintf("Level %d", n)
	}
	if label := FanLevelLabel(level); label != "—" {
		return label
	}
	return "—"
}

func fanModeRow(snap Snapshot, enabled bool) *v1.Node {
	level := NormalizeFanLevel(snap.State.FanLevel)
	auto := button(prefixFan+FanLevelAuto, "Let firmware control the fan", "Auto",
		fanModeButtonW, levelRowHeight, 6, fillFor(level == FanLevelAuto, fillAccent, fillSoft))
	full := button(prefixFan+FanLevelDisengaged, "Set the fan to full speed", "Full",
		fanModeButtonW, levelRowHeight, 6, fillFor(level == FanLevelDisengaged, fillAccent, fillSoft))
	auto.Disabled, full.Disabled = !enabled, !enabled
	return &v1.Node{
		Kind: v1.KindRow, Height: levelRowHeight, Gap: cardGap,
		Children: []*v1.Node{auto, full},
	}
}

func keyboardCard(snap Snapshot) *v1.Node {
	led := snap.State.Keyboard
	enabled := led.Present && led.Writable
	maxLevel := keyboardSliderMax(led)

	rows := []*v1.Node{
		keyboardHeader(snap, enabled),
	}
	if maxLevel > 0 {
		rows = append(rows, &v1.Node{
			Kind: v1.KindRow, Height: keyboardRowH,
			Children: []*v1.Node{{
				Kind: v1.KindSlider, ID: "kbd-slider", Name: "Keyboard brightness", Role: "slider",
				Min: 0, Max: float64(maxLevel), Step: 1,
				Value: float64(clampInt(led.Brightness, 0, maxLevel)),
				Width: cardContent(), Height: keyboardRowH,
				Disabled: !enabled, Events: []v1.EventKind{v1.EventChange},
			}},
		})
	}
	rows = append(rows, &v1.Node{
		Kind: v1.KindRow, Height: actionRowHeight,
		Children: []*v1.Node{caption("kbd-note", keyboardNote(snap, enabled), cardContent())},
	})
	return card(keyboardCardHeightFor(snap), cardPad, cardGap, rows...)
}

func keyboardHeader(snap Snapshot, enabled bool) *v1.Node {
	led := snap.State.Keyboard
	title := "Keyboard backlight"
	if !led.Present {
		title = "No keyboard backlight"
	}
	fill := fillSoft
	label := "Off"
	if !led.Present {
		// "Off" would read as a switchable state; there is nothing to switch.
		label = "None"
	} else if led.On() {
		fill = fillAccent
		label = "On"
	}
	toggle := button(prefixKeyboard+"toggle", "Toggle the keyboard backlight", label,
		72, headerRowHeight, 6, fill)
	toggle.Disabled = !enabled
	return &v1.Node{
		Kind: v1.KindRow, Height: headerRowHeight, Gap: cardGap,
		Children: []*v1.Node{
			iconNode("", "Keyboard", "keyboard", 18, toneFor(led.On(), v1.ToneAccent, v1.ToneSubtle)),
			columnNode(1, cardContent()-cardGap-18-2*cardGap-toggleW, headerRowHeight,
				textNode("kbd-title", title, sizeLabel, "", 0),
				caption("kbd-detail", ledDetail(led), 0),
			),
			toggle,
		},
	}
}

// keyboardSliderMax follows the LED's advertised range. Old systems that omit
// max_brightness get the kernel's smallest usable range, capped to what profiles
// can persist.
func keyboardSliderMax(led LEDState) int {
	if !led.Present {
		return 0
	}
	maxLevel := led.MaxBrightness
	if maxLevel < 1 {
		maxLevel = 1
	}
	return min(maxLevel, MaxKeyboardLevel)
}

// keyboardCardHeightFor is the keyboard card's height in this state. Without a
// backlight there is no grid, so the card drops a row rather than showing an
// empty band where the controls would be.
func keyboardCardHeightFor(snap Snapshot) int {
	if keyboardSliderMax(snap.State.Keyboard) == 0 {
		return keyboardCardHeight - keyboardRowH - cardGap
	}
	return keyboardCardHeight
}

func keyboardNote(snap Snapshot, enabled bool) string {
	led := snap.State.Keyboard
	switch {
	case !led.Present:
		return "This model has no backlight."
	case snap.ExternalKeyboard:
		return "A hardware key changed the backlight."
	case !enabled:
		return "Needs write access to change it."
	case led.On() && led.MaxBrightness > 1:
		return fmt.Sprintf("Level %d of %d · may dim on timeout.", led.Brightness, led.MaxBrightness)
	default:
		return "Level " + fmt.Sprintf("%d", led.Brightness) + "."
	}
}

func lidCard(snap Snapshot) *v1.Node {
	led := snap.State.Lid
	enabled := led.Present && led.Writable

	fill := fillSoft
	label := "Off"
	if !led.Present {
		label = "None" // matches the keyboard card: nothing to switch
	} else if led.On() {
		fill = fillAccent
		label = "On"
	}
	toggle := button(nodeLidToggle, "Toggle the lid light", label, 72, headerRowHeight, 6, fill)
	toggle.Disabled = !enabled

	return card(lidCardHeight, cardPad, cardGap,
		&v1.Node{
			Kind: v1.KindRow, Height: headerRowHeight, Gap: cardGap,
			Children: []*v1.Node{
				iconNode("", "Lid light", "brightness_high", 18, toneFor(led.On(), v1.ToneAccent, v1.ToneSubtle)),
				columnNode(1, cardContent()-cardGap-18-2*cardGap-toggleW, headerRowHeight,
					textNode("lid-title", "Lid light", sizeLabel, "", 0),
					caption("lid-detail", lidDetail(snap), 0),
				),
				toggle,
			},
		},
		&v1.Node{
			Kind: v1.KindRow, Height: actionRowHeight,
			Children: []*v1.Node{caption("lid-note", lidNote(snap, enabled), cardContent())},
		},
	)
}

// ledDetail is the sysfs node driving a light. The card title already says
// what the light is, so the line under it earns its space by naming the node -
// that is what a user needs when the control refuses to move and they go
// looking for the file to check permissions on.
func ledDetail(led LEDState) string {
	if !led.Present {
		return "not exposed by this model"
	}
	if led.Name == "" {
		return "tpacpi LED"
	}
	return led.Name
}

func lidDetail(snap Snapshot) string {
	if !snap.State.Lid.Present {
		return "not exposed by this model"
	}
	if snap.State.Lid.Name == "" {
		return "tpacpi lid LED"
	}
	return snap.State.Lid.Name
}

func lidNote(snap Snapshot, enabled bool) string {
	switch {
	case !snap.State.Lid.Present:
		return "This model has no lid dot."
	case snap.ExternalLid:
		return "A hardware key changed the lid light."
	case !enabled:
		return "Needs write access to switch it."
	default:
		return "Lit while the lid logo is on."
	}
}

// profileRowsHeight is the list height for n profiles, capped so a long list
// scrolls inside the card instead of stretching the panel without bound.
func profileRowsHeight(n int) int {
	if n <= 0 {
		return profileRowHeight
	}
	if n > profileMaxRows {
		n = profileMaxRows
	}
	return n*profileRowHeight + (n-1)*profileGap
}

func profileCardHeight(n int) int {
	return 2*cardPad + profileHeaderRow + cardGap + profileRowsHeight(n) + cardGap + profileInputH
}

func profileCard(snap Snapshot, selected string) *v1.Node {
	profiles := snap.Profiles

	children := make([]*v1.Node, 0, len(profiles))
	for _, profile := range profiles {
		children = append(children, profileRow(snap, profile, selected))
	}
	if len(children) == 0 {
		children = append(children, &v1.Node{
			Kind: v1.KindRow, Height: profileRowHeight,
			Children: []*v1.Node{caption("profile-empty", "No profiles saved yet.", profileNameW)},
		})
	}

	header := &v1.Node{
		Kind: v1.KindRow, Height: profileHeaderRow, Gap: cardGap,
		Children: []*v1.Node{
			iconNode("", "Profiles", "tune", 18, ""),
			columnNode(1, cardContent()-cardGap-18-(3*cardGap)-2*profileActionW, profileHeaderRow,
				textNode("profile-title", "Profiles", sizeLabel, "", 0),
				caption("profile-count", profileCountCaption(snap), 0),
			),
			button(nodeProfileRen, "Rename the selected profile", "Rename",
				profileActionW, profileHeaderRow, 6, fillSoft),
			button(nodeProfileSave, "Save the current controls as a profile",
				"Save", profileActionW, profileHeaderRow, 6, fillAccent),
		},
	}

	input := &v1.Node{
		Kind: v1.KindTextInput, ID: "profile-name", Key: "profile-name",
		Name: "New profile name", Role: "textbox", Placeholder: "New profile name",
		SubmitOnEnter: true, Height: profileInputH,
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit},
	}

	return card(profileCardHeight(len(profiles)), cardPad, cardGap,
		header,
		&v1.Node{
			Kind: v1.KindList, Key: "profiles", Height: profileRowsHeight(len(profiles)),
			Gap: profileGap, Children: children,
		},
		input,
	)
}

func profileCountCaption(snap Snapshot) string {
	count := len(snap.Profiles)
	if count == 1 {
		return "1 saved"
	}
	return fmt.Sprintf("%d saved", count)
}

func profileRow(snap Snapshot, profile Profile, selected string) *v1.Node {
	isActive := profile.Name == snap.Active
	isSelected := profile.Name == selected
	row := &v1.Node{
		Kind: v1.KindRow, Key: prefixPick + profile.Name,
		Height: profileRowHeight, Padding: profilePad, Gap: profileGap,
		Children: []*v1.Node{
			columnNode(1, profileNameW, 16,
				textNode("profile-name-"+profile.Name, profile.Name, sizeLabel, "", 0),
				caption("profile-body-"+profile.Name, profile.Settings.Summary(), 0),
			),
			button(prefixApply+profile.Name, "Apply the "+profile.Name+" profile", "Apply",
				profileApplyW, levelRowHeight, 6, fillFor(isActive, fillAccent, fillSoft)),
			iconButton(prefixDelete+profile.Name, "Delete the "+profile.Name+" profile",
				"delete", profileDeleteW, levelRowHeight, 6),
		},
	}
	switch {
	case isSelected:
		row.Fill = fillChip
		row.Stroke = 1
		row.StrokeFill = fillAccent
	case isActive:
		row.Fill = fillChip
	}
	row.Children[2].Disabled = len(snap.Profiles) <= 1
	return row
}

// setupCard is the honest answer to "nothing works": the exact commands, and a
// button that puts them on the clipboard.
// setupCard explains the refusal and, more importantly, offers the two actions
// that resolve it.
//
// An earlier draft printed a condensed copy of the setup script inline. That
// was wrong twice over: it went stale the moment the script grew its
// built-in-kernel branch, and the condensed form still contained the
// `modprobe -r` line that fails outright on a kernel with thinkpad_acpi
// compiled in. Telling someone to run a command you have already proven breaks
// on their machine is worse than telling them nothing, so the card names the
// problem, hands over the real script through the clipboard, and offers a
// re-check so they can see the fix land without restarting the shell.
func setupCard(snap Snapshot) *v1.Node {
	return &v1.Node{
		Kind: v1.KindColumn, Fill: fillErrored, Shape: shapeCard,
		Height: setupCardHeight, Padding: cardPad, Gap: cardGap,
		Children: []*v1.Node{
			textNode("setup-title", "Some controls need setup",
				sizeLabel, v1.ToneNormal, 0),
			textNode("setup-why", "A fan option or write permission is missing. "+
				"Copy the fix, run it in a terminal, then re-check.", sizeCaption, v1.ToneNormal, cardContent()),
			&v1.Node{
				Kind: v1.KindRow, Height: actionRowHeight, Gap: cardGap,
				Children: []*v1.Node{
					button(nodeSetupCopy, "Copy the setup script to the clipboard",
						"Copy fix", 120, actionRowHeight, 6, fillAccent),
					button(nodeRecheck, "Re-check whether the controls are ready",
						"Re-check", 120, actionRowHeight, 6, fillSoft),
				},
			},
		},
	}
}

func storeCard(snap Snapshot) *v1.Node {
	return &v1.Node{
		Kind: v1.KindColumn, Fill: fillErrored, Shape: shapeCard,
		Height: storeCardHeight, Padding: cardPad, Gap: 4,
		Children: []*v1.Node{
			textNode("store-title", "Profile store problem", sizeLabel, v1.ToneNormal, 0),
			textNode("store-body", snap.StoreIssue, sizeCaption, v1.ToneNormal, cardContent()),
			// The store refuses to overwrite a file it could not parse, so the
			// user's own profiles are still on disk. Say so: an error card that
			// only names the fault reads as "your profiles are gone".
			textNode("store-hint", "Left untouched — edit or delete it to start fresh.",
				sizeCaption, v1.ToneNormal, cardContent()),
		},
	}
}

// PanelPreferredHeight is what the plugin asks the host for. Recomputing the
// same arithmetic the builders use is the only way the request can stay honest
// when the profile count or the setup branch changes the stack.
func PanelPreferredHeight(snap Snapshot, selected string) int {
	total := 2*panelInset + heroHeight + panelGap
	left := fanCardHeight + panelGap + profileCardHeight(len(snap.Profiles))
	right := keyboardCardHeightFor(snap) + panelGap + lidCardHeight
	if left < right {
		total += right
	} else {
		total += left
	}
	if snap.State.NeedsSetup() {
		total += panelGap + setupCardHeight
	}
	if snap.StoreIssue != "" {
		total += panelGap + storeCardHeight
	}
	_ = selected // reserved for a future per-profile pane
	return total
}

func PanelTree(snap Snapshot, cfg Config, logo, selected string) *v1.Node {
	columns := &v1.Node{
		Kind: v1.KindRow, Gap: panelGap,
		Height: maxInt(fanCardHeight+panelGap+profileCardHeight(len(snap.Profiles)),
			keyboardCardHeightFor(snap)+panelGap+lidCardHeight),
		Children: []*v1.Node{
			columnNode(panelGap, columnWidth,
				fanCardHeight+panelGap+profileCardHeight(len(snap.Profiles)), fanCard(snap), profileCard(snap, selected)),
			columnNode(panelGap, columnWidth,
				keyboardCardHeightFor(snap)+panelGap+lidCardHeight, keyboardCard(snap), lidCard(snap)),
		},
	}

	children := []*v1.Node{
		hero(snap, cfg, logo),
		columns,
	}
	if snap.State.NeedsSetup() {
		children = append(children, setupCard(snap))
	}
	if snap.StoreIssue != "" {
		children = append(children, storeCard(snap))
	}
	return &v1.Node{
		Kind: v1.KindColumn, Padding: panelInset, Gap: panelGap,
		Children: children,
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SettingsPanelTree is the plugin-owned chrome above the host's settings form.
// The host wraps this tree in its own card, scroll and validated form, so the
// bar here is the whole of what the plugin owns on that surface.
func SettingsPanelTree() *v1.Node {
	return &v1.Node{
		Kind: v1.KindColumn,
		Children: []*v1.Node{
			card(settingsHeaderRow+2*cardPad, cardPad, cardGap, &v1.Node{
				Kind: v1.KindRow, Height: settingsHeaderRow, Gap: cardGap,
				Children: []*v1.Node{
					iconButton("back", "Back to the ThinkPad panel", "chevron_left",
						settingsBackW, settingsHeaderRow, 8),
					iconNode("", "Settings", "settings", settingsIcon, ""),
					columnNode(1, settingsTitleW, 32,
						textNode("settings-title", "ThinkPad settings", sizeTitle, "", 0),
						caption("settings-hint", "Settings save automatically.", 0),
					),
					button("close", "Close the settings panel", "Close",
						settingsCloseW, settingsHeaderRow, 8, fillSoft),
				},
			}),
		},
	}
}

// BarTree keeps the bar compact: the official wordmark is the only child and
// remains the button for opening the panel.
func BarTree(cfg Config) *v1.Node {
	return &v1.Node{
		Kind: v1.KindRow, Height: 32, Padding: 4,
		Children: []*v1.Node{barLogoButton(LogoPath(cfg, ""))},
	}
}

func barLogoButton(logo string) *v1.Node {
	button := &v1.Node{
		Kind: v1.KindButton, ID: nodeOpen, Name: "Open the ThinkPad panel",
		Width: 24, Height: 24, Padding: 4,
		Role: "button", Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}
	if logo == "" {
		button.Icon = "laptop"
		return button
	}
	button.Width, button.Padding = 52, 4
	button.Children = []*v1.Node{{
		Kind: v1.KindImage, Path: logo, Name: "ThinkPad", Role: "img",
		ImageW: 44, ImageH: 16,
	}}
	return button
}

// TooltipTree is text-only on purpose: the shell refuses interactive nodes in a
// tooltip view.
func TooltipTree(snap Snapshot, cfg Config) *v1.Node {
	lines := []string{
		"ThinkPad",
		liveLine(snap),
		detailLine(snap),
		thermalLabel(snap, cfg),
		accessLabel(snap),
		fmt.Sprintf("Profiles: %s", profileCountCaption(snap)),
	}
	if snap.Busy {
		lines = append(lines, "Applying…")
	}
	if snap.State.FanLevel == FanLevelDisengaged {
		lines = append(lines, "Fan is disengaged (full speed).")
	}
	if snap.State.NeedsSetup() {
		lines = append(lines, "Open the panel for setup steps.")
	}

	children := make([]*v1.Node, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			children = append(children, textNode("tip-title", line, sizeTitle, "", 0))
			continue
		}
		children = append(children, textNode(fmt.Sprintf("tip-%d", i), line, sizeCaption, v1.ToneSubtle, 0))
	}
	return &v1.Node{Kind: v1.KindColumn, Padding: 10, Gap: 3, Children: children}
}
