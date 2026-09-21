//go:build live

// The live gate runs the real service against the real docker daemon on
// this machine: read-only on purpose (no start/stop of live containers).
// Run: go test -tags live ./plugins/mini-docker/
package minidocker

import (
	"context"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestLiveListAndTrees(t *testing.T) {
	s := NewSession(CLI{})
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	s.Refresh(ctx)

	containers, available, loading, listErr, actErr, actingID := s.Snapshot()
	if !available {
		t.Fatalf("docker unavailable on live machine: listErr=%q", listErr)
	}
	if loading || listErr != "" || actErr != "" || actingID != "" {
		t.Fatalf("unexpected session state: loading=%v listErr=%q actErr=%q actingID=%q",
			loading, listErr, actErr, actingID)
	}
	if len(containers) == 0 {
		t.Skip("no containers on this machine; nothing to render")
	}

	panel := PanelTree(available, loading, listErr, actErr, actingID, containers)
	if err := v1.Validate(panel, v1.ViewPanel); err != nil {
		t.Fatalf("live panel tree rejected: %v", err)
	}

	running := s.RunningCount()
	if running < 1 {
		t.Fatalf("RunningCount = %d, live docker ps shows running containers", running)
	}
	bar := BarTree(BarLabel("always", running, available), !available)
	if err := v1.Validate(bar, v1.ViewBar); err != nil {
		t.Fatalf("live bar tree rejected: %v", err)
	}
	if err := v1.Validate(TooltipTree(TooltipText(running, available)), v1.ViewTooltip); err != nil {
		t.Fatalf("live tooltip tree rejected: %v", err)
	}
	t.Logf("live: %d containers, %d running", len(containers), running)
}
