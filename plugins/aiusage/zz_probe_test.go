package aiusage

// TEMPORARY AUDIT PROBE — delete before commit.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func probeReport(now time.Time) Report {
	return Report{Providers: []ProviderReport{
		{ID: "claude", Name: "Claude", Plan: "Max", State: StateFault, UpdatedAt: now.Add(-3 * time.Hour),
			Err: "usage response was not a quota body (an error page is not usage)",
			Windows: []Window{{Key: "primary", Label: "Session", ShortLabel: "5h", HasPercent: true,
				UsedPercent: 72, WindowMinutes: 300, ResetsAt: now.Add(2 * time.Hour)}}},
		{ID: "codex", Name: "Codex", Plan: "Plus", State: StateFresh, Snapshot: true, UpdatedAt: now.Add(-3 * time.Hour),
			Windows: []Window{
				{Key: "primary", Label: "Session", ShortLabel: "5h", HasPercent: true,
					UsedPercent: 9, WindowMinutes: 300, ResetsAt: now.Add(45 * time.Minute)},
				{Key: "secondary", Label: "Weekly", ShortLabel: "Wk", HasPercent: true,
					UsedPercent: 78, WindowMinutes: 10080, ResetsAt: now.Add(36 * time.Hour)},
			}},
		{ID: "commandcode", Name: "Command Code", State: StateFresh, UpdatedAt: now,
			Windows: []Window{
				{Key: "primary", Label: "Session", ShortLabel: "5h", HasPercent: true,
					UsedPercent: 0, WindowMinutes: 300, DisplayValue: "$0 / $14"},
				{Key: "secondary", Label: "Weekly", ShortLabel: "Wk", HasPercent: true,
					UsedPercent: 99.98, WindowMinutes: 10080, DisplayValue: "$34.99 / $35"},
			}},
		{ID: "copilot", Name: "Copilot", Plan: "Free", State: StateFresh, UpdatedAt: now,
			Windows: []Window{{Key: "primary", Label: "Premium requests", ShortLabel: "Mo", HasPercent: true,
				UsedPercent: 100, WindowMinutes: 43200, ResetsAt: now.Add(96 * time.Hour), DisplayValue: "0% remaining"}}},
		{ID: "minimax", Name: "MiniMax", State: StateFresh, UpdatedAt: now,
			Windows: []Window{{Key: "primary", Label: "Session", ShortLabel: "5h", HasPercent: true,
				UsedPercent: 0, WindowMinutes: 300, ResetsAt: now.Add(1 * time.Hour)}}},
	}}
}

func probeConfig() Config {
	return Config{Warn: 85, Crit: 95, Refresh: 5 * time.Minute, HostMinor: 7,
		Track: map[string]bool{"claude": true, "codex": true, "commandcode": true, "copilot": true, "minimax": true}}
}

// stageLogos copies the shipped marks beside the test binary so the probe
// renders what a deployed plugin renders.
func stageLogos(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(filepath.Dir(exe), "..", "assets", "logos")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("assets/logos")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("assets/logos", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestZZProbeRenderViews(t *testing.T) {
	stageLogos(t)
	providerLogos = sync.OnceValue(scanProviderLogos)
	now := time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)
	r := probeReport(now)
	cfg := probeConfig()
	hist := []float64{10, 20, 40, 35, 50, 72}
	for _, sel := range []string{"claude", "codex", "copilot"} {
		panel := PanelTree(r, sel, hist, cfg, 7, now)
		if err := shelllint.RenderPNG(panel, v1.ViewPanel, 750, 430, "/tmp/panel_"+sel+".png"); err != nil {
			t.Fatal(err)
		}
	}
	bar := BarTree(r, DefaultInstance(), cfg, 7, now)
	if err := shelllint.RenderPNG(bar, v1.ViewBar, 240, 32, "/tmp/bar_render.png"); err != nil {
		t.Fatal(err)
	}
}

func TestZZProbeMeasure(t *testing.T) {
	for _, tc := range []struct {
		s      string
		size   int
		weight int
	}{
		{"AI Usage", 17, 600}, {"Command Code", 15, 400}, {"Command Code", 21, 600}, {"Claude", 21, 600},
		{"Export CSV", 15, 400}, {"Session", 15, 600}, {"Weekly", 15, 600}, {"Peak Copilot 100%", 15, 400},
		{"Quota windows · last local snapshot per provider · not billing figures", 12, 400},
		{"usage response was not a quota body (an error page is not usage)", 12, 400},
		{"Avg 70%", 15, 600}, {"Peak Copilot 100%", 15, 400}, {"2 at risk", 12, 400},
		{"No data", 12, 400}, {"Ready", 12, 400}, {"Setup", 12, 400}, {"Stale", 12, 400}, {"Error", 12, 400}, {"Wait", 12, 400},
		{"resets in 2h 15m", 15, 400}, {"Mon 15:04", 12, 400}, {"Waiting for fresh data", 12, 400},
		{"↑12 pace — 12 points ahead of the clock", 12, 400}, {"$34.99 / $35", 12, 400},
	} {
		w, _, err := shelllint.MeasureText(tc.s, tc.size, tc.weight)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%-70q size=%d weight=%d real=%d contract=%d", tc.s, tc.size, tc.weight, w, len(tc.s)*8)
	}
}
