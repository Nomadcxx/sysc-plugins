package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestMain(m *testing.M) {
	if os.Getenv("SYSC_FAKE_DOCKER") == "1" {
		os.Exit(runGateFakeDocker())
	}
	if os.Getenv("SYSC_FAKE_RECORDER") == "1" {
		os.Exit(runGateFakeRecorder())
	}
	os.Exit(m.Run())
}

// runGateFakeRecorder backs the gpu-screen-recorder symlink that the recorder
// gate tests install on PATH: the test binary re-executes itself as the fake
// backend when SYSC_FAKE_RECORDER is set.
func runGateFakeRecorder() int {
	signal.Reset(syscall.SIGINT)
	if p := os.Getenv("SYSC_FAKE_PID"); p != "" {
		_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	if p := os.Getenv("SYSC_FAKE_ARGV"); p != "" {
		raw, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(p, raw, 0o644)
	}
	switch os.Getenv("SYSC_FAKE_BEHAVIOR") {
	case "crash":
		_, _ = os.Stdout.WriteString("ready\n")
		return 1
	case "flood":
		chunk := bytes.Repeat([]byte("x"), 4096)
		for i := 0; i < 40; i++ {
			_, _ = os.Stderr.Write(chunk)
		}
	case "ignore-int":
		signal.Ignore(syscall.SIGINT)
		_, _ = os.Stdout.WriteString("ready\n")
		for {
			// A pending timer keeps the runtime deadlock detector quiet;
			// a bare select{} can panic "all goroutines are asleep".
			time.Sleep(time.Hour)
		}
	}
	if p := argValue("-o"); p != "" {
		body := []byte("mp4")
		if os.Getenv("SYSC_FAKE_BEHAVIOR") == "zero" {
			body = nil
		}
		_ = os.WriteFile(p, body, 0o644)
	}
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGUSR1)
	_, _ = os.Stdout.WriteString("ready\n")
	for sig := range ch {
		if sig == syscall.SIGUSR1 {
			if dir := argValue("-ro"); dir != "" {
				_ = os.MkdirAll(dir, 0o755)
				_ = os.WriteFile(filepath.Join(dir, "gsr.mp4"), []byte("mp4"), 0o644)
			}
			continue
		}
		return 0
	}
	return 0
}

func argValue(flag string) string {
	args := os.Args[1:]
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// viewSlot is the box a host opened a view with. A scripted host records the
// slot before it sends ViewOpen, so every snapshot it captures can be laid out
// exactly as the real host would have laid it out.
type viewSlot struct {
	view v1.ViewKind
	w, h int
}

// recordSlot remembers the slot a host announced for a view. It is called
// before the ViewOpen goes out, so no snapshot can arrive unmonitored.
func recordSlot(m map[string]viewSlot, id string, slot viewSlot) map[string]viewSlot {
	if m == nil {
		m = make(map[string]viewSlot, 2)
	}
	m[id] = slot
	return m
}

// checkFits runs a captured tree through the host's own layout rules. A
// rejection here is the bug class that used to reach the desktop: a tree that
// validates and cannot be drawn. t.Errorf is safe off the test goroutine,
// which is where each decoder calls this.
func checkFits(t *testing.T, slot viewSlot, root *v1.Node) {
	t.Helper()
	if root == nil {
		return
	}
	for _, f := range lint.Tree(root, slot.view, slot.w, slot.h) {
		t.Errorf("%s view at %dx%d: %s", slot.view, slot.w, slot.h, f)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func walkFind(n *v1.Node, ok func(*v1.Node) bool) *v1.Node {
	if n == nil {
		return nil
	}
	if ok(n) {
		return n
	}
	for _, c := range n.Children {
		if found := walkFind(c, ok); found != nil {
			return found
		}
	}
	return nil
}

func findID(n *v1.Node, id string) *v1.Node {
	return walkFind(n, func(x *v1.Node) bool { return x.ID == id })
}

func nodeText(n *v1.Node, id string) string {
	found := findID(n, id)
	if found == nil {
		return ""
	}
	return found.Text
}

func findNode(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id || n.Key == id {
		return n
	}
	for _, c := range n.Children {
		if got := findNode(c, id); got != nil {
			return got
		}
	}
	return nil
}

func dumpTree(n *v1.Node) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func treeText(n *v1.Node) string {
	if n == nil {
		return ""
	}
	parts := []string{n.Text, n.Icon, n.Name}
	for _, c := range n.Children {
		parts = append(parts, treeText(c))
	}
	return strings.Join(parts, " ")
}

func waitFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got []byte
	var err error
	for time.Now().Before(deadline) {
		got, err = os.ReadFile(path)
		if err == nil && string(got) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s = %q %v, want %q", path, got, err, want)
}

func waitPinned(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, ".pinned.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(raw), name) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s was not pinned", name)
}

func hasArg(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasShell(args []string) bool {
	joined := strings.Join(args, " ")
	return strings.Contains(joined, ">>") || strings.Contains(joined, "&&")
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func barCapturing(n *v1.Node) bool {
	toggle := findNode(n, "toggle")
	return toggle != nil && toggle.Icon == "stop" && toggle.Tone == v1.ToneError
}

func barFailed(n *v1.Node) bool {
	toggle := findNode(n, "toggle")
	return toggle != nil && toggle.Icon == "camera-off"
}

func barIdle(n *v1.Node) bool {
	toggle := findNode(n, "toggle")
	return toggle != nil && toggle.Icon == "record" && toggle.Tone == v1.ToneNormal
}
