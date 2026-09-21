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

var alertNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
var alertReset = alertNow.Add(2 * time.Hour)

func TestAlertsFireOncePerLevelPerWindow(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	notices := CheckAlerts(alertReport(86, alertReset), 85, 95, led, alertNow)
	if len(notices) != 1 || notices[0].Critical {
		t.Fatalf("warn notices = %+v", notices)
	}
	if notices[0].Summary != "Alpha · Session 86%" {
		t.Fatalf("summary = %q", notices[0].Summary)
	}

	// Same data again: no re-fire.
	if again := CheckAlerts(alertReport(86, alertReset), 85, 95, led, alertNow); len(again) != 0 {
		t.Fatalf("re-fired: %+v", again)
	}

	// Escalation to 99 fires the two higher levels, one notice each.
	notices = CheckAlerts(alertReport(99, alertReset), 85, 95, led, alertNow)
	if len(notices) != 2 {
		t.Fatalf("escalation = %+v, want critical + depleted", notices)
	}
	for _, n := range notices {
		if !n.Critical {
			t.Fatalf("escalated notice not critical: %+v", n)
		}
	}
	// And again, nothing.
	if again := CheckAlerts(alertReport(99, alertReset), 85, 95, led, alertNow); len(again) != 0 {
		t.Fatalf("depleted re-fired: %+v", again)
	}
}

func TestAlertsHysteresisReArms(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	CheckAlerts(alertReport(86, alertReset), 85, 95, led, alertNow)

	// Boundary hovering between 80 (threshold−5) and 85 never re-fires…
	CheckAlerts(alertReport(82, alertReset), 85, 95, led, alertNow)
	if again := CheckAlerts(alertReport(86, alertReset), 85, 95, led, alertNow); len(again) != 0 {
		t.Fatalf("hover re-fired: %+v", again)
	}

	// …but a meaningful fall clears the record, and the next rise fires.
	CheckAlerts(alertReport(70, alertReset), 85, 95, led, alertNow)
	if again := CheckAlerts(alertReport(86, alertReset), 85, 95, led, alertNow); len(again) != 1 {
		t.Fatalf("re-armed rise did not fire: %+v", again)
	}
}

func TestAlertsRolloverReFires(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	CheckAlerts(alertReport(90, alertReset), 85, 95, led, alertNow)
	// A new window (different reset) re-arms the level.
	if again := CheckAlerts(alertReport(90, alertReset.Add(5*time.Hour)), 85, 95, led, alertNow); len(again) != 1 {
		t.Fatalf("rollover did not re-fire: %+v", again)
	}
}

func TestAlertsUnknownResetSentinel(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	// A window with no known reset fires once and stores the sentinel.
	if n := CheckAlerts(alertReport(90, time.Time{}), 85, 95, led, alertNow); len(n) != 1 {
		t.Fatalf("unknown-reset fire = %+v", n)
	}
	if rec := led["alpha:primary:1"]; rec.ResetsAt != 0 {
		t.Fatalf("sentinel = %+v, want ResetsAt 0", rec)
	}
	// It stays quiet while the reset stays unknown.
	if again := CheckAlerts(alertReport(90, time.Time{}), 85, 95, led, alertNow); len(again) != 0 {
		t.Fatalf("unknown-reset re-fired: %+v", again)
	}
	// A real reset appearing later is a new window: it fires once.
	if again := CheckAlerts(alertReport(90, alertReset), 85, 95, led, alertNow); len(again) != 1 {
		t.Fatalf("unknown→known did not fire: %+v", again)
	}
	if again := CheckAlerts(alertReport(90, alertReset), 85, 95, led, alertNow); len(again) != 0 {
		t.Fatalf("known reset re-fired: %+v", again)
	}
	// A known record tolerates the reset instant vanishing (schema drift).
	if again := CheckAlerts(alertReport(90, time.Time{}), 85, 95, led, alertNow); len(again) != 0 {
		t.Fatalf("drift re-fired: %+v", again)
	}
}

func TestAlertsSkipUnreadyProviders(t *testing.T) {
	t.Parallel()

	led := Ledger{}
	base := alertReport(100, alertReset)
	base.Providers[0].State = StateFault
	if n := CheckAlerts(base, 85, 95, led, alertNow); len(n) != 0 {
		t.Fatalf("fault fired: %+v", n)
	}
	base.Providers[0].State = StateNeedsSetup
	if n := CheckAlerts(base, 85, 95, led, alertNow); len(n) != 0 {
		t.Fatalf("setup fired: %+v", n)
	}
	base.Providers[0].State = StateNoData
	if n := CheckAlerts(base, 85, 95, led, alertNow); len(n) != 0 {
		t.Fatalf("no-data fired: %+v", n)
	}
	base.Providers[0].State = StateFresh
	base.Providers[0].Stale = true
	if n := CheckAlerts(base, 85, 95, led, alertNow); len(n) != 0 {
		t.Fatalf("stale fired: %+v", n)
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
