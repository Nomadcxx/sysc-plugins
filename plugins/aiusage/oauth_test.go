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
	if len(setup.Tried) != 2 {
		t.Fatalf("tried = %v", setup.Tried)
	}
}

func TestOAuthFallbackUsesSchedulerFloorBetweenCredentials(t *testing.T) {
	home := t.TempDir()
	writeCredentials(t, home, "claude-token", base.Add(time.Hour).UnixMilli())
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	opencodeAuth := `{"anthropic":{"type":"oauth","access":"opencode-token","expires":` + strconv.FormatInt(base.Add(time.Hour).Unix(), 10) + `}}`
	if err := os.WriteFile(filepath.Join(dataDir, "opencode", "auth.json"), []byte(opencodeAuth), 0o600); err != nil {
		t.Fatal(err)
	}

	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"five_hour":{"utilization":12}}`))
	}))
	defer srv.Close()

	env, now := loopEnv()
	env.Client = srv.Client()
	env.Home = home
	env.Env = func(key string) string {
		if key == "XDG_DATA_HOME" {
			return dataDir
		}
		return ""
	}
	cfg := testConfig()
	cfg.Track = map[string]bool{"claude": true}
	reg := Registry{"claude": func(e Env) Collector {
		return &oauthUsageCollector{env: e, base: srv.URL, version: "2.1.0"}
	}}
	cache, history := loopPaths(t)
	l := NewLoop(reg, cfg, env, cache, history)

	first := l.Round(t.Context(), false)
	if len(tokens) != 1 || tokens[0] != "Bearer claude-token" {
		t.Fatalf("first round requests = %v; want one Claude-token request", tokens)
	}
	if len(first.Providers) != 1 || first.Providers[0].State != StateFault {
		t.Fatalf("first report = %+v; want first credential fault", first.Providers)
	}

	*now = base.Add(179 * time.Second)
	deferred := l.Round(t.Context(), true)
	if len(tokens) != 1 {
		t.Fatalf("forced refresh bypassed Claude floor: requests = %v", tokens)
	}
	if len(deferred.Providers) != 1 || !deferred.Providers[0].DeferredUntil.Equal(base.Add(180*time.Second)) {
		t.Fatalf("deferred report = %+v; want defer until floor", deferred.Providers)
	}

	*now = base.Add(180 * time.Second)
	final := l.Round(t.Context(), true)
	if len(tokens) != 2 || tokens[1] != "Bearer opencode-token" {
		t.Fatalf("fallback requests = %v; want OpenCode token after the floor", tokens)
	}
	if len(final.Providers) != 1 || final.Providers[0].State != StateFresh {
		t.Fatalf("fallback report = %+v; want fresh", final.Providers)
	}

	*now = base.Add(360 * time.Second)
	again := l.Round(t.Context(), false)
	if len(tokens) != 3 || tokens[2] != "Bearer opencode-token" {
		t.Fatalf("post-fallback requests = %v; want the successful source retained", tokens)
	}
	if len(again.Providers) != 1 || again.Providers[0].State != StateFresh {
		t.Fatalf("post-fallback report = %+v; want fresh", again.Providers)
	}
}
