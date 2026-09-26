package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/bar"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/covers"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/panel"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source/lutris"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/store"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	env := environment{now: time.Now, run: runCommand, callTimeout: 5 * time.Second, fetchGrid: defaultFetchGrid}
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
	dbPath      string                                                  // "" = real Lutris pga.db (tests inject a fixture)
	procRoot    string                                                  // "" = /proc
	fetchGrid   func(ctx context.Context, key, slug, dir string) string // "" = default
}

type settings struct {
	sourceLutris    bool
	steamGridKey    string
	pollRunning     string // auto|off
	hideUnavailable bool
}

func defaultSettings() settings {
	return settings{sourceLutris: true, pollRunning: "auto"}
}

func (s *settings) apply(values map[string]any) {
	if v, ok := values["source_lutris"].(bool); ok {
		s.sourceLutris = v
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

type view struct {
	kind v1.ViewKind
	rev  uint64
}

type session struct {
	env      environment
	client   *v1.Client
	settings settings
	views    map[string]view

	src       source.Source
	cacheDir  string
	games     []source.Game
	running   map[string]time.Time
	missing   bool // library unavailable (no pga.db / source off)
	machine   *panel.Machine
	failed    map[string]bool
	prefs     store.Prefs
	sessions  store.Log
	loaded    bool
	query     string
	selected  string
	actions   bool
	poll      *time.Ticker
	pollC     <-chan time.Time
	pollEvery time.Duration

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

func runCommand(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.games", Name: "Games", Version: "0.1.0"})); err != nil {
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
	}
	if s.env.fetchGrid == nil {
		s.env.fetchGrid = defaultFetchGrid
	}
	if s.settings.sourceLutris {
		s.openLutris()
	} else {
		s.missing = true
	}
	s.prefs.EnsureMaps()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.coverWorker(ctx)
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
	s.refresh(ctx)
	s.setPoll(5 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.pollC:
			s.tick(ctx)
		case res := <-s.coverDone:
			s.applyCover(res)
			s.snapshotAll()
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View}
				s.refresh(ctx)
				s.scan(ctx)
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
				if !s.settings.sourceLutris {
					s.src, s.running = nil, nil
				} else if s.src == nil {
					s.openLutris()
				}
				s.refresh(ctx)
				if s.settings.pollRunning == "off" {
					s.running = nil
				} else {
					s.scan(ctx)
				}
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

func (s *session) hasBar() bool {
	for _, v := range s.views {
		if v.kind == v1.ViewBar {
			return true
		}
	}
	return false
}

func (s *session) openLutris() {
	src, err := lutris.New(lutris.Options{DBPath: s.env.dbPath, ProcRoot: s.env.procRoot, Run: s.env.run})
	if err != nil {
		s.missing = true
		return
	}
	s.src, s.missing = src, false
}

func (s *session) refresh(ctx context.Context) {
	if s.src == nil {
		s.missing = true
		return
	}
	games, err := s.src.List(ctx)
	s.missing = err != nil
	if err == nil {
		s.games = games
	}
}

// scan updates running detection and feeds the launch machine; it also
// adopts processes that were already running (Lutris launched them).
func (s *session) scan(ctx context.Context) {
	if s.src == nil || s.settings.pollRunning == "off" || s.missing {
		return
	}
	running, err := s.src.Running(ctx)
	if err != nil {
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
	for _, ev := range s.machine.Observe(set, s.env.now()) {
		switch ev.Kind {
		case panel.EventStarted:
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
	case len(s.running) > 0 && s.hasBar():
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

func (s *session) tick(ctx context.Context) {
	s.refresh(ctx)
	s.scan(ctx)
	s.snapshotAll()
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
	return panel.State{
		Now: s.env.now(), All: s.games, Prefs: s.prefs, Query: s.query,
		Selected: s.selected, Actions: s.actions, Running: s.running,
		Failed: s.failed, Sessions: s.sessions, HideUnavailable: s.settings.hideUnavailable,
		CacheDir: s.cacheDir, LibraryMissing: s.missing,
	}
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
