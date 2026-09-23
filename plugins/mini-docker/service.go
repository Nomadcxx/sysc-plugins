package minidocker

import (
	"context"
	"sync"
	"time"
)

type Scope string

const (
	ScopeContainers Scope = "containers"
	ScopeImages     Scope = "images"
	ScopeVolumes    Scope = "volumes"
	ScopeNetworks   Scope = "networks"
)

// TabStatus describes the last successful snapshot and any refresh in flight.
type TabStatus struct {
	Available    bool
	Loading      bool
	ListError    string
	SkippedLines int
	RefreshedAt  time.Time
}

// SessionSnapshot is an immutable copy of the state used to render a view.
type SessionSnapshot struct {
	Scope        Scope
	SelectedID   string
	Containers   []Container
	Images       []Image
	Volumes      []Volume
	Networks     []Network
	ContainerTab TabStatus
	ImageTab     TabStatus
	VolumeTab    TabStatus
	NetworkTab   TabStatus
	ActionError  string
	ActingID     string
}

// Session owns Docker snapshots and user interaction state. Docker calls run
// without mu held so view reads and independent tab state remain responsive.
type Session struct {
	mu         sync.Mutex
	docker     Docker
	scope      Scope
	selectedID string
	tabs       map[Scope]TabStatus
	containers []Container
	images     []Image
	volumes    []Volume
	networks   []Network
	actErr     string
	actingID   string
}

func NewSession(d Docker) *Session {
	return &Session{
		docker: d,
		scope:  ScopeContainers,
		tabs: map[Scope]TabStatus{
			ScopeContainers: {Loading: true},
			ScopeImages:     {},
			ScopeVolumes:    {},
			ScopeNetworks:   {},
		},
	}
}

func (s *Session) SetScope(scope Scope) bool {
	if !validScope(scope) {
		return false
	}
	s.mu.Lock()
	if s.scope != scope {
		s.scope = scope
		s.selectedID = ""
	}
	s.mu.Unlock()
	return true
}

func (s *Session) Select(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scope != ScopeContainers || !s.tabs[ScopeContainers].Available || !idRE.MatchString(id) {
		return false
	}
	if s.selectedID == id {
		s.selectedID = ""
		return true
	}
	for _, container := range s.containers {
		if container.ID == id {
			s.selectedID = id
			return true
		}
	}
	return false
}

func validScope(scope Scope) bool {
	switch scope {
	case ScopeContainers, ScopeImages, ScopeVolumes, ScopeNetworks:
		return true
	default:
		return false
	}
}

// State returns copied slices so the renderer never observes a concurrent
// refresh mutating its input.
func (s *Session) State() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionSnapshot{
		Scope:        s.scope,
		SelectedID:   s.selectedID,
		Containers:   append([]Container(nil), s.containers...),
		Images:       append([]Image(nil), s.images...),
		Volumes:      append([]Volume(nil), s.volumes...),
		Networks:     append([]Network(nil), s.networks...),
		ContainerTab: s.tabs[ScopeContainers],
		ImageTab:     s.tabs[ScopeImages],
		VolumeTab:    s.tabs[ScopeVolumes],
		NetworkTab:   s.tabs[ScopeNetworks],
		ActionError:  s.actErr,
		ActingID:     s.actingID,
	}
}

// Snapshot preserves the original container-only accessor for the bar and
// existing callers; new renderers should use State for all tabs.
func (s *Session) Snapshot() (containers []Container, available bool, loading bool, listErr string, actErr string, actingID string) {
	state := s.State()
	return state.Containers, state.ContainerTab.Available, state.ContainerTab.Loading,
		state.ContainerTab.ListError, state.ActionError, state.ActingID
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

// SkippedLines reports malformed records in the last successful container list.
func (s *Session) SkippedLines() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tabs[ScopeContainers].SkippedLines
}

func (s *Session) Refresh(ctx context.Context) {
	s.RefreshTab(ctx, ScopeContainers)
}

func (s *Session) RefreshTab(ctx context.Context, scope Scope) {
	if !validScope(scope) {
		return
	}
	s.mu.Lock()
	status := s.tabs[scope]
	status.Loading = true
	s.tabs[scope] = status
	s.mu.Unlock()

	switch scope {
	case ScopeContainers:
		items, skipped, err := s.docker.List(ctx)
		s.finishRefresh(scope, skipped, err, func() { s.containers = items })
	case ScopeImages:
		items, skipped, err := s.docker.Images(ctx)
		s.finishRefresh(scope, skipped, err, func() { s.images = items })
	case ScopeVolumes:
		items, skipped, err := s.docker.Volumes(ctx)
		s.finishRefresh(scope, skipped, err, func() { s.volumes = items })
	case ScopeNetworks:
		items, skipped, err := s.docker.Networks(ctx)
		s.finishRefresh(scope, skipped, err, func() { s.networks = items })
	}
}

func (s *Session) finishRefresh(scope Scope, skipped int, err error, replace func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.tabs[scope]
	status.Loading = false
	if err != nil {
		status.ListError = err.Error()
		if scope == ScopeContainers {
			// A failed primary list hides cached rows in the renderer and gates
			// mutations, while retaining the cache for the next successful poll.
			status.Available = false
		}
		s.tabs[scope] = status
		return
	}
	status.Available = true
	status.ListError = ""
	status.SkippedLines = skipped
	status.RefreshedAt = time.Now()
	s.tabs[scope] = status
	replace()
	if scope == ScopeContainers && s.selectedID != "" {
		found := false
		for _, container := range s.containers {
			if container.ID == s.selectedID {
				found = true
				break
			}
		}
		if !found {
			s.selectedID = ""
		}
	}
}

// Act accepts only a current, eligible container action. It snapshots the
// choice under the lock, then performs Docker I/O without holding the lock.
func (s *Session) Act(ctx context.Context, action, id string) {
	if !validContainerAction(action) || !idRE.MatchString(id) {
		return
	}
	s.mu.Lock()
	if s.scope != ScopeContainers || !s.tabs[ScopeContainers].Available || s.actingID != "" {
		s.mu.Unlock()
		return
	}
	var found *Container
	for i := range s.containers {
		if s.containers[i].ID == id {
			found = &s.containers[i]
			break
		}
	}
	if found == nil || !eligibleContainerAction(*found, action) {
		s.mu.Unlock()
		return
	}
	s.actingID = id
	s.mu.Unlock()

	var err error
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

func validContainerAction(action string) bool {
	switch action {
	case "start", "stop", "restart":
		return true
	default:
		return false
	}
}

func eligibleContainerAction(c Container, action string) bool {
	switch action {
	case "start":
		return !c.Running()
	case "stop", "restart":
		return c.Running()
	default:
		return false
	}
}
