// Package faugus reads Faugus Launcher's library and drives it through its
// own CLI. Faugus is a UMU-Launcher front end (Arch AUR, Fedora Copr, Debian
// PPA/.deb, Flatpak and source builds), so every path here exists twice: the
// native XDG locations and the Flatpak sandbox ones under ~/.var/app.
package faugus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/running"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

// flatpakID is the app id Faugus ships under on Flathub.
const flatpakID = "io.github.Faugus.faugus-launcher"

type Options struct {
	// DataDirs are probed in order; the first with a games.json wins.
	DataDirs []string
	// StateDirs are all probed for running-games.json (native and Flatpak
	// installs can disagree about where state lands).
	StateDirs []string
	Home      string // "" = os.UserHomeDir; injectable for tests
	ProcRoot  string // "" = /proc
	Run       func(ctx context.Context, name string, args ...string) error
	// LookPath finds the launcher; "" = exec.LookPath.
	LookPath func(string) (string, error)
	// Signal sends the runner its stop request; "" = syscall.Kill.
	Signal func(pid int, sig syscall.Signal) error
	// PidAlive reports whether a pid from running-games.json is still live.
	PidAlive func(pid int) bool
	StopFn   func(pid int, grace time.Duration) error
	// Grace bounds the polite wait after SIGUSR1 before escalating; 0 = 5s.
	Grace time.Duration
}

type Source struct {
	dataDir string
	state   []string
	proc    string
	grace   time.Duration
	run     func(ctx context.Context, name string, args ...string) error
	look    func(string) (string, error)
	signal  func(pid int, sig syscall.Signal) error
	alive   func(pid int) bool
	stop    func(pid int, grace time.Duration) error
}

func New(o Options) (*Source, error) {
	home := o.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = h
	}
	dataDirs := o.DataDirs
	if len(dataDirs) == 0 {
		dataDirs = defaultDataDirs(home)
	}
	stateDirs := o.StateDirs
	if len(stateDirs) == 0 {
		stateDirs = defaultStateDirs(home)
	}
	dataDir := ""
	for _, d := range dataDirs {
		if _, err := os.Stat(filepath.Join(d, "games.json")); err == nil {
			dataDir = d
			break
		}
	}
	if dataDir == "" {
		return nil, errors.New("faugus library not found")
	}
	if o.ProcRoot == "" {
		o.ProcRoot = "/proc"
	}
	if o.Run == nil {
		o.Run = source.Detach
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Signal == nil {
		o.Signal = syscall.Kill
	}
	if o.PidAlive == nil {
		o.PidAlive = pidAlive
	}
	if o.StopFn == nil {
		o.StopFn = running.Stop
	}
	if o.Grace <= 0 {
		o.Grace = 5 * time.Second
	}
	// The data dir is also a state candidate: some Flatpak layouts keep the
	// whole ~/.local/share/faugus-launcher tree inside the app's data dir.
	state := append([]string{dataDir}, stateDirs...)
	return &Source{
		dataDir: dataDir, state: state, proc: o.ProcRoot, grace: o.Grace,
		run: o.Run, look: o.LookPath, signal: o.Signal, alive: o.PidAlive, stop: o.StopFn,
	}, nil
}

// defaultDataDirs covers native installs (AUR/Copr/.deb), the Flatpak
// sandbox layout, and the legacy pre-2.0 location.
func defaultDataDirs(home string) []string {
	var dirs []string
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "faugus-launcher"))
	}
	dirs = append(dirs,
		filepath.Join(home, ".local/share/faugus-launcher"),
		filepath.Join(home, ".var/app", flatpakID, "data/faugus-launcher"),
		filepath.Join(home, ".config/faugus-launcher"),
	)
	return dirs
}

// defaultStateDirs probes every plausible home for running-games.json.
// Flatpak's XDG_STATE_HOME behaviour varies by version, so several layouts
// are tried; the first file that parses contributes.
func defaultStateDirs(home string) []string {
	var dirs []string
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "faugus-launcher"))
	}
	dirs = append(dirs,
		filepath.Join(home, ".local/state/faugus-launcher"),
		filepath.Join(home, ".var/app", flatpakID, ".local/state/faugus-launcher"),
		filepath.Join(home, ".var/app", flatpakID, "state/faugus-launcher"),
	)
	return dirs
}

func (s *Source) Name() string { return "faugus" }

// entry mirrors one games.json record. Numbers arrive as strings in some
// Faugus versions, so the loose fields are decoded as any and coerced.
type entry struct {
	GameID     any    `json:"gameid"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Runner     string `json:"runner"`
	Playtime   any    `json:"playtime"`
	LastPlayed string `json:"last_played"`
}

func (s *Source) List(ctx context.Context) ([]source.Game, error) {
	raw, err := os.ReadFile(filepath.Join(s.dataDir, "games.json"))
	if err != nil {
		return nil, err
	}
	var entries []entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse faugus games.json: %w", err)
	}
	out := make([]source.Game, 0, len(entries))
	for _, e := range entries {
		id := asString(e.GameID)
		if id == "" {
			continue
		}
		name := e.Title
		if name == "" {
			name = id
		}
		g := source.Game{
			ID:          id,
			Name:        name,
			Slug:        name,
			Runner:      runnerLabel(e.Runner),
			Platform:    platform(e.Runner),
			PlaytimeSec: asFloat(e.Playtime),
			LastPlayed:  parseLastPlayed(e.LastPlayed),
			Executable:  e.Path,
			Source:      "faugus",
		}
		if e.Path != "" && e.Runner != "Steam" {
			g.Directory = filepath.Dir(e.Path)
		}
		switch {
		case e.Path == "":
			// Faugus lists a game without a path only mid-edit; show it.
			g.Installed = true
		case e.Runner == "Steam":
			// An imported Steam game's path is its appid, not a file: the
			// install lives in Steam's own library, so it is always launchable
			// through Faugus.
			g.Installed = true
		default:
			_, statErr := os.Stat(e.Path)
			g.Installed = statErr == nil
		}
		if e.Runner == "Steam" && isAppID(e.Path) {
			g.Identity = "steam:" + e.Path
		}
		g.CoverPath = s.cover(id)
		out = append(out, g)
	}
	return out, nil
}

func (s *Source) cover(id string) string {
	for _, ext := range []string{".png", ".jpg", ".jpeg"} {
		p := filepath.Join(s.dataDir, "covers", id+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, ext := range []string{".png", ".jpg"} {
		p := filepath.Join(s.dataDir, "icons", id+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Launch drives Faugus' own CLI so its per-game settings, Proton choice and
// hooks all apply: `faugus-launcher --game <gameid>`. Native binary first;
// otherwise the Flatpak app, which runs the same runner inside the sandbox.
func (s *Source) Launch(ctx context.Context, g source.Game) error {
	if _, err := s.look("faugus-launcher"); err == nil {
		return s.run(ctx, "faugus-launcher", "--game", g.ID)
	}
	if _, err := s.look("flatpak"); err == nil {
		return s.run(ctx, "flatpak", "run", flatpakID, "--game", g.ID)
	}
	return errors.New("faugus-launcher is not installed (native or Flatpak)")
}

// runningFile is Faugus' own truth: {"<gameid>": <pid>} written by the runner
// while a game is live.
func (s *Source) runningFile() map[string]int {
	out := map[string]int{}
	for _, dir := range s.state {
		raw, err := os.ReadFile(filepath.Join(dir, "running-games.json"))
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for id, v := range m {
			if pid := int(asFloat(v)); pid > 0 {
				out[id] = pid
			}
		}
	}
	return out
}

// Running trusts running-games.json but verifies the pids: the file survives
// a Faugus crash, and a dead pid must not pin a game as running forever. The
// caller's List snapshot says which ids are games at all.
func (s *Source) Running(ctx context.Context, games []source.Game) (map[string]time.Time, error) {
	known := make(map[string]bool, len(games))
	for _, g := range games {
		known[g.ID] = true
	}
	now := time.Now()
	out := map[string]time.Time{}
	for id, pid := range s.runningFile() {
		if known[id] && s.alive(pid) {
			out[id] = now
		}
	}
	return out, nil
}

// Stop asks the runner to end the game the way Faugus' own tray does: SIGUSR1
// makes it save playtime, run post-launch hooks and reap the game. A runner
// that ignores it is escalated through the process group.
func (s *Source) Stop(ctx context.Context, g source.Game) error {
	if pid, ok := s.runningFile()[g.ID]; ok && s.alive(pid) {
		if err := s.signal(pid, syscall.SIGUSR1); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("signal faugus runner: %w", err)
		}
		if goneWithin(s.alive, pid, s.grace) {
			return nil
		}
		return s.stop(pid, s.grace)
	}
	// Not in the running file (started by an older Faugus, or the file was
	// lost): fall back to the install directory.
	dir := g.Directory
	if dir == "" && g.Executable != "" {
		dir = filepath.Dir(g.Executable)
	}
	if dir == "" {
		return fmt.Errorf("%s is not running", g.Name)
	}
	matches, err := running.Scan([]string{dir}, s.proc)
	if err != nil {
		return err
	}
	m, ok := matches[dir]
	if !ok {
		return fmt.Errorf("%s is not running", g.Name)
	}
	return s.stop(m.PID, s.grace)
}

func goneWithin(alive func(int) bool, pid int, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// runnerLabel keeps the caption short: Faugus stores the exact Proton name
// ("Proton-CachyOS Latest"), which is a config detail, not a category.
func runnerLabel(runner string) string {
	switch runner {
	case "":
		return ""
	case "Linux-Native":
		return "linux"
	case "Steam":
		return "steam"
	default:
		return "proton"
	}
}

func platform(runner string) string {
	if runner == "Linux-Native" {
		return "Linux"
	}
	return "Windows"
}

func isAppID(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	}
	return ""
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

// parseLastPlayed accepts Python's datetime.isoformat() (no zone) and
// RFC3339 from any future version that starts writing one.
func parseLastPlayed(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSuffix(s, "Z")); err == nil {
			return t
		}
	}
	return time.Time{}
}
