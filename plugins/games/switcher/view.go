// Package switcher builds the floating "now playing" list opened from a
// right-click on the bar pill. Floating views allow images and lists, so
// unlike the bar pill each row can show cover art.
package switcher

import (
	"sort"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/bar"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Run is one entry in the switcher.
type Run struct {
	ID        string
	Name      string
	Start     time.Time
	CoverPath string
}

// Build renders one row per running game, newest first.
func Build(runs []Run, now time.Time) *v1.Node {
	sorted := make([]Run, len(runs))
	copy(sorted, runs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.After(sorted[j].Start) })

	root := &v1.Node{Kind: v1.KindColumn, ID: "switcher", Key: "switcher", Gap: 6, Padding: 8}
	root.Children = append(root.Children, &v1.Node{
		Kind: v1.KindText, ID: "switcher-title", Text: "Now playing",
		Size: "title", Bold: true,
	})
	if len(sorted) == 0 {
		root.Children = append(root.Children, &v1.Node{
			Kind: v1.KindText, Text: "Nothing running", Tone: v1.ToneSubtle,
		})
		return root
	}

	list := &v1.Node{Kind: v1.KindList, ID: "switcher-list", Key: "switcher-list"}
	for _, r := range sorted {
		row := &v1.Node{Kind: v1.KindRow, Gap: 4}

		open := &v1.Node{
			Kind:   v1.KindButton,
			ID:     "sw-open-" + r.ID,
			Key:    "sw-open-" + r.ID,
			Name:   "focus " + r.Name,
			Role:   "button",
			Fill:   "card",
			Shape:  "card",
			Width:  244,
			Events: []v1.EventKind{v1.EventActivate},
		}
		inner := &v1.Node{Kind: v1.KindRow, Gap: 6}
		if r.CoverPath != "" {
			inner.Children = append(inner.Children, &v1.Node{
				Kind: v1.KindImage, Path: r.CoverPath, ImageW: 40, ImageH: 40, Background: true,
			})
		}
		info := &v1.Node{Kind: v1.KindColumn}
		info.Children = append(info.Children,
			&v1.Node{Kind: v1.KindText, Text: r.Name, Bold: true, MaxWidth: 170},
			&v1.Node{Kind: v1.KindText, Text: bar.Elapsed(r.Start, now), Tone: v1.ToneSubtle, Tabular: true},
		)
		inner.Children = append(inner.Children, info)
		open.Children = append(open.Children, inner)

		stop := &v1.Node{
			Kind:    v1.KindButton,
			ID:      "sw-stop-" + r.ID,
			Key:     "sw-stop-" + r.ID,
			Icon:    "stop",
			Name:    "stop " + r.Name,
			Role:    "button",
			Tooltip: "Stop " + r.Name,
			Width:   44,
			Events:  []v1.EventKind{v1.EventActivate},
		}
		row.Children = append(row.Children, open, stop)
		list.Children = append(list.Children, row)
	}
	root.Children = append(root.Children, list)
	return root
}
