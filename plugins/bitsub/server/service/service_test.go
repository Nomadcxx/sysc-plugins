package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

func netflix() AddInput {
	return AddInput{Name: "Netflix", Amount: "18.99", Currency: "AUD", Cycle: subs.Month, AnchorDate: "2026-12-14"}
}

func TestAddDefaultsAndRecordsActor(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	s, err := h.svc.Add(ctx, "claude-archpc", AddInput{Name: "  Grok ", Amount: "30", Currency: "usd",
		Cycle: subs.Month, TrialEnd: "2026-12-07"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID == "" || s.Name != "Grok" || s.Interval != 1 || s.Intent != subs.Undecided || s.Status != subs.Active ||
		s.Anchor.String() != "2026-12-07" || s.TrialEnd == nil || s.Price != (subs.Money{Minor: 3000, Currency: "USD"}) ||
		s.CreatedBy != "claude-archpc" {
		t.Fatalf("%+v", s)
	}
	rows := auditRows(t, h.st, s.ID)
	if len(rows) != 1 || rows[0].Actor != "claude-archpc" || rows[0].Action != "add" {
		t.Fatalf("audit %+v", rows)
	}
	select {
	case <-h.svc.Changed():
	default:
		t.Fatal("no change signal after Add")
	}
}

func TestAddRefusesNearDuplicate(t *testing.T) {
	svc := newTestService(t, "2026-11-29")
	mustAdd(t, svc, netflix())
	dupIn := netflix()
	dupIn.Name = "  NETFLIX "
	_, err := svc.Add(ctx, "codex-laptop", dupIn)
	var dup *DuplicateError
	if !errors.As(err, &dup) || dup.Existing.Name != "Netflix" {
		t.Fatal(err)
	}
	if !strings.Contains(err.Error(), dup.Existing.ID) || !strings.Contains(err.Error(), "Netflix") {
		t.Fatalf("error doesn't name the existing entry: %v", err)
	}
	dupIn.AllowDuplicate = true
	if _, err := svc.Add(ctx, "codex-laptop", dupIn); err != nil {
		t.Fatal(err)
	}
}

func TestValidationRejects(t *testing.T) {
	svc := newTestService(t, "2026-11-29")
	cases := []struct {
		field string
		mod   func(*AddInput)
	}{
		{"amount", func(in *AddInput) { in.Amount = "0" }},
		{"amount", func(in *AddInput) { in.Amount = "-5" }},
		{"amount", func(in *AddInput) { in.Amount = "100000.01" }},
		{"currency", func(in *AddInput) { in.Currency = "usd$" }},
		{"currency", func(in *AddInput) { in.Currency = "XYZ" }}, // not in the known rates
		{"interval", func(in *AddInput) { in.Interval = -1 }},
		{"interval", func(in *AddInput) { in.Interval = 37 }},
		{"anchor_date", func(in *AddInput) { in.AnchorDate = "2040-01-01" }},
		{"anchor_date", func(in *AddInput) { in.AnchorDate = "14/12/2026" }},
		{"payment_label", func(in *AddInput) { in.PaymentLabel = "4111 1111 1111 1111" }},
		{"payment_label", func(in *AddInput) { in.PaymentLabel = strings.Repeat("x", 41) }},
		{"url", func(in *AddInput) { in.URL = "javascript:x" }},
		{"name", func(in *AddInput) { in.Name = "   " }},
		{"name", func(in *AddInput) { in.Name = strings.Repeat("n", 81) }},
		{"anchor_date", func(in *AddInput) { in.AnchorDate, in.TrialEnd = "", "" }},
		{"cycle", func(in *AddInput) { in.Cycle = "fortnight" }},
		{"intent", func(in *AddInput) { in.Intent = "maybe" }},
		{"notice_days", func(in *AddInput) { in.NoticeDays = 91 }},
		{"reminder_days", func(in *AddInput) { in.ReminderDays = []int{3, 3} }},
		{"reminder_days", func(in *AddInput) { in.ReminderDays = []int{61} }},
		{"reminder_days", func(in *AddInput) { in.ReminderDays = []int{1, 2, 3, 4, 5, 6} }},
		{"notes", func(in *AddInput) { in.Notes = strings.Repeat("n", 2049) }},
		{"category", func(in *AddInput) { in.Category = strings.Repeat("c", 41) }},
		{"trial_end", func(in *AddInput) { in.TrialEnd = "2016-01-01" }},
	}
	for _, c := range cases {
		in := netflix()
		c.mod(&in)
		_, err := svc.Add(ctx, "test", in)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != c.field {
			t.Errorf("%s case: got %v", c.field, err)
		}
	}
	// Accepted edge values.
	for _, mod := range []func(*AddInput){
		func(in *AddInput) { in.Amount = "100000.00" },
		func(in *AddInput) { in.PaymentLabel = "Visa ··21" },
		func(in *AddInput) { in.URL = "https://netflix.com/account" },
		func(in *AddInput) { in.ReminderDays = []int{14, 3, 1} },
	} {
		in := netflix()
		mod(&in)
		in.AllowDuplicate = true
		if _, err := svc.Add(ctx, "test", in); err != nil {
			t.Errorf("rejected valid input: %v", err)
		}
	}
}

func TestCurrencyAcceptedBeforeFirstRates(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	h.rates.ok = false
	in := netflix()
	in.Currency = "CHF"
	if _, err := h.svc.Add(ctx, "test", in); err != nil {
		t.Fatalf("unknown rates should not block a syntactically valid code: %v", err)
	}
}

func TestSetIntentAndConfirmCancelledAudited(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	s := mustAdd(t, h.svc, netflix())
	if _, err := h.svc.SetIntent(ctx, "laptop-plugin", s.ID, subs.Cancel); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.ConfirmCancelled(ctx, "claude-archpc", s.ID, "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != subs.Cancelled || got.AccessUntil == nil || got.AccessUntil.String() != "2026-12-31" {
		t.Fatalf("%+v", got)
	}
	items, _ := h.svc.List(ctx, Filter{})
	if len(items) != 0 {
		t.Fatalf("cancelled still listed: %+v", items)
	}
	cancelled, _ := h.svc.List(ctx, Filter{Status: subs.Cancelled})
	if len(cancelled) != 1 {
		t.Fatalf("cancelled filter: %+v", cancelled)
	}
	rows := auditRows(t, h.st, s.ID)
	if len(rows) != 3 || rows[1].Actor != "laptop-plugin" || rows[1].Action != "set_intent" ||
		rows[2].Actor != "claude-archpc" || rows[2].Action != "confirm_cancelled" {
		t.Fatalf("audit %+v", rows)
	}
	if _, err := h.svc.SetIntent(ctx, "x", s.ID, "nope"); err == nil {
		t.Fatal("bad intent accepted")
	}
	if _, err := h.svc.SetIntent(ctx, "x", "missing", subs.Keep); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	if _, err := h.svc.ConfirmCancelled(ctx, "x", s.ID, "31/12/2026"); err == nil {
		t.Fatal("bad access_until accepted")
	}
}

func TestGetByIDOrName(t *testing.T) {
	svc := newTestService(t, "2026-11-29")
	s := mustAdd(t, svc, netflix())
	if got, err := svc.Get(ctx, s.ID); err != nil || got.ID != s.ID {
		t.Fatal(got, err)
	}
	if got, err := svc.Get(ctx, " netflix"); err != nil || got.ID != s.ID {
		t.Fatal(got, err)
	}
	if _, err := svc.Get(ctx, "stan"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	in := netflix()
	in.AllowDuplicate = true
	mustAdd(t, svc, in)
	var ve *ValidationError
	if _, err := svc.Get(ctx, "Netflix"); !errors.As(err, &ve) {
		t.Fatalf("ambiguous name: %v", err)
	}
}

func TestUpdatePartialAndClearTrial(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	s := mustAdd(t, h.svc, AddInput{Name: "Grok", Amount: "30", Currency: "USD", Cycle: subs.Month, TrialEnd: "2026-12-07"})
	amount, notes := "40", "price rise"
	got, err := h.svc.Update(ctx, "test", s.ID, UpdateInput{Amount: &amount, Notes: &notes, ClearTrialEnd: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Price.Minor != 4000 || got.Price.Currency != "USD" || got.Notes != "price rise" || got.TrialEnd != nil || got.Name != "Grok" {
		t.Fatalf("%+v", got)
	}
	bad := "0"
	var ve *ValidationError
	if _, err := h.svc.Update(ctx, "test", s.ID, UpdateInput{Amount: &bad}); !errors.As(err, &ve) || ve.Field != "amount" {
		t.Fatal(err)
	}
	other := mustAdd(t, h.svc, netflix())
	name := "grok"
	if _, err := h.svc.Update(ctx, "test", other.ID, UpdateInput{Name: &name}); !errors.As(err, new(*DuplicateError)) {
		t.Fatalf("rename onto an existing name: %v", err)
	}
	if rows := auditRows(t, h.st, s.ID); len(rows) != 2 || !strings.Contains(rows[1].Before, "3000") || !strings.Contains(rows[1].After, "4000") {
		t.Fatalf("price change not audited: %+v", rows)
	}
}

func TestRecordCharge(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	s := mustAdd(t, h.svc, AddInput{Name: "Synthetic", Amount: "20", Currency: "USD", Cycle: subs.Month, AnchorDate: "2026-11-05"})
	c, err := h.svc.RecordCharge(ctx, "test", s.ID, "2026-11-05", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != subs.Recorded || c.Amount.Minor != 2000 || c.AUDMinor != 3101 || c.FXRate != 0.645 || c.FXDate.String() != "2026-11-29" {
		t.Fatalf("%+v", c)
	}
	if _, err := h.svc.RecordCharge(ctx, "test", s.ID, "2026-11-05", "21", ""); err == nil {
		t.Fatal("second recorded charge on the same date accepted")
	}
	if _, err := h.svc.RecordCharge(ctx, "test", s.ID, "2026-12-05", "", ""); err == nil {
		t.Fatal("future charge accepted")
	}
	c2, err := h.svc.RecordCharge(ctx, "test", s.ID, "2026-10-05", "31.50", "AUD")
	if err != nil || c2.AUDMinor != 3150 || c2.Amount.Currency != "AUD" {
		t.Fatalf("%+v %v", c2, err)
	}
}

func TestUpcomingAndTotals(t *testing.T) {
	svc := newTestService(t, "2026-11-29")
	mustAdd(t, svc, netflix()) // 12-14
	mustAdd(t, svc, AddInput{Name: "Synthetic", Amount: "20", Currency: "USD", Cycle: subs.Month, AnchorDate: "2026-12-05"})
	mustAdd(t, svc, AddInput{Name: "Hetzner", Amount: "5.49", Currency: "EUR", Cycle: subs.Year, AnchorDate: "2027-03-01"})
	week, err := svc.Upcoming(ctx, 7)
	if err != nil || len(week) != 1 || week[0].Name != "Synthetic" {
		t.Fatalf("%+v %v", week, err)
	}
	month, _ := svc.Upcoming(ctx, 0)
	if len(month) != 2 {
		t.Fatalf("default 30 days: %+v", month)
	}
	if _, err := svc.Upcoming(ctx, 367); err == nil {
		t.Fatal("367 days accepted")
	}
	tot, err := svc.Totals(ctx)
	if err != nil || tot.MonthlyAUD != 1899+3101+78 { // 5.49/0.59 = 9.305 → 931 / 12 = 77.6 → 78
		t.Fatalf("%+v %v", tot, err)
	}
}

func TestDeleteAndRestore(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	s := mustAdd(t, h.svc, netflix())
	if err := h.svc.Delete(ctx, "test", s.ID); err != nil {
		t.Fatal(err)
	}
	if items, _ := h.svc.List(ctx, Filter{}); len(items) != 0 {
		t.Fatal("deleted listed")
	}
	if items, _ := h.svc.List(ctx, Filter{IncludeDeleted: true}); len(items) != 1 {
		t.Fatal("IncludeDeleted missing it")
	}
	// The name is free again while deleted.
	again := mustAdd(t, h.svc, netflix())
	if err := h.svc.Restore(ctx, "test", s.ID); !errors.As(err, new(*DuplicateError)) {
		t.Fatalf("restore over a live duplicate: %v", err)
	}
	if err := h.svc.Delete(ctx, "test", again.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Restore(ctx, "test", s.ID); err != nil {
		t.Fatal(err)
	}
	if items, _ := h.svc.List(ctx, Filter{}); len(items) != 1 || items[0].ID != s.ID {
		t.Fatalf("restored: %+v", items)
	}
	if err := h.svc.Delete(ctx, "test", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestListFilters(t *testing.T) {
	svc := newTestService(t, "2026-11-29")
	mustAdd(t, svc, netflix())
	in := AddInput{Name: "Synthetic", Amount: "20", Currency: "USD", Cycle: subs.Month, AnchorDate: "2026-12-05",
		Intent: subs.Keep, Category: "AI and LLMs"}
	mustAdd(t, svc, in)
	keep, _ := svc.List(ctx, Filter{Intent: subs.Keep})
	text, _ := svc.List(ctx, Filter{Text: "llm"})
	name, _ := svc.List(ctx, Filter{Text: "NETF"})
	if len(keep) != 1 || len(text) != 1 || text[0].Name != "Synthetic" || len(name) != 1 {
		t.Fatalf("%v %v %v", keep, text, name)
	}
	all, _ := svc.List(ctx, Filter{})
	if all[0].Name != "Synthetic" || all[1].Name != "Netflix" {
		t.Fatalf("not ordered by target: %+v", all)
	}
}

func TestSnapshotOrdersByTargetAndSkipsCancelledNotifications(t *testing.T) {
	h := newHarness(t, "2026-11-29")
	a := mustAdd(t, h.svc, netflix())
	b := mustAdd(t, h.svc, AddInput{Name: "Synthetic", Amount: "20", Currency: "USD", Cycle: subs.Month, AnchorDate: "2026-12-05"})
	c := mustAdd(t, h.svc, AddInput{Name: "Aardvark", Amount: "5", Currency: "AUD", Cycle: subs.Month, AnchorDate: "2026-12-05"})
	if err := h.st.Tx(ctx, func(tx *store.Tx) error {
		for i, id := range []string{a.ID, b.ID, c.ID} {
			if _, _, err := tx.InsertNotification(id, subs.Notification{SubscriptionID: id, Summary: "n", Urgency: "normal",
				IssuedAt: h.clock.now().Add(time.Duration(i) * time.Minute)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ConfirmCancelled(ctx, "test", b.ID, ""); err != nil {
		t.Fatal(err)
	}
	snap, err := h.svc.Snapshot(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Items) != 2 || snap.Items[0].Name != "Aardvark" || snap.Items[1].Name != "Netflix" {
		t.Fatalf("items %+v", snap.Items)
	}
	if len(snap.Notifications) != 2 || snap.Notifications[0].SubscriptionID != a.ID || snap.Notifications[1].SubscriptionID != c.ID {
		t.Fatalf("notifications %+v", snap.Notifications)
	}
	if snap.LastSeq != 3 || snap.ServedBy != "archPC" || len(snap.Series) != 12 || snap.GeneratedAt.IsZero() {
		t.Fatalf("snapshot %+v", snap)
	}
	later, _ := h.svc.Snapshot(ctx, 2)
	if n := len(later.Notifications); n != 1 {
		t.Fatalf("since=2 gave %d", n)
	}
}
