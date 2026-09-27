package protonvpn

import (
	"strings"
	"testing"

	lint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// findNode walks the tree and returns the node carrying the given ID,
// failing the test when none does.
func findNode(t *testing.T, root *v1.Node, id string) *v1.Node {
	t.Helper()
	var found *v1.Node
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil || found != nil {
			return
		}
		if n.ID == id {
			found = n
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no node with id %q", id)
	}
	return found
}

// assertTextContains fails when no node in the tree carries want as a
// substring of its text.
func assertTextContains(t *testing.T, root *v1.Node, want string) {
	t.Helper()
	found := false
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil || found {
			return
		}
		if strings.Contains(n.Text, want) {
			found = true
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if !found {
		t.Fatalf("no text contains %q", want)
	}
}

// assertTextLacks fails when any node in the tree carries unwanted as a
// substring of its text.
func assertTextLacks(t *testing.T, root *v1.Node, unwanted string) {
	t.Helper()
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if strings.Contains(n.Text, unwanted) {
			t.Fatalf("text contains %q", unwanted)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}

func TestPanelSkeleton(t *testing.T) {
	s := PanelState{Snap: Snapshot{Phase: PhaseConnected, IP: "198.51.100.7", Status: Status{Server: "US-NY#1", Location: "New York, United States", Country: "US", Protocol: "wireguard"}}, Tab: "connections", HasCLI: true, Traffic: true}
	root := Panel(s)
	assertTextContains(t, root, "Protected")
	assertTextContains(t, root, "US-NY#1")
	assertTextContains(t, root, "New York, United States")
	assertTextContains(t, root, "198.51.100.7 · wireguard")
	if btn := findNode(t, root, "action"); btn.Text != "Disconnect" {
		t.Fatalf("action %q", btn.Text)
	}
}

func TestActionButtonMorphs(t *testing.T) {
	cases := []struct {
		phase        Phase
		want         string
		wantDisabled bool
	}{
		{PhaseDisconnected, "Connect", false},
		{PhaseConnecting, "Cancel", false},
		{PhaseConnected, "Disconnect", false},
		{PhaseDisconnecting, "Disconnect", true},
		{PhaseError, "Connect", false},
	}
	for _, tc := range cases {
		btn := findNode(t, Panel(PanelState{Snap: Snapshot{Phase: tc.phase}, Tab: "connections", HasCLI: true}), "action")
		if btn.Text != tc.want || btn.Disabled != tc.wantDisabled {
			t.Fatalf("phase %v: %+v", tc.phase, btn)
		}
	}
}

func TestPanelDisconnectedCopy(t *testing.T) {
	root := Panel(PanelState{Snap: Snapshot{Phase: PhaseDisconnected}, Tab: "connections", HasCLI: true})
	assertTextContains(t, root, "Unprotected")
	assertTextContains(t, root, "Fastest country")
	assertTextContains(t, root, "Auto-selected on connect")
}

func TestPanelErrorDetailLine(t *testing.T) {
	root := Panel(PanelState{Snap: Snapshot{Phase: PhaseError, Err: "Tunnel setup failed"}, Tab: "connections", HasCLI: true})
	assertTextContains(t, root, "Connection error")
	assertTextContains(t, root, "Tunnel setup failed")
}

func TestPanelCLIMissingBanner(t *testing.T) {
	root := Panel(PanelState{Tab: "connections"})
	assertTextContains(t, root, "protonvpn CLI not found")
}

func TestPanelTabNav(t *testing.T) {
	root := Panel(PanelState{Tab: "protection", HasCLI: true})
	nav := findNode(t, root, "tab:protection")
	if nav.Fill != "accent" {
		t.Fatalf("active tab fill %q", nav.Fill)
	}
	if other := findNode(t, root, "tab:connections"); other.Fill != "soft" {
		t.Fatalf("inactive fill %q", other.Fill)
	}
}

func TestPanelTrafficLine(t *testing.T) {
	s := PanelState{Snap: Snapshot{Phase: PhaseConnected, IP: "198.51.100.7", RxRate: 1.2e6, TxRate: 340000, Status: Status{Server: "US-NY#1", Protocol: "wireguard"}}, Tab: "connections", HasCLI: true, Traffic: true}
	root := Panel(s)
	assertTextContains(t, root, "1.2 MB/s")
	assertTextContains(t, root, "340 KB/s")
	s.Traffic = false
	assertTextLacks(t, Panel(s), "MB/s")
	assertTextContains(t, Panel(s), "198.51.100.7 · wireguard")
}

func TestPanelLintEveryState(t *testing.T) {
	for _, tab := range []string{"connections", "protection", "account"} {
		for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
			root := Panel(PanelState{Snap: Snapshot{Phase: phase, IP: "198.51.100.7", Status: Status{Server: "US-NY#1", Location: "New York, United States", Country: "US", Protocol: "wireguard"}, RxRate: 1024, TxRate: 512, Port: 51820}, Tab: tab, HasCLI: true, Traffic: true})
			if findings := lint.Tree(root, v1.ViewPanel, 460, 580); len(findings) > 0 {
				t.Fatalf("tab %s phase %v: %v", tab, phase, findings)
			}
		}
	}
}

// TestPanelLintCoverageSweep walks the states TestPanelLintEveryState does
// not reach: search text, notice, an expanded country, a maintenance-marked
// country, the split-tunnel app picker, an error line, and a signed-in
// account, with and without the CLI banner.
func TestPanelLintCoverageSweep(t *testing.T) {
	fallback := FallbackCountries()
	variants := map[string]func(*PanelState){
		"plain": func(*PanelState) {},
		"search": func(s *PanelState) {
			s.Conns.Query = "nl"
			s.Conns.Notice = "Server list unavailable"
		},
		"expanded":    func(s *PanelState) { s.Conns.Expanded = "NL" },
		"maintenance": func(s *PanelState) { s.Conns.Countries[3].Maintenance = true },
		"app-picker": func(s *PanelState) {
			s.Prot.AppQuery = "fire"
			s.Prot.Candidates = []App{{Value: "/usr/bin/firefox", Label: "Firefox"}}
		},
		"err": func(s *PanelState) { s.Snap.Err = "Tunnel setup failed" },
	}
	for _, hasCLI := range []bool{true, false} {
		for _, tab := range []string{"connections", "protection", "account"} {
			for name, mut := range variants {
				s := PanelState{
					Snap: Snapshot{Phase: PhaseConnected, IP: "198.51.100.7",
						Status:    Status{Server: "NL#1", Country: "NL", Protocol: "wireguard"},
						Interface: "proton0", RxRate: 1024, TxRate: 512, Port: 51820},
					Tab: tab, HasCLI: hasCLI, Traffic: true,
					Conns: ConnectionsState{Countries: fallback},
					Prot:  ProtectionState{SplitTunnel: true, Apps: []string{"/usr/bin/thunderbird"}, Port: 51820, HasCopyTool: true},
					Acct:  AccountState{SignedIn: true, Settings: map[string]string{"bar_mode": "code"}},
				}
				mut(&s)
				if findings := lint.Tree(Panel(s), v1.ViewPanel, 460, 580); len(findings) > 0 {
					t.Fatalf("hasCLI %v tab %s variant %s: %v", hasCLI, tab, name, findings)
				}
			}
		}
	}
}
