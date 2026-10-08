package integration

import (
	"bytes"
	"encoding/json"
	"io"
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

// runGateFakeUpdates backs the checkupdates, paru, flatpak and
// xdg-terminal-exec symlinks the updates gate installs on PATH. The test
// binary re-executes itself with SYSC_FAKE_UPDATES=1 and dispatches on the
// name it was invoked as. Every invocation is logged as its argv.
func runGateFakeUpdates() int {
	logPath := os.Getenv("SYSC_FAKE_UPDATES_LOG")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	raw, err := json.Marshal(os.Args[1:])
	if err == nil {
		_, err = log.Write(append(raw, '\n'))
	}
	_ = log.Close()
	if err != nil {
		return 2
	}

	switch filepath.Base(os.Args[0]) {
	case "checkupdates":
		_, _ = os.Stdout.WriteString("linux 6.17.1.arch1-1 -> 6.17.2.arch1-1\n")
		_, _ = os.Stdout.WriteString("firefox 143.0.1-1 -> 143.0.2-1\n")
	case "paru":
		for _, arg := range os.Args[1:] {
			if arg == "-Qua" {
				_, _ = os.Stdout.WriteString("paru-bin 2.0.4-1 -> 2.0.5-1 [ignored]\n")
			}
		}
	case "flatpak":
		_, _ = os.Stdout.WriteString("org.mozilla.firefox 143.0.2\n")
	}
	return 0
}

type updatesCall struct {
	kind   v1.CallKind
	params json.RawMessage
}

type updatesGateHost struct {
	t          *testing.T
	enc        *v1.Encoder
	mu         sync.Mutex
	roots      map[string]*v1.Node
	revs       map[string]uint64
	slots      map[string]viewSlot
	outputs    map[string]string
	generation map[string]uint32
	wake       chan struct{}
	calls      chan updatesCall
	stdin      io.WriteCloser
	cmd        *exec.Cmd
	done       chan error
	stopOnce   sync.Once
	stopErr    error
	stderr     bytes.Buffer
}

func startUpdatesGate(t *testing.T, logPath string, tools ...string) *updatesGateHost {
	t.Helper()
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.updates")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-updates")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/updates/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-updates")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build updates: %v\n%s", err, out)
	}
	fakeBin := filepath.Join(t.TempDir(), "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if err := os.Symlink(executable, filepath.Join(fakeBin, tool)); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(bin)
	// PATH holds only the fake tools: a real checkupdates or paru from the
	// system PATH would leak real pending updates into the fake host.
	cmd.Env = gateEnv(os.Environ(), map[string]string{
		"PATH":                  fakeBin,
		"SYSC_FAKE_UPDATES":     "1",
		"SYSC_FAKE_UPDATES_LOG": logPath,
	})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	h := &updatesGateHost{
		t: t, enc: v1.NewEncoder(stdin), stdin: stdin, cmd: cmd,
		roots: map[string]*v1.Node{}, revs: map[string]uint64{}, slots: map[string]viewSlot{},
		outputs: map[string]string{}, generation: map[string]uint32{},
		wake: make(chan struct{}, 1), calls: make(chan updatesCall, 8),
		done: make(chan error, 1),
	}
	cmd.Stderr = &h.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	decoder := v1.NewDecoder(stdout, v1.ToHost)
	go func() { h.done <- cmd.Wait() }()
	go func() {
		for {
			msg, err := decoder.Decode()
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *v1.HostCall:
				select {
				case h.calls <- updatesCall{kind: m.Call, params: m.Params}:
				default:
				}
				_ = h.send(&v1.HostReply{ID: m.ID, OK: true})
			case *v1.ViewSnapshot:
				h.mu.Lock()
				slot, monitored := h.slots[m.ViewID]
				h.roots[m.ViewID] = m.Root
				h.revs[m.ViewID] = m.Revision
				h.mu.Unlock()
				if monitored {
					checkFits(h.t, slot, m.Root)
				}
				select {
				case h.wake <- struct{}{}:
				default:
				}
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 13}},
		Plugin:       v1.Identity{ID: "org.sysc.updates", Name: "System Updates", Version: "0.1.0"},
		Capabilities: []string{"notifications", "panels", "settings", "state", "open-url"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.stop()
		_ = h.stdin.Close()
		if t.Failed() {
			t.Logf("updates stderr:\n%s", h.stderr.String())
		}
	})
	return h
}

func (h *updatesGateHost) send(message v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(message)
}

func (h *updatesGateHost) open(id string, kind v1.ViewKind, entry string, slot viewSlot, instance, output string, generation uint32) {
	h.t.Helper()
	h.mu.Lock()
	h.slots = recordSlot(h.slots, id, slot)
	h.outputs[id] = output
	h.generation[id] = generation
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{
		ViewID: id, View: kind, Entry: entry, Instance: instance, Output: output,
		Generation: generation, Width: slot.w, Height: slot.h,
	}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *updatesGateHost) click(viewID, node string) {
	h.t.Helper()
	h.mu.Lock()
	revision, output, generation := h.revs[viewID], h.outputs[viewID], h.generation[viewID]
	h.mu.Unlock()
	if err := h.send(&v1.InputEvent{
		Type: v1.TypeInputEvent, ViewID: viewID, Revision: revision,
		Node: node, Event: v1.EventActivate, Output: output, Generation: generation,
	}); err != nil {
		h.t.Fatal(err)
	}
}

// clickUntilCall re-sends an activate click until the plugin emits the expected
// host call, because the plugin drops events carrying a superseded revision.
func (h *updatesGateHost) clickUntilCall(viewID, node string, kind v1.CallKind) updatesCall {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		h.click(viewID, node)
		select {
		case call := <-h.calls:
			if call.kind == kind {
				return call
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	h.t.Fatalf("click %q never produced host call %s", node, kind)
	return updatesCall{}
}

// clickUntil retries a click until the panel tree matches.
func (h *updatesGateHost) clickUntil(viewID, node string, matches func(*v1.Node) bool) *v1.Node {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root, rev, output, generation := h.roots[viewID], h.revs[viewID], h.outputs[viewID], h.generation[viewID]
		h.mu.Unlock()
		if root != nil && matches(root) {
			return root
		}
		if err := h.send(&v1.InputEvent{
			Type: v1.TypeInputEvent, ViewID: viewID, Revision: rev,
			Node: node, Event: v1.EventActivate, Output: output, Generation: generation,
		}); err != nil {
			h.t.Fatal(err)
		}
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			h.mu.Lock()
			root, current := h.roots[viewID], h.revs[viewID]
			h.mu.Unlock()
			if root != nil && matches(root) {
				return root
			}
			if current > rev {
				break
			}
		}
	}
	h.mu.Lock()
	root := h.roots[viewID]
	h.mu.Unlock()
	h.t.Fatalf("input %q never produced the expected panel tree\n%s", node, dumpTree(root))
	return nil
}

func (h *updatesGateHost) setting(values map[string]any) {
	h.t.Helper()
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: values}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *updatesGateHost) waitView(viewID string, match func(*v1.Node) bool) *v1.Node {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root := h.roots[viewID]
		h.mu.Unlock()
		if root != nil && match(root) {
			return root
		}
		select {
		case <-h.wake:
		case <-time.After(30 * time.Millisecond):
		}
	}
	h.mu.Lock()
	root := h.roots[viewID]
	h.mu.Unlock()
	h.t.Fatalf("view %s did not reach expected state\n%s", viewID, dumpTree(root))
	return nil
}

func (h *updatesGateHost) stop() error {
	h.stopOnce.Do(func() {
		if err := h.send(&v1.HostShutdown{Type: v1.TypeHostShutdown}); err != nil {
			h.stopErr = err
		}
		select {
		case err := <-h.done:
			h.stopErr = err
		case <-time.After(3 * time.Second):
			_ = h.cmd.Process.Kill()
			<-h.done
			h.stopErr = exec.ErrNotFound
		}
	})
	return h.stopErr
}

func updatesPanelSize(t *testing.T) (int, int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins/updates/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Panels []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Panels) != 1 {
		t.Fatalf("manifest has %d panels, want one", len(manifest.Panels))
	}
	return manifest.Panels[0].Width, manifest.Panels[0].Height
}

func updatesLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func waitUpdatesArgv(t *testing.T, path string, match func([]string) bool) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				var args []string
				if json.Unmarshal([]byte(line), &args) == nil && match(args) {
					return args
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected argv not observed; calls:\n%s", updatesLog(t, path))
	return nil
}

func TestPluginUpdatesGate(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "updates-argv.jsonl")
	h := startUpdatesGate(t, logPath, "checkupdates", "paru", "flatpak", "xdg-terminal-exec")

	h.setting(map[string]any{"hide_when_zero": false})
	h.open("bar-1", v1.ViewBar, "bar", viewSlot{v1.ViewBar, lint.BarWidth, lint.BarHeight}, "placement-1", "DP-1", 7)
	h.waitView("bar-1", func(root *v1.Node) bool {
		node := findID(root, "open")
		return node != nil && node.Icon == "download"
	})
	call := h.clickUntilCall("bar-1", "open", v1.CallPanelOpen)
	var panel v1.PanelParams
	if err := json.Unmarshal(call.params, &panel); err != nil {
		t.Fatal(err)
	}
	if panel.Entry != "panel" || panel.Instance != "placement-1" || panel.Output != "DP-1" || panel.Generation != 7 {
		t.Fatalf("panel.open params = %+v", panel)
	}

	panelW, panelH := updatesPanelSize(t)
	h.open("panel-1", v1.ViewPanel, "panel", viewSlot{v1.ViewPanel, panelW, panelH}, "placement-1", "DP-1", 7)
	tree := h.clickUntil("panel-1", "updates-refresh", func(root *v1.Node) bool {
		return findID(root, "updates-row-repo-linux") != nil
	})
	if !strings.Contains(treeText(tree), "4 updates") {
		t.Fatalf("panel header missing the total:\n%s", dumpTree(tree))
	}
	for _, id := range []string{
		"updates-row-repo-firefox",
		"updates-row-aur-paru-bin",
		"updates-row-flatpak-org.mozilla.firefox",
	} {
		if findID(tree, id) == nil {
			t.Fatalf("panel missing %s:\n%s", id, dumpTree(tree))
		}
	}

	news := h.clickUntilCall("panel-1", "updates-news", v1.CallOpenURL)
	var opened v1.OpenURLParams
	if err := json.Unmarshal(news.params, &opened); err != nil {
		t.Fatal(err)
	}
	if opened.URL != "https://archlinux.org/news/" {
		t.Fatalf("news opened %q", opened.URL)
	}

	h.click("panel-1", "updates-run")
	waitUpdatesArgv(t, logPath, func(args []string) bool {
		return len(args) == 3 && args[0] == "sh" && args[1] == "-c" && strings.HasPrefix(args[2], "paru -Syu")
	})

	h.setting(map[string]any{"aur_helper": "off"})
	h.waitView("panel-1", func(root *v1.Node) bool {
		return findID(root, "updates-row-aur-paru-bin") == nil && findID(root, "updates-row-repo-linux") != nil
	})
	if err := h.stop(); err != nil {
		t.Fatalf("plugin shutdown: %v", err)
	}
}

func TestPluginUpdatesGateMissingCheckupdates(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "updates-argv.jsonl")
	h := startUpdatesGate(t, logPath, "paru", "flatpak", "xdg-terminal-exec")

	h.setting(map[string]any{"hide_when_zero": false})
	h.open("bar-1", v1.ViewBar, "bar", viewSlot{v1.ViewBar, lint.BarWidth, lint.BarHeight}, "placement-1", "DP-1", 7)
	h.waitView("bar-1", func(root *v1.Node) bool {
		node := findID(root, "open")
		return node != nil && node.Icon == "download"
	})
	h.clickUntilCall("bar-1", "open", v1.CallPanelOpen)

	panelW, panelH := updatesPanelSize(t)
	h.open("panel-1", v1.ViewPanel, "panel", viewSlot{v1.ViewPanel, panelW, panelH}, "placement-1", "DP-1", 7)
	h.clickUntil("panel-1", "updates-refresh", func(root *v1.Node) bool {
		return strings.Contains(treeText(root), "Install pacman-contrib")
	})
	h.waitView("bar-1", func(root *v1.Node) bool {
		return strings.Contains(treeText(root), "!")
	})
	if err := h.stop(); err != nil {
		t.Fatalf("plugin shutdown: %v", err)
	}
}
