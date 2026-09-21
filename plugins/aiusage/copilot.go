package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// copilotCollector reads the GitHub Copilot subscription quota from the
// copilot_internal/user endpoint. Quota snapshots are percent-remaining per
// feature (premium requests, chat, completions), with the reset date at the
// top level. Auth is the user's GitHub token: `gh auth token` first, then
// the environment — the endpoint rejects unauthenticated reads.
type copilotCollector struct {
	env  Env
	base string
	// gh overrides the `gh auth token` probe. Nil means the real CLI; tests
	// inject an empty probe so they never depend on (or print) live tokens.
	gh func() string
}

// NewCopilot builds the Copilot collector.
func NewCopilot(env Env) Collector {
	return &copilotCollector{env: env, base: "https://api.github.com", gh: probeGhToken}
}

// probeGhToken asks the gh CLI for its stored token. Best-effort: any
// failure yields empty and the environment chain takes over.
func probeGhToken() string {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (c *copilotCollector) ID() string { return "copilot" }

// token resolves the GitHub credential: the gh CLI's stored token, then the
// environment's. Every source tried is recorded for the setup card.
func (c *copilotCollector) token() (string, *ErrSetup) {
	tried := []string{"gh auth token"}
	probe := c.gh
	if probe == nil {
		probe = probeGhToken
	}
	if tok := strings.TrimSpace(probe()); tok != "" {
		return tok, nil
	}
	chain := []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}
	for _, name := range chain {
		tried = append(tried, "env "+name)
		if v := c.env.getenv(name); v != "" {
			return v, nil
		}
	}
	return "", &ErrSetup{Tried: tried}
}

// copilotQuota is one feature's snapshot. Percents arrive as remainders;
// entitlement snapshots carry absolute counts instead.
type copilotQuota struct {
	Unlimited        bool     `json:"unlimited"`
	HasQuota         *bool    `json:"has_quota"`
	PercentRemaining *float64 `json:"percent_remaining"`
	Entitlement      *float64 `json:"entitlement"`
	Remaining        *float64 `json:"remaining"`
	OverageCount     *float64 `json:"overage_count"`
}

// copilotUser is the endpoint's payload. quota_reset_date_utc is top-level,
// not per snapshot; token_based_billing renames the premium label.
type copilotUser struct {
	Login             string `json:"login"`
	CopilotPlan       string `json:"copilot_plan"`
	AccessTypeSKU     string `json:"access_type_sku"`
	QuotaResetDateUTC string `json:"quota_reset_date_utc"`
	QuotaResetDate    string `json:"quota_reset_date"`
	TokenBasedBilling bool   `json:"token_based_billing"`
	QuotaSnapshots    struct {
		PremiumInteractions *copilotQuota `json:"premium_interactions"`
		Chat                *copilotQuota `json:"chat"`
		Completions         *copilotQuota `json:"completions"`
	} `json:"quota_snapshots"`
}

// usedPercent mirrors the reference reducer: unlimited is calm, a closed
// quota is fully used, a percent remainder inverts, and entitlement
// snapshots divide.
func usedPercent(q *copilotQuota) (float64, bool) {
	if q == nil {
		return 0, false
	}
	if q.Unlimited {
		return 0, true
	}
	if q.HasQuota != nil && !*q.HasQuota {
		return 100, true
	}
	if q.PercentRemaining != nil {
		return clampPercent(100 - *q.PercentRemaining), true
	}
	if q.Entitlement != nil && *q.Entitlement > 0 && q.Remaining != nil {
		return clampPercent((*q.Entitlement - *q.Remaining) / *q.Entitlement * 100), true
	}
	return 0, false
}

func clampPercent(v float64) float64 {
	return math.Max(0, math.Min(100, v))
}

// displayValue renders the human line for a snapshot.
func displayValue(q *copilotQuota) string {
	if q == nil {
		return ""
	}
	if q.Unlimited {
		return "Unlimited"
	}
	if q.Remaining != nil && q.Entitlement != nil && *q.Entitlement > 0 {
		s := fmt.Sprintf("%s / %s remaining", formatAmount(*q.Remaining), formatAmount(*q.Entitlement))
		if q.OverageCount != nil && *q.OverageCount > 0 {
			s += fmt.Sprintf(" (+%s overage)", formatAmount(*q.OverageCount))
		}
		return s
	}
	if q.PercentRemaining != nil {
		return fmt.Sprintf("%s%% remaining", formatAmount(*q.PercentRemaining))
	}
	return "Usage available"
}

// planLabel derives the plan chip from the SKU and plan fields: the SKU
// distinguishes the education and free grants that copilot_plan flattens.
func planLabel(sku, plan string) string {
	switch {
	case strings.Contains(sku, "educational") || strings.Contains(sku, "student"):
		return "Education"
	case strings.Contains(sku, "pro_plus") || strings.Contains(plan, "pro_plus") || strings.Contains(plan, "individual_pro"):
		return "Pro+"
	case strings.HasPrefix(sku, "free") || plan == "free":
		return "Free"
	case plan == "business":
		return "Business"
	case plan == "enterprise":
		return "Enterprise"
	case plan == "individual":
		return "Pro"
	case plan != "":
		return strings.ReplaceAll(plan, "_", " ")
	}
	return ""
}

func copilotWindow(q *copilotQuota, label, short string, reset time.Time) (Window, bool) {
	pct, ok := usedPercent(q)
	if !ok {
		return Window{}, false
	}
	w := Window{
		Key: "", Label: label, ShortLabel: short,
		HasPercent: true, UsedPercent: pct, WindowMinutes: 43200,
		ResetsAt: reset, DisplayValue: displayValue(q),
	}
	return w, true
}

func (c *copilotCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "copilot", Name: "Copilot", UpdatedAt: c.env.now()}
	tok, setup := c.token()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, c.base+"/copilot_internal/user", nil)
	if err != nil {
		rep.State, rep.Err = StateFault, err.Error()
		return rep, err
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", "vscode/1.105.0")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.32.0")
	req.Header.Set("X-Github-Api-Version", "2026-04-01")
	resp, err := c.env.httpClient().Do(req)
	if err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		err := &httpStatus{code: resp.StatusCode}
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		rep.State, rep.Err = StateFault, err.Error()
		return rep, err
	}
	var user copilotUser
	if err := json.Unmarshal(body, &user); err != nil {
		rep.State, rep.Err = StateFault, "copilot response was not a quota body"
		return rep, err
	}

	reset := time.Time{}
	if t, err := time.Parse(time.RFC3339Nano, user.QuotaResetDateUTC); err == nil {
		reset = t.UTC()
	} else if t, err := time.Parse(time.RFC3339Nano, user.QuotaResetDate); err == nil {
		reset = t.UTC()
	}

	premiumLabel := "Premium requests"
	if user.TokenBasedBilling {
		premiumLabel = "Premium requests · AI credits"
	}
	type slot struct {
		label, short string
		q            *copilotQuota
	}
	slots := []slot{
		{premiumLabel, "Mo", user.QuotaSnapshots.PremiumInteractions},
		{"Chat", "Chat", user.QuotaSnapshots.Chat},
		{"Completions", "Comp", user.QuotaSnapshots.Completions},
	}
	var wins []Window
	for i, s := range slots {
		// Chat and completions ride along only when they carry real quota:
		// unlimited-with-zero-entitlement adds no signal.
		if i > 0 && s.q != nil && s.q.Unlimited && (s.q.Entitlement == nil || *s.q.Entitlement == 0) {
			continue
		}
		w, ok := copilotWindow(s.q, s.label, s.short, reset)
		if !ok {
			continue
		}
		w.Key = []string{"primary", "secondary", "tertiary"}[min(i, 2)]
		wins = append(wins, w)
	}
	if len(wins) == 0 {
		msg := "copilot reported no quota snapshots"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	rep.Windows = wins
	account := user.Login
	if account == "" {
		account = "GitHub Copilot"
	}
	if plan := planLabel(user.AccessTypeSKU, user.CopilotPlan); plan != "" {
		rep.Plan = plan
		account += " · " + plan
	}
	rep.Account = account
	rep.State = StateFresh
	return rep, nil
}
