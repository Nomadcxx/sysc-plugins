// Command sysc-plugin-mini-docker manages Docker containers from the shell
// bar, ported from the Noctalia community plugin of the same name.
package main

import (
	"context"
	"os"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
	minidocker "github.com/Nomadcxx/sysc-plugins/plugins/mini-docker"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.1.0"}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := minidocker.NewSession(minidocker.CLI{})
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
		containers, available, loading, errMsg := session.Snapshot()
		running := session.RunningCount()
		barText := minidocker.BarLabel(settings.showCount, settings.statusMode, running, available)
		tooltip := minidocker.TooltipText(running, available)
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = minidocker.BarTree(barText)
			case v1.ViewTooltip:
				root = minidocker.BarTree(tooltip)
			default:
				root = minidocker.PanelTree(available, loading, errMsg, containers)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	poll := func() {
		session.Refresh(ctx)
		publish()
	}

	go func() {
		poll()
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(settings.interval):
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
				views[msg.ViewID] = view{kind: msg.View, instance: msg.Instance}
				publish()
			case *v1.ViewClose:
				delete(views, msg.ViewID)
			case *v1.InputEvent:
				switch {
				case msg.Node == "refresh":
					poll()
				case len(msg.Node) > 6 && msg.Node[:6] == "start:":
					go func(id string) { session.Act(ctx, "start", id); publish() }(msg.Node[6:])
				case len(msg.Node) > 5 && msg.Node[:5] == "stop:":
					go func(id string) { session.Act(ctx, "stop", id); publish() }(msg.Node[5:])
				case len(msg.Node) > 8 && msg.Node[:8] == "restart:":
					go func(id string) { session.Act(ctx, "restart", id); publish() }(msg.Node[8:])
				}
			case *v1.SettingsChanged:
				changed := false
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
				if changed {
					publish()
				}
			}
		}
	}
}
