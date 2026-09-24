package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func runGateFakeDocker() int {
	args := os.Args[1:]
	logPath := os.Getenv("SYSC_FAKE_DOCKER_LOG")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	raw, err := json.Marshal(args)
	if err == nil {
		_, err = log.Write(append(raw, '\n'))
	}
	_ = log.Close()
	if err != nil {
		return 2
	}

	switch {
	case len(args) > 0 && args[0] == "ps":
		_, _ = os.Stdout.WriteString("{\"ID\":\"container-running\",\"Names\":\"api\",\"Image\":\"alpine:3\",\"State\":\"running\",\"Status\":\"Up 1 minute\"}\n")
		_, _ = os.Stdout.WriteString("{\"ID\":\"container-stopped\",\"Names\":\"old-worker\",\"Image\":\"busybox:latest\",\"State\":\"exited\",\"Status\":\"Exited (0)\"}\n")
	case len(args) > 0 && args[0] == "images":
		_, _ = os.Stdout.WriteString("{\"Repository\":\"alpine\",\"Tag\":\"3\",\"ID\":\"sha256:image1\",\"CreatedSince\":\"2 weeks ago\",\"Size\":\"8MB\",\"Containers\":0}\n")
	case len(args) > 1 && args[0] == "volume" && args[1] == "ls":
		_, _ = os.Stdout.WriteString("{\"Name\":\"db-data\",\"Driver\":\"local\",\"Scope\":\"local\",\"Mountpoint\":\"/var/lib/docker/volumes/db-data\"}\n")
	case len(args) > 1 && args[0] == "network" && args[1] == "ls":
		_, _ = os.Stdout.WriteString("{\"Name\":\"bridge\",\"ID\":\"network-bridge\",\"Driver\":\"bridge\",\"Scope\":\"local\"}\n")
		_, _ = os.Stdout.WriteString("{\"Name\":\"custom\",\"ID\":\"network-custom\",\"Driver\":\"bridge\",\"Scope\":\"local\"}\n")
	case len(args) > 1 && args[0] == "image" && args[1] == "inspect":
		_, _ = os.Stdout.WriteString("{\"8080/tcp\":{},\"53/udp\":{}}\n")
	}

	if len(args) > 0 && args[0] == "rm" {
		entered := os.Getenv("SYSC_FAKE_DOCKER_RM_ENTERED")
		release := os.Getenv("SYSC_FAKE_DOCKER_RM_RELEASE")
		if entered != "" && release != "" {
			if err := os.WriteFile(entered, []byte("entered"), 0o600); err != nil {
				return 2
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(release); err == nil {
					return 0
				}
				time.Sleep(10 * time.Millisecond)
			}
			return 3
		}
	}
	return 0
}

type miniDockerCall struct {
	kind   v1.CallKind
	params v1.PanelParams
}

type miniDockerSnapshot struct {
	viewID string
	rev    uint64
	root   *v1.Node
}

type miniDockerGateHost struct {
	t          *testing.T
	enc        *v1.Encoder
	mu         sync.Mutex
	roots      map[string]*v1.Node
	revs       map[string]uint64
	slots      map[string]viewSlot
	outputs    map[string]string
	generation map[string]uint32
	wake       chan struct{}
	calls      chan miniDockerCall
	snapshots  chan miniDockerSnapshot
	stdin      io.WriteCloser
	cmd        *exec.Cmd
	done       chan error
	stopOnce   sync.Once
	stopErr    error
	stderr     bytes.Buffer
}

func startMiniDockerGate(t *testing.T, logPath, rmEntered, rmRelease string) *miniDockerGateHost {
	t.Helper()
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.mini-docker")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-mini-docker")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/mini-docker/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-mini-docker")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mini-docker: %v\n%s", err, out)
	}
	fakeBin := filepath.Join(t.TempDir(), "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(fakeBin, "docker")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin)
	cmd.Env = gateEnv(os.Environ(), map[string]string{
		"PATH":                        fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"SYSC_FAKE_DOCKER":            "1",
		"SYSC_FAKE_DOCKER_LOG":        logPath,
		"SYSC_FAKE_DOCKER_RM_ENTERED": rmEntered,
		"SYSC_FAKE_DOCKER_RM_RELEASE": rmRelease,
	})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	h := &miniDockerGateHost{
		t: t, enc: v1.NewEncoder(stdin), stdin: stdin, cmd: cmd,
		roots: map[string]*v1.Node{}, revs: map[string]uint64{}, slots: map[string]viewSlot{},
		outputs: map[string]string{}, generation: map[string]uint32{},
		wake: make(chan struct{}, 1), calls: make(chan miniDockerCall, 8),
		snapshots: make(chan miniDockerSnapshot, 256), done: make(chan error, 1),
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
				var params v1.PanelParams
				_ = json.Unmarshal(m.Params, &params)
				select {
				case h.calls <- miniDockerCall{kind: m.Call, params: params}:
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
				snapshot := miniDockerSnapshot{viewID: m.ViewID, rev: m.Revision, root: m.Root}
				select {
				case h.snapshots <- snapshot:
				default:
				}
				select {
				case h.wake <- struct{}{}:
				default:
				}
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 4}},
		Plugin:       v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.4.0"},
		Capabilities: []string{"panels", "settings"}, Limits: v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("mini-docker stderr:\n%s", h.stderr.String())
		}
		_ = h.stop()
		_ = h.stdin.Close()
	})
	return h
}

func gateEnv(env []string, overrides map[string]string) []string {
	result := make([]string, 0, len(env)+len(overrides))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func (h *miniDockerGateHost) send(message v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(message)
}

func (h *miniDockerGateHost) open(id string, kind v1.ViewKind, entry string, slot viewSlot, instance, output string, generation uint32) {
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

func (h *miniDockerGateHost) click(viewID, node string) {
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

func (h *miniDockerGateHost) inputText(viewID, node, text string) {
	h.t.Helper()
	h.mu.Lock()
	revision, output, generation := h.revs[viewID], h.outputs[viewID], h.generation[viewID]
	h.mu.Unlock()
	if err := h.send(&v1.InputEvent{
		Type: v1.TypeInputEvent, ViewID: viewID, Revision: revision,
		Node: node, Event: v1.EventChange, Text: text, Output: output, Generation: generation,
	}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *miniDockerGateHost) setting(values map[string]any) {
	h.t.Helper()
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: values}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *miniDockerGateHost) waitView(viewID string, match func(*v1.Node) bool) *v1.Node {
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

func (h *miniDockerGateHost) waitSnapshot(viewID string, match func(miniDockerSnapshot) bool) miniDockerSnapshot {
	h.t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case snapshot := <-h.snapshots:
			if snapshot.viewID == viewID && match(snapshot) {
				return snapshot
			}
		case <-timer.C:
			h.t.Fatalf("timed out waiting for view %s snapshot", viewID)
		}
	}
}

func (h *miniDockerGateHost) waitCall(kind v1.CallKind) miniDockerCall {
	h.t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case call := <-h.calls:
			if call.kind == kind {
				return call
			}
		case <-timer.C:
			h.t.Fatalf("timed out waiting for host call %s", kind)
		}
	}
}

func (h *miniDockerGateHost) stop() error {
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

func TestPluginMiniDockerGate(t *testing.T) {
	temp := t.TempDir()
	logPath := filepath.Join(temp, "docker-argv.jsonl")
	rmEntered := filepath.Join(temp, "rm.entered")
	rmRelease := filepath.Join(temp, "rm.release")
	h := startMiniDockerGate(t, logPath, rmEntered, rmRelease)
	t.Cleanup(func() { _ = os.WriteFile(rmRelease, []byte("release"), 0o600) })

	h.open("bar-1", v1.ViewBar, "bar", viewSlot{v1.ViewBar, lint.BarWidth, lint.BarHeight}, "placement-1", "DP-1", 7)
	h.waitView("bar-1", func(root *v1.Node) bool { return findID(root, "open") != nil })
	h.waitView("bar-1", func(root *v1.Node) bool { return nodeText(root, "open") == "docker 1" })
	h.open("tip-1", v1.ViewTooltip, "bar", viewSlot{v1.ViewTooltip, lint.TooltipWidth, lint.TooltipHeight}, "placement-1", "DP-1", 7)
	h.waitView("tip-1", func(root *v1.Node) bool { return strings.Contains(treeText(root), "1 container running") })
	h.click("bar-1", "open")
	call := h.waitCall(v1.CallPanelOpen)
	if call.params.Entry != "panel" || call.params.Instance != "placement-1" || call.params.Output != "DP-1" || call.params.Generation != 7 {
		t.Fatalf("panel.open params = %+v", call.params)
	}

	panelW, panelH := miniDockerPanelSize(t)
	h.open("panel-1", v1.ViewPanel, "panel", viewSlot{v1.ViewPanel, panelW, panelH}, "placement-1", "DP-1", 7)
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "select:container-stopped") != nil })
	h.setting(map[string]any{"default_network": "custom", "status_mode": "hidden"})
	h.waitView("bar-1", func(root *v1.Node) bool { return nodeText(root, "open") == "docker" })

	h.click("panel-1", "tab:images")
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "select:alpine:3") != nil })
	h.click("panel-1", "select:alpine:3")
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "run:alpine:3") != nil })
	h.click("panel-1", "run:alpine:3")
	form := h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "run-form") != nil })
	if nodeText(form, "form:network") != "Network: custom" || nodeText(form, "port") != "8080" {
		t.Fatalf("run form defaults: network=%q port=%q", nodeText(form, "form:network"), nodeText(form, "port"))
	}
	h.inputText("panel-1", "name", "gate-web")
	h.waitView("panel-1", func(root *v1.Node) bool { return nodeText(root, "name") == "gate-web" })
	port := availableHostPort(t)
	h.inputText("panel-1", "port", port)
	h.waitView("panel-1", func(root *v1.Node) bool { return nodeText(root, "port") == port })
	h.inputText("panel-1", "env", "A=one\nB=two=three")
	h.waitView("panel-1", func(root *v1.Node) bool { return nodeText(root, "env") == "A=one\nB=two=three" })
	h.click("panel-1", "form:publish")
	h.waitView("panel-1", func(root *v1.Node) bool { return nodeText(root, "form:publish") == "Publish port: on" })
	h.click("panel-1", "form:network")
	h.waitView("panel-1", func(root *v1.Node) bool { return nodeText(root, "form:network") == "Network: bridge" })
	h.click("panel-1", "form:network")
	h.waitView("panel-1", func(root *v1.Node) bool { return nodeText(root, "form:network") == "Network: custom" })
	h.click("panel-1", "run-submit")
	h.waitView("panel-1", func(root *v1.Node) bool {
		return findID(root, "run-form") == nil && strings.Contains(treeText(root), "Docker images")
	})
	wantRun := []string{"run", "-d", "--name", "gate-web", "-e", "A=one", "-e", "B=two=three", "-p", port + ":" + port, "--network", "custom", "alpine:3"}
	waitDockerCall(t, logPath, wantRun)

	h.click("panel-1", "tab:volumes")
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "select:db-data") != nil })
	h.click("panel-1", "tab:networks")
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "select:network-custom") != nil })
	h.click("panel-1", "tab:containers")
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "select:container-stopped") != nil })
	h.click("panel-1", "select:container-stopped")
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "remove:container-stopped") != nil })
	h.click("panel-1", "remove:container-stopped")
	h.waitView("panel-1", func(root *v1.Node) bool { return findID(root, "confirm") != nil })
	h.click("panel-1", "confirm")
	waitFile(t, rmEntered, "entered")
	busy := h.waitView("panel-1", func(root *v1.Node) bool {
		remove := findID(root, "remove:container-stopped")
		return remove != nil && remove.Disabled
	})
	if tab := findID(busy, "tab:images"); tab == nil || tab.Disabled {
		t.Fatalf("unrelated tab disabled during remove: %+v", tab)
	}
	waitDockerCall(t, logPath, []string{"rm", "container-stopped"})
	if err := os.WriteFile(rmRelease, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.waitView("panel-1", func(root *v1.Node) bool {
		remove := findID(root, "remove:container-stopped")
		return findID(root, "confirm") == nil && remove != nil && !remove.Disabled
	})

	before := h.currentRevision("panel-1")
	if err := h.send(&v1.ViewResync{Type: v1.TypeViewResync, ViewID: "panel-1"}); err != nil {
		t.Fatal(err)
	}
	resynced := h.waitSnapshot("panel-1", func(snapshot miniDockerSnapshot) bool { return snapshot.rev == 1 })
	if before <= 1 || findID(resynced.root, "refresh") == nil {
		t.Fatalf("resync failed to replace revision %d with a full snapshot", before)
	}

	for _, args := range [][]string{
		{"ps", "-a", "--format", "{{json .}}"},
		{"images", "--format", "{{json .}}"},
		{"volume", "ls", "--format", "{{json .}}"},
		{"network", "ls", "--format", "{{json .}}"},
		{"image", "inspect", "--format", "{{json .Config.ExposedPorts}}", "alpine:3"},
		wantRun,
		{"rm", "container-stopped"},
	} {
		if !hasDockerCall(logPath, args) {
			t.Errorf("Docker argv missing %q; calls:\n%s", args, dockerLog(t, logPath))
		}
	}
	if err := h.stop(); err != nil {
		t.Fatalf("plugin shutdown: %v", err)
	}
}

func miniDockerPanelSize(t *testing.T) (int, int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins/mini-docker/manifest.json"))
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

func (h *miniDockerGateHost) currentRevision(viewID string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revs[viewID]
}

func availableHostPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(strconv.Itoa(port))
}

func dockerLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hasDockerCall(path string, want []string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		var args []string
		if json.Unmarshal([]byte(line), &args) == nil && slices.Equal(args, want) {
			return true
		}
	}
	return false
}

func waitDockerCall(t *testing.T, path string, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hasDockerCall(path, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Docker argv %q not observed; calls:\n%s", want, dockerLog(t, path))
}
