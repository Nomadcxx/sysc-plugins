package bitsub

import (
	"cmp"
	"slices"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// FreshWindow: a notification older than this is not shown on reconnect.
const FreshWindow = 12 * time.Hour

// Fresh is the notifications newer than lastShown and issued within
// FreshWindow, in seq order.
func Fresh(ns []subs.Notification, lastShown int64, now time.Time) []subs.Notification {
	var out []subs.Notification
	for _, n := range ns {
		if n.Seq > lastShown && now.Sub(n.IssuedAt) <= FreshWindow {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b subs.Notification) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}
