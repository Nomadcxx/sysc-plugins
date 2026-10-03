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

func TestManifestKeepsSettingsOffTheUsagePanel(t *testing.T) {
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Panels []struct {
			ID              string `json:"id"`
			IncludeSettings bool   `json:"include_settings"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]bool, len(manifest.Panels))
	for _, panel := range manifest.Panels {
		entries[panel.ID] = panel.IncludeSettings
	}
	main, ok := entries["panel"]
	if !ok || main {
		t.Fatal("usage panel still includes the settings form")
	}
	if !entries["settings"] {
		t.Fatal("dedicated settings panel is missing its settings form")
	}
}

// TestViewsFitTheirHostSlots lays every view the plugin can build out with the
// host's own rules, at the sizes the host uses. v1.Validate is geometry-blind
// and the host's layout stops at the first rejection, so this matrix — state
// against negotiated host minor — is the panel's only whole check before a
// user sees it. The gap it closes reached a user once: a padded row declared
// Height 28 for controls that measure 16, and the panel was refused.
func TestViewsFitTheirHostSlots(t *testing.T) {
	panelW, panelH := panelSize(t)
	// The usage panel now carries the same combined inset in its own root.
	contentW, contentH := panelW, panelH
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
		"empty": {},
	}
	hist := []float64{10, 20, 40}
	for name, r := range states {
		for _, minor := range []int{7, 6, 5, 4, 3, 2} {
			cfg := viewConfig()
			inst := DefaultInstance()
			bar := BarTree(r, inst, cfg, minor, viewNow)
			button := findByID(bar, "open")
			for _, f := range shelllint.Tree(bar, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
				t.Errorf("%s minor %d bar: %s", name, minor, f)
			}
			for _, width := range []int{32, 64} {
				sideBar := BarTreeAtWidth(r, inst, cfg, minor, viewNow, width)
				if len(sideBar.Children) != 1 || sideBar.Children[0].Kind != v1.KindButton {
					t.Errorf("%s minor %d width %d: side bar has %d root children, want the launcher only", name, minor, width, len(sideBar.Children))
				}
				sideButton := findByID(sideBar, "open")
				if button == nil || sideButton == nil || button.Name != sideButton.Name || button.Role != sideButton.Role || len(button.Events) != 1 || len(sideButton.Events) != 1 || button.Events[0] != sideButton.Events[0] || button.Tooltip != sideButton.Tooltip {
					t.Errorf("%s minor %d width %d changed the bar action or tooltip", name, minor, width)
				}
				for _, f := range shelllint.Tree(sideBar, v1.ViewBar, width, shelllint.BarHeight) {
					t.Errorf("%s minor %d bar width %d: %s", name, minor, width, f)
				}
			}
			panel := PanelTree(r, "alpha", hist, cfg, minor, viewNow)
			for _, f := range shelllint.Tree(panel, v1.ViewPanel, contentW, contentH) {
				t.Errorf("%s minor %d panel: %s", name, minor, f)
			}
			settings := SettingsPanelTree()
			for _, f := range shelllint.Tree(settings, v1.ViewPanel,
				contentW-2*panelContentInset, contentH-2*panelContentInset) {
				t.Errorf("%s minor %d settings panel: %s", name, minor, f)
			}
		}
	}
}
