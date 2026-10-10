package herdr

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/herdr/screenshot.png when CAPTURE=1: two
// sessions from a fixed clock, a running one with a blocked, a working and a
// done agent and one workspace peek open, and a stopped one listing its
// workspaces. Every value is invented.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return HumanizeSince(now, now.Add(-d)) }

	pane := func(id, title, tab string, st Status, since time.Duration, seq uint64) PaneRow {
		return PaneRow{
			PaneID: id, Title: title, TabLabel: tab, Status: st,
			StatusLabel: StatusLabel(nil, st), SinceLabel: ago(since),
			Seq: seq, Session: "demo", Readable: true,
		}
	}

	demo := SessionRow{
		Name:    "demo",
		Default: true,
		Running: true,
		Socket:  "/run/user/1000/herdr/demo.sock",
		Counts:  Counts{Sessions: 1, Agents: 3, Blocked: 1, Working: 1, Done: 1},
		Workspaces: []WorkspaceRow{
			{ID: "w1", Label: "api", Status: StatusBlocked, Panes: []PaneRow{
				pane("p1", "claude", "tab 1", StatusBlocked, 12*time.Minute, 4),
				pane("p2", "build", "tab 2", StatusWorking, 4*time.Minute, 2),
			}},
			{ID: "w2", Label: "site", Status: StatusDone, Panes: []PaneRow{
				pane("p3", "deploy", "tab 1", StatusDone, 2*time.Minute, 1),
			}},
		},
	}
	old := SessionRow{
		Name:              "old",
		Running:           false,
		Counts:            Counts{Sessions: 1},
		StoppedWorkspaces: []string{"scratch", "docs"},
	}

	m := &Model{Sessions: []SessionRow{demo, old}}
	m.Sort()

	state := PanelState{
		Model:    m,
		Settings: DefaultSettings(),
		Peeks: map[string]Peek{"p1": {
			PaneID: "p1",
			Text: "make: nothing to be done for 'all'.\n" +
				"ok   server   12 passed\n" +
				"FAIL tests    1 failed\n" +
				"    want status done, got blocked",
			Lines: 20,
		}},
	}

	capture.Panel(t, "herdr", PanelTree(state, PanelWidth, PanelHeight))
}
