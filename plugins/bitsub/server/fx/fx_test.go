package fx

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
)

type fakeDoer struct {
	status int
	body   string
	err    error
	urls   []string
}

func (f *fakeDoer) Do(r *http.Request) (*http.Response, error) {
	f.urls = append(f.urls, r.URL.String())
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: f.status, Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

const good = `{"amount":1.0,"base":"AUD","date":"2026-11-28","rates":{"USD":0.645,"EUR":0.59}}`

func setup(t *testing.T, today string) (*store.Store, func() time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bitsub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now, _ := time.Parse(time.DateOnly, today)
	return st, func() time.Time { return now.Add(10 * time.Hour) }
}

func TestRefreshParsesAndPersists(t *testing.T) {
	st, now := setup(t, "2026-11-29")
	d := &fakeDoer{status: 200, body: good}
	r := New(st, d, "", now, time.UTC)
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.urls[0] != DefaultURL {
		t.Fatalf("url %s", d.urls[0])
	}
	got, ok := r.Current()
	if !ok || got.Date.String() != "2026-11-28" || got.PerAUD["USD"] != 0.645 || got.PerAUD["EUR"] != 0.59 {
		t.Fatalf("%+v %v", got, ok)
	}
	again, ok := New(st, &fakeDoer{err: io.EOF}, "", now, time.UTC).Current()
	if !ok || again.PerAUD["USD"] != 0.645 {
		t.Fatalf("not loaded from the store: %+v %v", again, ok)
	}
}

func TestRefreshFailureKeepsLastGood(t *testing.T) {
	st, now := setup(t, "2026-11-29")
	d := &fakeDoer{status: 200, body: good}
	r := New(st, d, "", now, time.UTC)
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*fakeDoer{
		{status: 500, body: "oops"},
		{status: 200, body: `{"base":"AUD","date":"2026-11-29","rates":{"USD":-1}}`},
		{status: 200, body: `{"base":"USD","date":"2026-11-29","rates":{"EUR":0.9}}`},
		{status: 200, body: `not json`},
		{status: 200, body: `{"base":"AUD","date":"2026-11-29","rates":{}}`},
		{status: 200, body: `{"base":"AUD","date":"29/11/2026","rates":{"USD":0.6}}`},
		{err: io.ErrUnexpectedEOF},
	} {
		*d = *bad
		if err := r.Refresh(context.Background()); err == nil {
			t.Errorf("accepted %+v", bad)
		}
		got, ok := r.Current()
		if !ok || got.PerAUD["USD"] != 0.645 {
			t.Fatalf("lost last good set after %+v: %+v", bad, got)
		}
		if _, _, lastErr := r.Status(); lastErr == "" {
			t.Errorf("no lastErr after %+v", bad)
		}
	}
	*d = fakeDoer{status: 200, body: good}
	r.Refresh(context.Background())
	if _, _, lastErr := r.Status(); lastErr != "" {
		t.Errorf("lastErr kept after success: %q", lastErr)
	}
}

func TestRefreshBodyCapped(t *testing.T) {
	st, now := setup(t, "2026-11-29")
	huge := `{"base":"AUD","date":"2026-11-28","rates":{"USD":0.645},"pad":"` + strings.Repeat("x", 70<<10) + `"}`
	r := New(st, &fakeDoer{status: 200, body: huge}, "", now, time.UTC)
	if err := r.Refresh(context.Background()); err == nil {
		t.Fatal("oversized body accepted")
	}
}

func TestNoRatesYet(t *testing.T) {
	st, now := setup(t, "2026-11-29")
	r := New(st, &fakeDoer{}, "", now, time.UTC)
	if _, ok := r.Current(); ok {
		t.Fatal("rates before any fetch")
	}
	if _, stale, _ := r.Status(); !stale {
		t.Fatal("no rates should read as stale")
	}
}

func TestStaleAfterSevenDays(t *testing.T) {
	st, now := setup(t, "2026-11-29")
	r := New(st, &fakeDoer{status: 200, body: good}, "", now, time.UTC)
	r.Refresh(context.Background())
	if _, stale, _ := r.Status(); stale {
		t.Fatal("1-day-old rates stale")
	}
	_, later := setup(t, "2026-12-06") // 8 days after 11-28
	r2 := New(st, &fakeDoer{}, "", later, time.UTC)
	if date, stale, _ := r2.Status(); !stale || date.String() != "2026-11-28" {
		t.Fatalf("8-day-old rates: %s stale=%v", date, stale)
	}
}
