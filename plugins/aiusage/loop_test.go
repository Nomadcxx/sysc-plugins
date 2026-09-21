package aiusage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCollector is a scripted collector: it counts calls and can fail,
// report a setup error, or carry a rate-limit floor.
type fakeCollector struct {
	id    string
	calls int
	rep   ProviderReport
	err   error
	floor time.Duration
}

func (f *fakeCollector) ID() string { return f.id }
func (f *fakeCollector) Floor() time.Duration {
	if f.floor > 0 {
		return f.floor
	}
	return 0
}
func (f *fakeCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	f.calls++
	return f.rep, f.err
}

// loopEnv returns a test env whose clock the test advances through the
// returned pointer.
func loopEnv() (Env, *time.Time) {
	now := base
	return Env{Now: func() time.Time { return now }}, &now
}

func testRegistry(reps ...*fakeCollector) Registry {
	reg := Registry{}
	for _, fc := range reps {
		fc := fc
		reg[fc.id] = func(Env) Collector { return fc }
	}
	return reg
}

func testConfig() Config {
	return Config{
		Track:     map[string]bool{"alpha": true, "beta": true},
		Refresh:   time.Minute,
		Warn:      85,
		Crit:      95,
		HostMinor: 4,
	}
}

func freshRep(id string) ProviderReport {
	return ProviderReport{ID: id, Name: id, State: StateFresh, Windows: []Window{
		{Key: "primary", HasPercent: true, UsedPercent: 40, WindowMinutes: 300},
	}}
}

func loopPaths(t *testing.T) (string, string) {
	dir := t.TempDir()
	return filepath.Join(dir, "report.json"), filepath.Join(dir, "history.jsonl")
}

func TestRoundIsSerialAndTrackedOnly(t *testing.T) {
	t.Parallel()

	env, _ := loopEnv()
	alpha := &fakeCollector{id: "alpha", rep: freshRep("alpha")}
	beta := &fakeCollector{id: "beta", rep: freshRep("beta")}
	gamma := &fakeCollector{id: "gamma", rep: freshRep("gamma")} // untracked
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(alpha, beta, gamma), testConfig(), env, cache, history)

	rep := l.Round(t.Context(), false)
	if alpha.calls != 1 || beta.calls != 1 {
		t.Fatalf("calls = %d/%d", alpha.calls, beta.calls)
	}
	if gamma.calls != 0 {
		t.Fatalf("untracked collector fetched: %d calls", gamma.calls)
	}
	if len(rep.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(rep.Providers))
	}
	if rep.Providers[0].ID != "alpha" || rep.Providers[1].ID != "beta" {
		t.Fatalf("order = %s, %s; want sorted", rep.Providers[0].ID, rep.Providers[1].ID)
	}
}

func TestFloorSkipsWithoutChurn(t *testing.T) {
	t.Parallel()

	env, now := loopEnv()
	// Refresh is one minute; this collector's floor is thirty.
	slow := &fakeCollector{id: "alpha", floor: 30 * time.Minute, rep: freshRep("alpha")}
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(slow), testConfig(), env, cache, history)

	if rep := l.Round(t.Context(), false); slow.calls != 1 || len(rep.Providers) != 1 {
		t.Fatalf("first round = %d calls, %+v", slow.calls, rep)
	}

	// Ten minutes later the floor still blocks: skipped, last good carried.
	*now = base.Add(10 * time.Minute)
	rep := l.Round(t.Context(), false)
	if slow.calls != 1 {
		t.Fatalf("floor-blocked round refetched: %d calls", slow.calls)
	}
	if len(rep.Providers) != 1 || rep.Providers[0].Windows[0].UsedPercent != 40 {
		t.Fatalf("carried = %+v", rep.Providers)
	}

	// Past the floor the collector runs again.
	*now = base.Add(31 * time.Minute)
	l.Round(t.Context(), false)
	if slow.calls != 2 {
		t.Fatalf("post-floor calls = %d, want 2", slow.calls)
	}
}

func TestBackoffDoublesAndForceOverrides(t *testing.T) {
	t.Parallel()

	env, now := loopEnv()
	failing := &fakeCollector{id: "alpha", err: errors.New("endpoint unreachable")}
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(failing), testConfig(), env, cache, history) // refresh 60s

	// First failure: next due in 2× interval.
	l.Round(t.Context(), false)
	if failing.calls != 1 {
		t.Fatalf("first round calls = %d", failing.calls)
	}

	*now = base.Add(90 * time.Second)
	l.Round(t.Context(), false)
	if failing.calls != 1 {
		t.Fatalf("backoff did not block: %d calls", failing.calls)
	}

	// Past 2× interval it runs, and the wait doubles again.
	*now = base.Add(2*time.Minute + time.Second)
	l.Round(t.Context(), false)
	if failing.calls != 2 {
		t.Fatalf("second round calls = %d", failing.calls)
	}
	*now = base.Add(3 * time.Minute)
	l.Round(t.Context(), false)
	if failing.calls != 2 {
		t.Fatalf("2^n backoff did not double: %d calls", failing.calls)
	}

	// A forced round overrides the backoff.
	l.Round(t.Context(), true)
	if failing.calls != 3 {
		t.Fatalf("force did not override: %d calls", failing.calls)
	}
}

func TestRetainLastGoodOnlyWhenEarned(t *testing.T) {
	t.Parallel()

	env, now := loopEnv()
	scripted := &fakeCollector{id: "alpha"}
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(scripted), testConfig(), env, cache, history)

	// Round 1: fresh.
	scripted.rep, scripted.err = freshRep("alpha"), nil
	good := l.Round(t.Context(), false)
	if good.Providers[0].State != StateFresh {
		t.Fatalf("first round = %+v", good.Providers[0])
	}

	// Round 2: a fault carries the last good forward, flagged stale.
	scripted.rep, scripted.err = ProviderReport{}, errors.New("boom")
	*now = base.Add(time.Minute)
	l.Force("alpha")
	faulted := l.Round(t.Context(), true)
	p := faulted.Providers[0]
	if p.State != StateFresh || !p.Stale || len(p.Windows) == 0 {
		t.Fatalf("retained = %+v, want last good flagged stale", p)
	}

	// Round 3: a setup error never retains — the instruction is the point.
	scripted.rep, scripted.err = ProviderReport{}, &ErrSetup{Tried: []string{"nowhere"}}
	*now = base.Add(2 * time.Minute)
	l.Force("alpha")
	setup := l.Round(t.Context(), true)
	p = setup.Providers[0]
	if p.State != StateNeedsSetup || len(p.Windows) != 0 {
		t.Fatalf("setup card = %+v, want no retained windows", p)
	}
}

func TestWarmStartFlagsStaleByAge(t *testing.T) {
	t.Parallel()

	env, now := loopEnv()
	cache, history := loopPaths(t)
	cfg := testConfig()
	fc := &fakeCollector{id: "alpha", rep: freshRep("alpha")}
	l := NewLoop(testRegistry(fc), cfg, env, cache, history)
	l.Round(t.Context(), false)

	// A fresh cache warm-starts unflagged.
	l2 := NewLoop(Registry{}, cfg, env, cache, history)
	warm := l2.WarmStart()
	if len(warm.Providers) != 1 || warm.Providers[0].Stale {
		t.Fatalf("fresh cache = %+v", warm.Providers)
	}

	// Beyond 2× interval the warm start is stale, still drawn.
	*now = base.Add(3 * cfg.Refresh)
	l3 := NewLoop(Registry{}, cfg, env, cache, history)
	stale := l3.WarmStart()
	if len(stale.Providers) != 1 || !stale.Providers[0].Stale {
		t.Fatalf("old cache = %+v, want stale-flagged", stale.Providers)
	}
}

func TestHistoryRecordsAndTrims(t *testing.T) {
	t.Parallel()

	env, now := loopEnv()
	fc := &fakeCollector{id: "alpha", rep: freshRep("alpha")}
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(fc), testConfig(), env, cache, history)
	l.Round(t.Context(), false)

	samples := l.History("alpha", 30)
	if len(samples) != 1 || samples[0] != 40 {
		t.Fatalf("history = %v, want one 40", samples)
	}

	// Unbounded and percent-less windows are skipped.
	fc.rep = ProviderReport{ID: "alpha", State: StateFresh, Windows: []Window{
		{Key: "primary", HasPercent: true, UsedPercent: 55, WindowMinutes: 300},
		{Key: "secondary", HasPercent: true, UsedPercent: 10, WindowMinutes: 0},
		{Key: "tertiary", HasPercent: false, DisplayValue: "$1 / $5", WindowMinutes: 300},
	}}
	*now = base.Add(time.Minute)
	l.Force("alpha")
	l.Round(t.Context(), true)
	samples = l.History("alpha", 30)
	if len(samples) != 2 || samples[1] != 55 {
		t.Fatalf("history after second round = %v", samples)
	}

	// A stale-carried provider records nothing new.
	fc.err, fc.rep = errors.New("down"), ProviderReport{}
	*now = base.Add(10 * time.Minute)
	l.Force("alpha")
	l.Round(t.Context(), true)
	if samples := l.History("alpha", 30); len(samples) != 2 {
		t.Fatalf("faulted round polluted history: %v", samples)
	}

	// A forced round on a healthy collector records again.
	fc.err, fc.rep = nil, freshRep("alpha")
	*now = base.Add(11 * time.Minute)
	l.Force("alpha")
	l.Round(t.Context(), true)
	if samples := l.History("alpha", 30); len(samples) != 3 {
		t.Fatalf("recovered round missing from history: %v", samples)
	}
}

func TestScrubAppliesAtPublish(t *testing.T) {
	t.Parallel()

	env, _ := loopEnv()
	dirty := &fakeCollector{id: "alpha", rep: ProviderReport{
		ID: "alpha", Name: "alpha",
		Plan:  "plan sk-abc123def456hi",
		Err:   "failed with sk-xyz98765432ab",
		State: StateFault,
		Windows: []Window{{Key: "primary", HasPercent: false,
			DisplayValue: "key gz1Ax9+/EE0fF2gHh5iJ8kK7lL6mM5nN4oO3pP2qQ1rR=="}}},
	}
	cache, history := loopPaths(t)
	l := NewLoop(testRegistry(dirty), testConfig(), env, cache, history)

	rep := l.Round(t.Context(), false)
	p := rep.Providers[0]
	if strings.Contains(p.Plan, "sk-abc") || strings.Contains(p.Err, "sk-xyz") {
		t.Fatalf("secrets survived the scrub: %q / %q", p.Plan, p.Err)
	}
	if strings.Contains(p.Windows[0].DisplayValue, "gz1Ax9") {
		t.Fatalf("display secret survived: %q", p.Windows[0].DisplayValue)
	}
}
