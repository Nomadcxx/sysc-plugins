package kdeconnect

import (
	"reflect"
	"strings"

	"github.com/godbus/dbus/v5"
)

// The KDE Connect daemon publishes one object tree under /modules/kdeconnect
// on the session bus: the daemon object, and one device object (with one
// child per device plugin) per known device. The service reads state from
// the tree's properties and drives it through one method per action; every
// mutation also arrives as a signal, so the service subscribes once to the
// daemon's name and reacts rather than polling.
const (
	kdeService     = "org.kde.kdeconnect"
	kdeDaemonPath  = dbus.ObjectPath("/modules/kdeconnect")
	kdeDaemonIface = kdeService + ".daemon"
	kdeDeviceIface = kdeService + ".device"

	propsIface      = "org.freedesktop.DBus.Properties"
	introspectIface = "org.freedesktop.DBus.Introspectable"
	getAllMethod    = propsIface + ".GetAll"
)

// Per-device plugin object paths append these names under the device path.
const (
	batteryIface        = kdeService + ".device.battery"
	connectivityIface   = kdeService + ".device.connectivity_report"
	notificationsIface  = kdeService + ".device.notifications"
	findMyPhoneIface    = kdeService + ".device.findmyphone"
	pingIface           = kdeService + ".device.ping"
	shareIface          = kdeService + ".device.share"
	clipboardIface      = kdeService + ".device.clipboard"
	smsIface            = kdeService + ".device.sms"
	sftpIface           = kdeService + ".device.sftp"
	devicePathPrefix    = string(kdeDaemonPath) + "/devices/"
	notificationsMember = "activeNotifications"
)

// daemonObject and daemonBus are deliberately narrower than godbus's public
// objects. The sole implementation wraps one *dbus.Conn; the seam lets the
// whole daemon protocol run against a deterministic fake in the tests.
type daemonObject interface {
	Call(method string, flags dbus.Flags, args ...any) *dbus.Call
}

type daemonBus interface {
	object(destination string, path dbus.ObjectPath) daemonObject
	addMatch(options ...dbus.MatchOption) error
	signal(ch chan<- *dbus.Signal)
	removeSignal(ch chan<- *dbus.Signal)
	close() error
}

// sessionBus is the godbus implementation of the seam.
type sessionBus struct{ conn *dbus.Conn }

func (b *sessionBus) object(destination string, path dbus.ObjectPath) daemonObject {
	return b.conn.Object(destination, path)
}

func (b *sessionBus) addMatch(options ...dbus.MatchOption) error {
	return b.conn.AddMatchSignal(options...)
}

func (b *sessionBus) signal(ch chan<- *dbus.Signal) { b.conn.Signal(ch) }

func (b *sessionBus) removeSignal(ch chan<- *dbus.Signal) { b.conn.RemoveSignal(ch) }

func (b *sessionBus) close() error { return b.conn.Close() }

// connectDaemon opens one fresh session-bus connection. ConnectSessionBus
// rather than SessionBus: the cached shared connection cannot survive the
// close/reopen cycle a daemon restart drives the service through.
func connectDaemon() (daemonBus, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	return &sessionBus{conn}, nil
}

// hasPlugin reports whether a device advertises a daemon plugin. The daemon
// is inconsistent about the kdeconnect_ prefix, so both spellings count.
func hasPlugin(dev *Device, name string) bool {
	if dev == nil {
		return false
	}
	for _, p := range dev.SupportedPlugins {
		if p == name || p == "kdeconnect_"+name {
			return true
		}
	}
	return false
}

// memberOf is the signal's member: the last segment of the interface name.
func memberOf(sig *dbus.Signal) string {
	if sig == nil {
		return ""
	}
	if i := strings.LastIndexByte(sig.Name, '.'); i >= 0 {
		return sig.Name[i+1:]
	}
	return sig.Name
}

// deviceIDFromPath extracts the device id from any object path under
// /modules/kdeconnect/devices/<id>[/<plugin>].
func deviceIDFromPath(path dbus.ObjectPath) string {
	p := string(path)
	if !strings.HasPrefix(p, devicePathPrefix) {
		return ""
	}
	rest := p[len(devicePathPrefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// notificationCount measures the daemon's activeNotifications reply. The
// signature is an array of notification values; a lone value counts as one,
// mirroring how the reference shell normalises the same reply.
func notificationCount(reply any) int {
	if reply == nil {
		return 0
	}
	switch rv := reflect.ValueOf(reply); rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len()
	case reflect.Struct:
		return 1
	}
	return 0
}
