package kdeconnect

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeObject scripts one object's replies. Every method the service calls
// goes through Call, so the fake dispatches on the method name and records
// what it was asked.
type fakeObject struct {
	calls     []string
	args      map[string][]any
	ifaces    map[string]map[string]dbus.Variant
	devices   []string
	announced string
	selfID    string
	xml       string
	notifs    int
	actions   map[string]bool
	failWith  error
	// mountPoint is the sftp object's mountPoint() reply.
	mountPoint string
}

func (f *fakeObject) Call(method string, flags dbus.Flags, args ...any) *dbus.Call {
	f.calls = append(f.calls, method)
	if f.args == nil {
		f.args = map[string][]any{}
	}
	f.args[method] = args
	if f.failWith != nil {
		return &dbus.Call{Err: f.failWith}
	}
	if f.actions[method] {
		return &dbus.Call{}
	}
	switch method {
	case getAllMethod:
		iface, _ := args[0].(string)
		return &dbus.Call{Body: []any{f.ifaces[iface]}}
	case kdeDaemonIface + ".devices":
		return &dbus.Call{Body: []any{f.devices}}
	case kdeDaemonIface + ".announcedName":
		return &dbus.Call{Body: []any{f.announced}}
	case kdeDaemonIface + ".selfId":
		return &dbus.Call{Body: []any{f.selfID}}
	case introspectIface + ".Introspect":
		return &dbus.Call{Body: []any{f.xml}}
	case notificationsIface + "." + notificationsMember:
		body := make([]any, f.notifs)
		return &dbus.Call{Body: []any{body}}
	case sftpIface + ".mountPoint":
		return &dbus.Call{Body: []any{f.mountPoint}}
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

// calledArgs returns the arguments of the most recent call to method.
func (f *fakeObject) calledArgs(method string) []any { return f.args[method] }

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
			ifaces:    map[string]map[string]dbus.Variant{},
			devices:   []string{"devA", "devB"},
			announced: "My Desktop",
			selfID:    "deadbeef01",
		},
		devicePath("devA"): {
			ifaces: map[string]map[string]dbus.Variant{
				kdeDeviceIface: deviceProps("Pixel 10 Pro XL", "phone", true, true,
					[]string{"kdeconnect_battery", "connectivity_report", "notifications", "findmyphone", "ping"}),
			},
			xml: `<node><interface name="other"/><node name="notifications"/></node>`,
			actions: map[string]bool{
				kdeDeviceIface + ".requestPairing": true, kdeDeviceIface + ".acceptPairing": true,
				kdeDeviceIface + ".cancelPairing": true, kdeDeviceIface + ".unpair": true,
				findMyPhoneIface + ".ring": true, pingIface + ".sendPing": true,
				shareIface + ".shareUrl": true, shareIface + ".shareText": true, shareIface + ".shareFile": true,
				clipboardIface + ".sendClipboard": true, sftpIface + ".startBrowsing": true,
				smsIface + ".sendSms": true, smsIface + ".launchApp": true,
			},
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
			actions: map[string]bool{
				kdeDeviceIface + ".requestPairing": true, kdeDeviceIface + ".acceptPairing": true,
				kdeDeviceIface + ".cancelPairing": true, kdeDeviceIface + ".unpair": true,
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
	if snap.SelfID != "deadbeef01" {
		t.Fatalf("selfId = %q", snap.SelfID)
	}
	// The daemon's own device order is preserved, not sorted by name.
	if snap.Devices[0].Name != "Pixel 10 Pro XL" || snap.Devices[1].Name != "Galaxy Tab" {
		t.Fatalf("devices are not in daemon order: %q, %q", snap.Devices[0].Name, snap.Devices[1].Name)
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
	tab := deviceByName(initial, "Galaxy Tab")
	if tab == nil {
		t.Fatal("tab missing")
	}
	// Auto-select picks the daemon's first reachable device; the saved
	// choice moves the panel to the tablet and survives the next publish.
	if initial.SelectedID == tab.ID {
		t.Fatalf("auto-select already chose the tablet: %q", initial.SelectedID)
	}
	svc.SetSelected(tab.ID)
	snap := waitForSnapshot(t, svc, func(s Snapshot) bool { return s.SelectedID == tab.ID })
	if snap.SelectedID != tab.ID {
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

func waitForEvent(t *testing.T, svc *Service, want func(Event) bool) Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-svc.Events():
			if want(e) {
				return e
			}
		case <-deadline:
			t.Fatal("timed out waiting for the wanted event")
		}
	}
}

func TestPairingActionCallsAndEmits(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	svc.Do(Action{Kind: ActionPair, DeviceID: "devA"})
	e := waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult && e.DeviceID == "devA" })
	if e.Err != nil || e.Message != "Pairing request sent to Pixel 10 Pro XL" {
		t.Fatalf("pair event = %+v", e)
	}
	if !bus.objects[devicePath("devA")].asked(kdeDeviceIface + ".requestPairing") {
		t.Fatal("requestPairing never called")
	}
}

func TestActionOnMissingDeviceEmitsFailure(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	svc.Do(Action{Kind: ActionUnpair, DeviceID: "devZ"})
	e := waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult && e.DeviceID == "devZ" })
	if e.Err == nil {
		t.Fatalf("missing-device action = %+v", e)
	}
}

func TestRingAction(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	svc.Do(Action{Kind: ActionRing, DeviceID: "devA"})
	e := waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult })
	if e.Err != nil || e.Message != "Ringing Pixel 10 Pro XL..." {
		t.Fatalf("ring event = %+v", e)
	}
	if !bus.objects[devicePath("devA")].asked(findMyPhoneIface + ".ring") {
		t.Fatal("ring never called")
	}
}

func TestAcceptPairingRefreshesImmediately(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	daemon := bus.objects[kdeDaemonPath]
	before := len(daemon.calls)
	svc.Do(Action{Kind: ActionAcceptPair, DeviceID: "devA"})
	e := waitForEvent(t, svc, func(e Event) bool {
		return e.Kind == EventActionResult && e.Message == "Pixel 10 Pro XL paired"
	})
	if e.Err != nil {
		t.Fatalf("accept event = %+v", e)
	}
	if !bus.objects[devicePath("devA")].asked(kdeDeviceIface + ".acceptPairing") {
		t.Fatal("acceptPairing never called")
	}
	if len(daemon.calls) <= before {
		t.Fatal("accept did not re-read the device list")
	}
}

func TestIncomingPairingRequestEvent(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	bus.objects[devicePath("devB")].ifaces[kdeDeviceIface]["isPairRequestedByPeer"] = dbus.MakeVariant(true)
	bus.objects[devicePath("devB")].ifaces[kdeDeviceIface]["verificationKey"] = dbus.MakeVariant("654321")
	bus.inject(t, &dbus.Signal{
		Path: devicePath("devB"),
		Name: kdeDeviceIface + ".pairStateChanged",
		Body: []any{true},
	})

	e := waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventPairingRequest })
	if e.DeviceName != "Galaxy Tab" || e.Message != "Pairing request from Galaxy Tab" || e.Detail != "Verification: 654321" {
		t.Fatalf("pairing request event = %+v", e)
	}
}

func TestShareAndFileMarshalling(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	dev := bus.objects[devicePath("devA")]

	svc.Do(Action{Kind: ActionShareURL, DeviceID: "devA", Arg: "https://example.com"})
	e := waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult && e.Message == "Shared with Pixel 10 Pro XL" })
	if e.Err != nil {
		t.Fatalf("share url event = %+v", e)
	}
	if args := dev.calledArgs(shareIface + ".shareUrl"); len(args) != 1 || args[0] != "https://example.com" {
		t.Fatalf("shareUrl args = %v", args)
	}

	svc.Do(Action{Kind: ActionShareFile, DeviceID: "devA", Arg: "/home/me/photo.png"})
	e = waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult && e.Detail == "photo.png" })
	if e.Err != nil || e.Message != "Shared with Pixel 10 Pro XL" {
		t.Fatalf("share file event = %+v", e)
	}
	if args := dev.calledArgs(shareIface + ".shareFile"); len(args) != 0 {
		t.Fatalf("shareFile called %v; file shares ride shareUrl", args)
	}
	// The fake records the latest call per method: the file share's URI is
	// the current shareUrl argument.
	if args := dev.calledArgs(shareIface + ".shareUrl"); len(args) != 1 || args[0] != "file:///home/me/photo.png" {
		t.Fatalf("share url file args = %v", args)
	}
}

func TestLocalFileURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/home/me/photo.png":           "file:///home/me/photo.png",
		"/home/me/my photos/pic 1.jpg": "file:///home/me/my%20photos/pic%201.jpg",
		"/home/me/photo (1).png":       "file:///home/me/photo%20(1).png",
		"/home/me/rock&roll.png":       "file:///home/me/rock%26roll.png",
		"file:///already/a%20url.png":  "file:///already/a%20url.png",
		"":                             "",
		"relative/path.txt":            "",
	}
	for in, want := range cases {
		if got := localFileURL(in); got != want {
			t.Fatalf("localFileURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSMSSendGoesThroughTheCLITransport(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })

	var gotDevice, gotNumber, gotBody string
	var fail error
	svc.smsSender = func(deviceID, number, body string) error {
		gotDevice, gotNumber, gotBody = deviceID, number, body
		return fail
	}

	svc.Do(Action{Kind: ActionSendSMS, DeviceID: "devA", Arg: "+1 555 0100", Arg2: "hello"})
	e := waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult && e.Message == "SMS sent successfully" })
	if e.Err != nil {
		t.Fatalf("sms event = %+v", e)
	}
	if gotDevice != "devA" || gotNumber != "+1 555 0100" || gotBody != "hello" {
		t.Fatalf("cli transport args = %q %q %q", gotDevice, gotNumber, gotBody)
	}

	fail = errors.New("kdeconnect-cli exited nonzero")
	svc.Do(Action{Kind: ActionSendSMS, DeviceID: "devA", Arg: "+1 555 0100", Arg2: "hello"})
	e = waitForEvent(t, svc, func(e Event) bool {
		return e.Kind == EventActionResult && e.Message == "Failed to send the SMS"
	})
	if e.Err == nil {
		t.Fatalf("cli failure not surfaced: %+v", e)
	}
}

func TestClipboardBrowseAndSMSAppActions(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	dev := bus.objects[devicePath("devA")]

	svc.Do(Action{Kind: ActionClipboard, DeviceID: "devA"})
	e := waitForEvent(t, svc, func(e Event) bool {
		return e.Kind == EventActionResult && e.Message == "Clipboard sent to Pixel 10 Pro XL"
	})
	if e.Err != nil || !dev.asked(clipboardIface+".sendClipboard") {
		t.Fatalf("clipboard event = %+v", e)
	}

	svc.Do(Action{Kind: ActionBrowse, DeviceID: "devA"})
	e = waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult && e.Message == "Opening the file browser..." })
	if e.Err != nil || !dev.asked(sftpIface+".startBrowsing") {
		t.Fatalf("browse event = %+v", e)
	}

	svc.Do(Action{Kind: ActionLaunchSMSApp, DeviceID: "devA"})
	e = waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventActionResult && e.Message == "Opening the SMS app..." })
	if e.Err != nil || !dev.asked(smsIface+".launchApp") {
		t.Fatalf("sms app event = %+v", e)
	}
}

func TestShareReceivedEvent(t *testing.T) {
	bus := testBus()
	svc := newService(singleConnect(bus))
	defer svc.Close()

	waitForSnapshot(t, svc, func(s Snapshot) bool { return s.Available && len(s.Devices) == 2 })
	bus.inject(t, &dbus.Signal{
		Path: devicePath("devA"),
		Name: shareIface + ".shareReceived",
		Body: []any{"file:///home/me/photo.png"},
	})

	e := waitForEvent(t, svc, func(e Event) bool { return e.Kind == EventShareReceived })
	if e.Message != "File received from Pixel 10 Pro XL" || e.Detail != "file:///home/me/photo.png" {
		t.Fatalf("share received event = %+v", e)
	}
}

// writeTouch is a tiny helper that writes a file with a forced mtime so the
// scan's mtime ordering is reproducible without depending on real time.
func writeTouch(t *testing.T, path string, mtime time.Time, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func TestScanRecentImagesRejectsRootOutsideMount(t *testing.T) {
	t.Parallel()
	mount := t.TempDir()
	other := t.TempDir()
	if _, err := scanRecentImages(filepath.Join(other, "DCIM"), mount, 6, false); err == nil {
		t.Fatal("scan accepted a root outside the SFTP mount")
	}
}

func TestScanRecentImagesSortsByMTimeDescAndCapsAtMax(t *testing.T) {
	t.Parallel()
	mount := t.TempDir()
	root := filepath.Join(mount, "DCIM", "Camera")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	writeTouch(t, filepath.Join(root, "old.png"), base.Add(-72*time.Hour), "old")
	writeTouch(t, filepath.Join(root, "new.jpg"), base.Add(-1*time.Hour), "new")
	writeTouch(t, filepath.Join(root, "mid.jpeg"), base.Add(-24*time.Hour), "mid")
	// webp is dropped by the scan (host cannot decode it; sysc-shell
	// decodableExtensions covers png/xpm/jpg/jpeg/gif/bmp only).
	writeTouch(t, filepath.Join(root, "ignored.webp"), base, "ignored")

	got, err := scanRecentImages(root, mount, 6, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []string{"new.jpg", "mid.jpeg", "old.png"}
	if len(got) != len(want) {
		t.Fatalf("scan returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for i, name := range want {
		if filepath.Base(got[i]) != name {
			t.Fatalf("scan[%d] = %q, want %q", i, got[i], name)
		}
	}
}

func TestScanRecentImagesCapsAtMax(t *testing.T) {
	t.Parallel()
	mount := t.TempDir()
	root := filepath.Join(mount, "Camera")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		writeTouch(t, filepath.Join(root, fmt.Sprintf("img%d.png", i)), base.Add(time.Duration(i)*time.Minute), "x")
	}
	got, err := scanRecentImages(root, mount, 3, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("scan returned %d entries, want 3: %v", len(got), got)
	}
	// The three newest by mtime are img7, img6, img5.
	want := []string{"img7.png", "img6.png", "img5.png"}
	for i, name := range want {
		if filepath.Base(got[i]) != name {
			t.Fatalf("scan[%d] = %q, want %q", i, got[i], name)
		}
	}
}

func TestScanRecentImagesSubdirectoryDepth(t *testing.T) {
	t.Parallel()
	mount := t.TempDir()
	root := filepath.Join(mount, "DCIM")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Camera"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	// top.png is the immediate child of root (depth 1); hidden.png lives in
	// Camera/ (depth 2), reachable only when subdirectories are scanned.
	writeTouch(t, filepath.Join(root, "top.png"), base.Add(-1*time.Hour), "top")
	writeTouch(t, filepath.Join(root, "Camera", "hidden.png"), base, "hidden")

	shallowOnly, err := scanRecentImages(root, mount, 6, false)
	if err != nil {
		t.Fatalf("shallow scan: %v", err)
	}
	if len(shallowOnly) != 1 || filepath.Base(shallowOnly[0]) != "top.png" {
		t.Fatalf("shallow scan returned %v, want just top.png", shallowOnly)
	}

	deepScan, err := scanRecentImages(root, mount, 6, true)
	if err != nil {
		t.Fatalf("deep scan: %v", err)
	}
	if len(deepScan) != 2 {
		t.Fatalf("deep scan returned %d entries, want 2: %v", len(deepScan), deepScan)
	}
	if filepath.Base(deepScan[0]) != "hidden.png" || filepath.Base(deepScan[1]) != "top.png" {
		t.Fatalf("deep scan order = %v, want hidden then top", deepScan)
	}
}

func TestScanRecentImagesNoMatches(t *testing.T) {
	t.Parallel()
	mount := t.TempDir()
	root := filepath.Join(mount, "Camera")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := scanRecentImages(root, mount, 6, false)
	if err != nil {
		t.Fatalf("empty scan: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty mount returned %v, want none", got)
	}
}

func TestThumbnailRoundTripAndCache(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "pic.png")
	mtime := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		for y := 0; y < 2; y++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	out, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(out, img); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	cached, err := thumbnail(src, cache)
	if err != nil {
		t.Fatalf("thumbnail: %v", err)
	}
	if !filepath.IsAbs(cached) || filepath.Dir(cached) != cache {
		t.Fatalf("cached path %q is not under cache %q", cached, cache)
	}
	if filepath.Ext(cached) != ".jpg" {
		t.Fatalf("cached path extension = %q, want .jpg", filepath.Ext(cached))
	}
	// Cached file exists and is a valid JPEG (starts with the JPEG SOI marker).
	body, err := os.ReadFile(cached)
	if err != nil {
		t.Fatalf("read cached: %v", err)
	}
	if len(body) < 4 || body[0] != 0xff || body[1] != 0xd8 {
		t.Fatalf("cached body does not start with JPEG SOI: %x", body[:4])
	}

	// A second call returns the same path unchanged.
	again, err := thumbnail(src, cache)
	if err != nil {
		t.Fatalf("thumbnail second call: %v", err)
	}
	if again != cached {
		t.Fatalf("second call returned %q, want %q", again, cached)
	}
}

func TestThumbnailRejectsMissingSource(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	if _, err := thumbnail(filepath.Join(t.TempDir(), "no.png"), cache); err == nil {
		t.Fatal("thumbnail accepted a missing source")
	}
}

func TestThumbnailSizeKeepsADegenerateAspectVisible(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		w, h, max, wantW, wantH int
	}{
		{10000, 1, 512, 512, 1},
		{1, 10000, 512, 1, 512},
		{100, 100, 512, 100, 100},
	} {
		w, h := thumbnailSize(tc.w, tc.h, tc.max)
		if w != tc.wantW || h != tc.wantH {
			t.Fatalf("thumbnailSize(%d, %d, %d) = %dx%d, want %dx%d",
				tc.w, tc.h, tc.max, w, h, tc.wantW, tc.wantH)
		}
	}
}

// recentImagesBus is a fake bus whose devA advertises sftp and mounts at
// mountPoint, the seam refreshRecentImages drives.
func recentImagesBus(mountPoint string) *fakeBus {
	return &fakeBus{objects: map[dbus.ObjectPath]*fakeObject{
		kdeDaemonPath: {devices: []string{"devA"}, announced: "My Desktop", selfID: "deadbeef01"},
		devicePath("devA"): {
			ifaces: map[string]map[string]dbus.Variant{
				kdeDeviceIface: deviceProps("Pixel 10 Pro XL", "phone", true, true, []string{"sftp"}),
			},
		},
		pluginPath("devA", "sftp"): {
			mountPoint: mountPoint,
			actions:    map[string]bool{sftpIface + ".startBrowsing": true},
		},
	}}
}

func recentImagesState(svc *Service) *daemonState {
	return &daemonState{
		svc:   svc,
		order: []string{"devA"},
		devices: map[string]*Device{"devA": {ID: "devA", Name: "Pixel 10 Pro XL",
			Reachable: true, Paired: true, SupportedPlugins: []string{"sftp"}}},
	}
}

func TestRefreshRecentImagesWiresTheMount(t *testing.T) {
	t.Parallel()
	mount := t.TempDir()
	root := filepath.Join(mount, "DCIM")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	// A real decodable PNG: the wiring thumbnails every scan hit, so a
	// text file would drop out of the grid.
	pic := image.NewRGBA(image.Rect(0, 0, 2, 1))
	pic.Set(0, 0, color.RGBA{R: 255, A: 255})
	photo := filepath.Join(root, "photo.png")
	out, err := os.Create(photo)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(out, pic); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(photo, base, base); err != nil {
		t.Fatal(err)
	}

	cache := t.TempDir()
	saved := recentImageThumbDir
	recentImageThumbDir = cache
	t.Cleanup(func() { recentImageThumbDir = saved })

	bus := recentImagesBus(mount)
	svc := &Service{settings: Settings{RecentImagesPath: "DCIM", MaxRecentImages: 6}}
	st := recentImagesState(svc)
	chosen := "devA"
	st.refreshRecentImages(bus, &chosen)

	sftp := bus.objects[pluginPath("devA", "sftp")]
	if !sftp.asked(sftpIface+".startBrowsing") || !sftp.asked(sftpIface+".mountPoint") {
		t.Fatalf("sftp calls = %v, want mount then mountPoint", sftp.calls)
	}
	if len(st.recentImages) != 1 {
		t.Fatalf("recentImages = %+v, want one entry", st.recentImages)
	}
	img := st.recentImages[0]
	if img.Source != filepath.Join(root, "photo.png") {
		t.Fatalf("source = %q, want the scanned mount path", img.Source)
	}
	if img.Thumb == "" || filepath.Dir(img.Thumb) != cache {
		t.Fatalf("thumb = %q, want a cached path under %q", img.Thumb, cache)
	}
	if len(img.ID) != 12 {
		t.Fatalf("id = %q, want a 12-hex wire key", img.ID)
	}
	// The snapshot carries the same grid, the ID → Source pair the action
	// routing resolves against.
	snap := buildSnapshot(true, st.announced, st.selfID, st.order, st.devices, &chosen, st.recentImages)
	if len(snap.RecentImages) != 1 || snap.RecentImages[0].ID != img.ID {
		t.Fatalf("snapshot recentImages = %+v", snap.RecentImages)
	}
}

func TestRefreshRecentImagesSkipsWithoutPathOrPlugin(t *testing.T) {
	t.Parallel()
	bus := recentImagesBus(t.TempDir())
	svc := &Service{}
	st := recentImagesState(svc)
	chosen := "devA"
	st.refreshRecentImages(bus, &chosen)
	if len(st.recentImages) != 0 {
		t.Fatalf("empty path produced %+v", st.recentImages)
	}
	if sftp := bus.objects[pluginPath("devA", "sftp")]; sftp != nil && len(sftp.calls) > 0 {
		t.Fatalf("empty path still called %v", sftp.calls)
	}

	// A selected device without the sftp plugin never mounts either.
	svc.settings = Settings{RecentImagesPath: "DCIM"}
	st.devices["devA"].SupportedPlugins = nil
	st.refreshRecentImages(bus, &chosen)
	if len(st.recentImages) != 0 {
		t.Fatalf("plugin-less device produced %+v", st.recentImages)
	}
	if sftp := bus.objects[pluginPath("devA", "sftp")]; len(sftp.calls) > 0 {
		t.Fatalf("plugin-less device still called %v", sftp.calls)
	}
}

func TestRefreshRecentImagesDegradesToEmptyGrid(t *testing.T) {
	t.Parallel()
	// The mountPoint reply fails: the grid empties instead of surfacing an
	// unavailable panel.
	bus := recentImagesBus("")
	svc := &Service{settings: Settings{RecentImagesPath: "DCIM"}}
	st := recentImagesState(svc)
	chosen := "devA"
	st.refreshRecentImages(bus, &chosen)
	if len(st.recentImages) != 0 {
		t.Fatalf("failed mount produced %+v", st.recentImages)
	}

	// A scan root outside the mount degrades the same way.
	mount := t.TempDir()
	bus = recentImagesBus(mount)
	st = recentImagesState(svc)
	st.refreshRecentImages(bus, &chosen)
	if len(st.recentImages) != 0 {
		t.Fatalf("foreign root produced %+v", st.recentImages)
	}
}
