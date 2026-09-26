package protonvpn

import (
	"strings"
	"testing"

	lint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// lookupNode is findNode without the failure side effect: it returns nil when
// no node carries the id, so tests can assert something is filtered out.
func lookupNode(root *v1.Node, id string) *v1.Node {
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
	return found
}

func TestConnectionsQuickConnectRow(t *testing.T) {
	root := ConnectionsTree(ConnectionsState{Snap: Snapshot{Phase: PhaseDisconnected}})
	for _, id := range []string{"qc:fastest", "qc:random", "qc:p2p", "qc:tor"} {
		findNode(t, root, id)
	}
}

func TestConnectionsCountryRows(t *testing.T) {
	countries := []Country{
		{Code: "US", Name: "United States", Load: 63, Servers: []Server{{Name: "US-NY#1", City: "New York", Load: 30, Up: true}}},
		{Code: "NL", Name: "Netherlands", Load: 0, Maintenance: true, Servers: []Server{{Name: "NL#1", Up: false}}},
	}
	root := ConnectionsTree(ConnectionsState{Countries: countries})
	assertTextContains(t, root, "United States")
	assertTextContains(t, root, "1 server · 63%")
	findNode(t, root, "country:US")
	nl := findNode(t, root, "country:NL")
	if nl.Tone != v1.ToneSubtle {
		t.Fatal("maintenance row dimmed")
	}
	if btn := findNode(t, root, "connect:NL"); !btn.Disabled {
		t.Fatal("maintenance connect disabled")
	}
}

func TestConnectionsExpandShowsServers(t *testing.T) {
	countries := []Country{{Code: "US", Name: "United States", Servers: []Server{
		{Name: "US-NY#1", City: "New York", Load: 30, Up: true, Features: FeatP2P},
		{Name: "US-CA#1", City: "Los Angeles", Load: 95, Up: true},
	}}}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Expanded: "US"})
	assertTextContains(t, root, "US-NY#1")
	assertTextContains(t, root, "US-CA#1")
	findNode(t, root, "server:US-NY#1")
}

func TestConnectionsLoadTone(t *testing.T) {
	countries := []Country{
		{Code: "US", Name: "United States", Load: 95, Servers: []Server{{Name: "US-NY#1", Up: true}}},
		{Code: "DE", Name: "Germany", Load: 80, Servers: []Server{{Name: "DE-FRA#1", Up: true}}},
		{Code: "SE", Name: "Sweden", Load: 40, Servers: []Server{{Name: "SE-STO#1", Up: true}}},
	}
	root := ConnectionsTree(ConnectionsState{Countries: countries})
	if p := findNode(t, root, "load:US"); p.Tone != v1.ToneError {
		t.Fatalf("95%% tone %q", p.Tone)
	}
	if p := findNode(t, root, "load:DE"); p.Tone != v1.ToneAccent {
		t.Fatalf("80%% tone %q", p.Tone)
	}
	if p := findNode(t, root, "load:SE"); p.Tone != "" {
		t.Fatalf("40%% tone %q", p.Tone)
	}
}

func TestConnectionsSearchFilters(t *testing.T) {
	countries := []Country{
		{Code: "US", Name: "United States", Servers: []Server{{Name: "US-NY#1", Up: true}}},
		{Code: "DE", Name: "Germany", Servers: []Server{{Name: "DE-FRA#1", Up: true}}},
	}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Query: "germ"})
	assertTextContains(t, root, "Germany")
	if lookupNode(root, "country:US") != nil {
		t.Fatal("US should be filtered out")
	}
	// Server-name search surfaces matches grouped under their country, auto-expanded.
	root = ConnectionsTree(ConnectionsState{Countries: countries, Query: "NY#1"})
	assertTextContains(t, root, "US-NY#1")
}

func TestConnectionsConnectedCountryTint(t *testing.T) {
	countries := []Country{{Code: "US", Name: "United States"}}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Snap: Snapshot{Phase: PhaseConnected, Status: Status{Country: "US"}}})
	if row := findNode(t, root, "country:US"); row.Fill != "container" {
		t.Fatalf("fill %q", row.Fill)
	}
}

func TestConnectionsFlagGlyph(t *testing.T) {
	countries := []Country{{Code: "US", Name: "United States"}}
	flag := "\U0001F1FA\U0001F1F8"
	root := ConnectionsTree(ConnectionsState{Countries: countries, Flags: true})
	assertTextContains(t, root, flag)
	assertTextLacks(t, root, "US")
	root = ConnectionsTree(ConnectionsState{Countries: countries})
	assertTextContains(t, root, "US")
	assertTextLacks(t, root, flag)
}

func TestConnectionsLintLongNames(t *testing.T) {
	long := strings.Repeat("X", 30)
	countries := []Country{{Code: "US", Name: long, Servers: []Server{{Name: long, City: long, Load: 50, Up: true}}}}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Expanded: "US"})
	if findings := lint.Tree(root, v1.ViewPanel, 460, 580); len(findings) > 0 {
		t.Fatalf("%v", findings)
	}
}

func TestConnectionsLintEveryState(t *testing.T) {
	countries := []Country{
		{Code: "US", Name: "United States", Load: 63, Servers: []Server{{Name: "US-NY#1", City: "New York", Load: 30, Up: true, Features: FeatP2P}}},
		{Code: "NL", Name: "Netherlands", Maintenance: true, Servers: []Server{{Name: "NL#1", Up: false}}},
	}
	for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
		for _, st := range []ConnectionsState{
			{Countries: countries},
			{Countries: countries, Notice: "Server list unavailable"},
			{Countries: countries, Expanded: "US"},
			{Countries: countries, Query: "NY#1"},
			{Countries: nil},
		} {
			st.Snap = Snapshot{Phase: phase, Status: Status{Country: "US"}}
			if findings := lint.Tree(ConnectionsTree(st), v1.ViewPanel, 460, 404); len(findings) > 0 {
				t.Fatalf("phase %v: %v", phase, findings)
			}
		}
	}
}
