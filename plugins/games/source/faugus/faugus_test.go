package faugus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

// fixture writes a games.json covering the shapes Faugus produces: a Proton
// game, an imported Steam game (identity!), a native game, and a game whose
// executable is gone (uninstalled), plus local covers.
func fixture(t *testing.T) (dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	exe := filepath.Join(dataDir, "Hades.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	games := `[
		{"gameid": "1", "title": "Hades", "path": "` + exe + `", "runner": "Proton-GE Latest",
		 "playtime": 5400, "last_played": "2026-10-01T12:00:00.000000"},
		{"gameid": "2", "title": "Portal 2", "path": "620", "runner": "Steam", "playtime": 0},
		{"gameid": "3", "title": "Native Game", "path": "` + filepath.Join(dataDir, "native") + `", "runner": "Linux-Native"},
		{"gameid": "4", "title": "Gone", "path": "` + filepath.Join(dataDir, "missing.exe") + `", "runner": "Proton-GE Latest"}
	]`
	if err := os.WriteFile(filepath.Join(dataDir, "games.json"), []byte(games), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "covers", "1.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "icons", "2.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func newSource(t *testing.T, o Options) *Source {
	t.Helper()
	if o.Home == "" {
		o.Home = t.TempDir()
	}
	if o.ProcRoot == "" {
		o.ProcRoot = t.TempDir()
	}
	src, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// mustGames is the List snapshot Running is handed, the way the plugin feeds
// one refresh's listing instead of a fresh scan-time read.
func mustGames(t *testing.T, src *Source) []source.Game {
	t.Helper()
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return games
}

func TestNewRequiresGamesJSON(t *testing.T) {
	if _, err := New(Options{DataDirs: []string{t.TempDir()}, Home: t.TempDir()}); err == nil {
		t.Fatal("a data dir without games.json must not be a library")
	}
}

func TestNewPicksFirstDataDirWithGames(t *testing.T) {
	empty, data := t.TempDir(), fixture(t)
	src, err := New(Options{DataDirs: []string{empty, data}, Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if src.dataDir != data {
		t.Fatalf("dataDir = %q, want %q", src.dataDir, data)
	}
}

func TestListMapsGamesJSON(t *testing.T) {
	src := newSource(t, Options{DataDirs: []string{fixture(t)}})
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 4 {
		t.Fatalf("games = %d, want 4: %+v", len(games), games)
	}
	hades := games[0]
	if hades.ID != "1" || hades.Name != "Hades" || hades.Source != "faugus" {
		t.Fatalf("hades = %+v", hades)
	}
	if hades.Runner != "proton" || hades.Platform != "Windows" {
		t.Fatalf("runner/platform = %q/%q", hades.Runner, hades.Platform)
	}
	if hades.PlaytimeSec != 5400 || hades.LastPlayed.IsZero() {
		t.Fatalf("playtime/lastplayed = %v/%v", hades.PlaytimeSec, hades.LastPlayed)
	}
	if !hades.Installed {
		t.Fatal("existing executable must read as installed")
	}
	if filepath.Base(hades.CoverPath) != "1.png" {
		t.Fatalf("cover = %q", hades.CoverPath)
	}
	if hades.Identity != "" {
		t.Fatalf("a Proton game has no store identity, got %q", hades.Identity)
	}

	steam := games[1]
	if steam.Identity != "steam:620" {
		t.Fatalf("imported Steam game identity = %q, want steam:620", steam.Identity)
	}
	if steam.Runner != "steam" {
		t.Fatalf("steam runner label = %q", steam.Runner)
	}
	// A Steam import's "path" is an appid, not a file: it must still launch.
	if !steam.Installed {
		t.Fatal("an imported Steam game must read as installed (the appid is not a file path)")
	}
	if steam.Directory != "" {
		t.Fatalf("an appid must not become an install directory: %q", steam.Directory)
	}
	if filepath.Base(steam.CoverPath) != "2.png" {
		t.Fatalf("icon fallback cover = %q", steam.CoverPath)
	}

	native := games[2]
	if native.Platform != "Linux" || native.Runner != "linux" {
		t.Fatalf("native platform/runner = %q/%q", native.Platform, native.Runner)
	}

	gone := games[3]
	if gone.Installed {
		t.Fatal("a game whose path is missing must read as not installed")
	}
}

func TestLaunchPrefersNativeBinary(t *testing.T) {
	var ran [][]string
	look := map[string]bool{"faugus-launcher": true, "flatpak": true}
	src := newSource(t, Options{
		DataDirs: []string{fixture(t)},
		Run: func(_ context.Context, name string, args ...string) error {
			ran = append(ran, append([]string{name}, args...))
			return nil
		},
		LookPath: func(name string) (string, error) {
			if look[name] {
				return "/usr/bin/" + name, nil
			}
			return "", os.ErrNotExist
		},
	})
	if err := src.Launch(context.Background(), source.Game{ID: "1", Name: "Hades"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran[0], " "); got != "faugus-launcher --game 1" {
		t.Fatalf("launch argv = %q", got)
	}
}

func TestLaunchFallsBackToFlatpak(t *testing.T) {
	var ran [][]string
	src := newSource(t, Options{
		DataDirs: []string{fixture(t)},
		Run: func(_ context.Context, name string, args ...string) error {
			ran = append(ran, append([]string{name}, args...))
			return nil
		},
		LookPath: func(name string) (string, error) {
			if name == "flatpak" {
				return "/usr/bin/flatpak", nil
			}
			return "", os.ErrNotExist
		},
	})
	if err := src.Launch(context.Background(), source.Game{ID: "2", Name: "Portal 2"}); err != nil {
		t.Fatal(err)
	}
	want := "flatpak run io.github.Faugus.faugus-launcher --game 2"
	if got := strings.Join(ran[0], " "); got != want {
		t.Fatalf("launch argv = %q, want %q", got, want)
	}
}

func TestLaunchWithoutFaugusFails(t *testing.T) {
	src := newSource(t, Options{
		DataDirs: []string{fixture(t)},
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	if err := src.Launch(context.Background(), source.Game{ID: "1"}); err == nil {
		t.Fatal("no native binary and no flatpak must fail loudly, not silently")
	}
}

func TestRunningFiltersDeadAndUnknownPids(t *testing.T) {
	data := fixture(t)
	state := t.TempDir()
	// 4242 is live and known; 999 is stale; 777 is alive but not in the library.
	body := `{"1": 4242, "4": 999, "99": 777}`
	if err := os.WriteFile(filepath.Join(state, "running-games.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newSource(t, Options{
		DataDirs:  []string{data},
		StateDirs: []string{state},
		PidAlive:  func(pid int) bool { return pid == 4242 || pid == 777 },
	})
	got, err := src.Running(context.Background(), mustGames(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("running = %+v, want only the live, known game", got)
	}
	if _, ok := got["1"]; !ok {
		t.Fatalf("running = %+v, want game 1", got)
	}
}

func TestRunningMergesNativeAndFlatpakStateDirs(t *testing.T) {
	data := fixture(t)
	native, flatpak := t.TempDir(), t.TempDir()
	// The native state file is stale JSON; the Flatpak one is the truth.
	if err := os.WriteFile(filepath.Join(native, "running-games.json"), []byte(`{"9": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flatpak, "running-games.json"), []byte(`{"2": 4242}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newSource(t, Options{
		DataDirs:  []string{data},
		StateDirs: []string{native, flatpak},
		PidAlive:  func(pid int) bool { return pid == 4242 },
	})
	got, err := src.Running(context.Background(), mustGames(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["2"]; !ok || len(got) != 1 {
		t.Fatalf("running = %+v, want game 2 from the Flatpak state dir", got)
	}
}

// The polite path: SIGUSR1 makes the runner exit on its own; no escalation.
func TestStopSendsSIGUSR1(t *testing.T) {
	data := fixture(t)
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "running-games.json"), []byte(`{"1": 4242}`), 0o644); err != nil {
		t.Fatal(err)
	}
	alive := true
	var signals []syscall.Signal
	var escalated bool
	src := newSource(t, Options{
		DataDirs:  []string{data},
		StateDirs: []string{state},
		PidAlive:  func(pid int) bool { return alive },
		Signal: func(pid int, sig syscall.Signal) error {
			signals = append(signals, sig)
			alive = false // the runner honoured it and exited
			return nil
		},
		StopFn: func(int, time.Duration) error { escalated = true; return nil },
		Grace:  50 * time.Millisecond,
	})
	if err := src.Stop(context.Background(), source.Game{ID: "1", Name: "Hades"}); err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 || signals[0] != syscall.SIGUSR1 {
		t.Fatalf("signals = %v, want one SIGUSR1", signals)
	}
	if escalated {
		t.Fatal("a runner that exited must not be escalated")
	}
}

// The stubborn path: the runner ignores SIGUSR1, so the process group is
// escalated through running.Stop.
func TestStopEscalatesWhenRunnerIgnoresSignal(t *testing.T) {
	data := fixture(t)
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "running-games.json"), []byte(`{"1": 4242}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var escalatedPID int
	src := newSource(t, Options{
		DataDirs:  []string{data},
		StateDirs: []string{state},
		PidAlive:  func(int) bool { return true }, // never dies
		Signal:    func(int, syscall.Signal) error { return nil },
		StopFn: func(pid int, _ time.Duration) error {
			escalatedPID = pid
			return nil
		},
		Grace: 20 * time.Millisecond,
	})
	if err := src.Stop(context.Background(), source.Game{ID: "1", Name: "Hades"}); err != nil {
		t.Fatal(err)
	}
	if escalatedPID != 4242 {
		t.Fatalf("escalated pid = %d, want 4242", escalatedPID)
	}
}

// With no running-file entry, the fallback finds the process through /proc by
// its install directory.
func TestStopFallsBackToProcScan(t *testing.T) {
	data := fixture(t)
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "5150")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte("wine\x00"+data+"/Hades.exe\x00"), 0o444); err != nil {
		t.Fatal(err)
	}
	var stoppedPID int
	src := newSource(t, Options{
		DataDirs: []string{data},
		ProcRoot: procRoot,
		StopFn: func(pid int, _ time.Duration) error {
			stoppedPID = pid
			return nil
		},
	})
	g := source.Game{ID: "1", Name: "Hades", Directory: data}
	if err := src.Stop(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if stoppedPID != 5150 {
		t.Fatalf("stopped pid = %d, want 5150", stoppedPID)
	}
}
