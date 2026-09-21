package aiusage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"
)

// syntheticCollector reads the subscription quota from the v2 quotas
// endpoint. The shape and URL are pinned by the official documentation
// (dev.synthetic.new/docs/synthetic/quotas, fetched 2026-09-20) and by the
// committed fixture; a live capture with a real key replaces the fixture
// during deployment verification. The quota is a request count against a
// limit with a renew instant — percent-derivable, so it renders as a real
// window rather than an informational note.
type syntheticCollector struct {
	env  Env
	base string
}

// NewSynthetic builds the Synthetic collector.
func NewSynthetic(env Env) Collector {
	return &syntheticCollector{env: env, base: "https://api.synthetic.new"}
}

func (c *syntheticCollector) ID() string { return "synthetic" }

func (c *syntheticCollector) key() (string, *ErrSetup) {
	tried := []string{"plugin settings"}
	if k := c.env.key("synthetic"); k != "" {
		return k, nil
	}
	tried = append(tried, "env SYNTHETIC_API_KEY")
	if k := c.env.getenv("SYNTHETIC_API_KEY"); k != "" {
		return k, nil
	}
	keyFile := filepath.Join(c.env.home(), ".synthetic", "api_key")
	tried = append(tried, keyFile)
	if k := keyFromFile(keyFile); k != "" {
		return k, nil
	}
	return "", &ErrSetup{Tried: tried}
}

// syntheticQuotas is the documented v2 payload.
type syntheticQuotas struct {
	Subscription struct {
		Limit    float64 `json:"limit"`
		Requests float64 `json:"requests"`
		RenewsAt string  `json:"renewsAt"`
	} `json:"subscription"`
}

func (c *syntheticCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: "synthetic", Name: "Synthetic", UpdatedAt: c.env.now()}
	key, setup := c.key()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var out syntheticQuotas
	if err := bearerGet(ctx, c.env, c.base+"/v2/quotas", key, &out); err != nil {
		rep.State, rep.Err = StateFault, Scrub(faultMessage(err))
		return rep, err
	}
	sub := out.Subscription
	if sub.Limit <= 0 {
		msg := "the quotas endpoint carried no subscription limit"
		rep.State, rep.Err = StateFault, msg
		return rep, errors.New(msg)
	}
	w := Window{
		Key:           "primary",
		Label:         "Subscription",
		ShortLabel:    "Sub",
		HasPercent:    true,
		UsedPercent:   math.Max(0, math.Min(100, sub.Requests/sub.Limit*100)),
		WindowMinutes: 0, // the period's length is not served; no elapsed, no pace
		DisplayValue: fmt.Sprintf("%s / %s requests",
			formatAmount(sub.Requests), formatAmount(sub.Limit)),
	}
	if t, err := time.Parse(time.RFC3339Nano, sub.RenewsAt); err == nil {
		w.ResetsAt = t.UTC()
	}
	rep.Windows = []Window{w}
	rep.State = StateFresh
	return rep, nil
}
