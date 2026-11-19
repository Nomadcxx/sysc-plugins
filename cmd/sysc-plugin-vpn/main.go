package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/vpn"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := runPlugin(os.Stdin, os.Stdout, environment{}); err != nil {
		os.Exit(1)
	}
}

type environment struct {
	now         func() time.Time
	lookPath    func(string) (string, error)
	callTimeout time.Duration
}

type view struct {
	kind    v1.ViewKind
	width   int
	rev     uint64
	root    *v1.Node
	encoded []byte
}

type result struct {
	kind        string
	provider    vpn.Provider
	generation  uint64
	observation vpn.Observation
	settings    vpn.Settings
	locations   []vpn.Location
	action      vpn.Action
	caps        vpn.ObscuraCaps
	bin         string
	err         error
}

// catalogue is one provider's location list and the state of reading it.
type catalogue struct {
	list                    []vpn.Location
	loaded, reading, wanted bool
	err                     string
}

func (s *session) catalogue(p vpn.Provider) *catalogue {
	if s.catalogues[p] == nil {
		s.catalogues[p] = &catalogue{}
	}
	return s.catalogues[p]
}

type session struct {
	env                                           environment
	ctx                                           context.Context
	client                                        *v1.Client
	cli                                           vpn.CLI
	machine                                       *vpn.Machine
	views                                         map[string]*view
	results                                       chan result
	workers                                       sync.WaitGroup
	screen, query, country, city, barMode, notice string
	page                                          int
	searchSeed                                    uint64
	confirm                                       string
	confirmValue, confirmWas                      bool
	confirmProvider                               vpn.Provider
	catalogues                                    map[vpn.Provider]*catalogue
	settings                                      map[vpn.Provider]vpn.Settings
	settingsLoaded                                map[vpn.Provider]bool
	settingsErrors                                map[vpn.Provider]string
	metadataEpoch                                 map[vpn.Provider]uint64
	settingsReads                                 map[vpn.Provider]uint64
	settingsWanted, readsWanted                   map[vpn.Provider]bool
	lastPoll                                      time.Time
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	if env.now == nil {
		env.now = time.Now
	}
	if env.lookPath == nil {
		env.lookPath = exec.LookPath
	}
	if env.callTimeout <= 0 {
		env.callTimeout = 5 * time.Second
	}
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.vpn", Name: "VPN", Version: "0.2.0"})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{env: env, ctx: ctx, client: c, views: map[string]*view{}, results: make(chan result, 16), screen: vpn.ScreenHome, barMode: "icon",
		catalogues: map[vpn.Provider]*catalogue{},
		settings:   map[vpn.Provider]vpn.Settings{}, settingsLoaded: map[vpn.Provider]bool{}, settingsErrors: map[vpn.Provider]string{},
		metadataEpoch: map[vpn.Provider]uint64{}, settingsReads: map[vpn.Provider]uint64{}, settingsWanted: map[vpn.Provider]bool{}, readsWanted: map[vpn.Provider]bool{}}
	defer func() {
		cancel()
		if closer, ok := in.(io.Closer); ok {
			_ = closer.Close()
		}
		s.workers.Wait()
	}()
	incoming := make(chan v1.Message, 32)
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
	s.discover()
	s.machine = vpn.NewMachine(s.cli.Installed(vpn.Mullvad), s.cli.Installed(vpn.Obscura))
	s.restoreProvider()
	s.machine.PreferInstalled()
	for _, p := range []vpn.Provider{vpn.Mullvad, vpn.Obscura} {
		s.requestRead(p)
	}
	s.lastPoll = env.now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				if m.View != v1.ViewPanel && m.View != v1.ViewBar && m.View != v1.ViewTooltip {
					continue
				}
				s.views[m.ViewID] = &view{kind: m.View, width: m.Width}
				if m.View == v1.ViewPanel {
					// Every opening lands on the home screen.
					s.screen, s.confirm, s.notice = vpn.ScreenHome, "", ""
					s.query, s.country, s.city, s.page = "", "", "", 0
					s.searchSeed++
					s.ensurePanelData(false)
				}
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v := s.views[m.ViewID]; v != nil {
					v.encoded = nil
				}
			case *v1.InputEvent:
				v := s.views[m.ViewID]
				if v == nil || m.Revision != v.rev || !allowedInput(v.root, m) {
					continue
				}
				s.handle(m)
			case *v1.SettingsChanged:
				if mode, ok := m.Values["bar_mode"].(string); ok && (mode == "icon" || mode == "status" || mode == "location") {
					s.barMode = mode
				}
			}
		case r := <-s.results:
			s.apply(r)
		case <-ticker.C:
			now := env.now()
			s.machine.Expire(now)
			// ponytail: polling costs up to five seconds of external-change
			// latency; use CLI event streams if that latency becomes a problem.
			if now.Sub(s.lastPoll) >= 5*time.Second {
				s.lastPoll = now
				for _, p := range []vpn.Provider{vpn.Mullvad, vpn.Obscura} {
					s.requestRead(p)
				}
				// The home screen's tiles show the settings.
				if s.panelOpen() {
					s.requestSettings(s.machine.Selected)
				}
			}
		}
		if err := s.publishAll(); err != nil {
			return err
		}
	}
}

func (s *session) discover() {
	s.cli.MullvadBin, _ = s.env.lookPath("mullvad")
	s.cli.ObscuraBin, _ = s.env.lookPath("obscura")
	s.cli.Obscura = vpn.ObscuraCaps{}
	if s.cli.ObscuraBin != "" {
		// The CLI's own help says what it supports; probed off the loop so
		// a hung binary cannot stall the panel.
		cli, ctx, bin := s.cli, s.ctx, s.cli.ObscuraBin
		s.launch(func() result {
			return result{kind: "caps", caps: cli.ProbeObscura(ctx), bin: bin}
		})
	}
	if s.machine != nil {
		for _, p := range []vpn.Provider{vpn.Mullvad, vpn.Obscura} {
			s.machine.SetInstalled(p, s.cli.Installed(p))
		}
		s.machine.PreferInstalled()
	}
}

func (s *session) call(kind v1.CallKind, params any) (v1.HostReply, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.env.callTimeout)
	defer cancel()
	reply, err := s.client.Call(ctx, kind, params)
	if err == nil && !reply.OK {
		err = errors.New("host rejected request")
	}
	return reply, err
}

func (s *session) restoreProvider() {
	reply, err := s.call(v1.CallStateGet, v1.StateGetParams{Key: "provider"})
	if err != nil {
		s.notice = "Could not restore provider preference"
		return
	}
	var state v1.StateGetResult
	if json.Unmarshal(reply.Result, &state) != nil {
		s.notice = "Invalid provider preference response"
		return
	}
	if !state.Found {
		return
	}
	var provider vpn.Provider
	if json.Unmarshal(state.Value, &provider) != nil || !provider.Valid() {
		s.notice = "Saved provider was invalid; choose a provider"
		return
	}
	_ = s.machine.Select(provider)
}

func (s *session) saveProvider() {
	raw, _ := json.Marshal(s.machine.Selected)
	if _, err := s.call(v1.CallStateSet, v1.StateSetParams{Key: "provider", Value: raw}); err != nil {
		s.notice = "Provider selected; preference could not be saved"
	}
}

func (s *session) launch(work func() result) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		r := work()
		select {
		case s.results <- r:
		case <-s.ctx.Done():
		}
	}()
}

func (s *session) requestRead(p vpn.Provider) {
	if !s.cli.Installed(p) {
		return
	}
	s.readsWanted[p] = true
	gen, ok := s.machine.BeginRead(p)
	if !ok {
		return
	}
	s.readsWanted[p] = false
	cli, ctx := s.cli, s.ctx
	s.launch(func() result {
		o, err := cli.Status(ctx, p)
		return result{kind: "status", provider: p, generation: gen, observation: o, err: err}
	})
}

func (s *session) requestSettings(p vpn.Provider) {
	if !s.cli.Installed(p) {
		return
	}
	s.settingsWanted[p] = true
	if _, reading := s.settingsReads[p]; reading {
		return
	}
	s.settingsWanted[p] = false
	gen := s.metadataEpoch[p]
	// A poll leaves the current values usable; only mutate marks them stale.
	s.settingsReads[p] = gen
	cli, root := s.cli, s.ctx
	s.launch(func() result {
		ctx, cancel := context.WithTimeout(root, 15*time.Second)
		defer cancel()
		value, err := cli.Settings(ctx, p)
		return result{kind: "settings", provider: p, generation: gen, settings: value, err: err}
	})
}

func (s *session) requestLocations(p vpn.Provider) {
	if !s.cli.Installed(p) || p == vpn.Obscura && !s.cli.Obscura.Locations {
		return
	}
	c := s.catalogue(p)
	c.wanted = true
	if c.reading {
		return
	}
	c.reading, c.wanted = true, false
	cli, ctx := s.cli, s.ctx
	s.launch(func() result {
		locations, err := cli.Locations(ctx, p)
		return result{kind: "locations", provider: p, locations: locations, err: err}
	})
}

func (s *session) apply(r result) {
	switch r.kind {
	case "status":
		s.machine.ApplyRead(r.provider, r.generation, r.observation, r.err, s.env.now())
		if s.readsWanted[r.provider] {
			s.requestRead(r.provider)
		}
	case "settings":
		if gen, ok := s.settingsReads[r.provider]; ok && gen == r.generation {
			delete(s.settingsReads, r.provider)
		}
		if r.generation == s.metadataEpoch[r.provider] {
			if r.err != nil {
				s.settingsErrors[r.provider] = r.err.Error()
			} else {
				s.settings[r.provider], s.settingsLoaded[r.provider], s.settingsErrors[r.provider] = r.settings, true, ""
			}
		}
		if s.settingsWanted[r.provider] {
			s.requestSettings(r.provider)
		}
	case "locations":
		c := s.catalogue(r.provider)
		c.reading = false
		if r.err != nil {
			c.err = r.err.Error()
		} else {
			c.list, c.loaded, c.err = r.locations, true, ""
		}
		if c.wanted {
			s.requestLocations(r.provider)
		}
	case "caps":
		if r.bin == s.cli.ObscuraBin {
			s.cli.Obscura = r.caps
			s.metadataEpoch[vpn.Obscura]++
			if s.panelOpen() {
				s.requestSettings(vpn.Obscura)
				if s.machine.Selected == vpn.Obscura {
					s.requestLocations(vpn.Obscura)
				}
			}
		}
	case "mutation":
		if s.machine.CompleteMutation(r.provider, r.generation, r.err, s.env.now()) {
			s.metadataEpoch[r.provider]++
			s.requestRead(r.provider)
			s.requestRead(r.provider.Other())
			if r.action.Kind == "setting" || r.action.Kind == "location" || s.panelOpen() {
				s.requestSettings(r.provider)
			}
		}
	}
}

func (s *session) mutate(action vpn.Action) {
	p := s.machine.Selected
	gen, err := s.machine.BeginMutation(p, action, s.env.now())
	if err != nil {
		s.notice = err.Error()
		return
	}
	s.confirm, s.notice = "", ""
	s.metadataEpoch[p]++
	s.settingsErrors[p] = "Refreshing settings after action"
	cli, root, locations := s.cli, s.ctx, s.catalogue(p).list
	s.launch(func() result {
		ctx, cancel := context.WithTimeout(root, 35*time.Second)
		defer cancel()
		err := cli.Mutate(ctx, p, action, locations)
		return result{kind: "mutation", provider: p, generation: gen, action: action, err: err}
	})
}

func (s *session) ensurePanelData(force bool) {
	// The tiles show the settings, and Mullvad's name the saved location.
	s.requestSettings(s.machine.Selected)
	if p := s.machine.Selected; force || !s.catalogue(p).loaded {
		s.requestLocations(p)
	}
}

func (s *session) panelOpen() bool {
	for _, v := range s.views {
		if v.kind == v1.ViewPanel {
			return true
		}
	}
	return false
}

func allowedInput(root *v1.Node, event *v1.InputEvent) bool {
	if root == nil {
		return false
	}
	if root.ID == event.Node && !root.Disabled {
		for _, kind := range root.Events {
			if kind == event.Event {
				return true
			}
		}
	}
	for _, c := range root.Children {
		if allowedInput(c, event) {
			return true
		}
	}
	return false
}

func (s *session) handle(event *v1.InputEvent) {
	id := event.Node
	if id == "search" && (event.Event == v1.EventChange || event.Event == v1.EventSubmit) {
		if len(event.Text) > 256 {
			s.notice = "Search is limited to 256 bytes"
			return
		}
		s.query, s.country, s.city, s.page = event.Text, "", "", 0
		return
	}
	if event.Event != v1.EventActivate {
		return
	}
	switch {
	case id == "bar":
		if _, err := s.call(v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: event.Output, Generation: event.Generation, Instance: event.ViewID}); err != nil {
			s.notice = "Could not open VPN panel"
		}
	case strings.HasPrefix(id, "provider:"):
		p := vpn.Provider(strings.TrimPrefix(id, "provider:"))
		if s.machine.Select(p) != nil {
			return
		}
		s.query, s.country, s.city, s.page, s.confirm, s.notice = "", "", "", 0, "", ""
		s.searchSeed++
		s.requestRead(p)
		s.ensurePanelData(false)
		s.saveProvider()
	case id == "refresh", id == "retry-read":
		s.notice = ""
		s.discover()
		for _, p := range []vpn.Provider{vpn.Mullvad, vpn.Obscura} {
			s.requestRead(p)
		}
		s.ensurePanelData(true)
	case id == "action":
		s.mutate(vpn.PrimaryAction(s.viewState()))
	case id == "open:locations", id == "open:dns", id == "open:bypass":
		s.screen, s.page, s.confirm, s.notice = strings.TrimPrefix(id, "open:"), 0, "", ""
		s.ensurePanelData(false)
	case id == "page:prev":
		s.page = max(s.page-1, 0)
	case id == "page:next":
		s.page++
	case id == "back":
		s.notice, s.page = "", 0
		switch {
		case s.confirm != "":
			s.confirm = ""
		case s.screen != vpn.ScreenLocations:
			s.screen = vpn.ScreenHome
		case s.query != "":
			s.query, s.country, s.city = "", "", ""
			s.searchSeed++
		case s.city != "":
			s.city = ""
		case s.country != "":
			s.country = ""
		default:
			s.screen = vpn.ScreenHome
		}
	case strings.HasPrefix(id, "browse:"):
		location := strings.TrimPrefix(id, "browse:")
		if !inCatalogue(location, s.catalogue(s.machine.Selected).list) {
			s.notice = "Location is not in the current list"
			return
		}
		parts := strings.Split(location, ":")
		if len(parts) != 3 || parts[2] != "" {
			return
		}
		s.country, s.city, s.query, s.page = parts[0], parts[1], "", 0
		s.searchSeed++
	case strings.HasPrefix(id, "location:"):
		connect := s.machine.State(s.machine.Selected).Observed.Phase == vpn.Disconnected
		// Home shows the move: the status card turns to Connecting….
		s.screen = vpn.ScreenHome
		s.mutate(vpn.Action{Kind: "location", Location: strings.TrimPrefix(id, "location:"), Connect: connect})
	case strings.HasPrefix(id, "set:"):
		p, key := s.machine.Selected, strings.TrimPrefix(id, "set:")
		if !s.settingsLoaded[p] || s.settingsErrors[p] != "" || p == vpn.Obscura && (!s.cli.Obscura.Settings || key == "lockdown-mode") {
			return
		}
		value, err := vpn.SettingValue(s.settings[p], key)
		if err != nil || value == nil {
			return
		}
		enabled := !*value
		if key == "lan" && enabled || key == "lockdown-mode" && !enabled {
			s.confirm, s.confirmProvider, s.confirmValue, s.confirmWas = key, p, enabled, *value
		} else {
			s.mutate(vpn.Action{Kind: "setting", Key: key, Enabled: enabled})
		}
	case id == "cancel":
		s.confirm = ""
	case id == "confirm":
		p, key := s.machine.Selected, s.confirm
		value, err := vpn.SettingValue(s.settings[p], key)
		if key == "" || p != s.confirmProvider || err != nil || value == nil || *value != s.confirmWas || s.settingsErrors[p] != "" {
			s.confirm, s.notice = "", "Setting changed; refresh and choose again"
			return
		}
		s.mutate(vpn.Action{Kind: "setting", Key: key, Enabled: s.confirmValue})
	}
}

func (s *session) viewState() vpn.ViewState {
	p := s.machine.Selected
	return vpn.ViewState{Machine: s.machine, Now: s.env.now(), Screen: s.screen, Query: s.query, Country: s.country, City: s.city, Page: s.page,
		Locations: s.catalogue(p).list, LocationsLoaded: s.catalogue(p).loaded, LocationsError: s.catalogue(p).err,
		Settings: s.settings[p], SettingsLoaded: s.settingsLoaded[p], SettingsError: s.settingsErrors[p],
		Confirm: s.confirm, Notice: s.notice, SearchSeed: s.searchSeed, Obscura: s.cli.Obscura, BrandDir: brandDir()}
}

func (s *session) publishAll() error {
	for id, v := range s.views {
		state := s.viewState()
		var tree *v1.Node
		switch v.kind {
		case v1.ViewPanel:
			tree = vpn.Panel(state)
		case v1.ViewBar:
			tree = vpn.Bar(state, v.width, s.barMode)
		case v1.ViewTooltip:
			tree = vpn.Tooltip(state)
		}
		encoded, err := json.Marshal(tree)
		if err != nil {
			return err
		}
		if bytes.Equal(encoded, v.encoded) {
			continue
		}
		v.rev++
		v.root, v.encoded = tree, encoded
		if err := s.client.Snapshot(id, v.rev, tree); err != nil {
			return err
		}
	}
	return nil
}

// brandDir is the shipped wordmarks' directory. The binary lives in
// <plugin>/bin/, so the marks sit one level up in assets/brand; a missing
// directory leaves the provider names in text.
var brandDir = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Join(filepath.Dir(exe), "..", "assets", "brand")
	if _, err := os.Stat(filepath.Join(dir, "mullvad-wordmark.png")); err != nil {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dir
})

// inCatalogue reports whether a browse target names a country or city in the
// provider's parsed list; it reaches no command, only the view's filter.
func inCatalogue(id string, list []vpn.Location) bool {
	for _, l := range list {
		if id == l.Country+"::" || id == l.Country+":"+l.City+":" || id == l.ID() {
			return true
		}
	}
	return false
}
