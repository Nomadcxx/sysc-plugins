package updates

import (
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func checkLint(t *testing.T, name string, root *v1.Node, view v1.ViewKind, width, height int) {
	t.Helper()
	for _, finding := range shelllint.Tree(root, view, width, height) {
		t.Errorf("%s does not lay out: %s", name, finding)
	}
}

func richState() State {
	return State{
		Updates: []Update{
			{Name: "linux", Old: "6.17.1.arch1-1", New: "6.17.2.arch1-1", Source: SourceRepo, Core: true},
			{Name: "mesa", Old: "1:25.2.4-1", New: "1:25.2.5-1", Source: SourceRepo, Core: true},
			{Name: "firefox", Old: "143.0.1-1", New: "143.0.2-1", Source: SourceRepo},
			{Name: "systemd", Old: "258-1", New: "258.1-1", Source: SourceRepo, Core: true},
			{Name: "a-very-long-aur-package-name-git", Old: "r128.abc1234-1", New: "r129.def5678-1", Source: SourceAUR},
			{Name: "visual-studio-code-bin", Old: "1.104.0-1", New: "1.105.0-1", Source: SourceAUR},
			{Name: "yay", Old: "12.4.2-1", New: "12.5.0-1", Source: SourceAUR},
			{Name: "nvidia-utils", Old: "580.95-1", New: "580.101-1", Source: SourceRepo, Core: true},
			{Name: "org.mozilla.firefox", Old: "143.0.1", New: "143.0.2", Source: SourceFlatpak},
			{Name: "com.spotify.Client", Old: "1.2.62", New: "1.2.63", Source: SourceFlatpak},
			{Name: "org.gnome.Calculator", Old: "49.0", New: "49.1", Source: SourceFlatpak},
			{Name: "org.keepassxc.KeePassXC", Old: "2.7.9", New: "2.7.10", Source: SourceFlatpak},
		},
		CheckedAt:    time.Date(2026, 10, 8, 14, 5, 0, 0, time.UTC),
		RebootNeeded: true,
		RebootDetail: "linux 6.17.2.arch1-1 is installed, 6.16.9-arch1-1 is running",
	}
}

func TestViewsLayOutInEveryState(t *testing.T) {
	now := time.Date(2026, 10, 8, 14, 5, 0, 0, time.UTC)
	states := map[string]State{
		"empty":              {},
		"loading":            {Checking: true},
		"rich":               richState(),
		"error with stale":   {Updates: richState().Updates, CheckErr: "Cannot fetch updates"},
		"no checkupdates":    {RepoMissing: true},
		"missing aur helper": {AURMissing: true},
		"flatpak missing":    {FlatpakMissing: true},
		"terminal hint":      {Updates: richState().Updates, Checking: false},
	}
	for name, state := range states {
		t.Run(name, func(t *testing.T) {
			for _, width := range []int{shelllint.BarWidth, 32, 64} {
				checkLint(t, name+" bar", BarTree(state, true), v1.ViewBar, width, shelllint.BarHeight)
			}
			checkLint(t, name+" tooltip", TooltipTree(state, now), v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight)
			checkLint(t, name+" panel", PanelTree(state, now, ""), v1.ViewPanel, 420, 600)
			checkLint(t, name+" panel with hint", PanelTree(state, now, "No terminal found. Set one in the plugin's settings."), v1.ViewPanel, 420, 600)
		})
	}
}

func TestBarHidesAtZeroWhenAsked(t *testing.T) {
	hidden := BarTree(State{}, true)
	if len(hidden.Children) != 0 {
		t.Fatalf("bar should be empty at zero updates, got %+v", hidden.Children)
	}
	shown := BarTree(State{}, false)
	if len(shown.Children) == 0 {
		t.Fatal("bar should still show the launcher when hide_when_zero is off")
	}
	reboot := BarTree(State{RebootNeeded: true, RebootDetail: "linux 6.17 is installed, 6.16 is running"}, true)
	if len(reboot.Children) == 0 {
		t.Fatal("reboot marker must show even with no pending updates")
	}
}

func TestBarReflectsMissingCheckupdates(t *testing.T) {
	bar := BarTree(State{RepoMissing: true}, true)
	if len(bar.Children) == 0 {
		t.Fatal("bar should still be visible when checkupdates is missing")
	}
}

func TestPanelListsRunRefreshAndNewsNodes(t *testing.T) {
	panel := PanelTree(richState(), time.Now(), "")
	var ids []string
	var walk func(n *v1.Node)
	walk = func(n *v1.Node) {
		if n.ID != "" {
			ids = append(ids, n.ID)
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(panel)
	for _, want := range []string{"updates-run", "updates-refresh", "updates-news", "updates-row-repo-linux", "updates-row-aur-yay", "updates-row-flatpak-org.mozilla.firefox"} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Errorf("panel is missing node %q (have %v)", want, ids)
		}
	}
}
