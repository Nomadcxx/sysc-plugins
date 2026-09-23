package minidocker

import (
	"context"
	"regexp"
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

// RemovalTarget identifies the current tab entity awaiting confirmation.
type RemovalTarget struct {
	Scope Scope
	ID    string
}

type actionKey struct {
	scope Scope
	verb  string
	id    string
}

var entityIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$`)

// SessionSnapshot is an immutable copy of the state used to render a view.
type SessionSnapshot struct {
	Scope          Scope
	SelectedID     string
	Containers     []Container
	Images         []Image
	Volumes        []Volume
	Networks       []Network
	ContainerTab   TabStatus
	ImageTab       TabStatus
	VolumeTab      TabStatus
	NetworkTab     TabStatus
	ActionError    string
	ActingID       string
	PendingRemoval *RemovalTarget
	InFlight       map[actionKey]struct{}
}

// Session owns Docker snapshots and user interaction state. Docker calls run
// without mu held so view reads and independent tab state remain responsive.
type Session struct {
	mu             sync.Mutex
	docker         Docker
	scope          Scope
	selectedID     string
	tabs           map[Scope]TabStatus
	containers     []Container
	images         []Image
	volumes        []Volume
	networks       []Network
	actErr         string
	pendingRemoval *RemovalTarget
	inFlight       map[actionKey]struct{}
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
		inFlight: make(map[actionKey]struct{}),
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
		s.pendingRemoval = nil
	}
	s.mu.Unlock()
	return true
}

func (s *Session) Select(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validEntityID(id) || !s.tabs[s.scope].Available || !s.hasEntityLocked(s.scope, id) {
		return false
	}
	if s.selectedID == id {
		s.selectedID = ""
		return true
	}
	s.selectedID = id
	return true
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
		Scope:          s.scope,
		SelectedID:     s.selectedID,
		Containers:     append([]Container(nil), s.containers...),
		Images:         append([]Image(nil), s.images...),
		Volumes:        append([]Volume(nil), s.volumes...),
		Networks:       append([]Network(nil), s.networks...),
		ContainerTab:   s.tabs[ScopeContainers],
		ImageTab:       s.tabs[ScopeImages],
		VolumeTab:      s.tabs[ScopeVolumes],
		NetworkTab:     s.tabs[ScopeNetworks],
		ActionError:    s.actErr,
		ActingID:       s.actingContainerLocked(),
		PendingRemoval: cloneRemovalTarget(s.pendingRemoval),
		InFlight:       cloneActionSet(s.inFlight),
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

func (s *Session) hasEntityLocked(scope Scope, id string) bool {
	switch scope {
	case ScopeContainers:
		for _, item := range s.containers {
			if item.ID == id {
				return true
			}
		}
	case ScopeImages:
		for _, item := range s.images {
			if item.ID == id {
				return true
			}
		}
	case ScopeVolumes:
		for _, item := range s.volumes {
			if item.Name == id {
				return true
			}
		}
	case ScopeNetworks:
		for _, item := range s.networks {
			if item.ID == id {
				return true
			}
		}
	}
	return false
}

func (s *Session) actingContainerLocked() string {
	var id string
	for key := range s.inFlight {
		if key.scope == ScopeContainers && (id == "" || key.id < id) {
			id = key.id
		}
	}
	return id
}

func cloneRemovalTarget(target *RemovalTarget) *RemovalTarget {
	if target == nil {
		return nil
	}
	copy := *target
	return &copy
}

func cloneActionSet(actions map[actionKey]struct{}) map[actionKey]struct{} {
	copy := make(map[actionKey]struct{}, len(actions))
	for key := range actions {
		copy[key] = struct{}{}
	}
	return copy
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
	if s.scope == scope && s.selectedID != "" && !s.hasEntityLocked(scope, s.selectedID) {
		s.selectedID = ""
	}
	if s.pendingRemoval != nil && s.pendingRemoval.Scope == scope && !s.hasEntityLocked(scope, s.pendingRemoval.ID) {
		s.pendingRemoval = nil
	}
}

// Act accepts only a current, eligible container action. It snapshots the
// choice under the lock, then performs Docker I/O without holding the lock.
func (s *Session) Act(ctx context.Context, action, id string) {
	if !validContainerAction(action) || !validEntityID(id) {
		return
	}
	s.mu.Lock()
	key := actionKey{scope: ScopeContainers, verb: action, id: id}
	if s.scope != ScopeContainers || !s.tabs[ScopeContainers].Available {
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
	if _, exists := s.inFlight[key]; exists {
		s.mu.Unlock()
		return
	}
	s.inFlight[key] = struct{}{}
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
	delete(s.inFlight, key)
	if err != nil {
		s.actErr = err.Error()
	} else {
		s.actErr = ""
	}
	s.mu.Unlock()
	s.Refresh(ctx)
}

// ArmRemoval records an eligible target from the current tab. The target is
// checked again on confirmation because Docker snapshots can change.
func (s *Session) ArmRemoval(id string) bool {
	if !validEntityID(id) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tabs[s.scope].Available || !s.canRemoveLocked(s.scope, id) {
		return false
	}
	if _, exists := s.inFlight[actionKey{scope: s.scope, verb: removeVerb(s.scope), id: id}]; exists {
		return false
	}
	s.pendingRemoval = &RemovalTarget{Scope: s.scope, ID: id}
	return true
}

func (s *Session) CancelRemoval() {
	s.mu.Lock()
	s.pendingRemoval = nil
	s.mu.Unlock()
}

// ConfirmRemoval consumes the confirmation before dispatch, then rechecks the
// active scope, snapshot membership, and eligibility immediately before I/O.
func (s *Session) ConfirmRemoval(ctx context.Context) {
	s.mu.Lock()
	target := s.pendingRemoval
	s.pendingRemoval = nil
	if target == nil || target.Scope != s.scope || !s.tabs[target.Scope].Available || !s.canRemoveLocked(target.Scope, target.ID) {
		s.mu.Unlock()
		return
	}
	verb := removeVerb(target.Scope)
	key := actionKey{scope: target.Scope, verb: verb, id: target.ID}
	if _, exists := s.inFlight[key]; exists {
		s.mu.Unlock()
		return
	}
	s.inFlight[key] = struct{}{}
	s.mu.Unlock()

	var err error
	switch target.Scope {
	case ScopeContainers:
		err = s.docker.Remove(ctx, target.ID)
	case ScopeImages:
		err = s.docker.Rmi(ctx, target.ID)
	case ScopeVolumes:
		err = s.docker.VolRm(ctx, target.ID)
	case ScopeNetworks:
		err = s.docker.NetRm(ctx, target.ID)
	}

	s.mu.Lock()
	delete(s.inFlight, key)
	if err != nil {
		s.actErr = err.Error()
	} else {
		s.actErr = ""
	}
	s.mu.Unlock()
	s.RefreshTab(ctx, target.Scope)
}

func removeVerb(scope Scope) string {
	switch scope {
	case ScopeImages:
		return "rmi"
	case ScopeVolumes:
		return "volrm"
	case ScopeNetworks:
		return "netrm"
	default:
		return "remove"
	}
}

func (s *Session) canRemoveLocked(scope Scope, id string) bool {
	if !s.hasEntityLocked(scope, id) {
		return false
	}
	switch scope {
	case ScopeContainers:
		for _, item := range s.containers {
			if item.ID == id {
				return !item.Running()
			}
		}
	case ScopeImages:
		for _, item := range s.images {
			if item.ID == id {
				return item.Containers <= 0
			}
		}
	case ScopeVolumes:
		return true
	case ScopeNetworks:
		for _, item := range s.networks {
			if item.ID == id {
				return item.Name != "bridge" && item.Name != "host" && item.Name != "none"
			}
		}
	}
	return false
}

func validEntityID(id string) bool { return entityIDRE.MatchString(id) }

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
