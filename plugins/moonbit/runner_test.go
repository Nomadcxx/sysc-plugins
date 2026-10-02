package moonbit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// The daemon cancels on EOF of its request reader and still writes its
// terminal event, so Cancel must leave the read side open to receive it.
