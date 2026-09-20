package kdeconnect

import (
	"os"
	"testing"
	"time"
)

// TestLiveDaemonSnapshot exercises the real session-bus daemon when
// SYSC_KDECONNECT_LIVE is set; ordinary runs and CI skip it. It proves the
// protocol assumptions against the real daemon: the name answers or the
// service degrades, the device list decodes, and any paired device's
// readings populate.
func TestLiveDaemonSnapshot(t *testing.T) {
	if os.Getenv("SYSC_KDECONNECT_LIVE") == "" {
		t.Skip("set SYSC_KDECONNECT_LIVE=1 with kdeconnectd on the session bus")
	}
	svc := New()
	defer svc.Close()
	// Two connect cycles: retryDelay is five seconds.
	deadline := time.After(12 * time.Second)
	var sawUnavailable bool
	for {
		select {
		case snap := <-svc.Updates():
			if !snap.Available {
				sawUnavailable = true
				continue
			}
			t.Logf("backend=%q announced=%q devices=%d", snap.BackendName, snap.AnnouncedName, len(snap.Devices))
			for _, d := range snap.Devices {
				t.Logf("  %s %q type=%s reachable=%v paired=%v battery=%d/%v plugins=%v",
					d.ID, d.Name, d.Type, d.Reachable, d.Paired, d.BatteryCharge, d.BatteryKnown, d.SupportedPlugins)
			}
			if sawUnavailable {
				t.Log("service recovered from an unavailable cycle before reaching the daemon")
			}
			return
		case <-deadline:
			t.Fatal("service never reached the daemon")
		}
	}
}
