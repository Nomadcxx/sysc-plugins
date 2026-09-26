// Package bar builds the sysc-games tray pill node. The bar view forbids
// KindImage, so the pill is icon/text only.
package bar

import (
	"fmt"
	"sort"
	"time"

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

func Pill(running map[string]Run, libraryMissing bool, now time.Time) *v1.Node {
	n := &v1.Node{
		Kind: v1.KindButton, ID: "bar", Key: "bar",
		Icon: "sports_esports", Name: "games", Role: "button",
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}
	switch len(running) {
	case 0:
		if libraryMissing {
			n.Tone = v1.ToneSubtle
		}
	case 1:
		for _, r := range running {
			n.Text = r.Name + " · " + Elapsed(r.Start, now)
		}
		n.Tabular = true
	default:
		first := newest(running)
		n.Text = fmt.Sprintf("%s +%d", first, len(running)-1)
	}
	return n
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
