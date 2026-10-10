package herdr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadSnapshot(t *testing.T) *SnapshotDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "snapshot.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var wrapper struct {
		Result struct {
			Snapshot SnapshotDoc `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &wrapper.Result.Snapshot
}

func TestStatusPriorityTotalOrder(t *testing.T) {
	ordered := []Status{StatusBlocked, StatusDone, StatusWorking, StatusIdle, StatusUnknown, StatusNone}
	for i := 1; i < len(ordered); i++ {
		if StatusPriority(ordered[i-1]) >= StatusPriority(ordered[i]) {
			t.Fatalf("priority not ascending: %s(%d) vs %s(%d)",
				ordered[i-1], StatusPriority(ordered[i-1]), ordered[i], StatusPriority(ordered[i]))
		}
	}
	if StatusPriority(StatusNone) <= StatusPriority(StatusUnknown) {
		t.Fatal("none must be the least urgent status")
	}
	if StatusPriority(Status("bogus")) != StatusPriority(StatusNone) {
		t.Fatalf("unknown status value should rank as none, got %d", StatusPriority(Status("bogus")))
	}
}

func TestPaneTitleFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base := PaneInfo{PaneID: "w1:p1"}

	cases := []struct {
		name  string
		pane  PaneInfo
		agent *AgentInfo
		want  string
	}{
		{"title", PaneInfo{PaneID: "w1:p1", Title: "T", TerminalTitle: "TT"}, nil, "T"},
		{"stripped", PaneInfo{PaneID: "w1:p1", TerminalTitleStripped: "S", TerminalTitle: "TT"}, nil, "S"},
		{"terminal_title", PaneInfo{PaneID: "w1:p1", TerminalTitle: "TT"}, nil, "TT"},
		{"display_agent", PaneInfo{PaneID: "w1:p1", DisplayAgent: "disp"}, nil, "disp"},
		{"agent", PaneInfo{PaneID: "w1:p1", Agent: "claude"}, nil, "claude"},
		{"cwd_basename", PaneInfo{PaneID: "w1:p1", Cwd: "/home/dev/demo"}, nil, "demo"},
		{"foreground_cwd_basename", PaneInfo{PaneID: "w1:p1", ForegroundCwd: "/home/dev/site"}, nil, "site"},
		{"home_relative", PaneInfo{PaneID: "w1:p1", Cwd: home}, nil, "~"},
		{"pane_id_last", base, nil, "w1:p1"},
		{"agent_title_wins", PaneInfo{PaneID: "w1:p1", Title: "pane"}, &AgentInfo{Title: "agent"}, "agent"},
		{"agent_pane_fills_gap", PaneInfo{PaneID: "w1:p1", Title: "pane"}, &AgentInfo{Cwd: "/x/y"}, "pane"},
		{"agent_name_tier", PaneInfo{PaneID: "w1:p1", DisplayAgent: "disp"}, &AgentInfo{Name: "myname"}, "myname"},
		{"agent_display_wins", PaneInfo{PaneID: "w1:p1", DisplayAgent: "paneD"}, &AgentInfo{DisplayAgent: "agentD"}, "agentD"},
		{"agent_cwd_wins", PaneInfo{PaneID: "w1:p1", Cwd: "/p/panecwd"}, &AgentInfo{Cwd: "/a/agentcwd"}, "agentcwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PaneTitle(tc.pane, tc.agent); got != tc.want {
				t.Fatalf("PaneTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusLabel(t *testing.T) {
	defaults := map[Status]string{
		StatusBlocked: "Needs you",
		StatusDone:    "Done",
		StatusWorking: "Working",
		StatusIdle:    "Ready",
		StatusUnknown: "Unknown",
		StatusNone:    "No agent",
	}
	for s, want := range defaults {
		if got := StatusLabel(nil, s); got != want {
			t.Errorf("StatusLabel(nil, %s) = %q, want %q", s, got, want)
		}
	}

	a := &AgentInfo{StateLabels: map[string]string{"blocked": "Custom", "working": "Busy"}}
	if got := StatusLabel(a, StatusBlocked); got != "Custom" {
		t.Fatalf("custom label = %q, want Custom", got)
	}
	if got := StatusLabel(a, StatusWorking); got != "Busy" {
		t.Fatalf("custom label = %q, want Busy", got)
	}
	if got := StatusLabel(a, StatusDone); got != "Done" {
		t.Fatalf("missing custom label should fall back to default, got %q", got)
	}
	if got := StatusLabel(&AgentInfo{StateLabels: map[string]string{"done": ""}}, StatusDone); got != "Done" {
		t.Fatalf("empty custom label should fall back to default, got %q", got)
	}
}

func TestHumanizeSince(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{65 * time.Second, "1m"},
		{90 * time.Minute, "1h"},
		{50 * time.Hour, "2d"},
		{-5 * time.Second, "0s"},
	}
	for _, tc := range cases {
		if got := HumanizeSince(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("HumanizeSince(-%s) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

func TestWrapText(t *testing.T) {
	t.Run("wraps_at_width", func(t *testing.T) {
		got := WrapText("abcdef", 2, 100)
		want := []string{"ab", "cd", "ef"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("WrapText = %v, want %v", got, want)
		}
	})
	t.Run("strips_ansi", func(t *testing.T) {
		got := WrapText("\x1b[31mred\x1b[0m", 100, 100)
		if len(got) != 1 || got[0] != "red" {
			t.Fatalf("WrapText = %v, want [red]", got)
		}
	})
	t.Run("keeps_newlines", func(t *testing.T) {
		got := WrapText("a\nb", 10, 100)
		if strings.Join(got, "|") != "a|b" {
			t.Fatalf("WrapText = %v, want [a b]", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := WrapText("", 10, 100); got != nil {
			t.Fatalf("WrapText(\"\") = %v, want nil", got)
		}
	})
	t.Run("caps_bytes_with_ellipsis", func(t *testing.T) {
		got := WrapText(strings.Repeat("a", 2600), 1500, 2000)
		total := 0
		for _, l := range got {
			total += len(l)
		}
		if total != 2000 {
			t.Fatalf("total bytes = %d, want 2000", total)
		}
		last := got[len(got)-1]
		if !strings.HasSuffix(last, "…") {
			t.Fatalf("last line %q should end with ellipsis", last)
		}
		if len(got[0]) != 1500 {
			t.Fatalf("first line len = %d, want 1500", len(got[0]))
		}
	})
}

func TestBuildSessionFromFixture(t *testing.T) {
	doc := loadSnapshot(t)
	info := SessionInfo{Name: "demo", Running: true}
	now := time.Unix(1000, 0)

	row := BuildSession(info, doc, nil, nil, now)

	if row.Name != "demo" || !row.Running || row.Default || row.Stale || row.Err != "" {
		t.Fatalf("session header wrong: %+v", row)
	}
	if len(row.Workspaces) != 1 {
		t.Fatalf("want 1 workspace, got %d", len(row.Workspaces))
	}
	ws := row.Workspaces[0]
	if ws.ID != "w1" || ws.Label != "demo" || ws.Status != StatusBlocked {
		t.Fatalf("workspace wrong: %+v", ws)
	}
	if len(ws.Panes) != 1 {
		t.Fatalf("want 1 pane, got %d", len(ws.Panes))
	}
	p := ws.Panes[0]
	if p.PaneID != "w1:p1" || p.Status != StatusBlocked || p.StatusLabel != "Needs you" {
		t.Fatalf("pane status wrong: %+v", p)
	}
	if p.Title != "dev@demo:/home/dev/demo" {
		t.Fatalf("pane title = %q", p.Title)
	}
	if p.TabLabel != "1" {
		t.Fatalf("tab label = %q, want 1", p.TabLabel)
	}
	if p.Seq != 2 {
		t.Fatalf("seq = %d, want 2", p.Seq)
	}
	if !p.Readable {
		t.Fatal("pane with agent should be readable")
	}
	if !p.Since.Equal(now) {
		t.Fatalf("since = %v, want now", p.Since)
	}
	if row.Counts.Sessions != 1 || row.Counts.Agents != 1 || row.Counts.Blocked != 1 || row.Counts.Done != 0 || row.Counts.Working != 0 {
		t.Fatalf("counts wrong: %+v", row.Counts)
	}
}

func TestBuildSessionFallbacks(t *testing.T) {
	doc := &SnapshotDoc{
		Version:  "0.9.1",
		Protocol: 22,
		Workspaces: []WorkspaceInfo{
			{WorkspaceID: "w1", Label: "", Worktree: &WorktreeInfo{RepoName: "repo"}},
			{WorkspaceID: "w2", Label: ""},
		},
		Tabs: []TabInfo{
			{TabID: "w1:t1", WorkspaceID: "w1", Label: "", Number: 3},
		},
		Panes: []PaneInfo{
			{PaneID: "w1:p1", WorkspaceID: "w1", TabID: "w1:t1", Agent: "claude", AgentStatus: "idle"},
			{PaneID: "w2:p1", WorkspaceID: "w2", TabID: "w2:t9", AgentStatus: "idle"},
		},
	}
	since := map[string]time.Time{"w1:p1": time.Unix(50, 0)}
	now := time.Unix(100, 0)
	stopped := []StoppedWorkspace{{ID: "s1", Name: "api"}, {ID: "s2", Name: "site"}}

	row := BuildSession(SessionInfo{Name: "demo"}, doc, stopped, since, now)

	if row.Workspaces[0].Label != "repo" {
		t.Fatalf("worktree fallback label = %q, want repo", row.Workspaces[0].Label)
	}
	if row.Workspaces[1].Label != "w2" {
		t.Fatalf("id fallback label = %q, want w2", row.Workspaces[1].Label)
	}
	if got := row.Workspaces[0].Panes[0].TabLabel; got != "tab 3" {
		t.Fatalf("tab number fallback = %q, want 'tab 3'", got)
	}
	if got := row.Workspaces[1].Panes[0].TabLabel; got != "" {
		t.Fatalf("missing tab label = %q, want empty", got)
	}
	if got := row.Workspaces[0].Panes[0].Since; !got.Equal(time.Unix(50, 0)) {
		t.Fatalf("since from map = %v, want 50", got)
	}
	if got := row.Workspaces[0].Panes[0].SinceLabel; got != "50s" {
		t.Fatalf("since label >10s = %q, want 50s", got)
	}
	if got := row.Workspaces[1].Panes[0].SinceLabel; got != "" {
		t.Fatalf("since label <10s = %q, want empty", got)
	}
	if got := row.Workspaces[1].Panes[0].Since; !got.Equal(now) {
		t.Fatalf("since default = %v, want now", got)
	}
	if row.Workspaces[1].Panes[0].Readable {
		t.Fatal("pane with no agent should not be readable")
	}
	if strings.Join(row.StoppedWorkspaces, ",") != "api,site" {
		t.Fatalf("stopped workspaces = %v", row.StoppedWorkspaces)
	}
}

func TestBuildSessionNoAgentIsNone(t *testing.T) {
	doc := &SnapshotDoc{
		Workspaces: []WorkspaceInfo{{WorkspaceID: "w1", Label: "one"}},
		Panes:      []PaneInfo{{PaneID: "w1:p1", WorkspaceID: "w1"}},
	}
	row := BuildSession(SessionInfo{Name: "demo"}, doc, nil, nil, time.Unix(0, 0))
	p := row.Workspaces[0].Panes[0]
	if p.Status != StatusNone || p.StatusLabel != "No agent" {
		t.Fatalf("agentless pane = %+v", p)
	}
	if row.Workspaces[0].Status != StatusNone {
		t.Fatalf("agentless workspace status = %s, want none", row.Workspaces[0].Status)
	}
	if row.Counts.Agents != 0 {
		t.Fatalf("agent count = %d, want 0", row.Counts.Agents)
	}
}

func TestBuildSessionUnknownAgentStatus(t *testing.T) {
	doc := &SnapshotDoc{
		Workspaces: []WorkspaceInfo{{WorkspaceID: "w1", Label: "one"}},
		Panes:      []PaneInfo{{PaneID: "w1:p1", WorkspaceID: "w1", Agent: "claude", AgentStatus: "unknown"}},
	}
	row := BuildSession(SessionInfo{Name: "demo"}, doc, nil, nil, time.Unix(0, 0))
	p := row.Workspaces[0].Panes[0]
	if p.Status != StatusUnknown || p.StatusLabel != "Unknown" {
		t.Fatalf("unknown-status pane = %+v", p)
	}
	if !p.Readable {
		t.Fatal("pane with agent should be readable")
	}
	if row.Counts.Agents != 1 {
		t.Fatalf("agents = %d, want 1", row.Counts.Agents)
	}
}

func TestSortPanesAndWorkspaces(t *testing.T) {
	doc := &SnapshotDoc{
		Workspaces: []WorkspaceInfo{
			{WorkspaceID: "wA", Label: "alpha"},
			{WorkspaceID: "wB", Label: "beta"},
		},
		Agents: []AgentInfo{
			{PaneID: "wA:p1", AgentStatus: "done", StateChangeSeq: 9},
			{PaneID: "wB:p1", AgentStatus: "working", StateChangeSeq: 5},
			{PaneID: "wB:p2", AgentStatus: "blocked", StateChangeSeq: 1},
		},
		Panes: []PaneInfo{
			{PaneID: "wA:p1", WorkspaceID: "wA", Agent: "claude", AgentStatus: "done"},
			{PaneID: "wB:p1", WorkspaceID: "wB", Agent: "claude", AgentStatus: "working"},
			{PaneID: "wB:p2", WorkspaceID: "wB", Agent: "claude", AgentStatus: "blocked"},
		},
	}
	row := BuildSession(SessionInfo{Name: "demo"}, doc, nil, nil, time.Unix(0, 0))
	m := &Model{Sessions: []SessionRow{row}}
	m.Sort()

	if m.Sessions[0].Workspaces[0].ID != "wB" || m.Sessions[0].Workspaces[1].ID != "wA" {
		t.Fatalf("workspaces not sorted by priority: got %s,%s", m.Sessions[0].Workspaces[0].ID, m.Sessions[0].Workspaces[1].ID)
	}
	got := []string{m.Sessions[0].Workspaces[0].Panes[0].PaneID, m.Sessions[0].Workspaces[0].Panes[1].PaneID}
	if strings.Join(got, ",") != "wB:p2,wB:p1" {
		t.Fatalf("panes not sorted: %v", got)
	}
}

func TestSortPanesTieBreaks(t *testing.T) {
	s := SessionRow{Workspaces: []WorkspaceRow{{Panes: []PaneRow{
		{PaneID: "b", Status: StatusWorking, Seq: 1},
		{PaneID: "a", Status: StatusWorking, Seq: 1},
		{PaneID: "c", Status: StatusWorking, Seq: 5},
	}}}}
	s.SortPanes()
	got := []string{s.Workspaces[0].Panes[0].PaneID, s.Workspaces[0].Panes[1].PaneID, s.Workspaces[0].Panes[2].PaneID}
	if strings.Join(got, ",") != "c,a,b" {
		t.Fatalf("tie-break order = %v, want c,a,b", got)
	}
}

func TestModelSortSessions(t *testing.T) {
	m := &Model{Sessions: []SessionRow{
		{Name: "zzz", Default: true},
		{Name: "mmm"},
		{Name: "aaa"},
	}}
	m.Sort()
	got := []string{m.Sessions[0].Name, m.Sessions[1].Name, m.Sessions[2].Name}
	if strings.Join(got, ",") != "zzz,aaa,mmm" {
		t.Fatalf("session order = %v, want zzz,aaa,mmm", got)
	}
}
