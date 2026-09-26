package protonvpn

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestProtectionSplitTunnelBlockedByKillSwitch(t *testing.T) {
	root := ProtectionTree(ProtectionState{Snap: Snapshot{Config: Config{KillSwitch: "standard"}}})
	if st := findNode(t, root, "st"); !st.Disabled {
		t.Fatal("split tunnel editable under kill switch")
	}
	assertTextContains(t, root, "Disable kill switch to use split tunneling")
}

func TestProtectionAppRowsAndSuggestions(t *testing.T) {
	root := ProtectionTree(ProtectionState{
		SplitTunnel: true,
		Apps:        []string{"/usr/bin/chromium"},
		Candidates:  []App{{"/usr/bin/firefox", "Firefox"}, {"/usr/bin/chromium", "Chromium"}},
		AppQuery:    "fire",
	})
	assertTextContains(t, root, "Chromium")
	findNode(t, root, "del-app:/usr/bin/chromium")
	findNode(t, root, "app-suggest:/usr/bin/firefox")
	if lookupNode(root, "app-suggest:/usr/bin/chromium") != nil {
		t.Fatal("already-added app suggested")
	}
}

func TestSplitTunnelPreservesUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	before := `{"features":{"split_tunneling":{"enabled":false,"apps":[]}},"other":{"keep":true}}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteSplitTunnel(path, true, []string{"/usr/bin/firefox"}); err != nil {
		t.Fatal(err)
	}
	enabled, apps, err := ReadSplitTunnel(path)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled || len(apps) != 1 || apps[0] != "/usr/bin/firefox" {
		t.Fatalf("enabled=%v apps=%v", enabled, apps)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	other, ok := doc["other"].(map[string]any)
	if !ok || other["keep"] != true {
		t.Fatalf("unknown keys lost: %v", doc["other"])
	}
}

func TestSplitTunnelMalformedFileFailsLoud(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteSplitTunnel(path, true, nil); err == nil {
		t.Fatal("malformed settings.json silently rewritten")
	}
}

func TestProtectionLint(t *testing.T) {
	for _, ks := range []string{"off", "standard"} {
		for _, pf := range []bool{false, true} {
			for _, st := range []bool{false, true} {
				root := ProtectionTree(ProtectionState{
					Snap:        Snapshot{Config: Config{KillSwitch: ks, PortForwarding: pf}},
					SplitTunnel: st,
				})
				if findings := lint.Tree(root, v1.ViewPanel, 460, 404); len(findings) > 0 {
					t.Fatalf("ks %s pf %v st %v: %v", ks, pf, st, findings)
				}
			}
		}
	}
	// The richest state: connected with a forwarded port, the copy control,
	// and a populated split tunneling editor.
	root := ProtectionTree(ProtectionState{
		Snap:        Snapshot{Phase: PhaseConnected, Config: Config{KillSwitch: "off", PortForwarding: true}},
		SplitTunnel: true,
		Apps:        []string{"/usr/bin/chromium"},
		Candidates:  []App{{"/usr/bin/firefox", "Firefox"}, {"/usr/bin/chromium", "Chromium"}},
		Port:        51820,
		HasCopyTool: true,
	})
	if findings := lint.Tree(root, v1.ViewPanel, 460, 404); len(findings) > 0 {
		t.Fatalf("connected: %v", findings)
	}
}
