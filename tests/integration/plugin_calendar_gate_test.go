package integration

import (
	"bytes"
	"encoding/json"
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

func TestPluginCalendarGateAllViews(t *testing.T) {
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
	build := exec.Command("go", "build", "-tags=calendar_gate", "-o", bin, "./cmd/sysc-plugin-calendar")
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
	h := &calendarHost{t: t, enc: v1.NewEncoder(stdin), roots: make(map[string]*v1.Node), revs: make(map[string]uint64), wake: make(chan struct{}, 1)}
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
				result := json.RawMessage(`{}`)
				if m.Call == v1.CallStateGet {
					result, _ = json.Marshal(v1.StateGetResult{Found: false})
				}
				_ = h.send(&v1.HostReply{ID: m.ID, OK: true, Result: result})
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
		Supported:    []v1.Version{{Major: 1, Minor: 8}},
		Plugin:       v1.Identity{ID: "org.sysc.calendar", Name: "Calendar", Version: "0.3.0"},
		Capabilities: []string{"panels", "settings", "state", "clipboard-write", "open-url"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.slots = recordSlot(h.slots, "panel-1", viewSlot{v1.ViewPanel, 1040, 760})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Output: "DP-1", Width: 1040, Height: 760}); err != nil {
		t.Fatal(err)
	}

	thisMonth := time.Now().Format("January 2006")
	prevMonth := time.Now().AddDate(0, -1, 0).Format("January 2006")
	h.waitNode("panel-1", func(n *v1.Node) bool {
		return findID(n, "cal-view-month") != nil && panelTitle(n) == thisMonth
	})

	h.clickUntil("cal-prev", func(n *v1.Node) bool { return panelTitle(n) == prevMonth })
	h.clickUntil("cal-today", func(n *v1.Node) bool {
		currentDay := findID(n, "cal-date-"+time.Now().Format("20060102"))
		return panelTitle(n) == thisMonth && currentDay != nil && strings.Contains(currentDay.Name, "selected")
	})
	tomorrow := time.Now().AddDate(0, 0, 1)
	h.clickUntil("cal-date-"+tomorrow.Format("20060102"), func(n *v1.Node) bool {
		return findID(n, "event-product-review") != nil
	})
	h.clickUntil("event-product-review", func(n *v1.Node) bool {
		return findID(n, "action-join") != nil && findID(n, "action-copy") != nil && strings.Contains(treeText(n), "Product review")
	})
	h.clickUntil("cal-back", func(n *v1.Node) bool { return findID(n, "event-product-review") != nil })
	for _, mode := range []struct {
		id       string
		schedule bool
	}{
		{"week", true}, {"four-days", true}, {"day", true}, {"agenda", false}, {"month", false},
	} {
		h.clickUntil("cal-view-"+mode.id, func(n *v1.Node) bool {
			selected := findID(n, "cal-view-"+mode.id)
			if selected == nil || !selected.Selected {
				return false
			}
			grid := findKindInTree(n, v1.KindScheduleGrid)
			return (grid != nil) == mode.schedule && (grid == nil || grid.Schedule != nil && len(grid.Schedule.Events) >= 2)
		})
	}

	// The bar view shows the day of the month.
	h.mu.Lock()
	h.slots = recordSlot(h.slots, "bar-1", viewSlot{v1.ViewBar, lint.BarWidth, lint.BarHeight})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Width: lint.BarWidth, Height: lint.BarHeight}); err != nil {
		t.Fatal(err)
	}
	h.waitNode("bar-1", func(n *v1.Node) bool {
		return findID(n, "calendar-open") != nil && strings.Contains(treeText(n), "Product review")
	})
	h.mu.Lock()
	h.slots = recordSlot(h.slots, "tooltip-1", viewSlot{v1.ViewTooltip, lint.TooltipWidth, lint.TooltipHeight})
	h.mu.Unlock()
	if err := h.send(&v1.ViewOpen{ViewID: "tooltip-1", View: v1.ViewTooltip, Entry: "tooltip", Output: "DP-1", Width: lint.TooltipWidth, Height: lint.TooltipHeight}); err != nil {
		t.Fatal(err)
	}
	h.waitNode("tooltip-1", func(n *v1.Node) bool { return strings.Contains(treeText(n), "Product review") })
}

func panelTitle(root *v1.Node) string {
	if root == nil || len(root.Children) == 0 || len(root.Children[0].Children) < 2 {
		return ""
	}
	return root.Children[0].Children[1].Text
}

func findKindInTree(root *v1.Node, kind v1.NodeKind) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Kind == kind {
		return root
	}
	for _, child := range root.Children {
		if found := findKindInTree(child, kind); found != nil {
			return found
		}
	}
	return nil
}

type calendarHost struct {
	t     *testing.T
	enc   *v1.Encoder
	mu    sync.Mutex
	roots map[string]*v1.Node
	revs  map[string]uint64
	slots map[string]viewSlot
	wake  chan struct{}
}

func (h *calendarHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *calendarHost) clickUntil(id string, matches func(*v1.Node) bool) *v1.Node {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root, rev := h.roots["panel-1"], h.revs["panel-1"]
		h.mu.Unlock()
		if root != nil && matches(root) {
			return root
		}
		if err := h.send(&v1.InputEvent{ViewID: "panel-1", Revision: rev, Node: id, Event: v1.EventActivate, Output: "DP-1"}); err != nil {
			h.t.Fatal(err)
		}
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			h.mu.Lock()
			root, current := h.roots["panel-1"], h.revs["panel-1"]
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
	root := h.roots["panel-1"]
	h.mu.Unlock()
	h.t.Fatalf("input %q never produced the expected panel tree\n%s", id, dumpTree(root))
	return nil
}

func (h *calendarHost) waitNode(viewID string, ok func(*v1.Node) bool) *v1.Node {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root := h.roots[viewID]
		h.mu.Unlock()
		if root != nil && ok(root) {
			return root
		}
		select {
		case <-h.wake:
		case <-time.After(40 * time.Millisecond):
		}
	}
	h.t.Fatalf("%s tree never matched\n%s", viewID, dumpTree(h.roots[viewID]))
	return nil
}
