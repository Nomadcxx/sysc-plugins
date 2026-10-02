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

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The Moonbit plugin drives `moonbit panel` through sudo, asking the user's
// password for every root run, as starting the TUI with sudo does. These
// gates build the real binary and drive it as the host would; a fake sudo
// bridges each run onto a stub daemon speaking moonbit's one-request JSON
// protocol. Every snapshot is laid out with the host's own rules at the
// declared slot, and the root-side effects (the forced clean narrowed to the
// checked category) are asserted from the requests the stub received.
func TestPluginMoonbitGateScanReviewClean(t *testing.T) {
	stub := newMoonbitStub(t)
	h := launchMoonbitGate(t, stub.path())

	h.openBar("bar-1")
	h.openTooltip("tip-1")
	h.openPanel("panel-1")

	// Idle: the status round-trip lands as a cache summary in the panel and a
	// size label beside the Moonbit mark in the bar.
	h.wait("the idle cache summary", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in") &&
			strings.Contains(treeText(h.root("bar-1")), "MiB") &&
			hasIcon(h.root("bar-1"), "moonbit")
	})

	// Scan: the stub holds the stream open mid-scan so the live progress state
	// is observable before review, not just the terminal frame.
	h.click("panel-1", "scan_quick", v1.EventActivate, "")
	h.password("panel-1", "hunter2")
	h.wait("live scan progress", func() bool {
		prog := findNode(h.root("panel-1"), "scan-prog")
		return prog != nil && prog.Value > 0 && prog.Value < 1 &&
			strings.Contains(treeText(h.root("panel-1")), "Scanning")
	})
	close(stub.scanHold)
	h.wait("the review list", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Choose what to clean") &&
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
	h.password("panel-1", "hunter2")
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
			if req.ScannedAt != gateScannedAt {
				t.Errorf("clean scanned_at = %q, want the reviewed scan's %q", req.ScannedAt, gateScannedAt)
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

// A wrong password keeps the prompt up with a reason, sends nothing to
// moonbit, and a right one then runs the scan.
func TestPluginMoonbitGateWrongPasswordAsksAgain(t *testing.T) {
	stub := newMoonbitStub(t)
	close(stub.scanHold)
	h := launchMoonbitGate(t, stub.path())
	h.openPanel("panel-1")
	h.wait("the idle cache summary", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in")
	})
	h.click("panel-1", "scan_deep", v1.EventActivate, "")
	h.password("panel-1", "wrong")
	h.wait("the rejection", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "wasn't accepted")
	})
	if n := len(stub.requests()); n != 0 {
		t.Fatalf("a wrong password reached moonbit with %d requests", n)
	}
	h.password("panel-1", "hunter2")
	h.wait("the review list", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Choose what to clean")
	})
	if reqs := stub.requests(); len(reqs) != 1 || reqs[0].Cmd != "scan" || reqs[0].Mode != "deep" {
		t.Fatalf("requests = %+v, want one deep scan", reqs)
	}
}

// Docker cleanup and the schedule run as root through the same prompt, as
// the TUI's Docker and Schedule screens do.
func TestPluginMoonbitGateDockerAndSchedule(t *testing.T) {
	stub := newMoonbitStub(t)
	h := launchMoonbitGate(t, stub.path())
	h.openPanel("panel-1")
	h.wait("the idle footer", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "No automatic cleaning scheduled")
	})

	h.click("panel-1", "docker", v1.EventActivate, "")
	h.click("panel-1", "docker:all", v1.EventActivate, "")
	h.wait("the Docker confirm", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Clean all unused Docker resources?")
	})
	h.click("panel-1", "docker_run", v1.EventActivate, "")
	h.password("panel-1", "hunter2")
	h.wait("the Docker result", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Docker freed 1.2GB.")
	})

	h.click("panel-1", "back", v1.EventActivate, "")
	h.click("panel-1", "schedule", v1.EventActivate, "")
	h.wait("the schedule screen", func() bool {
		return findNode(h.root("panel-1"), "sched:timers:enable") != nil
	})
	h.click("panel-1", "sched:timers:enable", v1.EventActivate, "")
	h.password("panel-1", "hunter2")
	h.wait("the schedule result", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Scan and clean timers enabled.")
	})

	var sawDocker, sawSchedule bool
	for _, req := range stub.requests() {
		switch req.Cmd {
		case "docker":
			sawDocker = req.Op == "all"
		case "schedule":
			sawSchedule = req.Target == "timers" && req.Action == "enable"
		}
	}
	if !sawDocker || !sawSchedule {
		t.Fatalf("requests = %+v, want docker all and schedule timers enable", stub.requests())
	}
}

// A deep scan clears absent-path categories in milliseconds. Publishing a
// frame per event for every open view outruns the host's update budget (60/s,
// burst 120), and the host ends the plugin. The burst must coalesce.
func TestPluginMoonbitGateCoalescesAScanBurst(t *testing.T) {
	stub := newMoonbitStub(t)
	stub.burst = 40
	h := launchMoonbitGate(t, stub.path())
	h.openBar("bar-1")
	h.openTooltip("tip-1")
	h.openPanel("panel-1")
	h.wait("the idle cache summary", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in")
	})

	before := h.frameCount()
	h.click("panel-1", "scan_quick", v1.EventActivate, "")
	h.password("panel-1", "hunter2")
	h.wait("the review list", func() bool {
		return findNode(h.root("panel-1"), "toggle:Cache 40") != nil
	})
	time.Sleep(300 * time.Millisecond) // let a trailing frame land
	// 40 categories are 121 events; a frame per event per view is 363.
	if n := h.frameCount() - before; n > 30 {
		t.Fatalf("a 40-category scan published %d frames, want it coalesced to at most 30", n)
	}
}

// Cancel ends the request side only, so the daemon's cancelled event still
// arrives and the panel returns to idle instead of reporting a broken stream.
func TestPluginMoonbitGateCancelReturnsToIdle(t *testing.T) {
	stub := newMoonbitStub(t)
	h := launchMoonbitGate(t, stub.path())
	h.openPanel("panel-1")
	h.wait("the idle cache summary", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in")
	})
	h.click("panel-1", "scan_quick", v1.EventActivate, "")
	h.password("panel-1", "hunter2")
	h.wait("live scan progress", func() bool {
		return findNode(h.root("panel-1"), "scan-prog") != nil
	})
	h.click("panel-1", "cancel", v1.EventActivate, "")
	h.wait("a return to idle", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in")
	})
	if text := treeText(h.root("panel-1")); strings.Contains(text, "unexpectedly") {
		t.Fatalf("a user cancel reads as a fault: %q", text)
	}
}

// The forced clean only fires from the confirm screen the user is looking at:
// not after Back, and not from an event aimed at an older revision.
func TestPluginMoonbitGateConfirmCleanNeedsTheConfirmScreen(t *testing.T) {
	stub := newMoonbitStub(t)
	close(stub.scanHold)
	h := launchMoonbitGate(t, stub.path())
	h.openPanel("panel-1")
	h.wait("the idle cache summary", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in")
	})
	h.click("panel-1", "scan_quick", v1.EventActivate, "")
	h.password("panel-1", "hunter2")
	h.wait("the review list", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Choose what to clean")
	})
	h.click("panel-1", "to_confirm", v1.EventActivate, "")
	h.wait("the confirm screen", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "Clean the selected categories")
	})
	confirmRev := h.rev("panel-1")

	// Stale: one revision behind the confirm screen.
	h.clickAt("panel-1", "confirm_clean", confirmRev-1)
	// After Back, the confirm screen is gone.
	h.click("panel-1", "back", v1.EventActivate, "")
	h.wait("a return to idle", func() bool {
		return strings.Contains(treeText(h.root("panel-1")), "cleanable in")
	})
	h.clickAt("panel-1", "confirm_clean", confirmRev)
	h.clickAt("panel-1", "confirm_clean", h.rev("panel-1"))

	// No password prompt opened for them, and a second scan after the
	// events proves the plugin handled them.
	if findNode(h.root("panel-1"), "password") != nil {
		t.Fatal("a stale confirm_clean opened the password prompt")
	}
	h.click("panel-1", "scan_quick", v1.EventActivate, "")
	h.password("panel-1", "hunter2")
	h.wait("a second scan", func() bool {
		n := 0
		for _, req := range stub.requests() {
			if req.Cmd == "scan" {
				n++
			}
		}
		return n == 2
	})
	for _, req := range stub.requests() {
		if req.Cmd == "clean" {
			t.Fatalf("a confirm_clean outside the confirm screen sent %+v", req)
		}
	}
}

// gateScannedAt stamps the stub's scan; the clean must send it back.
const gateScannedAt = "2026-10-02T12:30:54.078412521Z"

// moonbitReq is the daemon's line protocol header, as the plugin sends it.
type moonbitReq struct {
	Cmd        string   `json:"cmd"`
	Mode       string   `json:"mode"`
	Force      bool     `json:"force"`
	Categories []string `json:"categories"`
	ScannedAt  string   `json:"scanned_at"`
	Op         string   `json:"op"`
	Target     string   `json:"target"`
	Action     string   `json:"action"`
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
	// burst, when set, makes a scan stream that many categories back to
	// back with no hold, the way a deep scan clears absent paths.
	burst int
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
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
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
		if s.burst > 0 {
			var evs []map[string]any
			for i := 1; i <= s.burst; i++ {
				name := fmt.Sprintf("Cache %02d", i)
				evs = append(evs,
					map[string]any{"t": "category", "name": name, "i": i, "total": s.burst},
					map[string]any{"t": "scan", "files": i, "bytes": i * 1000, "dir": "/var/cache/" + name},
					map[string]any{"t": "category_done", "name": name, "files": i, "bytes": i * 1000, "duration_ms": 1},
				)
			}
			writeEvents(conn, append(evs, map[string]any{"t": "done", "categories": s.burst}))
			return
		}
		// Like the daemon, EOF on the request side cancels the scan, and the
		// stream still ends with its terminal event.
		eof := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, br)
			close(eof)
		}()
		if !writeEvents(conn, []map[string]any{
			{"t": "category", "name": "Pacman Cache", "i": 1, "total": 2},
			{"t": "scan", "files": 250, "bytes": 400000000, "dir": "/var/cache/pacman/pkg"},
			{"t": "category_done", "name": "Pacman Cache", "files": 498, "bytes": 782317440, "duration_ms": 1200},
		}) {
			return
		}
		select {
		case <-s.scanHold:
		case <-eof:
			writeEvents(conn, []map[string]any{{"t": "cancelled"}})
			return
		}
		writeEvents(conn, []map[string]any{
			{"t": "category", "name": "Journal Logs", "i": 2, "total": 2},
			{"t": "scan", "files": 510, "bytes": 911166458, "dir": "/var/log/journal"},
			{"t": "category_done", "name": "Journal Logs", "files": 12, "bytes": 128849018, "duration_ms": 80},
			{"t": "done", "files": 510, "bytes": 911166458, "categories": 2, "duration_ms": 1280, "scanned_at": gateScannedAt},
		})
	case "docker":
		writeEvents(conn, []map[string]any{{"t": "docker_done", "op": req.Op, "reclaimed": "1.2GB"}})
	case "schedule":
		writeEvents(conn, []map[string]any{{"t": "schedule_done", "target": req.Target, "action": req.Action}})
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
	revs   map[string]uint64
	frames int // snapshots received, all views
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

	// The plugin runs `sudo -S -k -p PROMPT moonbit panel`. A fake sudo on
	// PATH prompts like sudo -S, takes "hunter2", and execs this test binary
	// as a fake `moonbit panel` bridged onto the stub daemon. A fake
	// systemctl reports no schedule, and the last scan comes from a fixture.
	fakes := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeScript(t, filepath.Join(fakes, "sudo"), `#!/bin/sh
prompt=""
while [ $# -gt 0 ]; do case "$1" in -p) prompt="$2"; shift 2;; -S|-k) shift;; *) break;; esac; done
printf '%s' "$prompt" >&2
read -r pw
if [ "$pw" != "hunter2" ]; then echo "Sorry, try again." >&2; printf '%s' "$prompt" >&2; read -r pw; exit 1; fi
SYSC_FAKE_MOONBIT_PANEL='`+sockPath+`' exec '`+self+`'
`)
	writeScript(t, filepath.Join(fakes, "systemctl"), `#!/bin/sh
case "$1" in is-enabled) echo disabled;; is-active) echo inactive;; esac
`)
	cache := filepath.Join(fakes, "scan_results.json")
	if err := os.WriteFile(cache, []byte(gateLastScan), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "PATH="+fakes+":"+os.Getenv("PATH"), "SYSC_MOONBIT_CACHE="+cache)
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
		roots: map[string]*v1.Node{}, revs: map[string]uint64{}, slots: map[string]viewSlot{},
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
				h.revs[m.ViewID] = m.Revision
				h.frames++
				h.mu.Unlock()
				if ok {
					checkFits(h.t, slot, m.Root)
				}
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 15}},
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

// click sends an event against the revision the host last rendered, as the
// shell does.
func (h *moonbitHost) click(view, node string, ev v1.EventKind, button v1.PointerButton) {
	h.t.Helper()
	h.send1(&v1.InputEvent{ViewID: view, Revision: h.rev(view), Node: node, Event: ev, Button: button, Output: "DP-1"})
}

// clickAt activates a node against an explicit revision, to stand in for an
// event that left an older tree.
func (h *moonbitHost) clickAt(view, node string, rev uint64) {
	h.t.Helper()
	h.send1(&v1.InputEvent{ViewID: view, Revision: rev, Node: node, Event: v1.EventActivate, Output: "DP-1"})
}

func (h *moonbitHost) send1(m v1.Message) {
	h.t.Helper()
	if err := h.send(m); err != nil {
		h.t.Fatal(err)
	}
}

func (h *moonbitHost) rev(id string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revs[id]
}

func (h *moonbitHost) frameCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.frames
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

// gateLastScan is the user's last scan as `sudo moonbit` leaves it.
const gateLastScan = `{"scan_results":{"files":[
	{"path":"/var/cache/pacman/pkg/a.pkg.tar.zst","size":782317440,"category_name":"Pacman Cache"},
	{"path":"/var/log/journal/x.journal~","size":128849018,"category_name":"Journal Logs"}]},
	"total_size":911166458,"total_files":510,"scanned_at":"2026-09-30T10:38:49+10:00"}`

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runGateFakeMoonbitPanel stands in for `moonbit panel` once the fake sudo
// accepts the password: it says ready, then bridges stdin and stdout onto the
// stub daemon, half-closing on stdin EOF the way moonbit cancels.
func runGateFakeMoonbitPanel(sock string) int {
	if _, err := os.Stdout.WriteString(`{"t":"ready"}` + "\n"); err != nil {
		return 1
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return 1
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(os.Stdout, conn)
		close(done)
	}()
	_, _ = io.Copy(conn, os.Stdin)
	_ = conn.(*net.UnixConn).CloseWrite()
	<-done
	return 0
}

// password answers the panel's prompt as the user would.
func (h *moonbitHost) password(view, pw string) {
	h.t.Helper()
	h.wait("the password prompt", func() bool {
		return findNode(h.root(view), "password") != nil
	})
	h.send1(&v1.InputEvent{ViewID: view, Revision: h.rev(view), Node: "password", Event: v1.EventSubmit, Text: pw, Output: "DP-1"})
}
