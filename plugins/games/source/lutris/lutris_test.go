package lutris

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	_ "modernc.org/sqlite"
)

// makePGA builds a temp pga.db with the exact Lutris columns; observed in the
// wild: playtime is fractional HOURS, lastplayed is an epoch INTEGER.
func makePGA(t *testing.T, extra func(db *sql.DB)) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pga.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE games (id INTEGER PRIMARY KEY, name TEXT UNIQUE,
		sortname TEXT, slug TEXT, installer_slug TEXT, parent_slug TEXT, platform TEXT,
		runner TEXT, executable TEXT, directory TEXT, updated DATETIME, lastplayed DATETIME,
		installed BOOLEAN, installed_at DATETIME, year TEXT, configpath TEXT,
		has_custom_banner BOOLEAN DEFAULT 0, has_custom_icon BOOLEAN DEFAULT 0,
		has_custom_coverart_big BOOLEAN DEFAULT 0, playtime REAL DEFAULT 0,
		service TEXT, service_id TEXT, discord_id TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO games (name,slug,runner,platform,playtime,lastplayed,installed,directory,year)
		VALUES ('Hades','hades','wine','Windows',5.5,1759295749,1,'/Games/Hades','2020'),
		('Never Played','np','linux',NULL,0,0,1,NULL,''),
		('Ghost','ghost','wine',NULL,0,NULL,0,NULL,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	if extra != nil {
		extra(db)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func TestListReadsGames(t *testing.T) {
	src, err := New(Options{DBPath: makePGA(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 3 {
		t.Fatalf("want 3 rows (installed flag is the caller's filter), got %d", len(games))
	}
	var hades source.Game
	for _, g := range games {
		if g.Name == "Hades" {
			hades = g
		}
	}
	if hades.ID == "" || hades.Runner != "wine" || hades.Platform != "Windows" || hades.Year != "2020" {
		t.Fatalf("bad mapping: %+v", hades)
	}
	if !hades.Installed {
		t.Fatal("Hades should be installed")
	}
	if hades.PlaytimeSec != 5.5*3600 {
		t.Fatalf("playtime hours->seconds: got %v", hades.PlaytimeSec)
	}
	if !hades.LastPlayed.Equal(time.Unix(1759295749, 0)) {
		t.Fatalf("lastplayed epoch parse: got %v", hades.LastPlayed)
	}
	if hades.Source != "lutris" {
		t.Fatalf("source tag: %q", hades.Source)
	}
	for _, g := range games {
		if g.Name == "Ghost" && g.Installed {
			t.Fatal("Ghost must not be installed")
		}
	}
}

func TestCoverResolution(t *testing.T) {
	root := t.TempDir()
	dbPath := makePGA(t, nil)
	lutrisRoot := filepath.Join(root, "lutris")
	if err := os.MkdirAll(filepath.Join(lutrisRoot, "coverart"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lutrisRoot, "coverart", "hades.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := New(Options{DBPath: dbPath, LutrisRoot: lutrisRoot})
	if err != nil {
		t.Fatal(err)
	}
	games, _ := src.List(context.Background())
	for _, g := range games {
		switch g.Name {
		case "Hades":
			if g.CoverPath != filepath.Join(lutrisRoot, "coverart", "hades.jpg") {
				t.Fatalf("cover not resolved: %q", g.CoverPath)
			}
		case "Never Played":
			if g.CoverPath != "" {
				t.Fatalf("unexpected cover: %q", g.CoverPath)
			}
		}
	}
}

func TestLaunchUsesXdgOpen(t *testing.T) {
	var got []string
	src, err := New(Options{DBPath: makePGA(t, nil), Run: func(_ context.Context, name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Launch(context.Background(), source.Game{ID: "7"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "xdg-open lutris:rungameid/7" {
		t.Fatalf("launch argv: %v", got)
	}
}

func TestRunningAndStop(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "4242")
	if err := os.Mkdir(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte("/Games/Hades/run\x00"), 0o444); err != nil {
		t.Fatal(err)
	}
	var stopped []int
	src, err := New(Options{
		DBPath:   makePGA(t, nil),
		ProcRoot: procRoot,
		StopFn:   func(pid int, _ time.Duration) error { stopped = append(stopped, pid); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	running, err := src.Running(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 {
		t.Fatalf("want 1 running, got %v", running)
	}
	var hades source.Game
	for _, g := range mustList(t, src) {
		if g.Name == "Hades" {
			hades = g
		}
		if _, ok := running[g.ID]; ok && g.Name != "Hades" {
			t.Fatalf("wrong game keyed running: %s", g.Name)
		}
	}
	if err := src.Stop(context.Background(), hades); err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0] != 4242 {
		t.Fatalf("stop pids: %v", stopped)
	}
	if err := src.Stop(context.Background(), source.Game{Name: "Never Played", Slug: "np"}); err == nil {
		t.Fatal("stopping a game that is not running must error")
	}
}

func TestRealLibrarySmoke(t *testing.T) {
	db := os.Getenv("SYC_GAMES_TEST_PGA")
	if db == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home")
		}
		db = filepath.Join(home, ".local/share/lutris/pga.db")
	}
	if _, err := os.Stat(db); err != nil {
		t.Skip("no real pga.db")
	}
	src, err := New(Options{DBPath: db})
	if err != nil {
		t.Fatalf("open real pga.db: %v", err)
	}
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) == 0 {
		t.Fatal("real library lists zero games")
	}
	for _, g := range games {
		if g.PlaytimeSec < 0 {
			t.Fatalf("%s: negative playtime %v", g.Name, g.PlaytimeSec)
		}
	}
}

func TestMissingDB(t *testing.T) {
	if _, err := New(Options{DBPath: filepath.Join(t.TempDir(), "pga.db")}); err == nil {
		t.Fatal("missing pga.db must error at New")
	}
}

func mustList(t *testing.T, src *Source) []source.Game {
	t.Helper()
	games, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return games
}
