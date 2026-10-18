// Package heroic reads Heroic Games Launcher's config directory directly —
// the store caches legendary, GOG and nile write, plus the sideload library —
// and launches games through xdg-open's heroic:// deep links. Heroic records
// no playtime and no live-game state, so Listing is file truth and Running is
// a best-effort /proc scan.
package heroic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/running"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

type Options struct {
	// ConfigDirs are probed in order for the first existing Heroic config
	// directory; empty means the standard native and Flatpak locations.
	ConfigDirs []string
	ProcRoot   string // default /proc; injectable for tests
	Run        func(ctx context.Context, name string, args ...string) error
	StopFn     func(pid int, grace time.Duration) error
}

type Source struct {
	root     string // the Heroic config directory New settled on
	procRoot string
	run      func(ctx context.Context, name string, args ...string) error
	stop     func(pid int, grace time.Duration) error
}

// store is one of the three store integrations Heroic mirrors onto disk: each
// writes <name>_install_info.json (installed apps) and <name>_library.json
// (titles) under store_cache/, and names both the Runner value and the
// Identity prefix.
type store struct {
	name        string
	installInfo string
	library     string
}

var stores = []store{
	{name: "epic", installInfo: "legendary_install_info.json", library: "legendary_library.json"},
	{name: "gog", installInfo: "gog_install_info.json", library: "gog_library.json"},
	{name: "amazon", installInfo: "nile_install_info.json", library: "nile_library.json"},
}

func New(o Options) (*Source, error) {
	if len(o.ConfigDirs) == 0 {
		o.ConfigDirs = defaultConfigDirs()
	}
	root := ""
	for _, dir := range o.ConfigDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			root = dir
			break
		}
	}
	if root == "" {
		return nil, fmt.Errorf("no heroic config directory found (tried %s)", strings.Join(o.ConfigDirs, ", "))
	}
	if o.ProcRoot == "" {
		o.ProcRoot = "/proc"
	}
	if o.Run == nil {
		o.Run = source.Detach
	}
	if o.StopFn == nil {
		o.StopFn = running.Stop
	}
	return &Source{root: root, procRoot: o.ProcRoot, run: o.Run, stop: o.StopFn}, nil
}

// defaultConfigDirs lists where Heroic keeps its config, in probe order: the
// XDG location, then native ~/.config, then the Flatpak sandbox, which does
// not follow XDG_CONFIG_HOME.
func defaultConfigDirs() []string {
	var dirs []string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		dirs = append(dirs, filepath.Join(x, "heroic"))
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dirs
	}
	return append(dirs,
		filepath.Join(home, ".config", "heroic"),
		filepath.Join(home, ".var", "app", "com.heroicgameslauncher.hgl", "config", "heroic"),
	)
}

func (s *Source) Name() string { return "heroic" }

func (s *Source) List(ctx context.Context) ([]source.Game, error) {
	var out []source.Game
	for _, st := range stores {
		titles := readLibrary(filepath.Join(s.root, "store_cache", st.library))
		for appid, dir := range readInstallInfo(filepath.Join(s.root, "store_cache", st.installInfo)) {
			name := titles[appid]
			if name == "" {
				name = s.configTitle(appid)
			}
			if name == "" {
				name = appid // nothing on disk names this game
			}
			out = append(out, source.Game{
				ID:        appid,
				Name:      name,
				Slug:      name,
				Runner:    st.name,
				Platform:  "Windows",
				Installed: true,
				Directory: dir,
				CoverPath: s.icon(appid),
				Source:    "heroic",
				Identity:  st.name + ":" + appid,
			})
		}
	}
	out = append(out, s.sideload()...)
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID // stable when titles collide
	})
	return out, nil
}

// sideload reads sideload_apps/library.json, Heroic's home for games it did
// not install through a store. Only installed entries are games, and they
// carry no identity: no store knows them, so nothing can merge with them.
func (s *Source) sideload() []source.Game {
	raw, err := os.ReadFile(filepath.Join(s.root, "sideload_apps", "library.json"))
	if err != nil {
		return nil
	}
	var lib struct {
		Games []struct {
			AppName     string `json:"app_name"`
			Title       string `json:"title"`
			IsInstalled bool   `json:"is_installed"`
		} `json:"games"`
	}
	if json.Unmarshal(raw, &lib) != nil {
		return nil
	}
	var out []source.Game
	for _, g := range lib.Games {
		if !g.IsInstalled || g.AppName == "" {
			continue
		}
		name := g.Title
		if name == "" {
			name = g.AppName
		}
		out = append(out, source.Game{
			ID:        g.AppName,
			Name:      name,
			Slug:      name,
			Runner:    "sideload",
			Platform:  "Windows",
			Installed: true,
			CoverPath: s.icon(g.AppName),
			Source:    "heroic",
		})
	}
	return out
}

// Launch fires Heroic's documented deep link through xdg-open:
// heroic://launch/<app id>, with sideloaded games in a namespace of their
// own. The URI is the one launch path shared by every install shape — native
// package, AppImage and Flatpak — because Heroic registers the scheme itself.
func (s *Source) Launch(ctx context.Context, g source.Game) error {
	uri := "heroic://launch/" + url.PathEscape(g.ID)
	if g.Runner == "sideload" {
		uri = "heroic://launch/sideload/" + url.PathEscape(g.ID)
	}
	return s.run(ctx, "xdg-open", uri)
}

// Running is best-effort: Heroic exposes no live-game state, so a game counts
// as running when a process's argv names its install directory. Games without
// an install path — and everything when pieces of Heroic's state are missing —
// simply report nothing; a scan failure is never the panel's problem.
func (s *Source) Running(ctx context.Context, games []source.Game) (map[string]time.Time, error) {
	byDir := map[string]string{}
	var dirs []string
	for _, g := range games {
		if g.Directory == "" {
			continue
		}
		if _, dup := byDir[g.Directory]; !dup {
			dirs = append(dirs, g.Directory)
		}
		byDir[g.Directory] = g.ID
	}
	out := map[string]time.Time{}
	matches, _, err := running.ScanGames(ctx, dirs, nil, s.procRoot)
	if err != nil {
		return out, nil
	}
	for dir, m := range matches {
		out[byDir[dir]] = m.Start
	}
	return out, nil
}

// Stop ends the process whose argv names the game's install directory. Heroic
// never records a pid and offers no stop deep link, and the UI only offers
// Stop for a game it saw running, so no match means the game is already gone
// or Heroic holds no directory to match on.
func (s *Source) Stop(ctx context.Context, g source.Game) error {
	if g.Directory != "" {
		if matches, err := running.Scan([]string{g.Directory}, s.procRoot); err == nil {
			if m, ok := matches[g.Directory]; ok {
				return s.stop(m.PID, 5*time.Second)
			}
		}
	}
	return source.ErrNotImplemented
}

// readInstallInfo returns appid -> install dir from one store's install-info
// file: a JSON object keyed by app id. A value that is not an object is not an
// installed game, and an install_path that is not a string yields "".
func readInstallInfo(path string) map[string]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	out := map[string]string{}
	for id, v := range top {
		if id == "__timestamp" || !isObject(v) {
			continue
		}
		var fields map[string]any
		if json.Unmarshal(v, &fields) != nil {
			continue
		}
		dir, _ := fields["install_path"].(string)
		out[id] = dir
	}
	return out
}

// readLibrary returns appid -> title from a store's library cache. The usual
// shape is an object with a "library" array; a bare array (older caches) and
// an object keyed by app id are accepted defensively.
func readLibrary(path string) map[string]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var top json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	out := map[string]string{}
	switch {
	case isArray(top):
		collectEntries(top, out)
	case isObject(top):
		var fields map[string]json.RawMessage
		if json.Unmarshal(top, &fields) != nil {
			return nil
		}
		lib, ok := fields["library"]
		switch {
		case ok && isArray(lib):
			collectEntries(lib, out)
		case ok && isObject(lib):
			collectMap(lib, out)
		default:
			collectMap(top, out)
		}
	}
	return out
}

type libraryEntry struct {
	AppName string `json:"app_name"`
	Title   string `json:"title"`
}

// collectEntries records appid -> title from an array of {app_name, title}.
func collectEntries(raw json.RawMessage, out map[string]string) {
	var entries []libraryEntry
	if json.Unmarshal(raw, &entries) != nil {
		return
	}
	for _, e := range entries {
		addTitle(out, e.AppName, e.Title)
	}
}

// collectMap records appid -> title from an object keyed by app id; the key
// stands in when an entry has no app_name of its own.
func collectMap(raw json.RawMessage, out map[string]string) {
	var byID map[string]libraryEntry
	if json.Unmarshal(raw, &byID) != nil {
		return
	}
	for id, e := range byID {
		if e.AppName == "" {
			e.AppName = id
		}
		addTitle(out, e.AppName, e.Title)
	}
}

func addTitle(out map[string]string, appid, title string) {
	if appid != "" && title != "" {
		out[appid] = title
	}
}

func isObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

func isArray(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '['
}

// configTitle is the per-app fallback: GamesConfig/<appid>.json, written by
// Heroic for every game it manages, carries a title and sometimes only a
// name. "" when the file is missing, unreadable or nameless.
func (s *Source) configTitle(appid string) string {
	if !filepath.IsLocal(appid) {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(s.root, "GamesConfig", appid+".json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Title string `json:"title"`
		Name  string `json:"name"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	if cfg.Title != "" {
		return cfg.Title
	}
	return cfg.Name
}

// icon resolves icons/<id>.jpg, the cover Heroic stores under a predictable
// name; "" when no icon was downloaded or the id is not a plain file name.
func (s *Source) icon(id string) string {
	if !filepath.IsLocal(id) {
		return ""
	}
	p := filepath.Join(s.root, "icons", id+".jpg")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}
