package updates

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestPanelBoundsLargeUpdateLists(t *testing.T) {
	const visiblePerSource = 100
	sources := []Source{SourceRepo, SourceAUR, SourceFlatpak}
	cases := []struct {
		name   string
		counts [3]int
	}{
		{name: "one source exceeds child limit", counts: [3]int{400, 0, 0}},
		{name: "all sources exceed node limit", counts: [3]int{250, 250, 250}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var updates []Update
			for i, source := range sources {
				for n := 0; n < tc.counts[i]; n++ {
					updates = append(updates, Update{
						Name: fmt.Sprintf("%s-%04d", source, n), Source: source, Core: true,
					})
				}
			}
			state := State{
				Updates: updates, Checking: true, CheckErr: "database is locked",
				RepoMissing: true, AURMissing: true, FlatpakMissing: true,
				RebootNeeded: true, RebootDetail: "restart required",
				CheckedAt: time.Now(),
			}
			root := PanelTree(state, time.Now(), "terminal unavailable")
			if err := v1.Validate(root, v1.ViewPanel); err != nil {
				t.Fatalf("large update list produced an invalid panel: %v", err)
			}

			groups := make(map[Source]*v1.Node)
			var walk func(*v1.Node)
			walk = func(node *v1.Node) {
				for _, source := range sources {
					if node.ID == "updates-group-"+string(source) {
						groups[source] = node
					}
				}
				for _, child := range node.Children {
					walk(child)
				}
			}
			walk(root)

			for i, source := range sources {
				total := tc.counts[i]
				if total == 0 {
					continue
				}
				group := groups[source]
				if group == nil {
					t.Fatalf("missing %s update group", source)
				}
				if len(group.Children) > v1.MaxChildren {
					t.Fatalf("%s group has %d children, limit is %d", source, len(group.Children), v1.MaxChildren)
				}
				visible := total
				if visible > visiblePerSource {
					visible = visiblePerSource
					if got, want := group.Children[len(group.Children)-1].Text, fmt.Sprintf("%d more updates", total-visible); got != want {
						t.Errorf("%s overflow label = %q, want %q", source, got, want)
					}
				}
				childRows := len(group.Children) - 1
				if total > visiblePerSource {
					childRows-- // trailing summary row
				}
				if childRows != visible {
					t.Errorf("%s visible rows = %d, want %d", source, childRows, visible)
				}
			}
		})
	}
}

func TestUpdateRowBoundsLongPackageIDs(t *testing.T) {
	short := updateRow(Update{Name: "linux", Source: SourceRepo})
	if short.ID != "updates-row-repo-linux" {
		t.Fatalf("short package ID = %q, want stable readable ID", short.ID)
	}

	firstName := "pkg-" + strings.Repeat("x", v1.MaxIdentBytes) + "-end"
	secondName := "pkg-" + strings.Repeat("y", v1.MaxIdentBytes) + "-end"
	first := updateRow(Update{Name: firstName, Source: SourceRepo})
	second := updateRow(Update{Name: secondName, Source: SourceRepo})
	for _, row := range []*v1.Node{first, second} {
		if len(row.ID) > v1.MaxIdentBytes {
			t.Errorf("row ID is %d bytes, limit is %d", len(row.ID), v1.MaxIdentBytes)
		}
		if !utf8.ValidString(row.ID) {
			t.Errorf("row ID is not valid UTF-8: %q", row.ID)
		}
	}
	if first.ID == second.ID {
		t.Fatalf("different long package names collided at %q", first.ID)
	}
}

func TestUpdateRowShowsTheBareNameWithoutAVersion(t *testing.T) {
	row := updateRow(Update{Name: "org.gnome.Platform", Source: SourceFlatpak})
	if len(row.Children) == 0 {
		t.Fatal("row has no text child")
	}
	if got := row.Children[0].Text; got != "org.gnome.Platform" {
		t.Fatalf("row text = %q, want the bare name with no dangling arrow", got)
	}
}
