package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// oauthUsageCollector reads the subscription-limit percentages the usage
// endpoint computes server-side, with the OAuth token the CLI stores on
// disk. It is server-authoritative: no pricing tables, no local math. The
// endpoint rate-limits hard, so the loop never polls it faster than its
// floor and never serves a fault from cache (stale data would mislead).
type oauthUsageCollector struct {
	env     Env
	base    string // injectable for httptest
	version string // the User-Agent version the endpoint expects
}

// NewOAuthUsage builds the oauth usage collector.
func NewOAuthUsage(env Env) Collector {
	c := &oauthUsageCollector{env: env, base: "https://api.anthropic.com", version: "2.1.0"}
	if v := probeCLIVersion("claude"); v != "" {
		c.version = v
	}
	return c
}

func (c *oauthUsageCollector) ID() string { return "claude" }

// Floor is the scheduler-level rate-limit discipline: the endpoint 429s
// aggressively (safe only at 180 s or slower per the prior-art design), so
// the loop never schedules this collector faster, whatever the setting says.
func (c *oauthUsageCollector) Floor() time.Duration { return 180 * time.Second }

// tokenCand is one credential candidate. expiresAtMS is epoch milliseconds,
// the shape the CLI writes.
type tokenCand struct {
	expiresAtMS int64
	token       string
}

// credentials reads the on-disk OAuth token. The path is recorded so the
// setup card can name exactly what was looked for and where.
func (c *oauthUsageCollector) credentials() ([]tokenCand, *ErrSetup) {
	path := filepath.Join(c.env.home(), ".claude", ".credentials.json")
	tried := &ErrSetup{Tried: []string{path}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, tried
	}
	var file struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.ClaudeAiOauth.AccessToken == "" {
		return nil, tried
	}
	tokens := []tokenCand{{expiresAtMS: file.ClaudeAiOauth.ExpiresAt, token: file.ClaudeAiOauth.AccessToken}}
	// Non-expired first, freshest expiry first; expired candidates stay as a
	// best-effort fallback — a token past its nominal expiry still serves.
	nowMS := c.env.now().UnixMilli()
	sort.SliceStable(tokens, func(i, j int) bool {
		vi, vj := tokens[i].expiresAtMS > nowMS, tokens[j].expiresAtMS > nowMS
		if vi != vj {
			return vi
		}
		return tokens[i].expiresAtMS > tokens[j].expiresAtMS
	})
	return tokens, nil
}

func (c *oauthUsageCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "claude", Name: "Claude", UpdatedAt: c.env.now()}
	tokens, setup := c.credentials()
	if setup != nil {
		rep.State = StateNeedsSetup
		rep.Err = setup.Error()
		return rep, setup
	}
	var lastErr string
	for _, cand := range tokens {
		windows, msg := c.tryToken(ctx, cand.token)
		if msg == "" {
			rep.Windows = windows
			rep.State = StateFresh
			return rep, nil
		}
		lastErr = msg
	}
	rep.State = StateFault
	rep.Err = Scrub(lastErr)
	return rep, fmt.Errorf("oauth usage: %s", rep.Err)
}

// tryToken issues the usage request with one credential. An empty message
// means success; anything else is the scrubbed reason the attempt failed.
func (c *oauthUsageCollector) tryToken(ctx context.Context, token string) ([]Window, string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/oauth/usage", nil)
	if err != nil {
		return nil, "usage request: " + err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "claude-code/"+c.version)
	resp, err := c.env.httpClient().Do(req)
	if err != nil {
		return nil, "usage endpoint unreachable: " + err.Error()
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Sprintf("sign-in expired — run the sign-in flow (HTTP %d)", resp.StatusCode)
	case resp.StatusCode == 429:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, "the usage endpoint rate limited the request (HTTP 429)"
	case resp.StatusCode >= 500:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Sprintf("usage endpoint error (HTTP %d)", resp.StatusCode)
	case resp.StatusCode != 200:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Sprintf("usage endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "usage response: " + err.Error()
	}
	windows, ok := parseOAuthWindows(body)
	if !ok {
		return nil, "usage response was not a quota body (an error page is not usage)"
	}
	return windows, ""
}

// oauthWire is the endpoint's response: either the newer canonical limits
// array or the older flat pair.
type oauthWire struct {
	Limits []struct {
		Kind        string   `json:"kind"`
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
		IsActive    *bool    `json:"is_active"`
	} `json:"limits"`
	FiveHour *struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	} `json:"five_hour"`
	SevenDay *struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	} `json:"seven_day"`
}

// parseOAuthWindows turns a valid quota body into windows. A valid body is
// an object carrying a quota block; error bodies are rejected, never
// rendered as zeros.
func parseOAuthWindows(body []byte) ([]Window, bool) {
	var wire oauthWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, false
	}
	if len(wire.Limits) > 0 {
		var out []Window
		for _, l := range wire.Limits {
			var key string
			var minutes int
			switch l.Kind {
			case "session":
				key, minutes = "primary", 300
			case "weekly_all":
				key, minutes = "secondary", 10080
			case "weekly_scoped":
				key, minutes = "tertiary", 10080
			default:
				continue
			}
			if l.Utilization == nil {
				continue
			}
			out = append(out, windowFromUtilization(key, minutes, *l.Utilization, l.ResetsAt))
		}
		if len(out) > 0 {
			return out, true
		}
		return nil, false
	}
	if wire.FiveHour == nil {
		return nil, false // an error body has no five_hour block
	}
	var out []Window
	if wire.FiveHour.Utilization != nil {
		out = append(out, windowFromUtilization("primary", 300, *wire.FiveHour.Utilization, wire.FiveHour.ResetsAt))
	}
	if wire.SevenDay != nil && wire.SevenDay.Utilization != nil {
		out = append(out, windowFromUtilization("secondary", 10080, *wire.SevenDay.Utilization, wire.SevenDay.ResetsAt))
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func windowFromUtilization(key string, minutes int, utilization float64, resetsAt string) Window {
	label, short := LabelForMinutes(minutes)
	w := Window{
		Key:         key,
		Label:       label,
		ShortLabel:  short,
		HasPercent:  true,
		UsedPercent: math.Max(0, math.Min(100, math.Floor(utilization))),
	}
	if minutes > 0 {
		w.WindowMinutes = minutes
	}
	if t, err := time.Parse(time.RFC3339Nano, resetsAt); err == nil {
		w.ResetsAt = t.UTC()
	}
	return w
}

// probeCLIVersion asks an installed CLI its version for the User-Agent the
// endpoint expects. Best-effort: any failure keeps the literal fallback.
func probeCLIVersion(command string) string {
	out, err := exec.Command(command, "--version").Output()
	if err != nil {
		return ""
	}
	return regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`).FindString(strings.TrimSpace(string(out)))
}
