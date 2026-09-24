package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestPluginWallpaperDepthGate(t *testing.T) {
	h := startWallpaperDepth(t)
	if hello := h.pluginHello(); hello.Plugin != (v1.Identity{ID: "org.sysc.wallpaper-depth", Name: "Wallpaper Depth", Version: "1.0.0"}) {
		t.Fatalf("plugin handshake identity = %+v", hello.Plugin)
	}
	h.waitCallCount(v1.CallWallpaperSnapshot, 1)
	h.waitFakeOperation("status")

	h.open("bar-1", v1.ViewBar, lint.BarWidth, lint.BarHeight)
	h.waitView("bar-1", func(root *v1.Node) bool { return findID(root, "open") != nil })
	h.open("tooltip-1", v1.ViewTooltip, lint.TooltipWidth, lint.TooltipHeight)
	h.waitView("tooltip-1", func(root *v1.Node) bool { return strings.Contains(treeText(root), "Wallpaper Depth") })
	if err := h.send(&v1.InputEvent{ViewID: "bar-1", Node: "open", Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		t.Fatal(err)
	}
	panelCalls := h.waitPanelOpen(1)
	var panel v1.PanelParams
	if err := json.Unmarshal(panelCalls[0].params, &panel); err != nil {
		t.Fatal(err)
	}
	if panel.Entry != "panel" || panel.Output != "DP-1" {
		t.Fatalf("panel.open params = %+v", panel)
	}
	h.open("panel-1", v1.ViewPanel, 500, 560)
	h.waitView("panel-1", func(root *v1.Node) bool {
		return strings.Contains(treeText(root), "DP-1") && strings.Contains(treeText(root), "Ready")
	})
	initialMasks := h.waitMasks(1)
	var firstMask v1.WallpaperMaskSetParams
	if err := json.Unmarshal(initialMasks[0].params, &firstMask); err != nil {
		t.Fatal(err)
	}
	if firstMask.Output != "DP-1" || firstMask.WallpaperPath != "/wall-a.jpg" || firstMask.MaskPath != h.maskPath("/wall-a.jpg") {
		t.Fatalf("automatic mask registration = %+v", firstMask)
	}

	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{
		"auto_generate": false, "threshold": float64(40), "feather": float64(12),
	}}); err != nil {
		t.Fatal(err)
	}
	h.waitView("panel-1", func(root *v1.Node) bool {
		text := treeText(root)
		return strings.Contains(text, "Automatic off") && strings.Contains(text, "Threshold 40") && strings.Contains(text, "Feather 12")
	})
	clearedMasks := h.waitMasks(2)
	var cleared v1.WallpaperMaskSetParams
	if err := json.Unmarshal(clearedMasks[1].params, &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Output != "DP-1" || cleared.WallpaperPath != "/wall-a.jpg" || cleared.MaskPath != "" {
		t.Fatalf("parameter-change mask clear = %+v", cleared)
	}

	beforeStale := len(h.maskCalls())
	if err := os.WriteFile(filepath.Join(h.gateDir, "block-generate"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.click("panel-1", "generate-DP-1"); err != nil {
		t.Fatal(err)
	}
	h.waitFile("generate-started")
	beforeChange := h.view("panel-1").revision
	h.setWallpaper("/wall-b.jpg")
	h.waitCallCount(v1.CallWallpaperSnapshot, 2)
	h.waitView("panel-1", func(root *v1.Node) bool {
		return h.view("panel-1").revision > beforeChange && strings.Contains(treeText(root), "DP-1") && strings.Contains(treeText(root), "Waiting")
	})
	if err := os.WriteFile(filepath.Join(h.gateDir, "generate-release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.waitFile("generate-complete")
	// The next serialized poll is a completion barrier for the event loop: it
	// follows the helper result and lets the host inspect the resulting calls.
	pollsAtRelease := len(h.callsOf(v1.CallWallpaperSnapshot))
	h.waitCallCount(v1.CallWallpaperSnapshot, pollsAtRelease+1)
	if err := os.Remove(filepath.Join(h.gateDir, "block-generate")); err != nil {
		t.Fatal(err)
	}
	for _, call := range h.maskCalls()[beforeStale:] {
		var params v1.WallpaperMaskSetParams
		if err := json.Unmarshal(call.params, &params); err != nil {
			t.Fatal(err)
		}
		if params.WallpaperPath == "/wall-a.jpg" && params.MaskPath != "" {
			t.Fatalf("stale mask was registered after wallpaper changed: %+v", params)
		}
	}
	if got := depthOutputStatus(h.view("panel-1").root, "DP-1"); got != "Waiting" {
		t.Fatalf("output status after stale job completed = %q, want Waiting", got)
	}

	if err := h.click("panel-1", "clear-cache"); err != nil {
		t.Fatal(err)
	}
	h.waitFakeOperation("clear-cache")
	beforeResync := h.view("panel-1").revision
	if beforeResync <= 1 {
		t.Fatalf("panel revision before resync = %d", beforeResync)
	}
	if err := h.send(&v1.ViewResync{ViewID: "panel-1"}); err != nil {
		t.Fatal(err)
	}
	h.waitRevision("panel-1", 1)
	if len(h.callsOf(v1.CallNotify)) != 0 {
		t.Fatalf("unexpected generic completion notifications: %+v", h.callsOf(v1.CallNotify))
	}
	for _, id := range []string{"bar-1", "tooltip-1", "panel-1"} {
		if h.view(id).root == nil {
			t.Errorf("view %s never received a snapshot", id)
		}
	}
	h.stop()
}

type wallpaperDepthCall struct {
	kind   v1.CallKind
	params json.RawMessage
}

type wallpaperDepthView struct {
	root     *v1.Node
	revision uint64
}

type wallpaperDepthHost struct {
	t        *testing.T
	enc      *v1.Encoder
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex
	hello    *v1.PluginHello
	slots    map[string]viewSlot
	views    map[string]wallpaperDepthView
	calls    []wallpaperDepthCall
	snapshot v1.WallpaperSnapshotResult
	wake     chan struct{}
	done     chan error
	once     sync.Once
	gateDir  string
}

func startWallpaperDepth(t *testing.T) *wallpaperDepthHost {
	t.Helper()
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.wallpaper-depth")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-wallpaper-depth")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/wallpaper-depth/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile(filepath.Join(root, "plugins/wallpaper-depth/depth_helper.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "depth_helper.py"), helper, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-wallpaper-depth")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wallpaper-depth plugin: %v\n%s", err, output)
	}

	gateDir := t.TempDir()
	pythonDir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(pythonDir, "python3")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
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
	cmd.Env = append(os.Environ(),
		"PATH="+pythonDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSC_FAKE_WALLPAPER_DEPTH=1",
		"SYSC_DEPTH_GATE_DIR="+gateDir,
		"XDG_STATE_HOME="+filepath.Join(gateDir, "state"),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &wallpaperDepthHost{
		t: t, cmd: cmd, stdin: stdin, enc: v1.NewEncoder(stdin),
		slots: make(map[string]viewSlot), views: make(map[string]wallpaperDepthView),
		snapshot: v1.WallpaperSnapshotResult{Revision: 1, Scale: "fill", Outputs: []v1.WallpaperOutput{{Output: "DP-1", State: v1.WallpaperImage, Path: "/wall-a.jpg"}}},
		wake:     make(chan struct{}, 1), done: make(chan error, 1), gateDir: gateDir,
	}
	dec := v1.NewDecoder(stdout, v1.ToHost)
	go func() {
		for {
			message, err := dec.Decode()
			if err != nil {
				return
			}
			switch message := message.(type) {
			case *v1.PluginHello:
				h.mu.Lock()
				h.hello = message
				h.mu.Unlock()
				h.signal()
			case *v1.HostCall:
				h.handleCall(message)
			case *v1.ViewSnapshot:
				h.recordSnapshot(message)
			}
		}
	}()
	go func() { h.done <- cmd.Wait() }()
	t.Cleanup(func() {
		h.stop()
		if t.Failed() {
			t.Logf("plugin stderr:\n%s", stderr.String())
		}
	})
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 7}},
		Plugin:       v1.Identity{ID: "org.sysc.wallpaper-depth", Name: "Wallpaper Depth", Version: "1.0.0"},
		Capabilities: []string{"panels", "settings", "state", "wallpaper"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	h.waitPluginHello()
	return h
}

func (h *wallpaperDepthHost) handleCall(call *v1.HostCall) {
	h.mu.Lock()
	h.calls = append(h.calls, wallpaperDepthCall{kind: call.Call, params: append(json.RawMessage(nil), call.Params...)})
	snapshot := h.snapshot
	h.mu.Unlock()
	reply := v1.HostReply{ID: call.ID, OK: true}
	switch call.Call {
	case v1.CallWallpaperSnapshot:
		reply.Result, _ = json.Marshal(snapshot)
	case v1.CallWallpaperMaskSet, v1.CallPanelOpen, v1.CallNotify:
	default:
		reply.Result = json.RawMessage(`{}`)
	}
	if err := h.send(&reply); err != nil {
		h.t.Errorf("reply to %s: %v", call.Call, err)
	}
	h.signal()
}

func (h *wallpaperDepthHost) recordSnapshot(message *v1.ViewSnapshot) {
	h.mu.Lock()
	slot, monitored := h.slots[message.ViewID]
	h.views[message.ViewID] = wallpaperDepthView{root: message.Root, revision: message.Revision}
	h.mu.Unlock()
	if monitored {
		if err := v1.Validate(message.Root, slot.view); err != nil {
			h.t.Errorf("%s snapshot validation: %v", slot.view, err)
		}
		checkFits(h.t, slot, message.Root)
	}
	h.signal()
}

func (h *wallpaperDepthHost) send(message v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(message)
}

func (h *wallpaperDepthHost) open(id string, kind v1.ViewKind, width, height int) {
	h.t.Helper()
	h.mu.Lock()
	h.slots = recordSlot(h.slots, id, viewSlot{view: kind, w: width, h: height})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: id, View: kind, Entry: entryForView(kind), Output: "DP-1", Width: width, Height: height}); err != nil {
		h.t.Fatal(err)
	}
}

func entryForView(kind v1.ViewKind) string {
	switch kind {
	case v1.ViewBar:
		return "bar"
	case v1.ViewTooltip:
		return "tooltip"
	default:
		return "panel"
	}
}

func (h *wallpaperDepthHost) click(viewID, node string) error {
	h.mu.Lock()
	revision := h.views[viewID].revision
	h.mu.Unlock()
	return h.send(&v1.InputEvent{ViewID: viewID, Revision: revision, Node: node, Event: v1.EventActivate, Output: "DP-1"})
}

func (h *wallpaperDepthHost) setWallpaper(path string) {
	h.mu.Lock()
	h.snapshot.Revision++
	h.snapshot.Outputs[0].Path = path
	h.mu.Unlock()
	h.signal()
}

func (h *wallpaperDepthHost) waitPluginHello() {
	h.t.Helper()
	h.waitFor("plugin hello", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.hello != nil
	})
}

func (h *wallpaperDepthHost) waitCallCount(kind v1.CallKind, count int) []wallpaperDepthCall {
	h.t.Helper()
	h.waitFor(string(kind), func() bool {
		return len(h.callsOf(kind)) >= count
	})
	return h.callsOf(kind)
}

func (h *wallpaperDepthHost) callsOf(kind v1.CallKind) []wallpaperDepthCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	var calls []wallpaperDepthCall
	for _, call := range h.calls {
		if call.kind == kind {
			calls = append(calls, call)
		}
	}
	return calls
}

func (h *wallpaperDepthHost) waitPanelOpen(count int) []wallpaperDepthCall {
	return h.waitCallCount(v1.CallPanelOpen, count)
}

func (h *wallpaperDepthHost) waitMasks(count int) []wallpaperDepthCall {
	return h.waitCallCount(v1.CallWallpaperMaskSet, count)
}

func (h *wallpaperDepthHost) maskCalls() []wallpaperDepthCall {
	return h.callsOf(v1.CallWallpaperMaskSet)
}

func (h *wallpaperDepthHost) waitView(id string, ok func(*v1.Node) bool) {
	h.t.Helper()
	h.waitFor("view "+id, func() bool {
		view := h.view(id)
		return view.root != nil && ok(view.root)
	})
}

func (h *wallpaperDepthHost) view(id string) wallpaperDepthView {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.views[id]
}

func (h *wallpaperDepthHost) pluginHello() *v1.PluginHello {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hello
}

func (h *wallpaperDepthHost) waitRevision(id string, revision uint64) {
	h.t.Helper()
	h.waitFor("view revision", func() bool { return h.view(id).revision == revision })
}

func (h *wallpaperDepthHost) waitFakeOperation(operation string) {
	h.t.Helper()
	h.waitFor("fake helper "+operation, func() bool {
		raw, err := os.ReadFile(filepath.Join(h.gateDir, "calls"))
		return err == nil && strings.Contains("\n"+string(raw), "\n"+operation+"\n")
	})
}

func (h *wallpaperDepthHost) waitFile(name string) {
	h.t.Helper()
	h.waitFor("file "+name, func() bool {
		_, err := os.Stat(filepath.Join(h.gateDir, name))
		return err == nil
	})
}

func (h *wallpaperDepthHost) waitFor(name string, predicate func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		select {
		case <-h.wake:
		case <-time.After(40 * time.Millisecond):
		}
	}
	h.t.Fatalf("timed out waiting for %s", name)
}

func (h *wallpaperDepthHost) signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *wallpaperDepthHost) maskPath(wallpaper string) string {
	return filepath.Join(h.gateDir, "mask-"+filepath.Base(wallpaper)+".png")
}

func depthOutputStatus(root *v1.Node, output string) string {
	if root == nil {
		return ""
	}
	if root.Kind == v1.KindRow && len(root.Children) >= 2 && root.Children[0].Text == output {
		return root.Children[1].Text
	}
	for _, child := range root.Children {
		if status := depthOutputStatus(child, output); status != "" {
			return status
		}
	}
	return ""
}

func (h *wallpaperDepthHost) stop() {
	h.once.Do(func() {
		_ = h.send(&v1.HostShutdown{})
		_ = h.stdin.Close()
		select {
		case err := <-h.done:
			if err != nil {
				h.t.Errorf("plugin exit: %v", err)
			}
		case <-time.After(3 * time.Second):
			_ = h.cmd.Process.Kill()
			if err := <-h.done; err != nil {
				h.t.Errorf("plugin exit after kill: %v", err)
			}
			h.t.Errorf("plugin did not stop after HostShutdown")
		}
	})
}

func runGateFakeWallpaperDepth() int {
	args := os.Args[1:]
	operation := ""
	wallpaper := ""
	for i, arg := range args {
		switch arg {
		case "status", "generate", "clear-cache", "setup":
			operation = arg
		case "--wallpaper":
			if i+1 < len(args) {
				wallpaper = args[i+1]
			}
		}
	}
	gateDir := os.Getenv("SYSC_DEPTH_GATE_DIR")
	file, err := os.OpenFile(filepath.Join(gateDir, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	_, _ = fmt.Fprintln(file, operation)
	_ = file.Close()
	switch operation {
	case "status", "setup":
		_, _ = fmt.Fprintln(os.Stdout, `{"ready":true,"runtimeReady":true,"modelReady":true,"modelSha256":"fake-model"}`)
	case "generate":
		if _, err := os.Stat(filepath.Join(gateDir, "block-generate")); err == nil {
			_ = os.WriteFile(filepath.Join(gateDir, "generate-started"), nil, 0o600)
			deadline := time.Now().Add(20 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(gateDir, "generate-release")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					return 3
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		result, _ := json.Marshal(map[string]any{
			"ready": true, "maskPath": filepath.Join(gateDir, "mask-"+filepath.Base(wallpaper)+".png"),
			"wallpaperPath": wallpaper, "cacheHit": true, "elapsedMs": 12,
		})
		_, _ = fmt.Fprintln(os.Stdout, string(result))
		if _, err := os.Stat(filepath.Join(gateDir, "block-generate")); err == nil {
			_ = os.WriteFile(filepath.Join(gateDir, "generate-complete"), nil, 0o600)
		}
	case "clear-cache":
		_, _ = fmt.Fprintln(os.Stdout, `{"ready":true,"cleared":true}`)
	default:
		return 2
	}
	return 0
}
