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

	"github.com/Nomadcxx/sysc-plugins/internal/wire"
)

func TestPluginCalendarGateMonthNavigation(t *testing.T) {
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.calendar")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-calendar")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/calendar/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-calendar")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build calendar: %v\n%s", err, out)
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
	h := &calendarHost{t: t, enc: v1.NewEncoder(stdin), wake: make(chan struct{}, 1)}
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
				_ = h.send(&v1.HostReply{ID: m.ID, OK: true})
			case *v1.ViewSnapshot:
				h.mu.Lock()
				h.root = m.Root
				h.rev = m.Revision
				h.mu.Unlock()
				select {
				case h.wake <- struct{}{}:
				default:
				}
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 0}},
		Plugin:       v1.Identity{ID: "org.sysc.calendar", Name: "Calendar", Version: "0.1.0"},
		Capabilities: []string{"panels", "settings"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Output: "DP-1", Width: 320, Height: 420}); err != nil {
		t.Fatal(err)
	}

	thisMonth := time.Now().Format("January 2006")
	prevMonth := time.Now().AddDate(0, -1, 0).Format("January 2006")
	h.waitNode(func(n *v1.Node) bool {
		return nodeText(n, "root") == "" && treeText(n) != "" && strings.Contains(treeText(n), thisMonth)
	})

	h.click("cal-prev")
	h.waitNode(func(n *v1.Node) bool { return strings.Contains(treeText(n), prevMonth) })

	h.click("today")
	h.waitNode(func(n *v1.Node) bool {
		text := treeText(n)
		return strings.Contains(text, thisMonth) && findID(n, "today") != nil
	})

	// The bar view shows the day of the month.
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Width: 64, Height: 32}); err != nil {
		t.Fatal(err)
	}
	h.waitNode(func(n *v1.Node) bool {
		return nodeText(n, "root") == "" && treeText(n) != "" && strings.Contains(treeText(n), time.Now().Format("2"))
	})
}

type calendarHost struct {
	t    *testing.T
	enc  *v1.Encoder
	mu   sync.Mutex
	root *v1.Node
	rev  uint64
	wake chan struct{}
}

func (h *calendarHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *calendarHost) click(id string) {
	h.t.Helper()
	h.mu.Lock()
	rev := h.rev
	h.mu.Unlock()
	if err := h.send(&v1.InputEvent{ViewID: "panel-1", Revision: rev, Node: id, Event: v1.EventActivate, Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *calendarHost) waitNode(ok func(*v1.Node) bool) *v1.Node {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root := h.root
		h.mu.Unlock()
		if root != nil && ok(root) {
			return root
		}
		select {
		case <-h.wake:
		case <-time.After(40 * time.Millisecond):
		}
	}
	h.t.Fatalf("tree never matched\n%s", dumpTree(h.root))
	return nil
}
