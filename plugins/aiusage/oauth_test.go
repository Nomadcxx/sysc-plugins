package aiusage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeCredentials lays down a CLI credentials file with the given token.
func writeCredentials(t *testing.T, home, token string, expiresAtMS int64) {
	t.Helper()
	file := `{"claudeAiOauth":{"accessToken":"` + token + `","expiresAt":` +
		strconv.FormatInt(expiresAtMS, 10) + `}}`
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
}

func asSetup(err error, target **ErrSetup) bool {
	var e *ErrSetup
	if errors.As(err, &e) {
		*target = e
		return true
	}
	return false
}

func TestOAuthLimitsShapePreferred(t *testing.T) {
	t.Parallel()

	var gotAuth, gotBeta, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotBeta, gotUA = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta"), r.Header.Get("User-Agent")
		if r.URL.Path != "/api/oauth/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"limits":[{"kind":"session","utilization":49.7,"resets_at":"2026-09-19T15:30:00Z","is_active":true},{"kind":"weekly_all","utilization":9.0,"resets_at":"2026-09-22T02:00:00Z"},{"kind":"weekly_scoped","utilization":12.0,"resets_at":"2026-09-22T02:00:00Z"}]}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	writeCredentials(t, home, "tok-a", base.Add(time.Hour).UnixMilli())
	c := &oauthUsageCollector{
		env:     Env{Client: srv.Client(), Home: home, Now: func() time.Time { return base }},
		base:    srv.URL,
		version: "2.1.0",
	}

	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh || rep.ID != "claude" {
		t.Fatalf("report = %+v", rep)
	}
	if gotAuth != "Bearer tok-a" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotBeta != "oauth-2025-04-20" {
		t.Errorf("beta header = %q", gotBeta)
	}
	if !strings.HasPrefix(gotUA, "claude-code/") {
		t.Errorf("user agent = %q", gotUA)
	}
	if len(rep.Windows) != 3 {
		t.Fatalf("windows = %d, want 3", len(rep.Windows))
	}
	if rep.Windows[0].Key != "primary" || rep.Windows[0].UsedPercent != 49 {
		t.Fatalf("primary = %+v, want utilization floored to 49", rep.Windows[0])
	}
	if rep.Windows[0].WindowMinutes != 300 || rep.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("primary window = %+v", rep.Windows[0])
	}
	if rep.Windows[1].Key != "secondary" || rep.Windows[2].Key != "tertiary" {
		t.Fatalf("weekly windows = %+v / %+v", rep.Windows[1], rep.Windows[2])
	}
}

func TestOAuthFlatFallback(t *testing.T) {
	t.Parallel()

	flat, err := os.ReadFile("testdata/oauth-usage-flat.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(flat)
	}))
	defer srv.Close()

	home := t.TempDir()
	writeCredentials(t, home, "tok-a", 0) // expired: still a best-effort fallback
	c := &oauthUsageCollector{
		env:     Env{Client: srv.Client(), Home: home, Now: func() time.Time { return base }},
		base:    srv.URL,
		version: "2.1.0",
	}

	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh {
		t.Fatalf("expired token must still be tried: %+v", rep)
	}
	if len(rep.Windows) != 2 || rep.Windows[0].UsedPercent != 40 || rep.Windows[1].UsedPercent != 9 {
		t.Fatalf("flat windows = %+v", rep.Windows)
	}
}

func TestOAuthErrorBodyAndAuthFaults(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCredentials(t, home, "tok-a", base.Add(time.Hour).UnixMilli())
	body := `{"error":{"type":"rate_limit_error","message":"slow down"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := &oauthUsageCollector{
		env:     Env{Client: srv.Client(), Home: home, Now: func() time.Time { return base }},
		base:    srv.URL,
		version: "2.1.0",
	}
	rep, err := c.Fetch(t.Context())
	if err == nil {
		t.Fatal("an error body must fault")
	}
	if rep.State != StateFault {
		t.Fatalf("state = %v, want fault", rep.State)
	}

	srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv401.Close()
	c.base = srv401.URL
	rep, err = c.Fetch(t.Context())
	if err == nil {
		t.Fatal("a 401 must fault")
	}
	if rep.State != StateFault || !strings.Contains(rep.Err, "sign-in expired") {
		t.Fatalf("401 = %+v, %v", rep, err)
	}
}

func TestOAuthMissingCredentialsIsSetup(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	c := &oauthUsageCollector{
		env:     Env{Home: home, Now: func() time.Time { return base }},
		base:    "https://unused.invalid",
		version: "2.1.0",
	}
	rep, err := c.Fetch(t.Context())
	var setup *ErrSetup
	if !asSetup(err, &setup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
	if rep.State != StateNeedsSetup {
		t.Fatalf("state = %v, want needs-setup", rep.State)
	}
	if len(setup.Tried) != 1 || !strings.HasSuffix(setup.Tried[0], filepath.Join(".claude", ".credentials.json")) {
		t.Fatalf("tried = %v", setup.Tried)
	}
}
