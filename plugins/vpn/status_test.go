package vpn

import (
	"strings"
	"testing"
)

func TestMullvadStatus(t *testing.T) {
	cases := []struct {
		raw     string
		phase   Phase
		traffic TrafficState
	}{
		{`{"state":"disconnected","details":{"locked_down":false}}`, Disconnected, TrafficNotBlocked},
		{`{"state":"disconnected","details":{"locked_down":true}}`, Disconnected, TrafficBlocked},
		{`{"state":"connecting","details":{}}`, Connecting, TrafficUnknown},
		{`{"state":"connected","details":{}}`, Connected, TrafficUnknown},
		{`{"state":"disconnecting","details":"reconnect"}`, Disconnecting, TrafficUnknown},
		{`{"state":"error","details":{"cause":{"reason":"auth_failed"},"block_failure":null}}`, TunnelError, TrafficBlocked},
		{`{"state":"error","details":{"block_failure":{"type":"generic"}}}`, TunnelError, TrafficNotBlocked},
		{`{"state":"error","details":{}}`, TunnelError, TrafficUnknown},
	}
	for _, c := range cases {
		got, err := ParseMullvadStatus([]byte(c.raw))
		if err != nil || got.Phase != c.phase || got.Traffic != c.traffic {
			t.Errorf("%s: %+v, %v", c.raw, got, err)
		}
	}
	raw := `{"state":"connected","details":{"location":{"city":"Stockholm","country":"Sweden","hostname":"se-sto-wg-001","ipv4":"198.51.100.2"},"endpoint":{"tunnel_type":"wireguard"}}}`
	got, err := ParseMullvadStatus([]byte(raw))
	if err != nil || got.City != "Stockholm" || got.Exit != "se-sto-wg-001" || got.IP != "198.51.100.2" || got.Transport != "wireguard" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, warning := range []string{"Warning: You are not logged in to an account.", "Warning: This device has been revoked."} {
		got, err := ParseMullvadStatus([]byte(warning + "\n" + raw))
		if err != nil || got.Phase != Connected || got.Detail == "" {
			t.Fatalf("warning lost: %+v %v", got, err)
		}
	}
}

func TestObscuraStatus(t *testing.T) {
	cases := []struct {
		raw   string
		phase Phase
	}{
		{`{"vpnStatus":{"disconnected":{}}}`, Disconnected},
		{`{"vpnStatus":{"connecting":{"connectError":"apiUnreachable"}}}`, Connecting},
		{`{"vpnStatus":{"connected":{}}}`, Connected},
	}
	for _, c := range cases {
		got, err := ParseObscuraStatus([]byte(c.raw))
		if err != nil || got.Phase != c.phase {
			t.Errorf("%s: %+v %v", c.raw, got, err)
		}
	}
	raw := `{"accountId":"secret-account","vpnStatus":{"connected":{"exit":{"id":"NYC-001","city_name":"New York","country_code":"US"},"relay":{"id":"entry-1","ip_v4":"192.0.2.1"},"transport":"quic"}}}`
	got, err := ParseObscuraStatus([]byte(raw))
	if err != nil || got.City != "New York" || got.Country != "US" || got.Exit != "NYC-001" || got.Entry != "entry-1" || got.IP != "" {
		t.Fatalf("entry address confused with exit: %+v %v", got, err)
	}
	if strings.Contains(got.Detail, "secret-account") {
		t.Fatal("account leaked")
	}
}

func TestStatusRejectsUnknownFormats(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"state":"new-state"}`, `{"state":null}`, `{"state":"connected","details":null}`, `{"state":"connected"} {}`, "Warning: unexpected\n{}"} {
		if _, err := ParseMullvadStatus([]byte(raw)); err == nil {
			t.Errorf("Mullvad accepted %s", raw)
		}
	}
	for _, raw := range []string{`{}`, `null`, `{"vpnStatus":null}`, `{"vpnStatus":{"new":{}}}`, `{"vpnStatus":{"connected":null}}`, `{"vpnStatus":{"connected":{},"disconnected":{}}}`, `{"vpnStatus":{"disconnected":{}}} {}`} {
		if _, err := ParseObscuraStatus([]byte(raw)); err == nil {
			t.Errorf("Obscura accepted %s", raw)
		}
	}
}

const obscuraCatalogueFixture = `{"version":"1","last_updated":1,"value":{"exits":[
{"id":"mullvad-au-mel-wg-403","country_code":"au","city_code":"mel","city_name":"Melbourne","provider_id":"x"},
{"id":"mullvad-au-mel-wg-401","country_code":"au","city_code":"mel","city_name":"Melbourne","provider_id":"x"},
{"id":"mullvad-se-sto-wg-001","country_code":"se","city_code":"sto","city_name":"Stockholm","provider_id":"x"},
{"id":"mullvad-xq-abc-wg-001","country_code":"xq","city_code":"abc","city_name":"Nowhere","provider_id":"x"}]}}`

func TestObscuraCatalogueAndConnectArgs(t *testing.T) {
	locs, err := ParseObscuraLocations([]byte(obscuraCatalogueFixture))
	if err != nil || len(locs) != 4 {
		t.Fatalf("%v %+v", err, locs)
	}
	if locs[0].CountryName != "Australia" || locs[0].CityName != "Melbourne" || locs[3].CountryName != "XQ" {
		t.Fatalf("names %+v", locs)
	}
	for id, want := range map[string]string{
		"au::":                         "connect --country AU",
		"au:mel:":                      "connect --country AU --city mel",
		"se:sto:mullvad-se-sto-wg-001": "connect --exit mullvad-se-sto-wg-001",
	} {
		args, err := ObscuraConnectArgs(id, locs)
		if err != nil || strings.Join(args, " ") != want {
			t.Errorf("%s: %v %v", id, args, err)
		}
	}
	if _, err := ObscuraConnectArgs("--exit=evil::", locs); err == nil {
		t.Error("a location outside the catalogue reached the CLI")
	}
	for _, bad := range []string{
		`{"value":{"exits":[{"id":"--evil","country_code":"au","city_code":"mel","city_name":"M"}]}}`,
		`{"value":{"exits":[{"id":"ok-1","country_code":"AUS","city_code":"mel","city_name":"M"}]}}`,
	} {
		if _, err := ParseObscuraLocations([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
