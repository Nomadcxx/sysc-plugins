package heroic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

// heroicTree builds a temp Heroic config directory with the shapes observed
// in the wild: store_cache install-info and library JSON, sideload_apps, a
// per-game GamesConfig and downloaded icons.
func heroicTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"store_cache/legendary_install_info.json": `{
			"__timestamp": 1730000000,
			"Sugar": {"install_path": "/games/sugar"},
			"NoTitle": {"install_path": "/games/notitle"},
			"Named": {"install_path": "/games/named"},
			"Bare": {},
			"Num": {"install_path": 42},
			"lower": {"install_path": "/games/lower"},
			"Ghost": "not-an-object"
		}`,
		"store_cache/legendary_library.json": `{"library": [
			{"app_name": "Sugar", "title": "A Very Sweet Game", "art_cover": "https://example.invalid/sweet.jpg"},
			{"app_name": "lower", "title": "apple"}
		]}`,
		"store_cache/gog_install_info.json": `{
			"__timestamp": 1,
			"1207658927": {"install_path": "/games/gog"}
		}`,
		"store_cache/gog_library.json":       `{"library": [{"app_name": "1207658927", "title": "GOG Witcher"}]}`,
		"store_cache/nile_install_info.json": `{"amzn1.3434": {"install_path": "/games/amzn"}}`,
		// Older caches store the library as a bare array at the top level.
		"store_cache/nile_library.json": `[{"app_name": "amzn1.3434", "title": "Amazon Thing"}]`,
		"sideload_apps/library.json": `{"games": [
			{"app_name": "side-installed", "title": "Sideload Installed", "is_installed": true, "art_cover": "/covers/side.png"},
			{"app_name": "side-zebra", "title": "zebra", "is_installed": true},
			{"app_name": "side-uninstalled", "title": "Sideload Gone", "is_installed": false, "art_cover": "/covers/gone.png"}
		]}`,
		"GamesConfig/NoTitle.json": `{"title": "Config Title", "name": "Ignored Name"}`,
		"GamesConfig/Named.json":   `{"name": "Named Fallback"}`,
		"icons/Sugar.jpg":          "jpeg-bytes",
		"icons/side-installed.jpg": "jpeg-bytes",
	})
}

func TestListStores(t *testing.T) {
	src := mustNew(t, Options{ConfigDirs: []string{heroicTree(t)}})
	games := mustList(t, src)

	var names []string
	for _, g := range games {
		names = append(names, g.Name)
	}
	want := []string{
		// Case-insensitive sort: only lowercasing puts "apple" between
		// "amazon thing" and "bare" instead of after "Sideload Installed".
		"A Very Sweet Game", "Amazon Thing", "apple", "Bare", "Config Title",
		"GOG Witcher", "Named Fallback", "Num", "Sideload Installed", "zebra",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("names:\n got %v\nwant %v", names, want)
	}

	byID := map[string]source.Game{}
	for _, g := range games {
		if g.Slug != g.Name || g.Source != "heroic" || g.Platform != "Windows" || !g.Installed {
			t.Errorf("%s: base mapping: %+v", g.ID, g)
		}
		byID[g.ID] = g
	}

	for id, want := range map[string]struct {
		name      string
		runner    string
		identity  string
		directory string
	}{
		"Sugar":          {"A Very Sweet Game", "epic", "epic:Sugar", "/games/sugar"},
		"1207658927":     {"GOG Witcher", "gog", "gog:1207658927", "/games/gog"},
		"amzn1.3434":     {"Amazon Thing", "amazon", "amazon:amzn1.3434", "/games/amzn"},
		"Bare":           {"Bare", "epic", "epic:Bare", ""},
		"Num":            {"Num", "epic", "epic:Num", ""},
		"side-installed": {"Sideload Installed", "sideload", "", ""},
	} {
		g, ok := byID[id]
		if !ok {
			t.Errorf("%s missing from List", id)
			continue
		}
		if g.Name != want.name || g.Runner != want.runner || g.Identity != want.identity || g.Directory != want.directory {
			t.Errorf("%s: got %+v", id, g)
		}
	}

	for _, absent := range []string{"Ghost", "__timestamp", "side-uninstalled"} {
		if _, ok := byID[absent]; ok {
			t.Errorf("%s must not be listed", absent)
		}
	}
}

func TestListTitleFallbacks(t *testing.T) {
	src := mustNew(t, Options{ConfigDirs: []string{heroicTree(t)}})
	names := map[string]string{}
	for _, g := range mustList(t, src) {
		names[g.ID] = g.Name
	}
	// GamesConfig title wins over its name field; then name; then the app id.
	for id, want := range map[string]string{
		"NoTitle": "Config Title",
		"Named":   "Named Fallback",
		"Bare":    "Bare",
	} {
		if names[id] != want {
			t.Errorf("%s: name %q, want %q", id, names[id], want)
		}
	}
}

func TestListSideloadInstalledOnly(t *testing.T) {
	src := mustNew(t, Options{ConfigDirs: []string{heroicTree(t)}})
	var side []source.Game
	for _, g := range mustList(t, src) {
		if g.Runner == "sideload" {
			side = append(side, g)
		}
	}
	if len(side) != 2 {
		t.Fatalf("sideload games: %+v", side)
	}
	for _, g := range side {
		if g.ID == "side-uninstalled" {
			t.Fatal("uninstalled sideload game listed")
		}
		if g.Identity != "" {
			t.Errorf("%s: sideload identity %q, want empty", g.ID, g.Identity)
		}
		if !g.Installed || g.Platform != "Windows" || g.Source != "heroic" {
			t.Errorf("%s: mapping %+v", g.ID, g)
		}
	}
}

func TestCoverPath(t *testing.T) {
	root := heroicTree(t)
	src := mustNew(t, Options{ConfigDirs: []string{root}})
	covers := map[string]string{}
	for _, g := range mustList(t, src) {
		covers[g.ID] = g.CoverPath
	}
	if got, want := covers["Sugar"], filepath.Join(root, "icons", "Sugar.jpg"); got != want {
		t.Errorf("store cover: %q, want %q", got, want)
	}
	if got, want := covers["side-installed"], filepath.Join(root, "icons", "side-installed.jpg"); got != want {
		t.Errorf("sideload cover: %q, want %q", got, want)
	}
	if covers["Bare"] != "" {
		t.Errorf("no icon on disk, cover: %q", covers["Bare"])
	}
}

func TestLaunchArgv(t *testing.T) {
	var calls []string
	src := mustNew(t, Options{
		ConfigDirs: []string{heroicTree(t)},
		Run: func(_ context.Context, name string, args ...string) error {
			calls = append(calls, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	})
	// Launch the games List produced, so the Runner that selects the URI
	// namespace is the one the backend itself mapped.
	for _, g := range mustList(t, src) {
		if g.ID == "Sugar" || g.ID == "side-installed" {
			if err := src.Launch(context.Background(), g); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := []string{
		"xdg-open heroic://launch/Sugar",
		"xdg-open heroic://launch/sideload/side-installed",
	}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("launch argv:\n got %v\nwant %v", calls, want)
	}
}

func TestLaunchEscapesSideloadID(t *testing.T) {
	var got string
	src := mustNew(t, Options{
		ConfigDirs: []string{t.TempDir()},
		Run: func(_ context.Context, name string, args ...string) error {
			got = strings.Join(append([]string{name}, args...), " ")
			return nil
		},
	})
	if err := src.Launch(context.Background(), source.Game{ID: "Sugar II/100%?", Runner: "sideload"}); err != nil {
		t.Fatal(err)
	}
	if want := "xdg-open heroic://launch/sideload/Sugar%20II%2F100%25%3F"; got != want {
		t.Fatalf("launch argv = %q, want %q", got, want)
	}
}

func TestRunningAndStop(t *testing.T) {
	procRoot := t.TempDir()
	writeProc(t, procRoot, 4242, "/games/sugar/bin/sugar\x00--fullscreen\x00")

	var stopped []int
	var grace time.Duration
	src := mustNew(t, Options{
		ConfigDirs: []string{heroicTree(t)},
		ProcRoot:   procRoot,
		StopFn: func(pid int, g time.Duration) error {
			stopped = append(stopped, pid)
			grace = g
			return nil
		},
	})

	live, err := src.Running(context.Background(), mustList(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatalf("running: %v", live)
	}
	if start, ok := live["Sugar"]; !ok || start.IsZero() {
		t.Fatalf("running keyed by app id: %v", live)
	}

	var sugar source.Game
	for _, g := range mustList(t, src) {
		if g.ID == "Sugar" {
			sugar = g
		}
	}
	if err := src.Stop(context.Background(), sugar); err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0] != 4242 || grace != 5*time.Second {
		t.Fatalf("stop: pids=%v grace=%v", stopped, grace)
	}

	// Nothing names the directory: not this backend's to stop.
	if err := src.Stop(context.Background(), source.Game{ID: "Bare", Directory: "/games/bare"}); !errors.Is(err, source.ErrNotImplemented) {
		t.Fatalf("stop without a match: %v", err)
	}
	if err := src.Stop(context.Background(), source.Game{ID: "Bare"}); !errors.Is(err, source.ErrNotImplemented) {
		t.Fatalf("stop without a directory: %v", err)
	}
}

func TestNewErrorsWithoutConfigDir(t *testing.T) {
	if _, err := New(Options{ConfigDirs: []string{filepath.Join(t.TempDir(), "missing")}}); err == nil {
		t.Fatal("missing config dir must error at New")
	}
	file := filepath.Join(t.TempDir(), "heroic")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{ConfigDirs: []string{file}}); err == nil {
		t.Fatal("a plain file is not a config dir")
	}
}

func TestNewProbesConfigDirsInOrder(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	if _, err := New(Options{}); err == nil {
		t.Fatal("no default dir exists yet, New must fail")
	}

	// Escalate from the last default to the first; each new dir must win.
	xdgHeroic := filepath.Join(xdg, "heroic")
	native := filepath.Join(home, ".config", "heroic")
	flatpak := filepath.Join(home, ".var", "app", "com.heroicgameslauncher.hgl", "config", "heroic")
	for _, want := range []string{flatpak, native, xdgHeroic} {
		if err := os.MkdirAll(want, 0o755); err != nil {
			t.Fatal(err)
		}
		if src := mustNew(t, Options{}); src.root != want {
			t.Fatalf("root = %q, want %q", src.root, want)
		}
	}
}

func TestReadLibraryShapes(t *testing.T) {
	cases := map[string]string{
		"object with library": `{"library": [{"app_name": "a", "title": "Alpha"}], "__timestamp": 1}`,
		"bare array":          `[{"app_name": "a", "title": "Alpha"}]`,
		"object keyed by id":  `{"a": {"title": "Alpha"}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "library.json")
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readLibrary(p)["a"]; got != "Alpha" {
				t.Errorf("title: %q, want %q", got, "Alpha")
			}
		})
	}
}

func TestRunningWithoutInstallPaths(t *testing.T) {
	src := mustNew(t, Options{
		ConfigDirs: []string{t.TempDir()}, // a Heroic config with nothing in it
		ProcRoot:   filepath.Join(t.TempDir(), "nowhere"),
	})
	live, err := src.Running(context.Background(), mustList(t, src))
	if err != nil {
		t.Fatalf("Running must stay silent: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("running: %v", live)
	}
}

// writeTree writes a Heroic config tree: root is a fresh t.TempDir(), files
// maps slash-separated relative paths to contents and everything is written
// with os.WriteFile.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// writeProc fakes one /proc entry: <procRoot>/<pid>/cmdline with NUL-separated
// argv entries, exactly what running.Scan reads.
func writeProc(t *testing.T, procRoot string, pid int, cmdline string) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o444); err != nil {
		t.Fatal(err)
	}
}

func mustNew(t *testing.T, o Options) *Source {
	t.Helper()
	src, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func mustList(t *testing.T, src *Source) []source.Game {
	t.Helper()
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return games
}
