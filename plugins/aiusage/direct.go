package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This file holds the direct-API collectors: providers whose usage is one
// bearer-key GET with well-understood JSON. Every quirk a provider adds is
// confined to its own section; the shared helpers are the bearer transport,
// the fault vocabulary, and the key-resolution chain.

// httpStatus is a non-2xx response. The collectors translate it into the
// fault vocabulary; it is never shown raw to a user.
type httpStatus struct{ code int }

func (e *httpStatus) Error() string { return "HTTP " + strconv.Itoa(e.code) }

// bearerGet issues one authenticated GET and decodes the JSON body. A 20 s
// ceiling applies, matching the loop's per-fetch budget.
func bearerGet(ctx context.Context, env Env, url, key string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := env.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return &httpStatus{code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

// bearerPost issues one authenticated POST, discarding the body. It exists
// for best-effort identity probes whose failure must never fail a refresh.
func bearerPost(ctx context.Context, env Env, url, key string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := env.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return &httpStatus{code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(body, into)
}

// faultMessage renders a transport or status error as the scrubbed,
// human-readable reason a read failed.
func faultMessage(err error) string {
	var st *httpStatus
	if errors.As(err, &st) {
		switch st.code {
		case 401, 403:
			return fmt.Sprintf("key rejected — replace it in settings (HTTP %d)", st.code)
		case 429:
			return "the endpoint rate limited the request (HTTP 429)"
		default:
			return fmt.Sprintf("endpoint error (HTTP %d)", st.code)
		}
	}
	return err.Error()
}

// keyFromFile reads one well-known key file, whitespace-trimmed.
func keyFromFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ── Command Code ─────────────────────────────────────────────────────────────
//
// The billing-credits endpoint answers for the whole plan: quota windows in
// windowLimits and the credit balance beside them. A rejected key is a
// fault — the key exists, it just no longer works.

type commandcodeCollector struct {
	env  Env
	base string
}

// NewCommandCode builds the Command Code collector.
func NewCommandCode(env Env) Collector {
	return &commandcodeCollector{env: env, base: "https://api.commandcode.ai"}
}

func (c *commandcodeCollector) ID() string { return "commandcode" }

func (c *commandcodeCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("commandcode"); k != "" {
		return k, nil
	}
	tried = append(tried, "env COMMAND_CODE_API_KEY")
	if k := c.env.getenv("COMMAND_CODE_API_KEY"); k != "" {
		return k, nil
	}
	authFile := filepath.Join(c.env.home(), ".commandcode", "auth.json")
	tried = append(tried, authFile)
	if raw, err := os.ReadFile(authFile); err == nil {
		var f struct {
			APIKey string `json:"apiKey"`
		}
		if json.Unmarshal(raw, &f) == nil && strings.TrimSpace(f.APIKey) != "" {
			return strings.TrimSpace(f.APIKey), nil
		}
	}
	return "", &ErrSetup{Tried: tried}
}

// commandcodeCredits is the billing-credits payload. The alpha endpoint's
// window fields are pinned by the fixture in direct_test.go; anything else
// is a fault, never a guess.
type commandcodeCredits struct {
	WindowLimits struct {
		FiveHour *quotaWindow `json:"fiveHour"`
		Weekly   *quotaWindow `json:"weekly"`
	} `json:"windowLimits"`
	Credits struct {
		MonthlyCredits *float64 `json:"monthlyCredits"`
	} `json:"credits"`
}

type quotaWindow struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	ResetsAt int64   `json:"resetsAt"` // unix ms
}

func (q *quotaWindow) window(key string, minutes int) Window {
	label, short := LabelForMinutes(minutes)
	w := Window{
		Key:           key,
		Label:         label,
		ShortLabel:    short,
		HasPercent:    true,
		WindowMinutes: minutes,
	}
	if q.Cap > 0 {
		w.UsedPercent = math.Max(0, math.Min(100, q.Used/q.Cap*100))
	}
	w.DisplayValue = fmt.Sprintf("$%s / $%s", formatAmount(q.Used), formatAmount(q.Cap))
	if q.ResetsAt > 0 {
		w.ResetsAt = time.UnixMilli(q.ResetsAt).UTC()
	}
	return w
}

// formatAmount renders a dollar amount without trailing zeros.
func formatAmount(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (c *commandcodeCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "commandcode", Name: "Command Code", UpdatedAt: c.env.now()}
	key, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var out commandcodeCredits
	if err := bearerGet(ctx, c.env, c.base+"/alpha/billing/credits", key, &out); err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	var wins []Window
	if w := out.WindowLimits.FiveHour; w != nil {
		wins = append(wins, w.window("primary", 300))
	}
	if w := out.WindowLimits.Weekly; w != nil {
		wins = append(wins, w.window("secondary", 10080))
	}
	if len(wins) == 0 {
		msg := "the credits endpoint carried no quota windows"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	rep.Windows = wins
	if out.Credits.MonthlyCredits != nil {
		rep.Credits = out.Credits.MonthlyCredits
	}
	rep.State = StateFresh
	return rep, nil
}

// ── Ollama ───────────────────────────────────────────────────────────────────
//
// ollama.com serves plan usage for cloud accounts. The session window has no
// length and no reset instant — it is what it is — and the weekly reset is
// computed, not served. Identity is best-effort: a plan name is not worth
// failing a refresh over.

type ollamaCollector struct {
	env  Env
	base string
}

// NewOllama builds the Ollama collector.
func NewOllama(env Env) Collector {
	return &ollamaCollector{env: env, base: "https://ollama.com"}
}

func (c *ollamaCollector) ID() string { return "ollama" }

func (c *ollamaCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("ollama"); k != "" {
		return k, nil
	}
	tried = append(tried, "env OLLAMA_API_KEY")
	if k := c.env.getenv("OLLAMA_API_KEY"); k != "" {
		return k, nil
	}
	return "", &ErrSetup{Tried: tried}
}

// nextWeeklyReset is the next Monday 00:00 UTC strictly after now. 1970-01-01
// was a Thursday, so (days+4)%7 indexes Sunday as 0 — which makes Monday 1.
func nextWeeklyReset(now time.Time) time.Time {
	utc := now.UTC()
	midnight := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	weekday := (int(midnight.Unix()/86400) + 4) % 7
	ahead := (8 - weekday) % 7
	if ahead == 0 {
		ahead = 7
	}
	return midnight.AddDate(0, 0, ahead)
}

func (c *ollamaCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "ollama", Name: "Ollama", UpdatedAt: c.env.now()}
	key, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var out struct {
		Limits struct {
			Session *struct {
				Usage float64 `json:"usage"`
			} `json:"session"`
			Weekly *struct {
				Usage float64 `json:"usage"`
			} `json:"weekly"`
		} `json:"limits"`
	}
	if err := bearerGet(ctx, c.env, c.base+"/api/usage", key, &out); err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	now := c.env.now()
	var wins []Window
	if out.Limits.Session != nil {
		// The session window is unbounded: no length, no reset, no elapsed.
		wins = append(wins, Window{
			Key: "primary", Label: "Session usage", ShortLabel: "S",
			HasPercent:  true,
			UsedPercent: math.Max(0, math.Min(100, out.Limits.Session.Usage*100)),
		})
	}
	if out.Limits.Weekly != nil {
		wins = append(wins, Window{
			Key: "secondary", Label: "Weekly", ShortLabel: "Wk",
			HasPercent:    true,
			UsedPercent:   math.Max(0, math.Min(100, out.Limits.Weekly.Usage*100)),
			WindowMinutes: 10080,
			ResetsAt:      nextWeeklyReset(now),
		})
	}
	if len(wins) == 0 {
		msg := "the usage endpoint carried no plan limits"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	rep.Windows = wins
	// Identity probe: failure is explicitly not an error.
	var me struct {
		Plan  string `json:"plan"`
		Email string `json:"email"`
	}
	if err := bearerPost(ctx, c.env, c.base+"/api/me", key, &me); err == nil {
		if me.Plan != "" {
			rep.Plan = planTitle(me.Plan)
		}
		rep.Account = me.Email
	}
	rep.State = StateFresh
	return rep, nil
}

// ── MiniMax ──────────────────────────────────────────────────────────────────
//
// The coding-plan remains endpoint reports interval and weekly remainders as
// percents. base_resp carries the real outcome even on HTTP 200; the video
// service rides the same payload and is ignored; the general model entry is
// the plan. Keys fall back through the local key file and a sibling app's
// config, each recorded for the setup card.

type minimaxCollector struct {
	env  Env
	base string
}

// NewMinimax builds the MiniMax collector.
func NewMinimax(env Env) Collector {
	return &minimaxCollector{env: env, base: "https://api.minimax.io"}
}

func (c *minimaxCollector) ID() string { return "minimax" }

func (c *minimaxCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("minimax"); k != "" {
		return k, nil
	}
	tried = append(tried, "env MINIMAX_API_KEY")
	if k := c.env.getenv("MINIMAX_API_KEY"); k != "" {
		return k, nil
	}
	keyFile := filepath.Join(c.env.home(), ".minimax", "api_key")
	tried = append(tried, keyFile)
	if k := keyFromFile(keyFile); k != "" {
		return k, nil
	}
	// A sibling app's config is the last link: its stored key works for this
	// endpoint too, and borrowing it beats a setup card when one exists.
	borrow := filepath.Join(c.env.home(), ".config", "codexbar", "config.json")
	tried = append(tried, borrow)
	if raw, err := os.ReadFile(borrow); err == nil {
		var cfg struct {
			Providers []struct {
				ID     string `json:"id"`
				APIKey string `json:"apiKey"`
			} `json:"providers"`
		}
		if json.Unmarshal(raw, &cfg) == nil {
			for _, p := range cfg.Providers {
				if p.ID == "minimax" && p.APIKey != "" {
					return p.APIKey, nil
				}
			}
		}
	}
	return "", &ErrSetup{Tried: tried}
}

// remainingToUsed inverts a remainder. A missing or null percent is not 100%
// used, so it stays absent.
func remainingToUsed(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	return math.Max(0, math.Min(100, 100-v)), true
}

func (c *minimaxCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "minimax", Name: "MiniMax", UpdatedAt: c.env.now()}
	key, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var out struct {
		ModelRemains []struct {
			ModelName string `json:"model_name"`
			// Raw remainders: presence and absence both matter, so they are
			// decoded lazily rather than defaulted.
			CurrentIntervalRemaining json.RawMessage `json:"current_interval_remaining_percent"`
			CurrentWeeklyRemaining   json.RawMessage `json:"current_weekly_remaining_percent"`
			EndTime                  int64           `json:"end_time"`
			WeeklyEndTime            int64           `json:"weekly_end_time"`
		} `json:"model_remains"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := bearerGet(ctx, c.env, c.base+"/v1/api/openplatform/coding_plan/remains", key, &out); err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	// base_resp carries the real outcome even on HTTP 200.
	if out.BaseResp.StatusCode != 0 {
		msg := Scrub(out.BaseResp.StatusMsg)
		if msg == "" {
			msg = fmt.Sprintf("minimax status %d", out.BaseResp.StatusCode)
		}
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	if len(out.ModelRemains) == 0 {
		msg := "minimax reported no plan data"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	// The general model is the plan; the video service rides the same
	// payload and is ignored. First entry is the fallback.
	plan := out.ModelRemains[0]
	for _, m := range out.ModelRemains {
		if m.ModelName == "general" {
			plan = m
			break
		}
	}
	var wins []Window
	if used, ok := remainingToUsed(plan.CurrentIntervalRemaining); ok {
		w := Window{Key: "primary", HasPercent: true, UsedPercent: used, WindowMinutes: 300}
		w.Label, w.ShortLabel = LabelForMinutes(300)
		if plan.EndTime > 0 {
			w.ResetsAt = time.UnixMilli(plan.EndTime).UTC()
		}
		wins = append(wins, w)
	}
	if used, ok := remainingToUsed(plan.CurrentWeeklyRemaining); ok {
		w := Window{Key: "secondary", HasPercent: true, UsedPercent: used, WindowMinutes: 10080}
		w.Label, w.ShortLabel = LabelForMinutes(10080)
		if plan.WeeklyEndTime > 0 {
			w.ResetsAt = time.UnixMilli(plan.WeeklyEndTime).UTC()
		}
		wins = append(wins, w)
	}
	if len(wins) == 0 {
		msg := "minimax reported no interval or weekly usage"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	rep.Windows = wins
	rep.State = StateFresh
	return rep, nil
}
