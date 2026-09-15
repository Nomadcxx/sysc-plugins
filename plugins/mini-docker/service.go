package minidocker

import (
	"context"
	"sync"
)

// Session holds the last docker snapshot.
type Session struct {
	mu         sync.Mutex
	docker     Docker
	containers []Container
	available  bool
	loading    bool
	errMsg     string
}

func NewSession(d Docker) *Session {
	return &Session{docker: d, loading: true}
}

// Snapshot returns the current state.
func (s *Session) Snapshot() (containers []Container, available bool, loading bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Container, len(s.containers))
	copy(out, s.containers)
	return out, s.available, s.loading, s.errMsg
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
		s.errMsg = err.Error()
		return
	}
	s.containers = containers
	s.available = true
	s.loading = false
	s.errMsg = ""
}

// Act runs a container action and then refreshes; the error is recorded in
// the snapshot message.
func (s *Session) Act(ctx context.Context, action, id string) {
	var err error
	switch action {
	case "start":
		err = s.docker.Start(ctx, id)
	case "stop":
		err = s.docker.Stop(ctx, id)
	case "restart":
		err = s.docker.Restart(ctx, id)
	default:
		return
	}
	if err != nil {
		s.mu.Lock()
		s.errMsg = err.Error()
		s.mu.Unlock()
	}
	s.Refresh(ctx)
}
