package protonvpn

import (
	"fmt"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// TestPinEndRowsHonorTheTwoChildContract walks every view the plugin can
// publish. The shell's layout engine reserves the trailing child's width
// only for a [leading, trailing] PinEnd row (layout.go), yet it stretches
// any zero-width column inside a PinEnd row to the full remainder
// regardless of sibling count, so a third child is pushed out of the box
// and the shell rejects the whole view with "does not fit". The lint
// mirror does not reproduce the stretch, so the shape is asserted here.
func TestPinEndRowsHonorTheTwoChildContract(t *testing.T) {
	var walk func(n *v1.Node, path string)
	walk = func(n *v1.Node, path string) {
		if n == nil {
			return
		}
		if n.Kind == v1.KindRow && n.PinEnd && len(n.Children) != 2 {
			t.Errorf("%s: PinEnd row has %d children", path, len(n.Children))
		}
		for i, c := range n.Children {
			walk(c, fmt.Sprintf("%s.children[%d]", path, i))
		}
	}
	countries := make([]Country, 0, 25)
	for i := 0; i < 25; i++ {
		servers := make([]Server, 0, 25)
		for s := 0; s < 25; s++ {
			servers = append(servers, Server{
				Name: fmt.Sprintf("US-LONG-NAME-%02d#%d", i, s), City: "New York", Load: 50,
				Up: s%3 != 0, Features: FeatP2P | FeatSecureCore,
			})
		}
		code := fmt.Sprintf("X%c", 'A'+i%26)
		if i == 0 {
			code = "US"
		}
		countries = append(countries, Country{
			Code: code, Name: code + " United States of America", Load: 97, Servers: servers,
		})
	}
	snap := Snapshot{Phase: PhaseConnected, IP: "198.51.100.7", Port: 51820,
		Status: Status{Server: "US-NY#1", Country: "US", Protocol: "wireguard"},
		Config: Config{KillSwitch: "standard", PortForwarding: true}}
	states := []PanelState{
		{Snap: snap, Tab: "connections", HasCLI: true, Traffic: true,
			Conns: ConnectionsState{Countries: countries, Expanded: "US", Flags: true, Page: 1}},
		{Snap: snap, Tab: "protection", HasCLI: true,
			Prot: ProtectionState{Apps: []string{"/usr/bin/x11vnc", "/usr/bin/tigervnc"},
				SplitTunnel: true, Port: 41772, HasCopyTool: true,
				Candidates: []App{{Value: "/usr/bin/tigervnc", Label: "TigerVNC"}}}},
		{Snap: snap, Tab: "account", HasCLI: true,
			Acct: AccountState{SignedIn: true, Settings: map[string]string{"refresh_seconds": "60"}}},
	}
	for _, s := range states {
		walk(Panel(s), "panel:"+s.Tab)
	}
	for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
		b := BarState{Snap: Snapshot{Phase: phase, Status: Status{Country: "US", Server: "US-NY#1"}}, Mode: "status"}
		walk(Bar(b), "bar")
		walk(Tooltip(b), "tooltip")
	}
}
