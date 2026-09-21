// Command sysc-plugin-aiusage is the AI Usage plugin process: it speaks the
// plugin/v1 wire protocol, drives the collector loop, publishes the bar and
// panel views, and delivers threshold alerts.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/aiusage"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in, out *os.File) error {
	c := v1.NewClient(in, out)
	hello, err := c.Handshake(v1.Identity{ID: "org.sysc.aiusage", Name: "AI Usage", Version: "0.1.0"})
	if err != nil {
		return err
	}
	minor := negotiate(hello.Supported)

	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	paths := struct{ cache, history string }{
		filepath.Join(cacheDir, "sysc-shell", "plugins", "aiusage", "report.json"),
		filepath.Join(cacheDir, "sysc-shell", "plugins", "aiusage", "history.jsonl"),
	}

	var (
		loop     *aiusage.Loop
		cfg      aiusage.Config
		inst     = aiusage.DefaultInstance()
		selected string
		ledger   = aiusage.Ledger{}
	)

	ensure := func(values map[string]any) {
		cfg = resolveConfig(values, minor)
		inst = resolveInstance(values)
		if loop == nil {
			loop = aiusage.NewLoop(registry(), cfg, aiusage.Env{}, paths.cache, paths.history)
			loop.WarmStart()
		} else {
			loop.Reconfigure(registry(), cfg)
		}
	}

	type view struct {
		kind v1.ViewKind
		rev  uint64
	}
	views := map[string]view{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			incoming <- msg
		}
	}()

	publish := func() {
		if loop == nil {
			return
		}
		rep := loop.Snapshot()
		hist := loop.History(selected, 30)
		for id, v := range views {
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = aiusage.BarTree(rep, inst, cfg, minor, time.Now())
			case v1.ViewTooltip:
				// The shell auto-opens this view under the bar widget; a
				// text-only tree is what it can paint.
				root = aiusage.TooltipTree(rep, inst, cfg, minor, time.Now())
			default:
				root = aiusage.PanelTree(rep, selected, hist, cfg, minor, time.Now())
			}
			v.rev++
			views[id] = v
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	round := func(force bool) {
		if loop == nil {
			return
		}
		publish() // the refresh button reads busy while the round runs
		loop.SetLoading(true)
		rep := loop.Round(ctx, force)
		loop.SetLoading(false)
		for _, n := range aiusage.CheckAlerts(rep, cfg.Warn, cfg.Crit, ledger, time.Now()) {
			urgency := v1.UrgencyNormal
			if n.Critical {
				urgency = v1.UrgencyCritical
			}
			_, _ = c.Call(ctx, v1.CallNotify, v1.NotifyParams{Summary: n.Summary, Body: n.Body, Urgency: urgency})
		}
		if raw, err := json.Marshal(ledger); err == nil {
			_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "alerts", Value: raw})
		}
		publish()
	}

	// The alert ledger survives restarts via plugin state.
	loadLedger := func() {
		callCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		reply, err := c.Call(callCtx, v1.CallStateGet, v1.StateGetParams{Key: "alerts"})
		if err != nil || !reply.OK || len(reply.Result) == 0 {
			return
		}
		var result v1.StateGetResult
		if json.Unmarshal(reply.Result, &result) == nil && result.Found {
			var led aiusage.Ledger
			if json.Unmarshal(result.Value, &led) == nil {
				ledger = led
			}
		}
	}

	// The clock drives wake-from-suspend detection and countdown re-renders.
	clock := time.NewTicker(time.Minute)
	defer clock.Stop()
	lastTick := time.Now()
	schedule := time.NewTimer(time.Minute)
	defer schedule.Stop()

	started := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				if !started {
					started = true
					ensure(nil) // the first settings push fills real values
					loadLedger()
					// Warm-start aware: floors and the cross-instance cache
					// guard decide who fetches; a reload inside the guard
					// serves the cache and touches no endpoint.
					round(false)
				}
				views[m.ViewID] = view{kind: m.View}
				publish()
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					v.rev = 0
					views[m.ViewID] = v
				}
				publish()
			case *v1.InputEvent:
				switch {
				case m.Node == "open":
					_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
				case m.Node == "refresh":
					// Floors hold on a manual refresh: backoff is overridden
					// by Force, the rate-limit discipline is not.
					round(false)
				case strings.HasPrefix(m.Node, "retry:"):
					// Retry re-arms exactly the failed collector — the
					// zeroed next-due makes it due, the others keep theirs.
					loop.Force(strings.TrimPrefix(m.Node, "retry:"))
					round(false)
				case strings.HasPrefix(m.Node, "sel:"):
					selected = strings.TrimPrefix(m.Node, "sel:")
					publish()
				}
			case *v1.SettingsChanged:
				ensure(m.Values)
				schedule.Reset(cfg.Refresh)
				round(true)
			}
		case <-clock.C:
			gap := time.Since(lastTick)
			lastTick = time.Now()
			if gap > 2*time.Minute {
				// The shell was asleep: the data is old, refresh now.
				round(false)
			} else {
				publish() // countdowns re-render locally, never re-fetching
			}
		case <-schedule.C:
			lastTick = time.Now()
			round(false)
			schedule.Reset(cfg.Refresh)
		}
	}
}

// negotiate picks the highest minor the host offers, capped at the newest
// wire this plugin speaks. A minor-2 host gets the meter tree.
func negotiate(supported []v1.Version) int {
	minor := 0
	for _, v := range supported {
		if v.Major == 1 && v.Minor > minor && v.Minor <= 4 {
			minor = v.Minor
		}
	}
	return minor
}

// registry maps the provider ids the settings name onto constructors.
func registry() aiusage.Registry {
	return aiusage.Registry{
		"claude":      aiusage.NewOAuthUsage,
		"codex":       aiusage.NewSnapshot,
		"commandcode": aiusage.NewCommandCode,
		"minimax":     aiusage.NewMinimax,
		"ollama":      aiusage.NewOllama,
		"synthetic":   aiusage.NewSynthetic,
	}
}

// resolveConfig reads the committed settings into the loop's config.
func resolveConfig(values map[string]any, minor int) aiusage.Config {
	b := func(key string, d bool) bool {
		if v, ok := values[key].(bool); ok {
			return v
		}
		return d
	}
	i := func(key string, d int) int {
		if v, ok := values[key].(float64); ok {
			return int(v)
		}
		return d
	}
	s := func(key string) string {
		if v, ok := values[key].(string); ok {
			return v
		}
		return ""
	}
	// The nudge is applied once here so views and alerts agree on the
	// thresholds the settings commit to.
	warn, crit := aiusage.FixThresholds(i("warn_threshold", 85), i("critical_threshold", 95))
	return aiusage.Config{
		Track: map[string]bool{
			"claude":      b("track_claude", true),
			"codex":       b("track_codex", true),
			"commandcode": b("track_commandcode", true),
			"ollama":      b("track_ollama", false),
			"minimax":     b("track_minimax", false),
			"synthetic":   b("track_synthetic", false),
		},
		Keys: map[string]string{
			"ollama":    s("ollama_api_key"),
			"minimax":   s("minimax_api_key"),
			"synthetic": s("synthetic_api_key"),
		},
		Refresh:   time.Duration(i("refresh_interval", 300)) * time.Second,
		Warn:      warn,
		Crit:      crit,
		HostMinor: minor,
	}
}

// resolveInstance reads the bar instance's settings.
func resolveInstance(values map[string]any) aiusage.Instance {
	b := func(key string, d bool) bool {
		if v, ok := values[key].(bool); ok {
			return v
		}
		return d
	}
	s := func(key, d string) string {
		if v, ok := values[key].(string); ok && v != "" {
			return v
		}
		return d
	}
	inst := aiusage.DefaultInstance()
	inst.Vendor = s("vendor", "auto")
	inst.Visualization = s("visualization", "radial")
	inst.GlyphPosition = s("glyph_position", "before")
	inst.Extras = s("extras", "none")
	inst.ShowValue = b("show_value", true)
	inst.ShowGlyph = b("show_glyph", true)
	inst.ShowName = b("show_name", false)
	return inst
}
