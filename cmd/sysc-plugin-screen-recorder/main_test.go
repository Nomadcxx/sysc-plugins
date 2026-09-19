package main

import (
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/screen-recorder"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestMain(m *testing.M) {
	if os.Getenv("SYSC_FAKE_RECORDER") == "1" {
		os.Exit(runCmdFake())
	}
	os.Exit(m.Run())
}

func runCmdFake() int {
	signal.Reset(syscall.SIGINT)
	if os.Getenv("SYSC_FAKE_BEHAVIOR") == "crash" {
		_, _ = os.Stdout.WriteString("ready\n")
		return 1
	}
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGUSR1)
	if p := argAfter("-o"); p != "" {
		_ = os.WriteFile(p, []byte("mp4"), 0o644)
	}
	_, _ = os.Stdout.WriteString("ready\n")
	for sig := range ch {
		if sig == syscall.SIGUSR1 {
			continue
		}
		return 0
	}
	return 0
}

func argAfter(flag string) string {
	args := os.Args[1:]
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestPluginToggleOpensPanel(t *testing.T) {
	h := startPlugin(t, recorder.Options{
		Exe:      os.Args[0],
		LookPath: func(string) (string, error) { return os.Args[0], nil },
		Env:      append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR=hang"),
		StopWait: 250 * time.Millisecond,
	})
	h.open("bar-a", v1.ViewBar, "DP-1")
	h.wait("bar-a", func(n *v1.Node) bool { return findNode(n, "toggle") != nil })
	if err := h.send(&v1.InputEvent{
		ViewID: "bar-a", Node: "toggle", Event: v1.EventPointer,
		Button: v1.ButtonSecondary, Output: "DP-1",
	}); err != nil {
		t.Fatal(err)
	}
	p := h.waitPanelOpen()
	if p.Entry != "panel" {
		t.Fatalf("panel.open entry = %q, want panel", p.Entry)
	}
}

func TestPluginRightClickOpensPanelWhenUnavailable(t *testing.T) {
	h := startPlugin(t, recorder.Options{
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	h.open("bar-a", v1.ViewBar, "DP-1")
	h.wait("bar-a", func(n *v1.Node) bool { return findNode(n, "toggle") != nil })
	h.open("panel-a", v1.ViewPanel, "DP-1")
	h.wait("panel-a", func(n *v1.Node) bool {
		return n != nil && strings.Contains(treeText(n), "not installed")
	})
	if err := h.send(&v1.InputEvent{
		ViewID: "bar-a", Node: "toggle", Event: v1.EventPointer,
		Button: v1.ButtonSecondary, Output: "DP-1",
	}); err != nil {
		t.Fatal(err)
	}
	p := h.waitPanelOpen()
	if p.Entry != "panel" {
		t.Fatalf("panel.open entry = %q, want panel", p.Entry)
	}
}

func TestPluginPanelViewUsesPanelTree(t *testing.T) {
	h := startPlugin(t, recorder.Options{
		Exe:      os.Args[0],
		LookPath: func(string) (string, error) { return os.Args[0], nil },
		Env:      append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR=hang"),
		StopWait: 250 * time.Millisecond,
	})
	h.open("panel-a", v1.ViewPanel, "DP-1")
	h.wait("panel-a", func(n *v1.Node) bool {
		return n != nil && n.Kind == v1.KindColumn && strings.Contains(treeText(n), "Screen Recorder")
	})
}

func TestPluginElapsedTicksWhileRecording(t *testing.T) {
	h := startPlugin(t, recorder.Options{
		Exe:      os.Args[0],
		LookPath: func(string) (string, error) { return os.Args[0], nil },
		Env:      append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR=hang"),
		StopWait: 250 * time.Millisecond,
	})
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{"directory": h.dir}}); err != nil {
		t.Fatal(err)
	}
	h.open("panel-a", v1.ViewPanel, "DP-1")
	h.wait("panel-a", func(n *v1.Node) bool { return strings.Contains(treeText(n), "Screen Recorder") })
	if err := h.send(&v1.InputEvent{ViewID: "panel-a", Node: "record", Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		t.Fatal(err)
	}
	h.wait("panel-a", func(n *v1.Node) bool { return strings.Contains(treeText(n), "Recording") })
	h.wait("panel-a", func(n *v1.Node) bool {
		el := findNode(n, "elapsed")
		return el != nil && el.Text != "" && el.Text != "00:00"
	})
}

func TestPluginSettingsRebuildAndNotifyAndShutdown(t *testing.T) {
	h := startPlugin(t, recorder.Options{
		Exe:      os.Args[0],
		LookPath: func(string) (string, error) { return os.Args[0], nil },
		Env:      append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR=hang"),
		StopWait: 250 * time.Millisecond,
	})
	h.open("bar-a", v1.ViewBar, "DP-1")
	h.open("bar-b", v1.ViewBar, "HDMI-1")
	h.wait("bar-a", func(n *v1.Node) bool { return findNode(n, "toggle") != nil })
	h.wait("bar-b", func(n *v1.Node) bool { return findNode(n, "toggle") != nil })

	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{
		"directory": h.dir, "frame_rate": 30.0,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := h.send(&v1.InputEvent{ViewID: "bar-a", Node: "toggle", Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		t.Fatal(err)
	}
	h.wait("bar-a", barCapturing)
	h.wait("bar-b", barCapturing)

	if err := h.send(&v1.InputEvent{ViewID: "bar-a", Node: "toggle", Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		t.Fatal(err)
	}
	h.wait("bar-a", barIdle)
	h.waitNotify(func(p v1.NotifyParams) bool {
		return strings.Contains(p.Summary, "saved") || strings.Contains(p.Body, h.dir)
	})

	if err := h.send(&v1.HostShutdown{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not stop the plugin")
	}
}

func TestPluginFailureNotifyIncludesLogs(t *testing.T) {
	h := startPlugin(t, recorder.Options{
		Exe:      os.Args[0],
		LookPath: func(string) (string, error) { return os.Args[0], nil },
		Env:      append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR=crash"),
		StopWait: 250 * time.Millisecond,
	})
	h.open("bar-a", v1.ViewBar, "DP-1")
	h.wait("bar-a", func(n *v1.Node) bool { return findNode(n, "toggle") != nil })
	if err := h.send(&v1.InputEvent{ViewID: "bar-a", Node: "toggle", Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		t.Fatal(err)
	}
	h.wait("bar-a", barFailed)
	h.waitNotify(func(p v1.NotifyParams) bool {
		return strings.Contains(strings.ToLower(p.Body+p.Summary), "fail") || p.Body != ""
	})
}

type pluginHost struct {
	t         *testing.T
	dir       string
	enc       *v1.Encoder
	dec       *v1.Decoder
	mu        sync.Mutex
	roots     map[string]*v1.Node
	notify    []v1.NotifyParams
	panels    []v1.PanelParams
	wake      chan struct{}
	done      chan struct{}
	snapCount atomic.Int32
}

func startPlugin(t *testing.T, opt recorder.Options) *pluginHost {
	t.Helper()
	toPlugin, fromHost := io.Pipe()
	toHost, fromPlugin := io.Pipe()
	h := &pluginHost{
		t: t, dir: t.TempDir(),
		enc: v1.NewEncoder(fromHost), dec: v1.NewDecoder(toHost, v1.ToHost),
		roots: make(map[string]*v1.Node), wake: make(chan struct{}, 8), done: make(chan struct{}),
	}
	go func() {
		defer close(h.done)
		_ = run(toPlugin, fromPlugin, opt)
	}()
	t.Cleanup(func() {
		_ = h.send(&v1.HostShutdown{})
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
		}
		_ = fromHost.Close()
		_ = toHost.Close()
	})
	go func() {
		for {
			msg, err := h.dec.Decode()
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *v1.HostCall:
				reply := v1.HostReply{ID: m.ID, OK: true}
				switch m.Call {
				case v1.CallNotify:
					var p v1.NotifyParams
					_ = json.Unmarshal(m.Params, &p)
					h.mu.Lock()
					h.notify = append(h.notify, p)
					h.mu.Unlock()
					select {
					case h.wake <- struct{}{}:
					default:
					}
					raw, _ := json.Marshal(v1.NotifyResult{ID: 1})
					reply.Result = raw
				case v1.CallPanelOpen:
					var p v1.PanelParams
					_ = json.Unmarshal(m.Params, &p)
					h.mu.Lock()
					h.panels = append(h.panels, p)
					h.mu.Unlock()
					select {
					case h.wake <- struct{}{}:
					default:
					}
					raw, _ := json.Marshal(v1.PanelResult{})
					reply.Result = raw
				case v1.CallOutputContext:
					raw, _ := json.Marshal(v1.OutputContextResult{Output: "DP-1", Generation: 1})
					reply.Result = raw
				case v1.CallStateGet:
					raw, _ := json.Marshal(v1.StateGetResult{Found: false})
					reply.Result = raw
				}
				_ = h.send(&reply)
			case *v1.ViewSnapshot:
				h.mu.Lock()
				h.roots[m.ViewID] = m.Root
				h.mu.Unlock()
				h.snapCount.Add(1)
				select {
				case h.wake <- struct{}{}:
				default:
				}
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 0}},
		Plugin:       v1.Identity{ID: "org.sysc.screen-recorder", Name: "Screen Recorder", Version: "1.0.0"},
		Capabilities: []string{"notifications", "settings", "state"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *pluginHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *pluginHost) open(id string, kind v1.ViewKind, output string) {
	h.t.Helper()
	if err := h.send(&v1.ViewOpen{ViewID: id, View: kind, Entry: "bar", Output: output, Width: 120, Height: 32}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginHost) wait(id string, ok func(*v1.Node) bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root := h.roots[id]
		h.mu.Unlock()
		if root != nil && ok(root) {
			return
		}
		select {
		case <-h.wake:
		case <-time.After(40 * time.Millisecond):
		}
	}
	h.t.Fatalf("view %s never matched", id)
}

func (h *pluginHost) notifies() []v1.NotifyParams {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]v1.NotifyParams, len(h.notify))
	copy(out, h.notify)
	return out
}

func (h *pluginHost) waitNotify(ok func(v1.NotifyParams) bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range h.notifies() {
			if ok(p) {
				return
			}
		}
		select {
		case <-h.wake:
		case <-time.After(40 * time.Millisecond):
		}
	}
	h.t.Fatalf("notification never matched: %+v", h.notifies())
}

func (h *pluginHost) waitPanelOpen() v1.PanelParams {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		n := len(h.panels)
		var p v1.PanelParams
		if n > 0 {
			p = h.panels[n-1]
		}
		h.mu.Unlock()
		if n > 0 {
			return p
		}
		select {
		case <-h.wake:
		case <-time.After(40 * time.Millisecond):
		}
	}
	h.t.Fatal("panel.open never issued")
	return v1.PanelParams{}
}

func treeText(n *v1.Node) string {
	if n == nil {
		return ""
	}
	parts := []string{n.Text, n.Name}
	for _, c := range n.Children {
		parts = append(parts, treeText(c))
	}
	return strings.Join(parts, " ")
}

func findNode(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id || n.Key == id {
		return n
	}
	for _, c := range n.Children {
		if got := findNode(c, id); got != nil {
			return got
		}
	}
	return nil
}

func barCapturing(n *v1.Node) bool {
	tog := findNode(n, "toggle")
	return tog != nil && tog.Icon == "stop" && tog.Tone == v1.ToneError
}

func barFailed(n *v1.Node) bool {
	tog := findNode(n, "toggle")
	return tog != nil && tog.Icon == "camera-off"
}

func barIdle(n *v1.Node) bool {
	tog := findNode(n, "toggle")
	return tog != nil && tog.Icon == "record" && tog.Tone == v1.ToneNormal
}

func TestPluginHideInactive(t *testing.T) {
	h := startPlugin(t, recorder.Options{
		LookPath: func(string) (string, error) { return os.Args[0], nil },
		Exe:      os.Args[0],
		Env:      append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR=hang"),
		StopWait: 250 * time.Millisecond,
	})
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{"hide_inactive": true, "directory": h.dir}}); err != nil {
		t.Fatal(err)
	}
	h.open("bar-a", v1.ViewBar, "DP-1")
	h.wait("bar-a", func(n *v1.Node) bool {
		return n != nil && n.Kind == v1.KindRow && len(n.Children) == 0
	})
}
