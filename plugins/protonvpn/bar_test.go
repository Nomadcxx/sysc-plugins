package protonvpn

import (
	"strings"
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
		{PhaseDisconnected, "proton", v1.ToneSubtle},
		{PhaseConnecting, "proton", v1.ToneAccent},
		{PhaseDisconnecting, "proton", v1.ToneAccent},
		{PhaseConnected, "proton", v1.ToneAccent},
		{PhaseError, "proton", v1.ToneError},
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
	s.Snap.Phase = PhaseDisconnecting
	if text := barText(t, Bar(s)); text != "…" {
		t.Fatalf("disconnecting: got %q", text)
	}
}

func TestFormatRate(t *testing.T) {
	cases := []struct {
		bps  float64
		want string
	}{
		{0, "0 B/s"},
		{12, "12 B/s"},
		{999, "999 B/s"},
		{1000, "1.0 KB/s"},
		{1234, "1.2 KB/s"},
		{9999, "10.0 KB/s"},
		{10000, "10 KB/s"},
		{340000, "340 KB/s"},
		{1e6, "1.0 MB/s"},
		{1.2e6, "1.2 MB/s"},
		{1e9, "1.0 GB/s"},
	}
	for _, tc := range cases {
		if got := formatRate(tc.bps); got != tc.want {
			t.Errorf("formatRate(%v) = %q, want %q", tc.bps, got, tc.want)
		}
	}
}

func TestBarLintEveryState(t *testing.T) {
	for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
		for _, mode := range []string{"icon", "code", "status"} {
			root := Bar(BarState{Snap: Snapshot{Phase: phase, Status: Status{Country: "US", Server: "US-NY#1"}}, Mode: mode})
			if findings := lint.Tree(root, v1.ViewBar, lint.BarWidth, lint.BarHeight); len(findings) > 0 {
				t.Fatalf("phase %v mode %s: %v", phase, mode, findings)
			}
		}
	}
}

func TestBarFitsSideWidths(t *testing.T) {
	for _, mode := range []string{"icon", "code", "status"} {
		state := BarState{Snap: Snapshot{Phase: PhaseConnected, Status: Status{Country: "US", Server: "US-NY#1"}}, Mode: mode}
		for _, width := range []int{lint.BarWidth, 28, 32, 64} {
			bar := Bar(state)
			if width != lint.BarWidth {
				bar = BarAtWidth(state, width)
			}
			for _, finding := range lint.Tree(bar, v1.ViewBar, width, lint.BarHeight) {
				t.Errorf("mode %s width %d: %s", mode, width, finding)
			}
			if width <= 64 && len(bar.Children[0].Children) != 1 {
				t.Errorf("mode %s width %d retains country/status text", mode, width)
			}
			button := bar.Children[0]
			if button.ID != "bar" || button.Name != "ProtonVPN" || button.Role != "button" || len(button.Events) != 2 || button.Events[0] != v1.EventActivate || button.Events[1] != v1.EventPointer {
				t.Fatalf("mode %s width %d interaction = %+v", mode, width, button)
			}
		}
	}
}

func TestTooltipLint(t *testing.T) {
	for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
		root := Tooltip(BarState{Snap: Snapshot{Phase: phase, Err: "Tunnel setup failed", IP: "198.51.100.7", Status: Status{Server: "US-NY#1", Location: "New York, United States", Country: "US", Protocol: "wireguard"}, RxRate: 1024, TxRate: 512, Port: 51820}})
		if findings := lint.Tree(root, v1.ViewTooltip, lint.TooltipWidth, lint.TooltipHeight); len(findings) > 0 {
			t.Fatalf("phase %v: %v", phase, findings)
		}
	}
}

func TestTooltipDisconnected(t *testing.T) {
	root := Tooltip(BarState{Snap: Snapshot{Phase: PhaseDisconnected, IP: "198.51.100.7", Status: Status{Server: "US-NY#1"}}})
	var texts []string
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if n.Kind == v1.KindText {
			texts = append(texts, n.Text)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Unprotected") || !strings.Contains(joined, "Right-click to quick connect") {
		t.Fatalf("missing hint lines: %q", joined)
	}
	if strings.Contains(joined, "US-NY#1") || strings.Contains(joined, "198.51.100.7") {
		t.Fatalf("disconnected tooltip leaks server/IP: %q", joined)
	}
}
