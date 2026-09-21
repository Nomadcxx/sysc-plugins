package aiusage

import (
	"testing"
	"time"
)

func alertWindow(pct float64, resets time.Time) Window {
	w := Window{Key: "primary", Label: "Session", HasPercent: true, UsedPercent: pct, WindowMinutes: 300}
	if !resets.IsZero() {
		w.ResetsAt = resets
	}
	return w
}

func alertReport(pct float64, resets time.Time) Report {
	return Report{Providers: []ProviderReport{
		{ID: "alpha", Name: "Alpha", State: StateFresh, Windows: []Window{alertWindow(pct, resets)}},
	}}
}

func alertCfg() AlertConfig { return AlertConfig{Warn: 85, Crit: 95} }

var alertNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
var alertReset = alertNow.Add(2 * time.Hour)

func TestAlertsFireOncePerLevelPerWindow(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	notices := CheckAlerts(alertReport(86, alertReset), alertCfg(), led, alertNow)
	if len(notices) != 1 || notices[0].Critical {
		t.Fatalf("warn notices = %+v", notices)
	}
	if notices[0].Summary != "Alpha · Session 86%" {
		t.Fatalf("summary = %q", notices[0].Summary)
	}

	// Same data again: no re-fire.
	if again := CheckAlerts(alertReport(86, alertReset), alertCfg(), led, alertNow); len(again) != 0 {
		t.Fatalf("re-fired: %+v", again)
	}

	// Escalation to 99 fires the two higher levels, one notice each.
	notices = CheckAlerts(alertReport(99, alertReset), alertCfg(), led, alertNow)
	if len(notices) != 2 {
		t.Fatalf("escalation = %+v, want critical + depleted", notices)
	}
	for _, n := range notices {
		if !n.Critical {
			t.Fatalf("escalated notice not critical: %+v", n)
		}
	}
	// And again, nothing.
	if again := CheckAlerts(alertReport(99, alertReset), alertCfg(), led, alertNow); len(again) != 0 {
		t.Fatalf("depleted re-fired: %+v", again)
	}
}

func TestAlertsHysteresisReArms(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	CheckAlerts(alertReport(86, alertReset), alertCfg(), led, alertNow)

	// Boundary hovering between 80 (threshold−5) and 85 never re-fires…
	CheckAlerts(alertReport(82, alertReset), alertCfg(), led, alertNow)
	if again := CheckAlerts(alertReport(86, alertReset), alertCfg(), led, alertNow); len(again) != 0 {
		t.Fatalf("hover re-fired: %+v", again)
	}

	// …but a meaningful fall clears the record, and the next rise fires.
	CheckAlerts(alertReport(70, alertReset), alertCfg(), led, alertNow)
	if again := CheckAlerts(alertReport(86, alertReset), alertCfg(), led, alertNow); len(again) != 1 {
		t.Fatalf("re-armed rise did not fire: %+v", again)
	}
}

func TestAlertsRolloverReFires(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	CheckAlerts(alertReport(90, alertReset), alertCfg(), led, alertNow)
	// A new window (different reset) re-arms the level.
	if again := CheckAlerts(alertReport(90, alertReset.Add(5*time.Hour)), alertCfg(), led, alertNow); len(again) != 1 {
		t.Fatalf("rollover did not re-fire: %+v", again)
	}
}

func TestAlertsUnknownResetSentinel(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	// A window with no known reset fires once and stores the sentinel.
	if n := CheckAlerts(alertReport(90, time.Time{}), alertCfg(), led, alertNow); len(n) != 1 {
		t.Fatalf("unknown-reset fire = %+v", n)
	}
	if rec := led["alpha:primary:1"]; rec.ResetsAt != 0 {
		t.Fatalf("sentinel = %+v, want ResetsAt 0", rec)
	}
	// It stays quiet while the reset stays unknown.
	if again := CheckAlerts(alertReport(90, time.Time{}), alertCfg(), led, alertNow); len(again) != 0 {
		t.Fatalf("unknown-reset re-fired: %+v", again)
	}
	// A real reset appearing later is a new window: it fires once.
	if again := CheckAlerts(alertReport(90, alertReset), alertCfg(), led, alertNow); len(again) != 1 {
		t.Fatalf("unknown→known did not fire: %+v", again)
	}
	if again := CheckAlerts(alertReport(90, alertReset), alertCfg(), led, alertNow); len(again) != 0 {
		t.Fatalf("known reset re-fired: %+v", again)
	}
	// A known record tolerates the reset instant vanishing (schema drift).
	if again := CheckAlerts(alertReport(90, time.Time{}), alertCfg(), led, alertNow); len(again) != 0 {
		t.Fatalf("drift re-fired: %+v", again)
	}
}

func TestAlertsSkipUnreadyProviders(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	base := alertReport(100, alertReset)
	base.Providers[0].State = StateFault
	if n := CheckAlerts(base, alertCfg(), led, alertNow); len(n) != 0 {
		t.Fatalf("fault fired: %+v", n)
	}
	base.Providers[0].State = StateNeedsSetup
	if n := CheckAlerts(base, alertCfg(), led, alertNow); len(n) != 0 {
		t.Fatalf("setup fired: %+v", n)
	}
	base.Providers[0].State = StateNoData
	if n := CheckAlerts(base, alertCfg(), led, alertNow); len(n) != 0 {
		t.Fatalf("no-data fired: %+v", n)
	}
	base.Providers[0].State = StateFresh
	base.Providers[0].Stale = true
	if n := CheckAlerts(base, alertCfg(), led, alertNow); len(n) != 0 {
		t.Fatalf("stale fired: %+v", n)
	}
}

func TestAlertsPerProviderOverride(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	ac := AlertConfig{Warn: 85, Crit: 95, PerProvider: map[string]int{"alpha": 50}}

	// 60% is calm globally but over alpha's 50% override.
	if n := CheckAlerts(alertReport(60, alertReset), ac, led, alertNow); len(n) != 1 {
		t.Fatalf("override fire = %+v", n)
	}
	// The override provider re-arms at its own threshold minus hysteresis.
	CheckAlerts(alertReport(40, alertReset), ac, led, alertNow)
	if n := CheckAlerts(alertReport(60, alertReset), ac, led, alertNow); len(n) != 1 {
		t.Fatalf("override re-arm = %+v", n)
	}
	// Another provider at the same percent stays calm under the global warn.
	other := Report{Providers: []ProviderReport{
		{ID: "beta", Name: "Beta", State: StateFresh,
			Windows: []Window{alertWindow(60, alertReset)}}}}
	if n := CheckAlerts(other, ac, led, alertNow); len(n) != 0 {
		t.Fatalf("global ladder fired for beta: %+v", n)
	}
}

func TestAlertsWindowScope(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	twoWindows := Report{Providers: []ProviderReport{
		{ID: "alpha", Name: "Alpha", State: StateFresh, Windows: []Window{
			alertWindow(40, alertReset),
			{Key: "secondary", Label: "Weekly", HasPercent: true, UsedPercent: 96,
				WindowMinutes: 10080, ResetsAt: alertReset},
		}}}}

	// Default scope: every window alerts — the weekly window crosses warn
	// and critical, so the ladder fires both once.
	if n := CheckAlerts(twoWindows, alertCfg(), led, alertNow); len(n) != 2 {
		t.Fatalf("all-scope = %+v, want the critical weekly window", n)
	}

	// Primary scope: the weekly window never alerts.
	led2 := Ledger{}
	if n := CheckAlerts(twoWindows, AlertConfig{Warn: 85, Crit: 95, Scope: "primary"}, led2, alertNow); len(n) != 0 {
		t.Fatalf("primary scope fired: %+v", n)
	}
}

func TestAlertsCooldownSuppressesRollover(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	ac := AlertConfig{Warn: 85, Crit: 95, Cooldown: time.Hour}
	if n := CheckAlerts(alertReport(90, alertReset), ac, led, alertNow); len(n) != 1 {
		t.Fatalf("first fire = %+v", n)
	}
	// A rollover inside the cooldown stays quiet…
	if n := CheckAlerts(alertReport(90, alertReset.Add(5*time.Hour)), ac, led, alertNow.Add(10*time.Minute)); len(n) != 0 {
		t.Fatalf("cooldown did not hold: %+v", n)
	}
	// …and expires after the window.
	if n := CheckAlerts(alertReport(90, alertReset.Add(5*time.Hour)), ac, led, alertNow.Add(2*time.Hour)); len(n) != 1 {
		t.Fatalf("cooldown did not expire: %+v", n)
	}
}

func TestFixThresholds(t *testing.T) {
	t.Parallel()

	if w, c := FixThresholds(85, 80); w != 85 || c != 90 {
		t.Fatalf("nudged = %d/%d, want 85/90", w, c)
	}
	if w, c := FixThresholds(98, 50); w != 98 || c != 100 {
		t.Fatalf("capped = %d/%d, want 98/100", w, c)
	}
	if w, c := FixThresholds(85, 95); w != 85 || c != 95 {
		t.Fatalf("sane pair changed = %d/%d", w, c)
	}
}

func TestPruneSilentRecords(t *testing.T) {
	t.Parallel()

	led := Ledger{
		"alpha:primary:1": {ResetsAt: alertReset.Unix(), FiredAt: alertNow.Add(-61 * 24 * time.Hour).Unix()},
		"beta:primary:1":  {ResetsAt: alertReset.Unix(), FiredAt: alertNow.Add(-59 * 24 * time.Hour).Unix()},
	}
	Prune(led, alertNow)
	if _, ok := led["alpha:primary:1"]; ok {
		t.Fatal("silent record survived the prune")
	}
	if _, ok := led["beta:primary:1"]; !ok {
		t.Fatal("recent record was pruned")
	}
}
