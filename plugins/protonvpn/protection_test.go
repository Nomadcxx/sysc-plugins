package protonvpn

import (
	"testing"

	lint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestProtectionRows(t *testing.T) {
	root := ProtectionTree(ProtectionState{
		Snap: Snapshot{
			Phase:  PhaseConnected,
			Config: Config{KillSwitch: "standard", NetShield: "malware-only", PortForwarding: true},
		},
		Port:        51820,
		HasCopyTool: true,
	})
	if ks := findNode(t, root, "ks"); ks.Text != "On" {
		t.Fatalf("kill switch %q", ks.Text)
	}
	if ns := findNode(t, root, "ns:malware-only"); ns.Fill != "accent" {
		t.Fatalf("netshield fill %q", ns.Fill)
	}
	if pf := findNode(t, root, "pf"); pf.Text != "On" {
		t.Fatalf("port forwarding %q", pf.Text)
	}
	assertTextContains(t, root, "Active port: 51820")
	findNode(t, root, "copy-port")
}

func TestProtectionKillSwitchLockedWhileConnected(t *testing.T) {
	root := ProtectionTree(ProtectionState{
		Snap: Snapshot{Phase: PhaseConnected, Config: Config{KillSwitch: "standard"}},
	})
	if ks := findNode(t, root, "ks"); !ks.Disabled {
		t.Fatal("kill switch editable while connected")
	}
}

func TestProtectionPortRowHiddenWhenDisconnected(t *testing.T) {
	root := ProtectionTree(ProtectionState{
		Snap: Snapshot{Phase: PhaseDisconnected, Config: Config{PortForwarding: true}},
		Port: 51820,
	})
	if lookupNode(root, "copy-port") != nil {
		t.Fatal("copy button shown while disconnected")
	}
}

func TestProtectionCopyHiddenWithoutTool(t *testing.T) {
	root := ProtectionTree(ProtectionState{
		Snap: Snapshot{Phase: PhaseConnected, Config: Config{PortForwarding: true}},
		Port: 51820,
	})
	if lookupNode(root, "copy-port") != nil {
		t.Fatal("copy button without a copy tool")
	}
}

func TestProtectionSplitTunnelManagedInProtonApp(t *testing.T) {
	root := ProtectionTree(ProtectionState{Snap: Snapshot{Config: Config{KillSwitch: "standard"}}})
	assertTextContains(t, root, "Manage split tunneling in the Proton VPN app")
	for _, id := range []string{"st", "app-query"} {
		if lookupNode(root, id) != nil {
			t.Fatalf("plugin still exposes split-tunnel editor control %q", id)
		}
	}
}

func TestProtectionLint(t *testing.T) {
	for _, ks := range []string{"off", "standard"} {
		for _, pf := range []bool{false, true} {
			root := ProtectionTree(ProtectionState{
				Snap: Snapshot{Config: Config{KillSwitch: ks, PortForwarding: pf}},
			})
			if findings := lint.Tree(root, v1.ViewPanel, 460, 404); len(findings) > 0 {
				t.Fatalf("ks %s pf %v: %v", ks, pf, findings)
			}
		}
	}
	// The richest state: connected with a forwarded port and the copy control.
	root := ProtectionTree(ProtectionState{
		Snap:        Snapshot{Phase: PhaseConnected, Config: Config{KillSwitch: "off", PortForwarding: true}},
		Port:        51820,
		HasCopyTool: true,
	})
	if findings := lint.Tree(root, v1.ViewPanel, 460, 404); len(findings) > 0 {
		t.Fatalf("connected: %v", findings)
	}
}
