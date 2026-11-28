package service

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

func TestRolloverCatchesUpOnceAfterDowntime(t *testing.T) {
	h := newHarness(t, "2026-09-01")
	s := mustAdd(t, h.svc, AddInput{Name: "Synthetic", Amount: "20", Currency: "USD", Cycle: subs.Month, AnchorDate: "2026-09-15"})
	h.clock.set("2026-12-20")
	n, err := h.svc.Rollover(ctx)
	if err != nil || n != 4 {
		t.Fatalf("first rollover %d %v", n, err)
	}
	got := charges(t, h.st, s.ID)
	want := []string{"2026-09-15", "2026-10-15", "2026-11-15", "2026-12-15"}
	for i, c := range got {
		if c.Date.String() != want[i] || c.Kind != subs.Assumed || c.AUDMinor != 3101 || c.FXRate != 0.645 {
			t.Errorf("charge %d %+v", i, c)
		}
	}
	before, _ := h.svc.Totals(ctx)
	n, err = h.svc.Rollover(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second rollover %d %v", n, err)
	}
	after, _ := h.svc.Totals(ctx)
	if before != after || len(charges(t, h.st, s.ID)) != 4 {
		t.Fatal("rerun changed state")
	}
	snap, _ := h.svc.Snapshot(ctx, 0)
	for _, m := range snap.Series {
		if (m.Month >= "2026-09" && m.AUDMinor != 3101) || (m.Month < "2026-09" && m.AUDMinor != 0) {
			t.Errorf("series %s = %d", m.Month, m.AUDMinor)
		}
	}
}

func TestRolloverSkipsRecordedDatesAndCancelled(t *testing.T) {
	h := newHarness(t, "2026-11-20")
	s := mustAdd(t, h.svc, AddInput{Name: "Netflix", Amount: "18.99", Currency: "AUD", Cycle: subs.Month, AnchorDate: "2026-11-14"})
	gone := mustAdd(t, h.svc, AddInput{Name: "Stan", Amount: "17", Currency: "AUD", Cycle: subs.Month, AnchorDate: "2026-11-10"})
	if _, err := h.svc.RecordCharge(ctx, "test", s.ID, "2026-11-14", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ConfirmCancelled(ctx, "test", gone.ID, ""); err != nil {
		t.Fatal(err)
	}
	h.clock.set("2026-12-20")
	if n, err := h.svc.Rollover(ctx); err != nil || n != 1 {
		t.Fatalf("rollover %d %v (want only 12-14 for Netflix)", n, err)
	}
	if got := charges(t, h.st, gone.ID); len(got) != 0 {
		t.Fatalf("cancelled subscription charged: %+v", got)
	}
}

func TestRolloverTrialConvertsAndFlagsProbablyCharged(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	s := mustAdd(t, h.svc, AddInput{Name: "Speechify Premium", Amount: "139", Currency: "USD", Cycle: subs.Year,
		TrialEnd: "2026-12-02", Intent: subs.Cancel})
	h.clock.set("2026-12-03")
	if n, err := h.svc.Rollover(ctx); err != nil || n != 1 {
		t.Fatalf("rollover %d %v", n, err)
	}
	got := charges(t, h.st, s.ID)
	if len(got) != 1 || got[0].Date.String() != "2026-12-02" || got[0].Kind != subs.Assumed {
		t.Fatalf("charges %+v", got)
	}
	sub, _ := h.svc.Get(ctx, s.ID)
	if sub.TrialEnd != nil {
		t.Fatal("TrialEnd not cleared")
	}
	snap, _ := h.svc.Snapshot(ctx, 0)
	it := snap.Items[0]
	if !it.ProbablyCharged || it.Target.String() != "2027-12-02" || it.TargetKind != subs.TargetRenewal {
		t.Fatalf("item %+v", it)
	}
	// Setting intent again after the charge starts a fresh decision: not flagged.
	h.clock.set("2026-12-04")
	if _, err := h.svc.SetIntent(ctx, "test", s.ID, subs.Cancel); err != nil {
		t.Fatal(err)
	}
	snap, _ = h.svc.Snapshot(ctx, 0)
	if snap.Items[0].ProbablyCharged {
		t.Fatal("still flagged after a new intent")
	}
}

func TestRolloverWithoutRatesKeepsHistoryApproximate(t *testing.T) {
	h := newHarness(t, "2026-11-01")
	h.rates.ok = false
	mustAdd(t, h.svc, AddInput{Name: "Synthetic", Amount: "20", Currency: "USD", Cycle: subs.Month, AnchorDate: "2026-11-05"})
	h.clock.set("2026-11-06")
	if n, err := h.svc.Rollover(ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	// Series fills the unrated charge from current rates once they exist.
	h.rates.ok = true
	snap, err := h.svc.Snapshot(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if last := snap.Series[11]; last.Month != "2026-11" || last.AUDMinor != 3101 {
		t.Fatalf("series %+v", last)
	}
}
