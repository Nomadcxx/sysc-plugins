package main

import (
	"context"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/bar"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/covers"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/panel"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/switcher"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func (s *session) tree(v view) *v1.Node {
	switch v.kind {
	case v1.ViewBar:
		return bar.PillAtWidth(s.barState(), s.missing, s.env.now(), v.width)
	case v1.ViewFloating:
		return switcher.Build(s.switcherState(), s.env.now())
	case v1.ViewTooltip:
		return bar.TooltipTree(s.barState(), s.missing, len(s.games), s.env.now())
	}
	return panel.BuildTree(s.panelState())
}

// switcherState lists running games newest-first with resolved cover paths.
func (s *session) switcherState() []switcher.Run {
	var out []switcher.Run
	for id, start := range s.running {
		g := s.game(id)
		if g == nil {
			continue
		}
		out = append(out, switcher.Run{ID: id, Name: g.Name, Start: start, CoverPath: covers.Resolve(*g, s.cacheDir)})
	}
	return out
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	v.rev++
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, s.tree(v))
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}

func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	// One left click on a node declaring activate and pointer arrives twice:
	// a primary pointer event on press, then activate on release. Acting on
	// both toggled the panel open and shut and made one click launch a game,
	// so the press is ignored and only activate acts on a left click.
	if m.Event == v1.EventPointer && m.Button == v1.ButtonPrimary {
		return
	}
	node := m.Node[strings.Index(m.Node, ":")+1:] // host prefixes "<viewID>:"
	switch {
	case node == "bar":
		if m.Event == v1.EventPointer && m.Button == v1.ButtonSecondary && len(s.running) > 0 {
			_, _ = s.call(ctx, v1.CallSurfaceOpen, v1.SurfaceOpenParams{
				Key: "switcher", Title: "Now playing",
				Output: m.Output, Generation: m.Generation,
				X: 60, Y: 60, Width: 320, Height: 400,
			})
			return
		}
		s.focusRunning()
		_, _ = s.call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
	case node == "search" && (m.Event == v1.EventChange || m.Event == v1.EventSubmit):
		s.query = m.Text
	case m.Event == v1.EventShortcut && node == "focus-search":
		_, _ = s.call(ctx, v1.CallViewFocus, v1.ViewFocusParams{View: m.ViewID, Node: "search"})
	case strings.HasPrefix(node, "section-"):
		if s.setSection(strings.TrimPrefix(node, "section-")) {
			s.save(ctx, "prefs", s.prefs)
		}
	case strings.HasPrefix(node, "card-"):
		id := strings.TrimPrefix(node, "card-")
		switch {
		case m.Event == v1.EventPointer:
			s.selected = id // a right-click selects; it never launches
		case s.selected == id:
			s.doLaunch(ctx, id) // Enter/re-click on the selected card launches
		default:
			s.selected = id
		}
	case strings.HasPrefix(node, "launch-"):
		s.doLaunch(ctx, strings.TrimPrefix(node, "launch-"))
	case strings.HasPrefix(node, "stop-"):
		if g := s.game(strings.TrimPrefix(node, "stop-")); g != nil && s.src != nil {
			_ = s.src.Stop(ctx, *g)
		}
	case strings.HasPrefix(node, "sw-stop-"):
		if g := s.game(strings.TrimPrefix(node, "sw-stop-")); g != nil && s.src != nil {
			_ = s.src.Stop(ctx, *g)
		}
		s.closeSwitcherIfIdle(ctx)
	case strings.HasPrefix(node, "sw-open-"):
		s.selected = strings.TrimPrefix(node, "sw-open-")
		_, _ = s.call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
	case strings.HasPrefix(node, "favtoggle-"):
		id := strings.TrimPrefix(node, "favtoggle-")
		s.prefs.Favorites[id] = !s.prefs.Favorites[id]
		s.save(ctx, "prefs", s.prefs)
	case strings.HasPrefix(node, "hidetoggle-"):
		id := strings.TrimPrefix(node, "hidetoggle-")
		s.prefs.Hidden[id] = !s.prefs.Hidden[id]
		s.save(ctx, "prefs", s.prefs)
	case strings.HasPrefix(node, "folder-"):
		if g := s.game(strings.TrimPrefix(node, "folder-")); g != nil && g.Directory != "" {
			_ = s.env.run(ctx, "xdg-open", g.Directory)
		}
	case strings.HasPrefix(node, "config-"):
		if g := s.game(strings.TrimPrefix(node, "config-")); g != nil && g.ConfigPath != "" {
			_ = s.env.run(ctx, "xdg-open", g.ConfigPath)
		} else {
			_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Open Lutris", Body: "No game config file found"})
		}
	case strings.HasPrefix(node, "remove-"):
		// ponytail: Lutris exposes no uninstall URI or CLI flag (verified in
		// lutris gui/application.py — only rungameid/rungame/install), so
		// removal stays GUI-side rather than hacking pga.db rows.
		_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{
			Summary: "Uninstall in Lutris",
			Body:    "Lutris has no uninstall API — right-click the game there",
		})
	}
}

// closeSwitcherIfIdle hides the floating switcher once nothing is running,
// so a stopped game doesn't leave an empty list hovering on screen.
func (s *session) closeSwitcherIfIdle(ctx context.Context) {
	if len(s.running) > 0 {
		return
	}
	for id, v := range s.views {
		if v.kind == v1.ViewFloating {
			_, _ = s.call(ctx, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: id})
		}
	}
}

// focusRunning preselects the newest running game so opening the panel from
// the pill lands on something actionable (design: "focused on running game").
func (s *session) focusRunning() {
	if s.selected != "" || len(s.running) == 0 {
		return
	}
	var best string
	var bestT time.Time
	for id, t := range s.running {
		if t.After(bestT) {
			best, bestT = id, t
		}
	}
	s.selected = best
}

func (s *session) setSection(name string) bool {
	for _, sec := range panel.Sections {
		if sec == name && s.prefs.View != name {
			s.prefs.View = name
			return true
		}
	}
	return false
}

func (s *session) doLaunch(ctx context.Context, id string) {
	g := s.game(id)
	if g == nil || s.src == nil {
		return
	}
	phase := s.machine.Phase(id)
	if phase == panel.PhaseLaunching || phase == panel.PhaseRunning {
		return
	}
	delete(s.failed, id)
	s.machine.Request(id, s.env.now())
	if err := s.src.Launch(ctx, *g); err != nil {
		_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Launch failed", Body: g.Name + ": " + err.Error()})
	}
	s.setPoll(time.Second) // the launching branch of desiredPoll
}
