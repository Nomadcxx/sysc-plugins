package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
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
	kind  v1.ViewKind
	rev   uint64
	width int
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
	searchFloor  map[string]uint64
	floorGen     map[string]uint64
	saves        *hostcall.StateSaver
	saveResults  chan hostcall.StateWriteResult
	saveRevision uint64
}

type zonesLoadResult struct {
	reply v1.HostReply
	err   error
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.world-clock", Name: "World Clock", Version: "2.0.1"})); err != nil {
		return err
	}
	s := &session{env: env, client: c, store: worldclock.NewStore(), settings: defaultSettings(), views: map[string]view{},
		searchFloor: map[string]uint64{}, floorGen: map[string]uint64{}}
	ctx, cancel := context.WithCancel(context.Background())
	s.saveResults = make(chan hostcall.StateWriteResult, 8)
	s.saves = hostcall.NewStateSaverWithTimeout(ctx, c, env.callTimeout)
	defer func() {
		cancel()
		s.saves.Flush(time.Second)
	}()
	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	loadRequests := make(chan struct{}, 1)
	loadResults := make(chan zonesLoadResult, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-loadRequests:
				reply, err := s.call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "zones"})
				select {
				case loadResults <- zonesLoadResult{reply: reply, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	loadRequests <- struct{}{}
	loadingZones := true
	s.addErr = "Loading zones"
	var pendingInputs []*v1.InputEvent
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
		case result := <-s.saveResults:
			if result.Key != "zones" || result.Revision != s.saveRevision {
				continue
			}
			err := result.Err
			if err == nil && !result.Reply.OK {
				err = errors.New(result.Reply.Error)
			}
			if err != nil {
				s.saveErr = "Couldn't save zones"
			} else {
				s.saveErr = ""
			}
			s.snapshotAll()
		case result := <-loadResults:
			loadingZones = false
			loaded, changed := s.restoreResult(result.reply, result.err)
			if !loaded {
				s.addErr = "Couldn't load zones"
				s.snapshotAll()
				continue
			}
			wasLoading := s.addErr == "Loading zones"
			if s.addErr == "Couldn't load zones" || s.addErr == "Loading zones" {
				s.addErr = ""
			}
			queued := pendingInputs
			pendingInputs = nil
			published := false
			for _, input := range queued {
				if input.ViewID != "" {
					if _, open := s.views[input.ViewID]; !open {
						continue
					}
				}
				s.handle(ctx, input)
				s.snapshotAll()
				published = true
			}
			if !published && (changed || wasLoading) {
				s.snapshotAll()
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View, width: m.Width}
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
				if !s.loaded {
					if m.Node == "open" && m.Event == v1.EventActivate {
						s.handle(ctx, m)
						continue
					}
					if len(pendingInputs) == 32 {
						s.addErr = "Still loading saved zones; try that change again"
						s.snapshotAll()
						continue
					}
					pendingInputs = append(pendingInputs, m)
					// Keep search feedback live while replaying the same event later
					// in order with the deferred state-changing inputs.
					if m.Node == "search" && m.Event != v1.EventSubmit {
						s.handle(ctx, m)
					}
					s.addErr = "Loading zones"
					if !loadingZones {
						loadingZones = true
						select {
						case loadRequests <- struct{}{}:
						default:
						}
					}
					s.snapshotAll()
					continue
				}
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

func (s *session) restoreResult(reply v1.HostReply, err error) (bool, bool) {
	if err != nil || !reply.OK {
		return false, false
	}
	s.loaded = true
	var result v1.StateGetResult
	if json.Unmarshal(reply.Result, &result) != nil || !result.Found {
		return true, false
	}
	// An undecodable value keeps the defaults in memory; nothing is written
	// until the user changes something, so the stored value is not clobbered.
	if zones, err := worldclock.Decode(result.Value); err == nil {
		s.store.Load(zones)
		return true, true
	}
	return true, false
}

func (s *session) save() {
	if !s.loaded {
		s.saveErr = "Couldn't load zones"
		return
	}
	raw, err := worldclock.Encode(s.store.Zones())
	if err != nil {
		s.saveErr = "Couldn't save zones"
		return
	}
	s.saveErr = ""
	s.saveRevision = s.saves.SaveTracked("zones", raw, s.saveResults)
}

func (s *session) readings() []worldclock.Reading {
	if !s.loaded {
		return nil
	}
	return s.store.Readings(s.env.now(), s.env.local, s.settings.hour24)
}

func (s *session) suggestions() []worldclock.Suggestion {
	if strings.TrimSpace(s.query) == "" {
		return nil
	}
	var out []worldclock.Suggestion
	for _, m := range s.searchMatches(maxSuggestions) {
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

// searchMatches returns enough ranked results to skip every added zone while
// still finding the first five visible suggestions.
func (s *session) searchMatches(limit int) []worldclock.Match {
	return s.env.index.Search(s.query, len(s.store.Zones())+limit)
}

func (s *session) noSuggestionsText() string {
	matches := s.searchMatches(1)
	if len(matches) == 0 {
		return "No matching zone"
	}
	return matches[0].City + " is already in the list"
}

func (s *session) panelState(readings []worldclock.Reading) worldclock.PanelState {
	var errs []string
	if s.saveErr != "" {
		errs = append(errs, s.saveErr)
	}
	if s.addErr != "" {
		errs = append(errs, s.addErr)
	}
	notice := ""
	if s.env.index.Limited() {
		notice = "Limited search: tz tables not found"
	}
	suggestions := s.suggestions()
	noMatches := ""
	if s.query != "" && len(suggestions) == 0 {
		noMatches = s.noSuggestionsText()
	}
	return worldclock.PanelState{
		Readings: readings, Query: s.query, QueryReseed: s.queryReseed, Suggestions: suggestions,
		PendingDelete: s.store.PendingDelete(), Renaming: s.store.Renaming(),
		RenameDraft: s.renameDraft, RenameReseed: s.renameGen, Errors: errs, NoMatches: noMatches, HideNoMatches: s.addErr != "", Notice: notice,
	}
}

func (s *session) tree(v view, readings []worldclock.Reading) *v1.Node {
	onBar := worldclock.OnBar(readings)
	switch v.kind {
	case v1.ViewBar:
		return worldclock.BarAtWidth(s.settings.mode, onBar, s.cycle, v.width)
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
	_ = s.client.Snapshot(id, v.rev, s.tree(v, s.readings()))
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
			s.patch(id, []v1.Replacement{{Key: "bar", Node: worldclock.BarButtonAtWidth(s.settings.mode, worldclock.OnBar(readings), s.cycle, v.width)}})
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
			s.patch(id, []v1.Replacement{{Key: "bar", Node: worldclock.BarButtonAtWidth(s.settings.mode, onBar, s.cycle, v.width)}})
		}
	}
}

func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	node := m.Node
	id := node[strings.Index(node, ":")+1:]
	switch {
	case node == "open" && m.Event == v1.EventActivate:
		hostcall.Async(ctx, s.client, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
	case node == "search":
		if m.Revision < s.searchFloor[m.ViewID] {
			return
		}
		s.query, s.addErr = m.Text, ""
		if m.Event == v1.EventSubmit {
			s.addTop()
		}
	case node == "add":
		s.addTop()
	case strings.HasPrefix(node, "pick:"):
		s.addPick(id)
	case strings.HasPrefix(node, "drop:") && m.Event == v1.EventDrop:
		before := s.store.Zones()
		if at, err := strconv.Atoi(id); err == nil && s.store.Reorder(m.Text, at) {
			s.resetCycleIfBarChanged(before)
			s.save()
		}
	case strings.HasPrefix(node, "bar:"):
		if s.store.ToggleBar(id) {
			s.cycle = 0
			s.save()
		}
	case strings.HasPrefix(node, "edit:"):
		s.store.StartRename(id)
		s.renameDraft = s.store.Label(id)
		s.renameGen++
	case strings.HasPrefix(node, "label:"):
		s.renameDraft = m.Text
		if m.Event == v1.EventSubmit {
			s.commitRename()
		}
	case node == "rename-ok":
		s.commitRename()
	case node == "rename-cancel", node == "del-cancel":
		s.store.CancelEdit()
	case strings.HasPrefix(node, "del:"):
		s.store.ProposeDelete(id)
	case node == "del-ok":
		before := s.store.Zones()
		if s.store.ConfirmDelete() {
			s.resetCycleIfBarChanged(before)
			s.save()
		}
	}
}

func (s *session) addTop() {
	q := strings.TrimSpace(s.query)
	if q == "" {
		return
	}
	matches := s.searchMatches(1)
	if len(matches) == 0 {
		s.addErr = fmt.Sprintf("No zone matches %q", q)
		return
	}
	for _, m := range matches {
		if !s.store.Has(m.ID) {
			s.add(m)
			return
		}
	}
	s.addErr = matches[0].City + " is already in the list"
}

func (s *session) addPick(id string) {
	for _, m := range s.env.index.Search(s.query, maxSuggestions*2) {
		if m.ID == id {
			s.add(m)
			return
		}
	}
	s.add(worldclock.Match{ID: id, City: worldclock.ShortLabel(id)})
}

func (s *session) add(m worldclock.Match) {
	before := s.store.Zones()
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
		s.resetCycleIfBarChanged(before)
		s.query, s.addErr = "", ""
		s.queryReseed++
		s.save()
	}
}

func (s *session) resetCycleIfBarChanged(before []worldclock.Zone) {
	barIDs := func(zones []worldclock.Zone) []string {
		var ids []string
		for _, z := range zones {
			if z.OnBar {
				ids = append(ids, z.ID)
			}
		}
		return ids
	}
	if !slices.Equal(barIDs(before), barIDs(s.store.Zones())) {
		s.cycle = 0
	}
}

func (s *session) commitRename() {
	if id := s.store.Renaming(); id != "" && s.store.Rename(id, s.renameDraft) {
		s.save()
	}
}
