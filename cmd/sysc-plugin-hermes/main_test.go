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
	pluginIn, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	h := &testHost{t: t, enc: v1.NewEncoder(hostOut), msgs: make(chan v1.Message, 128), done: make(chan error, 1),
		fromPlug: hostIn, stderr: &lockedBuffer{}}
	setup(h)
	go func() {
		h.done <- runWith(pluginIn, pluginOut, h.stderr, hermes.Options{})
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
			if !h.silent.Load() {
				_ = h.send(&v1.HostReply{ID: call.ID, OK: true})
			}
		}
		select {
		case h.msgs <- msg:
		default:
		}
	}
}

func (h *testHost) wait(accept func(v1.Message) bool) v1.Message {
	h.t.Helper()
	deadline := time.After(2 * time.Second)
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
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Width: 240, Height: 32}); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Connecting…") })
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	snap := h.waitSnapshot("bar-1", func(s *v1.ViewSnapshot) bool { return strings.Contains(treeText(s.Root), "Online") })
	if err := v1.Validate(snap.Root, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	status := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateActive
	}).(*v1.ServiceStatus)
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
	v.section, v.composer = "chat", "draft"
	other := vs.open(&v1.ViewOpen{ViewID: "q", View: v1.ViewPanel, Entry: "panel"})
	if other.section != "" || other.composer != "" {
		t.Fatal("per-view state leaked between views")
	}
	vs.close("p")
	if vs.get("p") != nil {
		t.Fatal("close kept the view")
	}
	if again := vs.open(&v1.ViewOpen{ViewID: "p", View: v1.ViewPanel, Entry: "summary"}); again.section != "" || again.composer != "" {
		t.Fatal("reopened view kept the closed view's state")
	}
}

func TestStaleRevisionDroppedStatusOnlyRepublishAccepted(t *testing.T) {
	vs := newViewSet()
	v := vs.open(&v1.ViewOpen{ViewID: "p", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720})
	state := hermes.View{Mode: hermes.ModeClientOnly, Status: hermes.StatusFor(hermes.StatusIdle)}

	rev1, ok := v.next(viewTree(v, state, true))
	if !ok || rev1 != 1 {
		t.Fatalf("first publish rev %d sent=%v", rev1, ok)
	}
	// Same tree again: nothing to send.
	if _, ok := v.next(viewTree(v, state, true)); ok {
		t.Fatal("an unchanged tree was republished")
	}
	// Status-only change: the text moves, the addressable nodes do not.
	state.Status = hermes.StatusFor(hermes.StatusOffline)
	state.Message = hermes.MsgOffline
	rev2, ok := v.next(viewTree(v, state, true))
	if !ok || rev2 != 2 {
		t.Fatalf("status republish rev %d sent=%v", rev2, ok)
	}
	click := func(rev uint64) bool {
		return v.accepts(&v1.InputEvent{ViewID: "p", Revision: rev, Node: "refresh", Event: v1.EventActivate})
	}
	if !click(rev1) || !click(rev2) {
		t.Fatal("a click on the unchanged Refresh button was dropped after a status-only republish")
	}
	// Content change: Refresh is disabled while an action is pending.
	state.Pending = true
	rev3, ok := v.next(viewTree(v, state, true))
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
	if rev, ok := v.next(viewTree(v, state, true)); !ok || rev != 1 {
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
			tree := viewTree(v, st, true)
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
