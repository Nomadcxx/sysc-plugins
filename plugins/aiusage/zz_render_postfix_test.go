package aiusage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestZZRenderPostFix(t *testing.T) {
	logoDir, err := filepath.Abs("assets/logos")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(logoDir)
	if err != nil {
		t.Fatal(err)
	}
	providerLogos = func() map[string]string {
		out := map[string]string{}
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".png" {
				out[strings.TrimSuffix(entry.Name(), ".png")] = filepath.Join(logoDir, entry.Name())
			}
		}
		return out
	}

	now := time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)
	report := Report{CapturedAt: now, Providers: []ProviderReport{
		{ID: "claude", Name: "Claude", State: StateFault, Stale: true, UpdatedAt: now.Add(-3 * time.Hour),
			Err: "usage response was not a quota body (an error page is not usage)",
			Windows: []Window{{Key: "primary", Label: "Session", HasPercent: true, UsedPercent: 72, WindowMinutes: 300, ResetsAt: now.Add(2 * time.Hour)}}},
		{ID: "codex", Name: "Codex", Plan: "Plus", State: StateFresh, Snapshot: true, UpdatedAt: now.Add(-3 * time.Hour),
			Windows: []Window{
				{Key: "primary", Label: "Session", HasPercent: true, UsedPercent: 9, WindowMinutes: 300, ResetsAt: now.Add(45 * time.Minute)},
				{Key: "secondary", Label: "Weekly", HasPercent: true, UsedPercent: 78, WindowMinutes: 10080, ResetsAt: now.Add(36 * time.Hour)},
			}},
		{ID: "commandcode", Name: "Command Code", State: StateFresh, UpdatedAt: now,
			Windows: []Window{{Key: "primary", Label: "Session", HasPercent: true, UsedPercent: 99, WindowMinutes: 300, ResetsAt: now.Add(2 * time.Hour)}}},
		{ID: "copilot", Name: "Copilot", Plan: "Free", State: StateFresh, UpdatedAt: now,
			Windows: []Window{{Key: "primary", Label: "Premium requests", HasPercent: true, UsedPercent: 100, WindowMinutes: 43200, ResetsAt: now.Add(96 * time.Hour)}}},
	}}
	cfg := Config{Warn: 85, Crit: 95, Refresh: 5 * time.Minute, HostMinor: 7,
		Track: map[string]bool{"claude": true, "codex": true, "commandcode": true, "copilot": true}}
	hist := []float64{10, 20, 40, 35, 50, 72}
	for _, selected := range []string{"codex", "copilot"} {
		panel := PanelTree(report, selected, hist, cfg, 7, now)
		if err := shelllint.RenderPNG(panel, v1.ViewPanel, 750, 430, "/tmp/aiusage-panel-after-"+selected+".png"); err != nil {
			t.Fatal(err)
		}
	}
	bar := BarTree(report, DefaultInstance(), cfg, 7, now)
	if err := shelllint.RenderPNG(bar, v1.ViewBar, 240, 32, "/tmp/aiusage-bar-after.png"); err != nil {
		t.Fatal(err)
	}
}
