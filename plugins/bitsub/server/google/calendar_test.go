package google

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var ctx = context.Background()

// fakeRunner answers by subcommand ("calendar events", "gmail send", ...)
// and records every argv and stdin.
type fakeRunner struct {
	replies map[string]string
	errs    map[string]error
	calls   [][]string
	stdin   []string
}

func (f *fakeRunner) answer(args []string) ([]byte, error) {
	f.calls = append(f.calls, args)
	key := strings.Join(args[:min(2, len(args))], " ")
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	return []byte(f.replies[key]), nil
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) { return f.answer(args) }

func (f *fakeRunner) RunStdin(_ context.Context, stdin string, args ...string) ([]byte, error) {
	f.stdin = append(f.stdin, stdin)
	return f.answer(args)
}

func (f *fakeRunner) callsOf(sub string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) >= 2 && c[0]+" "+c[1] == sub {
			out = append(out, c)
		}
	}
	return out
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bitsub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newCal(t *testing.T, r *fakeRunner, now time.Time) *Calendar {
	c := NewCalendar(r, openStore(t), "Subscriptions")
	c.Now = func() time.Time { return now }
	c.Loc = time.UTC
	return c
}

func argValues(argv []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			out = append(out, argv[i+1])
		}
	}
	return out
}

func TestEnsureCreatesCalendarOnce(t *testing.T) {
	r := &fakeRunner{replies: map[string]string{"calendar create-calendar": fixture(t, "create-calendar.json")}}
	c := newCal(t, r, time.Date(2026, 11, 29, 9, 0, 0, 0, time.UTC))
	for i := 0; i < 2; i++ {
		id, err := c.Ensure(ctx)
		if err != nil || id != "abc123@group.calendar.google.com" {
			t.Fatalf("%q %v", id, err)
		}
	}
	calls := r.callsOf("calendar create-calendar")
	if len(calls) != 1 || calls[0][2] != "Subscriptions" {
		t.Fatalf("create-calendar calls %v", calls)
	}
}

func TestReconcileCreatesUpdatesDeletes(t *testing.T) {
	r := &fakeRunner{replies: map[string]string{
		"calendar create-calendar": fixture(t, "create-calendar.json"),
		"calendar events":          fixture(t, "events.json"),
		"calendar create":          fixture(t, "create-event.json"),
		"calendar update":          `{"event":{"id":"evB"}}`,
	}}
	now := time.Date(2026, 11, 29, 9, 0, 0, 0, time.UTC)
	c := newCal(t, r, now)
	desired := []Event{
		{SubscriptionID: "A", Date: "2026-12-05", Summary: "A renews · A$1.00/mo", Description: "d", Reminders: []string{"popup:159h", "popup:15h"}, Hash: "hA"},
		{SubscriptionID: "B", Date: "2026-12-10", Summary: "B renews · A$12.00/mo", Description: "d", Reminders: []string{"popup:15h"}, Hash: "hB"},
	}
	res, err := c.Reconcile(ctx, desired)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{Created: 1, Updated: 1, Deleted: 1}) {
		t.Fatalf("result %+v", res)
	}
	list := r.callsOf("calendar events")[0]
	if list[2] != "abc123@group.calendar.google.com" || !slices.Contains(argValues(list, "--private-prop-filter"), "bitsub=1") ||
		!slices.Contains(list, "--all-pages") {
		t.Fatalf("list argv %v", list)
	}
	create := r.callsOf("calendar create")[0]
	props := argValues(create, "--private-prop")
	if !slices.Contains(props, "bitsub_id=A") || !slices.Contains(props, "bitsub=1") || !slices.Contains(props, "bitsub_hash=hA") ||
		!slices.Contains(create, "--all-day") || !slices.Equal(argValues(create, "--reminder"), []string{"popup:159h", "popup:15h"}) ||
		argValues(create, "--from")[0] != "2026-12-05" || argValues(create, "--to")[0] != "2026-12-06" ||
		argValues(create, "--summary")[0] != "A renews · A$1.00/mo" {
		t.Fatalf("create argv %v", create)
	}
	update := r.callsOf("calendar update")[0]
	if update[3] != "evB" || !slices.Contains(argValues(update, "--private-prop"), "bitsub_hash=hB") ||
		!slices.Contains(argValues(update, "--private-prop"), "bitsub_id=B") {
		t.Fatalf("update argv %v", update)
	}
	del := r.callsOf("calendar delete")
	if len(del) != 1 || del[0][3] != "evC" || !slices.Contains(del[0], "--force") {
		t.Fatalf("delete argv %v", del)
	}
	syncedAt, lastErr := c.Status()
	if syncedAt == nil || !syncedAt.Equal(now) || lastErr != "" {
		t.Fatalf("status %v %q", syncedAt, lastErr)
	}
	if next := c.NextAttempt(); !next.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("next %v", next)
	}
}

func TestReconcileUnchangedHashDoesNothing(t *testing.T) {
	r := &fakeRunner{replies: map[string]string{
		"calendar create-calendar": fixture(t, "create-calendar.json"),
		"calendar events":          fixture(t, "events.json"),
	}}
	c := newCal(t, r, time.Date(2026, 11, 29, 9, 0, 0, 0, time.UTC))
	res, err := c.Reconcile(ctx, []Event{{SubscriptionID: "B", Date: "2026-12-10", Hash: "stale"}, {SubscriptionID: "C", Date: "2026-12-20", Hash: "whatever"}})
	if err != nil || res != (Result{}) {
		t.Fatalf("%+v %v", res, err)
	}
	if len(r.callsOf("calendar update"))+len(r.callsOf("calendar create"))+len(r.callsOf("calendar delete")) != 0 {
		t.Fatalf("calls %v", r.calls)
	}
}

func TestReconcileListFailureTouchesNothing(t *testing.T) {
	r := &fakeRunner{
		replies: map[string]string{"calendar create-calendar": fixture(t, "create-calendar.json")},
		errs:    map[string]error{"calendar events": errors.New("gog: insufficient scope (calendar)")},
	}
	now := time.Date(2026, 11, 29, 9, 0, 0, 0, time.UTC)
	c := newCal(t, r, now)
	_, err := c.Reconcile(ctx, []Event{{SubscriptionID: "A", Date: "2026-12-05", Hash: "h"}})
	if err == nil {
		t.Fatal("no error")
	}
	if len(r.callsOf("calendar create"))+len(r.callsOf("calendar delete"))+len(r.callsOf("calendar update")) != 0 {
		t.Fatalf("touched events after a failed list: %v", r.calls)
	}
	if _, lastErr := c.Status(); !strings.Contains(lastErr, "insufficient scope") {
		t.Fatalf("lastErr %q", lastErr)
	}
	if next := c.NextAttempt(); !next.Equal(now.Add(time.Minute)) {
		t.Fatalf("first backoff %v", next)
	}
	c.Reconcile(ctx, nil)
	if next := c.NextAttempt(); !next.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("second backoff %v", next)
	}
	for i := 0; i < 10; i++ {
		c.Reconcile(ctx, nil)
	}
	if next := c.NextAttempt(); !next.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("capped backoff %v", next)
	}
}

func TestReconcileMalformedListIsAFailure(t *testing.T) {
	r := &fakeRunner{replies: map[string]string{
		"calendar create-calendar": fixture(t, "create-calendar.json"),
		"calendar events":          `{"events": [`,
	}}
	c := newCal(t, r, time.Date(2026, 11, 29, 9, 0, 0, 0, time.UTC))
	if _, err := c.Reconcile(ctx, []Event{{SubscriptionID: "A", Hash: "h", Date: "2026-12-05"}}); err == nil {
		t.Fatal("malformed JSON accepted")
	}
	if len(r.callsOf("calendar create")) != 0 {
		t.Fatal("created after malformed list")
	}
}

func TestReconcileIgnoresEventsWithoutBitsubProp(t *testing.T) {
	r := &fakeRunner{replies: map[string]string{
		"calendar create-calendar": fixture(t, "create-calendar.json"),
		"calendar events":          fixture(t, "events.json"),
	}}
	c := newCal(t, r, time.Date(2026, 11, 29, 9, 0, 0, 0, time.UTC))
	if _, err := c.Reconcile(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, d := range r.callsOf("calendar delete") {
		if d[3] == "evMine" {
			t.Fatal("deleted an event bitsub does not own")
		}
	}
	if len(r.callsOf("calendar delete")) != 2 {
		t.Fatalf("deletes %v", r.callsOf("calendar delete"))
	}
}

func TestDesiredEventsSummaries(t *testing.T) {
	today := subs.MustDate("2026-11-29")
	rates := subs.Rates{Date: today, PerAUD: map[subs.Currency]float64{"USD": 0.645}}
	end := subs.MustDate("2026-12-02")
	list := []subs.Subscription{
		{ID: "t", Name: "Speechify", Price: subs.Money{Minor: 13900, Currency: "USD"}, Cycle: subs.Year, Interval: 1,
			Anchor: end, TrialEnd: &end, Intent: subs.Undecided, Status: subs.Active, URL: "https://speechify.com/account", PaymentLabel: "Visa ··21"},
		{ID: "r", Name: "Netflix", Price: subs.Money{Minor: 1899, Currency: "AUD"}, Cycle: subs.Month, Interval: 1,
			Anchor: subs.MustDate("2026-01-14"), Intent: subs.Keep, Status: subs.Active},
		{ID: "c", Name: "Stan", Price: subs.Money{Minor: 1700, Currency: "AUD"}, Cycle: subs.Month, Interval: 1,
			Anchor: subs.MustDate("2026-01-12"), Intent: subs.Cancel, NoticeDays: 2, Status: subs.Active},
	}
	got := DesiredEvents(list, rates, today)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	by := map[string]Event{}
	for _, e := range got {
		by[e.SubscriptionID] = e
	}
	if e := by["t"]; e.Summary != "Speechify trial ends · A$215.50/yr (US$139.00)" || e.Date != "2026-12-02" ||
		!slices.Equal(e.Reminders, []string{"popup:159h", "popup:39h", "popup:15h"}) ||
		!strings.Contains(e.Description, "https://speechify.com/account") || !strings.Contains(e.Description, "Visa ··21") ||
		!strings.Contains(e.Description, "Managed by bitsub — edits here are overwritten.") {
		t.Errorf("trial %+v", e)
	}
	if e := by["r"]; e.Summary != "Netflix renews · A$18.99/mo" || e.Date != "2026-12-14" || !slices.Equal(e.Reminders, []string{"popup:15h"}) {
		t.Errorf("renewal %+v", e)
	}
	if e := by["c"]; e.Summary != "Cancel Stan by Thu 10 Dec" || e.Date != "2026-12-10" ||
		!slices.Equal(e.Reminders, []string{"popup:63h", "popup:39h", "popup:15h"}) {
		t.Errorf("cancel %+v", e)
	}
	for _, e := range got {
		if len(e.Hash) != 64 {
			t.Errorf("hash %q", e.Hash)
		}
	}
	again := DesiredEvents(list, rates, today)
	list[1].Price.Minor = 1999
	changed := DesiredEvents(list, rates, today)
	if again[1].Hash != got[1].Hash || changed[1].Hash == got[1].Hash {
		t.Error("hash not a pure function of the content")
	}
}

func TestRemindersCappedAndInRange(t *testing.T) {
	s := subs.Subscription{ID: "x", Name: "X", Price: subs.Money{Minor: 100, Currency: "AUD"}, Cycle: subs.Year, Interval: 1,
		Anchor: subs.MustDate("2027-03-01"), Intent: subs.Undecided, Status: subs.Active, ReminderDays: []int{60, 30, 14, 7, 3}}
	e := DesiredEvents([]subs.Subscription{s}, subs.Rates{}, subs.MustDate("2026-11-29"))[0]
	// 60 and 30 days are beyond Google's 28-day reminder limit.
	if !slices.Equal(e.Reminders, []string{"popup:327h", "popup:159h", "popup:63h", "popup:15h"}) {
		t.Fatalf("%v", e.Reminders)
	}
}
