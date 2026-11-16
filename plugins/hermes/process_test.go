package hermes

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeBridgeScript writes a shell script standing in for the Python bridge.
// The tests run it with /bin/sh as the "python" command, so no Python and no
// real bridge is involved.
func fakeBridgeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake_bridge.sh")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForLog(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), want) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("log %s never contained %q; got %q", path, want, data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitDone(t *testing.T, p Process, within time.Duration) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(within):
		t.Fatalf("process %d still running after %s", p.Pid(), within)
	}
}

func TestSpawnArgvIsExactAndShellFree(t *testing.T) {
	spec := SpawnSpec{
		Python:        "python3",
		Script:        "/opt/hermes/scripts/hermes_bridge.py",
		StateFile:     "/home/u/.cache/sysc-hermes/state.json",
		HermesHome:    "/home/u/.hermes",
		HermesCommand: "hermes; rm -rf /",
	}
	want := []string{
		"python3", "/opt/hermes/scripts/hermes_bridge.py",
		"--host", "127.0.0.1", "--port", "19777",
		"--state-file", "/home/u/.cache/sysc-hermes/state.json",
		"--hermes-home", "/home/u/.hermes",
		"--hermes-command", "hermes; rm -rf /",
	}
	if got := spec.Argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
}

func TestExecStarterKeepsTheChildInOurGroupAndLogsIt(t *testing.T) {
	script := fakeBridgeScript(t, "echo \"args: $*\"\necho oops >&2\necho ready\nwhile :; do sleep 0.05; done\n")
	logPath := filepath.Join(t.TempDir(), "sysc-hermes", "bridge.log")
	spec := SpawnSpec{Python: "/bin/sh", Script: script, StateFile: "/s/state.json",
		HermesHome: "/h", HermesCommand: "hermes", LogPath: logPath}

	p, err := ExecStarter{}.Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { StopProcess(p, time.Second) })

	log := waitForLog(t, logPath, "ready")
	if !strings.Contains(log, "args: --host 127.0.0.1 --port 19777 --state-file /s/state.json --hermes-home /h --hermes-command hermes") {
		t.Fatalf("child stdout missing from log: %q", log)
	}
	if !strings.Contains(log, "oops") {
		t.Fatalf("child stderr missing from log: %q", log)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("log mode = %o, want 600", mode)
	}
	pgid, err := syscall.Getpgid(p.Pid())
	if err != nil {
		t.Fatal(err)
	}
	if pgid != syscall.Getpgrp() {
		t.Fatalf("child pgid %d, want ours %d (no setsid, no new group)", pgid, syscall.Getpgrp())
	}

	start := time.Now()
	StopProcess(p, 3*time.Second)
	waitDone(t, p, time.Second)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("SIGTERM stop took %s", elapsed)
	}
}

func TestStopKillsAChildThatIgnoresTerm(t *testing.T) {
	script := fakeBridgeScript(t, "trap '' TERM\necho ready\nwhile :; do sleep 0.05; done\n")
	logPath := filepath.Join(t.TempDir(), "bridge.log")
	p, err := ExecStarter{}.Start(SpawnSpec{Python: "/bin/sh", Script: script, LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	waitForLog(t, logPath, "ready")

	grace := 200 * time.Millisecond
	start := time.Now()
	StopProcess(p, grace)
	waitDone(t, p, time.Second)
	if elapsed := time.Since(start); elapsed < grace {
		t.Fatalf("killed after %s, before the %s grace", elapsed, grace)
	}
	if code := p.ExitCode(); code != 128+int(syscall.SIGKILL) {
		t.Fatalf("exit code = %d, want %d (SIGKILL)", code, 128+int(syscall.SIGKILL))
	}
}

func TestExitCodeIsReported(t *testing.T) {
	script := fakeBridgeScript(t, "exit 3\n")
	p, err := ExecStarter{}.Start(SpawnSpec{Python: "/bin/sh", Script: script,
		LogPath: filepath.Join(t.TempDir(), "bridge.log")})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, p, 5*time.Second)
	if code := p.ExitCode(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	// Stopping a process that already exited is a no-op, not a panic.
	StopProcess(p, 10*time.Millisecond)
}

func TestStartFailsForAMissingInterpreter(t *testing.T) {
	_, err := ExecStarter{}.Start(SpawnSpec{Python: filepath.Join(t.TempDir(), "no-python"),
		Script: "x.py", LogPath: filepath.Join(t.TempDir(), "bridge.log")})
	if err == nil {
		t.Fatal("start succeeded without an interpreter")
	}
}

func TestBridgeLogIsCappedAndRotatedOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.log")
	w, err := openBridgeLog(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	chunk := []byte(strings.Repeat("a", 40) + "\n")
	for i := 0; i < 2; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	// The second write crossed the cap: the first chunk is now bridge.log.1.
	if data, _ := os.ReadFile(path + ".1"); len(data) != len(chunk) {
		t.Fatalf("rotated file holds %d bytes, want %d", len(data), len(chunk))
	}
	if _, err := w.Write([]byte(strings.Repeat("b", 40) + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Only one rotation is kept.
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("a second backup exists: %v", err)
	}
	cur, _ := os.ReadFile(path)
	if len(cur) > 64 || !strings.Contains(string(cur), "b") {
		t.Fatalf("current log = %q, want the newest chunk under the cap", cur)
	}
	for _, p := range []string{path, path + ".1"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", p, info.Mode().Perm())
		}
	}

	// Reopening a log that is already over the cap rotates it first.
	if err := os.WriteFile(path, []byte(strings.Repeat("c", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err = openBridgeLog(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if data, _ := os.ReadFile(path + ".1"); len(data) != 100 {
		t.Fatalf("oversized log not rotated on open: %d bytes in .1", len(data))
	}
}
