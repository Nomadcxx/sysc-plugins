package minidocker

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
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

type RunDraft struct {
	ImageID     string
	ImageRef    string
	Name        string
	Port        string
	Publish     bool
	Network     string
	Environment string
	Error       string
	Reseed      uint64
}

type actionKey struct {
	scope Scope
	verb  string
	id    string
}

var entityIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$`)
var containerNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var decimalDigitsRE = regexp.MustCompile(`^[0-9]+$`)
var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	RunForm        *RunDraft
}

// Session owns Docker snapshots and user interaction state. Docker calls run
// without mu held so view reads and independent tab state remain responsive.
type Session struct {
	mu sync.Mutex
	// ponytail: one process-wide gate enforces the one-refresh-at-a-time
	// contract; split by tab only if the contract changes to permit overlap.
	refreshMu      sync.Mutex
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
	defaultNetwork string
	runForm        *RunDraft
	formReseed     uint64
	portInUse      func(int) (bool, error)
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
		inFlight:       make(map[actionKey]struct{}),
		defaultNetwork: "bridge",
		portInUse:      hostPortInUse,
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
		s.runForm = nil
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
		RunForm:        cloneRunDraft(s.runForm),
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
	s.RefreshWithStart(ctx, nil)
}

// RefreshWithStart reports Loading after updating the state and before Docker
// I/O starts, so callers can publish the last-known snapshot with its status.
func (s *Session) RefreshWithStart(ctx context.Context, started func()) {
	s.RefreshTabWithStart(ctx, ScopeContainers, started)
}

func (s *Session) RefreshTab(ctx context.Context, scope Scope) {
	s.RefreshTabWithStart(ctx, scope, nil)
}

// RefreshTabWithStart serializes refreshes and leaves the state lock free
// during Docker I/O and the optional start notification.
func (s *Session) RefreshTabWithStart(ctx context.Context, scope Scope, started func()) {
	if !validScope(scope) {
		return
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	status := s.tabs[scope]
	status.Loading = true
	s.tabs[scope] = status
	s.mu.Unlock()
	if started != nil {
		started()
	}

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

func cloneRunDraft(draft *RunDraft) *RunDraft {
	if draft == nil {
		return nil
	}
	copy := *draft
	return &copy
}

// SetDefaultNetwork records the preferred network used by the next run form.
func (s *Session) SetDefaultNetwork(name string) {
	s.mu.Lock()
	s.defaultNetwork = name
	s.mu.Unlock()
}

// OpenRunForm opens a draft for the selected image. Port inspection is
// optional; Docker can still run the image when inspect is unavailable.
func (s *Session) OpenRunForm(ctx context.Context, id string) bool {
	s.mu.Lock()
	_, ok := s.selectedImageLocked(id)
	if !ok {
		s.mu.Unlock()
		return false
	}
	loadNetworks := !s.tabs[ScopeNetworks].Available
	s.mu.Unlock()
	if loadNetworks {
		s.RefreshTab(ctx, ScopeNetworks)
	}

	s.mu.Lock()
	image, ok := s.selectedImageLocked(id)
	if !ok {
		s.mu.Unlock()
		return false
	}
	ref := imageReference(image)
	if !validEntityID(ref) {
		s.mu.Unlock()
		return false
	}
	s.formReseed++
	draft := &RunDraft{
		ImageID: id, ImageRef: ref, Network: s.defaultNetworkLocked(), Reseed: s.formReseed,
	}
	if draft.Network == "" {
		draft.Error = s.noNetworkErrorLocked()
	}
	s.runForm = draft
	s.mu.Unlock()

	ports, err := s.docker.ImageExposedPorts(ctx, ref)
	if err == nil {
		slices.Sort(ports)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runForm == nil || s.runForm.Reseed != draft.Reseed || !s.tabs[ScopeImages].Available || !s.hasEntityLocked(ScopeImages, id) {
		return false
	}
	if err == nil && s.runForm.Port == "" {
		for _, port := range ports {
			if port >= 1 && port <= 65535 {
				s.runForm.Port = strconv.Itoa(port)
				break
			}
		}
	}
	s.refreshRunFormErrorLocked()
	return true
}

func (s *Session) selectedImageLocked(id string) (Image, bool) {
	if s.scope != ScopeImages || s.selectedID != id || !s.tabs[ScopeImages].Available || !validEntityID(id) {
		return Image{}, false
	}
	for _, image := range s.images {
		if image.ID == id {
			return image, true
		}
	}
	return Image{}, false
}

func imageReference(image Image) string {
	if image.Repository == "" || image.Repository == "<none>" || image.Tag == "" || image.Tag == "<none>" {
		return image.ID
	}
	return image.Repository + ":" + image.Tag
}

func (s *Session) defaultNetworkLocked() string {
	if !s.tabs[ScopeNetworks].Available || len(s.networks) == 0 {
		return ""
	}
	networks := sortedNetworks(s.networks)
	for _, network := range networks {
		if network.Name == s.defaultNetwork {
			return network.Name
		}
	}
	return networks[0].Name
}

func (s *Session) noNetworkErrorLocked() string {
	if err := s.tabs[ScopeNetworks].ListError; err != "" {
		return err
	}
	return "No networks available"
}

func sortedNetworks(networks []Network) []Network {
	sorted := slices.Clone(networks)
	slices.SortFunc(sorted, func(a, b Network) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return sorted
}

// UpdateRunField stores a committed value from one of the fixed form inputs.
func (s *Session) UpdateRunField(field, value string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runForm == nil {
		return false
	}
	if len(value) > v1.MaxTextBytes {
		s.runForm.Error = fmt.Sprintf("Input exceeds %d bytes", v1.MaxTextBytes)
		return false
	}
	switch field {
	case "name":
		s.runForm.Name = value
	case "port":
		s.runForm.Port = value
	case "env":
		s.runForm.Environment = value
	default:
		return false
	}
	s.refreshRunFormErrorLocked()
	return true
}

func (s *Session) ToggleRunPublish() {
	s.mu.Lock()
	if s.runForm != nil {
		s.runForm.Publish = !s.runForm.Publish
		s.refreshRunFormErrorLocked()
	}
	s.mu.Unlock()
}

func (s *Session) CycleRunNetwork() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runForm == nil {
		return
	}
	if !s.tabs[ScopeNetworks].Available || len(s.networks) == 0 {
		s.runForm.Network = ""
		s.runForm.Error = s.noNetworkErrorLocked()
		return
	}
	networks := sortedNetworks(s.networks)
	for i, network := range networks {
		if network.Name == s.runForm.Network {
			s.runForm.Network = networks[(i+1)%len(networks)].Name
			s.refreshRunFormErrorLocked()
			return
		}
	}
	s.runForm.Network = networks[0].Name
	s.refreshRunFormErrorLocked()
}

func (s *Session) CancelRunForm() {
	s.mu.Lock()
	s.runForm = nil
	s.mu.Unlock()
}

func (s *Session) refreshRunFormErrorLocked() {
	if s.runForm == nil {
		return
	}
	_, err := s.validateRunFormLocked(*s.runForm)
	if err != nil {
		s.runForm.Error = err.Error()
	} else {
		s.runForm.Error = ""
	}
}

func (s *Session) validateRunFormLocked(draft RunDraft) (RunOpts, error) {
	if s.scope != ScopeImages || !s.tabs[ScopeImages].Available || !s.hasEntityLocked(ScopeImages, draft.ImageID) {
		return RunOpts{}, fmt.Errorf("selected image is no longer available")
	}
	if draft.ImageRef == "" || !validEntityID(draft.ImageRef) {
		return RunOpts{}, fmt.Errorf("invalid image reference")
	}
	if draft.Name != "" && !containerNameRE.MatchString(draft.Name) {
		return RunOpts{}, fmt.Errorf("container name must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
	}
	if draft.Port != "" {
		if !decimalDigitsRE.MatchString(draft.Port) {
			return RunOpts{}, fmt.Errorf("port must be an integer from 1 to 65535")
		}
		port, err := strconv.Atoi(draft.Port)
		if err != nil || port < 1 || port > 65535 {
			return RunOpts{}, fmt.Errorf("port must be an integer from 1 to 65535")
		}
	}
	if len(draft.Environment) > v1.MaxTextBytes {
		return RunOpts{}, fmt.Errorf("environment exceeds %d bytes", v1.MaxTextBytes)
	}
	environment, err := parseEnvironment(draft.Environment)
	if err != nil {
		return RunOpts{}, err
	}
	if !s.tabs[ScopeNetworks].Available || len(s.networks) == 0 {
		return RunOpts{}, fmt.Errorf("%s", s.noNetworkErrorLocked())
	}
	knownNetwork := false
	for _, network := range s.networks {
		if network.Name == draft.Network {
			knownNetwork = true
			break
		}
	}
	if !knownNetwork {
		return RunOpts{}, fmt.Errorf("network %q is no longer available", draft.Network)
	}
	return RunOpts{
		Image: draft.ImageRef, Name: draft.Name, Environment: environment,
		Port: draft.Port, Publish: draft.Publish, Network: draft.Network,
	}, nil
}

func parseEnvironment(text string) ([]string, error) {
	if len(text) > v1.MaxTextBytes {
		return nil, fmt.Errorf("environment exceeds %d bytes", v1.MaxTextBytes)
	}
	var environment []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !envKeyRE.MatchString(key) || value == "" {
			return nil, fmt.Errorf("environment line %d must be KEY=value with a non-empty value", i+1)
		}
		environment = append(environment, key+"="+value)
	}
	return environment, nil
}

func (s *Session) SubmitRun(ctx context.Context) bool {
	return s.SubmitRunWithStart(ctx, nil)
}

// SubmitRunWithStart calls started after recording the in-flight action and
// releasing the state lock, so the renderer can show the disabled Run button
// before Docker I/O begins.
func (s *Session) SubmitRunWithStart(ctx context.Context, started func()) bool {
	s.mu.Lock()
	if s.runForm == nil {
		s.mu.Unlock()
		return false
	}
	draft := *s.runForm
	opts, err := s.validateRunFormLocked(draft)
	if err != nil {
		s.runForm.Error = err.Error()
		s.mu.Unlock()
		return false
	}
	key := actionKey{scope: ScopeImages, verb: "run", id: draft.ImageID}
	if _, exists := s.inFlight[key]; exists {
		s.mu.Unlock()
		return false
	}
	s.inFlight[key] = struct{}{}
	s.runForm.Error = ""
	portInUse := s.portInUse
	s.mu.Unlock()
	if started != nil {
		started()
	}

	if draft.Publish && draft.Port != "" {
		port, _ := strconv.Atoi(draft.Port)
		occupied, checkErr := portInUse(port)
		if checkErr != nil {
			s.finishRunAttempt(key, draft, fmt.Errorf("Port preflight failed: %w", checkErr))
			return false
		}
		if occupied {
			s.finishRunAttempt(key, draft, fmt.Errorf("Port %s is already in use on the host", draft.Port))
			return false
		}
	}

	err = s.docker.Run(ctx, opts)
	s.finishRunAttempt(key, draft, err)
	if err != nil {
		return false
	}
	s.RefreshWithStart(ctx, started)
	s.RefreshTabWithStart(ctx, ScopeImages, started)
	return true
}

func (s *Session) finishRunAttempt(key actionKey, draft RunDraft, err error) {
	s.mu.Lock()
	delete(s.inFlight, key)
	if s.runForm != nil && s.runForm.Reseed == draft.Reseed {
		if err != nil {
			s.runForm.Error = err.Error()
		} else {
			s.runForm = nil
		}
	} else if err != nil {
		s.actErr = err.Error()
	} else {
		s.actErr = ""
	}
	s.mu.Unlock()
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
	if s.runForm != nil && (scope == ScopeImages || scope == ScopeNetworks) {
		s.refreshRunFormErrorLocked()
	}
}

// Act accepts only a current, eligible container action. It snapshots the
// choice under the lock, then performs Docker I/O without holding the lock.
func (s *Session) Act(ctx context.Context, action, id string) {
	s.ActWithStart(ctx, action, id, nil)
}

// ActWithStart publishes the in-flight state before Docker performs the
// container action. The callback always runs without the session lock held.
func (s *Session) ActWithStart(ctx context.Context, action, id string, started func()) {
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
	if started != nil {
		started()
	}

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
	s.RefreshWithStart(ctx, started)
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
	s.ConfirmRemovalWithStart(ctx, nil)
}

// ConfirmRemovalWithStart publishes the in-flight confirmation after the
// target is consumed and validated but before the Docker command starts.
func (s *Session) ConfirmRemovalWithStart(ctx context.Context, started func()) {
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
	if started != nil {
		started()
	}

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
	s.RefreshTabWithStart(ctx, target.Scope, started)
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
