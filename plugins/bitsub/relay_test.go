package bitsub

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

func TestFreshWindowAndOrder(t *testing.T) {
	now := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	ns := []subs.Notification{
		{Seq: 7, IssuedAt: now.Add(-time.Hour)},
		{Seq: 5, IssuedAt: now.Add(-2 * time.Hour)},
		{Seq: 4, IssuedAt: now.Add(-time.Minute)},    // already shown
		{Seq: 6, IssuedAt: now.Add(-13 * time.Hour)}, // too old
		{Seq: 8, IssuedAt: now.Add(-12 * time.Hour)}, // exactly 12h: still fresh
	}
	got := Fresh(ns, 4, now)
	var seqs []int64
	for _, n := range got {
		seqs = append(seqs, n.Seq)
	}
	if len(seqs) != 3 || seqs[0] != 5 || seqs[1] != 7 || seqs[2] != 8 {
		t.Fatalf("%v", seqs)
	}
	if len(Fresh(nil, 0, now)) != 0 {
		t.Fatal("nil")
	}
}
