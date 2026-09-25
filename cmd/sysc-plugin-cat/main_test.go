package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/cat"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// host drives run over pipes the way the shell's supervisor does.
type host struct {
	t    *testing.T
	enc  *v1.Encoder
	msgs chan v1.Message
	done chan error
}

func startPlugin(t *testing.T) *host {
	t.Helper()
	toPlugin, hostOut := io.Pipe()
	hostIn, fromPlugin := io.Pipe()
	h := &host{t: t, enc: v1.NewEncoder(hostOut), msgs: make(chan v1.Message, 256), done: make(chan error, 1)}
	go func() {
		h.done <- run(toPlugin, fromPlugin)
		fromPlugin.Close()
	}()
	go func() {
		dec := v1.NewDecoder(hostIn, v1.ToHost)
		for {
			msg, err := dec.Decode()
			if err != nil {
				close(h.msgs)
				return
			}
			h.msgs <- msg
		}
	}()
	t.Cleanup(func() {
		_ = h.enc.Encode(&v1.HostShutdown{})
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
			t.Error("plugin did not exit on shutdown")
		}
		hostOut.Close()
	})
	h.send(&v1.HostHello{Supported: []v1.Version{{Major: 1, Minor: 8}},
		Plugin:       v1.Identity{ID: "org.sysc.cat", Name: "Cat", Version: "1.0.0"},
		Capabilities: []string{"panels", "settings"}, Limits: v1.DefaultLimits})
	if _, ok := h.next(time.Second).(*v1.PluginHello); !ok {
		t.Fatal("no plugin.hello")
	}
	return h
}

func (h *host) send(m v1.Message) {
	h.t.Helper()
	if err := h.enc.Encode(m); err != nil {
		h.t.Fatalf("send: %v", err)
	}
}

func (h *host) next(within time.Duration) v1.Message {
	h.t.Helper()
	select {
	case m, ok := <-h.msgs:
		if !ok {
			h.t.Fatal("plugin closed its output")
		}
		return m
	case <-time.After(within):
		return nil
	}
}

// busy keeps the cat moving at an idle machine's load, and a slow sample
// period leaves a long window in which nothing should be sent.
func busy() *v1.SettingsChanged {
	return &v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{
		"sleep_below": 0.0, "top_speed_at": 10.0, "sample_seconds": 10.0}}
}

// The plugin never animates a pose: the bar gets a snapshot carrying the
// sprite, and then silence until something changes. The host steps through
// the poses on its own clock.
func TestBarGetsASpriteAndNoPerPoseTraffic(t *testing.T) {
	h := startPlugin(t)
	h.send(busy())
	h.send(&v1.ViewOpen{ViewID: "v1", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Height: 32})

	var last *v1.ViewSnapshot
	var rev uint64
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m := h.next(200 * time.Millisecond)
		if m == nil {
			continue
		}
		snap, ok := m.(*v1.ViewSnapshot)
		if !ok {
			t.Fatalf("%T sent to a bar; want snapshots only", m)
		}
		if snap.Revision != rev+1 {
			t.Fatalf("snapshot rev %d after %d", snap.Revision, rev)
		}
		for _, f := range shelllint.Tree(snap.Root, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
			t.Errorf("bar snapshot: %s", f)
		}
		last, rev = snap, snap.Revision
	}
	if last == nil {
		t.Fatal("no snapshot")
	}
	cat := last.Root.Children[0].Children[0]
	if cat.Key != "cat" || cat.Kind != v1.KindIcon || len(cat.Frames) < 2 || cat.CycleMS == 0 {
		t.Fatalf("bar cat = %+v, want a sprite", cat)
	}
	if !strings.HasPrefix(cat.Frames[0], "cat-walk-") && !strings.HasPrefix(cat.Frames[0], "cat-run-") {
		t.Fatalf("a busy cat shows %s", cat.Frames[0])
	}

	// Past the first reading and its easing, nothing is due for a sample
	// period: the line stays quiet.
	if m := h.next(1500 * time.Millisecond); m != nil {
		t.Fatalf("%T sent between samples", m)
	}

	h.send(&v1.ViewClose{ViewID: "v1"})
	if m := h.next(600 * time.Millisecond); m != nil {
		t.Fatalf("%T arrived after the last view closed", m)
	}
}

func TestPanelAndTooltipSnapshotsLayOut(t *testing.T) {
	h := startPlugin(t)
	h.send(&v1.ViewOpen{ViewID: "p", View: v1.ViewPanel, Entry: "panel", Width: cat.PanelWidth, Height: cat.PanelHeight})
	h.send(&v1.ViewOpen{ViewID: "t", View: v1.ViewTooltip, Entry: "bar"})
	seen := map[string]bool{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(seen) < 2 {
		m, ok := h.next(time.Second).(*v1.ViewSnapshot)
		if !ok {
			continue
		}
		switch m.ViewID {
		case "p":
			for _, f := range shelllint.Tree(m.Root, v1.ViewPanel, cat.PanelWidth, cat.PanelHeight) {
				t.Errorf("panel: %s", f)
			}
		case "t":
			for _, f := range shelllint.Tree(m.Root, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
				t.Errorf("tooltip: %s", f)
			}
		}
		seen[m.ViewID] = true
	}
	if !seen["p"] || !seen["t"] {
		t.Fatalf("snapshots seen = %v", seen)
	}
}

// A resync after a dropped patch is answered with a fresh snapshot at the
// next revision.
func TestResyncIsAnsweredWithASnapshot(t *testing.T) {
	h := startPlugin(t)
	h.send(&v1.ViewOpen{ViewID: "v1", View: v1.ViewBar, Entry: "bar", Height: 32})
	if _, ok := h.next(time.Second).(*v1.ViewSnapshot); !ok {
		t.Fatal("no first snapshot")
	}
	h.send(&v1.ViewResync{ViewID: "v1"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m, ok := h.next(time.Second).(*v1.ViewSnapshot); ok && m.ViewID == "v1" {
			return
		}
	}
	t.Fatal("resync went unanswered")
}
