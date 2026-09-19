// Command sysc-plugin-kdeconnect is the Phone Connect plugin.
package main

import (
	"context"
	"os"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/kdeconnect"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.kdeconnect", Name: "Phone Connect", Version: "0.1.0"})); err != nil {
		return err
	}
	svc := kdeconnect.New()
	defer svc.Close()

	type view struct {
		kind v1.ViewKind
		rev  uint64
	}
	views := map[string]view{}
	var snap kdeconnect.Snapshot

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

	publish := func() {
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = kdeconnect.BarTree(snap)
			case v1.ViewTooltip:
				root = kdeconnect.TooltipTree(snap)
			default:
				root = kdeconnect.PanelTree(snap)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case s := <-svc.Updates():
			snap = s
			publish()
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				views[m.ViewID] = view{kind: m.View}
				publish()
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.ViewResync:
				publish()
			case *v1.InputEvent:
				handleInput(ctx, c, svc, m)
			case *v1.SettingsChanged:
				svc.Reconfigure(settingsFrom(m.Values))
			}
		}
	}
}

func handleInput(ctx context.Context, c *v1.Client, svc *kdeconnect.Service, m *v1.InputEvent) {
	switch m.Node {
	case "open":
		_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
	case "refresh":
		svc.Refresh()
	}
}

func settingsFrom(values map[string]any) kdeconnect.Settings {
	s := kdeconnect.DefaultSettings()
	if raw, ok := values["refresh_seconds"].(float64); ok {
		s.RefreshSeconds = raw
	}
	if raw, ok := values["enable_clipboard_action"].(bool); ok {
		s.EnableClipboard = raw
	}
	if raw, ok := values["show_device_card"].(bool); ok {
		s.ShowDeviceCard = raw
	}
	return s
}
