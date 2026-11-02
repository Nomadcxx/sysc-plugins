package lidguard

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCommandRunnerUsesFixedUserServiceArguments(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "systemd-run.args")
	statePath := filepath.Join(dir, "unit-state")
	t.Setenv("SYSTEMD_RUN_ARGS", argsPath)
	t.Setenv("UNIT_STATE_FILE", statePath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin")
	writeExecutable(t, filepath.Join(dir, "systemctl"), `#!/bin/sh
if [ "$2" = "is-active" ]; then
	if [ -f "$UNIT_STATE_FILE" ]; then cat "$UNIT_STATE_FILE"; exit 0; fi
	printf 'inactive\n'
	exit 3
fi
exit 0
`)
	writeExecutable(t, filepath.Join(dir, "systemd-run"), `#!/bin/sh
printf '%s\n' "$@" > "$SYSTEMD_RUN_ARGS"
printf 'active\n' > "$UNIT_STATE_FILE"
`)
	writeExecutable(t, filepath.Join(dir, "systemd-inhibit"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(dir, "sleep"), "#!/bin/sh\nexit 0\n")

	s := New(Options{})
	if err := s.Request(context.Background(), "enable"); err != nil {
		t.Fatalf("Request(enable): %v", err)
	}
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy })
	if got.State != v1.ServiceStateActive {
		t.Fatalf("enable state = %+v, want active", got)
	}
	contents, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--user", "--quiet", "--collect", "--unit=" + unitName,
		"--description=Sysc Lid Guard", "--",
		filepath.Join(dir, "systemd-inhibit"), "--what=handle-lid-switch",
		"--who=Sysc Lid Guard", "--why=Keep background jobs running while the laptop lid is closed",
		"--mode=block", filepath.Join(dir, "sleep"), "infinity",
	}
	if got := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("systemd-run argv = %#v, want %#v", got, want)
	}
}

func TestCommandRunnerUsesFixedUserStopArguments(t *testing.T) {
	dir := t.TempDir()
	stopArgsPath := filepath.Join(dir, "systemctl-stop.args")
	statePath := filepath.Join(dir, "unit-state")
	if err := os.WriteFile(statePath, []byte("active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYSTEMCTL_STOP_ARGS", stopArgsPath)
	t.Setenv("UNIT_STATE_FILE", statePath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin")
	writeExecutable(t, filepath.Join(dir, "systemctl"), `#!/bin/sh
if [ "$2" = "is-active" ]; then
	cat "$UNIT_STATE_FILE"
	exit 0
fi
if [ "$2" = "stop" ]; then
	printf '%s\n' "$@" > "$SYSTEMCTL_STOP_ARGS"
	printf 'inactive\n' > "$UNIT_STATE_FILE"
	exit 0
fi
exit 1
`)

	s := New(Options{})
	if err := s.Request(context.Background(), "disable"); err != nil {
		t.Fatalf("Request(disable): %v", err)
	}
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy })
	if got.State != v1.ServiceStateInactive {
		t.Fatalf("disable state = %+v, want inactive", got)
	}
	contents, err := os.ReadFile(stopArgsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--user", "stop", unitName}
	if got := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("systemctl stop argv = %#v, want %#v", got, want)
	}
}

func TestCommandRunnerPreservesStderrAndMissingExecutables(t *testing.T) {
	dir := t.TempDir()
	failure := filepath.Join(dir, "failure")
	writeExecutable(t, failure, "#!/bin/sh\nprintf 'command detail\\n' >&2\nexit 7\n")
	_, stderr, err := runCommand(context.Background(), failure)
	if err == nil || stderr != "command detail\n" {
		t.Fatalf("runCommand stderr=%q err=%v, want preserved detail and failure", stderr, err)
	}

	missingDir := t.TempDir()
	t.Setenv("PATH", missingDir)
	s := New(Options{})
	got := s.Snapshot()
	if got.State != v1.ServiceStateUnavailable || got.Available {
		t.Fatalf("missing commands snapshot = %+v, want unavailable", got)
	}
	for _, command := range requiredCommands {
		if !strings.Contains(got.Message, command) {
			t.Errorf("missing-command message %q omits %s", got.Message, command)
		}
	}
}

func TestCommandRunnerHonorsContextTimeout(t *testing.T) {
	dir := t.TempDir()
	startedPath := filepath.Join(dir, "started")
	t.Setenv("STARTED_PATH", startedPath)
	sleeper := filepath.Join(dir, "slow")
	writeExecutable(t, sleeper, "#!/bin/sh\nprintf '' > \"$STARTED_PATH\"\nexec /usr/bin/sleep 10\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err := runCommand(ctx, sleeper)
	if err == nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("runCommand timeout error = %v, context = %v", err, ctx.Err())
	}
	if time.Since(started) > time.Second {
		t.Fatalf("runCommand took %s after its 100ms timeout", time.Since(started))
	}
	if _, err := os.Stat(startedPath); err != nil {
		t.Fatalf("slow command did not start before timeout: %v", err)
	}
}
