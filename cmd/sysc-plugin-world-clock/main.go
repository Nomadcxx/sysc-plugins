package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	worldclock "github.com/Nomadcxx/sysc-plugins/plugins/world-clock"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const maxSuggestions = 5

// wallCheck is how often the loop compares the wall-clock minute with the
// last one drawn. A timer armed for the next minute would run on the monotonic
// clock, which stops during suspend and leaves a stale time after resume.
const wallCheck = 5 * time.Second

func main() {
	env := environment{now: time.Now, local: time.Local, index: worldclock.OpenSystemIndex("/usr/share/zoneinfo"), callTimeout: 5 * time.Second}
	if err := runPlugin(os.Stdin, os.Stdout, env); err != nil {
		os.Exit(1)
	}
}

type environment struct {
	now   func() time.Time
	local *time.Location
	index *worldclock.Index
	// callTimeout bounds every host call. Calls run on the loop, and a reply
	// queued behind a burst of input would otherwise never be read.
	callTimeout time.Duration
}

type settings struct {
	hour24 bool
	mode   worldclock.BarMode
	cycle  time.Duration
}

func defaultSettings() settings {
	return settings{hour24: true, mode: worldclock.BarPrimary, cycle: 15 * time.Second}
}

// apply takes the values the host sent; a missing or mistyped key keeps its
// current value, and cycle_seconds is clamped to the manifest's 3–120.
func (s *settings) apply(values map[string]any) {
	if v, ok := values["hour24"].(bool); ok {
		s.hour24 = v
	}
	if v, ok := values["bar_mode"].(string); ok {
		if m, ok := worldclock.ParseBarMode(v); ok {
			s.mode = m
		}
	}
	if v, ok := values["cycle_seconds"].(float64); ok {
		v = min(max(v, 3), 120)
		s.cycle = time.Duration(v) * time.Second
	}
}

type view struct {
	kind v1.ViewKind
	rev  uint64
}

type session struct {
	env         environment
	client      *v1.Client
	store       *worldclock.Store
	settings    settings
	views       map[string]view
	query       string
	queryReseed uint64
	renameDraft string
	renameGen   uint64
	addErr      string
	saveErr     string
	cycle       int
	// loaded is false until state.get has answered. Saving before then would
	// replace the user's stored list with the defaults.
	loaded     bool
	lastMinute time.Time
	// searchFloor is, per panel view, the first revision carrying the current
	// search reseed. A search event from an older revision was typed into a
	// buffer the reseed has since cleared.
	searchFloor map[string]uint64
	floorGen    map[string]uint64
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.world-clock", Name: "World Clock", Version: "2.0.0"})); err != nil {
		return err
	}
	s := &session{env: env, client: c, store: worldclock.NewStore(), settings: defaultSettings(), views: map[string]view{},
		searchFloor: map[string]uint64{}, floorGen: map[string]uint64{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	s.lastMinute = env.now().Truncate(time.Minute)

	minute := time.NewTicker(wallCheck)
	defer minute.Stop()
	var cycle *time.Ticker
	var cycleC <-chan time.Time
	resetCycle := func() {
		if cycle != nil {
			cycle.Stop()
			cycle, cycleC = nil, nil
		}
		if s.settings.mode == worldclock.BarCycle {
			cycle = time.NewTicker(s.settings.cycle)
			cycleC = cycle.C
		}
	}
	defer func() {
		if cycle != nil {
			cycle.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-minute.C:
			s.maybeTick()
		case <-cycleC:
			s.cycle++
			s.patchBars()
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View}
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
				s.cycle = 0
				resetCycle()
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

func (s *session) restore(ctx context.Context) {
	reply, err := s.call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "zones"})
	if err != nil || !reply.OK {
		return
	}
	s.loaded = true
	var result v1.StateGetResult
	if json.Unmarshal(reply.Result, &result) != nil || !result.Found {
		return
	}
	// An undecodable value keeps the defaults in memory; nothing is written
	// until the user changes something, so the stored value is not clobbered.
	if zones, err := worldclock.Decode(result.Value); err == nil {
		s.store.Load(zones)
	}
}

// ensureLoaded retries a failed startup read before a change is applied, so
// the change lands on the user's list rather than on the defaults.
func (s *session) ensureLoaded(ctx context.Context) {
	if !s.loaded {
		s.restore(ctx)
	}
}

func (s *session) save(ctx context.Context) {
	if !s.loaded {
		s.saveErr = "Couldn't load zones"
		return
	}
	raw, err := worldclock.Encode(s.store.Zones())
	if err == nil {
		var reply v1.HostReply
		reply, err = s.call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "zones", Value: raw})
		if err == nil && !reply.OK {
			err = errors.New(reply.Error)
		}
	}
	if err != nil {
		s.saveErr = "Couldn't save zones"
		return
	}
	s.saveErr = ""
}

func (s *session) readings() []worldclock.Reading {
	return s.store.Readings(s.env.now(), s.env.local, s.settings.hour24)
}

func (s *session) suggestions() []worldclock.Suggestion {
	if strings.TrimSpace(s.query) == "" {
		return nil
	}
	var out []worldclock.Suggestion
	for _, m := range s.env.index.Search(s.query, maxSuggestions*2) {
		if s.store.Has(m.ID) {
			continue
		}
		rel := ""
		if r, err := worldclock.Read(worldclock.Zone{ID: m.ID}, s.env.now(), s.env.local, s.settings.hour24); err == nil {
			rel = r.Relative
		}
		out = append(out, worldclock.Suggestion{ID: m.ID, Title: worldclock.SuggestionTitle(m, rel)})
		if len(out) == maxSuggestions {
			break
		}
	}
	return out
}

func (s *session) panelState(readings []worldclock.Reading) worldclock.PanelState {
	errText := s.saveErr
	if errText == "" {
		errText = s.addErr
	}
	notice := ""
	if s.env.index.Limited() {
		notice = "Limited search: tz tables not found"
	}
	return worldclock.PanelState{
		Readings: readings, Query: s.query, QueryReseed: s.queryReseed, Suggestions: s.suggestions(),
		PendingDelete: s.store.PendingDelete(), Renaming: s.store.Renaming(),
		RenameDraft: s.renameDraft, RenameReseed: s.renameGen, Error: errText, Notice: notice,
	}
}

func (s *session) tree(kind v1.ViewKind, readings []worldclock.Reading) *v1.Node {
	onBar := worldclock.OnBar(readings)
	switch kind {
	case v1.ViewBar:
		return worldclock.Bar(s.settings.mode, onBar, s.cycle)
	case v1.ViewTooltip:
		return worldclock.Tooltip(onBar)
	}
	return worldclock.Panel(s.panelState(readings))
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	v.rev++
	s.views[id] = v
	if v.kind == v1.ViewPanel && s.floorGen[id] != s.queryReseed {
		s.searchFloor[id], s.floorGen[id] = v.rev, s.queryReseed
	}
	_ = s.client.Snapshot(id, v.rev, s.tree(v.kind, s.readings()))
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}

func (s *session) patch(id string, repl []v1.Replacement) {
	v := s.views[id]
	if v.rev == 0 {
		s.snapshot(id)
		return
	}
	if err := s.client.Patch(id, v.rev, v.rev+1, repl); err != nil {
		return
	}
	v.rev++
	s.views[id] = v
}

// maybeTick draws the minute update when the wall-clock minute has changed
// since the last one drawn.
func (s *session) maybeTick() bool {
	minute := s.env.now().Truncate(time.Minute)
	if minute.Equal(s.lastMinute) {
		return false
	}
	s.lastMinute = minute
	s.tick()
	return true
}

// tick is the minute update: keyed patches where the tree shape is stable,
// snapshots where it is not (tooltips, panels showing suggestions).
func (s *session) tick() {
	readings := s.readings()
	for id, v := range s.views {
		switch {
		case v.kind == v1.ViewBar:
			s.patch(id, []v1.Replacement{{Key: "bar", Node: worldclock.BarButton(s.settings.mode, worldclock.OnBar(readings), s.cycle)}})
		case v.kind == v1.ViewPanel && s.query == "":
			s.patch(id, worldclock.PanelPatch(readings, s.store.Renaming()))
		default:
			s.snapshot(id)
		}
	}
}

func (s *session) patchBars() {
	onBar := worldclock.OnBar(s.readings())
	for id, v := range s.views {
		if v.kind == v1.ViewBar {
			s.patch(id, []v1.Replacement{{Key: "bar", Node: worldclock.BarButton(s.settings.mode, onBar, s.cycle)}})
		}
	}
}

func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	node := m.Node
	id := node[strings.Index(node, ":")+1:]
	switch {
	case node == "open":
		_, _ = s.call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
	case node == "search":
		if m.Revision < s.searchFloor[m.ViewID] {
			return
		}
		s.query, s.addErr = m.Text, ""
		if m.Event == v1.EventSubmit {
			s.addTop(ctx)
		}
	case node == "add":
		s.addTop(ctx)
	case strings.HasPrefix(node, "pick:"):
		s.addPick(ctx, id)
	case strings.HasPrefix(node, "drop:") && m.Event == v1.EventDrop:
		s.ensureLoaded(ctx)
		if at, err := strconv.Atoi(id); err == nil && s.store.Reorder(m.Text, at) {
			s.save(ctx)
		}
	case strings.HasPrefix(node, "bar:"):
		s.ensureLoaded(ctx)
		if s.store.ToggleBar(id) {
			s.cycle = 0
			s.save(ctx)
		}
	case strings.HasPrefix(node, "edit:"):
		s.store.StartRename(id)
		s.renameDraft = s.store.Label(id)
		s.renameGen++
	case strings.HasPrefix(node, "label:"):
		s.renameDraft = m.Text
		if m.Event == v1.EventSubmit {
			s.commitRename(ctx)
		}
	case node == "rename-ok":
		s.commitRename(ctx)
	case node == "rename-cancel", node == "del-cancel":
		s.store.CancelEdit()
	case strings.HasPrefix(node, "del:"):
		s.store.ProposeDelete(id)
	case node == "del-ok":
		s.ensureLoaded(ctx)
		if s.store.ConfirmDelete() {
			s.save(ctx)
		}
	}
}

func (s *session) addTop(ctx context.Context) {
	q := strings.TrimSpace(s.query)
	if q == "" {
		return
	}
	top := s.env.index.Search(q, 1)
	if len(top) == 0 {
		s.addErr = fmt.Sprintf("No zone matches %q", q)
		return
	}
	s.add(ctx, top[0])
}

func (s *session) addPick(ctx context.Context, id string) {
	for _, m := range s.env.index.Search(s.query, maxSuggestions*2) {
		if m.ID == id {
			s.add(ctx, m)
			return
		}
	}
	s.add(ctx, worldclock.Match{ID: id, City: worldclock.ShortLabel(id)})
}

func (s *session) add(ctx context.Context, m worldclock.Match) {
	s.ensureLoaded(ctx)
	label := ""
	if m.Alias {
		label = m.City
	}
	switch err := s.store.Add(m.ID, label); {
	case errors.Is(err, worldclock.ErrDuplicate):
		s.addErr = m.City + " is already in the list"
	case err != nil:
		s.addErr = fmt.Sprintf("No zone matches %q", strings.TrimSpace(s.query))
	default:
		s.query, s.addErr = "", ""
		s.queryReseed++
		s.save(ctx)
	}
}

func (s *session) commitRename(ctx context.Context) {
	s.ensureLoaded(ctx)
	if id := s.store.Renaming(); id != "" && s.store.Rename(id, s.renameDraft) {
		s.save(ctx)
	}
}
