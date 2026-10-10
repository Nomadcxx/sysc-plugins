package herdr

import (
	"fmt"
	"strconv"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// PanelWidth and PanelHeight mirror the panel box declared in manifest.json.
const (
	PanelWidth  = 440
	PanelHeight = 560
)

// The bar's fixed glyph and strip height.
const (
	barIconName = "ai-usage"
	barIconSize = 16
	barHeight   = 32
)

// needsYouCount is blocked + done across every session: the number the bar
// stresses when an agent wants a human.
func needsYouCount(m *Model) int {
	if m == nil {
		return 0
	}
	n := 0
	for i := range m.Sessions {
		c := m.Sessions[i].Counts
		n += c.Blocked + c.Done
	}
	return n
}

// worstTone is the most urgent tone any session carries, so the bar glyph
// reads the fleet at a glance.
func worstTone(m *Model) v1.Tone {
	if m == nil {
		return v1.ToneSubtle
	}
	if m.HerdrMissing {
		return v1.ToneError
	}
	var blocked, done, working int
	stale := false
	for i := range m.Sessions {
		c := m.Sessions[i].Counts
		blocked += c.Blocked
		done += c.Done
		working += c.Working
		if m.Sessions[i].Running && m.Sessions[i].Stale {
			stale = true
		}
	}
	switch {
	case stale || blocked > 0:
		return v1.ToneError
	case done > 0:
		return v1.ToneAccent
	case working > 0:
		return v1.ToneNormal
	default:
		return v1.ToneSubtle
	}
}

// BarTree is the bar widget at the standard width.
func BarTree(m *Model, ws WidgetSettings, height int) *v1.Node {
	return BarTreeAtWidth(m, ws, height, barwidth.StandardWidth)
}

// BarTreeAtWidth builds the bar pill. The root is a row with one control; the
// count joins the glyph only when it fits.
func BarTreeAtWidth(m *Model, ws WidgetSettings, height, width int) *v1.Node {
	h := height
	if h <= 0 {
		h = barHeight
	}
	compact := barwidth.Compact(width)
	tone := worstTone(m)
	count := needsYouCount(m)
	countText := strconv.Itoa(count)

	button := &v1.Node{
		Kind: v1.KindButton, ID: "open", Name: "Herdr", Role: "button",
		Fill: "card", Height: h, Gap: 4, Tooltip: tooltipText(m),
		Events: []v1.EventKind{v1.EventActivate},
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: barIconName, IconSize: barIconSize, Tone: tone, Key: "herdr-bar"},
		},
	}

	show := ws.DisplayMode != "icon"
	if show {
		if compact {
			// The pill has no room for a padding-fit count: only include it
			// when the digits themselves fit beside the glyph.
			show = count > 0 && barIconSize+8*len(countText) <= width
		} else {
			show = count > 0 || !ws.HideCountWhenZero
		}
	}
	if show {
		button.Children = append(button.Children, &v1.Node{
			Kind: v1.KindText, Text: countText, Tabular: true, Tone: tone,
		})
	}
	if compact {
		button.Padding = 0
	} else {
		button.Padding = 4
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{button}}
}

// TooltipTree is the bar's hover content: a read-only column the shell opens,
// so it must hold no interactive nodes.
func TooltipTree(m *Model, ws WidgetSettings) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 2}
	if m == nil {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "Herdr"})
		return col
	}
	if m.HerdrMissing {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "herdr not found"})
		return col
	}
	n, agents := 0, 0
	var blocked, done, working int
	for i := range m.Sessions {
		n++
		c := m.Sessions[i].Counts
		agents += c.Agents
		blocked += c.Blocked
		done += c.Done
		working += c.Working
	}
	col.Children = append(col.Children, &v1.Node{
		Kind: v1.KindText, Text: fmt.Sprintf("%d sessions · %d agents", n, agents), Tabular: true,
	})
	if blocked > 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d blocked", blocked), Tone: v1.ToneError, Tabular: true})
	}
	if done > 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d done", done), Tone: v1.ToneAccent, Tabular: true})
	}
	if working > 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d working", working), Tabular: true})
	}
	return col
}

// tooltipText is the bar control's bounded hover hint; it mirrors the panel
// header summary so both surfaces lead with the same subject.
func tooltipText(m *Model) string {
	if m == nil {
		return "Herdr"
	}
	if m.HerdrMissing {
		return "herdr not found"
	}
	return summary(m)
}
