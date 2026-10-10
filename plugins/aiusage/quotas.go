package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This file holds the quota-plan collectors added from CodexBar's wire
// specs: Kimi, Z.AI, and DeepSeek are one bearer request each; Alibaba's
// token plan is a console cookie RPC because the plan gateway serves no
// API-key usage endpoint (probe: every /usage path 404s with a key).

// ── shared JSON flex helpers ───────────────────────────────────────────────
//
// These providers send numbers as strings and timestamps in mixed shapes,
// so decoding lands in json.RawMessage / any and is normalized here.

// flexNum accepts a JSON number or a numeric string.
func flexNum(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var v float64
	if json.Unmarshal(raw, &v) == nil {
		return v, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// flexAnyNum is flexNum for values decoded with a plain json.Unmarshal into
// any (numbers arrive as float64, strings as strings).
func flexAnyNum(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

// flexTime accepts an RFC3339 string or a unix-milliseconds number in
// either JSON shape. A zero result means "no reset instant".
func flexTime(v any) time.Time {
	switch t := v.(type) {
	case float64:
		if t > 0 {
			return time.UnixMilli(int64(t)).UTC()
		}
	case string:
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			return ts.UTC()
		}
		if ms, err := strconv.ParseFloat(t, 64); err == nil && ms > 0 {
			return time.UnixMilli(int64(ms)).UTC()
		}
	}
	return time.Time{}
}

// windowFromPercent builds a labeled percent window.
func windowFromPercent(key string, minutes int, percent float64, resets time.Time) Window {
	label, short := LabelForMinutes(minutes)
	return Window{
		Key: key, Label: label, ShortLabel: short,
		HasPercent: true, UsedPercent: clampPercent(percent),
		WindowMinutes: minutes, ResetsAt: resets,
	}
}

// ── Kimi ─────────────────────────────────────────────────────────────────────
//
// The coding-plan usage endpoint answers in three shapes at once: a modern
// usages map of used ratios, a legacy usage detail, and a limits array of
// counted windows. Whichever carry data win, and every reading is a percent
// beside its reset instant. Membership levels name the plan.

type kimiCollector struct {
	env  Env
	base string
}

// NewKimi builds the Kimi collector.
func NewKimi(env Env) Collector { return &kimiCollector{env: env, base: "https://api.kimi.com"} }

func (c *kimiCollector) ID() string { return "kimi" }

func (c *kimiCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("kimi"); k != "" {
		return k, nil
	}
	tried = append(tried, "env KIMI_CODE_API_KEY")
	if k := c.env.getenv("KIMI_CODE_API_KEY"); k != "" {
		return k, nil
	}
	cred := filepath.Join(c.env.home(), ".kimi-code", "credentials", "kimi-code.json")
	tried = append(tried, cred)
	if raw, err := os.ReadFile(cred); err == nil {
		var f struct {
			AccessToken string `json:"accessToken"`
		}
		if json.Unmarshal(raw, &f) == nil && strings.TrimSpace(f.AccessToken) != "" {
			return strings.TrimSpace(f.AccessToken), nil
		}
	}
	return "", &ErrSetup{Tried: tried}
}

var kimiLevels = map[string]string{
	"LEVEL_FREE": "Adagio", "LEVEL_TRIAL": "Andante", "LEVEL_BASIC": "Moderato",
	"LEVEL_INTERMEDIATE": "Allegretto", "LEVEL_ADVANCED": "Allegro",
}

type kimiDetail struct {
	Limit     json.RawMessage `json:"limit"`
	Used      json.RawMessage `json:"used"`
	Remaining json.RawMessage `json:"remaining"`
	ResetTime string          `json:"resetTime"`
}

func (c *kimiCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "kimi", Name: "Kimi", UpdatedAt: c.env.now()}
	key, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var out struct {
		Usages struct {
			Limit5H    *kimiRatio `json:"limit_5h"`
			Limit7D    *kimiRatio `json:"limit_7d"`
			LimitMonth *kimiRatio `json:"limit_month_total"`
		} `json:"usages"`
		Usage  *kimiDetail  `json:"usage"`
		Limits []kimiLimits `json:"limits"`
		User   struct {
			Membership struct {
				Level string `json:"level"`
			} `json:"membership"`
		} `json:"user"`
	}
	if err := bearerGet(ctx, c.env, c.base+"/coding/v1/usages", key, &out); err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	// minutes → reading, first writer wins: usages map, then legacy usage,
	// then counted limit windows.
	wins := map[int]Window{}
	for _, p := range []struct {
		minutes int
		r       *kimiRatio
	}{{300, out.Usages.Limit5H}, {10080, out.Usages.Limit7D}, {43200, out.Usages.LimitMonth}} {
		if p.r != nil && p.r.UsedRatio != nil {
			wins[p.minutes] = windowFromPercent("", p.minutes, *p.r.UsedRatio*100, flexTime(p.r.ResetTime))
		}
	}
	if _, ok := wins[10080]; !ok && out.Usage != nil {
		if w, ok := kimiCounted(*out.Usage, 10080); ok {
			wins[10080] = w
		}
	}
	for _, l := range out.Limits {
		minutes := l.Window.Duration * kimiUnitMinutes(l.Window.TimeUnit)
		if minutes <= 0 {
			continue
		}
		if _, ok := wins[minutes]; ok {
			continue
		}
		if w, ok := kimiCounted(l.Detail, minutes); ok {
			wins[minutes] = w
		}
	}
	if len(wins) == 0 {
		msg := "the usage endpoint carried no quota windows"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	var keys []int
	for m := range wins {
		keys = append(keys, m)
	}
	// Ascending window length; the shortest is the session and leads.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	slot := []string{"primary", "secondary", "tertiary"}
	for i, m := range keys {
		if i >= len(slot) {
			break
		}
		w := wins[m]
		w.Key = slot[i]
		rep.Windows = append(rep.Windows, w)
	}
	if name, ok := kimiLevels[out.User.Membership.Level]; ok {
		rep.Plan = name
	} else if out.User.Membership.Level != "" {
		rep.Plan = planTitle(out.User.Membership.Level)
	}
	rep.State = StateFresh
	return rep, nil
}

type kimiRatio struct {
	UsedRatio *float64 `json:"used_ratio"`
	ResetTime string   `json:"reset_time"`
}

type kimiLimits struct {
	Window struct {
		Duration int    `json:"duration"`
		TimeUnit string `json:"timeUnit"`
	} `json:"window"`
	Detail kimiDetail `json:"detail"`
}

func kimiUnitMinutes(unit string) int {
	switch unit {
	case "TIME_UNIT_MINUTE":
		return 1
	case "TIME_UNIT_HOUR":
		return 60
	case "TIME_UNIT_DAY":
		return 1440
	case "TIME_UNIT_WEEK":
		return 10080
	}
	return 0
}

// kimiCounted turns a limit/used/remaining triple into a percent window.
func kimiCounted(d kimiDetail, minutes int) (Window, bool) {
	cap, hasCap := flexNum(d.Limit)
	used, hasUsed := flexNum(d.Used)
	if !hasUsed {
		if rem, ok := flexNum(d.Remaining); ok && hasCap {
			used, hasUsed = cap-rem, true
		}
	}
	if !hasCap || cap <= 0 || !hasUsed {
		return Window{}, false
	}
	return windowFromPercent("", minutes, used/cap*100, flexTime(d.ResetTime)), true
}

// ── Z.AI ─────────────────────────────────────────────────────────────────────
//
// The quota endpoint wraps every answer in a success/code envelope and
// carries one limit per plan lane. Token and credit lanes are real quota
// windows; the time lane is the MCP meter and is skipped.

type zaiCollector struct {
	env  Env
	base string
}

// NewZAI builds the Z.AI collector.
func NewZAI(env Env) Collector { return &zaiCollector{env: env, base: "https://api.z.ai"} }

func (c *zaiCollector) ID() string { return "zai" }

func (c *zaiCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("zai"); k != "" {
		return k, nil
	}
	for _, e := range []string{"Z_AI_API_KEY", "BIGMODEL_API_KEY", "GLM_API_KEY"} {
		tried = append(tried, "env "+e)
		if k := c.env.getenv(e); k != "" {
			return k, nil
		}
	}
	return "", &ErrSetup{Tried: tried}
}

func (c *zaiCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "zai", Name: "Z.AI", UpdatedAt: c.env.now()}
	key, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var out struct {
		Success bool   `json:"success"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Data    struct {
			Limits []struct {
				Type         string   `json:"type"`
				Unit         int      `json:"unit"`
				Number       int      `json:"number"`
				Percentage   float64  `json:"percentage"`
				Usage        *float64 `json:"usage"`
				CurrentValue *float64 `json:"currentValue"`
				Remaining    *float64 `json:"remaining"`
				NextReset    int64    `json:"nextResetTime"`
			} `json:"limits"`
			PlanName    string `json:"planName"`
			Plan        string `json:"plan"`
			PlanType    string `json:"plan_type"`
			PackageName string `json:"packageName"`
			Level       string `json:"level"`
		} `json:"data"`
	}
	if err := bearerGet(ctx, c.env, c.base+"/api/monitor/usage/quota/limit", key, &out); err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	if !out.Success || out.Code != 200 {
		msg := Scrub(out.Msg)
		if msg == "" {
			msg = fmt.Sprintf("z.ai envelope code %d", out.Code)
		}
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	// unit → minutes multiplier, per the monitor API's enum.
	mult := map[int]int{1: 1440, 3: 60, 5: 1, 6: 10080}
	type lane struct {
		minutes int
		percent float64
		disp    string
		resets  time.Time
	}
	var lanes []lane
	for _, l := range out.Data.Limits {
		if l.Type != "TOKENS_LIMIT" && l.Type != "CREDIT_LIMIT" {
			continue
		}
		m, ok := mult[l.Unit]
		if !ok || l.Number <= 0 {
			continue
		}
		percent := clampPercent(l.Percentage)
		disp := ""
		if l.Usage != nil && *l.Usage > 0 {
			used, have := 0.0, false
			switch {
			case l.Remaining != nil:
				used, have = math.Max(0, *l.Usage-*l.Remaining), true
			case l.CurrentValue != nil:
				used, have = *l.CurrentValue, true
			}
			if have {
				percent = clampPercent(used / *l.Usage * 100)
				disp = formatAmount(used) + " / " + formatAmount(*l.Usage)
			}
		}
		w := lane{minutes: l.Number * m, percent: percent, disp: disp}
		if l.NextReset > 0 {
			w.resets = time.UnixMilli(l.NextReset).UTC()
		}
		lanes = append(lanes, w)
	}
	if len(lanes) == 0 {
		msg := "the quota endpoint carried no token or credit limits"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	for i := 0; i < len(lanes); i++ {
		for j := i + 1; j < len(lanes); j++ {
			if lanes[j].minutes < lanes[i].minutes {
				lanes[i], lanes[j] = lanes[j], lanes[i]
			}
		}
	}
	set := func(i int, key string) Window {
		w := windowFromPercent(key, lanes[i].minutes, lanes[i].percent, lanes[i].resets)
		w.DisplayValue = lanes[i].disp
		return w
	}
	rep.Windows = append(rep.Windows, set(0, "primary"))
	if len(lanes) > 1 {
		rep.Windows = append(rep.Windows, set(len(lanes)-1, "secondary"))
	}
	for _, p := range []string{out.Data.PlanName, out.Data.Plan, out.Data.PlanType, out.Data.PackageName, out.Data.Level} {
		if p != "" {
			rep.Plan = planTitle(p)
			break
		}
	}
	rep.State = StateFresh
	return rep, nil
}

// ── DeepSeek ─────────────────────────────────────────────────────────────────
//
// The balance endpoint is a pay-as-you-go wallet, not a quota plan: one
// total, no windows. The first funded wallet wins, USD preferred.

type deepseekCollector struct {
	env  Env
	base string
}

// NewDeepSeek builds the DeepSeek collector.
func NewDeepSeek(env Env) Collector {
	return &deepseekCollector{env: env, base: "https://api.deepseek.com"}
}

func (c *deepseekCollector) ID() string { return "deepseek" }

func (c *deepseekCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("deepseek"); k != "" {
		return k, nil
	}
	for _, e := range []string{"DEEPSEEK_API_KEY", "DEEPSEEK_KEY"} {
		tried = append(tried, "env "+e)
		if k := c.env.getenv(e); k != "" {
			return k, nil
		}
	}
	return "", &ErrSetup{Tried: tried}
}

func (c *deepseekCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "deepseek", Name: "DeepSeek", UpdatedAt: c.env.now()}
	key, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var out struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency     string          `json:"currency"`
			TotalBalance json.RawMessage `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := bearerGet(ctx, c.env, c.base+"/user/balance", key, &out); err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	if len(out.BalanceInfos) == 0 {
		msg := "the balance endpoint returned no wallets"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	pick := out.BalanceInfos[0]
	for _, b := range out.BalanceInfos {
		if v, ok := flexNum(b.TotalBalance); ok && v > 0 {
			pick = b
			if b.Currency == "USD" {
				break
			}
		}
	}
	total, _ := flexNum(pick.TotalBalance)
	rep.Credits = &total
	rep.Account = pick.Currency
	if !out.IsAvailable {
		rep.Account += " · balance unavailable for API calls"
	}
	rep.State = StateFresh
	return rep, nil
}

// ── Alibaba token plan ───────────────────────────────────────────────────────
//
// The token plan meter lives behind the Model Studio console RPC; there is
// no API-key usage endpoint on the plan gateway. The credential pasted in
// settings is a console Cookie header. ponytail: intl console host only;
// the cn mirror is bailian-cs.console.aliyun.com with action
// BroadScopeAspnGateway if a cn user ever needs it.

type alibabaCollector struct {
	env  Env
	base string
}

// NewAlibaba builds the Alibaba token-plan collector.
func NewAlibaba(env Env) Collector {
	return &alibabaCollector{env: env, base: "https://bailian-singapore-cs.alibabacloud.com"}
}

func (c *alibabaCollector) ID() string { return "alibaba" }

const alibabaUsageAPI = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"

func (c *alibabaCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("alibaba"); k != "" {
		return k, nil
	}
	tried = append(tried, "env ALIBABA_TOKEN_PLAN_COOKIE")
	if k := c.env.getenv("ALIBABA_TOKEN_PLAN_COOKIE"); k != "" {
		return k, nil
	}
	return "", &ErrSetup{Tried: tried}
}

func (c *alibabaCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "alibaba", Name: "Alibaba", UpdatedAt: c.env.now()}
	cookie, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	envelope := map[string]any{
		"Api": alibabaUsageAPI,
		"V":   "1.0",
		"Data": map[string]any{
			"cornerstoneParam": map[string]any{
				"protocol":          "V2",
				"console":           "ONE_CONSOLE",
				"productCode":       "p_efm",
				"switchUserType":    3,
				"domain":            strings.TrimPrefix(c.base, "https://"),
				"consoleSite":       "MODELSTUDIO_ALBABACLOUD",
				"xsp_lang":          "en-US",
				"userNickName":      "",
				"userPrincipalName": "",
			},
		},
	}
	params, err := json.Marshal(envelope)
	if err != nil {
		rep.State, rep.Err = StateFault, err.Error()
		return rep, err
	}
	form := url.Values{
		"product":  {"sfm_bailian"},
		"action":   {"IntlBroadScopeAspnGateway"},
		"region":   {"ap-southeast-1"},
		"language": {"en-US"},
		"params":   {string(params)},
	}
	endpoint := c.base + "/data/api.json?action=IntlBroadScopeAspnGateway&product=sfm_bailian&api=" +
		url.QueryEscape(alibabaUsageAPI) + "&_v=undefined"
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		rep.State, rep.Err = StateFault, err.Error()
		return rep, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", cookie)
	resp, err := c.env.httpClient().Do(req)
	if err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err := &httpStatus{code: resp.StatusCode}
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	var body any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		rep.State, rep.Err = StateFault, Scrub(err.Error())
		return rep, err
	}
	node := findJSONObject(body, func(m map[string]any) bool {
		_, ok := m["per5HourPercentage"]
		return ok
	})
	// A login redirect or expired cookie answers with an envelope but no
	// quota map — say so, do not guess.
	if node == nil {
		msg := "the console returned no token-plan quota (is the cookie still valid?)"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	specs := []struct {
		slot    string
		minutes int
		pctKey  string
	}{
		{"primary", 300, "per5HourPercentage"},
		{"secondary", 10080, "per1WeekPercentage"},
		{"tertiary", 43200, "per1MonthPercentage"},
	}
	for _, s := range specs {
		v, ok := flexAnyNum(node[s.pctKey])
		if !ok {
			continue
		}
		if v <= 1 {
			v *= 100 // ratios arrive as 0..1
		}
		resetKey := strings.Replace(s.pctKey, "Percentage", "ResetTime", 1)
		rep.Windows = append(rep.Windows, windowFromPercent(s.slot, s.minutes, v, flexTime(node[resetKey])))
	}
	if len(rep.Windows) == 0 {
		msg := "the token-plan quota map carried no percentages"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	rep.State = StateFresh
	return rep, nil
}

// findJSONObject depth-first searches decoded JSON for the first map
// satisfying pred. The console nests payloads under data / successResponse
// differently per gateway, so paths are searched, not assumed.
func findJSONObject(v any, pred func(map[string]any) bool) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		if pred(t) {
			return t
		}
		for _, c := range t {
			if r := findJSONObject(c, pred); r != nil {
				return r
			}
		}
	case []any:
		for _, c := range t {
			if r := findJSONObject(c, pred); r != nil {
				return r
			}
		}
	}
	return nil
}
