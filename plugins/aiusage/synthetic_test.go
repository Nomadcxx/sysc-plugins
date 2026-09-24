package aiusage

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The fixture is the payload exactly as documented at
// dev.synthetic.new/docs/synthetic/quotas (fetched 2026-09-20); the live
// capture with a real key replaces it during deployment verification (P11).
func TestSyntheticQuotas(t *testing.T) {
	t.Parallel()

	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Authorization")
		if r.URL.Path != "/v2/quotas" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"subscription":{"limit":135,"requests":41,"renewsAt":"2026-09-21T14:36:14.288Z"}}`))
	}))
	defer srv.Close()

	c := &syntheticCollector{
		env: directEnv("", func(string) string { return "" },
			map[string]string{"synthetic": "doc-key"}),
		base: srv.URL,
	}
	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh || rep.ID != "synthetic" {
		t.Fatalf("report = %+v", rep)
	}
	if gotKey != "Bearer doc-key" {
		t.Errorf("authorization = %q", gotKey)
	}
	if len(rep.Windows) != 1 {
		t.Fatalf("windows = %d", len(rep.Windows))
	}
	w := rep.Windows[0]
	if want := 41.0 / 135.0 * 100; w.UsedPercent != want {
		t.Fatalf("percent = %v, want %v", w.UsedPercent, want)
	}
	if !w.HasPercent || w.WindowMinutes != 0 {
		t.Fatalf("window = %+v", w)
	}
	if w.DisplayValue != "41 / 135 requests" {
		t.Fatalf("display = %q", w.DisplayValue)
	}
	want, _ := time.Parse(time.RFC3339Nano, "2026-09-21T14:36:14.288Z")
	if !w.ResetsAt.Equal(want.UTC()) {
		t.Fatalf("renew = %v, want %v", w.ResetsAt, want.UTC())
	}
}

func TestSyntheticMissingKeyAndZeroLimit(t *testing.T) {
	t.Parallel()

	c := &syntheticCollector{
		env:  directEnv("", func(string) string { return "" }, nil),
		base: "https://unused.invalid",
	}
	rep, err := c.Fetch(t.Context())
	var setup *ErrSetup
	if !asSetup(err, &setup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
	if rep.State != StateNeedsSetup {
		t.Fatalf("state = %v", rep.State)
	}
	if len(setup.Tried) != 3 {
		t.Fatalf("tried = %v", setup.Tried)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"subscription":{"limit":0,"requests":0,"renewsAt":"2026-09-21T14:36:14.288Z"}}`))
	}))
	defer srv.Close()
	c2 := &syntheticCollector{
		env: directEnv("", func(string) string { return "" },
			map[string]string{"synthetic": "k"}),
		base: srv.URL,
	}
	rep, err = c2.Fetch(t.Context())
	if err == nil || rep.State != StateFault {
		t.Fatalf("zero limit = %+v, %v; want a fault", rep, err)
	}
}

func TestSyntheticMissingRequestsIsFault(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"subscription":{"limit":135}}`))
	}))
	defer srv.Close()

	c := &syntheticCollector{
		env:  directEnv("", func(string) string { return "" }, map[string]string{"synthetic": "k"}),
		base: srv.URL,
	}
	rep, err := c.Fetch(t.Context())
	if err == nil || rep.State != StateFault {
		t.Fatalf("missing request usage = %+v, %v; want a fault", rep, err)
	}
}
