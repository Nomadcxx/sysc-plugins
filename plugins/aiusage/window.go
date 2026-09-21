// Package aiusage implements the AI Usage plugin: collectors that normalize
// each provider's quota data into one window model, a resilient fetch loop
// that publishes immutable report snapshots, and view-tree builders for the
// bar pill and the master/detail panel.
//
// The design it implements lives in docs/plans/2026-09-19-aiusage-design.md;
// the data paths it ports are documented in
// docs/plans/2026-09-19-aiusage-research.md.
package aiusage

import (
	"fmt"
	"regexp"
	"time"
)

// State is a provider's report state. Loading is report-level, not here: a
// provider always has one of these four truths about its last read.
type State uint8

const (
	// StateFresh is a live, successful read.
	StateFresh State = iota
	// StateNeedsSetup means no credential was found. The instruction is the
	// whole point of the card, so this state never retains last-good data.
	StateNeedsSetup
	// StateFault covers every non-credential failure: network, schema,
	// rejected token. Last good data carries forward, flagged stale.
	StateFault
	// StateNoData is a working read that found nothing to report.
	StateNoData
)

// Window is one quota window normalized across every provider. Percent-less
// windows (balances, informational probes) carry DisplayValue and never a
// fabricated UsedPercent.
type Window struct {
	Key              string // "primary" | "secondary" | "tertiary"
	Label            string // "Session" - "Weekly" - provider wording
	ShortLabel       string // "5h" - "Wk" - "Mo"
	UsedPercent      float64
	HasPercent       bool
	WindowMinutes    int // 0 = unbounded or unknown
	ResetsAt         time.Time
	ResetDescription string
	DisplayValue     string
}

// ProviderReport is one provider's normalized snapshot.
type ProviderReport struct {
	ID        string
	Name      string
	Plan      string
	Account   string
	Windows   []Window
	Credits   *float64
	State     State
	Stale     bool
	UpdatedAt time.Time
	Err       string // human-readable, secret-scrubbed
}

// Report is the immutable snapshot views render.
type Report struct {
	Providers  []ProviderReport
	CapturedAt time.Time
	Loading    bool
}

// LabelForMinutes names a window from its length. The well-known lengths get
// stable labels; anything else is described, never guessed at slot position.
func LabelForMinutes(m int) (label, short string) {
	switch m {
	case 300:
		return "Session", "5h"
	case 10080:
		return "Weekly", "Wk"
	case 43200:
		return "Monthly", "Mo"
	}
	if m <= 0 {
		return "", ""
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d hour", m/60), fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%d min", m), fmt.Sprintf("%dm", m)
}

// FormatCountdown renders the gap to a reset. Days and hours from a day out,
// hours and minutes below that, "now" once past.
func FormatCountdown(resets, now time.Time) string {
	if resets.IsZero() {
		return ""
	}
	d := resets.Sub(now)
	if d <= 0 {
		return "now"
	}
	if days := int(d.Hours()) / 24; days >= 1 {
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
	}
	if h := int(d.Hours()); h >= 1 {
		return fmt.Sprintf("%dh %dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// ElapsedPercent reports how far through the window the clock is, clamped to
// zero through one hundred. It is derivable only when both the window length
// and the reset instant are known.
func ElapsedPercent(w Window, now time.Time) (float64, bool) {
	if w.WindowMinutes <= 0 || w.ResetsAt.IsZero() {
		return 0, false
	}
	start := w.ResetsAt.Add(-time.Duration(w.WindowMinutes) * time.Minute)
	total := w.ResetsAt.Sub(start).Minutes()
	if total <= 0 {
		return 0, false
	}
	pct := now.Sub(start).Minutes() / total * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// Pace is usage minus window-elapsed, in percentage points: positive is
// spending faster than the clock. Suppressed when either side is unknown or
// outside the derivable band.
func Pace(w Window, now time.Time) (int, bool) {
	if !w.HasPercent {
		return 0, false
	}
	elapsed, ok := ElapsedPercent(w, now)
	if !ok {
		return 0, false
	}
	return int(w.UsedPercent - elapsed), true
}

// Severity ranks a percent against the alert thresholds: zero is calm, three
// is depleted.
func Severity(pct float64, warn, crit int) int {
	switch {
	case pct >= 99:
		return 3
	case pct >= float64(crit):
		return 2
	case pct >= float64(warn):
		return 1
	default:
		return 0
	}
}

// Headline picks the window a capsule leads with. A window at or past one
// hundred percent blocks, and among several blockers the latest reset is the
// one that matters. Otherwise the highest percent leads; percent-less windows
// keep the source's order.
func Headline(ws []Window) *Window {
	var blocking *Window
	for i := range ws {
		w := &ws[i]
		if w.HasPercent && w.UsedPercent >= 100 && (blocking == nil || w.ResetsAt.After(blocking.ResetsAt)) {
			blocking = w
		}
	}
	if blocking != nil {
		return blocking
	}
	var best *Window
	for i := range ws {
		w := &ws[i]
		if !w.HasPercent {
			continue
		}
		if best == nil || w.UsedPercent > best.UsedPercent {
			best = w
		}
	}
	if best != nil {
		return best
	}
	if len(ws) == 0 {
		return nil
	}
	return &ws[0]
}

// Scrub removes credential-shaped substrings from provider-derived text
// before it can reach a report, a cache file, or a view.
var (
	scrubKey     = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{8,}`)
	scrubBearer  = regexp.MustCompile(`(?i)bearer\s+\S+`)
	scrubLongB64 = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
)

func Scrub(s string) string {
	s = scrubKey.ReplaceAllString(s, "[redacted]")
	s = scrubBearer.ReplaceAllString(s, "[redacted]")
	s = scrubLongB64.ReplaceAllString(s, "[redacted]")
	return s
}
