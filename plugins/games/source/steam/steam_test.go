package steam

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

type app struct{ id, name, installDir string }

// listedGames is the List snapshot Running is handed, the way the plugin
// feeds it one refresh's listing instead of a fresh scan-time read.
func listedGames(t *testing.T, src *Source) []source.Game {
	t.Helper()
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return games
}

// makeLibrary lays out a library the way Steam does: an appmanifest per app
// under steamapps and the install tree under steamapps/common. The manifests
// keep appid/name/installdir up top and nested blocks below, like the real
// thing, so the scanner has to see through the nesting. The steamapps dir is
// returned so a test can drop a libraryfolders.vdf beside the manifests.
func makeLibrary(t *testing.T, root string, apps ...app) string {
	t.Helper()
	sa := filepath.Join(root, "steamapps")
	if err := os.MkdirAll(filepath.Join(sa, "common"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if err := os.MkdirAll(filepath.Join(sa, "common", a.installDir), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "\"AppState\"\n" +
			"{\n" +
			"\t\"appid\"\t\t\"" + a.id + "\"\n" +
			"\t\"Universe\"\t\t\"1\"\n" +
			"\t\"name\"\t\t\"" + a.name + "\"\n" +
			"\t\"StateFlags\"\t\t\"4\"\n" +
			"\t\"installdir\"\t\t\"" + a.installDir + "\"\n" +
			"\t\"LastUpdated\"\t\t\"1759295749\"\n" +
			"\t\"SizeOnDisk\"\t\t\"1073741824\"\n" +
			"\t\"UserConfig\"\n" +
			"\t{\n" +
			"\t\t\"language\"\t\t\"english\"\n" +
			"\t\t\"BetaKey\"\t\t\"\"\n" +
			"\t}\n" +
			"\t\"InstalledDepots\"\n" +
			"\t{\n" +
			"\t\t\"" + a.id + "\"\n" +
			"\t\t{\n" +
			"\t\t\t\"manifest\"\t\t\"99\"\n" +
			"\t\t\t\"size\"\t\t\"1073741824\"\n" +
			"\t\t}\n" +
			"\t}\n" +
			"}\n"
		writeManifest(t, sa, "appmanifest_"+a.id+".acf", body)
	}
	return sa
}

func writeManifest(t *testing.T, steamapps, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(steamapps, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeLibraryFolders writes libraryfolders.vdf as Steam does: one numbered
// block per library root, each with a "path" and a nested "apps" block.
func writeLibraryFolders(t *testing.T, steamapps string, paths ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("\"libraryfolders\"\n{\n")
	for i, p := range paths {
		fmt.Fprintf(&b, "\t\"%d\"\n\t{\n\t\t\"path\"\t\t\"%s\"\n\t\t\"label\"\t\t\"\"\n\t\t\"totalsize\"\t\t\"0\"\n\t\t\"apps\"\n\t\t{\n\t\t\t\"228980\"\t\t\"1024\"\n\t\t}\n\t}\n", i, p)
	}
	b.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(steamapps, "libraryfolders.vdf"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRegistry writes the fragment of registry.vdf that says which app Steam
// considers running.
func writeRegistry(t *testing.T, root, appid string) {
	t.Helper()
	body := "\"Registry\"\n" +
		"{\n" +
		"\t\"HKCU\"\n" +
		"\t{\n" +
		"\t\t\"Software\"\n" +
		"\t\t{\n" +
		"\t\t\t\"Valve\"\n" +
		"\t\t\t{\n" +
		"\t\t\t\t\"Steam\"\n" +
		"\t\t\t\t{\n" +
		"\t\t\t\t\t\"RunningAppID\"\t\t\"" + appid + "\"\n" +
		"\t\t\t\t\t\"LaunchOnStartup\"\t\t\"1\"\n" +
		"\t\t\t\t}\n" +
		"\t\t\t}\n" +
		"\t\t}\n" +
		"\t}\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(root, "registry.vdf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeProc builds a /proc-shaped tree holding one live process whose argv
// names dir, which is how a wrapper around a game shows up to running.Scan.
func fakeProc(t *testing.T, pid int, dir string) string {
	t.Helper()
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.Mkdir(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// /proc/<pid>/cmdline is NUL-separated argv.
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte(filepath.Join(dir, "run.sh")+"\x00"), 0o444); err != nil {
		t.Fatal(err)
	}
	return procRoot
}

// Both libraries are listed: the probed root and the one named by
// libraryfolders.vdf. The root appears in the vdf again (as Steam writes it),
// so the listing also proves deduplication; the incomplete manifest proves
// appid/name/installdir-less entries are skipped.
func TestListTwoLibraries(t *testing.T) {
	root := t.TempDir()
	sa := makeLibrary(t, root, app{id: "570", name: "Dota 2", installDir: "dota 2 beta"})
	writeManifest(t, sa, "appmanifest_999.acf", "\"AppState\"\n{\n\t\"appid\"\t\t\"999\"\n\t\"name\"\t\t\"No Install Dir\"\n}\n")

	extra := t.TempDir()
	makeLibrary(t, extra, app{id: "1145360", name: "Hades", installDir: "Hades"})
	writeLibraryFolders(t, sa, root, extra)

	src, err := New(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("want installed games from both libraries, got %+v", games)
	}
	if games[0].Name != "Dota 2" || games[1].Name != "Hades" {
		t.Fatalf("not sorted by name: %q, %q", games[0].Name, games[1].Name)
	}
	dota, hades := games[0], games[1]
	if dota.ID != "570" || dota.Slug != "Dota 2" || dota.Runner != "steam" ||
		dota.Platform != "Windows" || !dota.Installed || dota.Identity != "steam:570" || dota.Source != "steam" {
		t.Fatalf("bad mapping: %+v", dota)
	}
	if want := filepath.Join(root, "steamapps", "common", "dota 2 beta"); dota.Directory != want {
		t.Fatalf("Directory = %q, want %q", dota.Directory, want)
	}
	if want := filepath.Join(extra, "steamapps", "common", "Hades"); hades.Directory != want {
		t.Fatalf("extra library Directory = %q, want %q", hades.Directory, want)
	}
}

// Runtimes and tools Steam installs as apps are skipped, whatever their case.
func TestExcludedRuntimeNamesSkipped(t *testing.T) {
	root := t.TempDir()
	makeLibrary(t, root,
		app{id: "570", name: "Dota 2", installDir: "dota 2 beta"},
		app{id: "1493710", name: "Proton Experimental", installDir: "Proton - Experimental"},
		app{id: "1070560", name: "Steam Linux Runtime 3.0 (sniper)", installDir: "SteamLinuxRuntime_sniper"},
		app{id: "250820", name: "SteamVR", installDir: "SteamVR"},
		app{id: "228980", name: "Steamworks Common Redistributables", installDir: "Steamworks Shared"},
		app{id: "1007", name: "Steam Client Beta", installDir: "SteamClientBeta"},
	)
	src, err := New(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].ID != "570" {
		t.Fatalf("runtimes and tools must be skipped, got %+v", games)
	}
}

// appcache/librarycache/<appid>/library_600x900.jpg wins; the flat
// <appid>_header.jpg is the fallback, and a game with no cached art gets "".
func TestCoverResolution(t *testing.T) {
	root := t.TempDir()
	makeLibrary(t, root,
		app{id: "570", name: "Dota 2", installDir: "dota 2 beta"},
		app{id: "1145360", name: "Hades", installDir: "Hades"},
		app{id: "620", name: "Portal 2", installDir: "Portal 2"},
	)
	portrait := filepath.Join(root, "appcache", "librarycache", "570", "library_600x900.jpg")
	header := filepath.Join(root, "appcache", "librarycache", "1145360_header.jpg")
	for _, p := range []string{portrait, header} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("jpeg"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	src, err := New(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"570": portrait, "1145360": header, "620": ""}
	for _, g := range games {
		if g.CoverPath != want[g.ID] {
			t.Fatalf("%s cover: got %q, want %q", g.Name, g.CoverPath, want[g.ID])
		}
	}
}

func TestLaunchUsesSteamURI(t *testing.T) {
	var got []string
	root := t.TempDir()
	makeLibrary(t, root, app{id: "570", name: "Dota 2", installDir: "dota 2 beta"})
	src, err := New(Options{
		Roots: []string{root},
		Run: func(_ context.Context, name string, args ...string) error {
			got = append([]string{name}, args...)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Launch(context.Background(), source.Game{ID: "570", Name: "Dota 2"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "xdg-open steam://rungameid/570" {
		t.Fatalf("launch argv: %v", got)
	}
}

// Without a registry.vdf, a live process whose argv names the install dir is
// what shows a game running.
func TestRunningFallsBackToProcScan(t *testing.T) {
	root := t.TempDir()
	makeLibrary(t, root, app{id: "570", name: "Dota 2", installDir: "dota 2 beta"})
	gameDir := filepath.Join(root, "steamapps", "common", "dota 2 beta")
	procRoot := fakeProc(t, 4242, gameDir)

	src, err := New(Options{Roots: []string{root}, ProcRoot: procRoot})
	if err != nil {
		t.Fatal(err)
	}
	live, err := src.Running(context.Background(), listedGames(t, src))
	if err != nil {
		t.Fatal(err)
	}
	start, ok := live["570"]
	if !ok || len(live) != 1 {
		t.Fatalf("want only 570 live, got %v", live)
	}
	if start.IsZero() {
		t.Fatal("start must come from the process")
	}
}

// registry.vdf wins when it names a running app: the start time is the
// process's, a vanished process falls back to now(), "0" means idle, and an
// unparsable value falls back to the scan.
func TestRunningPrefersRegistry(t *testing.T) {
	root := t.TempDir()
	makeLibrary(t, root, app{id: "570", name: "Dota 2", installDir: "dota 2 beta"})
	gameDir := filepath.Join(root, "steamapps", "common", "dota 2 beta")
	writeRegistry(t, root, "570")

	procRoot := fakeProc(t, 4242, gameDir)
	info, err := os.Stat(filepath.Join(procRoot, "4242"))
	if err != nil {
		t.Fatal(err)
	}

	src, err := New(Options{Roots: []string{root}, ProcRoot: procRoot})
	if err != nil {
		t.Fatal(err)
	}
	live, err := src.Running(context.Background(), listedGames(t, src))
	if err != nil {
		t.Fatal(err)
	}
	start, ok := live["570"]
	if !ok || len(live) != 1 {
		t.Fatalf("registry says 570 is running: %v", live)
	}
	if !start.Equal(info.ModTime()) {
		t.Fatalf("registry path must date the process: got %v, want %v", start, info.ModTime())
	}

	// The process is gone but the registry still names it: running, un-dated.
	gone, err := New(Options{Roots: []string{root}, ProcRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	live, err = gone.Running(context.Background(), listedGames(t, gone))
	if err != nil {
		t.Fatal(err)
	}
	if start, ok := live["570"]; !ok || time.Since(start) > time.Minute {
		t.Fatalf("unfound process must fall back to now: %v", live)
	}

	// "0" means nothing is running; no scan needed.
	writeRegistry(t, root, "0")
	live, err = src.Running(context.Background(), listedGames(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("RunningAppID 0 must be empty, got %v", live)
	}

	// An unparsable value falls back to the scan, which sees the process.
	writeRegistry(t, root, "")
	live, err = src.Running(context.Background(), listedGames(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := live["570"]; !ok || len(live) != 1 {
		t.Fatalf("unparsable RunningAppID must fall back to the scan: %v", live)
	}
}

// Stop signals the matched process's group with a 5s grace; with no live
// match or no directory there is nothing this backend can stop.
func TestStopSignalsTheMatchedProcess(t *testing.T) {
	root := t.TempDir()
	makeLibrary(t, root, app{id: "570", name: "Dota 2", installDir: "dota 2 beta"})
	gameDir := filepath.Join(root, "steamapps", "common", "dota 2 beta")
	procRoot := fakeProc(t, 4242, gameDir)

	var stopped []int
	var grace time.Duration
	src, err := New(Options{
		Roots:    []string{root},
		ProcRoot: procRoot,
		StopFn:   func(pid int, g time.Duration) error { stopped = append(stopped, pid); grace = g; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Stop(context.Background(), games[0]); err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0] != 4242 || grace != 5*time.Second {
		t.Fatalf("stop = %v after %v, want 4242 after 5s", stopped, grace)
	}

	notRunning := source.Game{ID: "1145360", Directory: filepath.Join(root, "steamapps", "common", "Hades")}
	if err := src.Stop(context.Background(), notRunning); !errors.Is(err, source.ErrNotImplemented) {
		t.Fatalf("no live match must return ErrNotImplemented, got %v", err)
	}
	if err := src.Stop(context.Background(), source.Game{ID: "570"}); !errors.Is(err, source.ErrNotImplemented) {
		t.Fatalf("games without a directory must return ErrNotImplemented, got %v", err)
	}
}

// Probing walks past existing-but-steamless dirs to the first real Steam root;
// with none found it still opens the first existing dir as an empty library.
func TestNewProbesForARootWithSteamapps(t *testing.T) {
	plain := t.TempDir() // exists, but has no steamapps
	lib := t.TempDir()
	makeLibrary(t, lib, app{id: "570", name: "Dota 2", installDir: "dota 2 beta"})

	src, err := New(Options{Roots: []string{plain, lib}})
	if err != nil {
		t.Fatal(err)
	}
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].ID != "570" {
		t.Fatalf("must probe past a steamless dir to the real root: %+v", games)
	}

	src, err = New(Options{Roots: []string{plain}})
	if err != nil {
		t.Fatalf("first existing dir must be a usable fallback: %v", err)
	}
	if games, err = src.List(context.Background()); err != nil || len(games) != 0 {
		t.Fatalf("empty library: %v, %v", games, err)
	}
}

func TestNewErrorsWhenNoRootExists(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-steam-here")
	if _, err := New(Options{Roots: []string{missing}}); err == nil {
		t.Fatal("New must error when no candidate root exists")
	}
}
