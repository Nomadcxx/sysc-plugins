package protonvpn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	connected, _ := os.ReadFile("testdata/status-connected.txt")
	s, err := ParseStatus(string(connected))
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != PhaseConnected || s.Server != "US-NY#1" || s.Location != "New York, United States" ||
		s.Country != "US" || s.Load != 30 || s.Protocol != "wireguard" {
		t.Fatalf("got %+v", s)
	}
	disc, _ := os.ReadFile("testdata/status-disconnected.txt")
	s, err = ParseStatus(string(disc))
	if err != nil || s.Phase != PhaseDisconnected {
		t.Fatalf("got %+v err %v", s, err)
	}
}

func TestParseStatusGarbageIsAnError(t *testing.T) {
	if _, err := ParseStatus("hello world\nno keys here"); err == nil {
		t.Fatal("expected error on unparseable status")
	}
}

func TestParseStatusErrors(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
	}{
		{"unknown status value", "Status: Weird\n"},
		{"missing status key", "Server: US-NY#1\nLoad: 30%\n"},
		{"bad load value", "Status: connected\nLoad: soon%\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseStatus(tt.stdout); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseInfoMissingAccountIsAnError(t *testing.T) {
	if _, err := ParseInfo("Plan: free\nTier: 0\n"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseConfigEdgeCases(t *testing.T) {
	t.Run("upgrade to enable maps to off", func(t *testing.T) {
		out := "Setting                  Value\n-----------------------  ------------\n" +
			"netshield                Upgrade to enable\nkill-switch              off\nport-forwarding          off\n"
		c, err := ParseConfig(out)
		if err != nil {
			t.Fatal(err)
		}
		if c.NetShield != "off" {
			t.Fatalf("got %+v", c)
		}
	})
	t.Run("missing row is an error", func(t *testing.T) {
		for _, missing := range []string{"kill-switch", "netshield", "port-forwarding"} {
			rows := []string{"netshield                 malware-only", "kill-switch               standard", "port-forwarding           on"}
			var kept []string
			for _, r := range rows {
				if !strings.HasPrefix(r, missing) {
					kept = append(kept, r)
				}
			}
			out := "Setting                   Value\n------------------------  ----------------------\n" + strings.Join(kept, "\n") + "\n"
			if _, err := ParseConfig(out); err == nil {
				t.Fatalf("expected error when %q row missing", missing)
			}
		}
	})
}

func TestConnectArgs(t *testing.T) {
	tests := []struct {
		target string
		want   []string
	}{
		{"", []string{"connect"}},
		{"random", []string{"connect", "--random"}},
		{"p2p", []string{"connect", "--p2p"}},
		{"tor", []string{"connect", "--tor"}},
		{"US", []string{"connect", "--country", "US"}},
		{"US-NY#1", []string{"connect", "US-NY#1"}},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := connectArgs(tt.target)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("connectArgs(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestCLITypedMethodsWrapStderr(t *testing.T) {
	dir := t.TempDir()
	writeBin := func(name, script string) string {
		bin := filepath.Join(dir, name)
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	failing := writeBin("fake-protonvpn", "#!/bin/sh\necho '[!] Authentication denied.' >&2\nexit 1\n")
	silent := writeBin("silent-protonvpn", "#!/bin/sh\nexit 1\n")

	c := &CLI{Bin: failing}
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"Status": func() error { _, err := c.Status(ctx); return err },
		"Info":   func() error { _, err := c.Info(ctx); return err },
		"Config": func() error { _, err := c.Config(ctx); return err },
	} {
		if err := run(); err == nil || !strings.HasSuffix(err.Error(), "[!] Authentication denied.") {
			t.Fatalf("%s err = %v, want wrapped stderr detail", name, err)
		}
	}

	c = &CLI{Bin: silent}
	if _, err := c.Status(ctx); err == nil || strings.Contains(err.Error(), ": ") {
		t.Fatalf("Status err = %v, want bare error when stderr empty", err)
	}
}

func TestParseInfoAndConfig(t *testing.T) {
	info, _ := os.ReadFile("testdata/info.txt")
	i, err := ParseInfo(string(info))
	if err != nil || i.Username != "jane@example.com" {
		t.Fatalf("info %+v err %v", i, err)
	}
	cfg, _ := os.ReadFile("testdata/config-list.txt")
	c, err := ParseConfig(string(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if c.KillSwitch != "standard" || c.NetShield != "malware-only" || !c.PortForwarding {
		t.Fatalf("config %+v", c)
	}
}

func TestParseConnectIP(t *testing.T) {
	out, _ := os.ReadFile("testdata/connect-success.txt")
	if got := ParseConnectIP(string(out)); got != "198.51.100.7" {
		t.Fatalf("got %q", got)
	}
	if got := ParseConnectIP("no ip here"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestErrorDetail(t *testing.T) {
	b, _ := os.ReadFile("testdata/connect-error.txt")
	if got := ErrorDetail(string(b)); got != "[!] Authentication denied. Please check your credentials." {
		t.Fatalf("got %q", got)
	}
	if got := ErrorDetail(""); got != "" {
		t.Fatalf("got %q", got)
	}
}
