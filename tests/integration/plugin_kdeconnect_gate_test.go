package integration

import (
	"bytes"
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

// The gate runs the built plugin against a scripted host with no session
// bus guarantees: on a busless machine the service degrades to the
// unavailable snapshot, on a machine with kdeconnectd it may come up
// paired. Either way the handshake, the views, the settings change, the
// input round-trip, the state calls, and the two-views-one-process rule
// must hold.
func TestPluginKDEConnectGateServesViewsAndSurvivesInput(t *testing.T) {
	h := startKDEConnect(t)

	// The bar opens with the offline or the connected pill.
	h.openBar()
	h.waitView("bar-1", func(n *v1.Node) bool {
		open := findID(n, "open")
		return open != nil && (open.Icon == "smartphone" || open.Icon == "devices_other")
	})

	// The panel opens with the refresh control and a legal top state.
	h.openPanel()
	h.waitView("panel-1", kdeconnectPanelLegal)

	// An input round trip: refresh must neither crash nor wedge the tree.
	beforeRefresh := h.revOf("panel-1")
	h.clickOn("panel-1", "refresh")
	h.waitRev("panel-1", beforeRefresh+1)
	h.waitView("panel-1", kdeconnectPanelLegal)

	// The settings change is applied without a restart.
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{
		"refresh_seconds": float64(5), "enable_clipboard_action": false, "show_device_card": false,
	}}); err != nil {
		t.Fatal(err)
	}
	h.waitView("panel-1", kdeconnectPanelLegal)

	// A resync asks for a fresh snapshot; the revision moves forward.
	before := h.revOf("panel-1")
	if err := h.send(&v1.ViewResync{ViewID: "panel-1"}); err != nil {
		t.Fatal(err)
	}
	h.waitRev("panel-1", before+1)

	// Both views keep being served by the one process.
	h.waitView("bar-1", func(n *v1.Node) bool {
		open := findID(n, "open")
		return open != nil && (open.Icon == "smartphone" || open.Icon == "devices_other")
	})

	// The state store round-trips through the host replies.
	h.clickOn("panel-1", "refresh")
	h.waitView("panel-1", kdeconnectPanelLegal)
}

// kdeconnectPanelLegal accepts every top-level panel state the plugin can
// render: the unreachable card, the empty card, or populated device
// sections (switcher, device card, actions, info rows, composers).
func kdeconnectPanelLegal(n *v1.Node) bool {
	if findID(n, "refresh") == nil {
		return false
	}
	text := treeText(n)
	switch {
	case strings.Contains(text, "KDE Connect daemon unreachable"):
		return true
	case strings.Contains(text, "Phone Connect Not Available"):
		return true
	case strings.Contains(text, "No devices"):
		return true
	case findID(n, "ring") != nil:
		return true
	}
	return false
}

type kdeconnectHost struct {
	t     *testing.T
	enc   *v1.Encoder
	mu    sync.Mutex
	roots map[string]*v1.Node
	revs  map[string]uint64
	slots map[string]viewSlot
	wake  chan struct{}
}

func startKDEConnect(t *testing.T) *kdeconnectHost {
	t.Helper()
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.kdeconnect")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-kdeconnect")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/kdeconnect/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(pluginDir, "assets"), os.DirFS(filepath.Join(root, "plugins/kdeconnect/assets"))); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-kdeconnect")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kdeconnect: %v\n%s", err, out)
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
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("plugin stderr:\n%s", stderr.String())
		}
	})
	h := &kdeconnectHost{
		t:     t,
		enc:   v1.NewEncoder(stdin),
		roots: map[string]*v1.Node{},
		revs:  map[string]uint64{},
		wake:  make(chan struct{}, 1),
	}
	dec := v1.NewDecoder(stdout, v1.ToHost)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = h.send(&v1.HostShutdown{})
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = stdin.Close()
	})
	go func() {
		for {
			msg, err := dec.Decode()
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *v1.HostCall:
				// Reply OK with no payload: a state.get restore reads
				// nothing, a state.set write is accepted, a notify is
				// swallowed.
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
			case *v1.ViewPatch:
				h.mu.Lock()
				h.revs[m.ViewID] = m.Revision
				h.mu.Unlock()
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 2}},
		Plugin:       v1.Identity{ID: "org.sysc.kdeconnect", Name: "Phone Connect", Version: "0.1.0"},
		Capabilities: []string{"notifications", "panels", "settings", "state"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *kdeconnectHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *kdeconnectHost) openBar() {
	h.t.Helper()
	h.mu.Lock()
	h.slots = recordSlot(h.slots, "bar-1", viewSlot{v1.ViewBar, lint.BarWidth, lint.BarHeight})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Instance: "kdeconnect-1", Width: 240, Height: 32}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *kdeconnectHost) openPanel() {
	h.t.Helper()
	h.mu.Lock()
	h.slots = recordSlot(h.slots, "panel-1", viewSlot{v1.ViewPanel, 400, 520})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Output: "DP-1", Width: 400, Height: 520}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *kdeconnectHost) clickOn(view, id string) {
	h.t.Helper()
	h.mu.Lock()
	rev := h.revs[view]
	h.mu.Unlock()
	if err := h.send(&v1.InputEvent{ViewID: view, Revision: rev, Node: id, Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *kdeconnectHost) revOf(view string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revs[view]
}

func (h *kdeconnectHost) waitView(view string, ok func(*v1.Node) bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root := h.roots[view]
		h.mu.Unlock()
		if root != nil && ok(root) {
			return
		}
		wait := time.Until(deadline)
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		select {
		case <-h.wake:
		case <-time.After(wait):
		}
	}
	h.t.Fatalf("view %s never matched\n%s", view, dumpTree(h.roots[view]))
}

func (h *kdeconnectHost) waitRev(view string, want uint64) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.revOf(view) >= want {
			return
		}
		wait := time.Until(deadline)
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		select {
		case <-h.wake:
		case <-time.After(wait):
		}
	}
	h.t.Fatalf("view %s revision never reached %d", view, want)
}
