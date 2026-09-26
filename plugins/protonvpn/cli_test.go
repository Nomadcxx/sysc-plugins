package protonvpn

import (
	"os"
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
