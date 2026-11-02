package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/lid-guard"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type lidGuardHost struct {
	t        *testing.T
	mu       sync.Mutex
	enc      *v1.Encoder
	msgs     chan v1.Message
	done     chan error
	fromPlug *io.PipeReader
}

func startLidGuardPlugin(t *testing.T, opts lidguard.Options) *lidGuardHost {
	t.Helper()
	pluginIn, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	h := &lidGuardHost{t: t, enc: v1.NewEncoder(hostOut), msgs: make(chan v1.Message, 128), done: make(chan error, 1), fromPlug: hostIn}
	go func() {
		h.done <- run(pluginIn, pluginOut, opts)
		_ = pluginOut.Close()
	}()
	go h.read()
	t.Cleanup(func() {
		_ = h.send(&v1.HostShutdown{})
		select {
		case err := <-h.done:
			if err != nil {
				t.Errorf("plugin exit: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("plugin did not stop after host.shutdown")
		}
		_ = hostOut.Close()
		_ = hostIn.Close()
		_ = pluginIn.Close()
	})
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: v1.ProtocolMajor, Minor: v1.ProtocolMinor}},
		Plugin:       v1.Identity{ID: "org.sysc.lid-guard", Name: "Lid Guard", Version: "1.0.0"},
		Capabilities: []string{"notifications", "panels"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		_, ok := m.(*v1.PluginHello)
		return ok
	})
	return h
}

func (h *lidGuardHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *lidGuardHost) read() {
	dec := v1.NewDecoder(h.fromPlug, v1.ToHost)
	for {
		msg, err := dec.Decode()
		if err != nil {
			return
		}
		if call, ok := msg.(*v1.HostCall); ok {
			select {
			case h.msgs <- msg:
			default:
			}
			_ = h.send(&v1.HostReply{ID: call.ID, OK: true})
			continue
		}
		select {
		case h.msgs <- msg:
		default:
		}
	}
}

func (h *lidGuardHost) wait(accept func(v1.Message) bool) v1.Message {
	h.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-h.msgs:
			if accept(msg) {
				return msg
			}
		case <-deadline:
			h.t.Fatal("timed out waiting for plugin message")
		}
	}
}

func TestPluginActionsUpdateViewsAndReportObservedServiceState(t *testing.T) {
	var mu sync.Mutex
	active := false
	opts := lidguard.Options{
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			mu.Lock()
			defer mu.Unlock()
			base := filepath.Base(name)
			if base == "systemctl" && len(args) > 1 && args[1] == "is-active" {
				if active {
					return "active\n", "", nil
				}
				return "inactive\n", "", nil
			}
			if base == "systemd-run" {
				active = true
			}
			if base == "systemctl" && len(args) > 1 && args[1] == "stop" {
				active = false
			}
			return "", "", nil
		},
	}
	h := startLidGuardPlugin(t, opts)
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Width: 240}); err != nil {
		t.Fatal(err)
	}
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: lidguard.PanelWidth, Height: lidguard.PanelHeight}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateInactive && !s.Busy
	})
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "bar-1" && find(s.Root, "toggle") != nil
	})
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "panel-1" && strings.Contains(nodeText(s.Root), "Lid Guard")
	})

	if err := h.send(&v1.ServiceAction{ServiceID: "lid-guard", ActionID: "enable"}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateActive && !s.Busy
	})
	activePanel := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		n := findSnapshotNode(s, "panel-1", "panel-action")
		return ok && n != nil && n.Text == "Disable"
	}).(*v1.ViewSnapshot)
	if err := h.send(&v1.InputEvent{ViewID: "panel-1", Revision: activePanel.Revision, Node: "panel-action", Event: v1.EventActivate}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateInactive && !s.Busy && s.Revision > 1
	})
	bar := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		n := findSnapshotNode(s, "bar-1", "toggle")
		return ok && n != nil && n.Text == "Off"
	}).(*v1.ViewSnapshot)
	if err := h.send(&v1.InputEvent{ViewID: "bar-1", Revision: bar.Revision, Node: "toggle", Event: v1.EventPointer, Button: v1.ButtonMiddle}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		call, ok := m.(*v1.HostCall)
		if !ok || call.Call != v1.CallPanelOpen {
			return false
		}
		var params v1.PanelParams
		return json.Unmarshal(call.Params, &params) == nil && params.Entry == "panel"
	})
}

func TestReadOnlyRefreshDoesNotPublishTransientBusyOrRedrawViews(t *testing.T) {
	entered := make(chan int, 2)
	release := make(chan struct{}, 2)
	t.Cleanup(func() {
		for i := 0; i < 2; i++ {
			select {
			case release <- struct{}{}:
			default:
				return
			}
		}
	})
	var mu sync.Mutex
	checks := 0
	opts := lidguard.Options{
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "is-active" {
				mu.Lock()
				checks++
				current := checks
				mu.Unlock()
				if current == 2 || current == 3 {
					entered <- current
					<-release
				}
				return "inactive\n", "", nil
			}
			return "", "", nil
		},
	}
	h := startLidGuardPlugin(t, opts)
	if err := h.send(&v1.ViewOpen{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Width: 240}); err != nil {
		t.Fatal(err)
	}
	initial := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateInactive && !s.Busy
	}).(*v1.ServiceStatus)
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "bar-1"
	})
	if err := h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "refresh"}); err != nil {
		t.Fatal(err)
	}
	waitForRefresh := func(want int) {
		t.Helper()
		select {
		case got := <-entered:
			if got != want {
				t.Fatalf("refresh check number = %d, want %d", got, want)
			}
		case <-time.After(7 * time.Second):
			t.Fatalf("timed out waiting for refresh check %d", want)
		}
	}
	waitForRefresh(2)
	select {
	case msg := <-h.msgs:
		t.Fatalf("read-only refresh published a transient update: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
	release <- struct{}{}
	updated := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateInactive && !s.Busy && s.Revision > initial.Revision
	}).(*v1.ServiceStatus)
	select {
	case msg := <-h.msgs:
		t.Fatalf("unchanged manual refresh emitted another message: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}

	waitForRefresh(3)
	select {
	case msg := <-h.msgs:
		t.Fatalf("periodic refresh published a transient update: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
	release <- struct{}{}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateInactive && !s.Busy && s.Revision > updated.Revision
	})
	select {
	case msg := <-h.msgs:
		t.Fatalf("unchanged periodic refresh emitted another message: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestManifestMatchesPluginIdentityAndOperatorDocs(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(data)
	}
	var manifest struct {
		ID       string     `json:"id"`
		Name     string     `json:"name"`
		Version  string     `json:"version"`
		Exec     string     `json:"exec"`
		Protocol v1.Version `json:"protocol"`
		Services []struct {
			ID      string   `json:"id"`
			Actions []string `json:"actions"`
			Quick   struct {
				Label  string `json:"label"`
				Icon   string `json:"icon"`
				Action string `json:"action"`
			} `json:"quick_action"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(read("plugins/lid-guard/manifest.json")), &manifest); err != nil {
		t.Fatalf("decode Lid Guard manifest: %v", err)
	}
	if manifest.ID != pluginID || manifest.Name != "Lid Guard" || manifest.Version != "1.0.0" || manifest.Exec != "bin/sysc-plugin-lid-guard" {
		t.Fatalf("manifest identity = %+v, does not match the plugin handshake", manifest)
	}
	if manifest.Protocol.Major != v1.ProtocolMajor || manifest.Protocol.Minor < v1.ServiceActionMinor || len(manifest.Services) != 1 {
		t.Fatalf("manifest protocol/services = %d.%d / %d, want service actions and one service", manifest.Protocol.Major, manifest.Protocol.Minor, len(manifest.Services))
	}
	service := manifest.Services[0]
	if service.ID != serviceID || strings.Join(service.Actions, ",") != "enable,disable,toggle,refresh,status" ||
		service.Quick.Label != "Lid Guard" || service.Quick.Icon != "laptop" || service.Quick.Action != "toggle" {
		t.Fatalf("manifest service declaration = %+v, want complete Lid Guard actions and quick action", service)
	}
	for _, check := range []struct{ name, want string }{
		{"Makefile", "sysc-plugin-lid-guard:lid-guard"},
		{"README.md", "**Lid Guard**"},
		{"ATTRIBUTION.md", "noctalia-dev/community-plugins/tree/main/lid-guard"},
		{"plugins/lid-guard/README.md", "systemctl --user stop noctalia-lid-guard.service"},
		{"plugins/lid-guard/README.md", "handle-lid-switch"},
		{"plugins/lid-guard/README.md", "sysc-shell ipc plugin.action"},
		{"docs/writing-plugins.md", "service.status"},
	} {
		if !strings.Contains(read(check.name), check.want) {
			t.Errorf("%s does not document %q", check.name, check.want)
		}
	}
}

func find(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if got := find(child, id); got != nil {
			return got
		}
	}
	return nil
}

func nodeText(root *v1.Node) string {
	if root == nil {
		return ""
	}
	text := root.Text + " " + root.Name
	for _, child := range root.Children {
		text += " " + nodeText(child)
	}
	return text
}

func findSnapshotNode(message *v1.ViewSnapshot, viewID, id string) *v1.Node {
	if message == nil || message.ViewID != viewID {
		return nil
	}
	return find(message.Root, id)
}
