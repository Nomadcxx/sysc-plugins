package protonvpn

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/protonvpn/screenshot.png when CAPTURE=1: a
// connected tunnel on the Connections tab with a list of exit countries.
func TestCapturePanel(t *testing.T) {
	country := func(code, name string, load int) Country {
		return Country{Code: code, Name: name, Load: load, Servers: []Server{{Name: code + "#1", Country: code, Load: load, Up: true}}}
	}
	capture.Panel(t, "protonvpn", Panel(PanelState{
		Tab: "connections", HasCLI: true, Traffic: true,
		Snap: Snapshot{
			Phase: PhaseConnected, IP: "203.0.113.24",
			Status: Status{Phase: PhaseConnected, Server: "CH#12", Location: "Zurich, Switzerland", Country: "ch", Load: 34, Protocol: "wireguard"},
			RxRate: 2.4e6, TxRate: 310e3,
			Interface: "tun0",
		},
		Conns: ConnectionsState{
			Countries: []Country{
				country("CH", "Switzerland", 34), country("IS", "Iceland", 21), country("JP", "Japan", 58),
				country("DE", "Germany", 47), country("NL", "Netherlands", 62), country("SE", "Sweden", 29),
				country("CA", "Canada", 51), country("AU", "Australia", 44),
			},
		},
	}))
}
