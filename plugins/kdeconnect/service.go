package kdeconnect

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	// The scan selects .png sources; register the PNG decoder for image.Decode.
	_ "image/png"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/image/draw"
)

const recentImageMaxLongEdge = 512

// recentImageThumbDir lives under os.UserCacheDir()/sysc-plugins/kdeconnect/
// so per-thumb filenames stay short (sysc-shell's icons.FileResolver rejects
// paths past 4096 bytes; a SHA-1 hex + .jpg stays well under that).
var recentImageThumbDir = filepath.Join(os.TempDir(), "sysc-kdeconnect-thumbs")

func init() {
	if base, err := os.UserCacheDir(); err == nil {
		recentImageThumbDir = filepath.Join(base, "sysc-plugins", "kdeconnect", "thumbs")
	}
}

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
	SelfID        string
	Devices       []Device
	SelectedID    string
	RecentImages  []RecentImage
}

// RecentImage is one entry in the recent-images grid. ID is the wire key
// the view and the action routing share, Source is the absolute path on
// the SFTP mount, Thumb the cached local thumbnail the host decodes.
type RecentImage struct {
	ID     string
	Source string
	Thumb  string
}

// Settings carries the manifest-backed plugin settings.
type Settings struct {
	RefreshSeconds     float64
	EnableClipboard    bool
	ShowDeviceCard     bool
	RecentImagesPath   string
	MaxRecentImages    int
	ScanSubdirectories bool
}

// DefaultSettings are the manifest defaults.
func DefaultSettings() Settings {
	return Settings{
		RefreshSeconds:     30,
		EnableClipboard:    true,
		ShowDeviceCard:     true,
		RecentImagesPath:   "",
		MaxRecentImages:    6,
		ScanSubdirectories: false,
	}
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
	ActionShareURL
	ActionShareText
	ActionShareFile
	ActionClipboard
	ActionBrowse
	ActionSendSMS
	ActionLaunchSMSApp
)

// EventKind names a service event the entry point turns into a toast.
type EventKind uint8

const (
	// EventPairingRequest arrives when a device asks to pair.
	EventPairingRequest EventKind = iota + 1
	// EventActionResult reports an action's outcome.
	EventActionResult
	// EventShareReceived arrives when the device shares a file with us.
	EventShareReceived
)

// Action is one device action request. Do delivers it to the service
// goroutine, which performs the daemon call and emits the outcome. Arg
// carries the URL, text, file path, or phone number the kind needs; Arg2
// carries the SMS body.
type Action struct {
	Kind     ActionKind
	DeviceID string
	Arg      string
	Arg2     string
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
	connect   func() (daemonBus, error)
	smsSender func(deviceID, number, body string) error
	settings  Settings

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
		smsSender: sendSMSViaCLI,
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

// sendSMSViaCLI mirrors the reference shell's transport: SMS goes through
// kdeconnect-cli, not the daemon's sms DBus method, which has never worked
// reliably on the kdeconnect backend. Arguments are passed as separate exec
// arguments, never through a shell.
func sendSMSViaCLI(deviceID, number, body string) error {
	return exec.Command("kdeconnect-cli",
		"-d", deviceID, "--send-sms", body, "--destination", number).Run()
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

	st := &daemonState{svc: s, devices: map[string]*Device{}, exported: map[string]bool{}, events: s.events}
	saved := ""
	// A zero or negative interval disables the automatic ticker entirely,
	// the DMS stateUpdateInterval slider's off position; only manual
	// refreshes reconcile then.
	interval := tickInterval(s.settings.RefreshSeconds)
	var ticker *time.Ticker
	var tickC <-chan time.Time
	if interval > 0 {
		ticker = time.NewTicker(interval)
		tickC = ticker.C
		defer func() {
			if ticker != nil {
				ticker.Stop()
			}
		}()
	}

	publish := func() {
		st.refreshRecentImages(bus, &saved)
		s.push(buildSnapshot(true, st.announced, st.selfID, st.order, st.devices, &saved, st.recentImages))
	}

	if err := st.reconcile(bus); err != nil {
		return err
	}
	publish()

	for {
		select {
		case <-s.stop:
			return errStopped
		case <-tickC:
			_ = st.reconcile(bus)
			publish()
		case <-s.refresh:
			// A manual refresh always runs; the shared rate limit is for the
			// signal-driven reconciles only.
			_ = st.reconcileNow(bus)
			publish()
		case a := <-s.actions:
			st.performAction(bus, a)
		case set := <-s.configure:
			s.settings = set
			interval = tickInterval(set.RefreshSeconds)
			if ticker != nil {
				ticker.Stop()
				ticker = nil
				tickC = nil
			}
			if interval > 0 {
				ticker = time.NewTicker(interval)
				tickC = ticker.C
			}
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

// tickInterval resolves the settings value into a ticker interval. Zero or
// less disables the ticker; sub-five-second values clamp to five so a
// mistyped setting cannot hammer the daemon.
func tickInterval(seconds float64) time.Duration {
	switch {
	case seconds <= 0:
		return 0
	case seconds < 5:
		return 5 * time.Second
	}
	return time.Duration(seconds * float64(time.Second))
}

// daemonState is the live device model one serve cycle owns. It mutates on
// the serve goroutine only; snapshots are built from it and published.
type daemonState struct {
	svc *Service
	// order preserves the daemon's device ordering; the map alone would
	// randomise every publish.
	order         []string
	devices       map[string]*Device
	announced     string
	selfID        string
	exported      map[string]bool
	lastReconcile time.Time
	events        chan<- Event
	recentImages  []RecentImage
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
	// announcedName and selfId are Qt property getters, exposed as DBus
	// methods on the daemon interface; the property dictionary does not
	// carry them (the live smoke against a real daemon proved it).
	if call := obj.Call(kdeDaemonIface+".announcedName", 0); call.Err == nil && len(call.Body) > 0 {
		st.announced, _ = call.Body[0].(string)
	}
	if call := obj.Call(kdeDaemonIface+".selfId", 0); call.Err == nil && len(call.Body) > 0 {
		st.selfID, _ = call.Body[0].(string)
	}
	call := obj.Call(kdeDaemonIface+".devices", 0, false, false)
	if call.Err != nil {
		return call.Err
	}
	if len(call.Body) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(call.Body))
	st.order = st.order[:0]
	for _, id := range stringList(call.Body[0]) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		st.order = append(st.order, id)
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
	name := displayName(dev)
	// SMS travels through the CLI (see sendSMSViaCLI), never the daemon's
	// sms DBus method.
	if a.Kind == ActionSendSMS {
		if err := st.svc.smsSender(a.DeviceID, a.Arg, a.Arg2); err != nil {
			st.emit(Event{
				Kind: EventActionResult, DeviceID: a.DeviceID, DeviceName: name,
				Message: actionFailureText(a.Kind, name), Err: err,
			})
			return
		}
		st.emit(Event{
			Kind: EventActionResult, DeviceID: a.DeviceID, DeviceName: name,
			Message: actionSuccessText(a.Kind, name),
		})
		return
	}
	var method string
	var args []any
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
	case ActionShareURL:
		method, args = shareIface+".shareUrl", []any{a.Arg}
	case ActionShareText:
		method, args = shareIface+".shareText", []any{a.Arg}
	case ActionShareFile:
		// DMS routes file shares through shareUrl with a file:// URI; the
		// daemon's shareFile method is never used by it.
		fileURL := localFileURL(a.Arg)
		if fileURL == "" {
			st.emit(Event{
				Kind: EventActionResult, DeviceID: a.DeviceID, DeviceName: displayName(dev),
				Message: actionFailureText(a.Kind, displayName(dev)),
				Err:     errors.New("kdeconnect: invalid file path " + a.Arg),
			})
			return
		}
		method, args = shareIface+".shareUrl", []any{fileURL}
	case ActionClipboard:
		method = clipboardIface + ".sendClipboard"
	case ActionBrowse:
		method = sftpIface + ".startBrowsing"
	case ActionLaunchSMSApp:
		method = smsIface + ".launchApp"
	default:
		st.emit(Event{
			Kind: EventActionResult, DeviceID: a.DeviceID, DeviceName: displayName(dev),
			Message: "Unsupported action",
			Err:     fmt.Errorf("kdeconnect: unsupported action %d", a.Kind),
		})
		return
	}
	call := bus.object(kdeService, devicePath(a.DeviceID)).Call(method, 0, args...)
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
		Message: actionSuccessText(a.Kind, name), Detail: actionDetail(a),
	})
}

// actionDetail is the extra toast body some actions carry, such as the file
// being sent.
func actionDetail(a Action) string {
	if a.Kind == ActionShareFile && a.Arg != "" {
		return path.Base(a.Arg)
	}
	return ""
}

// localFileURL converts an absolute path into a file:// URI with every path
// segment percent-encoded exactly like the reference shell's
// encodeURIComponent per segment (unreserved set plus !'()*), the same
// encoding its file shares ride on. A path that is already a file URL
// passes through; anything else is rejected.
func localFileURL(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "file://") {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	segments := strings.Split(p, "/")
	for i, segment := range segments {
		segments[i] = encodeURIComponent(segment)
	}
	return "file://" + strings.Join(segments, "/")
}

// encodeURIComponent matches JavaScript's encodeURIComponent: it leaves
// A-Z a-z 0-9 - _ . ! ~ * ' ( ) bare and percent-encodes everything else.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~',
			c == '!', c == '\'', c == '(', c == ')', c == '*':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
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
	case ActionShareURL, ActionShareText, ActionShareFile:
		return "Shared with " + name
	case ActionClipboard:
		return "Clipboard sent to " + name
	case ActionBrowse:
		return "Opening the file browser..."
	case ActionSendSMS:
		return "SMS sent successfully"
	case ActionLaunchSMSApp:
		return "Opening the SMS app..."
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
	case ActionShareURL, ActionShareText, ActionShareFile:
		return "Failed to share with " + name
	case ActionClipboard:
		return "Failed to send the clipboard to " + name
	case ActionBrowse:
		return "Failed to open the file browser"
	case ActionSendSMS:
		return "Failed to send the SMS"
	case ActionLaunchSMSApp:
		return "Failed to launch the SMS app"
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
			st.order = append(st.order, id)
		}
		st.fetchDevice(bus, id)
		return true
	case "deviceRemoved":
		id, _ := sig.Body[0].(string)
		if _, ok := st.devices[id]; ok {
			delete(st.devices, id)
			for i, ordered := range st.order {
				if ordered == id {
					st.order = append(st.order[:i], st.order[i+1:]...)
					break
				}
			}
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
	case "shareReceived":
		if dev == nil {
			return false
		}
		url := ""
		if len(sig.Body) > 0 {
			url, _ = sig.Body[0].(string)
		}
		st.emit(Event{
			Kind: EventShareReceived, DeviceID: dev.ID, DeviceName: displayName(dev),
			Message: "File received from " + displayName(dev),
			Detail:  url,
		})
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

// scanRecentImages runs `find` over root (which must live under the
// SFTP mountPoint) and returns the most recently modified images, capped
// at max. The image filter is the host-decode subset (sysc-shell
// decodableExtensions covers png/xpm/jpg/jpeg/gif/bmp); the plan
// deliberately drops webp because the host cannot decode it.
func scanRecentImages(root, mountPoint string, max int, sub bool) ([]string, error) {
	if root == "" || mountPoint == "" {
		return nil, errors.New("kdeconnect: scanRecentImages needs a root and a mount point")
	}
	cleanedRoot := filepath.Clean(root)
	cleanedMount := filepath.Clean(mountPoint)
	if cleanedRoot != cleanedMount && !strings.HasPrefix(cleanedRoot, cleanedMount+string(filepath.Separator)) {
		return nil, fmt.Errorf("kdeconnect: scan root %s is not under SFTP mount %s", cleanedRoot, cleanedMount)
	}
	depth := "1"
	if sub {
		depth = "2"
	}
	cmd := exec.Command("find", cleanedRoot,
		"-maxdepth", depth,
		"(", "-iname", "*.png", "-o", "-iname", "*.jpg", "-o", "-iname", "*.jpeg", ")",
		"-printf", "%T@ %p\n")
	out, err := cmd.Output()
	// find exits nonzero on any error — one unreadable subdirectory under
	// scan_subdirectories — while still printing what it walked. Keep the
	// partial listing; only a walk that produced nothing is a failure.
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("kdeconnect: find %s: %w", cleanedRoot, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	type entry struct {
		mtime float64
		path  string
	}
	var entries []entry
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		space := bytes.IndexByte(line, ' ')
		if space < 0 {
			continue
		}
		mtime, err := strconv.ParseFloat(string(line[:space]), 64)
		if err != nil {
			continue
		}
		entries = append(entries, entry{mtime: mtime, path: string(line[space+1:])})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mtime > entries[j].mtime })
	if max > 0 && len(entries) > max {
		entries = entries[:max]
	}
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.path
	}
	return paths, nil
}

// thumbnail decodes src, scales the longest side to recentImageMaxLongEdge
// with Catmull-Rom resampling, encodes JPEG, and caches the result under
// cacheDir. The cache key hashes src with its mtime so a touched file
// re-thumbnails and a stale entry never serves.
func thumbnail(src, cacheDir string) (string, error) {
	if src == "" {
		return "", errors.New("kdeconnect: thumbnail needs a source path")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("kdeconnect: stat %s: %w", src, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("kdeconnect: %s is a directory", src)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("kdeconnect: mkdir %s: %w", cacheDir, err)
	}
	key := fmt.Sprintf("%x", sha1.Sum([]byte(src+"|"+strconv.FormatInt(info.ModTime().UnixNano(), 10))))
	cached := filepath.Join(cacheDir, key+".jpg")
	if body, err := os.ReadFile(cached); err == nil && len(body) >= 4 && body[0] == 0xff && body[1] == 0xd8 {
		return cached, nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("kdeconnect: read %s: %w", src, err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("kdeconnect: decode %s: %w", src, err)
	}
	dw, dh := thumbnailSize(img.Bounds().Dx(), img.Bounds().Dy(), recentImageMaxLongEdge)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	tmp := cached + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("kdeconnect: create %s: %w", tmp, err)
	}
	err = jpeg.Encode(out, dst, &jpeg.Options{Quality: 80})
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("kdeconnect: encode %s: %w", cached, err)
	}
	if err := os.Rename(tmp, cached); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("kdeconnect: rename %s: %w", cached, err)
	}
	return cached, nil
}

// thumbnailSize returns the destination size that fits the source inside
// the max-long-edge box, keeping the aspect ratio. Inputs at or below the
// cap are returned unchanged so the encoder does no extra work on already
// small images.
func thumbnailSize(w, h, max int) (int, int) {
	longest := w
	if h > longest {
		longest = h
	}
	if longest <= max || longest == 0 {
		return w, h
	}
	if w >= h {
		if short := h * max / w; short > 0 {
			return max, short
		}
		return max, 1
	}
	if short := w * max / h; short > 0 {
		return short, max
	}
	return 1, max
}

// recentImageID derives the wire key from the source path, so an image
// keeps its identity across publishes and the action routing can map a
// button press back to its file even after a re-render reorders the grid.
func recentImageID(source string) string {
	sum := sha1.Sum([]byte(source))
	return fmt.Sprintf("%x", sum[:6])
}

// refreshRecentImages rebuilds the recent-images grid for the selected
// device, the DMS sftp flow: mount() is idempotent, mountPoint() names
// where the share landed, and the scan plus thumbnails run over the mount.
// The configured path is relative to the mount point. Every failure —
// no path configured, no sftp plugin, a failed mount, an unreadable file —
// degrades to an empty grid, never an unavailable panel.
func (st *daemonState) refreshRecentImages(bus daemonBus, saved *string) {
	st.recentImages = nil
	sub := st.svc.settings.RecentImagesPath
	if sub == "" {
		return
	}
	devices := make([]Device, 0, len(st.order))
	for _, id := range st.order {
		if dev, ok := st.devices[id]; ok {
			devices = append(devices, *dev)
		}
	}
	selected := resolveSelection(devices, *saved)
	dev := st.devices[selected]
	if dev == nil || !dev.Reachable || !hasPlugin(dev, "sftp") {
		return
	}
	obj := bus.object(kdeService, pluginPath(selected, "sftp"))
	// The pinned SFTP interface carries startBrowsing, mountPoint, and
	// mountAndWait — no bare mount. startBrowsing is the DMS flow: it mounts
	// the share and the mountPoint call then reads where it landed.
	if call := obj.Call(sftpIface+".startBrowsing", 0); call.Err != nil {
		return
	}
	call := obj.Call(sftpIface+".mountPoint", 0)
	if call.Err != nil || len(call.Body) == 0 {
		return
	}
	mount, _ := call.Body[0].(string)
	if mount == "" {
		return
	}
	paths, err := scanRecentImages(filepath.Join(mount, sub), mount,
		st.svc.settings.MaxRecentImages, st.svc.settings.ScanSubdirectories)
	if err != nil {
		return
	}
	images := make([]RecentImage, 0, len(paths))
	for _, p := range paths {
		thumb, err := thumbnail(p, recentImageThumbDir)
		if err != nil {
			continue // an unreadable or undecodable file drops out of the grid
		}
		images = append(images, RecentImage{ID: recentImageID(p), Source: p, Thumb: thumb})
	}
	st.recentImages = images
}

// buildSnapshot assembles the live model in the daemon's own device order
// and resolves the selection.
func buildSnapshot(available bool, announced, selfID string, order []string, devices map[string]*Device, saved *string, recent []RecentImage) Snapshot {
	snap := Snapshot{Available: available, BackendName: "KDE Connect", AnnouncedName: announced, SelfID: selfID, RecentImages: recent}
	if !available {
		return snap
	}
	snap.Devices = make([]Device, 0, len(order))
	for _, id := range order {
		if dev, ok := devices[id]; ok {
			snap.Devices = append(snap.Devices, *dev)
		}
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
