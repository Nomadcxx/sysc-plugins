package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/wire"
)

func TestPluginNotesGateCreateEditAutosaveRenamePinDeleteReopen(t *testing.T) {
	h := startNotes(t)
	h.openPanel()
	h.click("new")
	h.waitNode(func(n *v1.Node) bool { return findID(n, "body") != nil })
	title := nodeText(h.lastRoot(), "title")
	if title == "" {
		t.Fatal("create left no title")
	}
	h.change("body", "c")
	h.change("body", "ca")
	h.change("body", "café")
	name := title + ".md"
	waitFile(t, filepath.Join(h.notes, name), "café")

	h.change("title", "kept")
	h.submit("title", "kept")
	waitFile(t, filepath.Join(h.notes, "kept.md"), "café")

	h.click("back")
	h.waitNode(func(n *v1.Node) bool { return findID(n, "pin:kept.md") != nil })
	h.click("pin:kept.md")
	waitPinned(t, h.notes, "kept.md")

	h.click("open:kept.md")
	h.waitNode(func(n *v1.Node) bool { return nodeText(n, "body") == "café" })

	h.click("back")
	h.click("rm:kept.md")
	h.click("confirm-delete")
	h.waitNode(func(n *v1.Node) bool { return findID(n, "new") != nil && findID(n, "open:kept.md") == nil })
	if _, err := os.Stat(filepath.Join(h.notes, "kept.md")); !os.IsNotExist(err) {
		t.Fatalf("delete left the file: %v", err)
	}
}

func TestPluginNotesGateExternalChangeAndReadOnly(t *testing.T) {
	h := startNotes(t)
	h.openPanel()
	h.click("new")
	h.waitNode(func(n *v1.Node) bool { return findID(n, "body") != nil })
	title := nodeText(h.lastRoot(), "title")
	name := title + ".md"
	path := filepath.Join(h.notes, name)
	h.change("body", "local")
	waitFile(t, path, "local")

	if err := os.WriteFile(path, []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.waitNode(func(n *v1.Node) bool { return nodeText(n, "body") == "disk" })

	h.change("body", "typed")
	if err := os.WriteFile(path, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.waitNode(func(n *v1.Node) bool {
		return findID(n, "reload") != nil && nodeText(n, "body") == "typed"
	})
	h.click("keep")
	h.waitNode(func(n *v1.Node) bool { return findID(n, "reload") == nil && nodeText(n, "body") == "typed" })

	h.change("body", "kept")
	if err := os.Chmod(h.notes, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.notes, 0o755) })
	time.Sleep(2 * time.Second)
	h.waitNode(func(n *v1.Node) bool {
		errText := false
		walkFind(n, func(x *v1.Node) bool {
			if x.Tone == v1.ToneError && x.Text != "" {
				errText = true
			}
			return false
		})
		return errText && nodeText(n, "body") == "kept"
	})
	_ = os.Chmod(h.notes, 0o755)
	body, err := os.ReadFile(path)
	if err != nil || string(body) == "kept" {
		t.Fatalf("read-only wrote through: %q %v", body, err)
	}
}

type notesHost struct {
	t     *testing.T
	notes string
	enc   *v1.Encoder
	mu    sync.Mutex
	root  *v1.Node
	rev   uint64
	wake  chan struct{}
}

func startNotes(t *testing.T) *notesHost {
	t.Helper()
	root := repoRoot(t)
	pluginDir := filepath.Join(t.TempDir(), "org.sysc.notes")
	bin := filepath.Join(pluginDir, "bin", "sysc-plugin-notes")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/notes/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/sysc-plugin-notes")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build notes: %v\n%s", err, out)
	}
	notesDir := t.TempDir()
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
	h := &notesHost{t: t, notes: notesDir, enc: v1.NewEncoder(stdin), wake: make(chan struct{}, 1)}
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
		Plugin:       v1.Identity{ID: "org.sysc.notes", Name: "Notes", Version: "1.0.0"},
		Capabilities: []string{"notifications", "panels", "settings", "state"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("plugin exited during hello: %v\n%s", err, stderr.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
		// PluginHello is decoded by the pump; continue once it is running.
		if cmd.Process != nil {
			break
		}
	}
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{
		"notes_dir": notesDir, "extension": "md",
	}}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *notesHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *notesHost) openPanel() {
	h.t.Helper()
	if err := h.send(&v1.ViewOpen{ViewID: "panel-1", View: v1.ViewPanel, Entry: "panel", Output: "DP-1", Width: 420, Height: 800}); err != nil {
		h.t.Fatal(err)
	}
	h.waitNode(func(n *v1.Node) bool { return findID(n, "new") != nil })
}

func (h *notesHost) click(id string) {
	h.t.Helper()
	h.event(id, v1.EventActivate, "")
}

func (h *notesHost) change(id, text string) {
	h.t.Helper()
	h.event(id, v1.EventChange, text)
}

func (h *notesHost) submit(id, text string) {
	h.t.Helper()
	h.event(id, v1.EventSubmit, text)
}

func (h *notesHost) event(id string, kind v1.EventKind, text string) {
	h.t.Helper()
	h.mu.Lock()
	rev := h.rev
	h.mu.Unlock()
	if err := h.send(&v1.InputEvent{ViewID: "panel-1", Revision: rev, Node: id, Event: kind, Text: text, Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *notesHost) lastRoot() *v1.Node {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.root
}

func (h *notesHost) waitNode(ok func(*v1.Node) bool) *v1.Node {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		root := h.root
		h.mu.Unlock()
		if root != nil && ok(root) {
			return root
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
	h.t.Fatalf("tree never matched\n%s", dumpTree(h.lastRoot()))
	return nil
}
