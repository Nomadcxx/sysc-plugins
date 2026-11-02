package lidguard

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func testOptions(run func(context.Context, string, ...string) (string, string, error)) Options {
	return Options{
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run:      run,
	}
}

func waitSnapshot(t *testing.T, s *Service, check func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := s.Snapshot()
		if check(got) {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	got := s.Snapshot()
	t.Fatalf("service did not reach expected state; last snapshot: %+v", got)
	return Snapshot{}
}

func TestMissingDependenciesAreExplicitlyUnavailable(t *testing.T) {
	s := New(Options{LookPath: func(name string) (string, error) {
		if name == "systemd-inhibit" {
			return "", errors.New("not installed")
		}
		return "/usr/bin/" + name, nil
	}})
	got := s.Snapshot()
	if got.State != v1.ServiceStateUnavailable || got.Available || !strings.Contains(got.Message, "systemd-inhibit") {
		t.Fatalf("missing dependency snapshot = %+v, want unavailable with the missing command named", got)
	}
}

func TestRefreshRechecksMissingDependenciesAndRecovers(t *testing.T) {
	systemdRunInstalled := false
	s := New(Options{
		LookPath: func(name string) (string, error) {
			if name == "systemd-run" && !systemdRunInstalled {
				return "", errors.New("not installed")
			}
			return "/usr/bin/" + name, nil
		},
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "is-active" {
				return "inactive\n", "", errors.New("exit status 4")
			}
			return "", "", nil
		},
	})
	if got := s.Snapshot(); got.State != v1.ServiceStateUnavailable || got.Available {
		t.Fatalf("initial snapshot = %+v, want unavailable while systemd-run is missing", got)
	}

	systemdRunInstalled = true
	if err := s.Request(context.Background(), "refresh"); err != nil {
		t.Fatalf("Request(refresh): %v", err)
	}
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy && !got.Refreshing })
	if got.State != v1.ServiceStateInactive || !got.Available || got.Message != "" {
		t.Fatalf("snapshot after dependency recovery = %+v, want available inactive state", got)
	}
}

func TestToggleStartsOnlyTheUserLidInhibitorAndSerializesActions(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var calls []string
	active := false
	run := func(_ context.Context, name string, args ...string) (string, string, error) {
		call := filepath.Base(name) + " " + strings.Join(args, " ")
		mu.Lock()
		calls = append(calls, call)
		if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "is-active" {
			state := "inactive\n"
			if active {
				state = "active\n"
			}
			mu.Unlock()
			return state, "", nil
		}
		if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "reset-failed" {
			mu.Unlock()
			close(entered)
			<-release
			return "", "", nil
		}
		if filepath.Base(name) == "systemd-run" {
			active = true
		}
		mu.Unlock()
		return "", "", nil
	}
	s := New(testOptions(run))
	if err := s.Request(context.Background(), "toggle"); err != nil {
		t.Fatalf("Request(toggle): %v", err)
	}
	<-entered
	if got := s.Snapshot(); !got.Busy {
		t.Fatalf("snapshot while systemd action is blocked = %+v, want busy", got)
	}
	if err := s.Request(context.Background(), "disable"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second action error = %v, want ErrBusy", err)
	}
	close(release)
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy && got.State == v1.ServiceStateActive })
	if got.Message != "" {
		t.Fatalf("successful enable left an error message: %+v", got)
	}

	mu.Lock()
	joined := strings.Join(calls, "\n")
	mu.Unlock()
	for _, want := range []string{
		"systemctl --user is-active noctalia-lid-guard.service",
		"systemctl --user reset-failed noctalia-lid-guard.service",
		"systemd-run --user --quiet --collect --unit=noctalia-lid-guard.service",
		"systemd-inhibit --what=handle-lid-switch",
		"sleep infinity",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("commands do not include %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "--system") || strings.Contains(joined, "logind.conf") {
		t.Fatalf("commands must be user-scoped and leave system policy alone:\n%s", joined)
	}
}

func TestMutationQueuesBehindReadOnlyRefresh(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case release <- struct{}{}:
		default:
		}
	})
	var mu sync.Mutex
	checks := 0
	active := false
	run := func(_ context.Context, name string, args ...string) (string, string, error) {
		if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "is-active" {
			mu.Lock()
			checks++
			currentCheck, wasActive := checks, active
			mu.Unlock()
			if currentCheck == 1 {
				close(entered)
				<-release
				return "inactive\n", "", nil
			}
			if wasActive {
				return "active\n", "", nil
			}
			return "inactive\n", "", nil
		}
		if filepath.Base(name) == "systemd-run" {
			mu.Lock()
			active = true
			mu.Unlock()
		}
		return "", "", nil
	}
	s := New(testOptions(run))
	if err := s.Request(context.Background(), "refresh"); err != nil {
		t.Fatalf("Request(refresh): %v", err)
	}
	<-entered
	if err := s.Request(context.Background(), "toggle"); err != nil {
		t.Fatalf("Request(toggle) during refresh: %v", err)
	}
	if err := s.Request(context.Background(), "disable"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second mutation error = %v, want ErrBusy", err)
	}
	release <- struct{}{}
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy && got.State == v1.ServiceStateActive })
	if got.Message != "" {
		t.Fatalf("queued toggle left an error message: %+v", got)
	}
}

func TestActionsRefuseSystemdTransitionStates(t *testing.T) {
	for _, state := range []string{"activating", "deactivating", "reloading"} {
		for _, action := range []string{"enable", "disable", "toggle"} {
			t.Run(state+"/"+action, func(t *testing.T) {
				var calls []string
				s := New(testOptions(func(_ context.Context, name string, args ...string) (string, string, error) {
					calls = append(calls, filepath.Base(name)+" "+strings.Join(args, " "))
					if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "is-active" {
						return state + "\n", "", nil
					}
					return "", "", nil
				}))
				if err := s.Request(context.Background(), action); err != nil {
					t.Fatalf("Request(%s): %v", action, err)
				}
				got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy })
				if got.State != v1.ServiceStateUnknown || !strings.Contains(got.Message, "wait for systemd to settle") {
					t.Fatalf("%s during %s = %+v, want unknown with transition guidance", action, state, got)
				}
				if len(calls) != 1 || calls[0] != "systemctl --user is-active "+unitName {
					t.Fatalf("%s during %s ran commands %v; want only the read-only state check", action, state, calls)
				}
			})
		}
	}
}

func TestEnableAndDisableAreIdempotentAfterObservedStateChanges(t *testing.T) {
	for _, tc := range []struct {
		action string
		state  string
		want   v1.ServiceState
	}{
		{action: "enable", state: "active", want: v1.ServiceStateActive},
		{action: "disable", state: "inactive", want: v1.ServiceStateInactive},
	} {
		t.Run(tc.action+"/"+tc.state, func(t *testing.T) {
			var calls []string
			s := New(testOptions(func(_ context.Context, name string, args ...string) (string, string, error) {
				calls = append(calls, filepath.Base(name)+" "+strings.Join(args, " "))
				if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "is-active" {
					if tc.state == "inactive" {
						return tc.state + "\n", "", errors.New("exit status 3")
					}
					return tc.state + "\n", "", nil
				}
				return "", "", nil
			}))
			if err := s.Request(context.Background(), tc.action); err != nil {
				t.Fatalf("Request(%s): %v", tc.action, err)
			}
			got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy })
			if got.State != tc.want || got.Message != "" {
				t.Fatalf("%s after observing %s = %+v, want %s without an error", tc.action, tc.state, got, tc.want)
			}
			if len(calls) != 1 || calls[0] != "systemctl --user is-active "+unitName {
				t.Fatalf("%s after observing %s ran commands %v; want only the state check", tc.action, tc.state, calls)
			}
		})
	}
}

func TestEnableIgnoresResetFailedWhenTheTransientUnitIsNotLoaded(t *testing.T) {
	active := false
	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, string, error) {
		command := filepath.Base(name)
		calls = append(calls, command+" "+strings.Join(args, " "))
		switch {
		case command == "systemctl" && len(args) > 1 && args[1] == "is-active":
			if active {
				return "active\n", "", nil
			}
			return "inactive\n", "", errors.New("exit status 4")
		case command == "systemctl" && len(args) > 1 && args[1] == "reset-failed":
			return "", "Unit noctalia-lid-guard.service not loaded.", errors.New("exit status 1")
		case command == "systemd-run":
			active = true
		}
		return "", "", nil
	}
	s := New(testOptions(run))
	if err := s.Request(context.Background(), "enable"); err != nil {
		t.Fatalf("Request(enable): %v", err)
	}
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy && !got.Refreshing })
	if got.State != v1.ServiceStateActive || got.Message != "" {
		t.Fatalf("enable after best-effort reset = %+v, want active without error", got)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "systemd-run --user --quiet --collect --unit="+unitName) {
		t.Fatalf("systemd-run was not attempted after reset-failed failed: %v", calls)
	}
}

func TestRefreshDistinguishesUnavailableUserManager(t *testing.T) {
	s := New(testOptions(func(_ context.Context, name string, args ...string) (string, string, error) {
		if filepath.Base(name) == "systemctl" && len(args) > 1 && args[1] == "is-active" {
			return "", "Failed to connect to bus: No medium found", errors.New("exit status 1")
		}
		return "", "", nil
	}))
	if err := s.Request(context.Background(), "refresh"); err != nil {
		t.Fatalf("Request(refresh): %v", err)
	}
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy && !got.Refreshing })
	if got.State != v1.ServiceStateUnavailable || !strings.Contains(got.Message, "user service manager") {
		t.Fatalf("manager failure snapshot = %+v, want unavailable with recovery context", got)
	}
}

func TestStatusMessageFitsHostLimitWithoutBreakingUTF8(t *testing.T) {
	got := cleanMessage(strings.Repeat("é\n", 400))
	if !utf8.ValidString(got) {
		t.Fatal("status message is not valid UTF-8")
	}
	if len(got) > v1.MaxServiceStatusMessageBytes {
		t.Fatalf("status message is %d bytes, host limit is %d", len(got), v1.MaxServiceStatusMessageBytes)
	}
	if strings.ContainsAny(got, "\r\n\t") {
		t.Fatalf("status message contains control whitespace: %q", got)
	}
}

func TestStatusMessageRemovesControlCharacters(t *testing.T) {
	got := cleanMessage("first\x00second\r\nthird")
	if strings.ContainsAny(got, "\x00\r\n\t") {
		t.Fatalf("status message contains control characters: %q", got)
	}
}

func TestRefreshPreservesLastConfirmedStateWhileRunning(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case release <- struct{}{}:
		default:
		}
	})
	s := New(testOptions(func(context.Context, string, ...string) (string, string, error) {
		close(entered)
		<-release
		return "inactive\n", "", nil
	}))
	s.status.State = v1.ServiceStateError
	s.status.Message = "Previous command failed"

	if err := s.Request(context.Background(), "refresh"); err != nil {
		t.Fatalf("Request(refresh): %v", err)
	}
	<-entered
	if got := s.Snapshot(); got.Busy || !got.Refreshing || got.State != v1.ServiceStateError || got.Message != "Previous command failed" {
		t.Fatalf("snapshot while refresh is pending = %+v, want unchanged confirmed state without busy", got)
	}
	release <- struct{}{}
	got := waitSnapshot(t, s, func(got Snapshot) bool { return !got.Busy && !got.Refreshing })
	if got.State != v1.ServiceStateInactive || got.Message != "" {
		t.Fatalf("snapshot after refresh = %+v, want newly observed inactive state", got)
	}
}
