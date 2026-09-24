package main

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	wallpaperdepth "github.com/Nomadcxx/sysc-plugins/plugins/wallpaper-depth"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestRunWallpaperFlowAndResync(t *testing.T) {
	runner := newCommandRunner()
	host := startCommandHost(t, runner)
	hello := host.hello()
	if hello.Plugin != (v1.Identity{ID: "org.sysc.wallpaper-depth", Name: "Wallpaper Depth", Version: "1.0.0"}) {
		t.Fatalf("plugin identity = %+v", hello.Plugin)
	}
	if !slices.Contains(hello.Capabilities, "wallpaper") {
		t.Fatalf("handshake capabilities = %v, want wallpaper", hello.Capabilities)
	}

	host.waitCallCount(t, v1.CallWallpaperSnapshot, 1)
	if len(host.callsOf(v1.CallPanelOpen)) != 0 {
		t.Fatal("first wallpaper poll waited for the panel to open")
	}
	host.send(t, &v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Instance: "bar-instance"})
	bar := host.waitView(t, "bar-1", func(s commandSnapshot) bool { return s.Root != nil })
	if bar.Root.Children[0].Icon != "wallpaper" {
		t.Fatalf("bar root = %+v, want wallpaper glyph", bar.Root)
	}
	host.send(t, &v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "bar-1", Node: "open", Event: v1.EventActivate, Output: "DP-1"})
	open := host.waitCallCount(t, v1.CallPanelOpen, 1)[0]
	var panelParams v1.PanelParams
	if err := json.Unmarshal(open.params, &panelParams); err != nil {
		t.Fatal(err)
	}
	if panelParams.Entry != "panel" || panelParams.Output != "DP-1" || panelParams.Instance != "bar-instance" {
		t.Fatalf("panel.open params = %+v", panelParams)
	}
	host.send(t, &v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Output: "DP-1"})
	panel := host.waitView(t, "panel-1", func(s commandSnapshot) bool {
		text := commandTreeText(s.Root)
		return strings.Contains(text, "Automatic on") && strings.Contains(text, "Threshold 30") && strings.Contains(text, "Feather 8")
	})
	if !strings.Contains(commandTreeText(panel.Root), "No outputs") && !strings.Contains(commandTreeText(panel.Root), "DP-1") {
		t.Fatalf("initial panel did not show wallpaper state: %s", commandTreeText(panel.Root))
	}
	maskCalls := host.waitCallCount(t, v1.CallWallpaperMaskSet, 1)
	var initialMask v1.WallpaperMaskSetParams
	if err := json.Unmarshal(maskCalls[0].params, &initialMask); err != nil {
		t.Fatal(err)
	}
	if initialMask.Output != "DP-1" || initialMask.WallpaperPath != "/wall.jpg" || initialMask.MaskPath != "/mask/generated.png" {
		t.Fatalf("initial mask registration = %+v", initialMask)
	}
	host.waitView(t, "panel-1", func(s commandSnapshot) bool { return strings.Contains(commandTreeText(s.Root), "DP-1") })

	host.send(t, &v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin, Values: map[string]any{
		"auto_generate": false, "threshold": 40.0, "feather": 12.0,
	}})
	host.waitView(t, "panel-1", func(s commandSnapshot) bool {
		return strings.Contains(commandTreeText(s.Root), "Automatic off") && strings.Contains(commandTreeText(s.Root), "Threshold 40")
	})
	maskCalls = host.waitCallCount(t, v1.CallWallpaperMaskSet, 2)
	var clearedMask v1.WallpaperMaskSetParams
	if err := json.Unmarshal(maskCalls[1].params, &clearedMask); err != nil {
		t.Fatal(err)
	}
	if clearedMask.Output != "DP-1" || clearedMask.WallpaperPath != "/wall.jpg" || clearedMask.MaskPath != "" {
		t.Fatalf("parameter-change mask clear = %+v", clearedMask)
	}

	host.send(t, &v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Node: "generate-DP-1", Event: v1.EventActivate})
	maskCalls = host.waitCallCount(t, v1.CallWallpaperMaskSet, 3)
	var generatedMask v1.WallpaperMaskSetParams
	if err := json.Unmarshal(maskCalls[2].params, &generatedMask); err != nil {
		t.Fatal(err)
	}
	if generatedMask.Output != "DP-1" || generatedMask.WallpaperPath != "/wall.jpg" || generatedMask.MaskPath == "" {
		t.Fatalf("manual mask registration = %+v", generatedMask)
	}
	foundSettings := false
	for _, args := range runner.callsOf("generate") {
		if slices.Contains(args, "0.40") && slices.Contains(args, "0.24") {
			foundSettings = true
			break
		}
	}
	if !foundSettings {
		t.Fatal("manual generation did not use threshold .40 and feather .24")
	}

	host.send(t, &v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Node: "clear-cache", Event: v1.EventActivate})
	waitCommand(t, func() bool { return runner.count("clear-cache") == 1 })
	maskCalls = host.waitCallCount(t, v1.CallWallpaperMaskSet, 4)
	var cacheClear v1.WallpaperMaskSetParams
	if err := json.Unmarshal(maskCalls[3].params, &cacheClear); err != nil {
		t.Fatal(err)
	}
	if cacheClear.Output != "DP-1" || cacheClear.WallpaperPath != "/wall.jpg" || cacheClear.MaskPath != "" {
		t.Fatalf("clear-cache mask clear = %+v", cacheClear)
	}
	host.send(t, &v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Node: "setup", Event: v1.EventActivate})
	waitCommand(t, func() bool { return runner.count("setup") == 1 })
	host.send(t, &v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Node: "check", Event: v1.EventActivate})
	waitCommand(t, func() bool { return runner.count("status") >= 2 })

	beforeResync := host.latestView("panel-1")
	if beforeResync.Revision <= 1 {
		t.Fatalf("panel revision before resync = %d, want a later revision", beforeResync.Revision)
	}
	host.send(t, &v1.ViewResync{Type: v1.TypeViewResync, ViewID: "panel-1"})
	resynced := host.waitView(t, "panel-1", func(s commandSnapshot) bool { return s.Revision == 1 })
	if resynced.Revision != 1 {
		t.Fatalf("resync revision = %d, want 1", resynced.Revision)
	}
	if len(host.callsOf(v1.CallNotify)) != 0 {
		t.Fatalf("unexpected generic notification calls: %+v", host.callsOf(v1.CallNotify))
	}
	host.stop(t)
}

func TestPollsDoNotOverlap(t *testing.T) {
	runner := newCommandRunner()
	host := startCommandHost(t, runner)
	host.setSnapshotDelay(100 * time.Millisecond)
	calls := host.waitCallCount(t, v1.CallWallpaperSnapshot, 3)
	for i := 1; i < len(calls); i++ {
		if gap := calls[i].at.Sub(calls[i-1].at); gap < 700*time.Millisecond {
			t.Fatalf("wallpaper polls overlapped or ran too quickly: gap %s", gap)
		}
	}
	if host.maxActiveSnapshots() != 1 {
		t.Fatalf("concurrent snapshot calls = %d, want 1", host.maxActiveSnapshots())
	}
	host.stop(t)
}

func TestRunShutdownCancelsHelperContext(t *testing.T) {
	runner := newCommandRunner()
	gate := make(chan struct{})
	runner.block("status", gate)
	host := startCommandHost(t, runner)
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("helper status did not start")
	}
	host.stop(t)
	select {
	case <-runner.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel helper context")
	}
}

type commandRunner struct {
	mu        sync.Mutex
	calls     [][]string
	active    int
	maxActive int
	gates     map[string]chan struct{}
	started   chan struct{}
	cancelled chan struct{}
	cancelOne sync.Once
}

func newCommandRunner() *commandRunner {
	return &commandRunner{gates: make(map[string]chan struct{}), started: make(chan struct{}, 8), cancelled: make(chan struct{})}
}

func (r *commandRunner) block(operation string, gate chan struct{}) {
	r.mu.Lock()
	r.gates[operation] = gate
	r.mu.Unlock()
}

func (r *commandRunner) Run(ctx context.Context, args ...string) (json.RawMessage, error) {
	args = slices.Clone(args)
	if len(args) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	r.mu.Lock()
	r.calls = append(r.calls, args)
	r.active++
	r.maxActive = max(r.maxActive, r.active)
	gate := r.gates[args[0]]
	delete(r.gates, args[0])
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	select {
	case r.started <- struct{}{}:
	default:
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			r.cancelOne.Do(func() { close(r.cancelled) })
			return nil, ctx.Err()
		}
	}
	switch args[0] {
	case "status", "setup", "clear-cache":
		return json.RawMessage(`{"ready":true,"runtimeReady":true,"modelReady":true}`), nil
	case "generate":
		return json.RawMessage(`{"ready":true,"maskPath":"/mask/generated.png","cacheHit":false,"elapsedMs":12}`), nil
	default:
		return nil, io.ErrUnexpectedEOF
	}
}

func (r *commandRunner) callsOf(operation string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]string
	for _, call := range r.calls {
		if call[0] == operation {
			out = append(out, slices.Clone(call))
		}
	}
	return out
}

func (r *commandRunner) count(operation string) int { return len(r.callsOf(operation)) }

type commandCall struct {
	kind   v1.CallKind
	params json.RawMessage
	at     time.Time
}

type commandSnapshot struct {
	ViewID   string
	Revision uint64
	Root     *v1.Node
}

type commandHost struct {
	input             *io.PipeWriter
	output            *io.PipeWriter
	enc               *v1.Encoder
	dec               *v1.Decoder
	sendMu            sync.Mutex
	mu                sync.Mutex
	calls             []commandCall
	snapshots         []commandSnapshot
	pluginHello       *v1.PluginHello
	wake              chan struct{}
	snapshotResult    v1.WallpaperSnapshotResult
	snapshotDelay     time.Duration
	snapshotActive    int
	maxSnapshotActive int
	runDone           chan struct{}
	runErr            error
	stopOnce          sync.Once
}

func startCommandHost(t *testing.T, runner wallpaperdepth.Runner) *commandHost {
	t.Helper()
	pluginIn, hostIn := io.Pipe()
	hostOut, pluginOut := io.Pipe()
	h := &commandHost{
		input: hostIn, output: pluginOut,
		enc: v1.NewEncoder(hostIn), dec: v1.NewDecoder(hostOut, v1.ToHost),
		wake: make(chan struct{}, 1), runDone: make(chan struct{}),
		snapshotResult: v1.WallpaperSnapshotResult{Revision: 1, Scale: "fill", Outputs: []v1.WallpaperOutput{{Output: "DP-1", State: v1.WallpaperImage, Path: "/wall.jpg"}}},
	}
	go h.readPlugin()
	go func() {
		h.runErr = runWith(pluginIn, pluginOut, runner)
		close(h.runDone)
	}()
	h.send(t, &v1.HostHello{
		Type: v1.TypeHostHello, Supported: []v1.Version{{Major: 1, Minor: 7}},
		Plugin:       v1.Identity{ID: "org.sysc.wallpaper-depth", Name: "Wallpaper Depth", Version: "1.0.0"},
		Capabilities: []string{"panels", "settings", "state", "wallpaper"}, Limits: v1.DefaultLimits,
	})
	waitCommand(t, func() bool { return h.hello() != nil })
	t.Cleanup(func() {
		if !isCommandDone(h.runDone) {
			h.sendMu.Lock()
			_ = h.enc.Encode(&v1.HostShutdown{Type: v1.TypeHostShutdown})
			h.sendMu.Unlock()
			_ = h.input.Close()
		}
		select {
		case <-h.runDone:
		case <-time.After(3 * time.Second):
			t.Error("plugin did not exit during cleanup")
		}
		_ = h.output.Close()
	})
	return h
}

func (h *commandHost) readPlugin() {
	for {
		message, err := h.dec.Decode()
		if err != nil {
			return
		}
		switch message := message.(type) {
		case *v1.PluginHello:
			h.mu.Lock()
			h.pluginHello = message
			h.mu.Unlock()
			h.signal()
		case *v1.ViewSnapshot:
			h.mu.Lock()
			h.snapshots = append(h.snapshots, commandSnapshot{ViewID: message.ViewID, Revision: message.Revision, Root: message.Root})
			h.mu.Unlock()
			h.signal()
		case *v1.HostCall:
			h.handleCall(message)
		}
	}
}

func (h *commandHost) handleCall(call *v1.HostCall) {
	record := commandCall{kind: call.Call, params: slices.Clone(call.Params), at: time.Now()}
	h.mu.Lock()
	h.calls = append(h.calls, record)
	var result json.RawMessage
	if call.Call == v1.CallWallpaperSnapshot {
		h.snapshotActive++
		h.maxSnapshotActive = max(h.maxSnapshotActive, h.snapshotActive)
		result, _ = json.Marshal(h.snapshotResult)
	}
	delay := h.snapshotDelay
	h.mu.Unlock()
	h.signal()
	if call.Call == v1.CallWallpaperSnapshot {
		if delay != 0 {
			time.Sleep(delay)
		}
		h.mu.Lock()
		h.snapshotActive--
		h.mu.Unlock()
	}
	h.sendReply(&v1.HostReply{Type: v1.TypeHostReply, ID: call.ID, OK: true, Result: result})
}

func (h *commandHost) send(t *testing.T, message v1.Message) {
	t.Helper()
	h.sendMu.Lock()
	err := h.enc.Encode(message)
	h.sendMu.Unlock()
	if err != nil {
		t.Fatalf("host send: %v", err)
	}
}

func (h *commandHost) sendReply(message v1.Message) {
	h.sendMu.Lock()
	_ = h.enc.Encode(message)
	h.sendMu.Unlock()
}

func (h *commandHost) signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *commandHost) callsOf(kind v1.CallKind) []commandCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	var calls []commandCall
	for _, call := range h.calls {
		if call.kind == kind {
			call.params = slices.Clone(call.params)
			calls = append(calls, call)
		}
	}
	return calls
}

func (h *commandHost) hello() *v1.PluginHello {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pluginHello == nil {
		return nil
	}
	copy := *h.pluginHello
	copy.Capabilities = slices.Clone(copy.Capabilities)
	return &copy
}

func (h *commandHost) setSnapshotDelay(delay time.Duration) {
	h.mu.Lock()
	h.snapshotDelay = delay
	h.mu.Unlock()
}

func (h *commandHost) maxActiveSnapshots() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.maxSnapshotActive
}

func (h *commandHost) waitCallCount(t *testing.T, kind v1.CallKind, count int) []commandCall {
	t.Helper()
	waitCommand(t, func() bool { return len(h.callsOf(kind)) >= count })
	return h.callsOf(kind)
}

func (h *commandHost) waitView(t *testing.T, id string, match func(commandSnapshot) bool) commandSnapshot {
	t.Helper()
	var got commandSnapshot
	waitCommand(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i := len(h.snapshots) - 1; i >= 0; i-- {
			if h.snapshots[i].ViewID == id && match(h.snapshots[i]) {
				got = h.snapshots[i]
				return true
			}
		}
		return false
	})
	return got
}

func (h *commandHost) latestView(id string) commandSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.snapshots) - 1; i >= 0; i-- {
		if h.snapshots[i].ViewID == id {
			return h.snapshots[i]
		}
	}
	return commandSnapshot{}
}

func (h *commandHost) stop(t *testing.T) {
	t.Helper()
	h.stopOnce.Do(func() {
		if !isCommandDone(h.runDone) {
			h.send(t, &v1.HostShutdown{Type: v1.TypeHostShutdown})
			_ = h.input.Close()
		}
	})
	select {
	case <-h.runDone:
		if h.runErr != nil {
			t.Fatalf("plugin run: %v", h.runErr)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("plugin did not stop after host.shutdown")
	}
}

func isCommandDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func waitCommand(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func commandTreeText(root *v1.Node) string {
	if root == nil {
		return ""
	}
	parts := []string{root.Text}
	for _, child := range root.Children {
		parts = append(parts, commandTreeText(child))
	}
	return strings.Join(parts, " ")
}
