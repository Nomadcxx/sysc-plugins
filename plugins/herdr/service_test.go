package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// --- deterministic fakes ---------------------------------------------------

type listResult struct {
	infos []SessionInfo
	err   error
}

type snapResult struct {
	doc *SnapshotDoc
	err error
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// testSub is the server side of one fake subscription stream.
type testSub struct {
	subs []SubSpec
	srv  net.Conn
	mu   sync.Mutex
}

func (ts *testSub) emit(t *testing.T, typ string, payload map[string]any) {
	t.Helper()
	line, err := json.Marshal(map[string]any{"event": typ, "data": payload})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_ = ts.srv.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := ts.srv.Write(append(line, '\n')); err != nil {
		t.Logf("emit %s: %v", typ, err)
	}
}

func (ts *testSub) close() { _ = ts.srv.Close() }

type harness struct {
	t        *testing.T
	clock    *fakeClock
	debounce time.Duration

	mu      sync.Mutex
	lists   []listResult
	listIdx int
	snaps   []snapResult
	snapIdx int
	subs    []*testSub

	svc    *Service
	cancel context.CancelFunc
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{
		t:        t,
		clock:    &fakeClock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)},
		debounce: 30 * time.Millisecond,
	}
}

func (h *harness) setLists(rs ...listResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lists, h.listIdx = rs, 0
}

func (h *harness) setSnaps(rs ...snapResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.snaps, h.snapIdx = rs, 0
}

func (h *harness) list(_ context.Context, _ string) ([]SessionInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listIdx < len(h.lists) {
		r := h.lists[h.listIdx]
		h.listIdx++
		return r.infos, r.err
	}
	if len(h.lists) > 0 {
		return h.lists[len(h.lists)-1].infos, h.lists[len(h.lists)-1].err
	}
	return nil, nil
}

func (h *harness) snapshot(_ context.Context, _ string) (*SnapshotDoc, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.snapIdx < len(h.snaps) {
		r := h.snaps[h.snapIdx]
		h.snapIdx++
		return r.doc, r.err
	}
	if len(h.snaps) > 0 {
		return h.snaps[len(h.snaps)-1].doc, h.snaps[len(h.snaps)-1].err
	}
	return nil, errors.New("no snapshot scripted")
}

func (h *harness) subscribe(ctx context.Context, _ string, subs []SubSpec) (*Subscription, error) {
	c, srv := net.Pipe()
	ts := &testSub{subs: append([]SubSpec(nil), subs...), srv: srv}
	h.mu.Lock()
	h.subs = append(h.subs, ts)
	h.mu.Unlock()
	go func() {
		br := bufio.NewReader(srv)
		if _, err := br.ReadString('\n'); err != nil {
			return
		}
		_, _ = srv.Write([]byte(`{"id":"c1","result":{}}` + "\n"))
	}()
	sub, err := subscribeConn(ctx, c, subs)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return sub, nil
}

func (h *harness) start() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.svc = NewService(Options{
		Bin:            "/fake/herdr",
		SocketTimeout:  2 * time.Second,
		PollInterval:   time.Hour,
		NotifyDebounce: h.debounce,
		List:           h.list,
		Snapshot:       h.snapshot,
		Subscribe:      h.subscribe,
		Now:            h.clock.Now,
		Since:          h.clock.Now,
	})
	h.svc.sleepFn = func(ctx context.Context, _ time.Duration) bool {
		return ctx.Err() == nil
	}
	go h.svc.Start(ctx)
	h.t.Cleanup(func() {
		cancel()
		h.svc.Close()
	})
}

func (h *harness) waitSub(n int) *testSub {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		if len(h.subs) >= n {
			s := h.subs[n-1]
			h.mu.Unlock()
			return s
		}
		h.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for subscription #%d", n)
	return nil
}

func (h *harness) waitModel(cond func(*Model) bool) *Model {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m := h.svc.Model()
		if cond(m) {
			return m
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for model; last=%+v", h.svc.Model())
	return nil
}

func (h *harness) waitNotification() Notification {
	h.t.Helper()
	select {
	case n := <-h.svc.Notifications():
		return n
	case <-time.After(3 * time.Second):
		h.t.Fatal("timed out waiting for notification")
		return Notification{}
	}
}

func (h *harness) expectNoNotification() {
	h.t.Helper()
	select {
	case n := <-h.svc.Notifications():
		h.t.Fatalf("unexpected notification: %+v", n)
	case <-time.After(80 * time.Millisecond):
	}
}

// --- model helpers ---------------------------------------------------------

func hasPaneStatus(paneID string, st Status) func(*Model) bool {
	return func(m *Model) bool {
		for _, s := range m.Sessions {
			for _, w := range s.Workspaces {
				for _, p := range w.Panes {
					if p.PaneID == paneID && p.Status == st {
						return true
					}
				}
			}
		}
		return false
	}
}

func hasSessions(n int) func(*Model) bool {
	return func(m *Model) bool { return len(m.Sessions) == n }
}

func demoSession() SessionInfo {
	return SessionInfo{Name: "demo", Running: true, Socket: "/tmp/demo.sock"}
}

type testPane struct {
	id     string
	status string
}

func mkDoc(panes ...testPane) *SnapshotDoc {
	d := &SnapshotDoc{Version: "0.9.1", Protocol: 22}
	d.Workspaces = []WorkspaceInfo{{WorkspaceID: "w1", Label: "api"}}
	for _, p := range panes {
		d.Panes = append(d.Panes, PaneInfo{
			PaneID: p.id, WorkspaceID: "w1", TabID: "t1",
			Agent: "claude", AgentStatus: p.status, Title: "pane " + p.id,
		})
		d.Agents = append(d.Agents, AgentInfo{
			PaneID: p.id, WorkspaceID: "w1", TabID: "t1",
			Agent: "claude", AgentStatus: p.status, Title: "pane " + p.id,
		})
	}
	return d
}

func statusEvent(paneID, status string) map[string]any {
	return map[string]any{"agent": "claude", "agent_status": status, "pane_id": paneID, "workspace_id": "w1"}
}

func hasSpec(subs []SubSpec, typ, paneID string) bool {
	for _, s := range subs {
		if s.Type == typ && s.PaneID == paneID {
			return true
		}
	}
	return false
}

// --- tests -----------------------------------------------------------------

func TestBaselineDoesNotNotify(t *testing.T) {
	h := newHarness(t)
	h.setLists(listResult{infos: []SessionInfo{demoSession()}})
	h.setSnaps(snapResult{doc: mkDoc(testPane{"p1", "blocked"})})
	h.start()
	h.waitModel(hasPaneStatus("p1", StatusBlocked))

	h.expectNoNotification()

	sub := h.waitSub(1)
	sub.emit(t, "pane.agent_status_changed", statusEvent("p1", "working"))
	sub.emit(t, "pane.agent_status_changed", statusEvent("p1", "blocked"))

	n := h.waitNotification()
	if n.Title != "Agent blocked" {
		t.Errorf("title = %q, want Agent blocked", n.Title)
	}
	if n.Summary {
		t.Errorf("Summary = true, want false for single transition")
	}
	if n.Body == "" {
		t.Error("body empty, want pane detail")
	}
}

func TestBlockedTransitionNotifies(t *testing.T) {
	h := newHarness(t)
	h.setLists(listResult{infos: []SessionInfo{demoSession()}})
	h.setSnaps(snapResult{doc: mkDoc(testPane{"p1", "working"})})
	h.start()
	h.waitModel(hasPaneStatus("p1", StatusWorking))

	sub := h.waitSub(1)
	sub.emit(t, "pane.agent_status_changed", statusEvent("p1", "blocked"))

	if n := h.waitNotification(); n.Title != "Agent blocked" || n.Summary {
		t.Errorf("notification = %+v, want non-summary Agent blocked", n)
	}
}

func TestDoneTransitionHonorsSetting(t *testing.T) {
	h := newHarness(t)
	h.setLists(listResult{infos: []SessionInfo{demoSession()}})
	h.setSnaps(snapResult{doc: mkDoc(testPane{"p1", "working"})})
	h.start()
	h.waitModel(hasPaneStatus("p1", StatusWorking))

	sub := h.waitSub(1)
	sub.emit(t, "pane.agent_status_changed", statusEvent("p1", "done"))
	h.expectNoNotification()

	if s := DefaultSettings(); s.NotifyOnDone {
		t.Fatal("default NotifyOnDone should be false")
	}
	h.svc.SetSettings(Settings{NotifyOnBlocked: true, NotifyOnDone: true})

	sub.emit(t, "pane.agent_status_changed", statusEvent("p1", "working"))
	sub.emit(t, "pane.agent_status_changed", statusEvent("p1", "done"))
	if n := h.waitNotification(); n.Title != "Agent done" {
		t.Errorf("notification = %+v, want Agent done", n)
	}
}

func TestDebounceCoalesces(t *testing.T) {
	h := newHarness(t)
	h.setLists(listResult{infos: []SessionInfo{demoSession()}})
	h.setSnaps(snapResult{doc: mkDoc(testPane{"p1", "working"}, testPane{"p2", "working"})})
	h.start()
	h.waitModel(hasPaneStatus("p2", StatusWorking))

	sub := h.waitSub(1)
	sub.emit(t, "pane.agent_status_changed", statusEvent("p1", "blocked"))
	sub.emit(t, "pane.agent_status_changed", statusEvent("p2", "blocked"))

	n := h.waitNotification()
	if !n.Summary {
		t.Errorf("Summary = false, want true for coalesced transitions")
	}
	if n.Title != "2 agents need attention" {
		t.Errorf("title = %q, want 2 agents need attention", n.Title)
	}
	if n.Body != "2 blocked" {
		t.Errorf("body = %q, want 2 blocked", n.Body)
	}
	h.expectNoNotification()
}

func TestReconnectRebaselines(t *testing.T) {
	h := newHarness(t)
	h.setLists(listResult{infos: []SessionInfo{demoSession()}})
	h.setSnaps(
		snapResult{doc: mkDoc(testPane{"p1", "working"})},
		snapResult{doc: mkDoc(testPane{"p1", "done"})},
	)
	h.start()
	h.waitModel(hasPaneStatus("p1", StatusWorking))

	sub1 := h.waitSub(1)
	sub1.close()

	sub2 := h.waitSub(2)
	h.waitModel(hasPaneStatus("p1", StatusDone))
	h.expectNoNotification()

	sub2.emit(t, "pane.agent_status_changed", statusEvent("p1", "working"))
	sub2.emit(t, "pane.agent_status_changed", statusEvent("p1", "blocked"))
	if n := h.waitNotification(); n.Title != "Agent blocked" {
		t.Errorf("notification = %+v, want Agent blocked after rebaseline", n)
	}
}

func TestPaneCreatedTriggersResubscribe(t *testing.T) {
	h := newHarness(t)
	h.setLists(listResult{infos: []SessionInfo{demoSession()}})
	h.setSnaps(
		snapResult{doc: mkDoc(testPane{"p1", "working"})},
		snapResult{doc: mkDoc(testPane{"p1", "working"}, testPane{"p2", "working"})},
	)
	h.start()
	h.waitModel(hasPaneStatus("p1", StatusWorking))

	sub1 := h.waitSub(1)
	sub1.emit(t, "pane.created", map[string]any{"pane_id": "p2", "workspace_id": "w1"})

	sub2 := h.waitSub(2)
	if !hasSpec(sub2.subs, "pane.agent_status_changed", "p2") {
		t.Errorf("second subscribe missing p2 spec: %+v", sub2.subs)
	}
	if !hasSpec(sub2.subs, "pane.agent_status_changed", "p1") {
		t.Errorf("second subscribe missing p1 spec: %+v", sub2.subs)
	}
}

func TestHerdrMissingKeepsModel(t *testing.T) {
	h := newHarness(t)
	h.setLists(
		listResult{infos: []SessionInfo{demoSession()}},
		listResult{err: ErrHerdrMissing},
	)
	h.setSnaps(snapResult{doc: mkDoc(testPane{"p1", "working"})})
	h.start()
	h.waitModel(hasSessions(1))

	h.svc.Refresh()
	m := h.waitModel(func(m *Model) bool { return m.HerdrMissing })
	if len(m.Sessions) != 1 {
		t.Errorf("sessions = %d, want 1 kept after herdr missing", len(m.Sessions))
	}
}

func TestUpdatesChannelSignals(t *testing.T) {
	h := newHarness(t)
	h.setLists(listResult{infos: []SessionInfo{demoSession()}})
	h.setSnaps(snapResult{doc: mkDoc(testPane{"p1", "working"})})
	h.start()
	h.waitModel(hasSessions(1))

	for {
		select {
		case <-h.svc.Updates():
			continue
		default:
		}
		break
	}

	h.svc.SetSettings(Settings{NotifyOnBlocked: true})
	select {
	case <-h.svc.Updates():
	case <-time.After(time.Second):
		t.Fatal("Updates() did not signal after SetSettings")
	}
}
