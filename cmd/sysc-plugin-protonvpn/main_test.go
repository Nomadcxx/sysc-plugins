package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// fakeCLI records every invocation in $PROTON_ARGS and answers with
// fixtures matching the official protonvpn output formats. A state file
// flips status between connected and disconnected.
const fakeCLI = `#!/bin/sh
# Builtin-only: the harness empties PATH, so external commands (touch, rm)
# would silently fail. ` + "`echo on > file`" + ` and truncation are redirections.
echo "$*" >> "$PROTON_ARGS"
case "$1" in
connect)
	echo on > "$PROTON_ON"
	echo "Your new IP address is 8.8.8.8."
	;;
disconnect)
	: > "$PROTON_ON"
	;;
status)
	if [ -s "$PROTON_ON" ]; then
		printf 'Status: connected\nServer: NL#1 in Netherlands\nLoad: 10%%\nProtocol: wireguard\n'
	else
		echo "Status: disconnected"
	fi
	;;
info)
	echo "Account: 'jane@example.com'"
	;;
config)
	if [ "$2" = "list" ]; then
		printf 'kill-switch               off\nnetshield                 off\nport-forwarding           off\n'
	fi
	;;
esac
exit 0
`

type harness struct {
	t      *testing.T
	host   io.WriteCloser
	hostMu sync.Mutex
	msgs   chan []byte
	calls  chan v1.HostCall
	done   chan error
	env    environment
}

func start(t *testing.T) *harness {
	return startWithSettings(t, `{"features":{"split_tunneling":{"enabled":false,"apps":[]}}}`)
}

func startWithSettings(t *testing.T, settings string) *harness {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "protonvpn")
	if err := os.WriteFile(fake, []byte(fakeCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROTON_ARGS", filepath.Join(dir, "args"))
	t.Setenv("PROTON_ON", filepath.Join(dir, "on"))
	t.Setenv("HOME", dir)
	t.Setenv("XDG_DATA_DIRS", "")
	t.Setenv("PATH", filepath.Join(dir, "empty"))

	env := environment{
		now: time.Now, callTimeout: 2 * time.Second,
		cliBin: fake,
		lookPath: func(name string) (string, error) {
			switch name {
			case fake, "wl-copy":
				return name, nil
			}
			return "", os.ErrNotExist
		},
		serversPath:  filepath.Join(dir, "missing-serverlist.json"),
		settingsPath: filepath.Join(dir, "settings.json"),
	}
	// Split tunneling persists into a recognised settings.json;
	// WriteSplitTunnel refuses to clobber a missing or malformed file.
	if err := os.WriteFile(env.settingsPath, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	in, host := io.Pipe()
	pluginW, output := io.Pipe()
	h := &harness{
		t: t, host: host, env: env,
		msgs:  make(chan []byte, 64),
		calls: make(chan v1.HostCall, 64),
		done:  make(chan error, 1),
	}
	go func() {
		err := runPlugin(in, output, env)
		_ = output.Close()
		h.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(pluginW)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			var call v1.HostCall
			if messageType(line) == v1.TypeHostCall && json.Unmarshal(line, &call) == nil {
				h.reply(call)
				h.calls <- call
				continue
			}
			h.msgs <- line
		}
		close(h.msgs)
	}()

	h.send(map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 7}}, "capabilities": []string{"panels", "settings", "state"}})
	if got := h.next(); messageType(got) != "plugin.hello" {
		t.Fatalf("first message = %s", got)
	}
	t.Cleanup(h.stop)
	return h
}

func (h *harness) reply(call v1.HostCall) {
	result := json.RawMessage(`null`)
	if call.Call == v1.CallStateGet {
		result = json.RawMessage(`{"found":false}`)
	}
	if call.Call == v1.CallPanelOpen {
		result = json.RawMessage(`{"view_id":"p"}`)
	}
	h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: result})
}

func (h *harness) send(message any) {
	h.t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		h.t.Fatal(err)
	}
	h.hostMu.Lock()
	defer h.hostMu.Unlock()
	if _, err := h.host.Write(append(data, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) next() []byte { return h.nextWithin(3 * time.Second) }

func (h *harness) nextWithin(d time.Duration) []byte {
	h.t.Helper()
	select {
	case line, ok := <-h.msgs:
		if !ok {
			h.t.Fatal("plugin output closed")
		}
		return line
	case <-time.After(d):
		h.t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

// snapshotUntil returns the first panel-or-bar snapshot whose root satisfies ok.
func (h *harness) snapshotUntil(ok func(*v1.Node) bool) v1.ViewSnapshot {
	h.t.Helper()
	for {
		line := h.next()
		var s v1.ViewSnapshot
		if messageType(line) == v1.TypeViewSnapshot && json.Unmarshal(line, &s) == nil && ok(s.Root) {
			return s
		}
	}
}

func (h *harness) openPanel() v1.ViewSnapshot {
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel", Width: 460, Height: 580})
	return h.snapshotUntil(func(n *v1.Node) bool { return find(n, "tab:connections") != nil })
}

func (h *harness) openBar() v1.ViewSnapshot {
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar"})
	return h.snapshotUntil(func(n *v1.Node) bool { return find(n, "bar") != nil })
}

func (h *harness) input(viewID string, snap v1.ViewSnapshot, node string, event v1.EventKind, text string, button v1.PointerButton) {
	h.send(v1.InputEvent{Type: "input.event", ViewID: viewID, Revision: snap.Revision, Node: node, Event: event, Text: text, Button: button})
}

// awaitCall drains recorded host calls until one matches.
func (h *harness) awaitCall(what string, ok func(v1.HostCall) bool) v1.HostCall {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case call := <-h.calls:
			if ok(call) {
				return call
			}
		case <-deadline:
			h.t.Fatalf("no host call %s", what)
			return v1.HostCall{}
		}
	}
}

// awaitArgs waits until the fake CLI log contains the given line.
func (h *harness) awaitArgs(want string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(os.Getenv("PROTON_ARGS"))
		if strings.Contains(string(b), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("fake CLI never ran %q", want)
}

func (h *harness) stop() {
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Errorf("runPlugin: %v", err)
		}
	case <-time.After(2 * time.Second):
		h.t.Error("plugin did not stop")
	}
}

func messageType(line []byte) string {
	var m struct{ Type string }
	_ = json.Unmarshal(line, &m)
	return m.Type
}

func find(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if n := find(c, id); n != nil {
			return n
		}
	}
	return nil
}

func contains(root *v1.Node, text string) bool {
	if root == nil {
		return false
	}
	if root.Text == text {
		return true
	}
	for _, c := range root.Children {
		if contains(c, text) {
			return true
		}
	}
	return false
}

func callParamsIs(call v1.HostCall, kind v1.CallKind, pred func(raw json.RawMessage) bool) bool {
	return call.Call == kind && pred(call.Params)
}

func TestTabSwitchPersists(t *testing.T) {
	h := start(t)
	p := h.openPanel()
	h.input("p", p, "tab:protection", v1.EventActivate, "", "")
	set := h.awaitCall("state.set tab", func(c v1.HostCall) bool {
		var params v1.StateSetParams
		return callParamsIs(c, v1.CallStateSet, func(raw json.RawMessage) bool {
			if json.Unmarshal(raw, &params) != nil {
				return false
			}
			return params.Key == "ui" && strings.Contains(string(params.Value), "protection")
		})
	})
	_ = set
	prot := h.snapshotUntil(func(n *v1.Node) bool { return find(n, "st") != nil })
	if !contains(prot.Root, "Split tunneling") {
		t.Fatal("protection tab not rendered after switch")
	}
}

func TestActionButtonWiresCommands(t *testing.T) {
	h := start(t)
	p := h.openPanel()
	h.input("p", p, "action", v1.EventActivate, "", "")
	h.awaitArgs("connect\n")
	h.awaitArgs("status\n")
	connected := h.snapshotUntil(func(n *v1.Node) bool {
		b := find(n, "action")
		return b != nil && b.Text == "Disconnect"
	})
	if !contains(connected.Root, "NL#1") {
		t.Fatalf("connected panel lacks the server: %+v", connected.Root)
	}
}

func TestRightClickQuickConnect(t *testing.T) {
	h := start(t)
	h.send(v1.SettingsChanged{Type: "settings.changed", Scope: v1.ScopePlugin, Values: map[string]any{"quick_connect": "p2p"}})
	b := h.openBar()
	h.input("b", b, "bar", v1.EventPointer, "", v1.ButtonSecondary)
	h.awaitArgs("connect --p2p\n")
	// Back on the bar, a right-click while connected disconnects. Code mode
	// carries the label in a child text node, so scan descendants.
	connected := h.snapshotUntil(func(n *v1.Node) bool {
		bar := find(n, "bar")
		return bar != nil && contains(bar, "NL")
	})
	h.input("b", connected, "bar", v1.EventPointer, "", v1.ButtonSecondary)
	h.awaitArgs("disconnect\n")
}

func TestBarClickOpensPanelOnce(t *testing.T) {
	h := start(t)
	b := h.openBar()
	// The shell delivers a primary click as press (pointer) plus release
	// (activate); only the release may open, or the pair toggles closed.
	h.send(v1.InputEvent{Type: "input.event", ViewID: "b", Revision: b.Revision, Node: "bar", Event: v1.EventPointer, Button: v1.ButtonPrimary})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "b", Revision: b.Revision, Node: "bar", Event: v1.EventActivate, Output: "DP-1", Generation: 3})
	open := h.awaitCall("panel.open", func(c v1.HostCall) bool {
		var params v1.PanelParams
		return callParamsIs(c, v1.CallPanelOpen, func(raw json.RawMessage) bool {
			return json.Unmarshal(raw, &params) == nil && params.Entry == "panel" &&
				params.Output == "DP-1" && params.Generation == 3 && params.Instance == "b"
		})
	})
	_ = open
	select {
	case call := <-h.calls:
		if call.Call == v1.CallPanelOpen {
			t.Fatal("one click sent two panel.open calls")
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSettingsChangeRearms(t *testing.T) {
	h := start(t)
	b := h.openBar()
	bar := find(b.Root, "bar")
	if len(bar.Children) == 0 || bar.Children[0].Icon == "" {
		t.Fatal("bar rendered without its phase icon")
	}
	h.send(v1.SettingsChanged{Type: "settings.changed", Scope: v1.ScopePlugin, Values: map[string]any{"bar_mode": "status"}})
	status := h.snapshotUntil(func(n *v1.Node) bool {
		bar := find(n, "bar")
		return bar != nil && contains(bar, "Unprotected")
	})
	_ = status
}

func TestNotifyOnConnect(t *testing.T) {
	h := start(t)
	p := h.openPanel()
	h.input("p", p, "action", v1.EventActivate, "", "")
	notify := h.awaitCall("connected notify", func(c v1.HostCall) bool {
		var params v1.NotifyParams
		return callParamsIs(c, v1.CallNotify, func(raw json.RawMessage) bool {
			return json.Unmarshal(raw, &params) == nil && params.Summary == "Connected to NL#1"
		})
	})
	_ = notify
}

func TestSplitTunnelEnableNotifies(t *testing.T) {
	h := start(t)
	p := h.openPanel()
	h.input("p", p, "tab:protection", v1.EventActivate, "", "")
	prot := h.snapshotUntil(func(n *v1.Node) bool { return find(n, "st") != nil })
	h.input("p", prot, "st", v1.EventActivate, "", "")
	h.awaitCall("st notify", func(c v1.HostCall) bool {
		var params v1.NotifyParams
		return callParamsIs(c, v1.CallNotify, func(raw json.RawMessage) bool {
			return json.Unmarshal(raw, &params) == nil &&
				params.Summary == "Split tunneling enabled. Remember to restart affected apps."
		})
	})
	b, err := os.ReadFile(h.env.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"enabled":true`) && !strings.Contains(string(b), `"enabled": true`) {
		t.Fatalf("settings.json not enabled: %s", b)
	}
}

func TestSplitTunnelStateRestored(t *testing.T) {
	h := startWithSettings(t, `{"features":{"split_tunneling":{"enabled":true,"apps":["/usr/bin/firefox"]}}}`)
	p := h.openPanel()
	h.input("p", p, "tab:protection", v1.EventActivate, "", "")
	prot := h.snapshotUntil(func(n *v1.Node) bool { return find(n, "st") != nil })
	if st := find(prot.Root, "st"); st == nil || st.Text != "On" {
		t.Fatal("split tunnel toggle not restored to On")
	}
	if !contains(prot.Root, "/usr/bin/firefox") {
		t.Fatal("restored app list not rendered")
	}
}

func TestSplitTunnelToggleRevertsOnWriteError(t *testing.T) {
	h := start(t)
	p := h.openPanel()
	h.input("p", p, "tab:protection", v1.EventActivate, "", "")
	prot := h.snapshotUntil(func(n *v1.Node) bool { return find(n, "st") != nil })
	if err := os.WriteFile(h.env.settingsPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.input("p", prot, "st", v1.EventActivate, "", "")
	h.snapshotUntil(func(n *v1.Node) bool {
		st := find(n, "st")
		return st != nil && st.Text == "Off" && hasTextContaining(n, "protonvpn:")
	})
}

// hasTextContaining reports whether any node's text contains sub (contains
// above is exact-match).
func hasTextContaining(root *v1.Node, sub string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, sub) {
		return true
	}
	for _, c := range root.Children {
		if hasTextContaining(c, sub) {
			return true
		}
	}
	return false
}
