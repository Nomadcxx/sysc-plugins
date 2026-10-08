package updates

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func okPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func noPath(string) (string, error) { return "", errors.New("not found") }

func goodRunner(t *testing.T) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		switch name {
		case "checkupdates":
			return sample(t, "checkupdates.txt"), nil, 0, nil
		case "paru", "yay":
			return sample(t, "paru.txt"), nil, 0, nil
		case "flatpak":
			return sample(t, "flatpak.txt"), nil, 0, nil
		}
		return nil, nil, -1, errors.New("unexpected command " + name)
	}
}

func TestCheckCollectsAllSources(t *testing.T) {
	svc := NewService(Config{AURHelper: "auto", IncludeFlatpak: true}, goodRunner(t), okPath, nil, nil)
	state := svc.Check(context.Background())
	if state.CheckErr != "" {
		t.Fatalf("unexpected check error: %s", state.CheckErr)
	}
	repo, aur, flatpak := Counts(state.Updates)
	if repo != 3 || aur != 3 || flatpak != 2 {
		t.Fatalf("counts = %d/%d/%d, want 3/3/2 (%+v)", repo, aur, flatpak, state.Updates)
	}
	if state.CheckedAt.IsZero() {
		t.Fatal("CheckedAt should be set after a good check")
	}
	if state.Checking {
		t.Fatal("Checking should be false once the check returns")
	}
}

func TestCheckSingleFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls int
	run := func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		calls++
		close(entered)
		<-release
		return []byte("linux 1 -> 2\n"), nil, 0, nil
	}
	svc := NewService(Config{AURHelper: "off"}, run, okPath, nil, nil)

	done := make(chan State, 1)
	go func() { done <- svc.Check(context.Background()) }()
	<-entered

	second := svc.Check(context.Background())
	if !second.Checking {
		t.Fatal("a second caller while a check runs should see Checking=true")
	}
	if calls != 1 {
		t.Fatalf("runner called %d times during single flight, want 1", calls)
	}

	close(release)
	final := <-done
	if final.Checking || len(final.Updates) != 1 {
		t.Fatalf("final state = %+v", final)
	}
}

func TestCheckErrorKeepsLastGoodList(t *testing.T) {
	fail := false
	run := func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		if fail {
			return nil, []byte("==> ERROR: Cannot fetch updates"), 1, nil
		}
		return sample(t, "checkupdates.txt"), nil, 0, nil
	}
	now := time.Date(2026, 10, 8, 14, 5, 0, 0, time.UTC)
	svc := NewService(Config{AURHelper: "off"}, run, okPath, func() time.Time { return now }, nil)

	first := svc.Check(context.Background())
	if first.CheckErr != "" || len(first.Updates) != 3 {
		t.Fatalf("first check = %+v", first)
	}

	fail = true
	now = now.Add(time.Hour)
	second := svc.Check(context.Background())
	if len(second.Updates) != 3 {
		t.Fatalf("error must keep the last good list, got %+v", second.Updates)
	}
	if !strings.Contains(second.CheckErr, "Cannot fetch updates") {
		t.Fatalf("CheckErr = %q", second.CheckErr)
	}
	if !second.CheckedAt.Equal(first.CheckedAt) {
		t.Fatalf("CheckedAt moved on a failed check: %v -> %v", first.CheckedAt, second.CheckedAt)
	}
}

func TestPersistRestoreRoundTrip(t *testing.T) {
	fresh := NewService(Config{AURHelper: "off"}, goodRunner(t), okPath, nil, nil)
	if data := fresh.PersistedState(); data != nil {
		t.Fatalf("nothing to persist before a check, got %s", data)
	}

	now := time.Date(2026, 10, 8, 14, 5, 0, 0, time.UTC)
	svc := NewService(Config{AURHelper: "off"}, goodRunner(t), okPath, func() time.Time { return now }, nil)
	svc.Check(context.Background())
	data := svc.PersistedState()
	if data == nil {
		t.Fatal("PersistedState should encode the last good result")
	}

	restored := NewService(Config{AURHelper: "off"}, goodRunner(t), okPath, nil, nil)
	restored.RestoreState(data)
	state := restored.State()
	if len(state.Updates) != 3 || !state.CheckedAt.Equal(now) {
		t.Fatalf("restored state = %+v", state)
	}
}

func TestCheckMissingTools(t *testing.T) {
	svc := NewService(Config{AURHelper: "auto", IncludeFlatpak: true}, goodRunner(t), noPath, nil, nil)
	state := svc.Check(context.Background())
	if !state.RepoMissing || !state.AURMissing || !state.FlatpakMissing {
		t.Fatalf("missing flags = repo:%v aur:%v flatpak:%v", state.RepoMissing, state.AURMissing, state.FlatpakMissing)
	}
	if state.CheckErr != "" {
		t.Fatalf("missing tools are not check errors: %q", state.CheckErr)
	}
}

func TestResolveAURHelper(t *testing.T) {
	both := func(name string) (string, error) {
		if name == "paru" || name == "yay" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	onlyYay := func(name string) (string, error) {
		if name == "yay" {
			return "/usr/bin/yay", nil
		}
		return "", errors.New("not found")
	}
	cases := []struct {
		setting string
		look    func(string) (string, error)
		want    string
	}{
		{"auto", both, "paru"},
		{"", both, "paru"},
		{"auto", onlyYay, "yay"},
		{"paru", onlyYay, ""},
		{"yay", onlyYay, "yay"},
		{"off", both, ""},
	}
	for _, c := range cases {
		if got := ResolveAURHelper(c.setting, c.look); got != c.want {
			t.Errorf("ResolveAURHelper(%q) = %q, want %q", c.setting, got, c.want)
		}
	}
}

func TestTakeNotificationOncePerDay(t *testing.T) {
	now := time.Date(2026, 10, 8, 14, 5, 0, 0, time.UTC)
	svc := NewService(Config{AURHelper: "off"}, goodRunner(t), okPath, func() time.Time { return now }, nil)
	svc.Check(context.Background())

	body, ok := svc.TakeNotification(now)
	if !ok || !strings.Contains(body, "3 updates") || !strings.Contains(body, "(2 core)") {
		t.Fatalf("first notification = %q, %v", body, ok)
	}
	if _, ok := svc.TakeNotification(now.Add(time.Hour)); ok {
		t.Fatal("only one notification per day")
	}
	if _, ok := svc.TakeNotification(now.Add(24 * time.Hour)); !ok {
		t.Fatal("a new day should notify again")
	}
}

func TestTakeNotificationRebootFirst(t *testing.T) {
	now := time.Date(2026, 10, 8, 14, 5, 0, 0, time.UTC)
	reboot := func() (bool, string) { return true, "linux 6.17.1 is installed, 6.16.9 is running" }
	svc := NewService(Config{AURHelper: "off"}, goodRunner(t), okPath, func() time.Time { return now }, reboot)
	svc.Check(context.Background())

	body, ok := svc.TakeNotification(now)
	if !ok || !strings.Contains(body, "Restart to finish updating") {
		t.Fatalf("reboot notification = %q, %v", body, ok)
	}
	body, ok = svc.TakeNotification(now)
	if !ok || strings.Contains(body, "Restart") {
		t.Fatalf("second notification = %q, %v; want the update count", body, ok)
	}
	if _, ok := svc.TakeNotification(now); ok {
		t.Fatal("nothing left to notify")
	}
}

func TestTakeNotificationNoUpdates(t *testing.T) {
	svc := NewService(Config{AURHelper: "off"}, func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return nil, nil, 2, nil
	}, okPath, nil, nil)
	svc.Check(context.Background())
	if _, ok := svc.TakeNotification(time.Now()); ok {
		t.Fatal("no updates means no notification")
	}
}

func TestLoopRunsAfterStartDelayAndRepeats(t *testing.T) {
	svc := NewService(Config{AURHelper: "off"}, goodRunner(t), okPath, nil, nil)
	svc.StartDelay = 10 * time.Millisecond
	results := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Loop(ctx, func() time.Duration { return 20 * time.Millisecond }, func() {
		select {
		case results <- struct{}{}:
		default:
		}
	})

	select {
	case <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("loop never ran its first check")
	}
	select {
	case <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("loop never ran its second check")
	}
}

func TestStateCopyCannotBeMutatedThroughCaller(t *testing.T) {
	svc := NewService(Config{AURHelper: "off"}, goodRunner(t), okPath, nil, nil)
	svc.Check(context.Background())
	state := svc.State()
	if len(state.Updates) == 0 {
		t.Fatal("expected updates")
	}
	state.Updates[0].Name = "mutated"
	if svc.State().Updates[0].Name == "mutated" {
		t.Fatal("State must return a copy")
	}
}
