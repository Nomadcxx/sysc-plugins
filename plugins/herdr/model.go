package herdr

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Wire types, JSON tags matching herdr schema v0.9.1 (protocol 22).

type WorkspaceInfo struct {
	WorkspaceID string        `json:"workspace_id"`
	Label       string        `json:"label"`
	Number      int           `json:"number"`
	Focused     bool          `json:"focused"`
	PaneCount   int           `json:"pane_count"`
	TabCount    int           `json:"tab_count"`
	ActiveTabID string        `json:"active_tab_id"`
	AgentStatus string        `json:"agent_status"`
	Worktree    *WorktreeInfo `json:"worktree"`
}

type WorktreeInfo struct {
	RepoKey          string `json:"repo_key"`
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

type TabInfo struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	Focused     bool   `json:"focused"`
	PaneCount   int    `json:"pane_count"`
	AgentStatus string `json:"agent_status"`
}

type PaneInfo struct {
	PaneID                string `json:"pane_id"`
	TerminalID            string `json:"terminal_id"`
	WorkspaceID           string `json:"workspace_id"`
	TabID                 string `json:"tab_id"`
	Focused               bool   `json:"focused"`
	Cwd                   string `json:"cwd"`
	ForegroundCwd         string `json:"foreground_cwd"`
	TerminalTitle         string `json:"terminal_title"`
	TerminalTitleStripped string `json:"terminal_title_stripped"`
	Title                 string `json:"title"`
	Label                 string `json:"label"`
	Agent                 string `json:"agent"`
	DisplayAgent          string `json:"display_agent"`
	AgentStatus           string `json:"agent_status"`
	Revision              uint64 `json:"revision"`
}

type AgentInfo struct {
	PaneID                string            `json:"pane_id"`
	TerminalID            string            `json:"terminal_id"`
	WorkspaceID           string            `json:"workspace_id"`
	TabID                 string            `json:"tab_id"`
	Agent                 string            `json:"agent"`
	DisplayAgent          string            `json:"display_agent"`
	Name                  string            `json:"name"`
	AgentStatus           string            `json:"agent_status"`
	Title                 string            `json:"title"`
	TerminalTitle         string            `json:"terminal_title"`
	TerminalTitleStripped string            `json:"terminal_title_stripped"`
	Cwd                   string            `json:"cwd"`
	ForegroundCwd         string            `json:"foreground_cwd"`
	Focused               bool              `json:"focused"`
	StateChangeSeq        uint64            `json:"state_change_seq"`
	StateLabels           map[string]string `json:"state_labels"`
}

type SnapshotDoc struct {
	Version            string          `json:"version"`
	Protocol           int             `json:"protocol"`
	FocusedWorkspaceID *string         `json:"focused_workspace_id"`
	FocusedTabID       *string         `json:"focused_tab_id"`
	FocusedPaneID      *string         `json:"focused_pane_id"`
	Workspaces         []WorkspaceInfo `json:"workspaces"`
	Tabs               []TabInfo       `json:"tabs"`
	Panes              []PaneInfo      `json:"panes"`
	Agents             []AgentInfo     `json:"agents"`
}

// Status is a pane's agent status, ordered from most to least urgent.
type Status string

const (
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	StatusWorking Status = "working"
	StatusIdle    Status = "idle"
	StatusUnknown Status = "unknown"
	StatusNone    Status = "none"
)

// StatusPriority returns the urgency rank of s, lowest being most urgent.
// Unrecognized values rank with StatusNone.
func StatusPriority(s Status) int {
	switch s {
	case StatusBlocked:
		return 0
	case StatusDone:
		return 1
	case StatusWorking:
		return 2
	case StatusIdle:
		return 3
	case StatusUnknown:
		return 4
	default:
		return 5
	}
}

// PaneRow is one agent pane in the view model.
type PaneRow struct {
	PaneID      string
	Title       string
	TabLabel    string
	Status      Status
	StatusLabel string
	Since       time.Time
	Seq         uint64
	Session     string
	Readable    bool
}

// WorkspaceRow is one workspace with its panes.
type WorkspaceRow struct {
	ID     string
	Label  string
	Panes  []PaneRow
	Status Status
}

// SessionRow is one herdr session in the view model.
type SessionRow struct {
	Name              string
	Default           bool
	Running           bool
	Stale             bool
	Err               string
	Workspaces        []WorkspaceRow
	Counts            Counts
	StoppedWorkspaces []string
}

// Counts rolls up agent totals for a session.
type Counts struct {
	Sessions int
	Agents   int
	Blocked  int
	Done     int
	Working  int
}

// Model is the full render model.
type Model struct {
	HerdrMissing bool
	DiscoveryErr string
	Sessions     []SessionRow
}

// BuildSession converts a raw snapshot into a SessionRow. since maps pane IDs
// to the time their status last changed; missing entries fall back to now.
func BuildSession(info SessionInfo, doc *SnapshotDoc, stopped []StoppedWorkspace, since map[string]time.Time, now time.Time) SessionRow {
	row := SessionRow{
		Name:    info.Name,
		Default: info.Default,
		Running: info.Running,
		Counts:  Counts{Sessions: 1},
	}
	for _, w := range stopped {
		row.StoppedWorkspaces = append(row.StoppedWorkspaces, w.Name)
	}

	if doc == nil {
		return row
	}

	agents := make(map[string]*AgentInfo, len(doc.Agents))
	for i := range doc.Agents {
		agents[doc.Agents[i].PaneID] = &doc.Agents[i]
	}
	tabs := make(map[string]*TabInfo, len(doc.Tabs))
	for i := range doc.Tabs {
		tabs[doc.Tabs[i].TabID] = &doc.Tabs[i]
	}

	for _, w := range doc.Workspaces {
		ws := WorkspaceRow{ID: w.WorkspaceID, Label: workspaceLabel(w), Status: StatusNone}
		for _, p := range doc.Panes {
			if p.WorkspaceID != w.WorkspaceID {
				continue
			}
			a := agents[p.PaneID]
			st := paneStatus(p, a)
			pr := PaneRow{
				PaneID:      p.PaneID,
				Title:       PaneTitle(p, a),
				TabLabel:    tabLabel(tabs, p.TabID),
				Status:      st,
				StatusLabel: StatusLabel(a, st),
				Since:       now,
				Session:     info.Name,
				Readable:    hasAgent(p, a),
			}
			if a != nil {
				pr.Seq = a.StateChangeSeq
			}
			if t, ok := since[p.PaneID]; ok {
				pr.Since = t
			}
			ws.Panes = append(ws.Panes, pr)
			if st != StatusNone {
				row.Counts.Agents++
			}
			switch st {
			case StatusBlocked:
				row.Counts.Blocked++
			case StatusDone:
				row.Counts.Done++
			case StatusWorking:
				row.Counts.Working++
			}
			if StatusPriority(st) < StatusPriority(ws.Status) {
				ws.Status = st
			}
		}
		row.Workspaces = append(row.Workspaces, ws)
	}
	return row
}

// SortPanes orders every workspace's panes by priority, then state-change seq
// descending, then pane ID ascending.
func (s SessionRow) SortPanes() {
	for i := range s.Workspaces {
		sortPanes(s.Workspaces[i].Panes)
	}
}

func sortPanes(panes []PaneRow) {
	sort.SliceStable(panes, func(i, j int) bool {
		pi, pj := StatusPriority(panes[i].Status), StatusPriority(panes[j].Status)
		if pi != pj {
			return pi < pj
		}
		if panes[i].Seq != panes[j].Seq {
			return panes[i].Seq > panes[j].Seq
		}
		return panes[i].PaneID < panes[j].PaneID
	})
}

// Sort orders sessions (default first, then name), workspaces (priority, then
// label), and panes.
func (m *Model) Sort() {
	sort.SliceStable(m.Sessions, func(i, j int) bool {
		if m.Sessions[i].Default != m.Sessions[j].Default {
			return m.Sessions[i].Default
		}
		return m.Sessions[i].Name < m.Sessions[j].Name
	})
	for i := range m.Sessions {
		s := &m.Sessions[i]
		sort.SliceStable(s.Workspaces, func(a, b int) bool {
			pa, pb := StatusPriority(s.Workspaces[a].Status), StatusPriority(s.Workspaces[b].Status)
			if pa != pb {
				return pa < pb
			}
			return s.Workspaces[a].Label < s.Workspaces[b].Label
		})
		s.SortPanes()
	}
}

// PaneTitle picks the most specific display name for a pane, preferring the
// richer agent record when present.
func PaneTitle(p PaneInfo, a *AgentInfo) string {
	var (
		agentTitle, agentStripped, agentTerm, agentName string
		agentDisplay, agentAgent, agentCwd, agentFg     string
	)
	if a != nil {
		agentTitle = a.Title
		agentStripped = a.TerminalTitleStripped
		agentTerm = a.TerminalTitle
		agentName = a.Name
		agentDisplay = a.DisplayAgent
		agentAgent = a.Agent
		agentCwd = a.Cwd
		agentFg = a.ForegroundCwd
	}
	for _, c := range []string{
		firstNonEmpty(agentTitle, p.Title),
		firstNonEmpty(agentStripped, p.TerminalTitleStripped),
		firstNonEmpty(agentTerm, p.TerminalTitle),
		agentName,
		firstNonEmpty(agentDisplay, p.DisplayAgent),
		firstNonEmpty(agentAgent, p.Agent),
	} {
		if c != "" {
			return c
		}
	}
	if n := pathName(firstNonEmpty(agentCwd, p.Cwd, agentFg, p.ForegroundCwd)); n != "" {
		return n
	}
	return p.PaneID
}

// StatusLabel returns the human label for s, preferring a pane's custom
// state_labels entry.
func StatusLabel(a *AgentInfo, s Status) string {
	if a != nil {
		if v, ok := a.StateLabels[string(s)]; ok && v != "" {
			return v
		}
	}
	switch s {
	case StatusBlocked:
		return "Needs you"
	case StatusDone:
		return "Done"
	case StatusWorking:
		return "Working"
	case StatusIdle:
		return "Ready"
	case StatusUnknown:
		return "Unknown"
	default:
		return "No agent"
	}
}

// HumanizeSince renders the elapsed time between now and t at whole-unit
// granularity.
func HumanizeSince(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	secs := int(d / time.Second)
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm", secs/60)
	case secs < 86400:
		return fmt.Sprintf("%dh", secs/3600)
	default:
		return fmt.Sprintf("%dd", secs/86400)
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// WrapText strips ANSI, wraps at width runes (preserving newlines), and caps
// the total byte count at maxBytes, appending "…" when truncated.
func WrapText(s string, width int, maxBytes int) []string {
	if maxBytes <= 0 {
		return nil
	}
	if width < 1 {
		width = 1
	}
	s = stripANSI(s)
	if s == "" {
		return nil
	}

	var out []string
	used := 0
	flush := func(line string) bool {
		if used+len(line) <= maxBytes {
			out = append(out, line)
			used += len(line)
			return true
		}
		remain := maxBytes - used
		if remain <= 0 {
			return false
		}
		if remain <= len("…") {
			out = append(out, truncBytes(line, remain))
			used = maxBytes
			return false
		}
		out = append(out, truncBytes(line, remain-len("…"))+"…")
		used = maxBytes
		return false
	}

	for _, para := range strings.Split(s, "\n") {
		r := []rune(para)
		for len(r) > width {
			if !flush(string(r[:width])) {
				return out
			}
			r = r[width:]
		}
		if !flush(string(r)) {
			return out
		}
	}
	return out
}

func truncBytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if b.Len()+utf8.RuneLen(r) > n {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func pathName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && path == home {
		return "~"
	}
	return filepath.Base(path)
}

func paneStatus(p PaneInfo, a *AgentInfo) Status {
	if !hasAgent(p, a) {
		return StatusNone
	}
	raw := p.AgentStatus
	if a != nil {
		raw = a.AgentStatus
	}
	switch Status(raw) {
	case StatusBlocked, StatusDone, StatusWorking, StatusIdle, StatusUnknown:
		return Status(raw)
	default:
		return StatusNone
	}
}

func hasAgent(p PaneInfo, a *AgentInfo) bool {
	return a != nil || p.Agent != ""
}

func workspaceLabel(w WorkspaceInfo) string {
	if w.Label != "" {
		return w.Label
	}
	if w.Worktree != nil && w.Worktree.RepoName != "" {
		return w.Worktree.RepoName
	}
	return w.WorkspaceID
}

func tabLabel(tabs map[string]*TabInfo, tabID string) string {
	t, ok := tabs[tabID]
	if !ok {
		return ""
	}
	if t.Label != "" {
		return t.Label
	}
	return fmt.Sprintf("tab %d", t.Number)
}
