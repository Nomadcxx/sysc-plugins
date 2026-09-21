package minidocker

import (
	"context"
	"sync"
)

// Session holds the last docker snapshot plus the action-error state: the
// two error kinds have different lifetimes, so they live in different slots.
// listErr (daemon problems) is whatever the last List produced; actErr is
// the last failed action and is cleared only by the next successful one -
// the action's own trailing refresh must not erase it.
type Session struct {
	mu         sync.Mutex
	docker     Docker
	containers []Container
	available  bool
	loading    bool
	listErr    string
	actErr     string
	actingID   string
}

func NewSession(d Docker) *Session {
	return &Session{docker: d, loading: true}
}

// Snapshot returns the current state. listErr is the list-side failure,
// actErr the last failed action, actingID the container an action is
// running against right now.
func (s *Session) Snapshot() (containers []Container, available bool, loading bool, listErr string, actErr string, actingID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Container, len(s.containers))
	copy(out, s.containers)
	return out, s.available, s.loading, s.listErr, s.actErr, s.actingID
}

func (s *Session) RunningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.containers {
		if c.Running() {
			n++
		}
	}
	return n
}

// Refresh polls docker ps and records availability.
func (s *Session) Refresh(ctx context.Context) {
	s.mu.Lock()
	s.loading = true
	s.mu.Unlock()

	containers, err := s.docker.List(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.available = false
		s.loading = false
		s.listErr = err.Error()
		return
	}
	s.containers = containers
	s.available = true
	s.loading = false
	s.listErr = ""
}

// Act runs a container action, then refreshes. A failure is recorded in
// actErr and survives the refresh that follows (the daemon is fine, the
// action was rejected - erasing it would show the user nothing); the next
// successful action clears it. actingID is visible for the whole call so
// the view can disable that container's buttons in flight.
func (s *Session) Act(ctx context.Context, action, id string) {
	var err error
	switch action {
	case "start":
	case "stop":
	case "restart":
	default:
		return
	}

	s.mu.Lock()
	s.actingID = id
	s.mu.Unlock()

	switch action {
	case "start":
		err = s.docker.Start(ctx, id)
	case "stop":
		err = s.docker.Stop(ctx, id)
	case "restart":
		err = s.docker.Restart(ctx, id)
	}

	s.mu.Lock()
	s.actingID = ""
	if err != nil {
		s.actErr = err.Error()
	} else {
		s.actErr = ""
	}
	s.mu.Unlock()

	s.Refresh(ctx)
}
