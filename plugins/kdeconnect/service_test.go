package kdeconnect

import "testing"

func TestNewPublishesUnavailableSnapshot(t *testing.T) {
	t.Parallel()
	svc := New()
	defer svc.Close()
	select {
	case snap := <-svc.Updates():
		if snap.Available {
			t.Fatal("the skeleton reports the daemon as available")
		}
		if snap.BackendName != "KDE Connect" {
			t.Fatalf("backend name = %q", snap.BackendName)
		}
		if len(snap.Devices) != 0 {
			t.Fatalf("devices = %+v, want none", snap.Devices)
		}
	default:
		t.Fatal("no initial snapshot")
	}
}

func TestReconfigureAndRefreshAreSafeWithoutABackend(t *testing.T) {
	t.Parallel()
	svc := New()
	defer svc.Close()
	svc.Reconfigure(Settings{RefreshSeconds: 10, EnableClipboard: false, ShowDeviceCard: false})
	svc.Refresh()
}
