// Package kdeconnect is the Phone Connect plugin: a KDE Connect daemon
// client served through the shell's plugin wire protocol. The service
// mirrors the daemon as immutable snapshots; the views render one snapshot
// into the bar, tooltip, and panel trees.
package kdeconnect

import "sync"

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

// Device mirrors one org.kde.kdeconnect device object as the views see it.
type Device struct {
	ID        string
	Name      string
	Type      string
	Reachable bool
	Paired    bool
}

// Snapshot is one immutable view of the daemon. Available false means the
// daemon is not on the session bus; Devices is sorted by name whenever it
// is populated.
type Snapshot struct {
	Available     bool
	BackendName   string
	AnnouncedName string
	Devices       []Device
	SelectedID    string
}

// Service publishes daemon snapshots on Updates. The skeleton reports the
// daemon as unavailable; the DBus backend arrives with the service work and
// keeps this contract.
type Service struct {
	mu       sync.Mutex
	settings Settings
	updates  chan Snapshot
}

// New starts the service. It publishes one unavailable snapshot so a view
// opened before the backend connects still has something honest to show.
func New() *Service {
	s := &Service{
		settings: DefaultSettings(),
		updates:  make(chan Snapshot, 1),
	}
	s.push(Snapshot{BackendName: "KDE Connect"})
	return s
}

// Updates streams snapshots, one value buffered, latest wins.
func (s *Service) Updates() <-chan Snapshot { return s.updates }

// Reconfigure applies new settings.
func (s *Service) Reconfigure(settings Settings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
}

// Refresh asks the backend to re-read the daemon. The skeleton has nothing
// to refresh; the daemon backend reconciles its device list.
func (s *Service) Refresh() {}

// Close stops the service.
func (s *Service) Close() {}

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
