package remind

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/service"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var ctx = context.Background()

type fakeRates struct{ r subs.Rates }

func (f fakeRates) Current() (subs.Rates, bool) { return f.r, true }

type fakeMail struct {
	sent []string
	err  error
}

func (m *fakeMail) Send(_ context.Context, subject, _ string) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, subject)
	return nil
}

type rig struct {
	now  time.Time
	st   *store.Store
	svc  *service.Service
	eng  *Engine
	mail *fakeMail
}

func newRig(t *testing.T, start time.Time) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bitsub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &rig{now: start, st: st, mail: &fakeMail{}}
	clock := func() time.Time { return r.now }
	fr := fakeRates{subs.Rates{Date: subs.Today(start, time.UTC), PerAUD: map[subs.Currency]float64{"USD": 0.645}}}
	r.svc = service.New(st, clock, time.UTC, fr, nil, "test")
	r.eng = NewEngine(r.svc, st, fr, r.mail, clock, time.UTC, 9)
	return r
}

func (r *rig) notifications(t *testing.T) []subs.Notification {
	t.Helper()
	var out []subs.Notification
	if err := r.st.Tx(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.NotificationsAfter(0, 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func at(day string, h, m int) time.Time {
	d := subs.MustDate(day)
	return time.Date(d.Year, d.Month, d.Day, h, m, 0, 0, time.UTC)
}

func TestEngineIdempotentAndBeforeHourSilent(t *testing.T) {
	r := newRig(t, at("2026-11-29", 8, 0))
	s, err := r.svc.Add(ctx, "test", service.AddInput{Name: "Grok", Amount: "30", Currency: "USD", Cycle: subs.Month, TrialEnd: "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	r.now = at("2026-11-29", 8, 59)
	if err := r.eng.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := r.notifications(t); len(n) != 0 {
		t.Fatalf("before 09:00: %+v", n)
	}
	r.now = at("2026-11-29", 9, 0)
	if err := r.eng.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	n := r.notifications(t)
	if len(n) != 1 || n[0].SubscriptionID != s.ID || n[0].Urgency != "critical" || n[0].Summary != "Grok trial ends in 2 days" ||
		!n[0].IssuedAt.Equal(r.now) || len(n[0].Actions) != 2 {
		t.Fatalf("%+v", n)
	}
	if len(r.mail.sent) != 1 || r.mail.sent[0] != "Grok trial ends in 2 days" {
		t.Fatalf("mail %v", r.mail.sent)
	}
	r.now = at("2026-11-29", 15, 0)
	r.eng.Tick(ctx)
	if len(r.notifications(t)) != 1 || len(r.mail.sent) != 1 {
		t.Fatal("second tick the same day repeated")
	}
}

func TestEngineCatchUpAfterDowntimeFiresOnlyLatestStep(t *testing.T) {
	r := newRig(t, at("2026-11-30", 9, 30))
	if _, err := r.svc.Add(ctx, "test", service.AddInput{Name: "Grok", Amount: "30", Currency: "AUD", Cycle: subs.Month, TrialEnd: "2026-12-09"}); err != nil {
		t.Fatal(err)
	}
	r.eng.Tick(ctx)
	if n := r.notifications(t); len(n) != 0 {
		t.Fatalf("11-30: %+v", n)
	}
	r.now = at("2026-12-08", 10, 0)
	r.eng.Tick(ctx)
	n := r.notifications(t)
	if len(n) != 1 || n[0].Summary != "Grok trial ends tomorrow" {
		t.Fatalf("catch-up: %+v", n)
	}
}

func TestEngineCancelAfterRenewalPassedKeepsNagging(t *testing.T) {
	r := newRig(t, at("2026-12-01", 10, 0))
	s, _ := r.svc.Add(ctx, "test", service.AddInput{Name: "Stan", Amount: "17", Currency: "AUD", Cycle: subs.Month, AnchorDate: "2026-12-10", Intent: subs.Cancel})
	r.now = at("2026-12-12", 9, 5) // archPC was off from 12-01 to 12-12
	if err := r.eng.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	n := r.notifications(t)
	if len(n) != 1 || n[0].Summary != "Stan: cancellation not confirmed" || n[0].SubscriptionID != s.ID {
		t.Fatalf("%+v", n)
	}
	var count int
	r.st.Tx(ctx, func(tx *store.Tx) error {
		c, _ := tx.ChargesFor(s.ID)
		count = len(c)
		return nil
	})
	if count != 1 {
		t.Fatalf("rollover did not run first: %d charges", count)
	}
}

func TestEngineMailErrorRecordedNotReturned(t *testing.T) {
	r := newRig(t, at("2026-11-29", 10, 0))
	r.mail.err = errors.New("gog: token expired")
	r.svc.Add(ctx, "test", service.AddInput{Name: "Grok", Amount: "30", Currency: "AUD", Cycle: subs.Month, TrialEnd: "2026-11-30"})
	if err := r.eng.Tick(ctx); err != nil {
		t.Fatalf("mail error returned: %v", err)
	}
	if r.eng.LastEmailError() != "gog: token expired" {
		t.Fatalf("LastEmailError %q", r.eng.LastEmailError())
	}
	if len(r.notifications(t)) != 1 {
		t.Fatal("notification lost with the email")
	}
}
