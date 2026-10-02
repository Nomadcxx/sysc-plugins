package moonbit

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// panelSize reads the box the host opens this plugin's panel with; the
// manifest is the only declaration of it.
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

// TestViewsFitTheirHostSlots lays every phase's views out with the host's
// own rules, at the sizes the manifest and bar declare. The budget consts in
// view.go are only checked here.
func TestViewsFitTheirHostSlots(t *testing.T) {
	panelW, panelH := panelSize(t)
	for _, phase := range []Phase{PhaseIdle, PhaseScanning, PhaseReview, PhaseConfirm, PhaseCleaning, PhaseDone, PhaseError,
		PhaseAuth, PhaseDocker, PhaseDockerConfirm, PhaseSchedule, PhaseWorking} {
		s := states()[phase]
		for _, f := range lint.Tree(Bar(s), v1.ViewBar, lint.BarWidth, lint.BarHeight) {
			t.Errorf("bar %v: %s", phase, f)
		}
		for _, f := range lint.Tree(Tooltip(s), v1.ViewTooltip, lint.TooltipWidth, lint.TooltipHeight) {
			t.Errorf("tooltip %v: %s", phase, f)
		}
		for _, f := range lint.Tree(Panel(s), v1.ViewPanel, panelW, panelH) {
			t.Errorf("panel %v: %s", phase, f)
		}
	}
}
