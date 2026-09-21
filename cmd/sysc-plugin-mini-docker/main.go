// Command sysc-plugin-mini-docker manages Docker containers from the shell
// bar, ported from the Noctalia community plugin of the same name.
package main

import (
	"context"
	"os"
	"sync"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	minidocker "github.com/Nomadcxx/sysc-plugins/plugins/mini-docker"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

// newSession is the seam tests replace to keep the real docker CLI out of
// the harness.
var newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) }

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.1.0"})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := newSession()
	type view struct {
		kind     v1.ViewKind
		rev      uint64
		instance string
	}
	views := map[string]view{}

	settings := struct {
		interval   time.Duration
		showCount  bool
		statusMode string
	}{interval: 5 * time.Second, showCount: true, statusMode: "always"}

	// mu guards views and settings. publish runs from the poller and from
	// action goroutines, so every access to this shared state goes through
	// it; the v1 encoder serializes one publish at a time with it.
	// ponytail: a single mutex beats a single-writer funnel at this size;
	// upgrade path is the funnel if lock contention ever shows on a profile.
	var mu sync.Mutex

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

	publish := func() {
		mu.Lock()
		defer mu.Unlock()
		containers, available, loading, listErr, actErr, actingID := session.Snapshot()
		running := session.RunningCount()
		barText := minidocker.BarLabel(settings.showCount, settings.statusMode, running, available)
		tooltip := minidocker.TooltipText(running, available)
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = minidocker.BarTree(barText, !available)
			case v1.ViewTooltip:
				root = minidocker.TooltipTree(tooltip)
			default:
				root = minidocker.PanelTree(available, loading, listErr, actErr, actingID, containers)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	poll := func() {
		session.Refresh(ctx)
		publish()
	}

	// refresh signals the poller goroutine that a click asked for an
	// immediate poll. Size 1 + non-blocking send coalesces a burst of
	// clicks into one extra poll and never blocks the main loop; the poll
	// itself runs off the main loop so a hung docker cannot freeze input.
	refresh := make(chan struct{}, 1)

	go func() {
		poll()
		for {
			mu.Lock()
			interval := settings.interval
			mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
				poll()
			case <-refresh:
				poll()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch msg := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				mu.Lock()
				views[msg.ViewID] = view{kind: msg.View, instance: msg.Instance}
				mu.Unlock()
				publish()
			case *v1.ViewClose:
				mu.Lock()
				delete(views, msg.ViewID)
				mu.Unlock()
			case *v1.InputEvent:
				switch {
				case msg.Node == "open":
				_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: msg.Output, Instance: msg.ViewID})
			case msg.Node == "refresh":
				select {
				case refresh <- struct{}{}:
				default:
				}
				case len(msg.Node) > 6 && msg.Node[:6] == "start:":
					go func(id string) { session.Act(ctx, "start", id); publish() }(msg.Node[6:])
				case len(msg.Node) > 5 && msg.Node[:5] == "stop:":
					go func(id string) { session.Act(ctx, "stop", id); publish() }(msg.Node[5:])
				case len(msg.Node) > 8 && msg.Node[:8] == "restart:":
					go func(id string) { session.Act(ctx, "restart", id); publish() }(msg.Node[8:])
				}
			case *v1.SettingsChanged:
				changed := false
				mu.Lock()
				if raw, ok := msg.Values["refresh_interval_seconds"]; ok {
					if f, ok := raw.(float64); ok && f >= 1 && f <= 30 {
						settings.interval = time.Duration(f) * time.Second
					}
				}
				if raw, ok := msg.Values["show_count"]; ok {
					if b, ok := raw.(bool); ok {
						settings.showCount = b
						changed = true
					}
				}
				if raw, ok := msg.Values["status_mode"]; ok {
					if s, ok := raw.(string); ok && (s == "always" || s == "running_only" || s == "hidden") {
						settings.statusMode = s
						changed = true
					}
				}
				mu.Unlock()
				if changed {
					publish()
				}
			}
		}
	}
}
