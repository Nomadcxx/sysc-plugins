// Package lutris reads Lutris' pga.db directly — no python, no shelling —
// and drives it through xdg-open's lutris: URIs.
package lutris

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/running"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	_ "modernc.org/sqlite"
)

type Options struct {
	DBPath     string // default ~/.local/share/lutris/pga.db
	LutrisRoot string // default ~/.local/share/lutris (coverart lives here)
	ProcRoot   string // default /proc; injectable for tests
	Run        func(ctx context.Context, name string, args ...string) error
	StopFn     func(pid int, grace time.Duration) error
}

type Source struct {
	db         *sql.DB
	lutrisRoot string
	procRoot   string
	run        func(ctx context.Context, name string, args ...string) error
	stop       func(pid int, grace time.Duration) error
}

func New(o Options) (*Source, error) {
	if o.DBPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		o.LutrisRoot = filepath.Join(home, ".local/share/lutris")
		o.DBPath = filepath.Join(o.LutrisRoot, "pga.db")
	}
	if o.LutrisRoot == "" {
		o.LutrisRoot = filepath.Dir(o.DBPath)
	}
	if o.ProcRoot == "" {
		o.ProcRoot = "/proc"
	}
	if o.Run == nil {
		o.Run = func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		}
	}
	if o.StopFn == nil {
		o.StopFn = running.Stop
	}
	// Read-only so a running Lutris is never blocked, busy_timeout so a
	// concurrent write fails fast instead of hanging the panel.
	db, err := sql.Open("sqlite", "file:"+o.DBPath+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open lutris library: %w", err)
	}
	return &Source{db: db, lutrisRoot: o.LutrisRoot, procRoot: o.ProcRoot, run: o.Run, stop: o.StopFn}, nil
}

func (s *Source) Name() string { return "lutris" }

func (s *Source) List(ctx context.Context) ([]source.Game, error) {
	rows, err := s.db.QueryContext(ctx, `select id, name, slug,
		coalesce(runner,''), coalesce(platform,''), coalesce(year,''),
		coalesce(playtime,0), lastplayed, coalesce(directory,''),
		coalesce(executable,''), coalesce(configpath,''), coalesce(installed,0)
		from games order by sortname collate nocase`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []source.Game
	for rows.Next() {
		var (
			id         int64
			g          source.Game
			playtime   float64
			lastplayed any
			installed  int
		)
		if err := rows.Scan(&id, &g.Name, &g.Slug, &g.Runner, &g.Platform, &g.Year,
			&playtime, &lastplayed, &g.Directory, &g.Executable, &g.ConfigPath, &installed); err != nil {
			return nil, err
		}
		g.ID = strconv.FormatInt(id, 10)
		g.PlaytimeSec = playtime * 3600 // the column is hours, the UI thinks seconds
		g.LastPlayed = parseEpoch(lastplayed)
		g.Installed = installed != 0
		g.Source = "lutris"
		g.CoverPath = s.cover(g.Slug)
		out = append(out, g)
	}
	return out, rows.Err()
}

// Lutris stores lastplayed as epoch seconds; older installers wrote datetime
// strings. Accept both, zero time for NULL/0.
func parseEpoch(v any) time.Time {
	switch x := v.(type) {
	case int64:
		if x == 0 {
			return time.Time{}
		}
		return time.Unix(x, 0)
	case float64:
		if x == 0 {
			return time.Time{}
		}
		return time.Unix(int64(x), 0)
	case string:
		if t, err := strconv.ParseFloat(x, 64); err == nil && t > 0 {
			return time.Unix(int64(t), 0)
		}
		if t, err := time.Parse("2006-01-02 15:04:05", x); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *Source) cover(slug string) string {
	for _, ext := range []string{".jpg", ".png"} {
		p := filepath.Join(s.lutrisRoot, "coverart", slug+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (s *Source) Launch(ctx context.Context, g source.Game) error {
	return s.run(ctx, "xdg-open", "lutris:rungameid/"+g.ID)
}

// Stop needs the live pid, which only the scan knows; Lookup by ID re-lists
// (20-game library, one query — cheaper than caching a whole index).
func (s *Source) Stop(ctx context.Context, g source.Game) error {
	if g.Directory == "" {
		g.Directory = s.directory(ctx, g.ID)
	}
	if g.Directory == "" {
		return errors.New("game has no install directory")
	}
	matches, err := running.Scan([]string{g.Directory}, s.procRoot)
	if err != nil {
		return err
	}
	m, ok := matches[g.Directory]
	if !ok {
		return fmt.Errorf("%s is not running", g.Name)
	}
	return s.stop(m.PID, 5*time.Second)
}

func (s *Source) directory(ctx context.Context, id string) string {
	var dir string
	err := s.db.QueryRowContext(ctx, `select coalesce(directory,'') from games where id = ?`, id).Scan(&dir)
	if err != nil {
		return ""
	}
	return dir
}

func (s *Source) Running(ctx context.Context) (map[string]time.Time, error) {
	games, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	byDir := map[string]string{}
	var dirs []string
	for _, g := range games {
		if g.Directory != "" {
			byDir[g.Directory] = g.ID
			dirs = append(dirs, g.Directory)
		}
	}
	matches, err := running.Scan(dirs, s.procRoot)
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for dir, m := range matches {
		out[byDir[dir]] = m.Start
	}
	return out, nil
}
