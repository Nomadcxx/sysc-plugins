package worldclock

import (
	"fmt"
	"strings"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type BarMode string

const (
	BarIcon    BarMode = "icon"
	BarPrimary BarMode = "primary"
	BarAll     BarMode = "all"
	BarCycle   BarMode = "cycle"
)

// barTextBytes budgets the bar label in the host's crude metric (8 px per
// byte) so the button never overruns the 240 px bar slot.
const barTextBytes = 28

const maxTooltipZones = 8

func ParseBarMode(s string) (BarMode, bool) {
	switch m := BarMode(s); m {
	case BarIcon, BarPrimary, BarAll, BarCycle:
		return m, true
	}
	return "", false
}

func OnBar(readings []Reading) []Reading {
	out := make([]Reading, 0, len(readings))
	for _, r := range readings {
		if r.OnBar {
			out = append(out, r)
		}
	}
	return out
}

// barEntry is "Label clock [marker]", shortening the label (never the time) to
// fit budget bytes.
func barEntry(r Reading, budget int) string {
	suffix := " " + r.Clock
	if m := DayMarker(r.DayShift); m != "" {
		suffix += " " + m
	}
	label := r.Label
	if len(label)+len(suffix) > budget {
		room := budget - len(suffix) - len("…")
		for len(label) > room && label != "" {
			_, size := utf8.DecodeLastRuneInString(label)
			label = label[:len(label)-size]
		}
		label += "…"
	}
	return label + suffix
}

// BarText is the bar's label for mode, or "" when the bar should show the
// globe instead.
func BarText(mode BarMode, onBar []Reading, cycle int) string {
	if mode == BarIcon || len(onBar) == 0 {
		return ""
	}
	switch mode {
	case BarAll:
		out := barEntry(onBar[0], barTextBytes)
		for _, r := range onBar[1:] {
			next := out + " · " + barEntry(r, barTextBytes)
			if len(next) > barTextBytes {
				break
			}
			out = next
		}
		return out
	case BarCycle:
		if cycle < 0 {
			cycle = 0
		}
		return barEntry(onBar[cycle%len(onBar)], barTextBytes)
	}
	return barEntry(onBar[0], barTextBytes)
}

// BarButton is the whole bar control: it opens the panel, and the minute
// patch replaces it by its key.
func BarButton(mode BarMode, onBar []Reading, cycle int) *v1.Node {
	n := &v1.Node{Kind: v1.KindButton, ID: "open", Key: "bar", Name: "Open world clock", Role: "button",
		Tabular: true, Events: []v1.EventKind{v1.EventActivate}}
	if text := BarText(mode, onBar, cycle); text != "" {
		n.Text = text
	} else {
		n.Icon = "public"
	}
	return n
}

func Bar(mode BarMode, onBar []Reading, cycle int) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{BarButton(mode, onBar, cycle)}}
}

func Tooltip(onBar []Reading) *v1.Node {
	lines := []*v1.Node{{Kind: v1.KindText, Text: "World Clock", Bold: true}}
	if len(onBar) == 0 {
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: "No zones on the bar", Tone: v1.ToneSubtle})
	}
	for i, r := range onBar {
		if i == maxTooltipZones {
			lines = append(lines, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("+%d more", len(onBar)-i), Tone: v1.ToneSubtle})
			break
		}
		rel := r.Relative
		switch {
		case r.DayShift > 0:
			rel += ", tomorrow"
		case r.DayShift < 0:
			rel += ", yesterday"
		}
		lines = append(lines, &v1.Node{Kind: v1.KindText, Text: strings.Join([]string{r.Label + " " + r.Clock, rel}, " · ")})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: lines}
}
