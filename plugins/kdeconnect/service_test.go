package kdeconnect

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeObject scripts one object's replies. Every method the service calls
// goes through Call, so the fake dispatches on the method name and records
// what it was asked.
type fakeObject struct {
	calls    []string
	ifaces   map[string]map[string]dbus.Variant
	devices  []string
	xml      string
	notifs   int
	failWith error
}

func (f *fakeObject) Call(method string, flags dbus.Flags, args ...any) *dbus.Call {
	f.calls = append(f.calls, method)
	if f.failWith != nil {
		return &dbus.Call{Err: f.failWith}
	}
	switch method {
	case getAllMethod:
		iface, _ := args[0].(string)
		return &dbus.Call{Body: []any{f.ifaces[iface]}}
	case kdeDaemonIface + ".devices":
		return &dbus.Call{Body: []any{f.devices}}
	case introspectIface + ".Introspect":
		return &dbus.Call{Body: []any{f.xml}}
	case notificationsIface + "." + notificationsMember:
		body := make([]any, f.notifs)
		return &dbus.Call{Body: []any{body}}
	default:
		return &dbus.Call{Err: fmt.Errorf("fake: unexpected method %s", method)}
	}
}

func (f *fakeObject) asked(method string) bool {
	for _, m := range f.calls {
		if m == method {
			return true
		}
	}
	return false
}

// fakeBus hands out scripted objects and forwards injected signals to the
// service's subscription.
type fakeBus struct {
	mu       sync.Mutex
	objects  map[dbus.ObjectPath]*fakeObject
	matches  int
	signalCh chan<- *dbus.Signal
}

func (b *fakeBus) object(destination string, path dbus.ObjectPath) daemonObject {
	b.mu.Lock()
	defer b.mu.Unlock()
	if obj, ok := b.objects[path]; ok {
		return obj
	}
	return &fakeObject{failWith: errors.New("fake: no object at " + string(path))}
}

func (b *fakeBus) addMatch(options ...dbus.MatchOption) error {
	b.matches++
	return nil
}

func (b *fakeBus) signal(ch chan<- *dbus.Signal) { b.signalCh = ch }

func (b *fakeBus) removeSignal(ch chan<- *dbus.Signal) {}

func (b *fakeBus) close() error { return nil }

func (b *fakeBus) inject(t *testing.T, sig *dbus.Signal) {
	t.Helper()
	select {
	case b.signalCh <- sig:
	case <-time.After(2 * time.Second):
		t.Fatal("signal subscription never took the injected signal")
	}
}

func deviceProps(name, typ string, reachable, paired bool, plugins []string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"name":                  dbus.MakeVariant(name),
		"type":                  dbus.MakeVariant(typ),
		"isReachable":           dbus.MakeVariant(reachable),
		"isPaired":              dbus.MakeVariant(paired),
		"isPairRequested":       dbus.MakeVariant(false),
		"isPairRequestedByPeer": dbus.MakeVariant(false),
		"verificationKey":       dbus.MakeVariant(""),
		"supportedPlugins":      dbus.MakeVariant(plugins),
	}
}

func batteryProps(charge int32, charging bool) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"charge":     dbus.MakeVariant(charge),
		"isCharging": dbus.MakeVariant(charging),
	}
}

func connectivityProps(networkType string, strength int32) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"cellularNetworkType":     dbus.MakeVariant(networkType),
		"cellularNetworkStrength": dbus.MakeVariant(strength),
	}
}

// testBus wires one phone (reachable, paired, all readings) and one tablet
// (reachable, paired, no plugin readings).
func testBus() *fakeBus {
	return &fakeBus{objects: map[dbus.ObjectPath]*fakeObject{
		kdeDaemonPath: {
			ifaces: map[string]map[string]dbus.Variant{
				kdeDaemonIface: {"announcedName": dbus.MakeVariant("My Desktop")},
			},
			devices: []string{"devA", "devB"},
		},
		devicePath("devA"): {
			ifaces: map[string]map[string]dbus.Variant{
				kdeDeviceIface: deviceProps("Pixel 10 Pro XL", "phone", true, true,
					[]string{"kdeconnect_battery", "connectivity_report", "notifications"}),
			},
			xml: `<node><interface name="other"/><node name="notifications"/></node>`,
		},
		pluginPath("devA", "battery"): {
			ifaces: map[string]map[string]dbus.Variant{
				batteryIface: batteryProps(98, false),
			},
		},
		pluginPath("devA", "connectivity_report"): {
			ifaces: map[string]map[string]dbus.Variant{
				connectivityIface: connectivityProps("LTE", 4),
			},
		},
		pluginPath("devA", "notifications"): {notifs: 2},
		devicePath("devB"): {
			ifaces: map[string]map[string]dbus.Variant{
				kdeDeviceIface: deviceProps("Galaxy Tab", "tablet", true, true, []string{"kdeconnect_battery"}),
			},
		},
		pluginPath("devB", "battery"): {
			ifaces: map[string]map[string]dbus.Variant{
				batteryIface: batteryProps(55, true),
			},
		},
	}}
}

func singleConnect(bus *fakeBus) func() (daemonBus, error) {
	used := false
	return func() (daemonBus, error) {
		if used {
			return nil, errors.New("fake: no further connections")
		}
		used = true
		return bus, nil
	}
}

func waitForSnapshot(t *testing.T, svc *Service, want func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case snap := <-svc.Updates():
			if want(snap) {
				return snap
			}
		case <-deadline:
			t.Fatal("timed out waiting for the wanted snapshot")
		}
	}
}

func deviceByName(snap Snapshot, name string) *Device {
	for i := range snap.Devices {
		if snap.Devices[i].Name == name {
			return &snap.Devices[i]
		}
	}
	return nil
}

func TestConnectDiscoversDevicesAndReadings(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	snap := waitForSnapshot(t, svc, func(s Snapshot) bool {
		return s.Available && len(s.Devices) == 2
	})
	if snap.AnnouncedName != "My Desktop" {
		t.Fatalf("announcedName = %q", snap.AnnouncedName)
	}
	if snap.Devices[0].Name != "Galaxy Tab" || snap.Devices[1].Name != "Pixel 10 Pro XL" {
		t.Fatalf("devices are not sorted by name: %q, %q", snap.Devices[0].Name, snap.Devices[1].Name)
	}
	pixel := deviceByName(snap, "Pixel 10 Pro XL")
	if pixel == nil {
		t.Fatal("pixel missing")
	}
	if !pixel.BatteryKnown || pixel.BatteryCharge != 98 || pixel.BatteryCharging {
		t.Fatalf("pixel battery = %+v", pixel)
	}
	if !pixel.NetworkKnown || pixel.NetworkType != "LTE" || pixel.NetworkStrength != 4 {
		t.Fatalf("pixel connectivity = %+v", pixel)
	}
	if !pixel.NotificationsKnown || pixel.NotificationCount != 2 {
		t.Fatalf("pixel notifications = %+v", pixel)
	}
	tab := deviceByName(snap, "Galaxy Tab")
	if !tab.BatteryKnown || tab.BatteryCharge != 55 || !tab.BatteryCharging {
		t.Fatalf("tab battery = %+v, want the advertised reading", tab)
	}
	if tab.NotificationsKnown || tab.NetworkKnown {
		t.Fatalf("tab readings fetched without the plugins: %+v", tab)
	}
	if snap.SelectedID != snap.Devices[0].ID {
		t.Fatalf("selected = %q, want the auto-selected first reachable device", snap.SelectedID)
	}
}

func TestSavedSelectionKeptWhileReachable(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	initial := waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	pixel := deviceByName(initial, "Pixel 10 Pro XL")
	if pixel == nil {
		t.Fatal("pixel missing")
	}
	// Auto-select picked the first reachable device; the saved choice
	// switches the panel to the pixel and survives the next publish.
	if initial.SelectedID == pixel.ID {
		t.Fatalf("auto-select already chose the pixel: %q", initial.SelectedID)
	}
	svc.SetSelected(pixel.ID)
	snap := waitForSnapshot(t, svc, func(s Snapshot) bool { return s.SelectedID == pixel.ID })
	if snap.SelectedID != pixel.ID {
		t.Fatalf("saved selection lost: %q", snap.SelectedID)
	}
}

func TestDeviceAddedSignal(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })

	bus.objects[devicePath("devC")] = &fakeObject{
		ifaces: map[string]map[string]dbus.Variant{
			kdeDeviceIface: deviceProps("New Phone", "phone", false, false, nil),
		},
	}
	bus.inject(t, &dbus.Signal{
		Path: kdeDaemonPath,
		Name: kdeDaemonIface + ".deviceAdded",
		Body: []any{"devC"},
	})

	snap := waitForSnapshot(t, svc, func(s Snapshot) bool {
		return deviceByName(s, "New Phone") != nil
	})
	added := deviceByName(snap, "New Phone")
	if added.Reachable || added.Paired {
		t.Fatalf("added device state = %+v", added)
	}
}

func TestDeviceRemovedSignalDropsAndReselects(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	bus.inject(t, &dbus.Signal{
		Path: kdeDaemonPath,
		Name: kdeDaemonIface + ".deviceRemoved",
		Body: []any{"devA"},
	})

	snap := waitForSnapshot(t, svc, func(s Snapshot) bool { return len(s.Devices) == 1 })
	if deviceByName(snap, "Pixel 10 Pro XL") != nil {
		t.Fatal("removed device stayed in the snapshot")
	}
	if snap.SelectedID != snap.Devices[0].ID {
		t.Fatalf("selection did not fall back: %q", snap.SelectedID)
	}
}

func TestBatteryRefreshedSignal(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	bus.inject(t, &dbus.Signal{
		Path: pluginPath("devA", "battery"),
		Name: batteryIface + ".refreshed",
		Body: []any{true, int32(42)},
	})

	snap := waitForSnapshot(t, svc, func(s Snapshot) bool {
		p := deviceByName(s, "Pixel 10 Pro XL")
		return p != nil && p.BatteryCharging && p.BatteryCharge == 42
	})
	pixel := deviceByName(snap, "Pixel 10 Pro XL")
	if !pixel.BatteryKnown || pixel.BatteryCharge != 42 || !pixel.BatteryCharging {
		t.Fatalf("pixel battery after refreshed = %+v", pixel)
	}
}

func TestPropertiesChangedRereadsBattery(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })

	battery := bus.objects[pluginPath("devA", "battery")]
	battery.ifaces[batteryIface] = batteryProps(10, true)
	bus.inject(t, &dbus.Signal{
		Path: pluginPath("devA", "battery"),
		Name: propsIface + ".PropertiesChanged",
		Body: []any{batteryIface},
	})

	waitForSnapshot(t, svc, func(s Snapshot) bool {
		p := deviceByName(s, "Pixel 10 Pro XL")
		return p != nil && p.BatteryCharge == 10 && p.BatteryCharging
	})
}

func TestNotificationProbeWithoutNodeStaysUnknown(t *testing.T) {
	bus := testBus()
	bus.objects[devicePath("devA")].xml = `<node><interface name="other"/></node>`
	svc := newService(singleConnect(bus))
	defer svc.Close()

	snap := waitForSnapshot(t, svc, func(s Snapshot) bool {
		p := deviceByName(s, "Pixel 10 Pro XL")
		return s.Available && p != nil && p.NetworkKnown
	})
	pixel := deviceByName(snap, "Pixel 10 Pro XL")
	if pixel.NotificationsKnown || pixel.NotificationCount != 0 {
		t.Fatalf("notifications read although the object exports none: %+v", pixel)
	}
}

func TestDaemonLossClearsState(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	bus.inject(t, &dbus.Signal{
		Path: "/",
		Name: busSender + ".NameOwnerChanged",
		Body: []any{kdeService, ":1.1", ""},
	})

	waitForSnapshot(t, svc, func(s Snapshot) bool { return !s.Available && len(s.Devices) == 0 })
}

func TestResolveSelectionOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		devices  []Device
		saved    string
		selected string
	}{
		{
			name: "saved reachable wins",
			devices: []Device{
				{ID: "a", Reachable: true, Paired: true},
				{ID: "b", Reachable: true, Paired: true},
			},
			saved:    "b",
			selected: "b",
		},
		{
			name: "saved offline falls to first reachable",
			devices: []Device{
				{ID: "a", Reachable: false, Paired: true},
				{ID: "b", Reachable: true, Paired: true},
			},
			saved:    "a",
			selected: "b",
		},
		{
			name: "nothing reachable falls to first device",
			devices: []Device{
				{ID: "a", Reachable: false, Paired: true},
				{ID: "b", Reachable: false, Paired: false},
			},
			saved:    "",
			selected: "a",
		},
		{
			name:     "no devices",
			devices:  nil,
			saved:    "a",
			selected: "",
		},
	}
	for _, tc := range cases {
		if got := resolveSelection(tc.devices, tc.saved); got != tc.selected {
			t.Fatalf("%s: resolveSelection = %q, want %q", tc.name, got, tc.selected)
		}
	}
}

func TestHasPluginSpelling(t *testing.T) {
	t.Parallel()
	dev := &Device{SupportedPlugins: []string{"kdeconnect_battery", "ping"}}
	if !hasPlugin(dev, "battery") || !hasPlugin(dev, "ping") {
		t.Fatal("hasPlugin missed a supported spelling")
	}
	if hasPlugin(dev, "sms") || hasPlugin(nil, "sms") {
		t.Fatal("hasPlugin invented a plugin")
	}
}

func TestDeviceIDFromPath(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/modules/kdeconnect/devices/abc":         "abc",
		"/modules/kdeconnect/devices/abc/battery": "abc",
		"/modules/kdeconnect":                     "",
		"/org/freedesktop/DBus":                   "",
	}
	for path, want := range cases {
		if got := deviceIDFromPath(dbus.ObjectPath(path)); got != want {
			t.Fatalf("deviceIDFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestNotificationCountShapes(t *testing.T) {
	t.Parallel()
	if got := notificationCount(nil); got != 0 {
		t.Fatalf("nil reply counted as %d", got)
	}
	if got := notificationCount(make([]any, 3)); got != 3 {
		t.Fatalf("array reply counted as %d", got)
	}
	if got := notificationCount(struct{}{}); got != 1 {
		t.Fatalf("lone value counted as %d", got)
	}
}
