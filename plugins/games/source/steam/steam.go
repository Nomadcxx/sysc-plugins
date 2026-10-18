// Package steam reads the local Steam library straight off disk and drives it
// through steam:// URIs — no client API, no protobufs, no shelling:
//
//   - libraryfolders.vdf names every library root (the primary one usually
//     reappears in it as library "0");
//   - appmanifest_<appid>.acf under a library's steamapps dir describes an
//     installed app;
//   - registry.vdf says which app the client currently has running.
//
// Games carry their bare appid as ID; the source set namespaces it on the way
// out. Launching goes through xdg-open's steam:// handler, which native and
// Flatpak Steam both register, so this package never learns how Steam was
// installed.
package steam

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/running"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

type Options struct {
	Roots    []string // Steam roots to probe in order; defaults below
	ProcRoot string   // default /proc; injectable for tests
	Run      func(ctx context.Context, name string, args ...string) error
	StopFn   func(pid int, grace time.Duration) error
}

// defaultRoots is probed in order when Options.Roots is empty: the two stock
// native locations (usually one symlinked directory), the older XDG location,
// and Flatpak Steam's private store, all relative to $HOME.
var defaultRoots = []string{
	".steam/steam",
	".steam/root",
	".local/share/Steam",
	".var/app/com.valvesoftware.Steam/.local/share/Steam",
}

type Source struct {
	root     string
	procRoot string
	run      func(ctx context.Context, name string, args ...string) error
	stop     func(pid int, grace time.Duration) error
}

func New(o Options) (*Source, error) {
	if o.ProcRoot == "" {
		o.ProcRoot = "/proc"
	}
	if o.Run == nil {
		o.Run = source.Detach
	}
	if o.StopFn == nil {
		o.StopFn = running.Stop
	}
	roots := o.Roots
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("steam: resolve home: %w", err)
		}
		for _, r := range defaultRoots {
			roots = append(roots, filepath.Join(home, r))
		}
	}
	root, err := probe(roots)
	if err != nil {
		return nil, err
	}
	return &Source{root: root, procRoot: o.ProcRoot, run: o.Run, stop: o.StopFn}, nil
}

// probe picks the root to read from: the first candidate that is a real Steam
// root (a directory with steamapps), else the first candidate that exists at
// all — a fresh install with no games yet still opens as an empty library.
func probe(roots []string) (string, error) {
	var firstExisting string
	for _, r := range roots {
		if r == "" {
			continue
		}
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			continue
		}
		if firstExisting == "" {
			firstExisting = r
		}
		if fi, err := os.Stat(filepath.Join(r, "steamapps")); err == nil && fi.IsDir() {
			return r, nil
		}
	}
	if firstExisting != "" {
		return firstExisting, nil
	}
	return "", fmt.Errorf("steam: no steam root exists among %s", strings.Join(roots, ", "))
}

func (s *Source) Name() string { return "steam" }

func (s *Source) List(ctx context.Context) ([]source.Game, error) {
	var out []source.Game
	for _, apps := range s.libraries() {
		entries, err := os.ReadDir(apps)
		if err != nil {
			continue // a library that vanished between probing and reading is not a failure
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasPrefix(name, "appmanifest_") || !strings.HasSuffix(name, ".acf") {
				continue
			}
			if g, ok := s.game(filepath.Join(apps, name), apps); ok {
				out = append(out, g)
			}
		}
	}
	// On-disk order is install order; the UI wants a stable alphabetical list.
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// game maps one appmanifest into a Game. A manifest missing an appid, name or
// installdir is mid-write or broken; runtimes are filtered by name.
func (s *Source) game(path, apps string) (source.Game, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return source.Game{}, false
	}
	appid, _ := kvValue(raw, "appid")
	name, _ := kvValue(raw, "name")
	installdir, _ := kvValue(raw, "installdir")
	if appid == "" || name == "" || installdir == "" || excluded(name) {
		return source.Game{}, false
	}
	return source.Game{
		ID:        appid,
		Name:      name,
		Slug:      name,
		Runner:    "steam",
		Platform:  "Windows",
		Installed: true,
		Directory: filepath.Join(apps, "common", installdir),
		CoverPath: cover(filepath.Dir(apps), appid),
		Source:    "steam",
		Identity:  "steam:" + appid,
	}, true
}

// excludedNames are Steam's runtimes and tools: Steam installs them as apps,
// but they are plumbing, not a library.
var excludedNames = []string{
	"proton",
	"steam linux runtime",
	"steamvr",
	"steamworks",
	"steam client",
	"steam runtime",
}

func excluded(name string) bool {
	lower := strings.ToLower(name)
	for _, sub := range excludedNames {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// libraries lists the steamapps dirs to read: the probed root plus every
// "path" in its libraryfolders.vdf, which names library roots on any disk.
// Deduplicated by resolved path, since the root usually reappears in the vdf
// and the stock locations reach one directory through symlinks.
func (s *Source) libraries() []string {
	libs := []string{s.root}
	if raw, err := os.ReadFile(filepath.Join(s.root, "steamapps", "libraryfolders.vdf")); err == nil {
		libs = append(libs, kvValues(raw, "path")...)
	}
	var out []string
	seen := map[string]bool{}
	for _, lib := range libs {
		if lib == "" {
			continue
		}
		key := lib
		if resolved, err := filepath.EvalSymlinks(lib); err == nil {
			key = resolved
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		apps := filepath.Join(lib, "steamapps")
		if fi, err := os.Stat(apps); err == nil && fi.IsDir() {
			out = append(out, apps)
		}
	}
	return out
}

// cover finds the cached portrait art for an appid. appcache lives beside
// steamapps rather than inside it, so the search runs against the library root
// and its parent — the two places it turns up across native and Flatpak
// installs.
func cover(lib, appid string) string {
	rels := []string{
		filepath.Join("librarycache", appid, "library_600x900.jpg"),
		filepath.Join("librarycache", appid+"_header.jpg"),
		filepath.Join("librarycache", appid, "header.jpg"),
	}
	for _, base := range []string{lib, filepath.Dir(lib)} {
		for _, rel := range rels {
			p := filepath.Join(base, "appcache", rel)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

// Launch hands the rungameid URI to the desktop's steam:// handler, which is
// registered by whichever client owns the scheme — native, distro build or
// Flatpak alike — so the same call launches in all of them.
func (s *Source) Launch(ctx context.Context, g source.Game) error {
	return s.run(ctx, "xdg-open", "steam://rungameid/"+g.ID)
}

func (s *Source) Running(ctx context.Context, games []source.Game) (map[string]time.Time, error) {
	dirs := make(map[string]string, len(games))
	for _, g := range games {
		dirs[g.ID] = g.Directory
	}
	// registry.vdf is the preferred signal: it names the app Steam itself
	// considers running, which survives Proton and Wine games whose argv never
	// mentions the install dir.
	if raw, err := os.ReadFile(filepath.Join(s.root, "registry.vdf")); err == nil {
		if v, ok := kvValue(raw, "RunningAppID"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				if n == 0 {
					return map[string]time.Time{}, nil
				}
				if n > 0 {
					id := strconv.Itoa(n)
					return map[string]time.Time{id: s.startTime(dirs[id])}, nil
				}
			}
		}
	}
	// No usable registry: match live processes whose argv names an install dir.
	byDir := map[string]string{}
	var all []string
	for _, g := range games {
		if g.Directory == "" {
			continue
		}
		byDir[g.Directory] = g.ID
		all = append(all, g.Directory)
	}
	matches, _, _ := running.ScanGames(ctx, all, nil, s.procRoot) // no /proc is "nothing running", not an error
	out := make(map[string]time.Time, len(matches))
	for dir, m := range matches {
		out[byDir[dir]] = m.Start
	}
	return out, nil
}

// startTime dates the game behind Steam's RunningAppID through its install
// dir. When no process is found — Steam may have started it in a way the scan
// cannot see — now() keeps the entry honest about "running" without inventing
// a start.
func (s *Source) startTime(dir string) time.Time {
	if dir != "" {
		if matches, err := running.Scan([]string{dir}, s.procRoot); err == nil {
			if m, ok := matches[dir]; ok {
				return m.Start
			}
		}
	}
	return time.Now()
}

// Stop signals the process group that owns the install dir; Steam itself is
// never signalled, since killing the client would take every other running
// game down with it. A game with no directory or no live match is not
// stoppable through the process table.
func (s *Source) Stop(ctx context.Context, g source.Game) error {
	if g.Directory == "" {
		return source.ErrNotImplemented
	}
	matches, err := running.Scan([]string{g.Directory}, s.procRoot)
	if err != nil {
		return err
	}
	m, ok := matches[g.Directory]
	if !ok {
		return source.ErrNotImplemented
	}
	return s.stop(m.PID, 5*time.Second)
}

// kvValue returns the first quoted value of key in Valve KeyValues text and
// kvValues every value of it (libraryfolders.vdf repeats "path" per library).
func kvValue(data []byte, key string) (string, bool) {
	var val string
	var ok bool
	kvPairs(data, func(k, v string) bool {
		if k == key {
			val, ok = v, true
			return false
		}
		return true
	})
	return val, ok
}

func kvValues(data []byte, key string) []string {
	var out []string
	kvPairs(data, func(k, v string) bool {
		if k == key {
			out = append(out, v)
		}
		return true
	})
	return out
}

// kvPairs walks the subset of Valve KeyValues the Steam files here use:
// quoted strings, braces, // comments. A block boundary restarts the
// key/value pairing, so nested blocks (the "apps" list, InstalledDepots)
// never shift it.
func kvPairs(data []byte, fn func(key, value string) bool) {
	var key string
	haveKey := false
	for i := 0; i < len(data); {
		c := data[i]
		switch {
		case c == '"':
			v, next := kvString(data, i)
			i = next
			if haveKey {
				if !fn(key, v) {
					return
				}
				haveKey = false
			} else {
				key, haveKey = v, true
			}
		case c == '{' || c == '}':
			haveKey = false
			i++
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
		default:
			i++
		}
	}
}

// kvString reads the quoted token starting at data[i], undoing only Valve's
// \" and \\ escapes, and returns it with the index just past the closing
// quote.
func kvString(data []byte, i int) (string, int) {
	var b strings.Builder
	for i++; i < len(data) && data[i] != '"'; i++ {
		if data[i] == '\\' && i+1 < len(data) && (data[i+1] == '"' || data[i+1] == '\\') {
			i++
		}
		b.WriteByte(data[i])
	}
	return b.String(), i + 1
}
