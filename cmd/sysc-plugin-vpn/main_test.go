package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestMain(m *testing.M) {
	if os.Getenv("SYSC_VPN_PLUGIN_HELPER") == "1" {
		helperVPN()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helperVPN() {
	dir := os.Getenv("SYSC_VPN_PLUGIN_DIR")
	name := filepath.Base(os.Args[0])
	f, _ := os.OpenFile(filepath.Join(dir, "args"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	_ = json.NewEncoder(f).Encode(append([]string{name}, os.Args[1:]...))
	_ = f.Close()
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	statePath := filepath.Join(dir, name+"-on")
	state, _ := os.ReadFile(statePath)
	switch os.Args[1] {
	case "connect":
		_ = os.WriteFile(statePath, []byte("1"), 0600)
	case "disconnect":
		_ = os.WriteFile(statePath, nil, 0600)
	case "status":
		phase := "disconnected"
		if len(state) > 0 {
			phase = "connected"
		}
		if name == "obscura" {
			fmt.Printf(`{"vpnStatus":{"%s":{"exit":{"id":"NYC-001","city_name":"New York","country_code":"US"},"relay":{"id":"entry-1"}}},"autoConnect":true,"localNetworkAccess":false,"useSystemDns":false,"dnsContentBlock":{"ad":false,"tracker":false,"malware":false,"adult":false,"gambling":false,"socialMedia":false}}`, phase)
		} else {
			fmt.Printf(`{"state":"%s","details":{"locked_down":false,"location":{"city":"Stockholm","hostname":"se-sto-wg-001"}}}`, phase)
		}
	case "relay":
		if len(os.Args) > 2 && os.Args[2] == "list" {
			fmt.Print("Sweden (se)\n\tStockholm (sto) @ x\n\t\tse-sto-wg-001 (192.0.2.1) - hosted by Test (rented)\n")
		} else if len(os.Args) > 2 && os.Args[2] == "get" {
			fmt.Print("Generic constraints\nLocation: any\n")
		}
	case "auto-connect":
		fmt.Print("Autoconnect: off\n")
	case "lan":
		fmt.Print("Local network sharing setting: block\n")
	case "lockdown-mode":
		fmt.Print("Block traffic when the VPN is disconnected: off\n")
	case "dns":
		fmt.Print("Custom DNS: no\nBlock ads: true\nBlock trackers: false\nBlock malware: false\nBlock adult content: false\nBlock gambling: true\nBlock social media: false\n")
	}
}

type harness struct {
	t         *testing.T
	input     io.WriteCloser
	mu        sync.Mutex
	snapshots chan v1.ViewSnapshot
	calls     chan v1.HostCall
	done      chan error
	current   v1.ViewSnapshot
	dir       string
}

func startPlugin(t *testing.T, installed []string, saved string, saveOK bool) *harness {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, name := range installed {
		paths[name] = filepath.Join(dir, name)
		if err := os.Symlink(exe, paths[name]); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SYSC_VPN_PLUGIN_HELPER", "1")
	t.Setenv("SYSC_VPN_PLUGIN_DIR", dir)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	reader, input := io.Pipe()
	output, sink := io.Pipe()
	h := &harness{t: t, input: input, snapshots: make(chan v1.ViewSnapshot, 128), calls: make(chan v1.HostCall, 64), done: make(chan error, 1), dir: dir}
	env := environment{now: time.Now, callTimeout: time.Second, lookPath: func(name string) (string, error) {
		if path := paths[name]; path != "" {
			return path, nil
		}
		return "", os.ErrNotExist
	}}
	go func() { err := runPlugin(reader, sink, env); _ = sink.Close(); h.done <- err }()
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			raw := append([]byte(nil), scanner.Bytes()...)
			var message struct{ Type string }
			_ = json.Unmarshal(raw, &message)
			switch message.Type {
			case "host.call":
				var call v1.HostCall
				_ = json.Unmarshal(raw, &call)
				result := json.RawMessage(`null`)
				ok := true
				if call.Call == v1.CallStateGet {
					if saved == "" {
						result = json.RawMessage(`{"found":false}`)
					} else {
						value, _ := json.Marshal(saved)
						result = json.RawMessage(`{"found":true,"value":` + string(value) + `}`)
					}
				}
				if call.Call == v1.CallStateSet {
					ok = saveOK
				}
				h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: ok, Result: result})
				h.calls <- call
			case "view.snapshot":
				var snap v1.ViewSnapshot
				_ = json.Unmarshal(raw, &snap)
				h.snapshots <- snap
			}
		}
	}()
	h.send(map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 8}}, "capabilities": []string{"panels", "state", "settings"}})
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "panel", View: v1.ViewPanel, Entry: "panel", Width: 460, Height: 580})
	t.Cleanup(func() {
		h.send(v1.HostShutdown{Type: "host.shutdown"})
		_ = input.Close()
		select {
		case err := <-h.done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("shutdown hung")
		}
		_ = output.Close()
	})
	return h
}

func (h *harness) send(value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		h.t.Error(err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.input.Write(append(raw, '\n')); err != nil && err != io.ErrClosedPipe {
		h.t.Error(err)
	}
}

func node(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if found := node(c, id); found != nil {
			return found
		}
	}
	return nil
}

func hasText(root *v1.Node, want string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, want) {
		return true
	}
	for _, c := range root.Children {
		if hasText(c, want) {
			return true
		}
	}
	return false
}

func (h *harness) await(predicate func(*v1.Node) bool) {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case snap := <-h.snapshots:
			if snap.ViewID != "panel" {
				continue
			}
			h.current = snap
			if predicate(snap.Root) {
				return
			}
		case <-deadline:
			h.t.Fatalf("expected panel not received; action=%+v commands=%s", node(h.current.Root, "action"), h.commands())
		}
	}
}

func (h *harness) activate(id string) {
	h.send(v1.InputEvent{Type: "input.event", ViewID: "panel", Revision: h.current.Revision, Node: id, Event: v1.EventActivate})
}

// click activates an idempotent control (provider choice, page navigation)
// until predicate holds. The plugin drops input aimed at a revision it has
// replaced, which a background read can do between a wait and a click.
func (h *harness) click(id string, predicate func(*v1.Node) bool) {
	h.t.Helper()
	h.activate(id)
	deadline := time.After(5 * time.Second)
	retry := time.NewTicker(250 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case snap := <-h.snapshots:
			if snap.ViewID != "panel" {
				continue
			}
			h.current = snap
			if predicate(snap.Root) {
				return
			}
		case <-retry.C:
			h.activate(id)
		case <-deadline:
			h.t.Fatalf("%s did not take effect; commands=%s", id, h.commands())
		}
	}
}

func (h *harness) commands() string {
	raw, _ := os.ReadFile(filepath.Join(h.dir, "args"))
	return string(raw)
}

func TestPluginMissingCLIAndSavedChoice(t *testing.T) {
	h := startPlugin(t, nil, "obscura", false)
	h.await(func(root *v1.Node) bool { n := node(root, "provider:obscura"); return n != nil && n.Selected })
	if node(h.current.Root, "action") != nil {
		t.Fatal("missing CLI offers an action")
	}
	h.activate("provider:mullvad")
	h.await(func(root *v1.Node) bool { return hasText(root, "could not be saved") })
	if h.commands() != "" {
		t.Fatal("CLI invocation without installation")
	}
}

func TestPluginConnectDisconnectAndStaleEvents(t *testing.T) {
	h := startPlugin(t, []string{"mullvad"}, "", true)
	// Wait for the asynchronous catalogue as well as status. Its initial
	// snapshot legitimately invalidates an input aimed at the earlier tree.
	h.await(func(root *v1.Node) bool { n := node(root, "action"); return n != nil && !n.Disabled })
	// The location page needs the asynchronous catalogue; back returns home.
	h.click("open:locations", func(root *v1.Node) bool { return node(root, "browse:se::") != nil })
	h.click("back", func(root *v1.Node) bool { n := node(root, "action"); return n != nil && !n.Disabled })
	stale := h.current.Revision
	h.activate("action")
	h.await(func(root *v1.Node) bool {
		n := node(root, "action")
		return n != nil && n.Text == "Disconnect" && !n.Disabled
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "panel", Revision: stale, Node: "action", Event: v1.EventActivate})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "closed", Revision: h.current.Revision, Node: "action", Event: v1.EventActivate})
	h.click("open:dns", func(root *v1.Node) bool { n := node(root, "set:dns:0"); return n != nil && !n.Disabled })
	// A page change synchronizes the loop after the forged events without a
	// delay: it always publishes, where a refresh of unchanged state does not.
	h.click("back", func(root *v1.Node) bool { n := node(root, "set:lan"); return n != nil && !n.Disabled })
	if got := h.commands(); strings.Count(got, `["mullvad","connect"]`) != 1 || strings.Contains(got, `["mullvad","disconnect"]`) {
		t.Fatalf("%s", got)
	}
	h.activate("action")
	h.await(func(root *v1.Node) bool {
		n := node(root, "action")
		return n != nil && n.Text == "Connect" && !n.Disabled
	})
}

func TestPluginProviderSelectionDoesNotSwitchVPNs(t *testing.T) {
	h := startPlugin(t, []string{"mullvad", "obscura"}, "obscura", true)
	h.await(func(root *v1.Node) bool { n := node(root, "action"); return n != nil && !n.Disabled })
	h.click("provider:mullvad", func(root *v1.Node) bool { n := node(root, "provider:mullvad"); return n != nil && n.Selected })
	h.click("provider:obscura", func(root *v1.Node) bool { n := node(root, "provider:obscura"); return n != nil && n.Selected })
	// The capability probe reads `obscura connect --help`; only a command
	// without --help changes anything.
	for _, line := range strings.Split(h.commands(), "\n") {
		if strings.Contains(line, `"--help"`) {
			continue
		}
		for _, word := range []string{`"connect"`, `"disconnect"`, `"set"`} {
			if strings.Contains(line, word) {
				t.Fatalf("selection mutated VPN: %s", line)
			}
		}
	}
}

func TestPluginLocationReturnsHomeAndConfirmFlow(t *testing.T) {
	h := startPlugin(t, []string{"mullvad"}, "", true)
	h.await(func(root *v1.Node) bool { n := node(root, "action"); return n != nil && !n.Disabled })
	h.click("open:locations", func(root *v1.Node) bool { n := node(root, "location:se::"); return n != nil && !n.Disabled })
	h.activate("location:se::")
	h.await(func(root *v1.Node) bool { return node(root, "search") == nil && node(root, "action") != nil })
	h.await(func(root *v1.Node) bool { n := node(root, "set:lan"); return n != nil && !n.Disabled })
	// Allowing LAN access asks first; cancel returns home without a command.
	h.activate("set:lan")
	h.await(func(root *v1.Node) bool { return node(root, "confirm") != nil })
	h.activate("cancel")
	h.await(func(root *v1.Node) bool { return node(root, "confirm") == nil && node(root, "set:lan") != nil })
	if strings.Contains(h.commands(), `"lan","set"`) {
		t.Fatalf("cancelled confirm ran a command: %s", h.commands())
	}
	if !strings.Contains(h.commands(), `"relay","set"`) {
		t.Fatalf("location choice did not reach the CLI: %s", h.commands())
	}
}
