package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The Moonbit plugin is a pure UI over the moonbit daemon's panel socket: it
// never runs moonbit itself. These gates build the real binary and drive it as
// the host would, against a stub daemon speaking the same one-request-per-
// connection JSON protocol. That is the headless stand-in for "render it in a
// live shell": every snapshot is laid out with the host's own rules at the
// declared slot, and the daemon-side effects (the forced clean narrowed to the
// checked category) are asserted from the requests the stub received.
func TestPluginMoonbitGateScanReviewClean(t *testing.T) {
	stub := newMoonbitStub(t)
	h := launchMoonbitGate(t, stub.path())

	h.openBar("bar-1")
	h.openTooltip("tip-1")
	h.openPanel("panel-1")

	// Idle: the status round-trip lands as a cache summary in the panel and a
	// size label beside the house icon in the bar.
	h.wait("the idle cache summary", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in") &&
			strings.Contains(treeText(h.root("bar-1")), "MiB") &&
			hasIcon(h.root("bar-1"), "home")
	})

	// Scan: the stub holds the stream open mid-scan so the live progress state
	// is observable before review, not just the terminal frame.
	h.click("panel-1", "scan_quick", v1.EventActivate, "")
	h.wait("live scan progress", func() bool {
		prog := findNode(h.root("panel-1"), "scan-prog")
		return prog != nil && prog.Value > 0 && prog.Value < 1 &&
			strings.Contains(treeText(h.root("panel-1")), "Scanning")
	})
	close(stub.scanHold)
	h.wait("the review list", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "choose what to clean") &&
			findNode(h.root("panel-1"), "toggle:Journal Logs") != nil
	})

	// Deselect one category; the confirm button count follows the selection.
	h.click("panel-1", "toggle:Journal Logs", v1.EventActivate, "")
	h.wait("the deselection to land", func() bool {
		return nodeText(h.root("panel-1"), "to_confirm") == "Clean 1 Selected"
	})

	h.click("panel-1", "to_confirm", v1.EventActivate, "")
	h.wait("the confirm screen", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Clean the selected categories")
	})

	// Clean: the same hold trick on the clean stream.
	h.click("panel-1", "confirm_clean", v1.EventActivate, "")
	h.wait("live clean progress", func() bool {
		prog := findNode(h.root("panel-1"), "clean-prog")
		return prog != nil && prog.Value > 0 && prog.Value < 1 &&
			strings.Contains(treeText(h.root("panel-1")), "Cleaning")
	})
	close(stub.cleanHold)
	h.wait("the completion summary", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Freed")
	})

	// The clean request the daemon actually received: forced, and narrowed to
	// the one category the user left checked.
	var sawScan, sawClean bool
	for _, req := range stub.requests() {
		switch req.Cmd {
		case "scan":
			sawScan = true
			if req.Mode != "quick" {
				t.Errorf("scan mode = %q, want quick", req.Mode)
			}
		case "clean":
			sawClean = true
			if !req.Force {
				t.Error("panel clean must send force:true (the daemon dry-runs otherwise)")
			}
			if len(req.Categories) != 1 || req.Categories[0] != "Pacman Cache" {
				t.Errorf("clean categories = %v, want [Pacman Cache]", req.Categories)
			}
		}
	}
	if !sawScan {
		t.Error("no scan request reached the daemon")
	}
	if !sawClean {
		t.Error("no clean request reached the daemon")
	}

	// Back to idle; the panel returns to the cache summary.
	h.click("panel-1", "back", v1.EventActivate, "")
	h.wait("a return to idle", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in")
	})
}

// With no socket the plugin degrades to an explanatory strip instead of
// crashing, and a scan attempt surfaces the unreachable daemon as an error.
func TestPluginMoonbitGateDegradedWithoutDaemon(t *testing.T) {
	h := launchMoonbitGate(t, filepath.Join(t.TempDir(), "absent.sock"))

	h.openBar("bar-1")
	h.openTooltip("tip-1")
	h.openPanel("panel-1")

	h.wait("the idle degraded hint", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Start the daemon with --socket")
	})
	h.click("panel-1", "scan_quick", v1.EventActivate, "")
	h.wait("the unreachable error", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "moonbit daemon unreachable")
	})
}

// moonbitReq is the daemon's line protocol header, as the plugin sends it.
type moonbitReq struct {
	Cmd        string   `json:"cmd"`
	Mode       string   `json:"mode"`
	Force      bool     `json:"force"`
	Categories []string `json:"categories"`
}

// moonbitStub answers one request per connection like the real daemon. The
// scan and clean streams are held open partway so a test can observe the
// plugin's live progress state before the terminal event arrives.
type moonbitStub struct {
	t   *testing.T
	ln  net.Listener
	mu  sync.Mutex
	got []moonbitReq

	scanHold  chan struct{}
	cleanHold chan struct{}
}

func newMoonbitStub(t *testing.T) *moonbitStub {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "panel.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	s := &moonbitStub{t: t, ln: ln, scanHold: make(chan struct{}), cleanHold: make(chan struct{})}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *moonbitStub) path() string { return s.ln.Addr().String() }

func (s *moonbitStub) requests() []moonbitReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]moonbitReq(nil), s.got...)
}

func (s *moonbitStub) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *moonbitStub) handle(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req moonbitReq
	if json.Unmarshal(line, &req) != nil {
		return
	}
	s.mu.Lock()
	s.got = append(s.got, req)
	s.mu.Unlock()

	switch req.Cmd {
	case "ping":
		writeEvents(conn, []map[string]any{{"t": "pong"}})
	case "status":
		writeEvents(conn, []map[string]any{{
			"t": "status", "daemon": true, "scan_count": 3, "clean_count": 1,
			"files_cleaned": 508, "space_freed": 900000000,
			"last_scan": "2026-09-30T10:38:49+10:00", "last_clean": "2026-09-30T09:00:00+10:00",
			"cache": map[string]any{
				"files": 510, "bytes": 911166458, "scanned_at": "2026-09-30T10:38:49+10:00",
				"categories": []map[string]any{
					{"name": "Pacman Cache", "files": 498, "bytes": 782317440},
					{"name": "Journal Logs", "files": 12, "bytes": 128849018},
				},
			},
		}})
	case "scan":
		if !writeEvents(conn, []map[string]any{
			{"t": "category", "name": "Pacman Cache", "i": 1, "total": 2},
			{"t": "scan", "files": 250, "bytes": 400000000, "dir": "/var/cache/pacman/pkg"},
			{"t": "category_done", "name": "Pacman Cache", "files": 498, "bytes": 782317440, "duration_ms": 1200},
		}) {
			return
		}
		<-s.scanHold
		writeEvents(conn, []map[string]any{
			{"t": "category", "name": "Journal Logs", "i": 2, "total": 2},
			{"t": "scan", "files": 510, "bytes": 911166458, "dir": "/var/log/journal"},
			{"t": "category_done", "name": "Journal Logs", "files": 12, "bytes": 128849018, "duration_ms": 80},
			{"t": "done", "files": 510, "bytes": 911166458, "categories": 2, "duration_ms": 1280},
		})
	case "clean":
		if !writeEvents(conn, []map[string]any{
			{"t": "clean_begin", "files": 510, "bytes": 911166458, "dry_run": false,
				"categories": []map[string]any{{"name": "Pacman Cache", "files": 498, "bytes": 782317440}}},
			{"t": "clean", "done": 120, "total": 510, "freed": 40000000, "file": "/var/cache/pacman/pkg/linux.pkg.tar.zst"},
		}) {
			return
		}
		<-s.cleanHold
		writeEvents(conn, []map[string]any{
			{"t": "clean", "done": 510, "total": 510, "freed": 782317440},
			{"t": "clean_done", "deleted": 508, "freed": 900000000, "errors": []string{}},
		})
	}
}

func writeEvents(conn net.Conn, evs []map[string]any) bool {
	for _, ev := range evs {
		b, err := json.Marshal(ev)
		if err != nil {
			return false
		}
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return false
		}
	}
	return true
}

// moonbitHost is the scripted shell side: it answers host calls, records every
// snapshot, and lays each one out at the slot it was opened with.
type moonbitHost struct {
	t   *testing.T
	enc *v1.Encoder

	sendMu sync.Mutex
	mu     sync.Mutex
	roots  map[string]*v1.Node
	slots  map[string]viewSlot
	calls  []v1.CallKind

	panelW, panelH int
}

func launchMoonbitGate(t *testing.T, sockPath string) *moonbitHost {
	t.Helper()
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.moonbit")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-moonbit")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/moonbit/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	// The wordmark header is part of the panel's height budget; copy the real
	// asset so the declared image box is exercised exactly as installed.
	if err := copyFile(filepath.Join(root, "plugins/moonbit/assets/wordmark.png"),
		filepath.Join(pluginDir, "assets", "wordmark.png")); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-moonbit")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build moonbit: %v\n%s", err, out)
	}
	var m struct {
		Panels []struct{ Width, Height int } `json:"panels"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil || len(m.Panels) != 1 {
		t.Fatalf("manifest panels: %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MOONBIT_PANEL_SOCKET="+sockPath)
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
	h := &moonbitHost{
		t: t, enc: v1.NewEncoder(stdin),
		roots: map[string]*v1.Node{}, slots: map[string]viewSlot{},
		panelW: m.Panels[0].Width, panelH: m.Panels[0].Height,
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
			t.Error("moonbit did not exit after host.shutdown")
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
				reply := v1.HostReply{ID: m.ID, OK: true}
				h.mu.Lock()
				h.calls = append(h.calls, m.Call)
				h.mu.Unlock()
				if m.Call == v1.CallPanelOpen {
					reply.Result, _ = json.Marshal(v1.PanelResult{ViewID: "panel-open-1"})
				}
				_ = h.send(&reply)
			case *v1.ViewSnapshot:
				h.mu.Lock()
				slot, ok := h.slots[m.ViewID]
				h.roots[m.ViewID] = m.Root
				h.mu.Unlock()
				if ok {
					checkFits(h.t, slot, m.Root)
				}
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 8}},
		Plugin:       v1.Identity{ID: "org.sysc.moonbit", Name: "Moonbit", Version: "1.0.0"},
		Capabilities: []string{"panels", "state"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *moonbitHost) send(m v1.Message) error {
	h.sendMu.Lock()
	defer h.sendMu.Unlock()
	return h.enc.Encode(m)
}

func (h *moonbitHost) open(id string, kind v1.ViewKind, entry string, w, height int) {
	h.t.Helper()
	h.mu.Lock()
	h.slots = recordSlot(h.slots, id, viewSlot{kind, w, height})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: id, View: kind, Entry: entry, Output: "DP-1", Width: w, Height: height}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *moonbitHost) openBar(id string) {
	h.open(id, v1.ViewBar, "bar", lint.BarWidth, lint.BarHeight)
}
func (h *moonbitHost) openTooltip(id string) {
	h.open(id, v1.ViewTooltip, "bar", lint.TooltipWidth, lint.TooltipHeight)
}
func (h *moonbitHost) openPanel(id string) { h.open(id, v1.ViewPanel, "panel", h.panelW, h.panelH) }

func (h *moonbitHost) click(view, node string, ev v1.EventKind, button v1.PointerButton) {
	h.t.Helper()
	if err := h.send(&v1.InputEvent{ViewID: view, Node: node, Event: ev, Button: button, Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *moonbitHost) root(id string) *v1.Node {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.roots[id]
}

func (h *moonbitHost) wait(what string, ok func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

func hasIcon(n *v1.Node, icon string) bool {
	return walkFind(n, func(x *v1.Node) bool { return x.Icon == icon }) != nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
