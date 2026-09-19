package kdeconnect

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// The service owns one connection to the daemon. It reconnects for as long
// as it lives: a fresh session-bus connection every cycle, an unavailable
// snapshot on the updates channel while the daemon is away, and one
// reconnect attempt after retryDelay. Everything the views need rides on
// immutable snapshots; nothing here blocks the publisher.
const (
	retryDelay    = 5 * time.Second
	minRefreshGap = time.Second
	signalBuffer  = 64
	busSender     = "org.freedesktop.DBus"
)

var (
	errStopped    = errors.New("kdeconnect: service stopped")
	errDaemonGone = errors.New("kdeconnect: daemon left the bus")
)

// Device mirrors one org.kde.kdeconnect device object as the views see it.
// Battery, network, and notification readings carry their own known flags:
// a device that does not advertise a plugin reports unknown, never zero.
type Device struct {
	ID        string
	Name      string
	Type      string
	Reachable bool
	Paired    bool

	PairRequested       bool
	PairRequestedByPeer bool
	VerificationKey     string
	SupportedPlugins    []string

	BatteryCharge   int // percent, -1 unknown
	BatteryCharging bool
	BatteryKnown    bool

	NetworkType     string
	NetworkStrength int // 0-4, -1 unknown
	NetworkKnown    bool

	NotificationCount  int
	NotificationsKnown bool
}

// Snapshot is one immutable view of the daemon. Available false means the
// daemon is not on the session bus; Devices is sorted by name whenever it
// is populated; SelectedID is the device the panel shows.
type Snapshot struct {
	Available     bool
	BackendName   string
	AnnouncedName string
	Devices       []Device
	SelectedID    string
}

// Settings carries the manifest-backed plugin settings.
type Settings struct {
	RefreshSeconds  float64
	EnableClipboard bool
	ShowDeviceCard  bool
}

// DefaultSettings are the manifest defaults.
func DefaultSettings() Settings {
	return Settings{RefreshSeconds: 30, EnableClipboard: true, ShowDeviceCard: true}
}

// ActionKind names a device action the panel can drive.
type ActionKind uint8

const (
	ActionRing ActionKind = iota + 1
	ActionPing
	ActionPair
	ActionAcceptPair
	ActionRejectPair
	ActionUnpair
)

// EventKind names a service event the entry point turns into a toast.
type EventKind uint8

const (
	// EventPairingRequest arrives when a device asks to pair.
	EventPairingRequest EventKind = iota + 1
	// EventActionResult reports an action's outcome.
	EventActionResult
)

// Action is one device action request. Do delivers it to the service
// goroutine, which performs the daemon call and emits the outcome.
type Action struct {
	Kind     ActionKind
	DeviceID string
}

// Event is one daemon-side occurrence worth surfacing: an incoming pairing
// request or an action's result. Message is the toast title, Detail the
// optional body, and Err marks a failure.
type Event struct {
	Kind       EventKind
	DeviceID   string
	DeviceName string
	Message    string
	Detail     string
	Err        error
}

// Service publishes daemon snapshots on Updates.
type Service struct {
	connect  func() (daemonBus, error)
	settings Settings

	updates   chan Snapshot
	events    chan Event
	actions   chan Action
	configure chan Settings
	refresh   chan struct{}
	selectReq chan string
	stop      chan struct{}
	done      chan struct{}
}

// New starts the service against the real session bus.
func New() *Service { return newService(connectDaemon) }

func newService(connect func() (daemonBus, error)) *Service {
	s := &Service{
		connect:   connect,
		settings:  DefaultSettings(),
		updates:   make(chan Snapshot, 1),
		events:    make(chan Event, 16),
		actions:   make(chan Action, 8),
		configure: make(chan Settings, 1),
		refresh:   make(chan struct{}, 1),
		selectReq: make(chan string, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go s.run()
	return s
}

// Updates streams snapshots, one value buffered, latest wins.
func (s *Service) Updates() <-chan Snapshot { return s.updates }

// Events streams pairing requests and action outcomes. It never blocks the
// service: a saturated channel drops the event.
func (s *Service) Events() <-chan Event { return s.events }

// Do performs a device action. The request is dropped when the service
// already has a queue of them.
func (s *Service) Do(a Action) { offer(s.actions, a) }

// Reconfigure applies new settings.
func (s *Service) Reconfigure(settings Settings) { offer(s.configure, settings) }

// Refresh asks for a device reconcile now.
func (s *Service) Refresh() { offer(s.refresh, struct{}{}) }

// SetSelected records the device the user chose. The service keeps the
// choice while it stays paired and reachable, and falls back to the best
// device otherwise.
func (s *Service) SetSelected(id string) { offer(s.selectReq, id) }

// Close stops the service and waits for its goroutine to finish.
func (s *Service) Close() {
	close(s.stop)
	<-s.done
}

func offer[T any](ch chan T, value T) {
	select {
	case ch <- value:
	default:
	}
}

// run owns the connect/serve/retry cycle for the whole service lifetime.
func (s *Service) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		bus, err := s.connect()
		if err == nil {
			err = s.serve(bus)
			_ = bus.close()
		}
		// Whatever ended the cycle, the daemon is not being served now.
		s.push(Snapshot{BackendName: "KDE Connect"})
		if errors.Is(err, errStopped) {
			return
		}
		select {
		case <-time.After(retryDelay):
		case <-s.stop:
			return
		}
	}
}

// serve drives one connected period. It returns errStopped when the service
// is closing and errDaemonGone when the daemon left the bus; any other
// error means the connection or the daemon failed and the cycle restarts.
func (s *Service) serve(bus daemonBus) error {
	signals := make(chan *dbus.Signal, signalBuffer)
	if err := bus.addMatch(dbus.WithMatchSender(kdeService)); err != nil {
		return err
	}
	if err := bus.addMatch(
		dbus.WithMatchSender(busSender),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchOption("arg0", kdeService),
	); err != nil {
		return err
	}
	bus.signal(signals)

	st := &daemonState{devices: map[string]*Device{}, exported: map[string]bool{}, events: s.events}
	saved := ""
	ticker := time.NewTicker(tickInterval(s.settings.RefreshSeconds))
	defer ticker.Stop()

	publish := func() {
		s.push(buildSnapshot(true, st.announced, st.devices, &saved))
	}

	if err := st.reconcile(bus); err != nil {
		return err
	}
	publish()

	for {
		select {
		case <-s.stop:
			return errStopped
		case <-ticker.C:
			_ = st.reconcile(bus)
			publish()
		case <-s.refresh:
			_ = st.reconcile(bus)
			publish()
		case a := <-s.actions:
			st.performAction(bus, a)
		case set := <-s.configure:
			s.settings = set
			ticker.Reset(tickInterval(set.RefreshSeconds))
		case id := <-s.selectReq:
			saved = id
			publish()
		case sig, ok := <-signals:
			if !ok {
				return errDaemonGone
			}
			if memberOf(sig) == "NameOwnerChanged" {
				if len(sig.Body) > 2 {
					if owner, isStr := sig.Body[2].(string); isStr && owner == "" {
						return errDaemonGone
					}
				}
				continue
			}
			if updateFromSignal(sig, bus, st) {
				publish()
			}
		}
	}
}

func tickInterval(seconds float64) time.Duration {
	if seconds < 5 {
		return 5 * time.Second
	}
	return time.Duration(seconds * float64(time.Second))
}

// daemonState is the live device model one serve cycle owns. It mutates on
// the serve goroutine only; snapshots are built from it and published.
type daemonState struct {
	devices       map[string]*Device
	announced     string
	exported      map[string]bool
	lastReconcile time.Time
	events        chan<- Event
}

// emit delivers an event without ever blocking the serve loop.
func (st *daemonState) emit(e Event) {
	select {
	case st.events <- e:
	default:
	}
}

// reconcile re-reads the daemon's device list and every device's state. It
// runs at most once per minRefreshGap no matter how many signals ask.
func (st *daemonState) reconcile(bus daemonBus) error {
	if !st.lastReconcile.IsZero() && time.Since(st.lastReconcile) < minRefreshGap {
		return nil
	}
	return st.reconcileNow(bus)
}

// reconcileNow re-reads the device list regardless of the refresh gap; the
// action work uses it so a pairing result shows immediately.
func (st *daemonState) reconcileNow(bus daemonBus) error {
	st.lastReconcile = time.Now()

	obj := bus.object(kdeService, kdeDaemonPath)
	if props, err := getAllProps(obj, kdeDaemonIface); err == nil {
		st.announced = strOf(props["announcedName"])
	}
	call := obj.Call(kdeDaemonIface+".devices", 0, false, false)
	if call.Err != nil {
		return call.Err
	}
	if len(call.Body) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(call.Body))
	for _, id := range stringList(call.Body[0]) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := st.devices[id]; !ok {
			st.devices[id] = newDevice(id)
		}
		st.fetchDevice(bus, id)
	}
	for id := range st.devices {
		if !seen[id] {
			delete(st.devices, id)
		}
	}
	return nil
}

// fetchDevice re-reads one device and, when it is paired and reachable, its
// battery, connectivity, and notification readings.
func (st *daemonState) fetchDevice(bus daemonBus, id string) {
	props, err := getAllProps(bus.object(kdeService, devicePath(id)), kdeDeviceIface)
	if err != nil {
		return // keep the last known state of a device mid-restart
	}
	dev, ok := st.devices[id]
	if !ok {
		dev = newDevice(id)
		st.devices[id] = dev
	}
	wasRequestedByPeer := dev.PairRequestedByPeer
	if name := strOf(props["name"]); name != "" {
		dev.Name = name
	}
	dev.Type = strOf(props["type"])
	dev.Reachable = boolOf(props["isReachable"])
	dev.Paired = boolOf(props["isPaired"])
	dev.PairRequested = boolOf(props["isPairRequested"])
	dev.PairRequestedByPeer = boolOf(props["isPairRequestedByPeer"])
	dev.VerificationKey = strOf(props["verificationKey"])
	dev.SupportedPlugins = stringListOf(props["supportedPlugins"])
	if dev.PairRequestedByPeer && !wasRequestedByPeer {
		e := Event{
			Kind:       EventPairingRequest,
			DeviceID:   dev.ID,
			DeviceName: displayName(dev),
			Message:    "Pairing request from " + displayName(dev),
		}
		if dev.VerificationKey != "" {
			e.Detail = "Verification: " + dev.VerificationKey
		}
		st.emit(e)
	}
	if dev.Reachable && dev.Paired {
		st.fetchBattery(bus, dev)
		st.fetchConnectivity(bus, dev)
		st.fetchNotifications(bus, dev)
	}
}

func (st *daemonState) fetchBattery(bus daemonBus, dev *Device) {
	if !hasPlugin(dev, "battery") {
		return
	}
	props, err := getAllProps(bus.object(kdeService, pluginPath(dev.ID, "battery")), batteryIface)
	if err != nil {
		return
	}
	dev.BatteryCharge = intOf(props["charge"])
	dev.BatteryCharging = boolOf(props["isCharging"])
	dev.BatteryKnown = true
}

func (st *daemonState) fetchConnectivity(bus daemonBus, dev *Device) {
	if !hasPlugin(dev, "connectivity_report") {
		return
	}
	props, err := getAllProps(bus.object(kdeService, pluginPath(dev.ID, "connectivity_report")), connectivityIface)
	if err != nil {
		return
	}
	dev.NetworkType = strOf(props["cellularNetworkType"])
	dev.NetworkStrength = intOf(props["cellularNetworkStrength"])
	dev.NetworkKnown = true
}

// fetchNotifications counts the device's active notifications, behind the
// introspect probe: a locally disabled plugin stays in supportedPlugins but
// exports no object, and probing once keeps the dead call off every refresh.
func (st *daemonState) fetchNotifications(bus daemonBus, dev *Device) {
	if !hasPlugin(dev, "notifications") {
		return
	}
	exported, probed := st.exported[dev.ID]
	if !probed {
		call := bus.object(kdeService, devicePath(dev.ID)).Call(introspectIface+".Introspect", 0)
		if call.Err != nil {
			return
		}
		xml, _ := call.Body[0].(string)
		exported = strings.Contains(xml, `name="notifications"`)
		st.exported[dev.ID] = exported
	}
	if !exported {
		return
	}
	call := bus.object(kdeService, pluginPath(dev.ID, "notifications")).Call(notificationsIface+"."+notificationsMember, 0)
	if call.Err != nil || len(call.Body) == 0 {
		return
	}
	dev.NotificationCount = notificationCount(call.Body[0])
	dev.NotificationsKnown = true
}

// performAction drives one panel action against the daemon and reports the
// outcome. Pairing actions re-read the device list immediately so the panel
// reflects the new state without waiting for the next signal.
func (st *daemonState) performAction(bus daemonBus, a Action) {
	dev := st.devices[a.DeviceID]
	if dev == nil {
		st.emit(Event{
			Kind: EventActionResult, DeviceID: a.DeviceID,
			Message: actionFailureText(a.Kind, "device"),
			Err:     errors.New("kdeconnect: device is no longer available"),
		})
		return
	}
	var method string
	switch a.Kind {
	case ActionRing:
		method = findMyPhoneIface + ".ring"
	case ActionPing:
		method = pingIface + ".sendPing"
	case ActionPair:
		method = kdeDeviceIface + ".requestPairing"
	case ActionAcceptPair:
		method = kdeDeviceIface + ".acceptPairing"
	case ActionRejectPair:
		method = kdeDeviceIface + ".cancelPairing"
	case ActionUnpair:
		method = kdeDeviceIface + ".unpair"
	default:
		st.emit(Event{
			Kind: EventActionResult, DeviceID: a.DeviceID, DeviceName: displayName(dev),
			Message: "Unsupported action",
			Err:     fmt.Errorf("kdeconnect: unsupported action %d", a.Kind),
		})
		return
	}
	call := bus.object(kdeService, devicePath(a.DeviceID)).Call(method, 0)
	name := displayName(dev)
	if call.Err != nil {
		st.emit(Event{
			Kind: EventActionResult, DeviceID: a.DeviceID, DeviceName: name,
			Message: actionFailureText(a.Kind, name), Err: call.Err,
		})
		return
	}
	switch a.Kind {
	case ActionPair, ActionAcceptPair, ActionRejectPair, ActionUnpair:
		_ = st.reconcileNow(bus)
	}
	st.emit(Event{
		Kind: EventActionResult, DeviceID: a.DeviceID, DeviceName: name,
		Message: actionSuccessText(a.Kind, name),
	})
}

// displayName prefers the device name and falls back to its id, which is
// all a fresh device has before its first property read.
func displayName(dev *Device) string {
	if dev.Name != "" {
		return dev.Name
	}
	return dev.ID
}

func actionSuccessText(kind ActionKind, name string) string {
	switch kind {
	case ActionRing:
		return "Ringing " + name + "..."
	case ActionPing:
		return "Ping sent to " + name
	case ActionPair:
		return "Pairing request sent to " + name
	case ActionAcceptPair:
		return name + " paired"
	case ActionRejectPair:
		return "Pairing request from " + name + " rejected"
	case ActionUnpair:
		return name + " unpaired"
	}
	return "Done"
}

func actionFailureText(kind ActionKind, name string) string {
	switch kind {
	case ActionRing:
		return "Failed to ring " + name
	case ActionPing:
		return "Failed to ping " + name
	case ActionPair:
		return "Pairing with " + name + " failed"
	case ActionAcceptPair:
		return "Failed to accept pairing with " + name
	case ActionRejectPair:
		return "Failed to reject the pairing request from " + name
	case ActionUnpair:
		return "Failed to unpair " + name
	}
	return "The action failed"
}

// updateFromSignal applies one daemon signal and reports whether the state
// changed. Unknown members and foreign paths are ignored.
func updateFromSignal(sig *dbus.Signal, bus daemonBus, st *daemonState) bool {
	member := memberOf(sig)
	switch member {
	case "deviceAdded":
		id, _ := sig.Body[0].(string)
		if id == "" {
			return false
		}
		if _, ok := st.devices[id]; !ok {
			st.devices[id] = newDevice(id)
		}
		st.fetchDevice(bus, id)
		return true
	case "deviceRemoved":
		id, _ := sig.Body[0].(string)
		if _, ok := st.devices[id]; ok {
			delete(st.devices, id)
			return true
		}
		return false
	case "deviceListChanged", "deviceVisibilityChanged", "pairingRequestsChanged":
		_ = st.reconcile(bus)
		return true
	}

	id := deviceIDFromPath(sig.Path)
	if id == "" {
		return false
	}
	dev := st.devices[id]
	switch member {
	case "reachableChanged", "pairStateChanged", "nameChanged", "pluginsChanged",
		"typeChanged", "statusIconNameChanged", "linksChanged":
		if dev == nil {
			return false
		}
		st.fetchDevice(bus, id)
		return true
	case "refreshed":
		if dev == nil || len(sig.Body) < 2 {
			return false
		}
		dev.BatteryCharging, _ = sig.Body[0].(bool)
		dev.BatteryCharge = intOfAny(sig.Body[1])
		dev.BatteryKnown = true
		return true
	case "connectivityUpdated":
		if dev == nil {
			return false
		}
		st.fetchConnectivity(bus, dev)
		return true
	case "notificationPosted", "notificationRemoved", "allNotificationsRemoved":
		if dev == nil {
			return false
		}
		st.fetchNotifications(bus, dev)
		return true
	case "PropertiesChanged":
		if dev == nil || len(sig.Body) == 0 {
			return false
		}
		switch iface, _ := sig.Body[0].(string); iface {
		case kdeDeviceIface:
			st.fetchDevice(bus, id)
			return true
		case batteryIface:
			st.fetchBattery(bus, dev)
			return true
		case connectivityIface:
			st.fetchConnectivity(bus, dev)
			return true
		case notificationsIface:
			st.fetchNotifications(bus, dev)
			return true
		}
	}
	return false
}

// buildSnapshot sorts the live model by name and resolves the selection.
func buildSnapshot(available bool, announced string, devices map[string]*Device, saved *string) Snapshot {
	snap := Snapshot{Available: available, BackendName: "KDE Connect", AnnouncedName: announced}
	if !available {
		return snap
	}
	ids := make([]string, 0, len(devices))
	for id := range devices {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ni, nj := devices[ids[i]].Name, devices[ids[j]].Name
		if ni != nj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})
	snap.Devices = make([]Device, 0, len(ids))
	for _, id := range ids {
		snap.Devices = append(snap.Devices, *devices[id])
	}
	snap.SelectedID = resolveSelection(snap.Devices, *saved)
	return snap
}

// resolveSelection keeps the saved choice while it stays paired and
// reachable, and otherwise falls back to the first reachable device, then
// the first device at all — the reference shell's auto-select order.
func resolveSelection(devices []Device, saved string) string {
	for i := range devices {
		if devices[i].ID == saved && devices[i].Paired && devices[i].Reachable {
			return saved
		}
	}
	for i := range devices {
		if devices[i].Reachable {
			return devices[i].ID
		}
	}
	if len(devices) > 0 {
		return devices[0].ID
	}
	return ""
}

// push delivers the newest snapshot without ever blocking the publisher.
func (s *Service) push(snap Snapshot) {
	select {
	case <-s.updates:
	default:
	}
	select {
	case s.updates <- snap:
	default:
	}
}

func newDevice(id string) *Device {
	return &Device{ID: id, BatteryCharge: -1, NetworkStrength: -1}
}

func devicePath(id string) dbus.ObjectPath {
	return dbus.ObjectPath(devicePathPrefix + id)
}

func pluginPath(id, plugin string) dbus.ObjectPath {
	return dbus.ObjectPath(devicePathPrefix + id + "/" + plugin)
}

// getAllProps reads one interface's property dictionary in one round trip.
func getAllProps(obj daemonObject, iface string) (map[string]dbus.Variant, error) {
	call := obj.Call(getAllMethod, 0, iface)
	if call.Err != nil {
		return nil, call.Err
	}
	if len(call.Body) == 0 {
		return nil, fmt.Errorf("kdeconnect: empty GetAll reply from %s", iface)
	}
	props, ok := call.Body[0].(map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("kdeconnect: unexpected GetAll reply from %s", iface)
	}
	return props, nil
}

func strOf(v dbus.Variant) string { s, _ := v.Value().(string); return s }

func boolOf(v dbus.Variant) bool { b, _ := v.Value().(bool); return b }

func intOf(v dbus.Variant) int { return intOfAny(v.Value()) }

func intOfAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int8:
		return int(n)
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint8:
		return int(n)
	case uint16:
		return int(n)
	case uint32:
		return int(n)
	case uint64:
		return int(n)
	}
	return -1
}

func stringListOf(v dbus.Variant) []string {
	switch list := v.Value().(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func stringList(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
