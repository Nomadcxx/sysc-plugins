package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/bar"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/covers"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/panel"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source/faugus"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source/heroic"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source/lutris"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source/steam"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/store"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	env := environment{now: time.Now, run: source.Detach, callTimeout: 5 * time.Second, fetchGrid: defaultFetchGrid}
	if err := runPlugin(os.Stdin, os.Stdout, env); err != nil {
		os.Exit(1)
	}
}

func defaultFetchGrid(ctx context.Context, key, slug, dir string) string {
	return covers.FetchGrid(ctx, nil, key, "", slug, dir)
}

type environment struct {
	now         func() time.Time
	run         func(ctx context.Context, name string, args ...string) error
	callTimeout time.Duration
	dbPath      string                                                  // "" = probe real Lutris pga.db locations (tests inject a fixture)
	procRoot    string                                                  // "" = /proc
	fetchGrid   func(ctx context.Context, key, slug, dir string) string // "" = default
	home        string                                                  // "" = os.UserHomeDir; the base of every library path
	steamRoots  []string                                                // "" = real Steam locations
	faugusData  []string                                                // "" = real Faugus data locations
	faugusState []string                                                // "" = real Faugus state locations
	heroicDirs  []string                                                // "" = real Heroic config locations
	lookPath    func(string) (string, error)                            // "" = exec.LookPath (tests inject launcher presence)
}

// launchers is the tie-break order of the backends, independent of which one
// is the default: Steam's copy of a game is the canonical one when no
// default is set (or the default's library is not installed).
var launchers = []string{"steam", "faugus", "heroic", "lutris"}

type settings struct {
	sourceSteam     bool
	sourceLutris    bool
	sourceHeroic    bool
	sourceFaugus    bool
	defaultLauncher string
	steamGridKey    string
	pollRunning     string // auto|off
	hideUnavailable bool
}

func defaultSettings() settings {
	return settings{
		sourceSteam: true, sourceLutris: true, sourceHeroic: true, sourceFaugus: true,
		defaultLauncher: "steam", pollRunning: "auto",
	}
}

func (s *settings) apply(values map[string]any) {
	if v, ok := values["source_steam"].(bool); ok {
		s.sourceSteam = v
	}
	if v, ok := values["source_lutris"].(bool); ok {
		s.sourceLutris = v
	}
	if v, ok := values["source_heroic"].(bool); ok {
		s.sourceHeroic = v
	}
	if v, ok := values["source_faugus"].(bool); ok {
		s.sourceFaugus = v
	}
	if v, ok := values["default_launcher"].(string); ok && knownLauncher(v) {
		s.defaultLauncher = v
	}
	if v, ok := values["steamgriddb_key"].(string); ok {
		s.steamGridKey = strings.TrimSpace(v)
	}
	if v, ok := values["poll_running"].(string); ok && (v == "off" || v == "auto") {
		s.pollRunning = v
	}
	if v, ok := values["hide_unavailable"].(bool); ok {
		s.hideUnavailable = v
	}
}

func knownLauncher(name string) bool {
	for _, l := range launchers {
		if l == name {
			return true
		}
	}
	return false
}

// launcherOrder lists every library with the default launcher first. The set
// merges in this order, so a game two libraries both list is served by the
// default launcher's copy and launches through it.
func (s *settings) launcherOrder() []string {
	out := make([]string, 0, len(launchers))
	if knownLauncher(s.defaultLauncher) {
		out = append(out, s.defaultLauncher)
	}
	for _, l := range launchers {
		if l != s.defaultLauncher {
			out = append(out, l)
		}
	}
	return out
}

type view struct {
	kind  v1.ViewKind
	entry string
	rev   uint64
	width int
}

type session struct {
	env      environment
	client   *v1.Client
	settings settings
	views    map[string]view

	set         *source.Set
	cacheDir    string
	games       []source.Game
	running     map[string]time.Time
	missing     bool // no library is available (none installed, or all disabled)
	machine     *panel.Machine
	failed      map[string]bool
	prefs       store.Prefs
	sessions    store.Log
	loaded      bool
	prunedStale bool // stale open sessions pruned after first successful scan
	query       string
	selected    string
	poll        *time.Ticker
	pollC       <-chan time.Time
	pollEvery   time.Duration

	refreshJobs       chan refreshJob
	refreshDone       chan refreshResult
	refreshGeneration uint64
	refreshRequestID  uint64
	refreshing        bool

	coverDirty map[string]bool
	coverJobs  chan coverJob
	coverDone  chan coverDone
}

type coverJob struct {
	gameID, slug, key, dir string
}

type coverDone struct {
	gameID, path string
}

// library is the slice of the source surface the refresh worker needs; the
// multi-library set satisfies it, so a job carries every enabled library.
type library interface {
	List(ctx context.Context) ([]source.Game, error)
	Running(ctx context.Context, games []source.Game) (map[string]time.Time, error)
}

type refreshJob struct {
	generation, requestID uint64
	src                   library
	scan                  bool
}

type refreshStage uint8

const (
	refreshLibrary refreshStage = iota + 1
	refreshRunning
)

type refreshResult struct {
	generation, requestID uint64
	stage                 refreshStage
	games                 []source.Game
	running               map[string]time.Time
	err                   error
	done                  bool
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.games", Name: "Games", Version: "0.3.0"})); err != nil {
		return err
	}
	cacheDir := ""
	if base, err := os.UserCacheDir(); err == nil {
		cacheDir = base + "/sysc-games"
		_ = os.MkdirAll(cacheDir, 0o755)
	}
	s := &session{
		env: env, client: c, settings: defaultSettings(), views: map[string]view{},
		machine: panel.NewMachine(), failed: map[string]bool{}, cacheDir: cacheDir,
		sessions:   store.Log{},
		coverDirty: map[string]bool{}, coverJobs: make(chan coverJob, 16), coverDone: make(chan coverDone, 16),
		refreshJobs: make(chan refreshJob, 1), refreshDone: make(chan refreshResult, 2),
	}
	if s.env.fetchGrid == nil {
		s.env.fetchGrid = defaultFetchGrid
	}
	s.openSources()
	s.prefs.EnsureMaps()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.coverWorker(ctx)
	go s.refreshWorker(ctx)
	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			incoming <- msg
		}
	}()
	s.restore(ctx)
	s.requestRefresh(false)
	s.setPoll(5 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.pollC:
			s.tick()
		case res := <-s.coverDone:
			s.applyCover(res)
			s.snapshotAll()
		case res := <-s.refreshDone:
			if s.applyRefresh(ctx, res) {
				s.snapshotAll()
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View, entry: m.Entry, width: m.Width}
				s.requestRefresh(true)
				s.setPoll(s.desiredPoll())
				s.snapshot(m.ViewID)
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := s.views[m.ViewID]; ok {
					v.rev = 0
					s.views[m.ViewID] = v
					s.snapshot(m.ViewID)
				}
			case *v1.InputEvent:
				s.handle(ctx, m)
				s.snapshotAll()
			case *v1.SettingsChanged:
				s.settings.apply(m.Values)
				s.openSources()
				s.refreshGeneration++
				if s.settings.pollRunning == "off" {
					s.running = nil
				}
				s.requestRefresh(true)
				s.setPoll(s.desiredPoll())
				s.snapshotAll()
			}
		}
	}
}

func (s *session) call(ctx context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
	if s.env.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.env.callTimeout)
		defer cancel()
	}
	return s.client.Call(ctx, kind, params)
}

func (s *session) panelOpen() bool {
	for _, v := range s.views {
		if v.kind == v1.ViewPanel {
			return true
		}
	}
	return false
}

// openSources builds every enabled library that is actually installed and
// hands them to the set in preference order (default launcher first). A
// library that is not installed is skipped silently: the deck works with
// whichever ones exist, and "missing" only means none do.
func (s *session) openSources() {
	home := s.env.home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	var srcs []source.Source
	for _, name := range s.settings.launcherOrder() {
		var (
			src source.Source
			err error
		)
		switch name {
		case "steam":
			if !s.settings.sourceSteam {
				continue
			}
			src, err = steam.New(steam.Options{Roots: s.env.steamRoots, ProcRoot: s.env.procRoot, Run: s.env.run})
		case "lutris":
			if !s.settings.sourceLutris {
				continue
			}
			dbPath := s.env.dbPath
			if dbPath == "" {
				dbPath = newestLutrisDB(home)
			}
			if dbPath == "" {
				continue
			}
			src, err = lutris.New(lutris.Options{DBPath: dbPath, ProcRoot: s.env.procRoot, Run: s.env.run})
		case "heroic":
			if !s.settings.sourceHeroic {
				continue
			}
			src, err = heroic.New(heroic.Options{ConfigDirs: s.env.heroicDirs, ProcRoot: s.env.procRoot, Run: s.env.run})
		case "faugus":
			if !s.settings.sourceFaugus {
				continue
			}
			src, err = faugus.New(faugus.Options{
				DataDirs: s.env.faugusData, StateDirs: s.env.faugusState,
				Home: home, ProcRoot: s.env.procRoot, Run: s.env.run, LookPath: s.env.lookPath,
			})
		}
		if err == nil && src != nil {
			srcs = append(srcs, src)
		}
	}
	s.set = source.NewSet(srcs...)
	s.missing = len(srcs) == 0
}

// newestLutrisDB prefers the most recently written pga.db, so a machine with
// both a native and a Flatpak Lutris reads the library in use.
func newestLutrisDB(home string) string {
	best, bestMod := "", time.Time{}
	for _, p := range lutris.DefaultDBPaths(home) {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.ModTime().After(bestMod) {
			best, bestMod = p, info.ModTime()
		}
	}
	return best
}

// requestRefresh keeps one active worker and one replaceable queued request.
func (s *session) requestRefresh(showProgress bool) {
	if s.set == nil {
		s.refreshing = false
		select {
		case <-s.refreshJobs:
		default:
		}
		return
	}
	s.refreshRequestID++
	job := refreshJob{
		generation: s.refreshGeneration,
		requestID:  s.refreshRequestID,
		src:        s.set,
		scan:       s.settings.pollRunning != "off",
	}
	s.refreshing = s.refreshing || showProgress
	select {
	case s.refreshJobs <- job:
	default:
		select {
		case <-s.refreshJobs:
		default:
		}
		s.refreshJobs <- job
	}
}

func (s *session) refreshWorker(ctx context.Context) {
	publish := func(result refreshResult) bool {
		select {
		case s.refreshDone <- result:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		var job refreshJob
		select {
		case <-ctx.Done():
			return
		case job = <-s.refreshJobs:
		}
		games, err := job.src.List(ctx)
		if ctx.Err() != nil {
			return
		}
		if !publish(refreshResult{
			generation: job.generation,
			requestID:  job.requestID,
			stage:      refreshLibrary,
			games:      games,
			err:        err,
			done:       err != nil || !job.scan,
		}) {
			return
		}
		if err != nil || !job.scan {
			continue
		}
		running, err := job.src.Running(ctx, games)
		if ctx.Err() != nil {
			return
		}
		if !publish(refreshResult{
			generation: job.generation,
			requestID:  job.requestID,
			stage:      refreshRunning,
			running:    running,
			err:        err,
			done:       true,
		}) {
			return
		}
	}
}

// copyGames gives the session its own copy of a published listing. The
// refresh worker keeps the listing it published and asks every source what is
// running with that same listing, so a cover path written into the session's
// games would land in memory the worker reads.
func copyGames(games []source.Game) []source.Game {
	if games == nil {
		return nil
	}
	out := make([]source.Game, len(games))
	copy(out, games)
	return out
}

func (s *session) applyRefresh(ctx context.Context, result refreshResult) bool {
	if result.generation != s.refreshGeneration {
		return false
	}
	changed := false
	switch result.stage {
	case refreshLibrary:
		if result.err == nil {
			changed = s.missing || !equalGames(s.games, result.games)
			s.games = copyGames(result.games)
			s.missing = false
		} else if len(s.games) == 0 && !s.missing {
			s.missing = true
			changed = true
		}
	case refreshRunning:
		if result.err == nil {
			changed = true
			s.scan(ctx, result.running)
		}
	}
	if result.done && result.requestID == s.refreshRequestID {
		if s.refreshing {
			s.refreshing = false
			changed = true
		}
	}
	return changed
}

// equalGames reports whether two listings hold the same games, so a refresh
// that changed nothing skips the repaint. Game is not comparable — the
// OtherSources list rules out slices.Equal — hence the field-wise compare.
func equalGames(a, b []source.Game) bool {
	return slices.EqualFunc(a, b, func(x, y source.Game) bool {
		return x.ID == y.ID && x.Name == y.Name && x.Slug == y.Slug &&
			x.Runner == y.Runner && x.Platform == y.Platform && x.Year == y.Year &&
			x.Installed == y.Installed && x.PlaytimeSec == y.PlaytimeSec &&
			x.LastPlayed.Equal(y.LastPlayed) && x.Directory == y.Directory &&
			x.Executable == y.Executable && x.ConfigPath == y.ConfigPath &&
			x.CoverPath == y.CoverPath && x.Source == y.Source &&
			x.Identity == y.Identity && slices.Equal(x.OtherSources, y.OtherSources) && slices.Equal(x.Copies, y.Copies)
	})
}

// scan applies running detection on the event-loop owner. It also adopts
// processes the libraries launched and updates persisted session accounting.
func (s *session) scan(ctx context.Context, running map[string]time.Time) {
	if s.set == nil || s.settings.pollRunning == "off" || s.missing {
		return
	}
	s.running = running
	set := make(map[string]bool, len(running))
	for id := range running {
		set[id] = true
	}
	for id, start := range running {
		if s.machine.Phase(id) == panel.PhaseIdle {
			s.machine.MarkRunning(id)
			s.sessions.Start(id, start)
			s.save(ctx, "sessions", s.sessions)
		}
	}
	// First successful scan after startup: dead games with a persisted open
	// session (shell quit or reboot mid-game) get it closed here (#92).
	if !s.prunedStale {
		s.prunedStale = true
		if closed := s.sessions.EndDangling(set, s.env.now()); len(closed) > 0 {
			s.save(ctx, "sessions", s.sessions)
		}
	}
	for _, ev := range s.machine.Observe(set, s.env.now()) {
		switch ev.Kind {
		case panel.EventStarted:
			delete(s.failed, ev.GameID) // a slow start that outlived the timeout
			s.sessions.Start(ev.GameID, ev.At)
			s.save(ctx, "sessions", s.sessions)
		case panel.EventStopped:
			s.sessions.End(ev.GameID, ev.At)
			s.save(ctx, "sessions", s.sessions)
		case panel.EventFailed:
			s.failed[ev.GameID] = true
			_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{
				Summary: "Launch failed", Body: s.name(ev.GameID) + " never showed up",
			})
		}
	}
	s.closeSwitcherIfIdle(ctx)
	s.setPoll(s.desiredPoll())
}

func (s *session) desiredPoll() time.Duration {
	launching := false
	for _, id := range s.ids() {
		if s.machine.Phase(id) == panel.PhaseLaunching {
			launching = true
		}
	}
	switch {
	case launching:
		return time.Second
	case s.panelOpen():
		return 2 * time.Second
	// Poll while anything runs even without a bar widget, or the game-quit
	// event (and its session End stamp) waits for the next panel open (#92).
	case len(s.running) > 0:
		return 5 * time.Second
	default:
		return 0 // battery: nothing visible is time-based
	}
}

func (s *session) ids() []string {
	out := make([]string, 0, len(s.games))
	for _, g := range s.games {
		out = append(out, g.ID)
	}
	return out
}

// setPoll arms the poll ticker for d, or stops it when d is zero.
func (s *session) setPoll(d time.Duration) {
	if d == s.pollEvery {
		return
	}
	s.pollEvery = d
	if s.poll != nil {
		s.poll.Stop()
		s.poll, s.pollC = nil, nil
	}
	if d > 0 {
		s.poll = time.NewTicker(d)
		s.pollC = s.poll.C
	}
}

func (s *session) tick() {
	s.requestRefresh(false)
}

func (s *session) name(id string) string {
	for _, g := range s.games {
		if g.ID == id {
			return g.Name
		}
	}
	return "Game"
}

func (s *session) game(id string) *source.Game {
	for i := range s.games {
		if s.games[i].ID == id {
			return &s.games[i]
		}
	}
	return nil
}

// ---- state persistence ----

// restore pulls persisted state. loaded flips as soon as the round-trip
// works — a fresh install (not Found) must still be able to save.
func (s *session) restore(ctx context.Context) {
	reply, err := s.call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "prefs"})
	if err == nil && reply.OK {
		s.loaded = true
		var result v1.StateGetResult
		if json.Unmarshal(reply.Result, &result) == nil && result.Found {
			_ = json.Unmarshal(result.Value, &s.prefs)
		}
		s.prefs.EnsureMaps()
	}
	if reply, err := s.call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "sessions"}); err == nil && reply.OK {
		var result v1.StateGetResult
		if json.Unmarshal(reply.Result, &result) == nil && result.Found {
			_ = json.Unmarshal(result.Value, &s.sessions)
		}
	}
	// Pre-0.3 state keyed games by their bare Lutris id; keys are namespaced
	// now. Rewrite once so favorites and history survive the upgrade.
	migrated := s.prefs.MigrateLegacyKeys("lutris")
	if s.sessions.MigrateLegacyKeys("lutris") {
		migrated = true
	}
	if migrated {
		s.save(ctx, "prefs", s.prefs)
		s.save(ctx, "sessions", s.sessions)
	}
}

func (s *session) save(ctx context.Context, key string, value any) {
	if !s.loaded {
		return // would clobber stored state with defaults
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	reply, err := s.call(ctx, v1.CallStateSet, v1.StateSetParams{Key: key, Value: raw})
	if err == nil && !reply.OK {
		err = errors.New(reply.Error)
	}
	if err != nil {
		// Loud once: silently losing favorites is worse than a toast.
		_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Couldn't save game settings"})
	}
}

func (s *session) barState() map[string]bar.Run {
	out := make(map[string]bar.Run, len(s.running))
	for id, start := range s.running {
		if s.machine.Phase(id) == panel.PhaseIdle {
			continue
		}
		out[id] = bar.Run{Name: s.name(id), Start: start}
	}
	return out
}

func (s *session) panelState() panel.State {
	if s.selected != "" {
		s.enqueueCover(s.selected)
	}
	launching := map[string]bool{}
	for _, id := range s.ids() {
		if s.machine.Phase(id) == panel.PhaseLaunching {
			launching[id] = true
		}
	}
	return panel.State{
		Now: s.env.now(), All: s.games, Prefs: s.prefs, Query: s.query,
		Selected: s.selected, Running: s.running, Launching: launching,
		Failed: s.failed, Sessions: s.sessions, HideUnavailable: s.settings.hideUnavailable,
		CacheDir: s.cacheDir, LibraryMissing: s.missing, Refreshing: s.refreshing,
		Libraries: s.libraryNames(),
	}
}

// libraryNames lists the available libraries for the deck's badges.
func (s *session) libraryNames() []string {
	if s.set == nil {
		return nil
	}
	return s.set.Names()
}

// enqueueCover queues one async SteamGridDB fetch per game; the panel stays
// responsive while covers download (ponytail: single worker + dirty-set
// coalescing — add a pool only if downloads queue behind slow ones).
func (s *session) enqueueCover(id string) {
	if s.settings.steamGridKey == "" || s.cacheDir == "" || s.coverJobs == nil {
		return
	}
	g := s.game(id)
	if g == nil || g.CoverPath != "" || s.coverDirty[id] {
		return
	}
	s.coverDirty[id] = true
	select {
	case s.coverJobs <- coverJob{gameID: id, slug: g.Slug, key: s.settings.steamGridKey, dir: s.cacheDir}:
	default: // queue full: retry on next selection
		delete(s.coverDirty, id)
	}
}

func (s *session) coverWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.coverJobs:
			fctx, cancel := context.WithTimeout(ctx, s.env.callTimeout)
			path := s.env.fetchGrid(fctx, job.key, job.slug, job.dir)
			cancel()
			select {
			case s.coverDone <- coverDone{gameID: job.gameID, path: path}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (s *session) applyCover(res coverDone) {
	delete(s.coverDirty, res.gameID)
	if res.path != "" {
		if g := s.game(res.gameID); g != nil {
			g.CoverPath = res.path
		}
	}
}
