package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/thinkpad"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestRunProcessesPanelSessionAndStopsPublishingClosedViews(t *testing.T) {
	roots := processTestRoots(t)
	host := startProcessHost(t, roots, filepath.Join(t.TempDir(), "profiles.json"))
	t.Cleanup(func() { host.cleanup(t) })

	if hello := host.waitHello(t); hello.Protocol != (v1.Version{Major: 1, Minor: 17}) {
		t.Fatalf("plugin protocol = %+v, want 1.17", hello.Protocol)
	}
	host.send(t, &v1.ViewOpen{
		ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Instance: "bar-instance",
		Output: "DP-1", Generation: 3, Width: 240, Height: 32,
	})
	bar := host.waitSnapshotAfter(t, "bar-1", 0)
	if bar.Revision != 1 {
		t.Fatalf("first bar revision = %d, want 1", bar.Revision)
	}

	host.send(t, &v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{
		"active_profile": "bright", "apply_on_start": true, "poll_interval_ms": float64(1000),
	}})
	barCount := host.snapshotCount("bar-1")
	host.send(t, &v1.SettingsChanged{Scope: v1.ScopeInstance, Instance: "bar-instance", Values: map[string]any{
		"show_fan": false, "show_kbd": true, "show_lid": true, "show_rpm": false,
	}})
	bar = host.waitSnapshotAfter(t, "bar-1", barCount)
	if wireNode(bar.Root, "fan") != nil {
		t.Error("instance settings left the fan gauge in the bar")
	}
	fan, err := os.ReadFile(roots.FanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fan), "level disengaged") {
		t.Errorf("startup profile left fan at %q, want bright profile's disengaged level", fan)
	}
	for path, want := range map[string]string{
		filepath.Join(roots.LEDsRoot, thinkpad.LEDKeyboardBacklight, "brightness"): "2",
		filepath.Join(roots.LEDsRoot, thinkpad.LEDLidLogoDot, "brightness"):        "3",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != want {
			t.Errorf("startup profile wrote %s = %q, want %s", path, strings.TrimSpace(string(got)), want)
		}
	}

	barCount = host.snapshotCount("bar-1")
	host.send(t, &v1.InputEvent{
		ViewID: "bar-1", Revision: bar.Revision, Node: "open", Event: v1.EventActivate,
		Output: "DP-1", Generation: 3,
	})
	openCall := host.waitCall(t, v1.CallPanelOpen, 0)
	assertPanelCall(t, openCall, "panel", "DP-1", "bar-instance", 3)
	host.waitSnapshotAfter(t, "bar-1", barCount)

	host.send(t, &v1.ViewOpen{
		ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Instance: "bar-instance",
		Output: "DP-1", Generation: 3, Width: thinkpad.PanelWidth, Height: 564,
	})
	panel := host.waitSnapshotAfter(t, "panel-1", 0)
	if wireNode(panel.Root, "fan-slider") == nil || wireNode(panel.Root, "kbd-slider") == nil {
		t.Error("panel snapshot is missing a fan or keyboard brightness slider")
	}

	panelCount := host.snapshotCount("panel-1")
	host.send(t, &v1.InputEvent{
		ViewID: "panel-1", Revision: panel.Revision, Node: "settings", Event: v1.EventActivate,
		Output: "DP-1", Generation: 3,
	})
	settingsCall := host.waitCall(t, v1.CallPanelOpen, 1)
	assertPanelCall(t, settingsCall, "settings", "DP-1", "bar-instance", 3)
	host.waitSnapshotAfter(t, "panel-1", panelCount)
	host.send(t, &v1.ViewClose{ViewID: "panel-1"})
	host.send(t, &v1.ViewOpen{
		ViewID: "settings-1", View: v1.ViewPanel, Entry: "settings", Instance: "bar-instance",
		Output: "DP-1", Generation: 3, Width: thinkpad.PanelWidth, Height: 720,
	})
	settings := host.waitSnapshotAfter(t, "settings-1", 0)
	if wireNode(settings.Root, "settings-title") == nil {
		t.Error("settings entry did not render its settings header")
	}

	settingsCount := host.snapshotCount("settings-1")
	host.send(t, &v1.InputEvent{
		ViewID: "settings-1", Revision: settings.Revision, Node: "back", Event: v1.EventActivate,
		Output: "DP-1", Generation: 3,
	})
	backCall := host.waitCall(t, v1.CallPanelOpen, 2)
	assertPanelCall(t, backCall, "panel", "DP-1", "bar-instance", 3)
	host.waitSnapshotAfter(t, "settings-1", settingsCount)
	host.send(t, &v1.ViewClose{ViewID: "settings-1"})
	host.send(t, &v1.ViewOpen{
		ViewID: "panel-2", View: v1.ViewPanel, Entry: "panel", Instance: "bar-instance",
		Output: "DP-1", Generation: 3, Width: thinkpad.PanelWidth, Height: 564,
	})
	panel2 := host.waitSnapshotAfter(t, "panel-2", 0)

	panelCount = host.snapshotCount("panel-2")
	host.send(t, &v1.InputEvent{
		ViewID: "panel-2", Revision: panel2.Revision, Node: "settings", Event: v1.EventActivate,
		Output: "DP-1", Generation: 3,
	})
	host.waitCall(t, v1.CallPanelOpen, 3)
	host.waitSnapshotAfter(t, "panel-2", panelCount)
	host.send(t, &v1.ViewClose{ViewID: "panel-2"})
	host.send(t, &v1.ViewOpen{
		ViewID: "settings-2", View: v1.ViewPanel, Entry: "settings", Instance: "bar-instance",
		Output: "DP-1", Generation: 3, Width: thinkpad.PanelWidth, Height: 720,
	})
	settings2 := host.waitSnapshotAfter(t, "settings-2", 0)

	settingsCount = host.snapshotCount("settings-2")
	host.send(t, &v1.InputEvent{
		ViewID: "settings-2", Revision: settings2.Revision, Node: "close", Event: v1.EventActivate,
		Output: "DP-1", Generation: 3,
	})
	closeCall := host.waitCall(t, v1.CallPanelClose, 0)
	assertPanelCall(t, closeCall, "settings", "DP-1", "bar-instance", 3)
	host.waitSnapshotAfter(t, "settings-2", settingsCount)
	host.send(t, &v1.ViewClose{ViewID: "settings-2"})
	host.send(t, &v1.ViewOpen{
		ViewID: "panel-3", View: v1.ViewPanel, Entry: "panel", Instance: "bar-instance",
		Output: "DP-1", Generation: 3, Width: thinkpad.PanelWidth, Height: 564,
	})
	host.waitSnapshotAfter(t, "panel-3", 0)
	host.send(t, &v1.ViewClose{ViewID: "bar-1"})
	host.send(t, &v1.SettingsChanged{Scope: v1.ScopeInstance, Instance: "bar-instance", Values: map[string]any{
		"show_fan": true, "show_kbd": true, "show_lid": true, "show_rpm": true,
	}})
	host.waitSnapshotAfter(t, "panel-3", 1)
	// The first post-close publish drains snapshots that were already in
	// flight from opening panel-3. Take the baseline only after that fence.
	barCount = host.snapshotCount("bar-1")
	host.send(t, &v1.SettingsChanged{Scope: v1.ScopeInstance, Instance: "bar-instance", Values: map[string]any{
		"show_fan": false, "show_kbd": true, "show_lid": true, "show_rpm": false,
	}})
	host.waitSnapshotAfter(t, "panel-3", 2)
	if got := host.snapshotCount("bar-1"); got != barCount {
		t.Errorf("closed bar received %d new snapshots, want none", got-barCount)
	}

	if err := host.shutdown(); err != nil {
		t.Fatal(err)
	}
	for id, snapshots := range host.allSnapshots() {
		for i := 1; i < len(snapshots); i++ {
			if snapshots[i].Revision <= snapshots[i-1].Revision {
				t.Errorf("%s revisions are not increasing: %d then %d",
					id, snapshots[i-1].Revision, snapshots[i].Revision)
			}
		}
	}
}

func TestViewResyncKeepsInputRevisionsMonotonic(t *testing.T) {
	roots := processTestRoots(t)
	host := startProcessHost(t, roots, filepath.Join(t.TempDir(), "profiles.json"))
	t.Cleanup(func() { host.cleanup(t) })
	host.waitHello(t)

	host.send(t, &v1.ViewOpen{
		ViewID: "bar", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Generation: 1,
		Width: 240, Height: 32,
	})
	initial := host.waitSnapshotAfter(t, "bar", 0)

	resyncCount := host.snapshotCount("bar")
	host.send(t, &v1.ViewResync{ViewID: "bar"})
	resynced := host.waitSnapshotAfter(t, "bar", resyncCount)
	if resynced.Revision <= initial.Revision {
		t.Fatalf("resync reused revision %d after revision %d", resynced.Revision, initial.Revision)
	}

	// The old tree may still have a queued pointer event when the shell asks for
	// a resync. Advance the process through another message before checking that
	// the stale event did not open the panel.
	count := host.snapshotCount("bar")
	host.send(t, &v1.InputEvent{
		ViewID: "bar", Revision: initial.Revision, Node: "open", Event: v1.EventActivate,
		Output: "DP-1", Generation: 1,
	})
	host.send(t, &v1.SettingsChanged{Scope: v1.ScopeInstance, Instance: "bar", Values: map[string]any{
		"show_fan": true,
	}})
	latest := host.waitSnapshotAfter(t, "bar", count)

	host.mu.Lock()
	var openCalls int
	for _, call := range host.calls {
		if call.Call == v1.CallPanelOpen {
			openCalls++
		}
	}
	host.mu.Unlock()
	if openCalls != 0 {
		t.Fatalf("stale revision opened the panel %d times", openCalls)
	}

	host.send(t, &v1.InputEvent{
		ViewID: "bar", Revision: latest.Revision, Node: "open", Event: v1.EventActivate,
		Output: "DP-1", Generation: 1,
	})
	host.waitCall(t, v1.CallPanelOpen, 0)
	if err := host.shutdown(); err != nil {
		t.Fatal(err)
	}
}

func processTestRoots(t *testing.T) thinkpad.Roots {
	t.Helper()
	root := t.TempDir()
	roots := thinkpad.Roots{FanPath: filepath.Join(root, "fan"), LEDsRoot: filepath.Join(root, "leds")}
	if err := os.WriteFile(roots.FanPath, []byte("status: enabled\nspeed: 3200\nlevel: auto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, brightness := range map[string]string{
		thinkpad.LEDKeyboardBacklight: "3",
		thinkpad.LEDLidLogoDot:        "0",
	} {
		led := filepath.Join(roots.LEDsRoot, name)
		if err := os.MkdirAll(led, 0o755); err != nil {
			t.Fatal(err)
		}
		for attr, value := range map[string]string{"brightness": brightness, "max_brightness": "3"} {
			if err := os.WriteFile(filepath.Join(led, attr), []byte(value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return roots
}

type processHost struct {
	pluginIn  *os.File
	hostIn    *os.File
	hostOut   *os.File
	pluginOut *os.File
	encoder   *v1.Encoder
	decoder   *v1.Decoder
	encodeMu  sync.Mutex
	mu        sync.Mutex
	snapshots map[string][]*v1.ViewSnapshot
	calls     []v1.HostCall
	changed   chan struct{}
	hello     chan *v1.PluginHello
	runDone   chan error
	readDone  chan struct{}
	closeOnce sync.Once
	stopErr   error
}

func startProcessHost(t *testing.T, roots thinkpad.Roots, storePath string) *processHost {
	t.Helper()
	pluginIn, hostIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	hostOut, pluginOut, err := os.Pipe()
	if err != nil {
		_ = pluginIn.Close()
		_ = hostIn.Close()
		t.Fatal(err)
	}
	h := &processHost{
		pluginIn: pluginIn, hostIn: hostIn, hostOut: hostOut, pluginOut: pluginOut,
		encoder: v1.NewEncoder(hostIn), decoder: v1.NewDecoder(hostOut, v1.ToHost),
		snapshots: map[string][]*v1.ViewSnapshot{}, changed: make(chan struct{}, 1),
		hello: make(chan *v1.PluginHello, 1), runDone: make(chan error, 1), readDone: make(chan struct{}),
	}
	go func() { h.runDone <- runWith(pluginIn, pluginOut, roots, storePath) }()
	go h.readLoop()
	h.send(t, &v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 17}},
		Plugin:       v1.Identity{ID: "org.sysc.thinkpad", Name: "ThinkPad", Version: "0.1.0"},
		Capabilities: []string{"panels", "settings", "notifications", "clipboard-write"},
		Limits:       v1.DefaultLimits,
	})
	return h
}

func (h *processHost) readLoop() {
	defer close(h.readDone)
	for {
		msg, err := h.decoder.Decode()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *v1.PluginHello:
			h.hello <- m
		case *v1.ViewSnapshot:
			h.mu.Lock()
			h.snapshots[m.ViewID] = append(h.snapshots[m.ViewID], m)
			h.mu.Unlock()
			h.wake()
		case *v1.HostCall:
			h.mu.Lock()
			h.calls = append(h.calls, *m)
			h.mu.Unlock()
			h.wake()
			result := json.RawMessage(`{"width":758,"height":564}`)
			if m.Call != v1.CallPanelResize {
				result = nil
			}
			h.encodeMu.Lock()
			_ = h.encoder.Encode(&v1.HostReply{ID: m.ID, OK: true, Result: result})
			h.encodeMu.Unlock()
		}
	}
}

func (h *processHost) send(t *testing.T, m v1.Message) {
	t.Helper()
	if err := h.write(m); err != nil {
		t.Fatal(err)
	}
}

func (h *processHost) write(m v1.Message) error {
	h.encodeMu.Lock()
	defer h.encodeMu.Unlock()
	return h.encoder.Encode(m)
}

func (h *processHost) waitHello(t *testing.T) *v1.PluginHello {
	t.Helper()
	select {
	case hello := <-h.hello:
		return hello
	case <-time.After(3 * time.Second):
		t.Fatal("plugin did not complete the protocol handshake")
		return nil
	}
}

func (h *processHost) waitSnapshotAfter(t *testing.T, viewID string, count int) *v1.ViewSnapshot {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		h.mu.Lock()
		items := h.snapshots[viewID]
		if len(items) > count {
			item := items[count]
			h.mu.Unlock()
			return item
		}
		h.mu.Unlock()
		select {
		case <-h.changed:
		case <-timer.C:
			t.Fatalf("view %s did not receive snapshot %d", viewID, count+1)
			return nil
		}
	}
}

func (h *processHost) waitCall(t *testing.T, kind v1.CallKind, index int) v1.HostCall {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		h.mu.Lock()
		var matching []v1.HostCall
		for _, call := range h.calls {
			if call.Call == kind {
				matching = append(matching, call)
			}
		}
		if len(matching) > index {
			call := matching[index]
			h.mu.Unlock()
			return call
		}
		h.mu.Unlock()
		select {
		case <-h.changed:
		case <-timer.C:
			t.Fatalf("plugin did not call %q at index %d", kind, index)
			return v1.HostCall{}
		}
	}
}

func (h *processHost) snapshotCount(viewID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.snapshots[viewID])
}

func (h *processHost) allSnapshots() map[string][]*v1.ViewSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	copyOf := make(map[string][]*v1.ViewSnapshot, len(h.snapshots))
	for id, items := range h.snapshots {
		copyOf[id] = append([]*v1.ViewSnapshot(nil), items...)
	}
	return copyOf
}

func (h *processHost) wake() {
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

func (h *processHost) shutdown() error {
	if err := h.write(&v1.HostShutdown{}); err != nil {
		return err
	}
	return h.stop()
}

func (h *processHost) stop() error {
	h.closeOnce.Do(func() {
		_ = h.hostIn.Close()
		select {
		case h.stopErr = <-h.runDone:
		case <-time.After(3 * time.Second):
			_ = h.pluginIn.Close()
			select {
			case h.stopErr = <-h.runDone:
			case <-time.After(3 * time.Second):
				h.stopErr = errors.New("plugin process did not stop")
			}
		}
		_ = h.pluginOut.Close()
		select {
		case <-h.readDone:
		case <-time.After(3 * time.Second):
		}
		_ = h.pluginIn.Close()
		_ = h.hostOut.Close()
	})
	return h.stopErr
}

func (h *processHost) cleanup(t *testing.T) {
	t.Helper()
	if err := h.stop(); err != nil {
		t.Errorf("stop plugin process: %v", err)
	}
}

func assertPanelCall(t *testing.T, call v1.HostCall, entry, output, instance string, generation uint32) {
	t.Helper()
	if call.Call != v1.CallPanelOpen && call.Call != v1.CallPanelClose {
		t.Fatalf("call = %q, want a panel open or close", call.Call)
	}
	var params v1.PanelParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatalf("decode panel params: %v", err)
	}
	if params.Entry != entry || params.Output != output || params.Instance != instance || params.Generation != generation {
		t.Errorf("panel params = %+v, want entry=%q output=%q instance=%q generation=%d",
			params, entry, output, instance, generation)
	}
}

func wireNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id || root.Key == id {
		return root
	}
	for _, child := range root.Children {
		if found := wireNode(child, id); found != nil {
			return found
		}
	}
	return nil
}
