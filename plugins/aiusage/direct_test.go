package aiusage

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// directEnv builds a test Env: httptest's client, a temp home, a frozen
// clock, injected environment, and optional pasted keys.
func directEnv(home string, getenv func(string) string, keys map[string]string) Env {
	return Env{
		Client: http.DefaultClient,
		Home:   home,
		Now:    func() time.Time { return base },
		Env:    getenv,
		Keys:   keys,
	}
}

// frozenNow is Wednesday 2026-09-16 noon UTC, so nextWeeklyReset lands on
// the 21st.
var frozenNow = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func TestCommandCodeCollector(t *testing.T) {
	t.Parallel()

	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Authorization")
		if r.URL.Path != "/alpha/billing/credits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"windowLimits":{"fiveHour":{"used":42,"cap":100,"resetsAt":1758033600000},"weekly":{"used":10,"cap":50,"resetsAt":1758528000000}},"credits":{"monthlyCredits":12.5}}`))
	}))
	defer srv.Close()

	c := &commandcodeCollector{env: directEnv("",
		func(key string) string { return "env-key" }, nil)}
	c.env.Client = srv.Client()
	c.base = srv.URL

	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh || rep.ID != "commandcode" {
		t.Fatalf("report = %+v", rep)
	}
	if gotKey != "Bearer env-key" {
		t.Errorf("authorization = %q", gotKey)
	}
	if rep.Windows[0].Key != "primary" || rep.Windows[0].UsedPercent != 42 {
		t.Fatalf("primary = %+v", rep.Windows[0])
	}
	if rep.Windows[0].DisplayValue != "$42 / $100" {
		t.Fatalf("display = %q", rep.Windows[0].DisplayValue)
	}
	if rep.Windows[0].WindowMinutes != 300 || rep.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("primary window = %+v", rep.Windows[0])
	}
	if rep.Windows[1].Key != "secondary" || rep.Windows[1].UsedPercent != 20 {
		t.Fatalf("secondary = %+v", rep.Windows[1])
	}
	if rep.Credits == nil || *rep.Credits != 12.5 {
		t.Fatalf("credits = %+v", rep.Credits)
	}
}

func TestCommandCodeKeyChain(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	c := &commandcodeCollector{env: directEnv(home, func(string) string { return "" }, nil)}
	c.base = "https://unused.invalid"

	_, err := c.Fetch(t.Context())
	var setup *ErrSetup
	if !asSetup(err, &setup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
	if len(setup.Tried) != 3 {
		t.Fatalf("tried = %v, want settings, env, auth file", setup.Tried)
	}

	// The CLI-owned auth file is the last link in the chain.
	authDir := filepath.Join(home, ".commandcode")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"apiKey":"file-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key, setupErr := c.key()
	if setupErr != nil || key != "file-key" {
		t.Fatalf("key = %q, %v; want the auth file", key, setupErr)
	}
}

func TestOllamaFractionsAndWeeklyReset(t *testing.T) {
	t.Parallel()

	var meFailing bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/usage":
			if r.Method != http.MethodGet {
				t.Errorf("usage method = %s", r.Method)
			}
			w.Write([]byte(`{"limits":{"session":{"usage":0.12},"weekly":{"usage":0.34}}}`))
		case "/api/me":
			if meFailing {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`{"plan":"free","email":"n@example.com"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := &ollamaCollector{env: directEnv("", func(string) string { return "" },
		map[string]string{"ollama": "pasted-key"})}
	c.base = srv.URL

	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh || rep.Plan != "Free" || rep.Account != "n@example.com" {
		t.Fatalf("report = %+v", rep)
	}
	session, weekly := rep.Windows[0], rep.Windows[1]
	if session.UsedPercent != 12 || session.WindowMinutes != 0 || session.Label != "Session usage" {
		t.Fatalf("session = %+v", session)
	}
	if weekly.UsedPercent != 34 || weekly.WindowMinutes != 10080 {
		t.Fatalf("weekly = %+v", weekly)
	}
	// Wednesday noon UTC: the next weekly boundary is the coming Monday 00:00.
	if want := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC); !weekly.ResetsAt.Equal(want) {
		t.Fatalf("weekly reset = %v, want %v", weekly.ResetsAt, want)
	}

	// A failing identity probe must not fail the refresh.
	meFailing = true
	rep, err = c.Fetch(t.Context())
	if err != nil || rep.State != StateFresh {
		t.Fatalf("me failure surfaced: %+v, %v", rep, err)
	}
}

func TestNextWeeklyResetTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		now  time.Time
		want time.Time
	}{
		// Saturday noon → Monday 00:00 (+2 days).
		{time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)},
		// Sunday noon → Monday 00:00 (+1 day).
		{time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)},
		// Monday noon → the NEXT Monday (+7): the boundary at midnight is past.
		{time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC), time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
		// Monday exactly at the boundary rolls a full week.
		{time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		if got := nextWeeklyReset(tc.now); !got.Equal(tc.want) {
			t.Errorf("nextWeeklyReset(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}

func TestOllamaMissingKey(t *testing.T) {
	t.Parallel()

	c := &ollamaCollector{env: directEnv("", func(string) string { return "" }, nil)}
	c.base = "https://unused.invalid"
	_, err := c.Fetch(t.Context())
	var setup *ErrSetup
	if !asSetup(err, &setup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
	if len(setup.Tried) != 2 {
		t.Fatalf("tried = %v", setup.Tried)
	}
}

func TestMinimaxRemainsAndQuirks(t *testing.T) {
	t.Parallel()

	// The general entry is not first, the video entry rides along and is
	// ignored, and both remainders invert into used percents.
	payload := `{"model_remains":[
		{"model_name":"video","current_interval_remaining_percent":100,"current_weekly_remaining_percent":100,
		 "end_time":1758033600000,"weekly_end_time":1758528000000},
		{"model_name":"general","current_interval_remaining_percent":61,"current_weekly_remaining_percent":93,
		 "end_time":1758033600000,"weekly_end_time":1758528000000}],
		"base_resp":{"status_code":0,"status_msg":"success"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/openplatform/coding_plan/remains" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	home := t.TempDir()
	c := &minimaxCollector{env: directEnv(home, func(string) string { return "" },
		map[string]string{"minimax": "test-key"})}
	c.base = srv.URL

	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Windows[0].Key != "primary" || rep.Windows[0].UsedPercent != 39 || rep.Windows[0].WindowMinutes != 300 {
		t.Fatalf("primary = %+v, want the inverted interval remainder", rep.Windows[0])
	}
	if rep.Windows[1].Key != "secondary" || rep.Windows[1].UsedPercent != 7 || rep.Windows[1].WindowMinutes != 10080 {
		t.Fatalf("secondary = %+v, want the inverted weekly remainder", rep.Windows[1])
	}
	if want := time.UnixMilli(1758033600000).UTC(); !rep.Windows[0].ResetsAt.Equal(want) {
		t.Fatalf("interval reset = %v, want the ms instant", rep.Windows[0].ResetsAt)
	}
}

func TestMinimaxStatusFaultAndBorrow(t *testing.T) {
	t.Parallel()

	// base_resp carries the real outcome even on HTTP 200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"invalid api key"},"model_remains":[]}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	c := &minimaxCollector{env: directEnv(home, func(string) string { return "" },
		map[string]string{"minimax": "test-key"})}
	c.base = srv.URL
	rep, err := c.Fetch(t.Context())
	if err == nil || rep.State != StateFault || rep.Err != "invalid api key" {
		t.Fatalf("status fault = %+v, %v", rep, err)
	}

	// The sibling app's stored key is the last link in the chain — for a
	// collector with no pasted key of its own.
	borrowDir := filepath.Join(home, ".config", "codexbar")
	if err := os.MkdirAll(borrowDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(borrowDir, "config.json"),
		[]byte(`{"providers":[{"id":"other","apiKey":"x"},{"id":"minimax","apiKey":"borrowed"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c2 := &minimaxCollector{env: directEnv(home, func(string) string { return "" }, nil)}
	key, setupErr := c2.key()
	if setupErr != nil || key != "borrowed" {
		t.Fatalf("key = %q, %v; want the borrowed key", key, setupErr)
	}
	c2.base = srv.URL
	if _, err2 := c2.Fetch(t.Context()); err2 != nil && strings.Contains(err2.Error(), "setup required") {
		t.Fatalf("borrowed key still produced setup: %v", err2)
	}
}

func TestMinimaxKeyRejectedFault(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := &minimaxCollector{env: directEnv("", func(string) string { return "" },
		map[string]string{"minimax": "stale"})}
	c.base = srv.URL
	rep, err := c.Fetch(t.Context())
	if err == nil || rep.State != StateFault {
		t.Fatalf("rejected key = %+v, %v", rep, err)
	}
	if !strings.Contains(rep.Err, "key rejected") {
		t.Fatalf("message = %q", rep.Err)
	}
}

func TestCopilotQuotaSnapshots(t *testing.T) {
	t.Parallel()

	var gotAuth, gotEditor, gotAPIVer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEditor = r.Header.Get("Editor-Version")
		gotAPIVer = r.Header.Get("X-Github-Api-Version")
		if r.URL.Path != "/copilot_internal/user" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"login":"octocat","copilot_plan":"individual","access_type_sku":"copilot_pro",
			"quota_reset_date_utc":"2026-10-01T00:00:00Z","token_based_billing":true,
			"quota_snapshots":{
				"premium_interactions":{"percent_remaining":62,"overage_count":0},
				"chat":{"unlimited":true,"entitlement":0},
				"completions":{"entitlement":300,"remaining":120,"overage_count":2}
			}}`))
	}))
	defer srv.Close()

	c := &copilotCollector{env: directEnv("", func(name string) string {
		if name == "GITHUB_TOKEN" {
			return "gh-token"
		}
		return ""
	}, nil), base: srv.URL, gh: func() string { return "" }}

	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh || rep.ID != "copilot" {
		t.Fatalf("report = %+v", rep)
	}
	if gotAuth != "token gh-token" || gotEditor == "" || gotAPIVer == "" {
		t.Fatalf("headers = %q / %q / %q", gotAuth, gotEditor, gotAPIVer)
	}
	// Premium: 100 − 62 remaining = 38% used.
	if rep.Windows[0].Key != "primary" || rep.Windows[0].UsedPercent != 38 {
		t.Fatalf("premium = %+v", rep.Windows[0])
	}
	if !strings.Contains(rep.Windows[0].Label, "AI credits") {
		t.Fatalf("premium label = %q, want the token-billing variant", rep.Windows[0].Label)
	}
	// Unlimited-with-zero-entitlement chat is dropped; completions use the
	// entitlement math: (300−120)/300 = 60%.
	if len(rep.Windows) != 2 {
		t.Fatalf("windows = %d, want premium + completions", len(rep.Windows))
	}
	if rep.Windows[1].UsedPercent != 60 || !strings.Contains(rep.Windows[1].DisplayValue, "+2 overage") {
		t.Fatalf("completions = %+v", rep.Windows[1])
	}
	if rep.Plan != "Pro" || !strings.Contains(rep.Account, "octocat") {
		t.Fatalf("plan/account = %q / %q", rep.Plan, rep.Account)
	}
}

func TestCopilotMissingTokenIsSetup(t *testing.T) {
	t.Parallel()

	c := &copilotCollector{env: directEnv("", func(string) string { return "" }, nil),
		base: "https://unused.invalid", gh: func() string { return "" }}
	rep, err := c.Fetch(t.Context())
	var setup *ErrSetup
	if !asSetup(err, &setup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
	if len(setup.Tried) < 3 {
		t.Fatalf("tried = %v", setup.Tried)
	}
	if rep.State != StateNeedsSetup {
		t.Fatalf("state = %v", rep.State)
	}
}

func TestExportCSV(t *testing.T) {
	t.Parallel()

	env, _ := loopEnv()
	fc := &fakeCollector{id: "alpha", rep: freshRep("alpha")}
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(fc), testConfig(), env, cache, history)
	l.Round(t.Context(), false)

	outDir := t.TempDir()
	path, err := l.ExportCSV(outDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "timestamp_iso,") {
		t.Fatalf("csv = %q", lines)
	}
	if !strings.Contains(lines[1], "alpha,primary,40") {
		t.Fatalf("csv row = %q", lines[1])
	}

	// A torn history line drops one record instead of aborting.
	if err := os.WriteFile(history, []byte(`{"ts":1,"provider":"alpha","window":"primary","pct":10}`+"\nGARBAGE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err = l.ExportCSV(outDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if got := strings.Count(string(raw), "\n"); got != 2 {
		t.Fatalf("loss-resilient export = %d lines, want header + the one valid record", got)
	}
}
