package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Floored is implemented by collectors whose source rate-limits harder than
// the refresh interval. The floor is scheduler-level: a floor-blocked round
// is never scheduled at all, so it cannot error and cannot churn the stale
// machinery.
type Floored interface {
	Floor() time.Duration
}

// Config is the resolved settings the loop and views run on.
type Config struct {
	Track     map[string]bool
	Keys      map[string]string // pasted keys by provider id
	Refresh   time.Duration
	Warn      int
	Crit      int
	HostMinor int // negotiated at handshake; views consume it

	// Alert surface: per-provider warn overrides ("provider:percent" CSV),
	// which windows alert, and the re-fire cooldown.
	AlertPerProvider map[string]int
	AlertScope       string        // "all" | "primary"
	AlertCooldown    time.Duration // 0 = once per window per level

	// History keeps this many lines per trim cycle (500 / 2000 / 10000).
	HistoryRetention int
}

// historyCap is the steady-state line budget; the file is trimmed only when
// it exceeds twice this.
const historyCap = 2000

// crossInstanceGuard is how young a cache snapshot must be before a reload
// trusts it instead of refetching: the usage endpoints rate-limit hard, and
// shells reload far more often than quotas move.
const crossInstanceGuard = 150 * time.Second

// Loop owns the collectors, the serial fetch rounds, the per-collector
// schedule (floors and backoff), the warm-start cache, and the usage
// history. One round runs at a time; the service loop in main.go drives it.
type Loop struct {
	env         Env
	cfg         Config
	cachePath   string
	historyPath string

	mu         sync.Mutex
	collectors map[string]Collector
	order      []string // sorted tracked ids, so rounds are deterministic
	nextDue    map[string]time.Time
	failures   map[string]int
	lastGood   map[string]ProviderReport
	last       Report
}

// NewLoop builds the loop and constructs a collector per tracked provider.
func NewLoop(reg Registry, cfg Config, env Env, cachePath, historyPath string) *Loop {
	l := &Loop{
		env: env, cfg: cfg,
		cachePath: cachePath, historyPath: historyPath,
		collectors: map[string]Collector{},
		nextDue:    map[string]time.Time{},
		failures:   map[string]int{},
		lastGood:   map[string]ProviderReport{},
	}
	l.rebuild(reg)
	return l
}

// rebuild (re)constructs the tracked collector set. Untracked providers
// vanish; newly tracked ones join sorted.
func (l *Loop) rebuild(reg Registry) {
	l.collectors = map[string]Collector{}
	l.order = nil
	for id := range l.cfg.Track {
		if !l.cfg.Track[id] {
			continue
		}
		if new, ok := reg[id]; ok {
			l.collectors[id] = new(l.env)
			l.order = append(l.order, id)
		}
	}
	sort.Strings(l.order)
}

// Reconfigure applies changed settings: tracking, keys, thresholds, refresh.
// The caller follows it with a forced round.
func (l *Loop) Reconfigure(reg Registry, cfg Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cfg = cfg
	if cfg.Keys != nil {
		l.env.Keys = cfg.Keys
	}
	l.rebuild(reg)
}

// WarmStart loads the cache so a shell restart shows numbers immediately.
// A snapshot older than twice the interval is flagged stale, still drawn.
func (l *Loop) WarmStart() Report {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = l.loadCache()
	return l.last
}

func (l *Loop) loadCache() Report {
	raw, err := os.ReadFile(l.cachePath)
	if err != nil {
		return Report{}
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return Report{} // a torn cache degrades to a cold start
	}
	if r.CapturedAt.Add(2 * l.cfg.Refresh).Before(l.env.now()) {
		for i := range r.Providers {
			r.Providers[i].Stale = true
		}
	}
	for i := range r.Providers {
		p := r.Providers[i]
		if p.State != StateFresh {
			continue
		}
		l.lastGood[p.ID] = p
		// A warm cache counts as a reading: the collector's next due moment
		// runs from the capture, and never sooner than the cross-instance
		// guard — a reload inside the guard serves the cache and touches
		// no endpoint.
		due := p.UpdatedAt
		if due.Before(r.CapturedAt) {
			due = r.CapturedAt
		}
		due = due.Add(maxDuration(l.cfg.Refresh, l.floorOf(p.ID)))
		if earliest := r.CapturedAt.Add(crossInstanceGuard); due.Before(earliest) {
			due = earliest
		}
		l.nextDue[p.ID] = due
	}
	return r
}

// floorOf returns the collector's rate-limit floor, if it declares one.
func (l *Loop) floorOf(id string) time.Duration {
	if col, ok := l.collectors[id]; ok {
		if f, isFloored := col.(Floored); isFloored {
			return f.Floor()
		}
	}
	return 0
}

// SetLoading flags the published snapshot while a round runs, so the
// refresh button can read as busy.
func (l *Loop) SetLoading(v bool) Report {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last.Loading = v
	return l.last
}

// Force queues a per-provider forced fetch on the next round.
func (l *Loop) Force(providerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextDue[providerID] = time.Time{} // due immediately, backoff overridden
	l.failures[providerID] = 0
}

// Snapshot returns the most recent report.
func (l *Loop) Snapshot() Report {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

// Round fetches every tracked provider once, serially, in sorted order.
// force bypasses floors and backoff — a user who explicitly asked is never
// refused. Returns the published snapshot.
func (l *Loop) Round(ctx context.Context, force bool) Report {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.env.now()
	var providers []ProviderReport
	for _, id := range l.order {
		col := l.collectors[id]
		if col == nil {
			continue
		}
		floor := time.Duration(0)
		if f, ok := col.(Floored); ok {
			floor = f.Floor()
		}
		if !force && now.Before(l.nextDue[id]) {
			if lg, ok := l.lastGood[id]; ok {
				providers = append(providers, lg)
			}
			continue
		}
		rep, err := col.Fetch(ctx)
		scrubProvider(&rep)
		switch {
		case isSetup(err):
			// The instruction is the whole point of the card: never retain.
			// The scrub runs here too — the error text is provider-derived.
			rep.State = StateNeedsSetup
			rep.Err = Scrub(err.Error())
			l.nextDue[id] = now.Add(l.cfg.Refresh)
			providers = append(providers, rep)
		case err != nil:
			rep.State = StateFault
			rep.Err = Scrub(err.Error())
			l.failures[id]++
			l.nextDue[id] = now.Add(l.backoffWait(id, floor))
			// Last good carries forward — but only a snapshot that was
			// itself error-free and non-empty counts as "good".
			if lg, ok := l.lastGood[id]; ok && lg.State == StateFresh && len(lg.Windows) > 0 {
				lg.Stale = true
				providers = append(providers, lg)
			} else {
				providers = append(providers, rep)
			}
		default:
			rep.State = StateFresh
			l.failures[id] = 0
			l.nextDue[id] = now.Add(maxDuration(l.cfg.Refresh, floor))
			l.lastGood[id] = rep
			providers = append(providers, rep)
		}
	}
	report := Report{Providers: providers, CapturedAt: now}
	l.writeCache(report)
	l.appendHistory(report)
	l.last = report
	return report
}

func isSetup(err error) bool {
	var setup *ErrSetup
	return errors.As(err, &setup)
}

// backoffWait doubles per consecutive failure from the second one, capped at
// fifteen minutes: one failure keeps the normal cadence, a streak escalates.
func (l *Loop) backoffWait(id string, floor time.Duration) time.Duration {
	shift := l.failures[id] - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 8 {
		shift = 8
	}
	wait := l.cfg.Refresh << shift // interval · 2^(n-1)
	const cap = 15 * time.Minute
	if wait > cap {
		wait = cap
	}
	return maxDuration(wait, floor)
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// scrubProvider runs the credential scrub over every provider-derived string
// entering a report. The cache invariant rests on this, not on providers
// never echoing secrets.
func scrubProvider(p *ProviderReport) {
	p.Err = Scrub(p.Err)
	p.Plan = Scrub(p.Plan)
	p.Account = Scrub(p.Account)
	p.Name = Scrub(p.Name)
	for j := range p.Windows {
		p.Windows[j].ResetDescription = Scrub(p.Windows[j].ResetDescription)
		p.Windows[j].DisplayValue = Scrub(p.Windows[j].DisplayValue)
	}
}

// writeCache persists the snapshot atomically, 0600. Only normalized
// reports are cached — never token material.
func (l *Loop) writeCache(r Report) {
	raw, err := json.Marshal(r)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.cachePath), 0o700); err != nil {
		return
	}
	tmp := l.cachePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, l.cachePath)
}

// historyLine is one recorded sample.
type historyLine struct {
	TS       int64   `json:"ts"`
	Provider string  `json:"provider"`
	Window   string  `json:"window"`
	Pct      float64 `json:"pct"`
}

// appendHistory records one line per tracked window carrying a meaningful
// percent. Percent-less and unbounded windows are skipped so trends stay
// meaningful; the file is trimmed only when it exceeds twice the cap.
func (l *Loop) appendHistory(r Report) {
	var lines []byte
	for _, p := range r.Providers {
		if p.State != StateFresh || p.Stale {
			continue // carried-forward stale numbers would double-count
		}
		for _, w := range p.Windows {
			if !w.HasPercent || w.WindowMinutes <= 0 {
				continue
			}
			line, err := json.Marshal(historyLine{
				TS: r.CapturedAt.Unix(), Provider: p.ID, Window: w.Key, Pct: w.UsedPercent,
			})
			if err != nil {
				continue
			}
			lines = append(append(lines, line...), '\n')
		}
	}
	if len(lines) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.historyPath), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(l.historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Write(lines); err != nil {
		return
	}
	l.trimHistory()
}

// trimHistory keeps the file at the retention once it doubles over.
func (l *Loop) trimHistory() {
	retention := l.cfg.HistoryRetention
	if retention <= 0 {
		retention = historyCap
	}
	raw, err := os.ReadFile(l.historyPath)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) <= 2*retention {
		return
	}
	kept := strings.Join(lines[len(lines)-retention:], "\n") + "\n"
	tmp := l.historyPath + ".tmp"
	if os.WriteFile(tmp, []byte(kept), 0o600) == nil {
		_ = os.Rename(tmp, l.historyPath)
	}
}

// ExportCSV writes the whole history as timestamp,provider,window,percent
// rows into dir (default: the download directory), returning the path.
// Malformed lines drop individually instead of aborting the export.
func (l *Loop) ExportCSV(dir string) (string, error) {
	raw, err := os.ReadFile(l.historyPath)
	if err != nil {
		return "", err
	}
	if dir == "" {
		dir = os.Getenv("XDG_DOWNLOAD_DIR")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Downloads")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "aiusage-history-"+time.Now().Format("20060102-150405")+".csv")
	var b strings.Builder
	b.WriteString("timestamp_iso,timestamp_epoch,provider,window,percent\n")
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var h historyLine
		if json.Unmarshal([]byte(line), &h) != nil || h.Provider == "" {
			continue // a bad line drops one record, not the export
		}
		b.WriteString(fmt.Sprintf("%s,%d,%s,%s,%v\n",
			time.Unix(h.TS, 0).UTC().Format(time.RFC3339), h.TS, h.Provider, h.Window, h.Pct))
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return out, nil
}

// History returns the provider's last n recorded primary-window percents,
// oldest first — the sparkline's data.
func (l *Loop) History(provider string, n int) []float64 {
	raw, err := os.ReadFile(l.historyPath)
	if err != nil {
		return nil
	}
	var out []float64
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var h historyLine
		if json.Unmarshal([]byte(line), &h) != nil {
			continue
		}
		if h.Provider == provider && h.Window == "primary" {
			out = append(out, h.Pct)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}
