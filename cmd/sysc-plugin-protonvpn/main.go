package main

import (
	"context"
	"io"
	"os"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	env := environment{now: time.Now, callTimeout: 5 * time.Second}
	if err := runPlugin(os.Stdin, os.Stdout, env); err != nil {
		os.Exit(1)
	}
}

type environment struct {
	now func() time.Time
	// callTimeout bounds every host call. Calls run on the loop, and a reply
	// queued behind a burst of input would otherwise never be read.
	callTimeout time.Duration
}

type settings struct {
	refreshSeconds    int
	trafficMonitoring bool
	notifyOnConnect   bool
	barMode           string
	quickConnect      string
}

func defaultSettings() settings {
	return settings{refreshSeconds: 5, trafficMonitoring: true, notifyOnConnect: true, barMode: "code", quickConnect: "fastest"}
}

// apply takes the values the host sent; a missing or mistyped key keeps its
// current value, and refresh_seconds is clamped to the manifest's 2–60.
func (s *settings) apply(values map[string]any) {
	if v, ok := values["refresh_seconds"].(float64); ok {
		s.refreshSeconds = int(min(max(v, 2), 60))
	}
	if v, ok := values["traffic_monitoring"].(bool); ok {
		s.trafficMonitoring = v
	}
	if v, ok := values["notify_on_connect"].(bool); ok {
		s.notifyOnConnect = v
	}
	if v, ok := values["bar_mode"].(string); ok {
		s.barMode = v
	}
	if v, ok := values["quick_connect"].(string); ok {
		s.quickConnect = v
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
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.protonvpn", Name: "ProtonVPN", Version: "1.0.0"})); err != nil {
		return err
	}
	s := &session{env: env, client: c, settings: defaultSettings(), views: map[string]view{}}
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
	for {
		select {
		case <-ctx.Done():
			return nil
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

func (s *session) tree(kind v1.ViewKind) *v1.Node {
	switch kind {
	case v1.ViewBar:
		return barTree(s)
	case v1.ViewTooltip:
		return tooltipTree(s)
	}
	return panelTree(s)
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

// handle is a stub until the event-loop task wires commands and tabs.
func (s *session) handle(_ context.Context, _ *v1.InputEvent) {}

func barTree(s *session) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: "vpn_key_off", Tone: v1.ToneSubtle},
	}}
}

func panelTree(s *session) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: 12, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "ProtonVPN", Size: "title", Bold: true},
	}}
}

func tooltipTree(s *session) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Unprotected", Tone: v1.ToneSubtle},
	}}
}
