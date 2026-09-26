package protonvpn

import (
	"testing"

	lint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// barContent walks the tree and returns the first icon's name and tone plus
// the first text it finds, each independently.
func barContent(t *testing.T, n *v1.Node) (string, v1.Tone, string) {
	t.Helper()
	var icon, text string
	var tone v1.Tone
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if n.Kind == v1.KindIcon && icon == "" {
			icon = n.Icon
			tone = n.Tone
		}
		if n.Kind == v1.KindText && text == "" {
			text = n.Text
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(n)
	return icon, tone, text
}

func barText(t *testing.T, n *v1.Node) string {
	t.Helper()
	_, _, text := barContent(t, n)
	return text
}

func TestBarStates(t *testing.T) {
	cases := []struct {
		phase    Phase
		wantIcon string
		wantTone v1.Tone
	}{
		{PhaseDisconnected, "vpn_key_off", v1.ToneSubtle},
		{PhaseConnecting, "bolt", v1.ToneAccent},
		{PhaseConnected, "shield", v1.ToneAccent},
		{PhaseError, "gpp_bad", v1.ToneError},
	}
	for _, tc := range cases {
		n := Bar(BarState{Snap: Snapshot{Phase: tc.phase, Status: Status{Country: "US"}}, Mode: "code"})
		icon, tone, text := barContent(t, n)
		if icon != tc.wantIcon {
			t.Errorf("phase %v icon %q", tc.phase, icon)
		}
		if tone != tc.wantTone {
			t.Errorf("phase %v tone %q", tc.phase, tone)
		}
		_ = text
	}
}

func TestBarModes(t *testing.T) {
	s := BarState{Snap: Snapshot{Phase: PhaseConnected, Status: Status{Country: "US"}}, Mode: "icon"}
	if text := barText(t, Bar(s)); text != "" {
		t.Fatalf("icon mode shows %q", text)
	}
	s.Mode = "code"
	if text := barText(t, Bar(s)); text != "US" {
		t.Fatalf("code mode shows %q", text)
	}
	s.Mode = "status"
	if text := barText(t, Bar(s)); text != "Protected" {
		t.Fatalf("status mode shows %q", text)
	}
}

func TestBarConnectingShowsEllipsis(t *testing.T) {
	s := BarState{Snap: Snapshot{Phase: PhaseConnecting}, Mode: "code"}
	if text := barText(t, Bar(s)); text != "…" {
		t.Fatalf("got %q", text)
	}
}

func TestBarLintEveryState(t *testing.T) {
	for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
		for _, mode := range []string{"icon", "code", "status"} {
			root := Bar(BarState{Snap: Snapshot{Phase: phase, Status: Status{Country: "US", Server: "US-NY#1"}}, Mode: mode})
			if findings := lint.Tree(root, "bar", lint.BarWidth, lint.BarHeight); len(findings) > 0 {
				t.Fatalf("phase %v mode %s: %v", phase, mode, findings)
			}
		}
	}
}

func TestTooltipLint(t *testing.T) {
	root := Tooltip(BarState{Snap: Snapshot{Phase: PhaseConnected, IP: "198.51.100.7", Status: Status{Server: "US-NY#1", Location: "New York, United States", Country: "US", Protocol: "wireguard"}, RxRate: 1024, TxRate: 512}})
	if findings := lint.Tree(root, "tooltip", 280, 200); len(findings) > 0 {
		t.Fatalf("%v", findings)
	}
}
