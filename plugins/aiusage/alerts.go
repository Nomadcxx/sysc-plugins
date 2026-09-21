package aiusage

import (
	"fmt"
	"time"
)

// Alert levels and their dedupe keys. A level fires at most once per quota
// window; the ledger persists across restarts so a shell reload cannot
// re-spam the user.
const (
	levelWarn     = 1
	levelCritical = 2
	levelDepleted = 3

	// depletedThreshold is fixed: a window at 99% is functionally gone, and
	// a settings-controlled depleted level would either duplicate critical
	// or never fire.
	depletedThreshold = 99

	// hysteresis is how far below a threshold the percent must fall before
	// the level's record clears: boundary hovering never re-fires.
	hysteresis = 5

	// ledgerTTL prunes records silent for this long.
	ledgerTTL = 60 * 24 * time.Hour
)

// AlertRec is one fired (provider, window, level). ResetsAt pins the quota
// window the fire belongs to — unix seconds, zero meaning the window had no
// known reset. FiredAt drives pruning.
type AlertRec struct {
	ResetsAt int64 `json:"resets_at"`
	FiredAt  int64 `json:"fired_at"`
}

// Ledger is the persisted dedupe state, keyed "provider:window:level".
type Ledger map[string]AlertRec

// Notice is one notification the caller delivers.
type Notice struct {
	Summary  string
	Body     string
	Critical bool
}

// FixThresholds applies the settings nudge: a critical at or below warn is
// raised to warn+5 rather than refused, capped so it can never collide with
// the fixed depleted level.
func FixThresholds(warn, crit int) (int, int) {
	if crit <= warn {
		crit = warn + 5
		if crit > 100 {
			crit = 100
		}
	}
	return warn, crit
}

// AlertConfig is the alert settings surface: the global ladder, per-provider
// warn overrides, which windows alert, and how loudly.
type AlertConfig struct {
	Warn int
	Crit int
	// PerProvider overrides the warn threshold per provider id (AIOC's
	// `provider:percent` overrides). Critical stays global.
	PerProvider map[string]int
	// Scope selects which windows alert: "all" (default) or "primary".
	Scope string
	// Cooldown suppresses re-firing for this long across windows — including
	// rollovers. Zero means the default: once per window per level.
	Cooldown time.Duration
}

// CheckAlerts walks every fresh window and returns the notices to deliver,
// updating the ledger in place. Faulty, stale, and setup-less providers
// never alert — an unreachable provider is not a full one.
func CheckAlerts(r Report, ac AlertConfig, led Ledger, now time.Time) []Notice {
	var out []Notice
	for _, p := range r.Providers {
		if p.State != StateFresh || p.Stale {
			continue
		}
		warn := ac.Warn
		if override, ok := ac.PerProvider[p.ID]; ok {
			warn = override
		}
		warn, crit := FixThresholds(warn, ac.Crit)
		for _, w := range p.Windows {
			if !w.HasPercent {
				continue
			}
			if ac.Scope == "primary" && w.Key != "primary" {
				continue
			}
			reset := int64(0)
			if !w.ResetsAt.IsZero() {
				reset = w.ResetsAt.Unix()
			}
			for _, lvl := range []struct {
				level     int
				threshold int
				name      string
			}{
				{levelWarn, warn, "warn"},
				{levelCritical, crit, "critical"},
				{levelDepleted, depletedThreshold, "depleted"},
			} {
				key := alertKey(p.ID, w.Key, lvl.level)
				switch {
				case w.UsedPercent < float64(lvl.threshold-hysteresis):
					delete(led, key) // re-arm on a meaningful fall
				case w.UsedPercent >= float64(lvl.threshold):
					// An absent record never blocks; a stored one blocks only
					// when it covers this window (same or unknown reset) —
					// and never inside the cooldown window.
					if rec, ok := led[key]; ok {
						if rec.matches(reset) {
							continue // already reported for this window
						}
						if ac.Cooldown > 0 && now.Unix()-rec.FiredAt < int64(ac.Cooldown.Seconds()) {
							continue // too soon after the last notice
						}
					}
					led[key] = AlertRec{ResetsAt: reset, FiredAt: now.Unix()}
					out = append(out, p.notice(w, lvl.name, lvl.level, now))
				}
			}
		}
	}
	return out
}

// matches reports whether a record already covers this window. Resets are
// compared only when both are known; an unknown record covers unknown-reset
// windows and is replaced the moment a real reset appears.
func (r AlertRec) matches(reset int64) bool {
	if r.ResetsAt == 0 {
		return reset == 0
	}
	if reset == 0 {
		return true // same logical window, its reset instant vanished
	}
	return r.ResetsAt == reset
}

func alertKey(provider, window string, level int) string {
	return fmt.Sprintf("%s:%s:%d", provider, window, level)
}

// Prune drops ledger entries that have been silent past the TTL.
func Prune(led Ledger, now time.Time) {
	cutoff := now.Add(-ledgerTTL).Unix()
	for key, rec := range led {
		if rec.FiredAt < cutoff {
			delete(led, key)
		}
	}
}

func (p ProviderReport) notice(w Window, name string, level int, now time.Time) Notice {
	verb := "Above"
	if level == levelDepleted {
		verb = "At" // 99%+ reads as reached, not approaching
	}
	body := fmt.Sprintf("%s %s threshold · %v%% used", verb, name, w.UsedPercent)
	if cd := FormatCountdown(w.ResetsAt, now); cd != "" {
		body += " · resets in " + cd
	} else {
		body += " · reset time unknown"
	}
	return Notice{
		Summary:  fmt.Sprintf("%s · %s %v%%", p.Name, w.Label, w.UsedPercent),
		Body:     body,
		Critical: level >= levelCritical,
	}
}
