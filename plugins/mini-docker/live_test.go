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

func TestLiveListsAndTrees(t *testing.T) {
	s := NewSession(CLI{})
	ctx, cancel := context.WithTimeout(context.Background(), 4*listTimeout)
	defer cancel()
	s.Refresh(ctx)

	scopes := []Scope{ScopeContainers, ScopeImages, ScopeVolumes, ScopeNetworks}
	counts := make(map[Scope]int, len(scopes))
	for _, scope := range scopes {
		if scope != ScopeContainers {
			s.SetScope(scope)
			s.RefreshTab(ctx, scope)
		}
		state := s.State()
		status := tabStatus(state)
		if !status.Available || status.Loading || status.ListError != "" {
			t.Fatalf("live %s list failed: available=%v loading=%v error=%q",
				scope, status.Available, status.Loading, status.ListError)
		}
		counts[scope] = countTab(state, scope)
		panel := PanelTreeForSession(state)
		if err := v1.Validate(panel, v1.ViewPanel); err != nil {
			t.Fatalf("live %s panel tree rejected: %v", scope, err)
		}
	}
	running := s.RunningCount()
	bar := BarTree(BarLabel("always", running, true), false)
	if err := v1.Validate(bar, v1.ViewBar); err != nil {
		t.Fatalf("live bar tree rejected: %v", err)
	}
	if err := v1.Validate(TooltipTreeForSession(s.State(), running), v1.ViewTooltip); err != nil {
		t.Fatalf("live tooltip tree rejected: %v", err)
	}
	occupied, err := hostPortInUse(1)
	if err != nil {
		t.Fatalf("live /proc TCP preflight failed: %v", err)
	}
	t.Logf("live: containers=%d running=%d images=%d volumes=%d networks=%d tcp-port-1-occupied=%v",
		counts[ScopeContainers], running, counts[ScopeImages], counts[ScopeVolumes], counts[ScopeNetworks], occupied)
}

func countTab(state SessionSnapshot, scope Scope) int {
	switch scope {
	case ScopeContainers:
		return len(state.Containers)
	case ScopeImages:
		return len(state.Images)
	case ScopeVolumes:
		return len(state.Volumes)
	case ScopeNetworks:
		return len(state.Networks)
	default:
		return 0
	}
}
