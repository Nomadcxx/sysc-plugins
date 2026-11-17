package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/hermes"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// repoRoot is the sysc-plugins checkout this test file lives in.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
}

func TestManifestPassesValidatorAndDeclaresServiceActions(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "run", "./tools/validate-manifests")
	cmd.Dir = root
	out, _ := cmd.CombinedOutput() // other plugins' results do not decide this test
	const ok = "ok   plugins/hermes/manifest.json"
	if !strings.Contains(string(out), ok) {
		t.Fatalf("validate-manifests did not accept plugins/hermes/manifest.json:\n%s", out)
	}

	data, err := os.ReadFile(filepath.Join(root, "plugins/hermes/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID       string     `json:"id"`
		Name     string     `json:"name"`
		Version  string     `json:"version"`
		Exec     string     `json:"exec"`
		Protocol v1.Version `json:"protocol"`
		Services []struct {
			ID string `json:"id"`
		} `json:"services"`
		Settings []struct {
			Key     string          `json:"key"`
			Default json.RawMessage `json:"default"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ID != pluginID || manifest.Name != pluginName || manifest.Version != pluginVersion || manifest.Exec != "bin/sysc-plugin-hermes" {
		t.Fatalf("manifest identity = %s %q %s %s, does not match the plugin handshake", manifest.ID, manifest.Name, manifest.Version, manifest.Exec)
	}
	if manifest.Protocol.Major != v1.ProtocolMajor || manifest.Protocol.Minor < v1.ServiceActionMinor {
		t.Fatalf("manifest protocol = %d.%d, want %d.>=%d", manifest.Protocol.Major, manifest.Protocol.Minor, v1.ProtocolMajor, v1.ServiceActionMinor)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].ID != serviceID {
		t.Fatalf("manifest services = %+v, want one %q service", manifest.Services, serviceID)
	}
	if len(manifest.Settings) == 0 || manifest.Settings[0].Key != "client_only_mode" || string(manifest.Settings[0].Default) != "false" {
		t.Fatalf("first setting must be client_only_mode defaulting to false")
	}

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makefile), "sysc-plugin-hermes:hermes") {
		t.Fatal("Makefile PLUGINS does not list sysc-plugin-hermes:hermes")
	}
}

// testHost drives run over pipes the way the shell drives the binary.
type testHost struct {
	t        *testing.T
	mu       sync.Mutex
	enc      *v1.Encoder
	msgs     chan v1.Message
	done     chan error
	fromPlug *io.PipeReader
	// silent makes the host never answer host calls, like a wedged shell.
	silent atomic.Bool
	calls  atomic.Int64
	stderr *lockedBuffer
	// timeout overrides the default 2 s wait for slow tests. Zero means 2 s.
	timeout time.Duration

	// reply customizes host replies; nil means OK with no result.
	reply func(*v1.HostCall) *v1.HostReply
	// callMu guards callLog, the host calls this test observed.
	callMu  sync.Mutex
	callLog []*v1.HostCall
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func startPlugin(t *testing.T) (*testHost, *v1.PluginHello) {
	t.Helper()
	return startPluginWith(t, func(*testHost) {})
}

func startPluginWith(t *testing.T, setup func(*testHost)) (*testHost, *v1.PluginHello) {
	t.Helper()
	return startPluginOptions(t, hermes.Options{}, setup)
}

// startPluginOptions is startPluginWith with explicit service options, for
// tests that must keep local mode away from a real bridge.
func startPluginOptions(t *testing.T, opts hermes.Options, setup func(*testHost)) (*testHost, *v1.PluginHello) {
	t.Helper()
	pluginIn, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	h := &testHost{t: t, enc: v1.NewEncoder(hostOut), msgs: make(chan v1.Message, 128), done: make(chan error, 1),
		fromPlug: hostIn, stderr: &lockedBuffer{}}
	setup(h)
	go func() {
		h.done <- runWith(pluginIn, pluginOut, h.stderr, opts)
		_ = pluginOut.Close()
	}()
	go h.read()
	t.Cleanup(func() {
		_ = h.send(&v1.HostShutdown{})
		select {
		case err := <-h.done:
			if err != nil {
				t.Errorf("plugin exit: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("plugin did not stop after host.shutdown")
		}
		_ = hostOut.Close()
		_ = hostIn.Close()
		_ = pluginIn.Close()
	})
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: v1.ProtocolMajor, Minor: v1.ProtocolMinor}},
		Plugin:       v1.Identity{ID: pluginID, Name: pluginName, Version: pluginVersion},
		Capabilities: []string{"notifications", "panels", "settings", "floating_surfaces"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	hello := h.wait(func(m v1.Message) bool {
		_, ok := m.(*v1.PluginHello)
		return ok
	}).(*v1.PluginHello)
	return h, hello
}

func (h *testHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *testHost) read() {
	dec := v1.NewDecoder(h.fromPlug, v1.ToHost)
	for {
		msg, err := dec.Decode()
		if err != nil {
			return
		}
		if call, ok := msg.(*v1.HostCall); ok {
			h.calls.Add(1)
			h.callMu.Lock()
			h.callLog = append(h.callLog, call)
			reply := h.reply
			h.callMu.Unlock()
			if !h.silent.Load() {
				r := &v1.HostReply{ID: call.ID, OK: true}
				if reply != nil {
					if custom := reply(call); custom != nil {
						r = custom
					}
				}
				_ = h.send(r)
			}
		}
		select {
		case h.msgs <- msg:
		default:
		}
	}
}

func (h *testHost) deadline() time.Duration {
	if h.timeout == 0 {
		return 2 * time.Second
	}
	return h.timeout
}

func (h *testHost) wait(accept func(v1.Message) bool) v1.Message {
	h.t.Helper()
	deadline := time.After(h.deadline())
	for {
		select {
		case msg := <-h.msgs:
			if accept(msg) {
				return msg
			}
		case <-deadline:
			h.t.Fatal("timed out waiting for plugin message")
		}
	}
}

// waitCall waits until the plugin makes a host call of kind.
func (h *testHost) waitCall(kind v1.CallKind) *v1.HostCall {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.callMu.Lock()
		for _, call := range h.callLog {
			if call.Call == kind {
				h.callMu.Unlock()
				return call
			}
		}
		h.callMu.Unlock()
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for host call %s", kind)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitCallParams waits for a host call of kind whose params satisfy accept.
func (h *testHost) waitCallParams(kind v1.CallKind, accept func(json.RawMessage) bool) *v1.HostCall {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.callMu.Lock()
		for _, call := range h.callLog {
			if call.Call == kind && accept(call.Params) {
				h.callMu.Unlock()
				return call
			}
		}
		h.callMu.Unlock()
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for host call %s", kind)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// countCalls counts the observed host calls of kind.
func (h *testHost) countCalls(kind v1.CallKind) int {
	h.callMu.Lock()
	defer h.callMu.Unlock()
	n := 0
	for _, call := range h.callLog {
		if call.Call == kind {
			n++
		}
	}
	return n
}

// treeHasName reports whether any node's accessible name carries want.
func treeHasName(n *v1.Node, want string) bool {
	if n == nil {
		return false
	}
	if strings.Contains(n.Name, want) {
		return true
	}
	for _, c := range n.Children {
		if treeHasName(c, want) {
			return true
		}
	}
	return false
}

func treeText(n *v1.Node) string {
	if n == nil {
		return ""
	}
	parts := []string{n.Text}
	for _, c := range n.Children {
		parts = append(parts, treeText(c))
	}
	return strings.Join(parts, " ")
}

func TestHandshakeIdentifiesThePlugin(t *testing.T) {
	_, hello := startPlugin(t)
	if hello.Plugin.ID != pluginID || hello.Plugin.Name != pluginName || hello.Plugin.Version != pluginVersion {
		t.Fatalf("plugin hello identity = %+v", hello.Plugin)
	}
	if hello.Protocol.Major != v1.ProtocolMajor {
		t.Fatalf("plugin hello protocol = %+v", hello.Protocol)
	}
}

// Every view the manifest can open, with the size the host grants it.
var openings = []v1.ViewOpen{
	{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Width: 240, Height: 32},
	{ViewID: "tip-1", View: v1.ViewTooltip, Entry: "bar", Width: 280, Height: 200},
	{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720},
	{ViewID: "summary-1", View: v1.ViewPanel, Entry: "summary", Width: 380, Height: 480},
	{ViewID: "menu-1", View: v1.ViewPanel, Entry: "menu", Width: 260, Height: 300},
	{ViewID: "settings-1", View: v1.ViewPanel, Entry: "settings", Width: 640, Height: 720},
	{ViewID: "pinned-1", View: v1.ViewFloating, Entry: "pinned", Width: 640, Height: 720},
}

func TestEveryViewKindGetsAConnectingSnapshot(t *testing.T) {
	h, _ := startPlugin(t)
	for _, open := range openings {
		open := open
		if err := h.send(&open); err != nil {
			t.Fatal(err)
		}
		snap := h.wait(func(m v1.Message) bool {
			s, ok := m.(*v1.ViewSnapshot)
			return ok && s.ViewID == open.ViewID
		}).(*v1.ViewSnapshot)
		if snap.Revision == 0 {
			t.Errorf("%s: snapshot revision is 0", open.ViewID)
		}
		if err := v1.Validate(snap.Root, open.View); err != nil {
			t.Errorf("%s: %v", open.ViewID, err)
		}
		text := treeText(snap.Root)
		if open.View == v1.ViewBar && snap.Root != nil && strings.TrimSpace(text) == "" {
			// A compact bar may be icon-only; at 240 it must say so in words.
			t.Errorf("%s: bar shows no label", open.ViewID)
		}
		if !strings.Contains(text, "Connecting…") {
			t.Errorf("%s: snapshot text %q does not say Connecting…", open.ViewID, text)
		}
	}
}

func TestResyncAndResizeRepublish(t *testing.T) {
	h, _ := startPlugin(t)
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720}); err != nil {
		t.Fatal(err)
	}
	first := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "panel-1"
	}).(*v1.ViewSnapshot)
	if err := h.send(&v1.ViewResize{ViewID: "panel-1", Width: 640, Height: 520}); err != nil {
		t.Fatal(err)
	}
	resized := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "panel-1"
	}).(*v1.ViewSnapshot)
	if resized.Revision <= first.Revision || resized.Root.Width != 640 || resized.Root.Height != 520 {
		t.Fatalf("resize snapshot rev %d size %dx%d, want rev > %d at 640x520", resized.Revision, resized.Root.Width, resized.Root.Height, first.Revision)
	}
	if err := h.send(&v1.ViewResync{ViewID: "panel-1"}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "panel-1"
	})
}

// The connecting trees lay out at each declared size and at the fitted-down
// size the shell may grant.
func TestConnectingTreesLayOutAtDeclaredAndFittedSizes(t *testing.T) {
	for _, tc := range []struct {
		view  v1.ViewKind
		entry string
		w, h  int
	}{
		{v1.ViewBar, "bar", 240, 32}, {v1.ViewBar, "bar", 120, 32}, {v1.ViewBar, "bar", 32, 32},
		{v1.ViewTooltip, "bar", 280, 200},
		{v1.ViewPanel, "panel", 780, 720}, {v1.ViewPanel, "panel", 640, 520},
		{v1.ViewPanel, "summary", 380, 480}, {v1.ViewPanel, "summary", 320, 400},
		{v1.ViewPanel, "menu", 260, 300},
		{v1.ViewPanel, "settings", 640, 720}, {v1.ViewPanel, "settings", 560, 520},
		{v1.ViewFloating, "pinned", 640, 720}, {v1.ViewFloating, "pinned", 400, 480},
	} {
		tree := connectingTree(tc.view, tc.entry, tc.w, tc.h)
		if err := v1.Validate(tree, tc.view); err != nil {
			t.Errorf("%s %s %dx%d: %v", tc.view, tc.entry, tc.w, tc.h, err)
		}
		for _, f := range shelllint.Tree(tree, tc.view, tc.w, tc.h) {
			t.Errorf("%s %s %dx%d: %s", tc.view, tc.entry, tc.w, tc.h, f)
		}
	}
}

// fakeRemote is an httptest stand-in for a client-only bridge, with a fake
// token.
func fakeRemote(t *testing.T, hermesStatus string) (host string, port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Bridge-Token") != "fake-token" && r.URL.Path != "/detect" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bridge": map[string]any{"status": "online"},
			"hermes": map[string]any{"status": hermesStatus},
		})
	}))
	t.Cleanup(srv.Close)
	h, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port
}

func clientOnlyValues(host string, port int) map[string]any {
	return map[string]any{
		"client_only_mode":    true,
		"bridge_host":         host,
		"bridge_port":         float64(port),
		"bridge_token_manual": "fake-token",
		"bridge_token_file":   "/nonexistent/hermes.token",
	}
}

func (h *testHost) waitSnapshot(viewID string, accept func(*v1.ViewSnapshot) bool) *v1.ViewSnapshot {
	h.t.Helper()
	return h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == viewID && accept(s)
	}).(*v1.ViewSnapshot)
}

func TestSettingsChangedConnectsAndRepublishes(t *testing.T) {
	host, port := fakeRemote(t, "idle")
	h, _ := startPlugin(t)
	// This test republishes a full tree and races the rest of the suite under
	// -race; the 2 s default is too tight. The assertions are unchanged.
	h.timeout = 10 * time.Second
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Width: 240, Height: 32}); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Connecting…") })
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	// The Online snapshot and the active status can arrive in either order;
	// take both from one stream so neither wait can consume the other's
	// message.
	var snap *v1.ViewSnapshot
	var status *v1.ServiceStatus
	deadline := time.After(h.deadline())
	for snap == nil || status == nil {
		select {
		case m := <-h.msgs:
			switch v := m.(type) {
			case *v1.ViewSnapshot:
				if v.ViewID == "bar-1" && treeHasName(v.Root, "Online") {
					snap = v
				}
			case *v1.ServiceStatus:
				if v.State == v1.ServiceStateActive {
					status = v
				}
			}
		case <-deadline:
			h.t.Fatalf("timed out waiting for the Online bar and active status (snapshot=%v status=%v)", snap != nil, status != nil)
		}
	}
	if err := v1.Validate(snap.Root, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if status.ServiceID != serviceID || status.Message != "Online" {
		t.Fatalf("service status = %+v", status)
	}
}

func TestAWedgedHostCallNeverBlocksTheEventLoop(t *testing.T) {
	host, port := fakeRemote(t, "idle")
	h, _ := startPluginWith(t, func(h *testHost) { h.silent.Store(true) })
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Online") })
	// Auto-configure's state.get is now waiting on a host that never answers.
	deadline := time.Now().Add(2 * time.Second)
	for h.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.calls.Load() == 0 {
		t.Fatal("auto-configure made no host call")
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		if err := h.send(&v1.ViewResize{ViewID: "panel-1", Width: 640 + i, Height: 520}); err != nil {
			t.Fatal(err)
		}
		w := 640 + i
		h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool { return s.Root.Width == w })
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("20 resizes took %s behind a wedged host call", d)
	}
	// A wedged host call holds the state writer, and its bound outlasts this
	// test's 2 s shutdown wait. Answer the outstanding calls the way a live
	// host would, before the shutdown cleanup runs (cleanups are LIFO).
	t.Cleanup(func() {
		h.callMu.Lock()
		calls := append([]*v1.HostCall(nil), h.callLog...)
		h.callMu.Unlock()
		h.silent.Store(false)
		for _, call := range calls {
			_ = h.send(&v1.HostReply{ID: call.ID, OK: true})
		}
	})
}

func TestViewErrorIsLoggedAndShownOnTheConnectionLine(t *testing.T) {
	host, port := fakeRemote(t, "idle")
	h, _ := startPlugin(t)
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.ViewOpen{ViewID: "settings-1", View: v1.ViewPanel, Entry: "settings", Width: 640, Height: 720}); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("settings-1", func(*v1.ViewSnapshot) bool { return true })
	if err := h.send(&v1.ViewError{ViewID: "settings-1", Revision: 1, Reason: "node too wide"}); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("settings-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "node too wide") })
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(h.stderr.String(), "node too wide") {
		if time.Now().After(deadline) {
			t.Fatalf("view.error not logged; stderr = %q", h.stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if strings.Contains(h.stderr.String(), "fake-token") {
		t.Fatal("the token reached the log")
	}
}

func TestViewCloseDropsTheView(t *testing.T) {
	h, _ := startPlugin(t)
	for _, id := range []string{"panel-1", "panel-2"} {
		if err := h.send(&v1.ViewOpen{ViewID: id, View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720}); err != nil {
			t.Fatal(err)
		}
		h.waitSnapshot(id, func(*v1.ViewSnapshot) bool { return true })
	}
	if err := h.send(&v1.ViewClose{ViewID: "panel-1"}); err != nil {
		t.Fatal(err)
	}
	// Messages for the closed view are ignored; the open one still answers.
	_ = h.send(&v1.ViewResize{ViewID: "panel-1", Width: 600, Height: 500})
	_ = h.send(&v1.ViewResync{ViewID: "panel-1"})
	_ = h.send(&v1.ViewResize{ViewID: "panel-2", Width: 600, Height: 500})
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		if ok && s.ViewID == "panel-1" {
			t.Fatal("closed view republished")
		}
		return ok && s.ViewID == "panel-2" && s.Root.Width == 600
	})
}

func TestViewOpenRecordsItsPlacement(t *testing.T) {
	vs := newViewSet()
	v := vs.open(&v1.ViewOpen{ViewID: "p", View: v1.ViewPanel, Entry: "summary", Instance: "i1",
		Output: "DP-1", Generation: 7, Width: 380, Height: 480})
	if v.kind != v1.ViewPanel || v.entry != "summary" || v.instance != "i1" || v.output != "DP-1" ||
		v.generation != 7 || v.width != 380 || v.height != 480 || v.rev != 0 {
		t.Fatalf("open recorded %+v", v)
	}
	v.section, v.composer = hermes.SectionSessions, hermes.ComposerState{Reseed: 3}
	other := vs.open(&v1.ViewOpen{ViewID: "q", View: v1.ViewPanel, Entry: "panel"})
	if other.section != "" || other.composer != (hermes.ComposerState{}) {
		t.Fatal("per-view state leaked between views")
	}
	vs.close("p")
	if vs.get("p") != nil {
		t.Fatal("close kept the view")
	}
	if again := vs.open(&v1.ViewOpen{ViewID: "p", View: v1.ViewPanel, Entry: "summary"}); again.section != "" || again.composer != (hermes.ComposerState{}) {
		t.Fatal("reopened view kept the closed view's state")
	}
}

func TestStaleRevisionDroppedStatusOnlyRepublishAccepted(t *testing.T) {
	vs := newViewSet()
	v := vs.open(&v1.ViewOpen{ViewID: "p", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720})
	state := hermes.View{Mode: hermes.ModeClientOnly, Status: hermes.StatusFor(hermes.StatusIdle)}

	rev1, ok := v.next(viewTree(v, state, true, renderEnv{}))
	if !ok || rev1 != 1 {
		t.Fatalf("first publish rev %d sent=%v", rev1, ok)
	}
	// Same tree again: nothing to send.
	if _, ok := v.next(viewTree(v, state, true, renderEnv{})); ok {
		t.Fatal("an unchanged tree was republished")
	}
	// Status-only change: the text moves, the addressable nodes do not.
	state.Status = hermes.StatusFor(hermes.StatusOffline)
	state.Message = hermes.MsgOffline
	rev2, ok := v.next(viewTree(v, state, true, renderEnv{}))
	if !ok || rev2 != 2 {
		t.Fatalf("status republish rev %d sent=%v", rev2, ok)
	}
	click := func(rev uint64) bool {
		return v.accepts(&v1.InputEvent{ViewID: "p", Revision: rev, Node: "refresh", Event: v1.EventActivate})
	}
	if !click(rev1) || !click(rev2) {
		t.Fatal("a click on the unchanged Refresh button was dropped after a status-only republish")
	}
	// Content change: a running session adds the Interrupt button.
	state.State.Session.Running = true
	rev3, ok := v.next(viewTree(v, state, true, renderEnv{}))
	if !ok {
		t.Fatal("content change not sent")
	}
	if click(rev2) {
		t.Fatal("a stale revision was accepted after the content changed")
	}
	if !click(rev3) {
		t.Fatal("the current revision was dropped")
	}
	if click(rev3 + 1) {
		t.Fatal("a revision from the future was accepted")
	}
	// Resync restarts the revision count.
	v.resync()
	if rev, ok := v.next(viewTree(v, state, true, renderEnv{})); !ok || rev != 1 {
		t.Fatalf("after resync rev %d sent=%v", rev, ok)
	}
}

func TestActionSignatureCoversAddressableNodesOnly(t *testing.T) {
	base := func() *v1.Node {
		return &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Online"},
			{Kind: v1.KindButton, ID: "refresh", Text: "Refresh", Events: []v1.EventKind{v1.EventActivate}},
		}}
	}
	a := actionSignature(base())
	status := base()
	status.Children[0].Text = "Offline"
	if actionSignature(status) != a {
		t.Fatal("a text change on a node without an id changed the signature")
	}
	for name, mutate := range map[string]func(n *v1.Node){
		"label":    func(n *v1.Node) { n.Text = "Reload" },
		"disabled": func(n *v1.Node) { n.Disabled = true },
		"events":   func(n *v1.Node) { n.Events = append(n.Events, v1.EventPointer) },
		"kind":     func(n *v1.Node) { n.Kind = v1.KindText },
		"id":       func(n *v1.Node) { n.ID = "other" },
	} {
		n := base()
		mutate(n.Children[1])
		if actionSignature(n) == a {
			t.Errorf("%s change kept the signature", name)
		}
	}
}

func TestStatusTreesLayOut(t *testing.T) {
	vs := newViewSet()
	for _, st := range []hermes.View{
		{Mode: hermes.ModeLocal, Status: hermes.StatusFor(hermes.StatusIdle)},
		{Mode: hermes.ModeClientOnly, Status: hermes.StatusFor(hermes.StatusError), Message: hermes.MsgNoToken,
			Warning: hermes.MsgCleartext("192.168.0.10"), Pending: true, ViewError: "node too wide"},
	} {
		for _, tc := range []struct {
			view  v1.ViewKind
			entry string
			w, h  int
		}{
			{v1.ViewBar, "bar", 240, 32}, {v1.ViewBar, "bar", 32, 32},
			{v1.ViewTooltip, "bar", 280, 200},
			{v1.ViewPanel, "panel", 780, 720}, {v1.ViewPanel, "summary", 320, 400},
			{v1.ViewPanel, "menu", 260, 300}, {v1.ViewPanel, "settings", 560, 520},
			{v1.ViewFloating, "pinned", 400, 480},
		} {
			v := vs.open(&v1.ViewOpen{ViewID: "x", View: tc.view, Entry: tc.entry, Width: tc.w, Height: tc.h})
			tree := viewTree(v, st, true, renderEnv{})
			if err := v1.Validate(tree, tc.view); err != nil {
				t.Errorf("%s %s %dx%d: %v", tc.view, tc.entry, tc.w, tc.h, err)
			}
			for _, f := range shelllint.Tree(tree, tc.view, tc.w, tc.h) {
				t.Errorf("%s %s %dx%d: %s", tc.view, tc.entry, tc.w, tc.h, f)
			}
		}
	}
}

func TestBundledScriptPath(t *testing.T) {
	got := bundledScript("/usr/lib/sysc-shell/plugins/hermes/bin/sysc-plugin-hermes")
	if want := "/usr/lib/sysc-shell/plugins/hermes/scripts/hermes_bridge.py"; got != want {
		t.Fatalf("bundled script = %q, want %q", got, want)
	}
}

// fakeRemoteState is a client-only bridge that answers /health, /detect,
// /state, /sessions and /approval; state is returned by /state and an
// approval body is passed to onApproval when one is given.
func fakeRemoteState(t *testing.T, state map[string]any, onApproval ...func(map[string]any)) (host string, port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/detect":
			_, _ = io.WriteString(w, `{}`)
			return
		case "/health":
			_, _ = io.WriteString(w, `{"bridge":{"status":"online"}}`)
			return
		}
		if r.Header.Get("X-Bridge-Token") != "fake-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/sessions":
			_, _ = io.WriteString(w, `{"sessions":[]}`)
		case "/approval":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(onApproval) > 0 {
				onApproval[0](body)
			}
			_, _ = io.WriteString(w, `{}`)
		case "/state":
			_ = json.NewEncoder(w).Encode(state)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	h, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port
}

func idleState(model string) map[string]any {
	return map[string]any{
		"bridge":  map[string]any{"status": "online"},
		"hermes":  map[string]any{"status": "idle", "model": model, "provider": "openrouter"},
		"summary": map[string]any{"model": map[string]any{"name": model, "provider": "openrouter"}, "gateway": map[string]any{"status": "running"}},
	}
}

// surfaceReply answers a pinned surface open with a view id and a state.get
// with found/value, so the pinned and settings paths can run.
func surfaceReply(found bool, value string) func(*v1.HostCall) *v1.HostReply {
	return func(call *v1.HostCall) *v1.HostReply {
		reply := &v1.HostReply{ID: call.ID, OK: true}
		switch call.Call {
		case v1.CallSurfaceOpen:
			result, _ := json.Marshal(v1.SurfaceResult{ViewID: "view-pinned"})
			reply.Result = result
		case v1.CallStateGet:
			result, _ := json.Marshal(v1.StateGetResult{Found: found, Value: json.RawMessage(value)})
			reply.Result = result
		}
		return reply
	}
}

func TestServiceActionOpenOpensTheChatPanel(t *testing.T) {
	remoteHost, remotePort := fakeRemoteState(t, idleState("example-model"))
	h, _ := startPlugin(t)
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(remoteHost, remotePort)}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "open"}); err != nil {
		t.Fatal(err)
	}
	call := h.waitCall(v1.CallPanelOpen)
	var params v1.PanelParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatalf("panel.open params: %v", err)
	}
	if params.Entry != "panel" {
		t.Fatalf("open action asked for panel %q, want the chat panel", params.Entry)
	}
}

func TestServiceActionSettingsOpensTheSettingsPanel(t *testing.T) {
	remoteHost, remotePort := fakeRemoteState(t, idleState("example-model"))
	h, _ := startPlugin(t)
	_ = h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(remoteHost, remotePort)})
	_ = h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "settings"})
	call := h.waitCall(v1.CallPanelOpen)
	var params v1.PanelParams
	_ = json.Unmarshal(call.Params, &params)
	if params.Entry != "settings" {
		t.Fatalf("settings action asked for panel %q", params.Entry)
	}
}

func TestPinSidePanelOpensTheSurfaceAndOpenRaisesIt(t *testing.T) {
	remoteHost, remotePort := fakeRemoteState(t, idleState("example-model"))
	h, _ := startPluginWith(t, func(h *testHost) { h.reply = surfaceReply(false, "") })
	_ = h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(remoteHost, remotePort)})
	if err := h.send(&v1.ViewOpen{ViewID: "settings-1", View: v1.ViewPanel, Entry: "settings", Width: 640, Height: 720}); err != nil {
		t.Fatal(err)
	}
	snap := h.waitSnapshot("settings-1", func(*v1.ViewSnapshot) bool { return true })
	if err := h.send(&v1.InputEvent{ViewID: "settings-1", Revision: snap.Revision, Node: "pin-side-panel", Event: v1.EventActivate}); err != nil {
		t.Fatal(err)
	}
	open := h.waitCall(v1.CallSurfaceOpen)
	var geometry v1.SurfaceOpenParams
	if err := json.Unmarshal(open.Params, &geometry); err != nil {
		t.Fatalf("surface.open params: %v", err)
	}
	if geometry.Width != 640 || geometry.Height != 720 || geometry.X != 32 || geometry.Y != 32 {
		t.Fatalf("surface.open geometry = %dx%d at %d,%d", geometry.Width, geometry.Height, geometry.X, geometry.Y)
	}
	setCall := h.waitCallParams(v1.CallStateSet, func(raw json.RawMessage) bool {
		var p v1.StateSetParams
		return json.Unmarshal(raw, &p) == nil && p.Key == "pinned"
	})
	var saved v1.StateSetParams
	_ = json.Unmarshal(setCall.Params, &saved)
	if saved.Key != "pinned" || string(saved.Value) != "true" {
		t.Fatalf("state.set saved %q=%s, want pinned=true", saved.Key, saved.Value)
	}

	// With the surface pinned, open raises it instead of asking for the
	// chat panel again.
	beforePanel := h.countCalls(v1.CallPanelOpen)
	beforeSurface := h.countCalls(v1.CallSurfaceOpen)
	if err := h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "open"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.countCalls(v1.CallSurfaceOpen) <= beforeSurface {
		if time.Now().After(deadline) {
			t.Fatal("open action did not raise the pinned surface")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if h.countCalls(v1.CallPanelOpen) != beforePanel {
		t.Fatal("open action asked for the chat panel while pinned")
	}
}

func TestPinnedSurfaceRestoresAtStartup(t *testing.T) {
	h, _ := startPluginWith(t, func(h *testHost) { h.reply = surfaceReply(true, "true") })
	h.waitCall(v1.CallSurfaceOpen)
}

func (h *testHost) waitCalls(kind v1.CallKind, n int) {
	deadline := time.Now().Add(h.deadline())
	for time.Now().Before(deadline) {
		if h.countCalls(kind) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %d host calls %s", n, kind)
}

// refusedRestoreReply refuses the first surface.open and accepts the rest,
// the way the shell does while the bar has no output yet.
func refusedRestoreReply() func(*v1.HostCall) *v1.HostReply {
	var mu sync.Mutex
	refused := false
	return func(call *v1.HostCall) *v1.HostReply {
		switch call.Call {
		case v1.CallStateGet:
			result, _ := json.Marshal(v1.StateGetResult{Found: true, Value: json.RawMessage("true")})
			return &v1.HostReply{ID: call.ID, OK: true, Result: result}
		case v1.CallSurfaceOpen:
			mu.Lock()
			defer mu.Unlock()
			if !refused {
				refused = true
				return &v1.HostReply{ID: call.ID, OK: false, Error: "no output"}
			}
			result, _ := json.Marshal(v1.SurfaceResult{ViewID: "view-pinned"})
			return &v1.HostReply{ID: call.ID, OK: true, Result: result}
		}
		return nil
	}
}

func TestPinnedRestoreRetriesWhenTheOutputArrives(t *testing.T) {
	// Push the backoff far beyond any wait deadline: only the bar's ViewOpen
	// with an output may retry the refused restore in this test.
	old := restoreBackoff
	restoreBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { restoreBackoff = old })

	h, _ := startPluginWith(t, func(h *testHost) { h.reply = refusedRestoreReply() })
	h.waitCalls(v1.CallSurfaceOpen, 1)
	// The first attempt is refused; the restore must stay pending instead of
	// giving up.
	time.Sleep(100 * time.Millisecond)
	before := h.countCalls(v1.CallSurfaceOpen)
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Output: "12:00", Width: 240, Height: 32}); err != nil {
		t.Fatal(err)
	}
	h.waitCalls(v1.CallSurfaceOpen, before+1)
	h.waitSnapshot("bar-1", func(*v1.ViewSnapshot) bool { return true })

	// A later bar output must not open a second surface.
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Output: "12:01", Width: 240, Height: 32}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := h.countCalls(v1.CallSurfaceOpen); got != before+1 {
		t.Fatalf("surface.open ran %d times after the restore, want %d", got, before+1)
	}
}

func TestPinnedRestoreRetriesOnItsOwn(t *testing.T) {
	old := restoreBackoff
	restoreBackoff = []time.Duration{10 * time.Millisecond}
	t.Cleanup(func() { restoreBackoff = old })

	h, _ := startPluginWith(t, func(h *testHost) { h.reply = refusedRestoreReply() })
	// No ViewOpen is sent: the backoff alone must retry the refused restore.
	h.waitCalls(v1.CallSurfaceOpen, 2)
}

func TestPinnedRestoreGivesUpWithoutRewritingThePin(t *testing.T) {
	old := restoreBackoff
	restoreBackoff = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	t.Cleanup(func() { restoreBackoff = old })

	h, _ := startPluginWith(t, func(h *testHost) {
		h.reply = func(call *v1.HostCall) *v1.HostReply {
			switch call.Call {
			case v1.CallStateGet:
				result, _ := json.Marshal(v1.StateGetResult{Found: true, Value: json.RawMessage("true")})
				return &v1.HostReply{ID: call.ID, OK: true, Result: result}
			case v1.CallSurfaceOpen:
				return &v1.HostReply{ID: call.ID, OK: false, Error: "no output"}
			}
			return nil
		}
	})
	// One startup attempt plus the two retries in the schedule.
	h.waitCalls(v1.CallSurfaceOpen, 3)
	time.Sleep(150 * time.Millisecond)
	if got := h.countCalls(v1.CallSurfaceOpen); got != 3 {
		t.Fatalf("surface.open ran %d times, want the retries to stop after 3", got)
	}

	h.callMu.Lock()
	defer h.callMu.Unlock()
	for _, call := range h.callLog {
		if call.Call == v1.CallStateSet {
			t.Fatalf("a failed restore rewrote the saved pin: %s", call.Params)
		}
	}
}

func TestPinnedRestoreDoesNotReopenAfterAViewOpen(t *testing.T) {
	h, _ := startPluginWith(t, func(h *testHost) { h.reply = surfaceReply(true, "true") })
	h.waitCall(v1.CallSurfaceOpen)
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Output: "12:00", Width: 240, Height: 32}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := h.countCalls(v1.CallSurfaceOpen); got != 1 {
		t.Fatalf("surface.open ran %d times, want no second open", got)
	}
}

func TestViewsRenderServiceState(t *testing.T) {
	state := idleState("example-model")
	state["usage"] = map[string]any{"input": 10, "output": 5, "total": 15, "cost_usd": 0.5}
	remoteHost, remotePort := fakeRemoteState(t, state)
	h, _ := startPlugin(t)
	_ = h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(remoteHost, remotePort)})

	_ = h.send(&v1.ViewOpen{ViewID: "tip-1", View: v1.ViewTooltip, Entry: "bar", Width: 280, Height: 200})
	tip := h.waitSnapshot("tip-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Online") })
	if text := treeText(tip.Root); !strings.Contains(text, "Hermes: Online · example-model") || strings.Contains(text, "wrong") {
		t.Fatalf("tooltip %q does not carry the model", text)
	}

	_ = h.send(&v1.ViewOpen{ViewID: "summary-1", View: v1.ViewPanel, Entry: "summary", Width: 380, Height: 480})
	sum := h.waitSnapshot("summary-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Usage") })
	if text := treeText(sum.Root); !strings.Contains(text, "10 in · 5 out") {
		t.Fatalf("summary %q does not carry usage", text)
	}
}

// TestPanelStateCarriesSignedApprovalButtons pins the entrypoint wiring: the
// rendered card gets approval.go's buttons for the state it was built from,
// so a click names the request on screen.
func TestPanelStateCarriesSignedApprovalButtons(t *testing.T) {
	view := hermes.View{Status: hermes.StatusFor(hermes.StatusAttention)}
	view.State.Session.ID = "s1"
	view.State.Approval = hermes.Approval{Pending: true, ToolName: "terminal",
		Message: "Run go test ./...", Request: json.RawMessage(`{"cmd":"go test"}`)}
	p := panelStateFor(&viewState{}, view, renderEnv{})
	want := hermes.ApprovalButtons(view.State)
	if !reflect.DeepEqual(p.Approvals, want) {
		t.Fatalf("panel approvals = %#v, want %#v", p.Approvals, want)
	}
	if len(p.Approvals) != 3 || p.Approvals[0].Label != "Deny" {
		t.Fatalf("panel approvals = %#v", p.Approvals)
	}
}

// treeButtonID returns the id of the first node whose id is an approval
// action for the given choice, as the panel rendered it.
func treeButtonID(n *v1.Node, action string) string {
	if n == nil {
		return ""
	}
	if strings.HasPrefix(n.ID, "approval:") && strings.HasSuffix(n.ID, ":"+action) {
		return n.ID
	}
	for _, child := range n.Children {
		if id := treeButtonID(child, action); id != "" {
			return id
		}
	}
	return ""
}

// TestApprovalClickReachesTheBridge drives an approval button from the
// rendered panel through the entrypoint and proves the click is routed to
// Service.ApprovalClick: the bridge receives the signed request's answer.
func TestApprovalClickReachesTheBridge(t *testing.T) {
	state := idleState("example-model")
	state["session"] = map[string]any{"id": "s1"}
	state["approval"] = map[string]any{
		"pending": true, "tool_name": "terminal", "message": "Run go test ./...",
		"request": map[string]any{"cmd": "go test ./..."},
	}
	approvals := make(chan map[string]any, 4)
	remoteHost, remotePort := fakeRemoteState(t, state, func(body map[string]any) { approvals <- body })
	h, _ := startPlugin(t)
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(remoteHost, remotePort)}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720}); err != nil {
		t.Fatal(err)
	}
	snap := h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool {
		return treeButtonID(s.Root, hermes.ApprovalAllow) != ""
	})
	node := treeButtonID(snap.Root, hermes.ApprovalAllow)
	if err := h.send(&v1.InputEvent{ViewID: "panel-1", Revision: snap.Revision, Node: node, Event: v1.EventActivate}); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-approvals:
		if body["choice"] != "once" {
			t.Fatalf("approval body = %#v, want choice once", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval activate never reached the bridge")
	}
}

// TestPanelStateCarriesTheLastActionError pins the entrypoint wiring for D4:
// a published view's ActionError reaches the rendered panel and summary.
func TestPanelStateCarriesTheLastActionError(t *testing.T) {
	view := hermes.View{Status: hermes.StatusFor(hermes.StatusAttention)}
	view.ActionError = hermes.MsgOffline
	tree := viewTree(&viewState{kind: v1.ViewPanel, entry: "panel"}, view, true, renderEnv{})
	if text := treeText(tree); !strings.Contains(text, hermes.MsgOffline) {
		t.Fatalf("panel tree %q does not say the failed action", text)
	}
	summary := viewTree(&viewState{kind: v1.ViewPanel, entry: "summary"}, view, true, renderEnv{})
	if text := treeText(summary); !strings.Contains(text, hermes.MsgOffline) {
		t.Fatalf("summary tree %q does not say the failed action", text)
	}
}

// TestApprovalRequestReachesTheSummaryAlert pins the entrypoint wiring for
// D5's summary half: the request's description is the alert's message.
func TestApprovalRequestReachesTheSummaryAlert(t *testing.T) {
	view := hermes.View{Status: hermes.StatusFor(hermes.StatusAttention)}
	view.State.Approval = hermes.Approval{Pending: true,
		Request: json.RawMessage(`{"command":"rm -r /tmp/x","description":"recursive delete"}`)}
	s := summaryStateFor(view, renderEnv{})
	if s.ApprovalMessage != "recursive delete" {
		t.Fatalf("approval message = %q, want the request description", s.ApprovalMessage)
	}
	tree := viewTree(&viewState{kind: v1.ViewPanel, entry: "summary"}, view, true, renderEnv{})
	if text := treeText(tree); !strings.Contains(text, "recursive delete") {
		t.Fatalf("summary tree %q does not say the reason", text)
	}
}

// panelEntry matches panel.open or panel.close params for one entry.
func panelEntry(entry string) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var params v1.PanelParams
		return json.Unmarshal(raw, &params) == nil && params.Entry == entry
	}
}

// openTestView opens a view and returns the first snapshot the plugin sends.
func openTestView(t *testing.T, h *testHost, open *v1.ViewOpen) *v1.ViewSnapshot {
	t.Helper()
	if err := h.send(open); err != nil {
		t.Fatalf("view.open %s: %v", open.ViewID, err)
	}
	return h.waitSnapshot(open.ViewID, func(*v1.ViewSnapshot) bool { return true })
}

// clickNode activates one node of the tree the host last saw.
func clickNode(t *testing.T, h *testHost, viewID string, revision uint64, node string) {
	t.Helper()
	if err := h.send(&v1.InputEvent{ViewID: viewID, Revision: revision, Node: node, Event: v1.EventActivate}); err != nil {
		t.Fatalf("input %s on %s: %v", node, viewID, err)
	}
}

// settle ends a click's host round trip: the plugin answers a fresh resync
// for a view it still has open, so any host call that click started has
// reached the host before a test checks what was not sent.
func settle(t *testing.T, h *testHost, viewID string) {
	t.Helper()
	if err := h.send(&v1.ViewResync{ViewID: viewID}); err != nil {
		t.Fatalf("view.resync %s: %v", viewID, err)
	}
	h.waitSnapshot(viewID, func(*v1.ViewSnapshot) bool { return true })
}

// callIndex is the position of the first observed host call of kind that
// matches, or -1 when none did.
func (h *testHost) callIndex(kind v1.CallKind, accept func(json.RawMessage) bool) int {
	h.callMu.Lock()
	defer h.callMu.Unlock()
	for i, call := range h.callLog {
		if call.Call == kind && accept(call.Params) {
			return i
		}
	}
	return -1
}

// lastCallIndex is the position of the last observed host call of kind.
func (h *testHost) lastCallIndex(kind v1.CallKind) int {
	h.callMu.Lock()
	defer h.callMu.Unlock()
	for i := len(h.callLog) - 1; i >= 0; i-- {
		if h.callLog[i].Call == kind {
			return i
		}
	}
	return -1
}

// pinnedClientOnlyHost is clientOnlyHost with the floating surface pinned,
// the state the close-before-raise ordering needs.
func pinnedClientOnlyHost(t *testing.T) *testHost {
	t.Helper()
	remoteHost, remotePort := fakeRemoteState(t, idleState("example-model"))
	h, _ := startPluginWith(t, func(h *testHost) { h.reply = surfaceReply(false, "") })
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(remoteHost, remotePort)}); err != nil {
		t.Fatal(err)
	}
	settings := openTestView(t, h, &v1.ViewOpen{ViewID: "settings-1", View: v1.ViewPanel,
		Entry: "settings", Width: 640, Height: 720})
	clickNode(t, h, "settings-1", settings.Revision, "pin-side-panel")
	h.waitCall(v1.CallSurfaceOpen)
	// The loop sets the pinned flag in the post that follows the host call,
	// and republishes with it, so that snapshot proves the flag is set.
	h.waitSnapshot("settings-1", func(s *v1.ViewSnapshot) bool { return s.Revision > settings.Revision })
	return h
}

func clientOnlyHost(t *testing.T) *testHost {
	t.Helper()
	remoteHost, remotePort := fakeRemoteState(t, idleState("example-model"))
	h, _ := startPlugin(t)
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(remoteHost, remotePort)}); err != nil {
		t.Fatal(err)
	}
	return h
}

func summaryView() *v1.ViewOpen {
	return &v1.ViewOpen{ViewID: "summary-1", View: v1.ViewPanel, Entry: "summary", Instance: "bar-inst",
		Output: "DP-1", Generation: 7, Width: 380, Height: 480}
}

func menuView() *v1.ViewOpen {
	return &v1.ViewOpen{ViewID: "menu-1", View: v1.ViewPanel, Entry: "menu", Instance: "bar-inst",
		Output: "DP-1", Generation: 7, Width: 260, Height: 300}
}

func barView() *v1.ViewOpen {
	return &v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Instance: "bar-inst",
		Output: "DP-1", Generation: 7, Width: 240, Height: 32}
}

// TestBarPrimaryTogglesTheSummary is Task 4: a primary click opens the
// summary, and a second one closes it instead of opening anything.
func TestBarPrimaryTogglesTheSummary(t *testing.T) {
	h := clientOnlyHost(t)
	bar := openTestView(t, h, barView())
	clickNode(t, h, "bar-1", bar.Revision, "open")

	call := h.waitCallParams(v1.CallPanelOpen, panelEntry("summary"))
	var params v1.PanelParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatalf("panel.open params: %v", err)
	}
	if params.Output != "DP-1" || params.Generation != 7 || params.Instance != "bar-inst" {
		t.Fatalf("summary opened away from the bar placement: %+v", params)
	}
	openTestView(t, h, summaryView())

	opens := h.countCalls(v1.CallPanelOpen)
	clickNode(t, h, "bar-1", bar.Revision, "open")
	h.waitCallParams(v1.CallPanelClose, panelEntry("summary"))
	if got := h.countCalls(v1.CallPanelOpen); got != opens {
		t.Fatalf("the second primary opened %d panels, want none", got-opens)
	}
}

// TestBarSecondaryOpensTheMenu is Task 4: secondary opens the menu, and the
// primary press that arrives as a pointer event does nothing.
func TestBarSecondaryOpensTheMenu(t *testing.T) {
	h := clientOnlyHost(t)
	bar := openTestView(t, h, barView())
	if err := h.send(&v1.InputEvent{ViewID: "bar-1", Revision: bar.Revision, Node: "open",
		Event: v1.EventPointer, Button: v1.ButtonPrimary}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.InputEvent{ViewID: "bar-1", Revision: bar.Revision, Node: "open",
		Event: v1.EventPointer, Button: v1.ButtonSecondary}); err != nil {
		t.Fatal(err)
	}
	call := h.waitCallParams(v1.CallPanelOpen, panelEntry("menu"))
	var params v1.PanelParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatalf("panel.open params: %v", err)
	}
	if params.Output != "DP-1" || params.Generation != 7 || params.Instance != "bar-inst" {
		t.Fatalf("menu opened away from the bar placement: %+v", params)
	}
	if got := h.countCalls(v1.CallPanelOpen); got != 1 {
		t.Fatalf("%d panels opened, want only the menu", got)
	}
}

// TestMenuChoiceReplacesTheMenu is QA-2: the items that open another plugin
// panel must not close the menu first. The host's panel.open already replaces
// the menu, and a panel.close of ours can reach the host after that open and
// close the panel the click just opened.
func TestMenuChoiceReplacesTheMenu(t *testing.T) {
	cases := []struct{ node, entry string }{
		{"open", "panel"},
		{"new-session", "panel"},
		{"settings", "settings"},
	}
	for _, tc := range cases {
		t.Run(tc.node, func(t *testing.T) {
			h := clientOnlyHost(t)
			menu := openTestView(t, h, menuView())
			clickNode(t, h, "menu-1", menu.Revision, tc.node)
			h.waitCallParams(v1.CallPanelOpen, panelEntry(tc.entry))
			settle(t, h, "menu-1")
			if got := h.countCalls(v1.CallPanelClose); got != 0 {
				t.Fatalf("%s sent %d panel.close calls, want the open to replace the menu", tc.node, got)
			}
			if got := h.countCalls(v1.CallPanelOpen); got != 1 {
				t.Fatalf("%s opened %d panels, want exactly the %s panel", tc.node, got, tc.entry)
			}
		})
	}
}

// TestMenuActsCloseOnly keeps the other menu items as they were: they do
// their work and close the menu, and nothing opens.
func TestMenuActsCloseOnly(t *testing.T) {
	for _, node := range []string{"interrupt", "refresh"} {
		t.Run(node, func(t *testing.T) {
			h := clientOnlyHost(t)
			menu := openTestView(t, h, menuView())
			clickNode(t, h, "menu-1", menu.Revision, node)
			h.waitCallParams(v1.CallPanelClose, panelEntry("menu"))
			settle(t, h, "menu-1")
			if got := h.countCalls(v1.CallPanelClose); got != 1 {
				t.Fatalf("%s sent %d panel.close calls, want exactly one", node, got)
			}
			if got := h.countCalls(v1.CallPanelOpen); got != 0 {
				t.Fatalf("%s opened %d panels, want none", node, got)
			}
		})
	}
}

// TestMenuOpenWhilePinnedClosesTheMenuFirst is QA-2's other half: with the
// surface pinned the menu is a separate surface, so it is closed before the
// raise, in that order.
func TestMenuOpenWhilePinnedClosesTheMenuFirst(t *testing.T) {
	h := pinnedClientOnlyHost(t)
	menu := openTestView(t, h, menuView())
	raised := h.countCalls(v1.CallSurfaceOpen)
	clickNode(t, h, "menu-1", menu.Revision, "open")
	deadline := time.Now().Add(2 * time.Second)
	for h.countCalls(v1.CallSurfaceOpen) == raised {
		if time.Now().After(deadline) {
			t.Fatal("open did not raise the pinned surface")
		}
		time.Sleep(5 * time.Millisecond)
	}
	closeAt := h.callIndex(v1.CallPanelClose, panelEntry("menu"))
	openAt := h.lastCallIndex(v1.CallSurfaceOpen)
	if closeAt < 0 {
		t.Fatal("the menu was not closed before the pinned surface was raised")
	}
	if closeAt > openAt {
		t.Fatalf("panel.close at %d reached the host after surface.open at %d", closeAt, openAt)
	}
	if got := h.countCalls(v1.CallPanelClose); got != 1 {
		t.Fatalf("sent %d panel.close calls, want exactly the menu's", got)
	}
	if got := h.countCalls(v1.CallPanelOpen); got != 0 {
		t.Fatalf("asked for %d plugin panels, want none while pinned", got)
	}
}

// TestMenuOmitsStartGatewayInClientOnly is Task 5: no Start gateway button
// client-only, and the node cannot start anything.
func TestMenuOmitsStartGatewayInClientOnly(t *testing.T) {
	h := clientOnlyHost(t)
	menu := openTestView(t, h, menuView())
	if id := treeButtonID(menu.Root, "start-gateway"); id != "" {
		t.Fatalf("client-only menu offers start-gateway as %q", id)
	}
	opens := h.countCalls(v1.CallPanelOpen)
	clickNode(t, h, "menu-1", menu.Revision, "start-gateway")
	time.Sleep(200 * time.Millisecond)
	if got := h.countCalls(v1.CallPanelOpen); got != opens {
		t.Fatalf("start-gateway opened %d panels, want none", got-opens)
	}
}

// TestSummaryChoiceReplacesIt is QA-2 on the summary: Open Hermes, New
// session and Settings must not pair a close with the open.
func TestSummaryChoiceReplacesIt(t *testing.T) {
	cases := []struct{ node, entry string }{
		{"open", "panel"},
		{"new-session", "panel"},
		{"settings", "settings"},
	}
	for _, tc := range cases {
		t.Run(tc.node, func(t *testing.T) {
			h := clientOnlyHost(t)
			sum := openTestView(t, h, summaryView())
			clickNode(t, h, "summary-1", sum.Revision, tc.node)
			h.waitCallParams(v1.CallPanelOpen, panelEntry(tc.entry))
			settle(t, h, "summary-1")
			if got := h.countCalls(v1.CallPanelClose); got != 0 {
				t.Fatalf("%s sent %d panel.close calls, want the open to replace the summary", tc.node, got)
			}
			if got := h.countCalls(v1.CallPanelOpen); got != 1 {
				t.Fatalf("%s opened %d panels, want exactly the %s panel", tc.node, got, tc.entry)
			}
		})
	}
	for _, node := range []string{"interrupt", "refresh"} {
		t.Run(node+"KeepsItOpen", func(t *testing.T) {
			h := clientOnlyHost(t)
			sum := openTestView(t, h, summaryView())
			clickNode(t, h, "summary-1", sum.Revision, node)
			settle(t, h, "summary-1")
			if got := h.countCalls(v1.CallPanelClose); got != 0 {
				t.Fatalf("%s closed the summary", node)
			}
		})
	}
}

// --- D2: settings cached in plugin state survive a respawn ---

// shortGrace shortens the grace that lets host settings beat the cache.
func shortGrace(t *testing.T) {
	t.Helper()
	old := settingsGrace
	settingsGrace = 20 * time.Millisecond
	t.Cleanup(func() { settingsGrace = old })
}

// settingsCacheReply answers state.get for one key; other host calls keep the
// default reply.
func settingsCacheReply(key string, found bool, value string, fail bool) func(*v1.HostCall) *v1.HostReply {
	return func(call *v1.HostCall) *v1.HostReply {
		if call.Call != v1.CallStateGet {
			return nil
		}
		var p v1.StateGetParams
		if err := json.Unmarshal(call.Params, &p); err != nil || p.Key != key {
			return nil
		}
		if fail {
			return &v1.HostReply{ID: call.ID, OK: false, Error: "state backend down"}
		}
		result, _ := json.Marshal(v1.StateGetResult{Found: found, Value: json.RawMessage(value)})
		return &v1.HostReply{ID: call.ID, OK: true, Result: result}
	}
}

// waitOnlineAndActive takes the Online bar and the active status from one
// stream, because either can arrive first.
func waitOnlineAndActive(t *testing.T, h *testHost) {
	t.Helper()
	var snap *v1.ViewSnapshot
	var status *v1.ServiceStatus
	deadline := time.After(h.deadline())
	for snap == nil || status == nil {
		select {
		case m := <-h.msgs:
			switch v := m.(type) {
			case *v1.ViewSnapshot:
				if v.ViewID == "bar-1" && treeHasName(v.Root, "Online") {
					snap = v
				}
			case *v1.ServiceStatus:
				if v.State == v1.ServiceStateActive {
					status = v
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for the Online bar and active status (snapshot=%v status=%v)", snap != nil, status != nil)
		}
	}
	if status.Message != "Online" {
		t.Fatalf("service status = %+v", status)
	}
}

func TestSettingsChangedSavesHostSettingsCache(t *testing.T) {
	host, port := fakeRemote(t, "idle")
	h, _ := startPlugin(t)
	values := clientOnlyValues(host, port)
	const token = "test-token-not-secret"
	values["bridge_token_manual"] = token
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: values}); err != nil {
		t.Fatal(err)
	}
	h.waitCallParams(v1.CallStateSet, func(raw json.RawMessage) bool {
		var p v1.StateSetParams
		if err := json.Unmarshal(raw, &p); err != nil || p.Key != settingsCacheKey {
			return false
		}
		var got map[string]any
		if err := json.Unmarshal(p.Value, &got); err != nil {
			return false
		}
		return got["client_only_mode"] == true && got["bridge_token_manual"] == token
	})
	time.Sleep(50 * time.Millisecond)
	if s := h.stderr.String(); strings.Contains(s, token) {
		t.Fatalf("stderr leaked the bridge token: %s", s)
	}
}

func TestRespawnAppliesCachedSettingsAfterGrace(t *testing.T) {
	shortGrace(t)
	host, port := fakeRemote(t, "idle")
	cached, err := json.Marshal(clientOnlyValues(host, port))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := startPluginWith(t, func(h *testHost) {
		h.reply = settingsCacheReply(settingsCacheKey, true, string(cached), false)
	})
	if err := h.send(barView()); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Connecting…") })
	waitOnlineAndActive(t, h)
	if h.countCalls(v1.CallStateSet) > 0 {
		// A state.set may only be the pin; the cache must not be re-saved.
		var p v1.StateSetParams
		h.callMu.Lock()
		for _, call := range h.callLog {
			if call.Call == v1.CallStateSet && json.Unmarshal(call.Params, &p) == nil && p.Key == settingsCacheKey {
				h.callMu.Unlock()
				t.Fatal("the restored cache was written back to plugin state")
			}
		}
		h.callMu.Unlock()
	}
}

func TestRespawnWithDefaultSettingsCacheApplies(t *testing.T) {
	// The defaults name ~/.cache/sysc-hermes/state.json, ~/.hermes and the
	// bridge log under the cache home, and the spawn opens the log before it
	// starts the bridge. Point every home at a throwaway directory so a test
	// run can never create pieces of a real environment.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	opts := hermes.Options{LocalPort: 1, DefaultScript: filepath.Join(home, "missing-bridge.py")}

	// The host sends nil values when the user never edited a setting, so the
	// saved copy must be an empty object, not JSON null.
	t.Run("saves an empty object for nil values", func(t *testing.T) {
		h, _ := startPluginOptions(t, opts, func(*testHost) {})
		if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin}); err != nil {
			t.Fatal(err)
		}
		h.waitCallParams(v1.CallStateSet, func(raw json.RawMessage) bool {
			var p v1.StateSetParams
			if err := json.Unmarshal(raw, &p); err != nil || p.Key != settingsCacheKey {
				return false
			}
			return string(p.Value) == "{}"
		})
	})

	// An all-defaults copy applies the manifest defaults, so the bar leaves
	// the Connecting… placeholder. A stored null is this plugin's own old
	// copy. An unreachable local port and a missing script keep local mode
	// away from a real bridge and let the spawn fail before it starts
	// anything.
	for _, tc := range []struct{ name, value string }{
		{"null", "null"},
		{"empty object", "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortGrace(t)
			h, _ := startPluginOptions(t, opts, func(h *testHost) {
				h.reply = settingsCacheReply(settingsCacheKey, true, tc.value, false)
			})
			if err := h.send(barView()); err != nil {
				t.Fatal(err)
			}
			h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Connecting…") })
			h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return !strings.Contains(treeText(s.Root), "Connecting…") })
			if s := h.stderr.String(); !strings.Contains(s, "using the settings saved at the last start") {
				t.Fatalf("stderr does not carry the restore note: %s", s)
			}
		})
	}
}

func TestHostSettingsBeatCache(t *testing.T) {
	// Host settings must win the race against the cache. Model production
	// ordering with a grace wide enough for them to arrive under -race.
	old := settingsGrace
	settingsGrace = 500 * time.Millisecond
	t.Cleanup(func() { settingsGrace = old })
	var cachedHits atomic.Int64
	cachedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cachedHits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(cachedSrv.Close)
	cachedHost, cachedPortText, _ := net.SplitHostPort(cachedSrv.Listener.Addr().String())
	cachedPort, _ := strconv.Atoi(cachedPortText)
	cached, _ := json.Marshal(clientOnlyValues(cachedHost, cachedPort))

	host, port := fakeRemoteState(t, idleState("example-model"))
	h, _ := startPluginWith(t, func(h *testHost) {
		h.reply = settingsCacheReply(settingsCacheKey, true, string(cached), false)
	})
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateActive
	})
	time.Sleep(3 * settingsGrace)
	if n := cachedHits.Load(); n != 0 {
		t.Fatalf("the cached bridge was contacted %d times, want none", n)
	}
}

func TestCacheThenIdenticalHostSettingsDoesNotReconnect(t *testing.T) {
	shortGrace(t)
	var detects atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect" {
			detects.Add(1)
		}
		if r.Header.Get("X-Bridge-Token") != "fake-token" && r.URL.Path != "/detect" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/state" {
			_ = json.NewEncoder(w).Encode(idleState("example-model"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bridge": map[string]any{"status": "online"},
			"hermes": map[string]any{"status": "idle"},
		})
	}))
	t.Cleanup(srv.Close)
	host, portText, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	values := clientOnlyValues(host, port)
	cached, _ := json.Marshal(values)

	h, _ := startPluginWith(t, func(h *testHost) {
		h.reply = settingsCacheReply(settingsCacheKey, true, string(cached), false)
	})
	if err := h.send(barView()); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateActive
	})
	deadline := time.Now().Add(2 * time.Second)
	for detects.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := detects.Load(); n != 1 {
		t.Fatalf("detect calls after the cached start = %d, want 1", n)
	}
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: values}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := detects.Load(); n != 1 {
		t.Fatalf("identical host settings reconnected: %d detect calls, want 1", n)
	}
}

func TestNoCacheStaysConnecting(t *testing.T) {
	cases := []struct {
		name string
		want string
		call func(*v1.HostCall) *v1.HostReply
	}{
		{"missing", "no saved settings", settingsCacheReply(settingsCacheKey, false, "", false)},
		{"error", "cannot read saved settings", settingsCacheReply(settingsCacheKey, false, "", true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shortGrace(t)
			h, _ := startPluginWith(t, func(h *testHost) { h.reply = tc.call })
			if err := h.send(barView()); err != nil {
				t.Fatal(err)
			}
			h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Connecting…") })
			time.Sleep(3 * settingsGrace)
			if err := h.send(&v1.ViewResync{ViewID: "bar-1"}); err != nil {
				t.Fatal(err)
			}
			h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Connecting…") })
			if s := h.stderr.String(); !strings.Contains(s, tc.want) {
				t.Fatalf("stderr does not carry %q: %s", tc.want, s)
			}
		})
	}
}

// TestCachedSettingsWaitForTheGrace pins the ~2 s wait (qa-d2c m3): the
// saved copy is not applied before the grace has passed, even when the
// state.get reply is already in. The default grace is pinned separately.
func TestCachedSettingsWaitForTheGrace(t *testing.T) {
	old := settingsGrace
	settingsGrace = 400 * time.Millisecond
	t.Cleanup(func() { settingsGrace = old })
	var firstHit atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHit.CompareAndSwap(0, time.Now().UnixNano())
		if r.Header.Get("X-Bridge-Token") != "fake-token" && r.URL.Path != "/detect" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bridge": map[string]any{"status": "online"},
			"hermes": map[string]any{"status": "idle"},
		})
	}))
	t.Cleanup(srv.Close)
	host, portText, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	cached, _ := json.Marshal(clientOnlyValues(host, port))

	start := time.Now()
	h, _ := startPluginWith(t, func(h *testHost) {
		h.reply = settingsCacheReply(settingsCacheKey, true, string(cached), false)
	})
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateActive
	})
	hit := firstHit.Load()
	if hit == 0 {
		t.Fatal("the cached bridge was never contacted")
	}
	if waited := time.Unix(0, hit).Sub(start); waited < settingsGrace {
		t.Fatalf("the saved settings were applied after %s, before the %s grace", waited, settingsGrace)
	}
}

func TestSettingsGraceDefault(t *testing.T) {
	if settingsGrace != 2*time.Second {
		t.Fatalf("settings grace = %s, want 2s", settingsGrace)
	}
}

// TestRestoreBackoffDefault pins the package default of the pinned-restore
// retry delays. Every D7 test overrides restoreBackoff, so without this an
// emptied or changed default went unnoticed (qa-t11c B1).
func TestRestoreBackoffDefault(t *testing.T) {
	want := []time.Duration{
		250 * time.Millisecond,
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	}
	if !reflect.DeepEqual(restoreBackoff, want) {
		t.Fatalf("restoreBackoff = %v, want %v", restoreBackoff, want)
	}
}

// treeHasID reports whether any node in the tree carries id.
func treeHasID(n *v1.Node, id string) bool {
	if n == nil {
		return false
	}
	if n.ID == id {
		return true
	}
	for _, c := range n.Children {
		if treeHasID(c, id) {
			return true
		}
	}
	return false
}

// TestComposerOneShotDoesNotHoldTheActionQueue: a one-shot sent from the
// composer runs `hermes -z` for up to ten minutes. It used to go through the
// serial action queue, so an interrupt, an approval click or a new session
// waited behind it. Service.OneShot runs it off the queue, as the launcher's
// Ask already does.
func TestComposerOneShotDoesNotHoldTheActionQueue(t *testing.T) {
	release := make(chan struct{})
	oneshots := make(chan struct{}, 4)
	interrupts := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/detect":
			_, _ = io.WriteString(w, `{}`)
			return
		case "/health":
			_, _ = io.WriteString(w, `{"bridge":{"status":"online"}}`)
			return
		}
		if r.Header.Get("X-Bridge-Token") != "fake-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/state":
			_ = json.NewEncoder(w).Encode(idleState("example-model"))
		case "/oneshot":
			oneshots <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			_, _ = io.WriteString(w, `{"result":{"status":"completed"}}`)
		case "/interrupt":
			interrupts <- struct{}{}
			_, _ = io.WriteString(w, `{}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	host, portText, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	h, _ := startPlugin(t)
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720}); err != nil {
		t.Fatal(err)
	}
	snap := h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool { return treeHasID(s.Root, "one-shot") })
	clickNode(t, h, "panel-1", snap.Revision, "one-shot")
	snap = h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool { return s.Revision > snap.Revision })
	if err := h.send(&v1.InputEvent{ViewID: "panel-1", Revision: snap.Revision, Node: "composer",
		Event: v1.EventSubmit, Text: "what is the time"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oneshots:
	case <-time.After(3 * time.Second):
		t.Fatal("the composer one-shot never reached the bridge")
	}
	if err := h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "interrupt"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("the interrupt waited behind the running one-shot")
	}
}

// modelBridge is a client-only bridge whose live model is model; it hands
// every /model body to the returned channel.
func modelBridge(t *testing.T, model string) (string, int, <-chan map[string]any) {
	t.Helper()
	bodies := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/detect":
			_, _ = io.WriteString(w, `{}`)
			return
		case "/health":
			_, _ = io.WriteString(w, `{"bridge":{"status":"online"}}`)
			return
		}
		if r.Header.Get("X-Bridge-Token") != "fake-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/state":
			_ = json.NewEncoder(w).Encode(idleState(model))
		case "/model":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies <- body
			_, _ = io.WriteString(w, `{}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	host, portText, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	return host, port, bodies
}

// openModelSection connects to the bridge, opens the panel and its Model
// section, and returns the snapshot that shows the Apply button.
func openModelSection(t *testing.T, h *testHost, host string, port int, model string) *v1.ViewSnapshot {
	t.Helper()
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720}); err != nil {
		t.Fatal(err)
	}
	snap := h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool {
		return treeHasID(s.Root, "section:model") && strings.Contains(treeText(s.Root), model)
	})
	clickNode(t, h, "panel-1", snap.Revision, "section:model")
	return h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool { return treeHasID(s.Root, "model-apply") })
}

// TestModelApplyShowsALocalRefusal: a model name the bridge would reject is
// refused before it is sent, and the refusal shows where a failed action
// does instead of the click doing nothing (R8).
func TestModelApplyShowsALocalRefusal(t *testing.T) {
	host, port, bodies := modelBridge(t, "bad model")
	h, _ := startPlugin(t)
	snap := openModelSection(t, h, host, port, "bad model")
	clickNode(t, h, "panel-1", snap.Revision, "model-apply")
	h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool {
		return strings.Contains(treeText(s.Root), "Invalid model or provider name")
	})
	settle(t, h, "panel-1")
	select {
	case body := <-bodies:
		t.Fatalf("posted /model %v for a refused name", body)
	default:
	}
}

// TestModelApplyKeepsTheLiveModel: toggling Persist and pressing Apply
// applies the model the picker shows, the live one, instead of an empty
// picker the click silently refused.
func TestModelApplyKeepsTheLiveModel(t *testing.T) {
	host, port, bodies := modelBridge(t, "example-model")
	h, _ := startPlugin(t)
	snap := openModelSection(t, h, host, port, "example-model")
	clickNode(t, h, "panel-1", snap.Revision, "model-persist")
	snap = h.waitSnapshot("panel-1", func(s *v1.ViewSnapshot) bool { return s.Revision > snap.Revision })
	if !strings.Contains(treeText(snap.Root), "example-model") {
		t.Fatal("the picker dropped the live model after Persist")
	}
	clickNode(t, h, "panel-1", snap.Revision, "model-apply")
	select {
	case body := <-bodies:
		want := map[string]any{"provider": "openrouter", "model": "example-model", "persist": true}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("/model body = %v, want %v", body, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Apply never posted /model")
	}
}
