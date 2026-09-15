package recorder

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("SYSC_FAKE_RECORDER") == "1" {
		signal.Reset(syscall.SIGINT)
		if os.Getenv("SYSC_FAKE_BEHAVIOR") == "ignore-int" {
			signal.Ignore(syscall.SIGINT)
		}
		os.Exit(runFakeRecorder())
	}
	os.Exit(m.Run())
}

func TestProcessStartStatusAndSIGINTStop(t *testing.T) {
	p := startFake(t, "hang", nil)
	waitReady(t, p)
	if p.PID() <= 0 {
		t.Fatal("start left no pid")
	}
	if !p.Running() {
		t.Fatal("a live recorder is not running")
	}
	if err := p.Stop(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if p.Running() {
		t.Fatal("SIGINT stop left the process running")
	}
}

func TestProcessStartupFailure(t *testing.T) {
	t.Parallel()
	_, err := Start(context.Background(), "/no/such/gpu-screen-recorder", []string{"-w", "portal"}, nil)
	if err == nil {
		t.Fatal("Start accepted a missing executable")
	}
}

func TestProcessExitResult(t *testing.T) {
	p := startFake(t, "fail", nil)
	err := p.Wait()
	if err == nil {
		t.Fatal("a failing recorder returned a nil wait")
	}
}

func TestProcessKillAfterStopTimeout(t *testing.T) {
	p := startFake(t, "ignore-int", nil)
	waitReady(t, p)
	start := time.Now()
	if err := p.Stop(150 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Fatalf("Stop returned in %s; SIGINT should have been ignored until SIGKILL", elapsed)
	}
	if elapsed > time.Second {
		t.Fatal("Stop waited past the kill timeout")
	}
	if p.Running() {
		t.Fatal("kill after timeout left the process running")
	}
}

func TestProcessLogTailIsBounded(t *testing.T) {
	p := startFake(t, "flood", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(p.Logs()) < 100 {
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.Stop(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := len(p.Logs()); got == 0 || got > maxLogBytes {
		t.Fatalf("logs = %d, want 1 through %d", got, maxLogBytes)
	}
}

func TestProcessShutdownLeavesNoOrphan(t *testing.T) {
	p := startFake(t, "hang", nil)
	waitReady(t, p)
	pid := p.PID()
	if err := p.Stop(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		t.Fatalf("pid %d still accepts signals after Stop", pid)
	}
}

func TestAdoptMatchesExactExecutableAndArgs(t *testing.T) {
	t.Parallel()
	args := []string{"-w", "DP-1", "-o", "/tmp/out.mp4"}
	scan := func() ([]ProcInfo, error) {
		return []ProcInfo{{PID: 42, Exe: "/usr/bin/gpu-screen-recorder", Args: args}}, nil
	}
	p, err := Adopt(scan, "/usr/bin/gpu-screen-recorder", args)
	if err != nil {
		t.Fatal(err)
	}
	if p.PID() != 42 {
		t.Fatalf("pid = %d", p.PID())
	}
}

func TestListProcsFindsALiveFake(t *testing.T) {
	args := []string{"-w", "portal", "-o", filepath.Join(t.TempDir(), "a.mp4")}
	p := startFake(t, "hang", args)
	waitReady(t, p)
	list, err := listProcs()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Adopt(func() ([]ProcInfo, error) { return list, nil }, os.Args[0], args)
	if err != nil {
		t.Fatalf("live fake not listed: %v", err)
	}
	if got.PID() != p.PID() {
		t.Fatalf("pid = %d, want %d", got.PID(), p.PID())
	}
}

func TestAdoptRejectsDifferentArgsAndAmbiguousMatches(t *testing.T) {
	t.Parallel()
	want := []string{"-w", "portal", "-o", "/tmp/a.mp4"}
	_, err := Adopt(func() ([]ProcInfo, error) {
		return []ProcInfo{{PID: 1, Exe: "/usr/bin/gpu-screen-recorder", Args: []string{"-w", "other"}}}, nil
	}, "/usr/bin/gpu-screen-recorder", want)
	if err == nil {
		t.Fatal("adopted a same-name process with different arguments")
	}
	_, err = Adopt(func() ([]ProcInfo, error) {
		return []ProcInfo{
			{PID: 1, Exe: "/usr/bin/gpu-screen-recorder", Args: want},
			{PID: 2, Exe: "/usr/bin/gpu-screen-recorder", Args: want},
		}, nil
	}, "/usr/bin/gpu-screen-recorder", want)
	if err == nil {
		t.Fatal("adopted when more than one process matched")
	}
}

func startFake(t *testing.T, behavior string, args []string) *Proc {
	t.Helper()
	env := append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR="+behavior)
	p, err := Start(context.Background(), os.Args[0], args, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop(time.Second) })
	return p
}

func waitReady(t *testing.T, p *Proc) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains(p.Logs(), []byte("ready")) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fake recorder never printed ready")
}

func runFakeRecorder() int {
	switch os.Getenv("SYSC_FAKE_BEHAVIOR") {
	case "fail":
		return 1
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
	case "silent-run":
		_, _ = os.Stderr.WriteString("gsr info: using socketpair\n")
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGUSR1)
		writeRecordArtifact()
		<-ch
		return 0
	}
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGUSR1)
	writeRecordArtifact()
	_, _ = os.Stdout.WriteString("ready\n")
	for sig := range ch {
		if sig == syscall.SIGUSR1 {
			writeReplayArtifact()
			continue
		}
		return 0
	}
	return 0
}

func argAfter(flag string) string {
	args := os.Args[1:]
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func writeRecordArtifact() {
	p := argAfter("-o")
	if p == "" {
		return
	}
	var body []byte
	if os.Getenv("SYSC_FAKE_BEHAVIOR") != "zero" {
		body = []byte("mp4")
	}
	_ = os.WriteFile(p, body, 0o644)
}

func writeReplayArtifact() {
	dir := argAfter("-ro")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "gsr.mp4"), []byte("mp4"), 0o644)
}
