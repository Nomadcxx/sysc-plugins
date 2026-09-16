// Command sysc-plugin-wallpaper-depth drives the vendored Depth Anything V2
// mask helper from the shell. The compositing half of the original noctalia
// plugin awaits a wallpaper API in the shell's plugin protocol.
package main

import (
	"context"
	"os"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
	wallpaperdepth "github.com/Nomadcxx/sysc-plugins/plugins/wallpaper-depth"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(v1.Identity{ID: "org.sysc.wallpaper-depth", Name: "Wallpaper Depth", Version: "0.1.0"}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const pluginID = "org.sysc.wallpaper-depth"
	session := wallpaperdepth.NewSession(wallpaperdepth.Helper{
		ScriptPath: wallpaperdepth.ScriptPath(),
		DataDir:    wallpaperdepth.DataDir(pluginID),
	})
	type view struct {
		kind     v1.ViewKind
		rev      uint64
		instance string
	}
	views := map[string]view{}

	settings := struct {
		wallpaper string
		autoGen   bool
		threshold int
		feather   int
	}{}

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

	tooltipReady := func(status wallpaperdepth.HelperStatus) bool {
		return status.Ready
	}

	publish := func() {
		status, errMsg, busy, lastMask := session.Snapshot()
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = wallpaperdepth.BarTree(status.Ready)
			case v1.ViewTooltip:
				root = wallpaperdepth.BarTree(tooltipReady(status))
			default:
				root = wallpaperdepth.PanelTree(status, errMsg, busy, lastMask, settings.wallpaper, settings.threshold, settings.feather)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	// Long operations run in the background; the session serializes them.
	async := func(fn func()) {
		go func() {
			fn()
			publish()
			if status, _, _, _ := session.Snapshot(); status.Ready {
				_, _ = c.Call(ctx, v1.CallNotify, v1.NotifyParams{
					Summary: "Wallpaper Depth", Body: "Done", Urgency: v1.UrgencyNormal,
				})
			}
		}()
	}

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
				if msg.View == v1.ViewPanel {
					go func() {
						session.Check(ctx)
						publish()
					}()
				}
			case *v1.ViewClose:
				delete(views, msg.ViewID)
			case *v1.InputEvent:
				switch msg.Node {
				case "check":
					async(func() { session.Check(ctx) })
				case "setup":
					async(func() { session.Setup(ctx) })
				case "generate":
					wallpaper := settings.wallpaper
					threshold, feather := settings.threshold, settings.feather
					async(func() { session.Generate(ctx, wallpaper, threshold, feather) })
				case "clear-cache":
					async(func() { session.ClearCache(ctx) })
				}
				publish()
			case *v1.SettingsChanged:
				changed := false
				if raw, ok := msg.Values["wallpaper_path"]; ok {
					if s, ok := raw.(string); ok {
						settings.wallpaper = s
						changed = true
					}
				}
				if raw, ok := msg.Values["auto_generate"]; ok {
					if b, ok := raw.(bool); ok {
						settings.autoGen = b
					}
				}
				if raw, ok := msg.Values["threshold"]; ok {
					if f, ok := raw.(float64); ok {
						settings.threshold = int(f)
						changed = true
					}
				}
				if raw, ok := msg.Values["feather"]; ok {
					if f, ok := raw.(float64); ok {
						settings.feather = int(f)
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
