package main

import (
	"context"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/bar"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/panel"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func (s *session) tree(kind v1.ViewKind) *v1.Node {
	if kind == v1.ViewBar {
		return bar.Pill(s.barState(), s.missing, s.env.now())
	}
	return panel.BuildTree(s.panelState())
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	v.rev++
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, s.tree(v.kind))
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}

func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	node := m.Node[strings.Index(m.Node, ":")+1:] // host prefixes "<viewkind>:"
	switch {
	case node == "bar":
		if m.Event == v1.EventPointer && m.Button == v1.ButtonSecondary && len(s.running) > 0 {
			s.prefs.View = "playing"
			s.save(ctx, "prefs", s.prefs)
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
		case m.Event == v1.EventPointer && m.Button == v1.ButtonSecondary:
			s.selected, s.actions = id, true
		case s.selected == id && !s.actions:
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
	case strings.HasPrefix(node, "config-"), strings.HasPrefix(node, "remove-"):
		_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Open Lutris", Body: "Configure and uninstall live in the Lutris GUI"})
	case strings.HasPrefix(node, "more-"):
		s.actions = true
	case node == "detail-back":
		s.actions = false
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
