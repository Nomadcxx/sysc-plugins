package vpn

import (
	"reflect"
	"testing"
)

const relayFixture = "Sweden (se)\n\tStockholm (sto) @ 59.3°N, 18.0°W\n\t\tse-sto-wg-001 (192.0.2.1, 2001:db8::1) - hosted by Test (Mullvad-owned)\n\t\tse-sto-wg-002 (192.0.2.2) - hosted by Test (rented) - DAITA\nAustralia (au)\n\tMelbourne (mel) @ 37.8°S, 144.9°E\n\t\tau-mel-wg-001 (192.0.2.3) - hosted by Test (rented)\n"
const dnsFixture = "Custom DNS: no\nBlock ads: true\nBlock trackers: false\nBlock malware: false\nBlock adult content: false\nBlock gambling: true\nBlock social media: false\n"

func TestMullvadRelayArguments(t *testing.T) {
	locations, err := ParseMullvadRelays(relayFixture)
	if err != nil || len(locations) != 3 {
		t.Fatalf("%+v %v", locations, err)
	}
	for id, want := range map[string][]string{
		"se::":                 {"relay", "set", "location", "se"},
		"se:sto:":              {"relay", "set", "location", "se", "sto"},
		"se:sto:se-sto-wg-001": {"relay", "set", "location", "se", "sto", "se-sto-wg-001"},
	} {
		got, err := LocationArgs(id, locations)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v %v", id, got, err)
		}
	}
	for _, id := range []string{"--reset", "se:mel:", "au:sto:se-sto-wg-001", "xx::", "se:sto:--option", "se:sto:se-sto-wg-001\n", "se"} {
		if _, err := LocationArgs(id, locations); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	for _, raw := range []string{"garbage", "\t\tse-sto-wg-001 (192.0.2.1) - hosted by Test (rented)", "Sweden (se)\n\tStockholm (sto) @ x\n\t\tau-mel-wg-001 (192.0.2.1) - hosted by Test (rented)"} {
		if _, err := ParseMullvadRelays(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestDNSPreservesAllBlockers(t *testing.T) {
	dns, err := ParseMullvadDNS(dnsFixture)
	if err != nil || dns.Mode != "default" || !dns.Blocks[0] || !dns.Blocks[4] {
		t.Fatalf("%+v %v", dns, err)
	}
	args, err := DNSArgs(dns, 1, true)
	if err != nil || !reflect.DeepEqual(args, []string{"dns", "set", "default", "--block-ads", "--block-trackers", "--block-gambling"}) {
		t.Fatalf("%v %v", args, err)
	}
	custom, err := ParseMullvadDNS("Custom DNS: yes\nServers:\n1.1.1.1\n2001:db8::1\n")
	if err != nil || custom.Mode != "custom" {
		t.Fatalf("%+v %v", custom, err)
	}
	if _, err := DNSArgs(custom, 0, true); err == nil {
		t.Fatal("overwrote custom DNS")
	}
	for _, raw := range []string{"Custom DNS: no\nBlock ads: true", dnsFixture + "Unknown blocker: true\n", "Custom DNS: yes\nServers:\nnot-an-IP"} {
		if _, err := ParseMullvadDNS(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, i := range []int{-1, 6} {
		if _, err := DNSArgs(dns, i, true); err == nil {
			t.Fatal("invalid blocker index")
		}
	}
}

func TestMullvadSettingsPolicyParsing(t *testing.T) {
	if value, err := ParsePolicy("Autoconnect: on\n", "Autoconnect", "on", "off"); err != nil || !value {
		t.Fatalf("%v %v", value, err)
	}
	if _, err := ParsePolicy("Autoconnect: perhaps", "Autoconnect", "on", "off"); err == nil {
		t.Fatal("guessed unknown value")
	}
}

func TestObscuraSettings(t *testing.T) {
	s, err := ParseObscuraSettings([]byte(`{"vpnStatus":{"disconnected":{}},"autoConnect":true,"localNetworkAccess":false,"useSystemDns":true,"dnsContentBlock":{"ad":true,"tracker":false,"malware":false,"adult":false,"gambling":true,"socialMedia":false}}`))
	if err != nil || s.AutoConnect == nil || !*s.AutoConnect || s.LAN == nil || *s.LAN || s.DNS == nil || s.DNS.Mode != "system" || !s.DNS.Blocks[4] {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = ParseObscuraSettings([]byte(`{"vpnStatus":{"disconnected":{}}}`))
	if err != nil || s.AutoConnect != nil || s.DNS != nil {
		t.Fatalf("missing values treated as off: %+v %v", s, err)
	}
}

// Formats follow mullvad-cli print_option and GeographicLocationConstraint's
// Display: "{:<4}{:<24}{value}".
func TestParseSavedLocation(t *testing.T) {
	line := func(v string) string {
		return "Generic constraints\n    Location:               " + v + "\n    Provider(s):            any\n"
	}
	for raw, want := range map[string]string{
		line("country se"):                           "se::",
		line("city sto, se"):                         "se:sto:",
		line("city sto, se, hostname se-sto-wg-001"): "se:sto:se-sto-wg-001",
		line("any"):                                  "",
		line("My travel list"):                       "",
		"Custom endpoint: 192.0.2.1:51820":           "",
	} {
		if got := ParseSavedLocation(raw); got != want {
			t.Errorf("%q: got %q want %q", raw, got, want)
		}
	}
}
