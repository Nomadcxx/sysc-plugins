package updates

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarTree renders the bar: a download glyph, the pending count, and a dot
// when a reboot is waiting.
func BarTree(state State, hideWhenZero bool) *v1.Node {
	repo, aur, flatpak := Counts(state.Updates)
	total := repo + aur + flatpak
	if hideWhenZero && total == 0 && !state.RebootNeeded && !state.RepoMissing {
		return &v1.Node{Kind: v1.KindRow}
	}
	children := []*v1.Node{{
		Kind:   v1.KindButton,
		ID:     "open",
		Name:   "Open system updates",
		Role:   "button",
		Icon:   "download",
		Events: []v1.EventKind{v1.EventActivate},
	}}
	switch {
	case state.RepoMissing:
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "!", Tone: v1.ToneError})
	case total > 0:
		text := strconv.Itoa(total)
		if total > 99 {
			text = "99+"
		}
		children = append(children, &v1.Node{Kind: v1.KindText, Text: text})
	}
	if state.RebootNeeded {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "●", Tone: v1.ToneAccent})
	}
	return &v1.Node{Kind: v1.KindRow, Children: children}
}

// TooltipTree renders the bar tooltip.
func TooltipTree(state State, now time.Time) *v1.Node {
	repo, aur, flatpak := Counts(state.Updates)
	total := repo + aur + flatpak
	root := &v1.Node{Kind: v1.KindColumn, Gap: 2}
	if total == 0 {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: "No updates"})
	} else {
		summary := fmt.Sprintf("%d updates (%d repo, %d AUR, %d Flatpak)", total, repo, aur, flatpak)
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: compactDisplayText(summary, 32)})
	}
	if !state.CheckedAt.IsZero() {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: "checked " + state.CheckedAt.Format("15:04"), Tone: v1.ToneSubtle})
	}
	if state.RebootNeeded {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: compactDisplayText("reboot needed ("+state.RebootDetail+")", 32), Tone: v1.ToneAccent})
	}
	if state.CheckErr != "" {
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: compactDisplayText("Couldn't check: "+state.CheckErr, 32), Tone: v1.ToneError})
	}
	return root
}

// PanelTree renders the panel. terminalHint carries a one-line problem from
// the last attempt to start an update (no terminal, pkexec failure).
func PanelTree(state State, now time.Time, terminalHint string) *v1.Node {
	repo, aur, flatpak := Counts(state.Updates)
	total := repo + aur + flatpak

	root := &v1.Node{Kind: v1.KindColumn, Gap: 8, Padding: 16}
	root.Children = append(root.Children, panelHeader(state, total))
	root.Children = append(root.Children, panelActions())

	list := &v1.Node{Kind: v1.KindList, Height: 430, Gap: 4}
	if state.RebootNeeded {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindRow, Gap: 8, Height: 24, Children: []*v1.Node{
			{Kind: v1.KindText, Icon: "restart_alt"},
			{Kind: v1.KindText, Text: compactDisplayText("Restart to finish updating: "+state.RebootDetail, 42), Tone: v1.ToneAccent},
		}})
	}
	if state.Checking {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindRow, Gap: 8, Height: 20, Children: []*v1.Node{
			{Kind: v1.KindSpinner, Key: "updates-spinner", Width: 16, Height: 16},
			{Kind: v1.KindText, Text: "Checking…", Tone: v1.ToneSubtle},
		}})
	}
	if state.CheckErr != "" {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: compactDisplayText("Couldn't check: "+state.CheckErr, 44), Tone: v1.ToneError})
	}
	if state.RepoMissing {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: "Install pacman-contrib to check for updates: sudo pacman -S pacman-contrib", Tone: v1.ToneSubtle})
	}
	if terminalHint != "" {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: compactDisplayText(terminalHint, 44), Tone: v1.ToneError})
	}
	if repo > 0 {
		list.Children = append(list.Children, panelGroup("Repo", state.Updates, SourceRepo))
	}
	if aur > 0 {
		list.Children = append(list.Children, panelGroup("AUR", state.Updates, SourceAUR))
	} else if state.AURMissing {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: "AUR: no paru or yay found", Tone: v1.ToneSubtle})
	}
	if flatpak > 0 {
		list.Children = append(list.Children, panelGroup("Flatpak", state.Updates, SourceFlatpak))
	} else if state.FlatpakMissing {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: "Flatpak: flatpak is not installed", Tone: v1.ToneSubtle})
	}
	if total == 0 && !state.Checking && state.CheckErr == "" && !state.RepoMissing {
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindText, Text: "Everything is up to date.", Tone: v1.ToneSubtle})
	}
	root.Children = append(root.Children, list)
	return root
}

func panelHeader(state State, total int) *v1.Node {
	title := "No updates"
	switch {
	case state.Checking && total == 0:
		title = "Checking for updates…"
	case total == 1:
		title = "1 update"
	case total > 1:
		title = fmt.Sprintf("%d updates", total)
	}
	children := []*v1.Node{{Kind: v1.KindText, Text: title, Size: "title", Bold: true}}
	if !state.CheckedAt.IsZero() {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "Last checked " + state.CheckedAt.Format("15:04"), Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Height: 36, PinEnd: true, Children: children}
}

func panelActions() *v1.Node {
	refresh := actionButton("updates-refresh", "Refresh", "Refresh update list")
	refresh.Icon = "refresh"
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Height: 32, Children: []*v1.Node{
		actionButton("updates-run", "Update now", "Run the update in a terminal"),
		refresh,
		actionButton("updates-news", "Arch news", "Open the Arch Linux news page"),
	}}
}

func panelGroup(name string, updates []Update, source Source) *v1.Node {
	group := &v1.Node{Kind: v1.KindColumn, ID: "updates-group-" + string(source), Gap: 2}
	group.Children = append(group.Children, &v1.Node{Kind: v1.KindText, Text: name, Bold: true, Tone: v1.ToneSubtle})
	for _, update := range updates {
		if update.Source != source {
			continue
		}
		group.Children = append(group.Children, updateRow(update))
	}
	return group
}

func updateRow(update Update) *v1.Node {
	text := update.Name
	if update.Old != "" {
		text += "  " + update.Old + " → " + update.New
	} else {
		text += "  → " + update.New
	}
	children := []*v1.Node{{Kind: v1.KindText, Text: compactDisplayText(text, 40)}}
	if update.Core {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "core", Tone: v1.ToneAccent})
	}
	return &v1.Node{Kind: v1.KindRow, ID: "updates-row-" + string(update.Source) + "-" + update.Name, Gap: 8, Height: 20, Children: children}
}

func actionButton(id, label, name string) *v1.Node {
	return &v1.Node{
		Kind:   v1.KindButton,
		ID:     id,
		Text:   label,
		Name:   compactMiddleText(strings.Join(strings.Fields(name), " "), v1.MaxIdentBytes),
		Role:   "button",
		Height: 28,
		Events: []v1.EventKind{v1.EventActivate},
	}
}

// compactDisplayText shortens text to maxBytes for a single line, keeping
// the front and marking the cut with an ellipsis.
func compactDisplayText(text string, maxBytes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= maxBytes {
		return text
	}
	runes := []rune(text)
	budget := maxBytes - len("…")
	if budget < 1 {
		budget = 1
	}
	for len(string(runes[:budget])) > maxBytes-len("…") && budget > 1 {
		budget--
	}
	return string(runes[:budget]) + "…"
}

// compactMiddleText keeps the head and tail of an identifier within
// maxBytes, which matters for accessible names and node IDs.
func compactMiddleText(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	runes := []rune(text)
	budget := maxBytes - len("…")
	if budget < 2 {
		budget = 2
	}
	head := budget / 2
	tail := budget - head
	for len(string(runes[:head])) > head && head > 1 {
		head--
	}
	for len(string(runes[len(runes)-tail:])) > tail && tail > 1 {
		tail--
	}
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}
