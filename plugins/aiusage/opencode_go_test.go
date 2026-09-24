package aiusage

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeGoFetchParsesOfficialUsage(t *testing.T) {
	now := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	resets := []string{"2026-09-24T05:00:00Z", "2026-09-28T00:00:00Z", "2026-10-01T00:00:00Z"}
	called := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		if r.Method != http.MethodGet || r.URL.Path != "/zen/go/v1/usage" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Errorf("authorization header was not set from configured key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{
			"rolling":{"status":"ok","percent":27,"resetsAt":"` + resets[0] + `"},
			"weekly":{"status":"ok","percent":41,"resetsAt":"` + resets[1] + `"},
			"monthly":{"status":"rate-limited","percent":100,"resetsAt":"` + resets[2] + `"}
		}}`))
	}))
	defer srv.Close()
	c := &openCodeGoCollector{
		env:  Env{Client: srv.Client(), Keys: map[string]string{"opencode-go": "fixture-key"}, Now: func() time.Time { return now }},
		base: srv.URL,
	}

	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	default:
		t.Fatal("usage endpoint was not called")
	}
	if got.State != StateFresh || got.ID != "opencode-go" || got.Name != "OpenCode Go" {
		t.Fatalf("unexpected report: %+v", got)
	}
	want := []struct {
		key, label, short, reset string
		percent                  float64
		display                  string
	}{
		{"primary", "Rolling", "Roll", resets[0], 27, ""},
		{"secondary", "Weekly", "Wk", resets[1], 41, ""},
		{"tertiary", "Monthly", "Mo", resets[2], 100, "Rate limited"},
	}
	if len(got.Windows) != len(want) {
		t.Fatalf("got %d windows, want %d", len(got.Windows), len(want))
	}
	for i, w := range got.Windows {
		target := want[i]
		reset, _ := time.Parse(time.RFC3339, target.reset)
		if w.Key != target.key || w.Label != target.label || w.ShortLabel != target.short ||
			!w.HasPercent || w.UsedPercent != target.percent || w.WindowMinutes != 0 ||
			!w.ResetsAt.Equal(reset) || w.DisplayValue != target.display {
			t.Errorf("window %d = %+v, want %+v", i, w, target)
		}
	}
}

func TestOpenCodeGoNoSubscriptionIsExplainedWithoutLeakingBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"OpenCode Go subscription required; account=private-user"}`))
	}))
	defer srv.Close()
	c := &openCodeGoCollector{env: Env{Client: srv.Client(), Keys: map[string]string{"opencode-go": "fixture-key"}}, base: srv.URL}

	got, err := c.Fetch(context.Background())
	if err != nil || got.State != StateNoData || !strings.Contains(got.Err, "subscription required") {
		t.Fatalf("report = %+v, error = %v; want explained no-data state", got, err)
	}
	if strings.Contains(got.Err, "private-user") {
		t.Fatalf("response body leaked into report: %q", got.Err)
	}
}

func TestOpenCodeGoRejectsIncompleteOrInvalidUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{
			"rolling":{"status":"ok","resetsAt":"2026-09-24T05:00:00Z"},
			"weekly":{"status":"ok","percent":41,"resetsAt":"2026-09-28T00:00:00Z"},
			"monthly":{"status":"ok","percent":140,"resetsAt":"2026-10-01T00:00:00Z"}
		}}`))
	}))
	defer srv.Close()
	c := &openCodeGoCollector{env: Env{Client: srv.Client(), Keys: map[string]string{"opencode-go": "fixture-key"}}, base: srv.URL}

	got, err := c.Fetch(context.Background())
	if err == nil || got.State != StateFault || got.Err == "" {
		t.Fatalf("report = %+v, error = %v; want invalid quota fault", got, err)
	}
}

func TestOpenCodeGoAPIKeyResolutionIsOrderedAndReadOnly(t *testing.T) {
	home := t.TempDir()
	authPath := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"opencode":{"type":"api","key":"stored-key"}}`)
	if err := os.WriteFile(authPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(name string) string {
		if name == "OPENCODE_GO_API_KEY" {
			return "environment-key"
		}
		return ""
	}
	c := &openCodeGoCollector{env: Env{Home: home, Env: env, Keys: map[string]string{"opencode-go": "settings-key"}}}
	for _, tc := range []struct {
		name string
		env  Env
		want string
	}{
		{"settings", c.env, "settings-key"},
		{"environment", Env{Home: home, Env: env}, "environment-key"},
		{"OpenCode auth store", Env{Home: home}, "stored-key"},
	} {
		c.env = tc.env
		got, setup := c.apiKey()
		if setup != nil || got != tc.want {
			t.Errorf("%s source: key=%q setup=%v, want key present", tc.name, got, setup)
		}
	}

	// Reads are deliberately non-mutating: the key resolution path only opens
	// the store for reading and leaves its exact contents intact.
	contents, err := os.ReadFile(authPath)
	if err != nil || !bytes.Equal(contents, original) {
		t.Fatal("OpenCode auth fixture changed during key resolution")
	}
}
