package aiusage

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKimiUsagesMap(t *testing.T) {
	t.Parallel()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usages":{
			"limit_5h":{"used_ratio":0.5,"reset_time":"2026-09-19T15:00:00Z"},
			"limit_7d":{"used_ratio":0.25}
		},"user":{"membership":{"level":"LEVEL_ADVANCED"}}}`))
	}))
	defer srv.Close()
	c := &kimiCollector{env: directEnv(t.TempDir(), nil, map[string]string{"kimi": "k1"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k1" {
		t.Errorf("auth = %q", auth)
	}
	if rep.State != StateFresh || rep.Plan != "Allegro" {
		t.Fatalf("state=%v plan=%q", rep.State, rep.Plan)
	}
	if len(rep.Windows) != 2 {
		t.Fatalf("windows = %+v", rep.Windows)
	}
	p := rep.Windows[0]
	if p.Key != "primary" || p.Label != "Session" || p.UsedPercent != 50 || p.ResetsAt.IsZero() {
		t.Errorf("primary = %+v", p)
	}
	s := rep.Windows[1]
	if s.Key != "secondary" || s.Label != "Weekly" || s.UsedPercent != 25 {
		t.Errorf("secondary = %+v", s)
	}
}

func TestKimiCountedFallback(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"limit":"200","used":"139","resetTime":"2026-09-19T15:00:00Z"},
			"limits":[{"window":{"duration":5,"timeUnit":"TIME_UNIT_HOUR"},
				"detail":{"limit":"20","remaining":"3"}}]}`))
	}))
	defer srv.Close()
	c := &kimiCollector{env: directEnv(t.TempDir(), nil, map[string]string{"kimi": "k1"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Windows) != 2 {
		t.Fatalf("windows = %+v", rep.Windows)
	}
	if rep.Windows[0].WindowMinutes != 300 || rep.Windows[0].UsedPercent != 85 {
		t.Errorf("5h window = %+v", rep.Windows[0])
	}
	if rep.Windows[1].WindowMinutes != 10080 || rep.Windows[1].UsedPercent != 69.5 {
		t.Errorf("weekly window = %+v", rep.Windows[1])
	}
}

func TestKimiFaults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer bad" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &kimiCollector{env: directEnv(t.TempDir(), nil, map[string]string{"kimi": "bad"}), base: srv.URL}
	if rep, _ := c.Fetch(t.Context()); rep.State != StateFault || rep.Err == "" {
		t.Errorf("401: %+v", rep)
	}
	c.env = directEnv(t.TempDir(), nil, map[string]string{"kimi": "good"})
	rep, err := c.Fetch(t.Context())
	if err == nil || rep.State != StateFault || rep.Err != "the usage endpoint carried no quota windows" {
		t.Errorf("empty: %+v %v", rep, err)
	}
	c.env = directEnv(t.TempDir(), func(string) string { return "" }, nil)
	if rep, _ := c.Fetch(t.Context()); rep.State != StateNeedsSetup {
		t.Errorf("no key: %+v", rep)
	}
}

func TestZAIQuota(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":{
			"limits":[
				{"type":"CREDIT_LIMIT","unit":6,"number":1,"percentage":10,"nextResetTime":1790000000000},
				{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":40,"usage":1000000,"remaining":400000},
				{"type":"TIME_LIMIT","unit":1,"number":30,"percentage":1}
			],"planName":"pro"}}`))
	}))
	defer srv.Close()
	c := &zaiCollector{env: directEnv(t.TempDir(), nil, map[string]string{"zai": "z1"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh || rep.Plan != "Pro" {
		t.Fatalf("state=%v plan=%q", rep.State, rep.Plan)
	}
	if len(rep.Windows) != 2 {
		t.Fatalf("windows = %+v", rep.Windows)
	}
	p := rep.Windows[0]
	if p.Key != "primary" || p.WindowMinutes != 300 || p.UsedPercent != 60 || p.DisplayValue != "600000 / 1000000" {
		t.Errorf("primary = %+v", p)
	}
	if rep.Windows[1].Key != "secondary" || rep.Windows[1].WindowMinutes != 10080 || rep.Windows[1].UsedPercent != 10 {
		t.Errorf("secondary = %+v", rep.Windows[1])
	}
}

func TestZAIEnvelopeFault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"code":429,"msg":"quota exhausted"}`))
	}))
	defer srv.Close()
	c := &zaiCollector{env: directEnv(t.TempDir(), nil, map[string]string{"zai": "z1"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err == nil || rep.State != StateFault || rep.Err != "quota exhausted" {
		t.Errorf("%+v %v", rep, err)
	}
}

func TestDeepSeekBalance(t *testing.T) {
	t.Parallel()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	env := directEnv(t.TempDir(), nil, map[string]string{"deepseek": "d1"})
	c := &deepseekCollector{env: env, base: srv.URL}

	body = `{"is_available":true,"balance_infos":[
		{"currency":"CNY","total_balance":"10.00"},
		{"currency":"USD","total_balance":"5.50"}]}`
	rep, err := c.Fetch(t.Context())
	if err != nil || rep.State != StateFresh {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep.Credits == nil || *rep.Credits != 5.5 || rep.Account != "USD" || len(rep.Windows) != 0 {
		t.Errorf("rep = %+v credits=%v", rep, rep.Credits)
	}

	body = `{"is_available":false,"balance_infos":[{"currency":"USD","total_balance":0}]}`
	rep, _ = c.Fetch(t.Context())
	if rep.Account != "USD · balance unavailable for API calls" {
		t.Errorf("account = %q", rep.Account)
	}

	body = `{"is_available":true,"balance_infos":[]}`
	rep, err = c.Fetch(t.Context())
	if err == nil || rep.State != StateFault {
		t.Errorf("empty wallets: %+v %v", rep, err)
	}
}

func TestAlibabaTokenPlan(t *testing.T) {
	t.Parallel()
	var gotCookie, form string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		gotCookie = r.Header.Get("Cookie")
		_ = r.ParseForm()
		form = r.FormValue("product") + "|" + r.FormValue("action") + "|" + r.FormValue("region")
		_, _ = w.Write([]byte(alibabaTestEnvelope(`{
			"per5HourPercentage":0.25,"per5HourResetTime":1790000000000,
			"per1WeekPercentage":0.5,"per1WeekResetTime":1790000000000,
			"per1MonthPercentage":0.1,"per1MonthResetTime":1790000000000}`)))
	}))
	defer srv.Close()
	c := &alibabaCollector{env: directEnv(t.TempDir(), nil, map[string]string{"alibaba": "login_a=1; cna=2"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "login_a=1; cna=2" {
		t.Errorf("cookie = %q", gotCookie)
	}
	if form != "sfm_bailian|IntlBroadScopeAspnGateway|ap-southeast-1" {
		t.Errorf("form = %q", form)
	}
	if len(rep.Windows) != 3 {
		t.Fatalf("windows = %+v", rep.Windows)
	}
	want := []struct {
		key  string
		mins int
		pct  float64
	}{{"primary", 300, 25}, {"secondary", 10080, 50}, {"tertiary", 43200, 10}}
	for i, w := range want {
		g := rep.Windows[i]
		if g.Key != w.key || g.WindowMinutes != w.mins || g.UsedPercent != w.pct {
			t.Errorf("window %d = %+v, want %+v", i, g, w)
		}
	}
	if rep.Windows[0].ResetsAt.IsZero() {
		t.Error("primary reset time missing")
	}
}

func alibabaTestEnvelope(quota string) string {
	return `{"successResponse":true,"data":{"success":true,"DataV2":{"data":{"success":true,"data":` + quota + `}}}}`
}

func TestAlibabaMonthlyOnlyTokenPlan(t *testing.T) {
	t.Parallel()
	const resetMillis = int64(1793116800000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(alibabaTestEnvelope(`{"per1MonthPercentage":0.8291326937774272,"per1MonthResetTime":1793116800000}`)))
	}))
	defer srv.Close()
	c := &alibabaCollector{env: directEnv(t.TempDir(), nil, map[string]string{"alibaba": "session=fixture"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err != nil || rep.State != StateFresh {
		t.Fatalf("state=%v windows=%+v err=%v", rep.State, rep.Windows, err)
	}
	if len(rep.Windows) != 1 {
		t.Fatalf("windows = %+v, want the single monthly quota", rep.Windows)
	}
	window := rep.Windows[0]
	if window.Key != "primary" || window.WindowMinutes != 43200 {
		t.Errorf("window identity = %q/%d minutes, want primary/monthly", window.Key, window.WindowMinutes)
	}
	if !window.HasPercent || math.Abs(window.UsedPercent-82.91326937774272) > 1e-9 {
		t.Errorf("used percent = %v (has=%t), want 82.91326937774272%%", window.UsedPercent, window.HasPercent)
	}
	if got := window.ResetsAt.UnixMilli(); got != resetMillis {
		t.Errorf("reset = %d, want %d", got, resetMillis)
	}
}

func TestAlibabaTokenPlanPercentageIsFractionalRatio(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value string
		want  float64
		valid bool
	}{
		{name: "zero", value: "0", want: 0, valid: true},
		{name: "fraction", value: "0.5", want: 50, valid: true},
		{name: "one", value: "1", want: 100, valid: true},
		{name: "over one", value: "1.01"},
		{name: "negative", value: "-0.01"},
		{name: "not finite", value: `"NaN"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := alibabaTestEnvelope(`{"per1MonthPercentage":` + tc.value + `}`)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			c := &alibabaCollector{env: directEnv(t.TempDir(), nil, map[string]string{"alibaba": "session=fixture"}), base: srv.URL}
			rep, err := c.Fetch(t.Context())
			if !tc.valid {
				if err == nil || rep.State != StateFault || len(rep.Windows) != 0 {
					t.Fatalf("invalid ratio accepted: state=%v windows=%+v", rep.State, rep.Windows)
				}
				return
			}
			if err != nil || rep.State != StateFresh || len(rep.Windows) != 1 {
				t.Fatalf("state=%v windows=%+v err=%v", rep.State, rep.Windows, err)
			}
			if got := rep.Windows[0].UsedPercent; got != tc.want {
				t.Errorf("used percent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAlibabaLiveTokenPlan(t *testing.T) {
	if os.Getenv("SYSC_AIUSAGE_ALIBABA_LIVE") != "1" {
		t.Skip("set SYSC_AIUSAGE_ALIBABA_LIVE=1 to use the local .env cookie")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("home directory unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(home, ".env"))
	if err != nil {
		t.Fatal("could not read the local .env file")
	}
	var cookie string
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		if key != "ALIBABA_TOKEN_PLAN_COOKIE" {
			continue
		}
		cookie = strings.TrimSpace(value)
		if len(cookie) >= 2 && cookie[0] == cookie[len(cookie)-1] && (cookie[0] == '\'' || cookie[0] == '"') {
			cookie = cookie[1 : len(cookie)-1]
		}
		break
	}
	if cookie == "" || strings.ContainsAny(cookie, "\r\n") {
		t.Fatal("local Alibaba cookie missing or malformed")
	}
	rep, err := NewAlibaba(Env{Keys: map[string]string{"alibaba": cookie}}).Fetch(t.Context())
	if err != nil || rep.State != StateFresh {
		t.Fatalf("live collector failed: state=%v err-type=%T", rep.State, err)
	}
	for _, window := range rep.Windows {
		if window.Key == "primary" && window.WindowMinutes == 43200 && window.HasPercent && !window.ResetsAt.IsZero() {
			return
		}
	}
	t.Fatal("live report did not include a monthly quota and reset")
}

func TestAlibabaRejectsUnsuccessfulOrMisplacedQuota(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{"outer gateway", `{"successResponse":false,"data":{"success":true,"DataV2":{"data":{"success":true,"data":{"per5HourPercentage":0.5}}}}}`},
		{"Bailian gateway", `{"successResponse":true,"data":{"success":false,"DataV2":{"data":{"success":true,"data":{"per5HourPercentage":0.5}}}}}`},
		{"quota service", `{"successResponse":true,"data":{"success":true,"DataV2":{"data":{"success":false,"data":{"per5HourPercentage":0.5}}}}}`},
		{"unrelated nested metadata", `{"metadata":{"per5HourPercentage":0.5}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := &alibabaCollector{env: directEnv(t.TempDir(), nil, map[string]string{"alibaba": "session=fixture"}), base: srv.URL}
			rep, err := c.Fetch(t.Context())
			if err == nil || rep.State != StateFault || len(rep.Windows) != 0 {
				t.Fatalf("invalid envelope accepted: state=%v windows=%+v", rep.State, rep.Windows)
			}
		})
	}
}

func TestAlibabaStaleCookie(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"successResponse":{"code":"ConsoleNeedLogin"}}}`))
	}))
	defer srv.Close()
	c := &alibabaCollector{env: directEnv(t.TempDir(), nil, map[string]string{"alibaba": "expired"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err == nil || rep.State != StateFault || rep.Err != "the console returned no token-plan quota (is the cookie still valid?)" {
		t.Errorf("%+v %v", rep, err)
	}
	c.env = directEnv(t.TempDir(), func(string) string { return "" }, nil)
	if rep, _ := c.Fetch(t.Context()); rep.State != StateNeedsSetup {
		t.Errorf("no cookie: %+v", rep)
	}
}

func TestQuotaEnvKeyFallback(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer env-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":40}]}}`))
	}))
	defer srv.Close()
	env := directEnv(t.TempDir(), func(k string) string {
		if k == "Z_AI_API_KEY" {
			return "env-key"
		}
		return ""
	}, nil)
	c := &zaiCollector{env: env, base: srv.URL}
	if rep, err := c.Fetch(t.Context()); err != nil || rep.State != StateFresh {
		t.Fatalf("zai env fallback: %+v %v", rep, err)
	}
	dsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer env-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"1.00"}]}`))
	}))
	defer dsrv.Close()
	denv := directEnv(t.TempDir(), func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "env-key"
		}
		return ""
	}, nil)
	dc := &deepseekCollector{env: denv, base: dsrv.URL}
	if rep, err := dc.Fetch(t.Context()); err != nil || rep.State != StateFresh {
		t.Fatalf("deepseek env fallback: %+v %v", rep, err)
	}
}

func TestZAIUsageWithoutCountersKeepsPercentage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":{"limits":[
			{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":77,"usage":1000000}]}}`))
	}))
	defer srv.Close()
	c := &zaiCollector{env: directEnv(t.TempDir(), nil, map[string]string{"zai": "z1"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Windows[0].UsedPercent != 77 || rep.Windows[0].DisplayValue != "" {
		t.Errorf("window = %+v", rep.Windows[0])
	}
}

func TestRoundKeepsCreditsOnlyFresh(t *testing.T) {
	t.Parallel()
	env, _ := loopEnv()
	bal := 5.5
	fc := &fakeCollector{id: "alpha", rep: ProviderReport{ID: "alpha", Name: "alpha", State: StateFresh, Credits: &bal}}
	cfg := testConfig()
	cfg.Track = map[string]bool{"alpha": true}
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(fc), cfg, env, cache, history)
	rep := l.Round(t.Context(), false)
	if len(rep.Providers) != 1 || rep.Providers[0].State != StateFresh || rep.Providers[0].Credits == nil {
		t.Fatalf("credits-only report = %+v", rep.Providers)
	}
}

func TestAlibabaApiKeyGuard(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the sk- guard must short-circuit before any request")
	}))
	defer srv.Close()
	c := &alibabaCollector{env: directEnv(t.TempDir(), nil, map[string]string{"alibaba": "sk-sp-abc"}), base: srv.URL}
	rep, err := c.Fetch(t.Context())
	want := "this field wants the console cookie — token-plan API keys work for inference only"
	if err == nil || rep.State != StateFault || rep.Err != want {
		t.Errorf("%+v %v", rep, err)
	}
}
