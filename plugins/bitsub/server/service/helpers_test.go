package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var ctx = context.Background()

var melbourne = func() *time.Location {
	loc, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		return time.FixedZone("AEDT", 11*3600)
	}
	return loc
}()

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// set moves the clock to 10:00 local on day.
func (c *clock) set(day string) { c.t = subs.MustDate(day).At(10, melbourne) }

type fakeRates struct {
	r  subs.Rates
	ok bool
}

func (f *fakeRates) Current() (subs.Rates, bool) { return f.r, f.ok }

type fakeChannels struct{ ch subs.Channels }

func (f fakeChannels) Channels() subs.Channels { return f.ch }

type harness struct {
	svc   *Service
	st    *store.Store
	clock *clock
	rates *fakeRates
}

func newHarness(t *testing.T, today string) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bitsub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &clock{}
	c.set(today)
	r := &fakeRates{r: subs.Rates{Date: subs.MustDate(today), PerAUD: map[subs.Currency]float64{"USD": 0.645, "EUR": 0.59}}, ok: true}
	return &harness{svc: New(st, c.now, melbourne, r, fakeChannels{}, "archPC"), st: st, clock: c, rates: r}
}

func newTestService(t *testing.T, today string) *Service { return newHarness(t, today).svc }

func mustAdd(t *testing.T, svc *Service, in AddInput) subs.Subscription {
	t.Helper()
	s, err := svc.Add(ctx, "test", in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func auditRows(t *testing.T, st *store.Store, id string) []store.AuditRow {
	t.Helper()
	var rows []store.AuditRow
	if err := st.Tx(ctx, func(tx *store.Tx) error {
		var err error
		rows, err = tx.ListAudit(id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func charges(t *testing.T, st *store.Store, id string) []subs.Charge {
	t.Helper()
	var out []subs.Charge
	if err := st.Tx(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ChargesFor(id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
