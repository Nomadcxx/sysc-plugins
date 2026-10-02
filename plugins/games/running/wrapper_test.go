package running

import (
	"testing"
	"time"
)

// Lutris runs every game under lutris-wrapper and retitles it
// "lutris-wrapper: <game name>"; without setproctitle the title is argv[2].
// The wrapper lives as long as Lutris counts the game as running, whatever
// the runner, so it is the signal for Wine prefixes and Steam games alike,
// where no argv names the install directory.
func TestScanWrappersMatchesLutrisTitles(t *testing.T) {
	root := fakeProc(t, map[string]string{
		"100": "lutris-wrapper: Hearts of Iron IV\x00\x00\x00",
		"101": "python3\x00/usr/share/lutris/bin/lutris-wrapper\x00Counter-Strike 2\x000\x000\x00steam\x00",
		"102": "lutris-wrapper: Hades II\x00",
		"103": "/usr/bin/vim\x00lutris-wrapper: Hades\x00",
	})
	got, err := ScanWrappers([]string{"Hearts of Iron IV", "Counter-Strike 2", "Hades", "Hades II"}, root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"Hearts of Iron IV": 100, "Counter-Strike 2": 101, "Hades II": 102}
	if len(got) != len(want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}
	for name, pid := range want {
		if got[name].PID != pid {
			t.Errorf("%s matched pid %d, want %d", name, got[name].PID, pid)
		}
	}
}

func TestStopWrapperTerminates(t *testing.T) {
	cmd := spawn(t, "sleep", "30")
	go func() { _ = StopWrapper(cmd.Process.Pid, time.Second) }()
	waitGone(t, cmd)
}

// A wrapper that ignores SIGTERM is killed once the grace runs out twice.
func TestStopWrapperForcesAStubbornWrapper(t *testing.T) {
	cmd := spawn(t, "sh", "-c", "trap '' TERM; while :; do sleep 0.05; done")
	time.Sleep(100 * time.Millisecond) // let the trap install
	go func() { _ = StopWrapper(cmd.Process.Pid, 200*time.Millisecond) }()
	waitGone(t, cmd)
}

func TestStopWrapperOfAGoneProcessIsNotAnError(t *testing.T) {
	cmd := spawn(t, "true")
	_ = cmd.Wait()
	if err := StopWrapper(cmd.Process.Pid, 100*time.Millisecond); err != nil {
		t.Fatalf("stopping an exited wrapper: %v", err)
	}
}
