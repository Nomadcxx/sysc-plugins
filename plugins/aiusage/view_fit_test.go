package aiusage

import (
	"encoding/json"
	"os"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// panelSize reads the box the host opens this plugin's panel with. The
// manifest is the only declaration of it, so a test that hardcoded 750x430
// would drift the day the manifest moves.
func panelSize(t *testing.T) (int, int) {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Panels []struct {
			ID            string `json:"id"`
			Width, Height int
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Panels) == 0 {
		t.Fatal("manifest declares no panels")
	}
	return m.Panels[0].Width, m.Panels[0].Height
}

// TestViewsFitTheirHostSlots lays every view the plugin can build out with the
// host's own rules, at the sizes the host uses. v1.Validate is geometry-blind
// and the host's layout stops at the first rejection, so this matrix — state
// against negotiated host minor — is the panel's only whole check before a
// user sees it. The gap it closes reached a user once: a padded row declared
// Height 28 for controls that measure 16, and the panel was refused.
func TestViewsFitTheirHostSlots(t *testing.T) {
	panelW, panelH := panelSize(t)
	states := map[string]Report{
		"fresh": viewReport(),
		"setup": {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha",
			State: StateNeedsSetup, Err: "setup required: /none"}}},
		"fault": {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha",
			State: StateFault, Stale: true, UpdatedAt: viewNow, Err: "boom",
			Windows: viewReport().Providers[0].Windows}}},
		"nodata": {Providers: []ProviderReport{{ID: "alpha", Name: "Alpha", State: StateNoData}}},
		"opencode-no-subscription": {Providers: []ProviderReport{{ID: "opencode-go", Name: "OpenCode Go",
			State: StateNoData, Err: "OpenCode Go subscription required (HTTP 403)"}}},
		"empty":  {},
	}
	hist := []float64{10, 20, 40}
	for name, r := range states {
		for _, minor := range []int{7, 6, 5, 4, 3, 2} {
			cfg := viewConfig()
			bar := BarTree(r, DefaultInstance(), cfg, minor, viewNow)
			for _, f := range shelllint.Tree(bar, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
				t.Errorf("%s minor %d bar: %s", name, minor, f)
			}
			panel := PanelTree(r, "alpha", hist, cfg, minor, viewNow)
			for _, f := range shelllint.Tree(panel, v1.ViewPanel, panelW, panelH) {
				t.Errorf("%s minor %d panel: %s", name, minor, f)
			}
		}
	}
}
