package herdr

import (
	"fmt"
	"strings"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// --- shared model builders ---

func modelWith(sessions ...SessionRow) *Model { return &Model{Sessions: sessions} }

func runningSess(name string, counts Counts, wss ...WorkspaceRow) SessionRow {
	return SessionRow{Name: name, Running: true, Workspaces: wss, Counts: counts}
}

func stoppedSess(name string, projects ...string) SessionRow {
	return SessionRow{Name: name, Running: false, StoppedWorkspaces: projects}
}

func ws(id, label string, st Status, panes ...PaneRow) WorkspaceRow {
	return WorkspaceRow{ID: id, Label: label, Panes: panes, Status: st}
}

func onePaneWS(label string, p PaneRow) WorkspaceRow {
	return ws(label, label, p.Status, p)
}

func pane(id, title string, st Status) PaneRow {
	return PaneRow{PaneID: id, Title: title, TabLabel: "tab 1", Status: st,
		StatusLabel: StatusLabel(nil, st), Readable: st != StatusNone, Session: title}
}

// --- panel states ---

func panelStates() map[string]PanelState {
	def := DefaultSettings()
	return map[string]PanelState{
		"missing": {Model: &Model{HerdrMissing: true}, Settings: def},
		"empty":   {Model: &Model{}, Settings: def},
		"single": {Model: modelWith(runningSess("alpha",
			Counts{Sessions: 1, Agents: 1, Blocked: 1},
			onePaneWS("work", pane("p1", "Fix login", StatusBlocked)))), Settings: def},
		"discovery": {Model: &Model{DiscoveryErr: "dial unix: refused",
			Sessions: []SessionRow{runningSess("alpha", Counts{Sessions: 1, Agents: 1},
				onePaneWS("work", pane("p1", "shell", StatusIdle)))}}, Settings: def},
		"multi": {Model: modelWith(
			runningSess("beta", Counts{Sessions: 1, Agents: 3, Blocked: 1, Working: 2},
				ws("w1", "repo-alpha", StatusBlocked,
					pane("p1", "Fix login", StatusBlocked),
					pane("p2", "Run tests", StatusWorking)),
				onePaneWS("notes", pane("p3", "scratch", StatusWorking))),
			runningSess("alpha", Counts{Sessions: 1, Agents: 1, Done: 1},
				onePaneWS("main", pane("p9", "ship it", StatusDone))),
		), Settings: def},
		"stopped": {Model: modelWith(
			stoppedSess("old", "proj-a", "proj-b"),
			stoppedSess("older"),
		), Settings: def},
		"stoppedhidden": {Model: modelWith(stoppedSess("old", "proj-a")),
			Settings: Settings{ShowStoppedSessions: false, OutputPreviewLines: 20}},
		"peek": {Model: modelWith(runningSess("alpha", Counts{Sessions: 1, Agents: 1},
			onePaneWS("work", pane("p1", "shell", StatusIdle)))),
			Settings: def,
			Peeks:    map[string]Peek{"p1": {PaneID: "p1", Text: "line one\nline two", Lines: 20}}},
		"confirm": {Model: modelWith(stoppedSess("old")),
			Settings:      def,
			ConfirmDelete: "old"},
		"actionerror": {Model: modelWith(runningSess("alpha", Counts{Sessions: 1, Agents: 1},
			onePaneWS("work", pane("p1", "shell", StatusIdle)))),
			Settings:    def,
			ActionError: "herdr session stop failed: exit status 1"},
		"settingsentry": {Model: &Model{}, Settings: def, SettingsEntry: true, NewName: "fresh"},
	}
}

func TestPanelLintsInEveryState(t *testing.T) {
	t.Parallel()
	for _, st := range panelStates() {
		for _, width := range []int{PanelWidth, 360, 300} {
			tree := PanelTree(st, width, PanelHeight)
			checkLint(t, v1.ViewPanel, tree, width, PanelHeight)
		}
	}
	if tree := SettingsPanelTree("Herdr Settings"); tree == nil {
		t.Fatal("SettingsPanelTree returned nil")
	} else {
		checkLint(t, v1.ViewPanel, tree, PanelWidth, PanelHeight)
	}
}

func TestPanelHeaderControls(t *testing.T) {
	t.Parallel()
	tree := PanelTree(panelStates()["single"], PanelWidth, PanelHeight)
	ids := map[string]*v1.Node{}
	walk(tree, func(n *v1.Node) {
		if n.ID != "" {
			ids[n.ID] = n
		}
	})
	for _, id := range []string{"refresh", "close"} {
		if ids[id] == nil {
			t.Fatalf("header is missing the %q button", id)
		}
	}
	if ids["settings"] != nil {
		t.Fatal("settings button present without SettingsEntry")
	}
	tree = PanelTree(panelStates()["settingsentry"], PanelWidth, PanelHeight)
	ids = map[string]*v1.Node{}
	walk(tree, func(n *v1.Node) { ids[n.ID] = n })
	if ids["settings"] == nil {
		t.Fatal("SettingsEntry did not add a settings button")
	}
	// The new-session row is always present.
	if ids["newname"] == nil || ids["newstart"] == nil {
		t.Fatal("new session row missing")
	}
	if in := ids["newname"]; in.Kind != v1.KindTextInput || !in.SubmitOnEnter ||
		in.Placeholder != "session name" {
		t.Fatalf("new name input = %+v", in)
	}
}

func TestPanelPeekAndActions(t *testing.T) {
	t.Parallel()
	tree := PanelTree(panelStates()["peek"], PanelWidth, PanelHeight)
	texts := map[string]bool{}
	walk(tree, func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Size == "mono" {
			texts[n.Text] = true
		}
	})
	if !texts["line one"] || !texts["line two"] {
		t.Fatalf("peek text = %v, want the two wrapped lines", texts)
	}

	// Delete confirmation renders both steps; the plain delete renders alone.
	confirm := PanelTree(panelStates()["confirm"], PanelWidth, PanelHeight)
	ids := map[string]*v1.Node{}
	walk(confirm, func(n *v1.Node) { ids[n.ID] = n })
	if ids["confirmdelete"] == nil || ids["canceldelete"] == nil {
		t.Fatal("confirm state missing confirm/cancel buttons")
	}
	plain := PanelTree(panelStates()["stopped"], PanelWidth, PanelHeight)
	ids = map[string]*v1.Node{}
	walk(plain, func(n *v1.Node) { ids[n.ID] = n })
	if ids["delete:old"] == nil || ids["attach:old"] == nil {
		t.Fatal("stopped session missing attach/delete")
	}
	if ids["stop:old"] != nil {
		t.Fatal("stopped session must not offer stop")
	}

	// Action error lands in an error-container card at the bottom (spec).
	errTree := PanelTree(panelStates()["actionerror"], PanelWidth, PanelHeight)
	found := false
	walk(errTree, func(n *v1.Node) {
		if n.Fill == "error-container" {
			for _, c := range n.Children {
				if c.Kind == v1.KindText && c.Tone == v1.ToneError {
					found = true
				}
			}
		}
	})
	if !found {
		t.Fatal("action error card not rendered")
	}
}

func TestPanelTimeInStateCaption(t *testing.T) {
	t.Parallel()
	p := pane("p1", "Fix login", StatusBlocked)
	p.SinceLabel = "2m"
	st := PanelState{
		Model:    modelWith(runningSess("alpha", Counts{Sessions: 1, Agents: 1, Blocked: 1}, onePaneWS("work", p))),
		Settings: DefaultSettings(),
	}
	tree := PanelTree(st, PanelWidth, PanelHeight)
	checkLint(t, v1.ViewPanel, tree, PanelWidth, PanelHeight)
	found := false
	walk(tree, func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text == "2m" {
			found = true
		}
	})
	if !found {
		t.Fatal("time-in-state caption not rendered")
	}
	p.SinceLabel = ""
	st.Model = modelWith(runningSess("alpha", Counts{Sessions: 1, Agents: 1, Blocked: 1}, onePaneWS("work", p)))
	tree = PanelTree(st, PanelWidth, PanelHeight)
	walk(tree, func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text == "2m" {
			t.Fatal("empty SinceLabel must not render caption")
		}
	})
}

func TestPanelStoppedWorkspacesHonourSetting(t *testing.T) {
	t.Parallel()
	shown := PanelTree(panelStates()["stopped"], PanelWidth, PanelHeight)
	hidden := PanelTree(panelStates()["stoppedhidden"], PanelWidth, PanelHeight)
	if countText(shown, "proj-a") == 0 {
		t.Fatal("stopped workspaces not shown when enabled")
	}
	if countText(hidden, "proj-a") != 0 {
		t.Fatal("stopped workspaces shown when disabled")
	}
}

func stressModel() *Model {
	var sessions []SessionRow
	for i := 0; i < 30; i++ {
		panes := make([]PaneRow, 0, 30)
		for j := 0; j < 30; j++ {
			panes = append(panes, pane(
				fmt.Sprintf("p%d-%d", i, j),
				fmt.Sprintf("pane %d", j), StatusWorking))
		}
		sessions = append(sessions, runningSess(fmt.Sprintf("s%d", i),
			Counts{Sessions: 1, Agents: 30, Working: 30},
			ws("w", "work", StatusWorking, panes...)))
	}
	return &Model{Sessions: sessions}
}

func TestPanelStressStaysUnderNodeBudget(t *testing.T) {
	t.Parallel()
	st := PanelState{Model: stressModel(), Settings: DefaultSettings()}
	tree := PanelTree(st, PanelWidth, PanelHeight)
	if n := nodeCount(tree); n > v1.MaxNodes {
		t.Fatalf("stress tree has %d nodes, over the %d budget", n, v1.MaxNodes)
	}
	checkLint(t, v1.ViewPanel, tree, PanelWidth, PanelHeight)
}

// Every interactive node must be reachable by a unique, space-free ID and
// declare at least one event.
func TestPanelInteractiveNodesAreAddressable(t *testing.T) {
	t.Parallel()
	for name, st := range panelStates() {
		seen := map[string]bool{}
		tree := PanelTree(st, PanelWidth, PanelHeight)
		walk(tree, func(n *v1.Node) {
			if n.Kind != v1.KindButton && n.Kind != v1.KindTextInput {
				return
			}
			if n.ID == "" || n.Name == "" || n.Role == "" || len(n.Events) == 0 {
				t.Errorf("%s: interactive node %+v is not addressable", name, n)
			}
			if strings_ContainsSpace(n.ID) {
				t.Errorf("%s: id %q has a space", name, n.ID)
			}
			if seen[n.ID] {
				t.Errorf("%s: duplicate id %q", name, n.ID)
			}
			seen[n.ID] = true
		})
	}
}

// --- tree helpers ---

func walk(n *v1.Node, fn func(*v1.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		walk(c, fn)
	}
}

func nodeCount(n *v1.Node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += nodeCount(c)
	}
	return total
}

func countText(n *v1.Node, want string) int {
	found := 0
	walk(n, func(x *v1.Node) {
		if x.Kind == v1.KindText && x.Text == want {
			found++
		}
	})
	return found
}

func strings_ContainsSpace(s string) bool {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			return true
		}
	}
	return false
}

func TestPanelStaleLabelAndErrorPlacement(t *testing.T) {
	t.Parallel()
	stale := runningSess("alpha", Counts{Sessions: 1, Agents: 1},
		onePaneWS("work", pane("p1", "shell", StatusIdle)))
	stale.Stale = true
	tree := PanelTree(PanelState{Model: modelWith(stale), Settings: DefaultSettings()}, PanelWidth, PanelHeight)
	checkLint(t, v1.ViewPanel, tree, PanelWidth, PanelHeight)
	labeled := false
	walk(tree, func(n *v1.Node) {
		if n.Kind == v1.KindText && n.Text == "stale" && n.Tone == v1.ToneError {
			labeled = true
		}
	})
	if !labeled {
		t.Fatal("stale session not labelled in error tone")
	}
	if got := worstTone(modelWith(stale)); got != v1.ToneError {
		t.Fatalf("worstTone(stale) = %v, want error tone", got)
	}

	// Spec: the action-error card sits at the bottom, after the session cards.
	errTree := PanelTree(panelStates()["actionerror"], PanelWidth, PanelHeight)
	var texts []string
	walk(errTree, func(n *v1.Node) {
		if n.Kind == v1.KindText {
			texts = append(texts, n.Text)
		}
	})
	errIdx, sessIdx := -1, -1
	for i, tx := range texts {
		if sessIdx < 0 && tx == "alpha" {
			sessIdx = i
		}
		if errIdx < 0 && strings.Contains(tx, "stop failed") {
			errIdx = i
		}
	}
	if errIdx < 0 || sessIdx < 0 || errIdx < sessIdx {
		t.Fatalf("action error at %d must follow session name at %d in %q", errIdx, sessIdx, texts)
	}
}
