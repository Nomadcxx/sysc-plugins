package aiusage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/aiusage/screenshot.png when CAPTURE=1: three
// providers with invented usage, the first selected, and a day of history.
func TestCapturePanel(t *testing.T) {
	logos, err := filepath.Abs("assets/logos")
	if err != nil {
		t.Fatal(err)
	}
	prev := providerLogos
	providerLogos = func() map[string]string {
		out := map[string]string{}
		for _, id := range []string{"claude", "codex", "copilot"} {
			out[id] = filepath.Join(logos, id+".png")
		}
		return out
	}
	t.Cleanup(func() { providerLogos = prev })

	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	window := func(key, label, short string, used float64, minutes int, resets time.Duration) Window {
		return Window{Key: key, Label: label, ShortLabel: short, HasPercent: true, UsedPercent: used,
			WindowMinutes: minutes, ResetsAt: now.Add(resets)}
	}
	report := Report{CapturedAt: now, Providers: []ProviderReport{
		{ID: "claude", Name: "Claude", Plan: "Max", State: StateFresh, UpdatedAt: now, Windows: []Window{
			window("primary", "Session", "5h", 62, 300, 110*time.Minute),
			window("secondary", "Weekly", "Wk", 38, 10080, 71*time.Hour),
		}},
		{ID: "codex", Name: "Codex", Plan: "Plus", State: StateFresh, UpdatedAt: now, Windows: []Window{
			window("primary", "Session", "5h", 21, 300, 200*time.Minute),
			window("secondary", "Weekly", "Wk", 54, 10080, 40*time.Hour),
		}},
		{ID: "copilot", Name: "Copilot", Plan: "Pro", State: StateFresh, UpdatedAt: now, Windows: []Window{
			window("primary", "Monthly", "Mo", 87, 43200, 9*24*time.Hour),
		}},
	}}
	cfg := Config{Warn: 85, Crit: 95, Refresh: time.Minute, HostMinor: 4,
		Track: map[string]bool{"claude": true, "codex": true, "copilot": true}}
	var hist []float64
	for i := 0; i < 48; i++ {
		hist = append(hist, 20+float64((i*7)%40)+float64(i)/2)
	}
	capture.Panel(t, "aiusage", PanelTree(report, "claude", hist, cfg, 4, now))
}
