package running

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func fakeProc(t *testing.T, entries map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, cmdline := range entries {
		dir := filepath.Join(root, pid)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestScanMatchesByDirectory(t *testing.T) {
	root := fakeProc(t, map[string]string{
		"1234": "/Games/Hades/start.sh\x00--flag\x00",
		"1235": "/usr/bin/foo\x00",
		"abc":  "not-a-pid",
	})
	got, err := Scan([]string{"/Games/Hades", "/Games/Missing"}, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %v", got)
	}
	m, ok := got["/Games/Hades"]
	if !ok || m.PID != 1234 {
		t.Fatalf("want PID 1234 for /Games/Hades, got %+v", got)
	}
	if m.Start.IsZero() {
		t.Fatal("Start must come from proc dir mtime")
	}
}

func TestScanProtonPrefixInsideDirectory(t *testing.T) {
	// A wine process whose argv mentions a path *inside* the game directory
	// still counts: proton runs from <dir>/drive_c.
	root := fakeProc(t, map[string]string{
		"99": "/Games/Hades/pfx/drive_c/games/hades.exe\x00",
	})
	got, err := Scan([]string{"/Games/Hades"}, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["/Games/Hades"]; !ok {
		t.Fatalf("prefix path not matched: %v", got)
	}
}

func TestScanEmptyInputs(t *testing.T) {
	got, err := Scan(nil, filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestScanSiblingPrefixes(t *testing.T) {
	root := fakeProc(t, map[string]string{
		"10": "/Games/Hades II/start.sh\x00",
		"11": "/Games/Hades/start.sh\x00",
	})
	got, err := Scan([]string{"/Games/Hades", "/Games/Hades II"}, root)
	if err != nil {
		t.Fatal(err)
	}
	if got["/Games/Hades"].PID != 11 {
		t.Fatalf("Hades matched wrong pid: %+v", got)
	}
	if got["/Games/Hades II"].PID != 10 {
		t.Fatalf("Hades II matched wrong pid: %+v", got)
	}
}

func TestStopTerminatesProcessGroup(t *testing.T) {
	cmd := spawn(t, "sleep", "60")
	if err := Stop(cmd.Process.Pid, 2*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitGone(t, cmd)
}

func TestStopForcesAfterGrace(t *testing.T) {
	// Ignores SIGTERM; Stop must escalate to SIGKILL once grace expires.
	cmd := spawn(t, "sh", "-c", "trap '' TERM; sleep 60")
	start := time.Now()
	if err := Stop(cmd.Process.Pid, 50*time.Millisecond); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("returned before grace elapsed")
	}
	waitGone(t, cmd)
}

func TestStopSignalsGroupOfNonLeader(t *testing.T) {
	leader := spawn(t, "sleep", "60")
	child := exec.Command("sleep", "60")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() })
	if pgid, err := syscall.Getpgid(child.Process.Pid); err != nil || pgid != leader.Process.Pid {
		t.Fatalf("child not in leader group: pgid=%d err=%v", pgid, err)
	}
	if err := Stop(child.Process.Pid, 2*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitGone(t, child)
	waitGone(t, leader)
}

func TestStopRefusesOwnProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	if err := Stop(cmd.Process.Pid, 50*time.Millisecond); err == nil {
		t.Fatal("Stop must refuse a process in the plugin's own group")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process was signalled despite refusal: %v", err)
	}
}
