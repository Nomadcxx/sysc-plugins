// Package bar builds the sysc-games tray pill node. The bar view forbids
// KindImage, so the pill is icon/text only.
package bar

import (
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type Run struct {
	Name  string
	Start time.Time
}

// Elapsed renders whole-minute granularity (60s poll cadence).
func Elapsed(start, now time.Time) string {
	m := int64(now.Sub(start).Minutes())
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

// Pill returns the bar view root: a row wrapping the button, because the
// host's Convert refuses any non-row bar root (internal/plugin/view.go).
func Pill(running map[string]Run, libraryMissing bool, now time.Time) *v1.Node {
	return PillAtWidth(running, libraryMissing, now, barwidth.StandardWidth)
}

func PillAtWidth(running map[string]Run, libraryMissing bool, now time.Time, width int) *v1.Node {
	compact := barwidth.Compact(width)
	n := &v1.Node{
		Kind: v1.KindButton, ID: "bar", Key: "bar",
		Icon: "sports_esports", Name: "games", Role: "button",
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
		// Host wraps bar roots in a capsule; card fill keeps the pill from
		// painting a lighter inner shape (same contract as calendar/cat).
		Fill: "card",
	}
	switch len(running) {
	case 0:
		if libraryMissing {
			n.Tone = v1.ToneSubtle
		}
	case 1:
		for _, r := range running {
			if !compact {
				n.Text = fitLabel(r.Name, " · "+Elapsed(r.Start, now))
			}
		}
		n.Tabular = !compact
	default:
		if !compact {
			first := newestFirst(running)[0].Name
			n.Text = fitLabel(first, fmt.Sprintf(" +%d", len(running)-1))
		}
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{n}}
}

// TooltipTree returns the hover view for the bar pill. Tooltip views reject
// interactive and panel-only nodes (segmented, list, button), so this is a
// plain read-only column; the host opens one for every bar widget.
func TooltipTree(running map[string]Run, libraryMissing bool, libraryCount int, now time.Time) *v1.Node {
	root := &v1.Node{Kind: v1.KindColumn, Gap: 4}
	root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: "Games", Bold: true})
	switch {
	case libraryMissing:
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: "Lutris library not found", Tone: v1.ToneSubtle})
	case len(running) == 0:
		root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: "No games running", Tone: v1.ToneSubtle})
		if libraryCount > 0 {
			root.Children = append(root.Children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d games in library", libraryCount), Tone: v1.ToneSubtle})
		}
	default:
		// 280x200 slot, 16px text lines: title + 6 rows + "+N more" fits.
		const maxRows = 6
		list := newestFirst(running)
		for i, r := range list {
			if i == maxRows {
				root.Children = append(root.Children, &v1.Node{Kind: v1.KindText,
					Text: fmt.Sprintf("+%d more", len(list)-maxRows), Tone: v1.ToneSubtle})
				break
			}
			root.Children = append(root.Children, &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: "play_arrow", IconSize: 14},
				{Kind: v1.KindText, Text: fitLabel(r.Name, "")},
				{Kind: v1.KindText, Text: Elapsed(r.Start, now), Tone: v1.ToneSubtle, Tabular: true},
			}})
		}
	}
	return root
}

// fitLabel keeps "name+suffix" within a 24-byte text budget (bar pill 240x32
// capsule minus icon/padding; tooltip row 280 wide minus icon and elapsed;
// measured bytes x8px). Long Lutris titles would otherwise overflow and the
// host would refuse the whole view.
func fitLabel(name, suffix string) string {
	const budget = 24
	if len(name)+len(suffix) <= budget {
		return name + suffix
	}
	keep := budget - 3 - len(suffix) // 3 = bytes in "…"
	if keep < 1 {
		keep = 1
	}
	for keep > 0 && !utf8.ValidString(name[:keep]) {
		keep-- // cut on a rune boundary
	}
	return name[:keep] + "…" + suffix
}

// newestFirst orders runs by start time, newest first.
func newestFirst(running map[string]Run) []Run {
	list := make([]Run, 0, len(running))
	for _, r := range running {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Start.After(list[j].Start) })
	return list
}
