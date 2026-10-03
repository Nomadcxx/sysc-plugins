package moonbit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeSudo writes a stand-in for `sudo -S -k -p PROMPT moonbit panel`: it
// prompts on stderr the way sudo -S does, re-prompts after a wrong password,
// then plays moonbit: ready, read one request, answer per FAKE_MODE.
func fakeSudo(t *testing.T, mode string) Runner {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/bash
prompt=""
while [ $# -gt 0 ]; do case "$1" in -p) prompt="$2"; shift 2;; -S|-k) shift;; *) break;; esac; done
case "$FAKE_MODE" in
  refused) echo "nomadx is not in the sudoers file." >&2; exit 1;;
  nopasswd) ;;
  *)
    printf '%s' "$prompt" >&2; read -r pw
    if [ "$pw" != "hunter2" ]; then echo "Sorry, try again." >&2; printf '%s' "$prompt" >&2; read -r pw; exit 1; fi;;
esac
echo '{"t":"ready"}'
read -r req
echo "$req" > "$FAKE_DIR/request"
case "$FAKE_MODE" in
  cancel) echo '{"t":"category","name":"a","i":1,"total":9}'; cat >/dev/null; echo '{"t":"cancelled"}';;
  long) printf '{"t":"clean_done","deleted":3,"errors":["%s"]}\n' "$(head -c 200000 /dev/zero | tr '\0' x)";;
  # Grandchild keeps stdout open and ignores stdin EOF and catchable signals.
  # Killing the parent script must not be enough to end the run.
  hung)
    echo $$ > "$FAKE_DIR/sudo.pid"
    bash -c 'trap "" TERM HUP INT QUIT ALRM; echo $BASHPID > "$FAKE_DIR/child"; exec sleep 100000' </dev/null &
    wait;;
  *) echo '{"t":"category_done","name":"Pacman Cache","files":2,"bytes":20}'; echo '{"t":"done","scanned_at":"2026-10-02T12:30:54Z"}';;
esac
`
	path := filepath.Join(dir, "sudo")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MODE", mode)
	t.Setenv("FAKE_DIR", dir)
	return Runner{Sudo: path}
}

func drain(t *testing.T, op *Op) []Event {
	t.Helper()
	var evs []Event
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-op.Events:
			if !ok {
				return evs
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Fatal("run never ended")
		}
	}
}

func lastRequest(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("FAKE_DIR"), "request"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestStartAnswersSudoAndSendsTheRequest(t *testing.T) {
	r := fakeSudo(t, "scan")
	pw := []byte("hunter2")
	op, err := r.Start(pw, Request{Cmd: "scan", Mode: "deep"})
	if err != nil {
		t.Fatal(err)
	}
	evs := drain(t, op)
	if len(evs) != 2 || evs[1].T != "done" || evs[1].ScannedAt == "" {
		t.Fatalf("events = %+v", evs)
	}
	if got := lastRequest(t); got != `{"cmd":"scan","mode":"deep"}` {
		t.Fatalf("moonbit got %s", got)
	}
	if string(pw) != "\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatal("the password was not zeroed after use")
	}
}

func TestStartReportsAWrongPassword(t *testing.T) {
	r := fakeSudo(t, "scan")
	if _, err := r.Start([]byte("nope"), Request{Cmd: "scan"}); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("err = %v, want ErrWrongPassword", err)
	}
}

// With NOPASSWD sudo never prompts; the password must not reach moonbit as
// if it were the request.
func TestStartWithoutAPromptNeverSendsThePassword(t *testing.T) {
	r := fakeSudo(t, "nopasswd")
	op, err := r.Start([]byte("hunter2"), Request{Cmd: "scan", Mode: "quick"})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, op)
	if got := lastRequest(t); got != `{"cmd":"scan","mode":"quick"}` {
		t.Fatalf("moonbit's first line = %q, want the request", got)
	}
}

func TestStartExplainsASudoRefusal(t *testing.T) {
	r := fakeSudo(t, "refused")
	_, err := r.Start([]byte("hunter2"), Request{Cmd: "scan"})
	if err == nil || !strings.Contains(err.Error(), "may not run moonbit with sudo") {
		t.Fatalf("err = %v", err)
	}
}

// Cancel ends the request side; moonbit's cancelled event still arrives.
func TestCancelStillReadsMoonbitsCancelledEvent(t *testing.T) {
	r := fakeSudo(t, "cancel")
	op, err := r.Start([]byte("hunter2"), Request{Cmd: "scan"})
	if err != nil {
		t.Fatal(err)
	}
	<-op.Events // category
	op.Cancel()
	evs := drain(t, op)
	if len(evs) == 0 || evs[len(evs)-1].T != "cancelled" {
		t.Fatalf("events after Cancel = %+v, want cancelled last", evs)
	}
}

// clean_done lists every failed path; a line past bufio.Scanner's 64 KiB
// default used to drop the terminal event.
func TestLongEventLineIsDelivered(t *testing.T) {
	r := fakeSudo(t, "long")
	op, err := r.Start([]byte("hunter2"), Request{Cmd: "clean", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	evs := drain(t, op)
	if len(evs) != 1 || evs[0].T != "clean_done" || len(evs[0].Errors[0]) != 200000 {
		t.Fatalf("got %d events, want the one long clean_done", len(evs))
	}
}

func TestRequestLabels(t *testing.T) {
	for _, tc := range []struct {
		req  Request
		want string
	}{
		{Request{Cmd: "scan", Mode: "deep"}, "a deep scan"},
		{Request{Cmd: "clean"}, "the clean"},
		{Request{Cmd: "docker", Op: "all"}, "the Docker cleanup"},
		{Request{Cmd: "schedule", Target: "timers", Action: "enable"}, "enabling the scan and clean timers"},
		{Request{Cmd: "schedule", Target: "daemon", Action: "disable"}, "disabling daemon mode"},
	} {
		if got := RequestLabel(tc.req); got != tc.want {
			t.Errorf("label(%+v) = %q, want %q", tc.req, got, tc.want)
		}
	}
}

func TestFoldCancelFromDaemon(t *testing.T) {
	s := &State{}
	s.StartScan()
	if !s.Fold(Event{T: "cancelled"}) || s.Phase != PhaseIdle {
		t.Fatal("cancelled must fold to idle")
	}
}

func TestStreamEndedRecoversActiveOperation(t *testing.T) {
	for _, phase := range []Phase{PhaseScanning, PhaseCleaning} {
		s := &State{
			Phase:      phase,
			ScanCat:    "cache",
			ScanDir:    "/tmp/cache",
			ScanTotal:  3,
			ScanFiles:  8,
			ScanBytes:  512,
			CleanTotal: 8,
			CleanDone:  4,
			CleanFile:  "/tmp/cache/item",
		}
		if !s.StreamEnded() || s.Phase != PhaseError || s.Err == "" {
			t.Fatalf("phase %d did not recover to an error state: %+v", phase, s)
		}
		if s.ScanCat != "" || s.ScanDir != "" || s.ScanTotal != 0 || s.ScanFiles != 0 || s.ScanBytes != 0 ||
			s.CleanTotal != 0 || s.CleanDone != 0 || s.CleanFile != "" {
			t.Fatalf("phase %d retained stale progress: %+v", phase, s)
		}
	}
}

// Docker and schedule runs wait on PhaseWorking. Closing that stream without
// docker_done, schedule_done, cancelled, or error must not leave the spinner
// up: that screen's action bar is empty, and Cancel does nothing once the op
// is gone.
func TestStreamEndedRecoversWorkingRun(t *testing.T) {
	for _, tc := range []struct {
		name    string
		working string
		back    Phase
	}{
		{name: "docker", working: "Cleaning Docker", back: PhaseDocker},
		{name: "schedule", working: "Enabling daemon mode", back: PhaseSchedule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &State{Phase: PhaseWorking, Working: tc.working, Back: tc.back}
			if !s.StreamEnded() {
				t.Fatal("closed stream must recover a working run")
			}
			if s.Phase != PhaseError || s.Err == "" {
				t.Fatalf("phase = %d err = %q, want PhaseError with a message", s.Phase, s.Err)
			}
			if s.Working != "" {
				t.Fatalf("working label still set: %q", s.Working)
			}
			var buttons int
			for _, side := range actionBar(s).Children {
				buttons += len(side.Children)
			}
			if buttons == 0 {
				t.Fatal("action bar is empty, so there is no way off the screen")
			}
		})
	}
}

// The daemon cancels on EOF of its request reader and still writes its
// terminal event, so Cancel must leave the read side open to receive it.

func withShortGrace(t *testing.T) {
	t.Helper()
	prev := cancelGrace
	cancelGrace = 200 * time.Millisecond
	t.Cleanup(func() { cancelGrace = prev })
}

func TestStopCommandGivesSIGALRMHandlerTimeToFinish(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	marker := filepath.Join(dir, "alarm-handled")
	script := fmt.Sprintf(`trap 'sleep 1; echo handled > %s; exit 0' ALRM
sleep 100000 &
echo $! > "$FAKE_DIR/child"
wait
`, strconv.Quote(marker))
	cmd := exec.Command("bash", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := pidFile(t, "child")
	t.Cleanup(func() { releasePIDs(child) })

	stopCommand(cmd, stdout, cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("command did not finish after SIGALRM: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("SIGALRM handler did not finish before cleanup: %v", err)
	}
	if !waitForProcessEnd(child) {
		t.Fatalf("descendant %d survived process-group cleanup", child)
	}
}

func pidFile(t *testing.T, name string) int {
	t.Helper()
	path := filepath.Join(os.Getenv("FAKE_DIR"), name)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			n, conv := strconv.Atoi(strings.TrimSpace(string(b)))
			if conv == nil && n > 0 {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid file %s did not appear", name)
	return 0
}

func processEnded(pid int) bool {
	if pid <= 0 {
		return true
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.Contains(line, "Z")
		}
	}
	return false
}

func waitForProcessEnd(pid int) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if processEnded(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return processEnded(pid)
}

func releasePIDs(pids ...int) {
	own, _ := syscall.Getpgid(0)
	for _, pid := range pids {
		if pid <= 0 || processEnded(pid) {
			continue
		}
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 && pgid != own {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			_ = exec.Command("sudo", "-n", "kill", "-9", strconv.Itoa(pid)).Run()
		}
	}
}

// recoverLikeStreamOp is the plugin loop's rule: a terminal event folds on
// its own, and a stream that closes without one must go through StreamEnded.
func recoverLikeStreamOp(t *testing.T, op *Op, s *State) {
	t.Helper()
	timer := time.NewTimer(cancelGrace + 5*time.Second)
	defer timer.Stop()
	terminal := false
	for {
		select {
		case ev, ok := <-op.Events:
			if !ok {
				if !terminal && !s.StreamEnded() {
					t.Fatal("closed stream skipped StreamEnded")
				}
				return
			}
			s.Fold(ev)
			switch ev.T {
			case "done", "clean_done", "cancelled", "error", "docker_done", "schedule_done":
				terminal = true
			}
		case <-timer.C:
			t.Fatalf("stream still open after cancel; phase %d", s.Phase)
		}
	}
}

// A sudo stand-in that forks a grandchild holding stdout must not survive
// Cancel. The grandchild ignores stdin EOF, so closing the request side does
// nothing; after the grace kill the child is dead and the working phase is
// gone. StreamEnded is what leaves PhaseWorking when no terminal event arrives.
func TestCancelKillsHungGrandchildAndLeavesTheProgressPhase(t *testing.T) {
	withShortGrace(t)
	r := fakeSudo(t, "hung")
	op, err := r.Start([]byte("hunter2"), Request{Cmd: "docker", Op: "all"})
	if err != nil {
		t.Fatal(err)
	}
	child := pidFile(t, "child")
	sudoPID := pidFile(t, "sudo.pid")
	t.Cleanup(func() { releasePIDs(child, sudoPID) })

	s := &State{Phase: PhaseWorking, Working: "Cleaning Docker", Back: PhaseDocker}
	op.Cancel()
	recoverLikeStreamOp(t, op, s)
	if s.Phase == PhaseWorking {
		t.Fatalf("phase still working; grandchild ended=%v", processEnded(child))
	}
	if s.Phase != PhaseError && s.Phase != PhaseIdle {
		t.Fatalf("phase = %d, want PhaseError or PhaseIdle", s.Phase)
	}
	if s.Phase == PhaseError && s.Err == "" {
		t.Fatal("StreamEnded left PhaseError with no message")
	}
	if !waitForProcessEnd(child) {
		t.Fatalf("grandchild %d still running", child)
	}
}

// moonbit runs as root. A direct signal from the plugin is EPERM, and killing
// sudo's process group does not reach that child. The child ignores catchable
// signals and stdin EOF; Cancel must still end it and leave the progress phase.
func TestCancelKillsRootChildThatIgnoresSignals(t *testing.T) {
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skip("passwordless sudo is required")
	}
	withShortGrace(t)
	dir := t.TempDir()
	childPath := filepath.Join(dir, "child")
	reqPath := filepath.Join(dir, "request")
	script := fmt.Sprintf(`#!/bin/bash
trap '' TERM HUP INT ALRM QUIT
echo '{"t":"ready"}'
read -r req
echo "$req" > %s
echo $$ > %s
exec sleep 100000
`, strconv.Quote(reqPath), strconv.Quote(childPath))
	bin := filepath.Join(dir, "moonbit")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	op, err := (Runner{Sudo: "sudo", Moonbit: bin}).Start(nil, Request{Cmd: "docker", Op: "all"})
	if err != nil {
		t.Fatal(err)
	}
	var child int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(childPath)
		if err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if child > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("root child pid did not appear")
	}
	t.Cleanup(func() { releasePIDs(child) })

	s := &State{Phase: PhaseWorking, Working: "Cleaning Docker", Back: PhaseDocker}
	op.Cancel()
	recoverLikeStreamOp(t, op, s)
	if s.Phase == PhaseWorking {
		t.Fatalf("phase still working; root child ended=%v", processEnded(child))
	}
	if s.Phase != PhaseError && s.Phase != PhaseIdle {
		t.Fatalf("phase = %d, want PhaseError or PhaseIdle", s.Phase)
	}
	if !processEnded(child) {
		t.Fatalf("root child %d still running", child)
	}
}
