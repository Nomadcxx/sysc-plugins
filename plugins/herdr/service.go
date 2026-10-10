package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Notification is a single user-facing alert produced by the service.
type Notification struct {
	Title   string
	Body    string
	Summary bool
}

// Options configures a Service. Zero-value durations get sane defaults and nil
// seams fall back to the real implementations in discover.go / socket.go.
type Options struct {
	Bin            string
	SocketTimeout  time.Duration
	PollInterval   time.Duration
	NotifyDebounce time.Duration
	SessionLog     func(format string, args ...any)

	// Test seams (nil → real implementations).
	List      func(ctx context.Context, bin string) ([]SessionInfo, error)
	Snapshot  func(ctx context.Context, sock string) (*SnapshotDoc, error)
	Subscribe func(ctx context.Context, sock string, subs []SubSpec) (*Subscription, error)
	Since     func() time.Time
	Now       func() time.Time
}

// globalSubTypes are the workspace/tab/pane lifecycle events subscribed on
// every session. pane.agent_status_changed is added per known pane.
var globalSubTypes = []string{
	"pane.created",
	"pane.closed",
	"pane.updated",
	"pane.focused",
	"workspace.created",
	"workspace.updated",
	"workspace.renamed",
	"workspace.closed",
	"workspace.focused",
	"tab.created",
	"tab.updated",
	"tab.closed",
	"tab.focused",
}

const (
	sessionRefreshInterval = 30 * time.Second
	notifyBuffer           = 16
	maxBackoff             = 30 * time.Second
)

// sessionState is the per-session bookkeeping. All fields except refresh are
// guarded by Service.mu. ctx and started track the per-session runtime, which
// runs only while the session is reported as running.
type sessionState struct {
	ctx      context.Context
	info     SessionInfo
	cancel   context.CancelFunc
	started  bool
	refresh  chan struct{}
	doc      *SnapshotDoc
	since    map[string]time.Time
	statuses map[string]Status
	baseline bool
	stale    bool
	err      string
}

type pendingNote struct {
	note Notification
	kind Status
}

// Service polls herdr sessions and streams their agent-status events,
// producing a render model and debounced notifications.
type Service struct {
	bin            string
	socketTimeout  time.Duration
	pollInterval   time.Duration
	notifyDebounce time.Duration
	log            func(string, ...any)

	listFn      func(ctx context.Context, bin string) ([]SessionInfo, error)
	snapshotFn  func(ctx context.Context, sock string) (*SnapshotDoc, error)
	subscribeFn func(ctx context.Context, sock string, subs []SubSpec) (*Subscription, error)
	sinceFn     func() time.Time
	nowFn       func() time.Time
	sleepFn     func(ctx context.Context, d time.Duration) bool

	mu       sync.Mutex
	model    Model
	settings Settings
	sessions map[string]*sessionState
	stopped  map[string][]StoppedWorkspace

	updates       chan struct{}
	notifications chan Notification
	refreshCh     chan struct{}
	discoveryCh   chan discovery
	closeCh       chan struct{}
	closeOnce     sync.Once
	listing       atomic.Bool

	notifyMu    sync.Mutex
	notifyQueue []pendingNote
	notifyTimer *time.Timer
}

type discovery struct {
	infos []SessionInfo
	err   error
}

// NewService builds a Service from o, applying defaults for unset fields.
func NewService(o Options) *Service {
	s := &Service{
		bin:            o.Bin,
		socketTimeout:  o.SocketTimeout,
		pollInterval:   o.PollInterval,
		notifyDebounce: o.NotifyDebounce,
		log:            o.SessionLog,
		listFn:         o.List,
		snapshotFn:     o.Snapshot,
		subscribeFn:    o.Subscribe,
		sinceFn:        o.Since,
		nowFn:          o.Now,
		settings:       DefaultSettings(),
		sessions:       map[string]*sessionState{},
		stopped:        map[string][]StoppedWorkspace{},
		updates:        make(chan struct{}, 1),
		notifications:  make(chan Notification, notifyBuffer),
		refreshCh:      make(chan struct{}, 1),
		discoveryCh:    make(chan discovery, 1),
		closeCh:        make(chan struct{}),
	}
	if s.bin == "" {
		s.bin = DefaultHerdrBin()
	}
	if s.socketTimeout <= 0 {
		s.socketTimeout = 6 * time.Second
	}
	if s.pollInterval <= 0 {
		s.pollInterval = 5 * time.Second
	}
	if s.notifyDebounce <= 0 {
		s.notifyDebounce = 500 * time.Millisecond
	}
	if s.listFn == nil {
		s.listFn = ListSessions
	}
	if s.snapshotFn == nil {
		s.snapshotFn = defaultSnapshot
	}
	if s.subscribeFn == nil {
		s.subscribeFn = SubscribeTo
	}
	if s.sinceFn == nil {
		s.sinceFn = time.Now
	}
	if s.nowFn == nil {
		s.nowFn = time.Now
	}
	s.sleepFn = sleepCtx
	return s
}

func defaultSnapshot(ctx context.Context, sock string) (*SnapshotDoc, error) {
	// The session.snapshot reply's result is an envelope
	// {"type":"session_snapshot","snapshot":{...}} (herdr v0.9.1); the document
	// itself is nested under "snapshot".
	var env struct {
		Snapshot SnapshotDoc `json:"snapshot"`
	}
	if err := Call(ctx, sock, "session.snapshot", map[string]any{}, &env); err != nil {
		return nil, err
	}
	return &env.Snapshot, nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.log != nil {
		s.log(format, args...)
	}
}

// Start blocks running discovery + per-session event runtimes until ctx is
// cancelled or Close is called.
func (s *Service) Start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.closeCh:
			cancel()
		case <-runCtx.Done():
		}
	}()

	s.kickDiscovery(runCtx)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-runCtx.Done():
			s.shutdownSessions()
			return
		case d := <-s.discoveryCh:
			s.handleDiscovery(runCtx, d)
		case <-ticker.C:
			s.kickDiscovery(runCtx)
		case <-s.refreshCh:
			s.kickDiscovery(runCtx)
		}
	}
}

// SetSettings replaces the plugin settings used for notification gating.
func (s *Service) SetSettings(set Settings) {
	s.mu.Lock()
	s.settings = set
	s.mu.Unlock()
	s.signalUpdate()
}

// Refresh forces an immediate rediscovery and per-session resubscribe.
func (s *Service) Refresh() {
	select {
	case s.refreshCh <- struct{}{}:
	default:
	}
	s.mu.Lock()
	for _, st := range s.sessions {
		select {
		case st.refresh <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
}

// Model returns a deep copy safe for renderers to read without locking.
func (s *Service) Model() *Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &Model{HerdrMissing: s.model.HerdrMissing, DiscoveryErr: s.model.DiscoveryErr}
	m.Sessions = make([]SessionRow, len(s.model.Sessions))
	for i, sr := range s.model.Sessions {
		m.Sessions[i] = copySessionRow(sr)
	}
	return m
}

func copySessionRow(sr SessionRow) SessionRow {
	out := sr
	out.StoppedWorkspaces = append([]string(nil), sr.StoppedWorkspaces...)
	out.Workspaces = make([]WorkspaceRow, len(sr.Workspaces))
	for i, w := range sr.Workspaces {
		out.Workspaces[i] = w
		out.Workspaces[i].Panes = append([]PaneRow(nil), w.Panes...)
	}
	return out
}

// Updates returns a coalesced wake-up channel pinged on any visible change.
func (s *Service) Updates() <-chan struct{} { return s.updates }

// Notifications returns the debounced notification stream.
func (s *Service) Notifications() <-chan Notification { return s.notifications }

// Close stops the service. Safe to call multiple times.
func (s *Service) Close() {
	s.closeOnce.Do(func() { close(s.closeCh) })
}

func (s *Service) signalUpdate() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// --- discovery -------------------------------------------------------------

func (s *Service) kickDiscovery(ctx context.Context) {
	if !s.listing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.listing.Store(false)
		lctx, cancel := context.WithTimeout(ctx, s.socketTimeout)
		defer cancel()
		infos, err := s.listFn(lctx, s.bin)
		select {
		case s.discoveryCh <- discovery{infos: infos, err: err}:
		case <-ctx.Done():
		}
	}()
}

func (s *Service) handleDiscovery(ctx context.Context, d discovery) {
	if d.err != nil {
		s.mu.Lock()
		if errors.Is(d.err, ErrHerdrMissing) {
			s.model.HerdrMissing = true
		} else {
			s.model.DiscoveryErr = d.err.Error()
		}
		s.mu.Unlock()
		s.signalUpdate()
		return
	}
	s.applyDiscovery(ctx, d.infos)
}

func (s *Service) applyDiscovery(ctx context.Context, infos []SessionInfo) {
	s.mu.Lock()
	s.model.HerdrMissing = false
	s.model.DiscoveryErr = ""

	seen := make(map[string]bool, len(infos))
	for _, info := range infos {
		seen[info.Name] = true
		st, ok := s.sessions[info.Name]
		if !ok || st.info.Socket != info.Socket {
			if ok {
				st.cancel()
			}
			cctx, cancel := context.WithCancel(ctx)
			st = &sessionState{
				ctx:      cctx,
				info:     info,
				cancel:   cancel,
				started:  info.Running,
				refresh:  make(chan struct{}, 1),
				since:    map[string]time.Time{},
				statuses: map[string]Status{},
			}
			s.sessions[info.Name] = st
			// A stopped session has no socket to probe: skip the runtime until
			// herdr reports it running.
			if st.started {
				go s.runSession(cctx, st, info.Socket)
			}
		} else {
			st.info = info
			switch {
			case info.Running && !st.started:
				st.started = true
				st.ctx, st.cancel = context.WithCancel(ctx)
				go s.runSession(st.ctx, st, info.Socket)
			case !info.Running && st.started:
				st.started = false
				st.cancel()
			}
		}
	}
	for name, st := range s.sessions {
		if !seen[name] {
			st.cancel()
			delete(s.sessions, name)
		}
	}

	rows := make([]SessionRow, 0, len(infos))
	for _, info := range infos {
		st := s.sessions[info.Name]
		if st == nil {
			continue
		}
		rows = append(rows, s.buildRowLocked(st))
	}
	s.model.Sessions = rows
	s.model.Sort()
	s.mu.Unlock()
	s.signalUpdate()
}

func (s *Service) buildRowLocked(st *sessionState) SessionRow {
	row := BuildSession(st.info, st.doc, s.stoppedLocked(st.info), st.since, s.nowFn())
	row.Stale = st.stale
	row.Err = st.err
	return row
}

func (s *Service) rebuildLocked(st *sessionState) {
	row := s.buildRowLocked(st)
	for i := range s.model.Sessions {
		if s.model.Sessions[i].Name == st.info.Name {
			s.model.Sessions[i] = row
			s.model.Sort()
			return
		}
	}
	s.model.Sessions = append(s.model.Sessions, row)
	s.model.Sort()
}

// stoppedLocked returns cached stopped workspaces for a non-running session.
func (s *Service) stoppedLocked(info SessionInfo) []StoppedWorkspace {
	if info.Running || info.Dir == "" {
		return nil
	}
	if ws, ok := s.stopped[info.Dir]; ok {
		return ws
	}
	ws, _ := LoadStopped(info.Dir)
	s.stopped[info.Dir] = ws
	return ws
}

func (s *Service) shutdownSessions() {
	s.mu.Lock()
	for name, st := range s.sessions {
		st.cancel()
		delete(s.sessions, name)
	}
	s.mu.Unlock()
}

// --- per-session runtime ---------------------------------------------------

func (s *Service) runSession(ctx context.Context, st *sessionState, sock string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}

		snapCtx, cancel := context.WithTimeout(ctx, s.socketTimeout)
		doc, err := s.snapshotFn(snapCtx, sock)
		cancel()
		if err != nil {
			s.markSessionErr(st, err)
			if !s.sleepFn(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		s.applySnapshot(st, doc, s.sinceFn())
		backoff = time.Second

		subCtx, sc := context.WithTimeout(ctx, s.socketTimeout)
		sub, err := s.subscribeFn(subCtx, sock, s.buildSubs(st))
		sc()
		if err != nil {
			s.markSessionErr(st, err)
			if !s.sleepFn(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		resub := s.streamEvents(ctx, st, sub)
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		if !resub {
			s.markSessionErr(st, io.ErrUnexpectedEOF)
			if !s.sleepFn(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
		}
	}
}

// streamEvents returns true when the caller should immediately reconnect and
// resubscribe (pane set changed, refresh requested, periodic refresh) and false
// when the stream closed and a backoff reconnect is needed.
func (s *Service) streamEvents(ctx context.Context, st *sessionState, sub *Subscription) bool {
	pumpCtx, pumpCancel := context.WithCancel(ctx)
	defer pumpCancel()

	events := make(chan Event, 8)
	go func() {
		defer close(events)
		for {
			ev, ok := sub.Next()
			if !ok {
				return
			}
			select {
			case events <- ev:
			case <-pumpCtx.Done():
				return
			}
		}
	}()

	refresh := time.NewTicker(sessionRefreshInterval)
	defer refresh.Stop()

	for {
		select {
		case <-ctx.Done():
			return true
		case <-st.refresh:
			return true
		case <-refresh.C:
			return true
		case ev, ok := <-events:
			if !ok {
				return false
			}
			switch ev.Type {
			case "pane.created", "pane.closed":
				return true
			case "pane.agent_status_changed":
				s.handleAgentEvent(st, ev)
			}
		}
	}
}

func (s *Service) buildSubs(st *sessionState) []SubSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(globalSubTypes)
	if st.doc != nil {
		n += len(st.doc.Panes)
	}
	subs := make([]SubSpec, 0, n)
	for _, t := range globalSubTypes {
		subs = append(subs, SubSpec{Type: t})
	}
	if st.doc != nil {
		for _, p := range st.doc.Panes {
			subs = append(subs, SubSpec{Type: "pane.agent_status_changed", PaneID: p.PaneID})
		}
	}
	return subs
}

func (s *Service) markSessionErr(st *sessionState, err error) {
	s.mu.Lock()
	st.stale = true
	st.err = err.Error()
	s.rebuildLocked(st)
	s.mu.Unlock()
	s.signalUpdate()
}

// applySnapshot installs a fresh doc and (re-)baselines statuses without
// emitting notifications.
func (s *Service) applySnapshot(st *sessionState, doc *SnapshotDoc, now time.Time) {
	statuses := paneStatuses(doc)
	s.mu.Lock()
	if st.statuses == nil {
		st.statuses = map[string]Status{}
	}
	if st.since == nil {
		st.since = map[string]time.Time{}
	}
	for id, cur := range statuses {
		if old, ok := st.statuses[id]; !ok || old != cur {
			st.since[id] = now
		}
	}
	for id := range st.statuses {
		if _, ok := statuses[id]; !ok {
			delete(st.since, id)
		}
	}
	st.statuses = statuses
	st.doc = doc
	st.baseline = true
	st.stale = false
	st.err = ""
	s.rebuildLocked(st)
	s.mu.Unlock()
	s.signalUpdate()
}

func (s *Service) handleAgentEvent(st *sessionState, ev Event) {
	var payload struct {
		PaneID      string `json:"pane_id"`
		AgentStatus string `json:"agent_status"`
	}
	if err := json.Unmarshal(ev.Data, &payload); err != nil || payload.PaneID == "" {
		return
	}
	newStatus := normalizeStatus(payload.AgentStatus)
	now := s.sinceFn()

	s.mu.Lock()
	old, had := st.statuses[payload.PaneID]
	if had && old == newStatus {
		s.mu.Unlock()
		return
	}
	st.statuses[payload.PaneID] = newStatus
	st.since[payload.PaneID] = now
	setPaneStatus(st.doc, payload.PaneID, newStatus)

	notify := st.baseline && had && old == StatusWorking &&
		((newStatus == StatusBlocked && s.settings.NotifyOnBlocked) ||
			(newStatus == StatusDone && s.settings.NotifyOnDone))
	detail := paneDetail(st.info.Name, st.doc, payload.PaneID)
	s.rebuildLocked(st)
	s.mu.Unlock()

	if notify {
		s.queueNotification(Notification{Title: "Agent " + string(newStatus), Body: detail}, newStatus)
	}
	s.signalUpdate()
}

// --- status helpers --------------------------------------------------------

func paneStatuses(doc *SnapshotDoc) map[string]Status {
	out := map[string]Status{}
	if doc == nil {
		return out
	}
	agents := make(map[string]*AgentInfo, len(doc.Agents))
	for i := range doc.Agents {
		agents[doc.Agents[i].PaneID] = &doc.Agents[i]
	}
	for _, p := range doc.Panes {
		if hasAgent(p, agents[p.PaneID]) {
			out[p.PaneID] = paneStatus(p, agents[p.PaneID])
		}
	}
	return out
}

func setPaneStatus(doc *SnapshotDoc, paneID string, st Status) {
	if doc == nil {
		return
	}
	for i := range doc.Panes {
		if doc.Panes[i].PaneID == paneID {
			doc.Panes[i].AgentStatus = string(st)
		}
	}
	for i := range doc.Agents {
		if doc.Agents[i].PaneID == paneID {
			doc.Agents[i].AgentStatus = string(st)
		}
	}
}

func normalizeStatus(raw string) Status {
	switch Status(raw) {
	case StatusBlocked, StatusDone, StatusWorking, StatusIdle, StatusUnknown:
		return Status(raw)
	default:
		return StatusUnknown
	}
}

func paneDetail(session string, doc *SnapshotDoc, paneID string) string {
	if doc == nil {
		return session
	}
	var pane *PaneInfo
	for i := range doc.Panes {
		if doc.Panes[i].PaneID == paneID {
			pane = &doc.Panes[i]
			break
		}
	}
	if pane == nil {
		return session
	}
	agents := make(map[string]*AgentInfo, len(doc.Agents))
	for i := range doc.Agents {
		agents[doc.Agents[i].PaneID] = &doc.Agents[i]
	}
	var wsLabel string
	for _, w := range doc.Workspaces {
		if w.WorkspaceID == pane.WorkspaceID {
			wsLabel = workspaceLabel(w)
			break
		}
	}
	parts := []string{session}
	if wsLabel != "" {
		parts = append(parts, wsLabel)
	}
	if title := PaneTitle(*pane, agents[paneID]); title != "" {
		parts = append(parts, title)
	}
	return strings.Join(parts, " · ")
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// --- notifications ---------------------------------------------------------

func (s *Service) queueNotification(n Notification, kind Status) {
	s.notifyMu.Lock()
	s.notifyQueue = append(s.notifyQueue, pendingNote{note: n, kind: kind})
	if s.notifyTimer == nil {
		s.notifyTimer = time.AfterFunc(s.notifyDebounce, s.flushNotifications)
	} else {
		s.notifyTimer.Reset(s.notifyDebounce)
	}
	s.notifyMu.Unlock()
}

func (s *Service) flushNotifications() {
	s.notifyMu.Lock()
	q := s.notifyQueue
	s.notifyQueue = nil
	s.notifyTimer = nil
	s.notifyMu.Unlock()

	switch len(q) {
	case 0:
		return
	case 1:
		n := q[0].note
		n.Summary = false
		s.emit(n)
	default:
		var blocked, done int
		for _, p := range q {
			switch p.kind {
			case StatusBlocked:
				blocked++
			case StatusDone:
				done++
			}
		}
		s.emit(Notification{
			Title:   fmt.Sprintf("%d agents need attention", len(q)),
			Body:    summaryBody(blocked, done),
			Summary: true,
		})
	}
}

func summaryBody(blocked, done int) string {
	var parts []string
	if blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", blocked))
	}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	return strings.Join(parts, " · ")
}

func (s *Service) emit(n Notification) {
	select {
	case s.notifications <- n:
	default:
		s.logf("herdr: notification dropped, buffer full")
	}
}
