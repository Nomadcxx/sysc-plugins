package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("SYSC_VPN_HELPER"); mode != "" {
		if path := os.Getenv("SYSC_VPN_ARGS"); path != "" {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(2)
			}
			_ = json.NewEncoder(f).Encode(os.Args[1:])
			_ = f.Close()
		}
		switch mode {
		case "args":
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[1:])
		case "locale":
			fmt.Print(os.Getenv("LC_ALL") + "/" + os.Getenv("LANG") + "/" + os.Getenv("NO_COLOR"))
		case "fail":
			fmt.Fprint(os.Stderr, "secret-account 1234567890123456")
			os.Exit(7)
		case "slow":
			time.Sleep(10 * time.Second)
		case "overflow":
			fmt.Print(strings.Repeat("x", 4096))
		case "status":
			fmt.Print(`{"state":"disconnected","details":{"locked_down":false}}`)
		case "connected":
			fmt.Print(`{"state":"connected","details":{}}`)
		case "obscura-new":
			switch strings.Join(os.Args[1:], " ") {
			case "--help":
				fmt.Print("Commands:\n  connect       Connect to the VPN\n  status        Show account and VPN status\n  locations     Show the exit catalogue\n  auto-connect  Show or change the automatic connection\n  lan           Show or change the access to the local network\n  dns           Show or change the DNS mode and the content blockers\n")
			case "status --help":
				fmt.Print("Options:\n      --json        Print full JSON status\n      --local       Don't refresh the account information\n")
			case "connect --help":
				fmt.Print("Options:\n      --country <COUNTRY>  Connect to an exit in this country\n      --city <CITY>\n      --exit <EXIT>\n")
			case "status --local --json":
				fmt.Print(obscuraSettingsFixture)
			}
		case "settings":
			if len(os.Args) >= 3 && os.Args[2] == "get" {
				switch os.Args[1] {
				case "dns":
					if os.Getenv("SYSC_VPN_CUSTOM_DNS") != "" {
						fmt.Print("Custom DNS: yes\nServers:\n1.1.1.1\n")
					} else {
						fmt.Print(dnsFixture)
					}
				case "auto-connect":
					fmt.Print("Autoconnect: on\n")
				case "lan":
					fmt.Print("Local network sharing setting: block\n")
				case "lockdown-mode":
					fmt.Print("Block traffic when the VPN is disconnected: off\n")
				case "relay":
					fmt.Print("Generic constraints\nLocation: any\n")
				}
			} else if len(os.Args) >= 3 && os.Args[1] == "relay" && os.Args[2] == "list" {
				fmt.Print(relayFixture)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helperCLI(t *testing.T, mode string) CLI {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYSC_VPN_HELPER", mode)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	return CLI{MullvadBin: exe, ObscuraBin: exe}
}

func TestCLIArgumentsAndLocale(t *testing.T) {
	c := helperCLI(t, "args")
	for _, p := range []Provider{Mullvad, Obscura} {
		out, err := c.run(context.Background(), p, time.Second, 1024, "status", "--json")
		var args []string
		if err != nil || json.Unmarshal(out, &args) != nil || strings.Join(args, " ") != "status --json" {
			t.Fatalf("%s %s %v", p, out, err)
		}
	}
	log := filepath.Join(t.TempDir(), "args")
	t.Setenv("SYSC_VPN_ARGS", log)
	for _, p := range []Provider{Mullvad, Obscura} {
		if err := c.Connect(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if err := c.Disconnect(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(log)
	if err != nil || strings.Count(string(b), `["connect"]`) != 2 || strings.Count(string(b), `["disconnect"]`) != 2 {
		t.Fatalf("%s %v", b, err)
	}
	t.Setenv("SYSC_VPN_HELPER", "locale")
	t.Setenv("LC_ALL", "xx")
	t.Setenv("LANG", "yy")
	t.Setenv("NO_COLOR", "0")
	out, err := c.run(context.Background(), Mullvad, time.Second, 1024, "status")
	if err != nil || string(out) != "C/C/1" {
		t.Fatalf("%s %v", out, err)
	}
}

func TestCLIFailuresAreBoundedAndPrivate(t *testing.T) {
	c := helperCLI(t, "fail")
	_, err := c.run(context.Background(), Mullvad, time.Second, 1024, "status")
	if err == nil || strings.Contains(err.Error(), "123456") || strings.Contains(err.Error(), "secret-account") {
		t.Fatalf("%v", err)
	}
	t.Setenv("SYSC_VPN_HELPER", "overflow")
	if _, err := c.run(context.Background(), Mullvad, time.Second, 64, "status"); err == nil {
		t.Fatal("accepted overflow")
	}
	t.Setenv("SYSC_VPN_HELPER", "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := c.run(ctx, Mullvad, time.Second, 1024, "status"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("child was not reaped promptly")
	}
	t.Setenv("SYSC_VPN_HELPER", "args")
	if _, err := c.run(context.Background(), Provider("other"), time.Second, 1024, "status"); err == nil {
		t.Fatal("unknown provider")
	}
	if _, err := (CLI{}).Status(context.Background(), Mullvad); err == nil {
		t.Fatal("missing CLI")
	}
}

func TestCLIStatusUsesParser(t *testing.T) {
	c := helperCLI(t, "status")
	got, err := c.Status(context.Background(), Mullvad)
	if err != nil || got.Phase != Disconnected {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCLISettingsPreserveDNSAndValidateWrites(t *testing.T) {
	c := helperCLI(t, "settings")
	path := filepath.Join(t.TempDir(), "args")
	t.Setenv("SYSC_VPN_ARGS", path)
	got, err := c.Settings(context.Background(), Mullvad)
	if err != nil || got.LAN == nil || *got.LAN || got.AutoConnect == nil || !*got.AutoConnect {
		t.Fatalf("%+v %v", got, err)
	}
	if err := c.Set(context.Background(), Mullvad, "dns:1", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `["dns","set","default","--block-ads","--block-trackers","--block-gambling"]`) {
		t.Fatalf("%s", b)
	}
	_ = os.WriteFile(path, nil, 0600)
	for _, key := range []string{"bad", "--reset", "dns:-1", "dns:6"} {
		if err := c.Set(context.Background(), Mullvad, key, true); err == nil {
			t.Fatalf("accepted %s", key)
		}
	}
	if err := c.Set(context.Background(), Obscura, "lan", true); err == nil {
		t.Fatal("Obscura mutation")
	}
	t.Setenv("SYSC_VPN_CUSTOM_DNS", "1")
	if err := c.Set(context.Background(), Mullvad, "dns:0", true); err == nil {
		t.Fatal("custom DNS mutation")
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), `"set"`) {
		t.Fatalf("unsafe write: %s", b)
	}
}

func TestMutationPreflightAndNoRetry(t *testing.T) {
	c := helperCLI(t, "connected")
	path := filepath.Join(t.TempDir(), "args")
	t.Setenv("SYSC_VPN_ARGS", path)
	if err := c.Mutate(context.Background(), Obscura, Action{Kind: "connect"}, nil); err == nil {
		t.Fatal("connected over Mullvad")
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `["connect"]`) {
		t.Fatalf("mutation before preflight: %s", b)
	}
	_ = os.WriteFile(path, nil, 0600)
	if err := c.Mutate(context.Background(), Mullvad, Action{Kind: "location", Location: "--bad"}, nil); err == nil {
		t.Fatal("invalid location")
	}
	b, _ = os.ReadFile(path)
	if len(b) != 0 {
		t.Fatalf("invalid action reached CLI: %s", b)
	}
	c.ObscuraBin = ""
	t.Setenv("SYSC_VPN_HELPER", "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Mutate(ctx, Mullvad, Action{Kind: "connect"}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	b, _ = os.ReadFile(path)
	if strings.Count(string(b), `["connect"]`) > 1 {
		t.Fatalf("retried mutation: %s", b)
	}
}

// While disconnected a location choice also connects, the way the Mullvad
// app does on tap; while connected relay set location alone moves the tunnel.
func TestLocationConnectsOnlyWhenAsked(t *testing.T) {
	c := helperCLI(t, "args")
	c.ObscuraBin = ""
	locations, _ := ParseMullvadRelays(relayFixture)
	for _, connect := range []bool{true, false} {
		path := filepath.Join(t.TempDir(), "args")
		t.Setenv("SYSC_VPN_ARGS", path)
		if err := c.Mutate(context.Background(), Mullvad, Action{Kind: "location", Location: "se::", Connect: connect}, locations); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		want := `["relay","set","location","se"]` + "\n"
		if connect {
			want += `["connect"]` + "\n"
		}
		if string(b) != want {
			t.Fatalf("connect=%v ran %s", connect, b)
		}
	}
}

const obscuraSettingsFixture = `{"vpnStatus":{"disconnected":{}},"autoConnect":false,"localNetworkAccess":true,"useSystemDns":false,"tailscaleBypass":false,"wireguardBypass":true,"dnsContentBlock":{"ad":true,"tracker":false,"malware":false,"adult":false,"gambling":false,"socialMedia":false}}`

// The laptop's Obscura build adds settings, location targets and a local
// status read; the upstream CLI has none, so support is probed, not assumed.
func TestObscuraProbeAndWrites(t *testing.T) {
	c := helperCLI(t, "obscura-new")
	c.MullvadBin = ""
	c.Obscura = c.ProbeObscura(context.Background())
	if c.Obscura != (ObscuraCaps{Settings: true, Locations: true, Local: true}) {
		t.Fatalf("caps %+v", c.Obscura)
	}
	path := filepath.Join(t.TempDir(), "args")
	t.Setenv("SYSC_VPN_ARGS", path)
	if _, err := c.Status(context.Background(), Obscura); err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		key string
		on  bool
	}{{"auto-connect", true}, {"lan", false}, {"dns:2", true}} {
		if err := c.Set(context.Background(), Obscura, w.key, w.on); err != nil {
			t.Fatal(w.key, err)
		}
	}
	b, _ := os.ReadFile(path)
	want := `["status","--local","--json"]` + "\n" +
		`["auto-connect","set","on"]` + "\n" +
		`["lan","set","block"]` + "\n" +
		`["status","--local","--json"]` + "\n" +
		`["dns","block","set","--ads","on","--trackers","off","--malware","on","--adult","off","--gambling","off","--social-media","off"]` + "\n"
	if string(b) != want {
		t.Fatalf("ran\n%s", b)
	}
	if err := c.Set(context.Background(), Obscura, "tailscale", true); err == nil {
		t.Fatal("bypass has no CLI and must be refused")
	}
	old := helperCLI(t, "args")
	old.MullvadBin = ""
	if caps := old.ProbeObscura(context.Background()); caps != (ObscuraCaps{}) {
		t.Fatalf("upstream CLI caps %+v", caps)
	}
	if err := old.Set(context.Background(), Obscura, "lan", true); err == nil {
		t.Fatal("write to a CLI without settings support")
	}
}

func TestObscuraLocationIsOneConnect(t *testing.T) {
	c := helperCLI(t, "obscura-new")
	c.MullvadBin = ""
	c.Obscura = ObscuraCaps{Locations: true}
	locs, _ := ParseObscuraLocations([]byte(obscuraCatalogueFixture))
	path := filepath.Join(t.TempDir(), "args")
	t.Setenv("SYSC_VPN_ARGS", path)
	if err := c.Mutate(context.Background(), Obscura, Action{Kind: "location", Location: "au:mel:", Connect: true}, locs); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != `["connect","--country","AU","--city","mel"]`+"\n" {
		t.Fatalf("ran %s", b)
	}
	c.Obscura = ObscuraCaps{}
	if err := c.Mutate(context.Background(), Obscura, Action{Kind: "location", Location: "au:mel:"}, locs); err == nil {
		t.Fatal("location on a CLI without connect targets")
	}
}
