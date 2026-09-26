package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestPluginFaithGate builds the Faith binary and drives it as the host
// would: the bar, the tooltip, and the panel, a prayer from the cross, a
// right click, verse navigation, and a cross-reference jump. Every snapshot
// is laid out with the host's own rules at the slot it was opened with, and
// the commentary host is down, so the offline path is the one exercised.
func TestPluginFaithGate(t *testing.T) {
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.faith")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-faith")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/faith/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-faith")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build faith: %v\n%s", err, out)
	}
	var m struct {
		Panels []struct{ Width, Height int } `json:"panels"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil || len(m.Panels) != 1 {
		t.Fatalf("manifest panels: %v", err)
	}
	down := httptest.NewServer(http.NotFoundHandler())
	api := down.URL
	down.Close()

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "SYSC_FAITH_API_BASE="+api)
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
	h := &faithHost{t: t, enc: v1.NewEncoder(stdin), roots: map[string]*v1.Node{}, slots: map[string]viewSlot{}}
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
			t.Error("faith did not exit after host.shutdown")
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
				if m.Call == v1.CallStateGet {
					reply.Result, _ = json.Marshal(v1.StateGetResult{})
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
		Supported:    []v1.Version{{Major: 1, Minor: 4}},
		Plugin:       v1.Identity{ID: "org.sysc.faith", Name: "Faith", Version: "0.1.0"},
		Capabilities: []string{"notifications", "panels", "settings", "state"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}

	h.open("bar-1", v1.ViewBar, "bar", lint.BarWidth, lint.BarHeight)
	h.open("tip-1", v1.ViewTooltip, "bar", lint.TooltipWidth, lint.TooltipHeight)
	h.open("panel-1", v1.ViewPanel, "panel", m.Panels[0].Width, m.Panels[0].Height)
	h.wait("the three views", func() bool {
		return findID(h.roots["bar-1"], "cross") != nil && strings.Contains(treeText(h.roots["tip-1"]), "Right-click") &&
			strings.Contains(treeText(h.roots["panel-1"]), "Commentary unavailable offline.")
	})

	h.click("bar-1", "cross", v1.EventActivate, "")
	h.wait("a prayer", func() bool { return h.count(v1.CallNotify) == 1 })
	h.click("bar-1", "cross", v1.EventPointer, v1.ButtonSecondary)
	h.wait("panel.open", func() bool { return h.count(v1.CallPanelOpen) == 1 })

	before := faithRef(h.root("panel-1"))
	h.click("panel-1", "next", v1.EventActivate, "")
	h.wait("the next verse", func() bool { return faithRef(h.roots["panel-1"]) != before })
	if findID(h.root("panel-1"), "xref:0") != nil {
		at := faithRef(h.root("panel-1"))
		h.click("panel-1", "xref:0", v1.EventActivate, "")
		h.wait("a cross-reference jump", func() bool { return faithRef(h.roots["panel-1"]) != at })
	}
	h.wait("a state save", func() bool { return h.count(v1.CallStateSet) > 0 })
}

type faithHost struct {
	t      *testing.T
	enc    *v1.Encoder
	sendMu sync.Mutex
	mu     sync.Mutex
	roots  map[string]*v1.Node
	slots  map[string]viewSlot
	calls  []v1.CallKind
}

func (h *faithHost) send(m v1.Message) error {
	h.sendMu.Lock()
	defer h.sendMu.Unlock()
	return h.enc.Encode(m)
}

func (h *faithHost) open(id string, kind v1.ViewKind, entry string, w, height int) {
	h.t.Helper()
	h.mu.Lock()
	h.slots = recordSlot(h.slots, id, viewSlot{kind, w, height})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: id, View: kind, Entry: entry, Output: "DP-1", Width: w, Height: height}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *faithHost) click(view, node string, ev v1.EventKind, button v1.PointerButton) {
	h.t.Helper()
	if err := h.send(&v1.InputEvent{ViewID: view, Node: node, Event: ev, Button: button, Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *faithHost) root(id string) *v1.Node {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.roots[id]
}

func (h *faithHost) count(kind v1.CallKind) int {
	n := 0
	for _, c := range h.calls {
		if c == kind {
			n++
		}
	}
	return n
}

func (h *faithHost) wait(what string, ok func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		good := ok()
		h.mu.Unlock()
		if good {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

// faithRef is the reference in the panel's header.
func faithRef(n *v1.Node) string {
	if n == nil || len(n.Children) == 0 || len(n.Children[0].Children) == 0 {
		return ""
	}
	return n.Children[0].Children[0].Text
}
