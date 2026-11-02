package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/lid-guard"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	pluginID  = "org.sysc.lid-guard"
	serviceID = "lid-guard"
)

type openView struct {
	kind     v1.ViewKind
	revision uint64
	width    int
	output   string
}

func main() {
	if err := run(os.Stdin, os.Stdout, lidguard.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-plugin-lid-guard:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer, options lidguard.Options) error {
	client := v1.NewClient(in, out)
	hello, err := client.Handshake(v1.Identity{ID: pluginID, Name: "Lid Guard", Version: "1.0.0"})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service := lidguard.New(options)
	if err := service.Request(ctx, "refresh"); err != nil && err != lidguard.ErrUnavailable {
		fmt.Fprintln(os.Stderr, "sysc-plugin-lid-guard: initial refresh:", err)
	}
	views := make(map[string]openView)
	var pendingAction string
	var statusRevision uint64
	var lastViewSnapshot lidguard.Snapshot
	hasViewSnapshot := false
	publish := func(forceViews bool) error {
		snapshot := service.Snapshot()
		if snapshot.Revision > statusRevision {
			if err := client.Send(&v1.ServiceStatus{
				ServiceID: serviceID, State: snapshot.State, Busy: snapshot.Busy,
				Message: snapshot.Message, Revision: snapshot.Revision,
			}); err != nil {
				return err
			}
			statusRevision = snapshot.Revision
		}
		if pendingAction != "" && !snapshot.Busy {
			if pendingAction != "refresh" && pendingAction != "status" {
				notifyAction(ctx, client, hello, snapshot)
			}
			pendingAction = ""
		}
		// Suppress redundant redraws from same-state status polls.
		if forceViews || !hasViewSnapshot || !sameViewState(snapshot, lastViewSnapshot) {
			for id, view := range views {
				view.revision++
				views[id] = view
				var root *v1.Node
				switch view.kind {
				case v1.ViewTooltip:
					root = lidguard.TooltipTree(snapshot)
				case v1.ViewPanel:
					root = lidguard.PanelTree(snapshot)
				default:
					root = lidguard.BarTreeAtWidth(snapshot, view.width)
				}
				if err := client.Snapshot(id, view.revision, root); err != nil {
					return err
				}
			}
			lastViewSnapshot = snapshot
			hasViewSnapshot = true
		}
		return nil
	}
	if err := publish(true); err != nil {
		return err
	}

	incoming := make(chan v1.Message, 16)
	go func() {
		for {
			msg, err := client.Recv()
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

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-service.Updates():
			if err := publish(false); err != nil {
				return err
			}
		case <-ticker.C:
			snapshot := service.Snapshot()
			if !snapshot.Busy && !snapshot.Refreshing {
				if err := service.Request(ctx, "refresh"); err != nil && err != lidguard.ErrUnavailable {
					fmt.Fprintln(os.Stderr, "sysc-plugin-lid-guard: periodic refresh:", err)
				}
			}
		case message := <-incoming:
			switch m := message.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				width := m.Width
				if width <= 0 {
					width = 240
				}
				views[m.ViewID] = openView{kind: m.View, width: width, output: m.Output}
				if err := publish(true); err != nil {
					return err
				}
			case *v1.ViewResize:
				if view, ok := views[m.ViewID]; ok {
					view.width = m.Width
					views[m.ViewID] = view
					if err := publish(true); err != nil {
						return err
					}
				}
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.ViewResync:
				if view, ok := views[m.ViewID]; ok {
					view.revision = 0
					views[m.ViewID] = view
					if err := publish(true); err != nil {
						return err
					}
				}
			case *v1.ServiceAction:
				if m.ServiceID != serviceID {
					continue
				}
				if err := service.Request(ctx, m.ActionID); err != nil {
					fmt.Fprintln(os.Stderr, "sysc-plugin-lid-guard: service action:", err)
					continue
				}
				pendingAction = m.ActionID
				if m.ActionID != "refresh" && m.ActionID != "status" {
					if err := publish(false); err != nil {
						return err
					}
				}
			case *v1.InputEvent:
				view, ok := views[m.ViewID]
				if !ok || view.revision != m.Revision {
					continue
				}
				action, openPanel := lidguard.HandleInput(m, service.Snapshot())
				if action != "" {
					if err := service.Request(ctx, action); err != nil {
						fmt.Fprintln(os.Stderr, "sysc-plugin-lid-guard: UI action:", err)
						continue
					}
					pendingAction = action
				}
				if openPanel && view.kind == v1.ViewBar {
					output := inputOutput(ctx, client, m, view)
					if _, err := client.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: output, Instance: m.ViewID}); err != nil {
						fmt.Fprintln(os.Stderr, "sysc-plugin-lid-guard: open panel:", err)
					}
				}
				if action != "" && action != "refresh" && action != "status" {
					if err := publish(false); err != nil {
						return err
					}
				}
			}
		}
	}
}

func sameViewState(a, b lidguard.Snapshot) bool {
	return a.State == b.State && a.Available == b.Available && a.Busy == b.Busy &&
		a.Refreshing == b.Refreshing && a.Message == b.Message
}

func inputOutput(ctx context.Context, client *v1.Client, input *v1.InputEvent, view openView) string {
	if input.Output != "" {
		return input.Output
	}
	if view.output != "" {
		return view.output
	}
	reply, err := client.Call(ctx, v1.CallOutputContext, v1.OutputContextParams{Generation: input.Generation})
	if err != nil || !reply.OK {
		return ""
	}
	var output v1.OutputContextResult
	if json.Unmarshal(reply.Result, &output) != nil {
		return ""
	}
	return output.Output
}

func notifyAction(ctx context.Context, client *v1.Client, hello *v1.HostHello, snapshot lidguard.Snapshot) {
	if hello == nil || !hasCapability(hello.Capabilities, "notifications") {
		return
	}
	summary, body := "Lid Guard", snapshot.Message
	switch snapshot.State {
	case v1.ServiceStateActive:
		summary, body = "Lid Guard enabled", "Lid close is now inhibited while the user service runs."
	case v1.ServiceStateInactive:
		summary, body = "Lid Guard disabled", "The system's normal lid-close policy is back in effect."
	case v1.ServiceStateUnavailable:
		summary = "Lid Guard unavailable"
	case v1.ServiceStateError:
		summary = "Lid Guard could not change state"
	}
	if body == "" {
		body = "Systemd did not confirm the requested state."
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, _ = client.Call(notifyCtx, v1.CallNotify, v1.NotifyParams{Summary: summary, Body: strings.TrimSpace(body), Urgency: v1.UrgencyNormal})
}

func hasCapability(capabilities []string, capability string) bool {
	for _, got := range capabilities {
		if got == capability {
			return true
		}
	}
	return false
}
