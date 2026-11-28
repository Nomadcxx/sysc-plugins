package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bitsub "github.com/Nomadcxx/sysc-plugins/plugins/bitsub"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var testNow = time.Date(2026, 11, 29, 14, 40, 0, 0, time.Local)

// fakeServer is a scripted bitsubd.
type fakeServer struct {
	mu           sync.Mutex
	items        []subs.Item
	notes        []subs.Notification
	down         bool
	unauthorized bool
	calls        []string
	srv          *httptest.Server
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(body)))
	switch {
	case f.down:
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case f.unauthorized:
		w.WriteHeader(http.StatusUnauthorized)
		return
	case r.URL.Path == "/api/v1/snapshot":
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		snap := subs.Snapshot{Items: f.items, ServedBy: "archPC", GeneratedAt: testNow, Notifications: []subs.Notification{},
			Totals: subs.Totals{MonthlyAUD: 1899}, Series: make([]subs.MonthTotal, 12)}
		for _, n := range f.notes {
			if n.Seq > since {
				snap.Notifications = append(snap.Notifications, n)
			}
			snap.LastSeq = max(snap.LastSeq, n.Seq)
		}
		json.NewEncoder(w).Encode(snap)
	default:
		// Apply changes the way bitsubd does, so the repoll after an action
		// agrees with the plugin's optimistic view.
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/subscriptions/"), "/")
		if len(parts) == 2 {
			var in struct {
				Intent subs.Intent `json:"intent"`
			}
			json.Unmarshal(body, &in)
			kept := f.items[:0]
			for _, it := range f.items {
				if it.ID == parts[0] && parts[1] == "intent" {
					it.Intent = in.Intent
				}
				if it.ID != parts[0] || parts[1] != "confirm-cancelled" {
					kept = append(kept, it)
				}
			}
			f.items = kept
		}
		io.WriteString(w, `{"ok":true}`)
	}
}

func (f *fakeServer) set(fn func(*fakeServer)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeServer) callsWith(fragment string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.Contains(c, fragment) {
			out = append(out, c)
		}
	}
	return out
}

func clientConfig(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.json")
	raw, _ := json.Marshal(bitsub.ClientConfig{URL: url, Token: "bsk_test"})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type harness struct {
	t             *testing.T
	host          io.WriteCloser
	lines         chan []byte
	notifications chan v1.NotifyParams
	stateSets     chan v1.StateSetParams
	done          chan error
	closed        chan struct{}
	once          sync.Once
	stopped       bool
	stateMu       sync.Mutex
	state         map[string]json.RawMessage
	views         int
}

type hostOptions struct {
	minor  int
	state  map[string]json.RawMessage
	config string
}

func start(t *testing.T, opts hostOptions) *harness {
	t.Helper()
	if opts.minor == 0 {
		opts.minor = 28
	}
	if opts.state == nil {
		opts.state = map[string]json.RawMessage{}
	}
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &harness{t: t, host: host, lines: make(chan []byte, 256), notifications: make(chan v1.NotifyParams, 16),
		stateSets: make(chan v1.StateSetParams, 64), done: make(chan error, 1), closed: make(chan struct{}), state: opts.state}
	env := environment{now: func() time.Time { return testNow }, pollEvery: time.Hour, retryEvery: time.Hour, firstPoll: 10 * time.Millisecond}
	go func() {
		err := runPluginWith(input, output, env, settings{clientConfig: opts.config})
		output.Close()
		h.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(plugin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			var call v1.HostCall
			if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil {
				reply := v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: json.RawMessage(`{}`)}
				switch call.Call {
				case v1.CallStateGet:
					var p v1.StateGetParams
					json.Unmarshal(call.Params, &p)
					h.stateMu.Lock()
					v, ok := h.state[p.Key]
					h.stateMu.Unlock()
					reply.Result, _ = json.Marshal(v1.StateGetResult{Found: ok, Value: v})
				case v1.CallStateSet:
					var p v1.StateSetParams
					if json.Unmarshal(call.Params, &p) == nil {
						h.stateMu.Lock()
						h.state[p.Key] = append(json.RawMessage(nil), p.Value...)
						h.stateMu.Unlock()
						h.stateSets <- p
					}
				case v1.CallNotify:
					var p v1.NotifyParams
					if json.Unmarshal(call.Params, &p) == nil {
						h.notifications <- p
					}
				}
				data, _ := json.Marshal(reply)
				if _, err := h.host.Write(append(data, '\n')); err != nil {
					return
				}
				continue
			}
			select {
			case h.lines <- line:
			case <-h.closed:
			}
		}
		close(h.lines)
	}()
	h.send(map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: opts.minor}},
		"capabilities": []string{"notifications", "panels", "settings", "state"}})
	if got := h.next(); messageType(got) != "plugin.hello" {
		t.Fatalf("first message = %s", got)
	}
	t.Cleanup(h.stop)
	return h
}

func (h *harness) send(message any) {
	h.t.Helper()
	data, _ := json.Marshal(message)
	if _, err := h.host.Write(append(data, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) next() []byte {
	h.t.Helper()
	select {
	case line, ok := <-h.lines:
		if !ok {
			h.t.Fatal("plugin output closed")
		}
		return line
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

func (h *harness) openPanel() string {
	h.views++
	id := fmt.Sprintf("p%d", h.views)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: id, View: v1.ViewPanel, Entry: "panel", Width: bitsub.PanelWidth, Height: bitsub.PanelHeight})
	return id
}

// snapshotFor waits for a snapshot of view id whose text contains want.
func (h *harness) snapshotFor(id, want string) v1.ViewSnapshot {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		line := h.next()
		var s v1.ViewSnapshot
		if messageType(line) != v1.TypeViewSnapshot || json.Unmarshal(line, &s) != nil || s.ViewID != id {
			continue
		}
		last = allText(s.Root)
		if strings.Contains(last, want) {
			return s
		}
	}
	h.t.Fatalf("no snapshot of %s containing %q; last:\n%s", id, want, last)
	return v1.ViewSnapshot{}
}

func (h *harness) click(s v1.ViewSnapshot, node string) {
	h.send(v1.InputEvent{Type: "input.event", ViewID: s.ViewID, Revision: s.Revision, Node: node, Event: v1.EventActivate})
}

// waitState waits for a state.set of key and returns its value.
func (h *harness) waitState(key string) string {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case p := <-h.stateSets:
			if p.Key == key {
				return string(p.Value)
			}
		case <-deadline:
			h.t.Fatalf("no state.set %q", key)
			return ""
		}
	}
}

func (h *harness) noNotification(within time.Duration) {
	h.t.Helper()
	select {
	case n := <-h.notifications:
		h.t.Fatalf("unexpected notification %+v", n)
	case <-time.After(within):
	}
}

func (h *harness) savedState() map[string]json.RawMessage {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	out := map[string]json.RawMessage{}
	for k, v := range h.state {
		out[k] = v
	}
	return out
}

func (h *harness) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	h.once.Do(func() { close(h.closed) })
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		h.t.Fatal("plugin did not stop")
	}
}

func messageType(line []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	json.Unmarshal(line, &m)
	return m.Type
}

func allText(n *v1.Node) string {
	if n == nil {
		return ""
	}
	out := n.Text + "\n"
	for _, c := range n.Children {
		out += allText(c)
	}
	return out
}

func find(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if hit := find(c, id); hit != nil {
			return hit
		}
	}
	return nil
}

func note(seq int64, age time.Duration) subs.Notification {
	return subs.Notification{Seq: seq, SubscriptionID: "netflix", Summary: fmt.Sprintf("note %d", seq), Body: "A$18.99/mo",
		Urgency: "normal", IssuedAt: testNow.Add(-age), Actions: []subs.Action{{Key: "keep:netflix", Label: "Keep"}}}
}

func netflix(intent subs.Intent) subs.Item {
	d := subs.MustDate("2026-12-14")
	return subs.Item{ID: "netflix", Name: "Netflix", Price: subs.Money{Minor: 1899, Currency: "AUD"}, PriceAUD: 1899,
		Cycle: subs.Month, Interval: 1, Target: d, TargetKind: subs.TargetRenewal, Intent: intent, CancelBy: d}
}

func TestFirstRunDoesNotReplayHistory(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) {
		f.items = []subs.Item{netflix(subs.Undecided)}
		for i := int64(1); i <= 5; i++ {
			f.notes = append(f.notes, note(i, time.Minute))
		}
	})
	h := start(t, hostOptions{config: clientConfig(t, f.srv.URL)})
	if got := h.waitState("last_seq"); got != "5" {
		t.Fatalf("last_seq %s", got)
	}
	h.noNotification(200 * time.Millisecond)
}

func TestRestartDoesNotRepeatNotifications(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) {
		f.items = []subs.Item{netflix(subs.Undecided)}
		for i := int64(1); i <= 6; i++ {
			f.notes = append(f.notes, note(i, time.Minute))
		}
		f.notes[5].Urgency = "critical"
	})
	cfg := clientConfig(t, f.srv.URL)
	h := start(t, hostOptions{config: cfg, state: map[string]json.RawMessage{"last_seq": json.RawMessage("5")}})
	select {
	case n := <-h.notifications:
		if n.Summary != "note 6" || n.Urgency != v1.UrgencyCritical || n.TimeoutMS != 0 {
			t.Fatalf("%+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
	if got := h.waitState("last_seq"); got != "6" {
		t.Fatalf("last_seq %s", got)
	}
	h.noNotification(200 * time.Millisecond)
	saved := h.savedState()
	h.stop()

	h2 := start(t, hostOptions{config: cfg, state: saved})
	id := h2.openPanel()
	h2.snapshotFor(id, "Renews Mon 14 Dec")
	h2.noNotification(300 * time.Millisecond)
}

func TestNormalNotificationTimesOut(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) { f.notes = []subs.Notification{note(1, time.Minute), note(2, 13*time.Hour)} })
	h := start(t, hostOptions{config: clientConfig(t, f.srv.URL), state: map[string]json.RawMessage{"last_seq": json.RawMessage("0")}})
	select {
	case n := <-h.notifications:
		if n.Summary != "note 1" || n.TimeoutMS != 10000 || n.Urgency != v1.UrgencyNormal {
			t.Fatalf("%+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
	h.noNotification(200 * time.Millisecond) // note 2 is older than 12 hours
}

func TestNoActionsBelowMinor29(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) { f.notes = []subs.Notification{note(1, time.Minute)} })
	h := start(t, hostOptions{minor: 28, config: clientConfig(t, f.srv.URL), state: map[string]json.RawMessage{"last_seq": json.RawMessage("0")}})
	select {
	case n := <-h.notifications:
		if len(n.Actions) != 0 {
			t.Fatalf("actions sent to a minor-28 host: %+v", n.Actions)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
}

func TestOfflineShowsLastSnapshotAndIgnoresClicks(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) { f.down = true })
	stored, _ := json.Marshal(subs.Snapshot{Items: []subs.Item{netflix(subs.Undecided)}, ServedBy: "archPC", Series: make([]subs.MonthTotal, 12)})
	h := start(t, hostOptions{config: clientConfig(t, f.srv.URL), state: map[string]json.RawMessage{"snapshot": stored, "last_seq": json.RawMessage("3")}})
	id := h.openPanel()
	s := h.snapshotFor(id, "Offline: archPC hasn't answered")
	if !strings.Contains(allText(s.Root), "Netflix") {
		t.Fatalf("last snapshot not shown:\n%s", allText(s.Root))
	}
	if btn := find(s.Root, "keep:netflix"); btn == nil || !btn.Disabled {
		t.Fatalf("keep button %+v", btn)
	}
	h.click(s, "keep:netflix")
	time.Sleep(200 * time.Millisecond)
	if calls := f.callsWith("/intent"); len(calls) != 0 {
		t.Fatalf("offline click reached the server: %v", calls)
	}
}

func TestUnauthorizedShowsTokenRejected(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) { f.unauthorized = true })
	h := start(t, hostOptions{config: clientConfig(t, f.srv.URL)})
	id := h.openPanel()
	h.snapshotFor(id, "Token rejected: check")
}

func TestMissingConfigExplainsItself(t *testing.T) {
	h := start(t, hostOptions{config: filepath.Join(t.TempDir(), "absent.json")})
	id := h.openPanel()
	h.snapshotFor(id, "Client config:")
}

func TestKeepTogglesAndPolls(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) { f.items = []subs.Item{netflix(subs.Undecided)} })
	h := start(t, hostOptions{config: clientConfig(t, f.srv.URL)})
	id := h.openPanel()
	s := h.snapshotFor(id, "Netflix")
	for find(s.Root, "keep:netflix") == nil || find(s.Root, "keep:netflix").Disabled {
		s = h.snapshotFor(id, "Netflix")
	}
	h.click(s, "keep:netflix")
	deadline := time.Now().Add(3 * time.Second)
	for len(f.callsWith(`/intent {"intent":"keep"}`)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(f.callsWith(`/intent {"intent":"keep"}`)) != 1 {
		t.Fatalf("calls %v", f.callsWith("/intent"))
	}
	// Clicking the filled Keep again sets undecided.
	s = h.snapshotFor(id, "Netflix")
	for find(s.Root, "keep:netflix") == nil || find(s.Root, "keep:netflix").Fill != "accent" {
		s = h.snapshotFor(id, "Netflix")
	}
	h.click(s, "keep:netflix")
	deadline = time.Now().Add(3 * time.Second)
	for len(f.callsWith(`"undecided"`)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(f.callsWith(`"undecided"`)) != 1 {
		t.Fatalf("calls %v", f.callsWith("/intent"))
	}
}

func TestCancelTwiceAsksToConfirm(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) { f.items = []subs.Item{netflix(subs.Cancel)} })
	h := start(t, hostOptions{config: clientConfig(t, f.srv.URL)})
	id := h.openPanel()
	s := h.snapshotFor(id, "cancel before then")
	for find(s.Root, "cancel:netflix") == nil || find(s.Root, "cancel:netflix").Disabled {
		s = h.snapshotFor(id, "cancel before then")
	}
	h.click(s, "cancel:netflix")
	s = h.snapshotFor(id, "Have you cancelled it?")
	if calls := f.callsWith("/intent"); len(calls) != 0 {
		t.Fatalf("second Cancel changed the intent: %v", calls)
	}
	h.click(s, "confirm-yes:netflix")
	deadline := time.Now().Add(3 * time.Second)
	for len(f.callsWith("/confirm-cancelled")) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(f.callsWith("/subscriptions/netflix/confirm-cancelled")) != 1 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestStaleRevisionClickDropped(t *testing.T) {
	f := newFakeServer(t)
	f.set(func(f *fakeServer) { f.items = []subs.Item{netflix(subs.Undecided)} })
	h := start(t, hostOptions{config: clientConfig(t, f.srv.URL)})
	id := h.openPanel()
	s := h.snapshotFor(id, "Netflix")
	for find(s.Root, "keep:netflix").Disabled {
		s = h.snapshotFor(id, "Netflix")
	}
	s.Revision += 7
	h.click(s, "keep:netflix")
	time.Sleep(200 * time.Millisecond)
	if calls := f.callsWith("/intent"); len(calls) != 0 {
		t.Fatalf("stale click reached the server: %v", calls)
	}
}
