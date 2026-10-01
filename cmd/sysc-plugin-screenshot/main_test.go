package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

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
	h.send(&v1.HostHello{Supported: []v1.Version{{Major: 1, Minor: 10}, {Major: 1, Minor: 9}},
		Plugin:       v1.Identity{ID: "org.sysc.screenshot", Name: "Screenshot", Version: "1.0.0"},
		Capabilities: []string{"panels", "screenshot"}, Limits: v1.DefaultLimits})
	hello, ok := h.next(time.Second).(*v1.PluginHello)
	if !ok {
		t.Fatal("no plugin.hello")
	}
	if hello.Protocol != (v1.Version{Major: 1, Minor: 10}) {
		t.Fatalf("negotiated %+v, want 1.10", hello.Protocol)
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

func hasButton(n *v1.Node, id string) bool {
	if n.Kind == v1.KindButton && n.ID == id {
		return true
	}
	for _, c := range n.Children {
		if hasButton(c, id) {
			return true
		}
	}
	return false
}

func hasText(n *v1.Node, prefix string) bool {
	if n.Kind == v1.KindText && strings.HasPrefix(n.Text, prefix) {
		return true
	}
	for _, c := range n.Children {
		if hasText(c, prefix) {
			return true
		}
	}
	return false
}

func TestBarShowsOneButtonAndItOpensThePanel(t *testing.T) {
	h := startPlugin(t)
	h.send(&v1.ViewOpen{ViewID: "bar1", View: v1.ViewBar, Entry: "bar", Instance: "screenshot-1", Output: "eDP-1", Generation: 4, Height: 32})
	snap, ok := h.next(time.Second).(*v1.ViewSnapshot)
	if !ok {
		t.Fatal("no bar snapshot")
	}
	for _, f := range shelllint.Tree(snap.Root, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
		t.Errorf("bar snapshot: %s", f)
	}
	if !hasButton(snap.Root, "open") {
		t.Fatalf("bar has no open button: %+v", snap.Root)
	}

	h.send(&v1.InputEvent{ViewID: "bar1", Revision: snap.Revision, Node: "open", Event: v1.EventActivate, Output: "eDP-1", Generation: 4})
	call, ok := h.next(time.Second).(*v1.HostCall)
	if !ok || call.Call != v1.CallPanelOpen {
		t.Fatalf("expected a panel.open call, got %#v", call)
	}
	var params v1.PanelParams
	if err := json.Unmarshal(call.Params, &params); err != nil || params.Entry != "panel" || params.Output != "eDP-1" || params.Generation != 4 || params.Instance != "screenshot-1" {
		t.Fatalf("panel.open params = %s (%v)", call.Params, err)
	}
}

func TestPanelReadsTheDirectoryAndDrawsItsCaption(t *testing.T) {
	h := startPlugin(t)
	h.send(&v1.ViewOpen{ViewID: "p1", View: v1.ViewPanel, Entry: "panel", Output: "eDP-1", Width: 360, Height: 260})

	var sawDirectoryCall, sawCaption bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !sawCaption {
		switch m := h.next(200 * time.Millisecond).(type) {
		case *v1.HostCall:
			if m.Call != v1.CallScreenshotDirectory {
				t.Fatalf("unexpected call %s", m.Call)
			}
			sawDirectoryCall = true
			raw, _ := json.Marshal(v1.ScreenshotDirectoryResult{Directory: "/home/u/Pictures/Screenshots"})
			h.send(&v1.HostReply{ID: m.ID, OK: true, Result: raw})
		case *v1.ViewSnapshot:
			for _, f := range shelllint.Tree(m.Root, v1.ViewPanel, 360, 260) {
				t.Errorf("panel snapshot: %s", f)
			}
			if hasText(m.Root, "/home/u/Pictures/Screenshots") {
				sawCaption = true
			}
		}
	}
	if !sawDirectoryCall || !sawCaption {
		t.Fatalf("directory call %v, caption drawn %v; want both", sawDirectoryCall, sawCaption)
	}
}

// The shell opens a tooltip view for every bar widget, and rejects one that
// holds a control. It was found live: the panel tree was answered to it.
func TestTooltipViewGetsReadOnlyText(t *testing.T) {
	h := startPlugin(t)
	h.send(&v1.ViewOpen{ViewID: "t1", View: v1.ViewTooltip, Entry: "bar", Output: "eDP-1", Width: shelllint.TooltipWidth, Height: shelllint.TooltipHeight})
	snap, ok := h.next(time.Second).(*v1.ViewSnapshot)
	if !ok {
		t.Fatal("no tooltip snapshot")
	}
	for _, f := range shelllint.Tree(snap.Root, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
		t.Errorf("tooltip snapshot: %s", f)
	}
	if hasButton(snap.Root, "region") || hasButton(snap.Root, "close") {
		t.Fatalf("a tooltip carries panel controls: %+v", snap.Root)
	}
	if m := h.next(300 * time.Millisecond); m != nil {
		if _, isCall := m.(*v1.HostCall); isCall {
			t.Fatalf("a tooltip view triggered a host call: %#v", m)
		}
	}
}
