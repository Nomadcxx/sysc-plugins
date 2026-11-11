package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// repoRoot is the sysc-plugins checkout this test file lives in.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
}

func TestManifestPassesValidatorAndDeclaresServiceActions(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "run", "./tools/validate-manifests")
	cmd.Dir = root
	out, _ := cmd.CombinedOutput() // other plugins' results do not decide this test
	const ok = "ok   plugins/hermes/manifest.json"
	if !strings.Contains(string(out), ok) {
		t.Fatalf("validate-manifests did not accept plugins/hermes/manifest.json:\n%s", out)
	}

	data, err := os.ReadFile(filepath.Join(root, "plugins/hermes/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID       string     `json:"id"`
		Name     string     `json:"name"`
		Version  string     `json:"version"`
		Exec     string     `json:"exec"`
		Protocol v1.Version `json:"protocol"`
		Services []struct {
			ID string `json:"id"`
		} `json:"services"`
		Settings []struct {
			Key     string          `json:"key"`
			Default json.RawMessage `json:"default"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ID != pluginID || manifest.Name != pluginName || manifest.Version != pluginVersion || manifest.Exec != "bin/sysc-plugin-hermes" {
		t.Fatalf("manifest identity = %s %q %s %s, does not match the plugin handshake", manifest.ID, manifest.Name, manifest.Version, manifest.Exec)
	}
	if manifest.Protocol.Major != v1.ProtocolMajor || manifest.Protocol.Minor < v1.ServiceActionMinor {
		t.Fatalf("manifest protocol = %d.%d, want %d.>=%d", manifest.Protocol.Major, manifest.Protocol.Minor, v1.ProtocolMajor, v1.ServiceActionMinor)
	}
	if len(manifest.Services) != 1 || manifest.Services[0].ID != serviceID {
		t.Fatalf("manifest services = %+v, want one %q service", manifest.Services, serviceID)
	}
	if len(manifest.Settings) == 0 || manifest.Settings[0].Key != "client_only_mode" || string(manifest.Settings[0].Default) != "false" {
		t.Fatalf("first setting must be client_only_mode defaulting to false")
	}

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makefile), "sysc-plugin-hermes:hermes") {
		t.Fatal("Makefile PLUGINS does not list sysc-plugin-hermes:hermes")
	}
}

// testHost drives run over pipes the way the shell drives the binary.
type testHost struct {
	t        *testing.T
	mu       sync.Mutex
	enc      *v1.Encoder
	msgs     chan v1.Message
	done     chan error
	fromPlug *io.PipeReader
}

func startPlugin(t *testing.T) (*testHost, *v1.PluginHello) {
	t.Helper()
	pluginIn, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	h := &testHost{t: t, enc: v1.NewEncoder(hostOut), msgs: make(chan v1.Message, 128), done: make(chan error, 1), fromPlug: hostIn}
	go func() {
		h.done <- run(pluginIn, pluginOut)
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
		Plugin:       v1.Identity{ID: pluginID, Name: pluginName, Version: pluginVersion},
		Capabilities: []string{"notifications", "panels", "settings", "floating_surfaces"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	hello := h.wait(func(m v1.Message) bool {
		_, ok := m.(*v1.PluginHello)
		return ok
	}).(*v1.PluginHello)
	return h, hello
}

func (h *testHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *testHost) read() {
	dec := v1.NewDecoder(h.fromPlug, v1.ToHost)
	for {
		msg, err := dec.Decode()
		if err != nil {
			return
		}
		if call, ok := msg.(*v1.HostCall); ok {
			_ = h.send(&v1.HostReply{ID: call.ID, OK: true})
		}
		select {
		case h.msgs <- msg:
		default:
		}
	}
}

func (h *testHost) wait(accept func(v1.Message) bool) v1.Message {
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

func treeText(n *v1.Node) string {
	if n == nil {
		return ""
	}
	parts := []string{n.Text}
	for _, c := range n.Children {
		parts = append(parts, treeText(c))
	}
	return strings.Join(parts, " ")
}

func TestHandshakeIdentifiesThePlugin(t *testing.T) {
	_, hello := startPlugin(t)
	if hello.Plugin.ID != pluginID || hello.Plugin.Name != pluginName || hello.Plugin.Version != pluginVersion {
		t.Fatalf("plugin hello identity = %+v", hello.Plugin)
	}
	if hello.Protocol.Major != v1.ProtocolMajor {
		t.Fatalf("plugin hello protocol = %+v", hello.Protocol)
	}
}

// Every view the manifest can open, with the size the host grants it.
var openings = []v1.ViewOpen{
	{ViewID: "bar-1", View: v1.ViewBar, Entry: "bar", Width: 240, Height: 32},
	{ViewID: "tip-1", View: v1.ViewTooltip, Entry: "bar", Width: 280, Height: 200},
	{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720},
	{ViewID: "summary-1", View: v1.ViewPanel, Entry: "summary", Width: 380, Height: 480},
	{ViewID: "menu-1", View: v1.ViewPanel, Entry: "menu", Width: 260, Height: 300},
	{ViewID: "settings-1", View: v1.ViewPanel, Entry: "settings", Width: 640, Height: 720},
	{ViewID: "pinned-1", View: v1.ViewFloating, Entry: "pinned", Width: 640, Height: 720},
}

func TestEveryViewKindGetsAConnectingSnapshot(t *testing.T) {
	h, _ := startPlugin(t)
	for _, open := range openings {
		open := open
		if err := h.send(&open); err != nil {
			t.Fatal(err)
		}
		snap := h.wait(func(m v1.Message) bool {
			s, ok := m.(*v1.ViewSnapshot)
			return ok && s.ViewID == open.ViewID
		}).(*v1.ViewSnapshot)
		if snap.Revision == 0 {
			t.Errorf("%s: snapshot revision is 0", open.ViewID)
		}
		if err := v1.Validate(snap.Root, open.View); err != nil {
			t.Errorf("%s: %v", open.ViewID, err)
		}
		text := treeText(snap.Root)
		if open.View == v1.ViewBar && snap.Root != nil && strings.TrimSpace(text) == "" {
			// A compact bar may be icon-only; at 240 it must say so in words.
			t.Errorf("%s: bar shows no label", open.ViewID)
		}
		if !strings.Contains(text, "Connecting…") {
			t.Errorf("%s: snapshot text %q does not say Connecting…", open.ViewID, text)
		}
	}
}

func TestResyncAndResizeRepublish(t *testing.T) {
	h, _ := startPlugin(t)
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Width: 780, Height: 720}); err != nil {
		t.Fatal(err)
	}
	first := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "panel-1"
	}).(*v1.ViewSnapshot)
	if err := h.send(&v1.ViewResize{ViewID: "panel-1", Width: 640, Height: 520}); err != nil {
		t.Fatal(err)
	}
	resized := h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "panel-1"
	}).(*v1.ViewSnapshot)
	if resized.Revision <= first.Revision || resized.Root.Width != 640 || resized.Root.Height != 520 {
		t.Fatalf("resize snapshot rev %d size %dx%d, want rev > %d at 640x520", resized.Revision, resized.Root.Width, resized.Root.Height, first.Revision)
	}
	if err := h.send(&v1.ViewResync{ViewID: "panel-1"}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ViewSnapshot)
		return ok && s.ViewID == "panel-1"
	})
}

// The connecting trees lay out at each declared size and at the fitted-down
// size the shell may grant.
func TestConnectingTreesLayOutAtDeclaredAndFittedSizes(t *testing.T) {
	for _, tc := range []struct {
		view  v1.ViewKind
		entry string
		w, h  int
	}{
		{v1.ViewBar, "bar", 240, 32}, {v1.ViewBar, "bar", 120, 32}, {v1.ViewBar, "bar", 32, 32},
		{v1.ViewTooltip, "bar", 280, 200},
		{v1.ViewPanel, "panel", 780, 720}, {v1.ViewPanel, "panel", 640, 520},
		{v1.ViewPanel, "summary", 380, 480}, {v1.ViewPanel, "summary", 320, 400},
		{v1.ViewPanel, "menu", 260, 300},
		{v1.ViewPanel, "settings", 640, 720}, {v1.ViewPanel, "settings", 560, 520},
		{v1.ViewFloating, "pinned", 640, 720}, {v1.ViewFloating, "pinned", 400, 480},
	} {
		tree := connectingTree(tc.view, tc.entry, tc.w, tc.h)
		if err := v1.Validate(tree, tc.view); err != nil {
			t.Errorf("%s %s %dx%d: %v", tc.view, tc.entry, tc.w, tc.h, err)
		}
		for _, f := range shelllint.Tree(tree, tc.view, tc.w, tc.h) {
			t.Errorf("%s %s %dx%d: %s", tc.view, tc.entry, tc.w, tc.h, f)
		}
	}
}
