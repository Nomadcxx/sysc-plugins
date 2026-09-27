// Package bar builds the sysc-games tray pill node. The bar view forbids
// KindImage, so the pill is icon/text only.
package bar

import (
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

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
			n.Text = fitLabel(r.Name, " · "+Elapsed(r.Start, now))
		}
		n.Tabular = true
	default:
		first := newest(running)
		n.Text = fitLabel(first, fmt.Sprintf(" +%d", len(running)-1))
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{n}}
}

// fitLabel keeps "name+suffix" within the 24-byte text budget of a bar pill
// (240x32 capsule minus icon/padding; measured bytes x8px). Long Lutris titles
// would otherwise overflow and the host would refuse the whole view.
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

func newest(running map[string]Run) string {
	type kv struct {
		name  string
		start time.Time
	}
	list := make([]kv, 0, len(running))
	for _, r := range running {
		list = append(list, kv{r.Name, r.Start})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].start.After(list[j].start) })
	return list[0].name
}
