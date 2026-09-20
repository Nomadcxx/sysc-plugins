// Command sysc-plugin-kdeconnect is the Phone Connect plugin.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/kdeconnect"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

// uiState is the entry point's view state: which composer is open and what
// the user has typed into its fields. The host owns the live text buffers;
// these are the committed values the sends use.
type uiState struct {
	composer  kdeconnect.Composer
	shareText string
	shareFile string
	smsNumber string
	smsBody   string
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
	settings := kdeconnect.DefaultSettings()
	ui := uiState{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The reader must run before any host call: a Call blocks until its
	// reply is decoded, so without the reader it would deadlock here.
	incoming := make(chan v1.Message, 8)
	var lastAvailable *bool
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

	// Restore the saved device choice before any view can render, so the
	// first snapshot already carries it.
	if saved := restoreSelection(ctx, c); saved != "" {
		svc.SetSelected(saved)
	}

	// publish pushes the current snapshot into every open view. A non-nil
	// delta patches the panel views instead of resending them; a patch the
	// host refuses falls back to the full snapshot at the new revision.
	publish := func(delta []v1.Replacement) {
		for id, v := range views {
			v.rev++
			views[id] = v
			if delta != nil && v.kind == v1.ViewPanel {
				if err := c.Patch(id, v.rev-1, v.rev, delta); err == nil {
					continue
				}
			}
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = kdeconnect.BarTree(snap)
			case v1.ViewTooltip:
				root = kdeconnect.TooltipTree(snap)
			default:
				root = kdeconnect.PanelTree(snap, settings, ui.composer)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case s := <-svc.Updates():
			delta := kdeconnect.PanelDelta(snap, s)
			snap = s
			if lastAvailable == nil || *lastAvailable != snap.Available {
				status := v1.PluginStatus{State: v1.StatusOK}
				if !snap.Available {
					status.State = v1.StatusError
					status.Message = "KDE Connect daemon unreachable"
				}
				_ = c.Send(&status)
				available := snap.Available
				lastAvailable = &available
			}
			publish(delta)
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				views[m.ViewID] = view{kind: m.View}
				if m.View == v1.ViewPanel {
					// Opening the panel re-reads the daemon first, so the
					// device state is fresh, the reference shell's behaviour
					// on popout open.
					svc.Refresh()
				}
				publish(nil)
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.ViewResync:
				publish(nil)
			case *v1.InputEvent:
				if handleInput(ctx, c, svc, m, &ui, snap) {
					publish(nil)
				}
			case *v1.SettingsChanged:
				settings = settingsFrom(m.Values)
				svc.Reconfigure(settings)
				publish(nil)
			}
		case e := <-svc.Events():
			notify(ctx, c, e)
		}
	}
}

// handleInput routes one input event. It reports whether the panel tree
// changed and needs a republish — toggles and sends do, typing does not.
func handleInput(ctx context.Context, c *v1.Client, svc *kdeconnect.Service, m *v1.InputEvent, ui *uiState, snap kdeconnect.Snapshot) bool {
	device := snap.SelectedID
	switch {
	case m.Node == "open":
		_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
	case m.Node == "refresh":
		svc.Refresh()
	case strings.HasPrefix(m.Node, "select-"):
		id := strings.TrimPrefix(m.Node, "select-")
		svc.SetSelected(id)
		saveSelection(ctx, c, id)
	case m.Node == "share":
		return toggleComposer(ui, kdeconnect.ComposerShare)
	case m.Node == "sms":
		return toggleComposer(ui, kdeconnect.ComposerSMS)
	case m.Node == "share-text":
		if m.Event == v1.EventChange {
			ui.shareText = m.Text
			return false
		}
		// Submit sends the field's content, as a URL when it looks like one.
		svc.Do(kdeconnect.Action{Kind: shareKindFor(ui.shareText), DeviceID: device, Arg: ui.shareText})
		ui.composer = kdeconnect.ComposerNone
		return true
	case m.Node == "share-url-send":
		svc.Do(kdeconnect.Action{Kind: kdeconnect.ActionShareURL, DeviceID: device, Arg: ui.shareText})
		ui.composer = kdeconnect.ComposerNone
		return true
	case m.Node == "share-text-send":
		svc.Do(kdeconnect.Action{Kind: kdeconnect.ActionShareText, DeviceID: device, Arg: ui.shareText})
		ui.composer = kdeconnect.ComposerNone
		return true
	case m.Node == "share-file":
		if m.Event == v1.EventChange {
			ui.shareFile = m.Text
			return false
		}
		svc.Do(kdeconnect.Action{Kind: kdeconnect.ActionShareFile, DeviceID: device, Arg: ui.shareFile})
		ui.composer = kdeconnect.ComposerNone
		return true
	case m.Node == "share-file-send":
		svc.Do(kdeconnect.Action{Kind: kdeconnect.ActionShareFile, DeviceID: device, Arg: ui.shareFile})
		ui.composer = kdeconnect.ComposerNone
		return true
	case m.Node == "sms-number":
		if m.Event == v1.EventChange {
			ui.smsNumber = m.Text
		}
	case m.Node == "sms-body":
		if m.Event == v1.EventChange {
			ui.smsBody = m.Text
		}
	case m.Node == "sms-send":
		svc.Do(kdeconnect.Action{Kind: kdeconnect.ActionSendSMS, DeviceID: device, Arg: ui.smsNumber, Arg2: ui.smsBody})
		ui.composer = kdeconnect.ComposerNone
		return true
	case m.Node == "sms-app":
		svc.Do(kdeconnect.Action{Kind: kdeconnect.ActionLaunchSMSApp, DeviceID: device})
		ui.composer = kdeconnect.ComposerNone
		return true
	}
	return false
}

func toggleComposer(ui *uiState, want kdeconnect.Composer) bool {
	if ui.composer == want {
		ui.composer = kdeconnect.ComposerNone
	} else {
		ui.composer = want
	}
	return true
}

// shareKindFor picks URL versus text sharing for a submitted share field.
func shareKindFor(text string) kdeconnect.ActionKind {
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		return kdeconnect.ActionShareURL
	}
	return kdeconnect.ActionShareText
}

// notify surfaces one service event as a shell toast, the DMS plugin's
// ToastService calls. A failed action raises the urgency.
func notify(ctx context.Context, c *v1.Client, e kdeconnect.Event) {
	p := v1.NotifyParams{Summary: e.Message, Body: e.Detail, Urgency: v1.UrgencyNormal}
	if e.Err != nil {
		p.Urgency = v1.UrgencyCritical
	}
	_, _ = c.Call(ctx, v1.CallNotify, p)
}

// restoreSelection reads the saved device choice from the plugin state.
func restoreSelection(ctx context.Context, c *v1.Client) string {
	reply, err := c.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "selected_device_id"})
	if err != nil || !reply.OK {
		return ""
	}
	var result v1.StateGetResult
	if err := json.Unmarshal(reply.Result, &result); err != nil || !result.Found {
		return ""
	}
	var id string
	if err := json.Unmarshal(result.Value, &id); err != nil {
		return ""
	}
	return id
}

// saveSelection writes the device choice the user picked. Auto-selection
// never writes: the saved choice is the user's alone.
func saveSelection(ctx context.Context, c *v1.Client, id string) {
	raw, err := json.Marshal(id)
	if err != nil {
		return
	}
	_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "selected_device_id", Value: raw})
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
