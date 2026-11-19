package vpn

import (
	"path/filepath"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/vpn/screenshot.png when CAPTURE=1: Mullvad
// connected through Zurich on the home screen, with Obscura installed beside
// it so the provider switch shows both wordmarks.
func TestCapturePanel(t *testing.T) {
	relays := "Switzerland (ch)\n\tZurich (zrh) @ x\n\t\tch-zrh-wg-004 (192.0.2.4) - hosted by Example (rented)\n" +
		"Sweden (se)\n\tStockholm (sto) @ x\n\t\tse-sto-wg-001 (192.0.2.1) - hosted by Example (rented)\n"
	locations, err := ParseMullvadRelays(relays)
	if err != nil {
		t.Fatal(err)
	}
	brand, err := filepath.Abs("assets/brand")
	if err != nil {
		t.Fatal(err)
	}
	m, now := readyMachine(t)
	st := m.State(Mullvad)
	st.Observed = Observation{Phase: Connected, City: "Zurich", Country: "Switzerland", Exit: "ch-zrh-wg-004", IP: "203.0.113.42", Transport: "WireGuard"}
	m.Providers[Mullvad] = st
	on, off := true, false
	capture.Panel(t, "vpn", Panel(ViewState{
		Machine: m, Now: now, Locations: locations, LocationsLoaded: true, BrandDir: brand, SettingsLoaded: true,
		Settings: Settings{AutoConnect: &on, LAN: &off, Lockdown: &off, DNS: &DNS{Mode: "default", Blocks: [6]bool{true, true, true}}},
	}))
}
