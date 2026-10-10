package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// TestPluginHerdrGate builds the Herdr binary once and drives it twice: once
// against a hermetic fake herdr (a scripted CLI plus a newline-JSON socket
// server), and once with herdr absent from PATH. No real herdr, network, or
// user state is touched: the fake CLI fabricates the session list and the
// socket serves a canned v0.9.1 snapshot, then pushes a status change.
func TestPluginHerdrGate(t *testing.T) {
	bin, _, panelW, panelH := buildHerdrPlugin(t)
	t.Run("protocol", func(t *testing.T) { herdrProtocolScenario(t, bin, panelW, panelH) })
	t.Run("herdr missing", func(t *testing.T) { herdrMissingScenario(t, bin, panelW, panelH) })
}

// herdrProtocolScenario runs the bar -> panel -> focus/read -> notify flow
// against the fake herdr socket and CLI.
func herdrProtocolScenario(t *testing.T, bin string, panelW, panelH int) {
	tmp := t.TempDir()
	fake := startFakeHerdr(t, tmp)
	binDir := writeFakeHerdrCLI(t, tmp, fake.sockPath, filepath.Join(tmp, "state-probe"))
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	h := startHerdrHost(t, bin, binDir, home, panelW, panelH)

	// Step 2: the bar opens with the open control, and the fake CLI is told
	// to list one running session named probe.
	h.open("bar-1", v1.ViewBar, "bar", "herdr-1", 240, 32)
	h.open("tip-1", v1.ViewTooltip, "bar", "herdr-1", 280, 200)
	h.waitView("bar-1", func(n *v1.Node) bool { return findID(n, "open") != nil })

	// Step 3: activating open asks the host for the panel; the host answers
	// with a 440x560 ViewOpen, and the panel must carry every action node.
	h.click("bar-1", "open")
	h.wait("a panel.open call", func() bool { return h.count(v1.CallPanelOpen) == 1 })
	h.waitView("panel-1", func(n *v1.Node) bool {
		for _, id := range []string{
			"refresh", "close", "attach:probe", "stop:probe",
			"focus:probe:w1:p1", "read:probe:w1:p1",
		} {
			if findID(n, id) == nil {
				return false
			}
		}
		return strings.Contains(treeText(n), "probe")
	})
	if open := h.panelOpens(); len(open) != 1 || open[0].Entry != "panel" || open[0].Instance != "herdr-1" {
		t.Fatalf("panel.open params = %+v", open)
	}

	// Step 4a: focus reaches the socket as agent.focus {target: W1:P1}.
	h.click("panel-1", "focus:probe:w1:p1")
	select {
	case target := <-fake.focus:
		if target != "w1:p1" {
			t.Fatalf("agent.focus target = %q, want w1:p1", target)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fake herdr never received agent.focus")
	}
	fake.waitRequest(t, "agent.focus")

	// Step 4b: read round-trips the peek text back into the panel.
	h.click("panel-1", "read:probe:w1:p1")
	fake.waitRequest(t, "agent.read")
	h.waitView("panel-1", func(n *v1.Node) bool {
		return strings.Contains(treeText(n), "hello from herdr")
	})

	// Step 5: a working -> blocked push must surface as a notify call.
	fake.trigger()
	h.wait("a notify call", func() bool { return len(h.notifies()) > 0 })
	notes := h.notifies()
	if !strings.Contains(notes[0].Summary, "blocked") {
		t.Errorf("notify summary = %q, want it to name blocked", notes[0].Summary)
	}
	if !strings.Contains(notes[0].Body, "probe") {
		t.Errorf("notify body = %q, want it to name the session", notes[0].Body)
	}
}

// herdrMissingScenario launches the same binary with herdr off PATH and
// asserts the bar still serves and the panel degrades to the missing card.
func herdrMissingScenario(t *testing.T, bin string, panelW, panelH int) {
	tmp := t.TempDir()
	nobin := filepath.Join(tmp, "nobin")
	if err := os.MkdirAll(nobin, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	h := startHerdrHost(t, bin, nobin, home, panelW, panelH)

	h.open("bar-1", v1.ViewBar, "bar", "herdr-1", 240, 32)
	h.waitView("bar-1", func(n *v1.Node) bool { return findID(n, "open") != nil })
	h.click("bar-1", "open")
	h.waitView("panel-1", func(n *v1.Node) bool {
		return strings.Contains(treeText(n), "herdr not found")
	})
}

// buildHerdrPlugin compiles the plugin into a TempDir plugin layout and copies
// the manifest beside it, so the process resolves its identity normally.
func buildHerdrPlugin(t *testing.T) (bin string, pluginDir string, panelW, panelH int) {
	t.Helper()
	root := repoRoot(t)
	pluginDir = filepath.Join(t.TempDir(), "org.sysc.herdr")
	bin = filepath.Join(pluginDir, "bin", "sysc-plugin-herdr")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/herdr/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	var declared struct {
		Panels []struct{ Width, Height int } `json:"panels"`
	}
	if err := json.Unmarshal(manifest, &declared); err != nil || len(declared.Panels) == 0 {
		t.Fatalf("manifest panels: %v %+v", err, declared)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-herdr")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build herdr: %v\n%s", err, out)
	}
	return bin, pluginDir, declared.Panels[0].Width, declared.Panels[0].Height
}

// writeFakeHerdrCLI installs a shell script named herdr that answers
// `session list --json` from fabricated sessions and acknowledges every other
// subcommand, mirroring the real CLI's JSON keys.
func writeFakeHerdrCLI(t *testing.T, dir, sock, sessionDir string) string {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	list := fmt.Sprintf(`{"sessions":[{"name":"default","default":true,"running":false,"session_dir":%q,"socket_path":%q},{"name":"probe","default":false,"running":true,"session_dir":%q,"socket_path":%q}]}`,
		filepath.Join(dir, "state-default"), filepath.Join(dir, "default.sock"), sessionDir, sock)
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"session\" ] && [ \"$2\" = \"list\" ]; then\n" +
		"  printf '%s\\n' '" + list + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"printf '{}\\n'\n"
	path := filepath.Join(binDir, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir
}

// fakeHerdr is a hermetic stand-in for the herdr socket server: one accept
// loop, per-connection handlers, one-shot replies for calls and a held-open
// connection for events.subscribe.
type fakeHerdr struct {
	sockPath string
	snapshot []byte

	mu       sync.Mutex
	requests []string
	focus    chan string
	push     chan struct{}
	pushOnce sync.Once
}

func startFakeHerdr(t *testing.T, dir string) *fakeHerdr {
	t.Helper()
	sock := filepath.Join(dir, "herdr.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHerdr{
		sockPath: sock,
		snapshot: loadHerdrSnapshotResult(t),
		focus:    make(chan string, 8),
		push:     make(chan struct{}),
	}
	go f.serve(ln)
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

// loadHerdrSnapshotResult reads the captured v0.9.1 reply from the fixture,
// rewrites the agent statuses to working (so the later blocked push is a real
// transition) and the first workspace label to api, and returns the result
// envelope exactly as herdr sends it.
func loadHerdrSnapshotResult(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins/herdr/testdata/snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(env.Result, &obj); err != nil {
		t.Fatal(err)
	}
	snap, ok := obj["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("fixture result has no snapshot: %s", env.Result)
	}
	for _, key := range []string{"agents", "panes", "tabs", "workspaces"} {
		list, _ := snap[key].([]any)
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				if _, has := m["agent_status"]; has {
					m["agent_status"] = "working"
				}
			}
		}
	}
	if ws, ok := snap["workspaces"].([]any); ok && len(ws) > 0 {
		if m, ok := ws[0].(map[string]any); ok {
			m["label"] = "api"
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *fakeHerdr) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeHerdr) handle(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	if !sc.Scan() {
		return
	}
	line := sc.Bytes()
	f.mu.Lock()
	f.requests = append(f.requests, string(line))
	f.mu.Unlock()

	var req struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	switch req.Method {
	case "session.snapshot":
		reply, _ := json.Marshal(map[string]any{"id": req.ID, "result": json.RawMessage(f.snapshot)})
		_, _ = conn.Write(append(reply, '\n'))
	case "events.subscribe":
		_, _ = conn.Write([]byte(`{"id":"` + req.ID + `","result":{}}` + "\n"))
		<-f.push
		_, _ = conn.Write([]byte(`{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1","agent":"claude","agent_status":"blocked","workspace_id":"w1"}}` + "\n"))
		_, _ = io.Copy(io.Discard, conn)
	case "agent.focus":
		var p struct {
			Target string `json:"target"`
		}
		_ = json.Unmarshal(req.Params, &p)
		select {
		case f.focus <- p.Target:
		default:
		}
		_, _ = conn.Write([]byte(`{"id":"` + req.ID + `","result":{}}` + "\n"))
	case "agent.read":
		_, _ = conn.Write([]byte(`{"id":"` + req.ID + `","result":{"text":"hello from herdr","truncated":false}}` + "\n"))
	default:
		_, _ = conn.Write([]byte(`{"id":"` + req.ID + `","result":{}}` + "\n"))
	}
}

// trigger releases the held-open subscription so it emits one working ->
// blocked event. Safe to call more than once.
func (f *fakeHerdr) trigger() { f.pushOnce.Do(func() { close(f.push) }) }

func (f *fakeHerdr) waitRequest(t *testing.T, method string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		found := false
		for _, r := range f.requests {
			if strings.Contains(r, `"`+method+`"`) {
				found = true
				break
			}
		}
		f.mu.Unlock()
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fake herdr never saw a %s request", method)
}

// herdrHost drives the plugin as the host would, answering its calls and
// laying every snapshot out with the host's own rules.
type herdrHost struct {
	t      *testing.T
	enc    *v1.Encoder
	sendMu sync.Mutex
	mu     sync.Mutex
	roots  map[string]*v1.Node
	slots  map[string]viewSlot
	calls  []v1.CallKind
	notes  []v1.NotifyParams
	opens  []v1.PanelParams
	wake   chan struct{}

	panelW, panelH int
}

func startHerdrHost(t *testing.T, bin, pathDir, home string, panelW, panelH int) *herdrHost {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "PATH="+pathDir, "HOME="+home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &herdrHost{
		t:      t,
		enc:    v1.NewEncoder(stdin),
		roots:  map[string]*v1.Node{},
		slots:  map[string]viewSlot{},
		wake:   make(chan struct{}, 1),
		panelW: panelW,
		panelH: panelH,
	}
	dec := v1.NewDecoder(stdout, v1.ToHost)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = h.send(&v1.HostShutdown{})
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("herdr did not exit after host.shutdown")
		}
		_ = stdin.Close()
		if t.Failed() {
			t.Logf("plugin stderr:\n%s", stderr.String())
		}
	})
	go func() {
		for {
			msg, err := dec.Decode()
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *v1.HostCall:
				h.handleCall(m)
			case *v1.ViewSnapshot:
				h.mu.Lock()
				slot, monitored := h.slots[m.ViewID]
				h.roots[m.ViewID] = m.Root
				h.mu.Unlock()
				if monitored {
					checkFits(h.t, slot, m.Root)
				}
				h.ping()
			case *v1.ViewPatch:
				h.ping()
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 16}},
		Plugin:       v1.Identity{ID: "org.sysc.herdr", Name: "Herdr", Version: "0.1.0"},
		Capabilities: []string{"notifications", "panels", "settings"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *herdrHost) send(m v1.Message) error {
	h.sendMu.Lock()
	defer h.sendMu.Unlock()
	return h.enc.Encode(m)
}

func (h *herdrHost) handleCall(m *v1.HostCall) {
	h.mu.Lock()
	h.calls = append(h.calls, m.Call)
	switch m.Call {
	case v1.CallNotify:
		var p v1.NotifyParams
		if json.Unmarshal(m.Params, &p) == nil {
			h.notes = append(h.notes, p)
		}
		h.mu.Unlock()
	case v1.CallPanelOpen:
		var p v1.PanelParams
		if json.Unmarshal(m.Params, &p) != nil {
			h.mu.Unlock()
			break
		}
		h.opens = append(h.opens, p)
		h.slots = recordSlot(h.slots, "panel-1", viewSlot{v1.ViewPanel, h.panelW, h.panelH})
		h.mu.Unlock()
		_ = h.send(&v1.ViewOpen{
			ViewID: "panel-1", View: v1.ViewPanel, Entry: p.Entry, Output: p.Output,
			Instance: p.Instance, Width: h.panelW, Height: h.panelH,
		})
	default:
		h.mu.Unlock()
	}
	_ = h.send(&v1.HostReply{ID: m.ID, OK: true})
	h.ping()
}

func (h *herdrHost) ping() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *herdrHost) open(id string, kind v1.ViewKind, entry, instance string, w, height int) {
	h.t.Helper()
	h.mu.Lock()
	h.slots = recordSlot(h.slots, id, viewSlot{kind, w, height})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: id, View: kind, Entry: entry, Instance: instance, Output: "DP-1", Width: w, Height: height}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *herdrHost) click(view, node string) {
	h.t.Helper()
	if err := h.send(&v1.InputEvent{ViewID: view, Node: node, Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *herdrHost) count(kind v1.CallKind) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if c == kind {
			n++
		}
	}
	return n
}

func (h *herdrHost) notifies() []v1.NotifyParams {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]v1.NotifyParams(nil), h.notes...)
}

func (h *herdrHost) panelOpens() []v1.PanelParams {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]v1.PanelParams(nil), h.opens...)
}

func (h *herdrHost) wait(what string, ok func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		select {
		case <-h.wake:
		case <-time.After(20 * time.Millisecond):
		}
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

func (h *herdrHost) waitView(view string, ok func(*v1.Node) bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root := h.roots[view]
		h.mu.Unlock()
		if root != nil && ok(root) {
			return
		}
		select {
		case <-h.wake:
		case <-time.After(20 * time.Millisecond):
		}
	}
	h.mu.Lock()
	root := h.roots[view]
	h.mu.Unlock()
	h.t.Fatalf("view %s never matched\n%s", view, dumpTree(root))
}
