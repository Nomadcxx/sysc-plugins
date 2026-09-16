// Command sysc-plugin-github-notifications polls GitHub notifications through
// the gh CLI and presents them as a bar count and a panel list, ported from
// the Noctalia community plugin of the same name.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
	githubnotifications "github.com/Nomadcxx/sysc-plugins/plugins/github-notifications"
)

const (
	stateCacheKey = "cache"
	minInterval   = 30 * time.Second
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(v1.Identity{ID: "org.sysc.github-notifications", Name: "GitHub Notifications", Version: "0.1.0"}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := githubnotifications.NewSession(githubnotifications.CLI{}, 50)
	type view struct {
		kind     v1.ViewKind
		rev      uint64
		instance string
	}
	views := map[string]view{}

	settings := struct {
		interval     time.Duration
		displayMode  string
		hideWhenZero bool
	}{interval: 120 * time.Second, displayMode: "icon_and_count"}

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

	saveCache := func() {
		_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: stateCacheKey, Value: session.Cache()})
	}

	if reply, err := c.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: stateCacheKey}); err == nil && reply.OK {
		var result v1.StateGetResult
		if json.Unmarshal(reply.Result, &result) == nil && result.Found {
			session.RestoreCache(result.Value)
		}
	}

	publish := func() {
		status, errMsg := session.Status()
		items, unread := session.Items()
		updated, stale := session.UpdatedAt()
		statusLine := statusLine(status, unread, updated, stale)
		displayCount := countText(settings.displayMode, settings.hideWhenZero, status, unread)
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = githubnotifications.BarTree(displayCount, unread > 0)
			case v1.ViewTooltip:
				root = githubnotifications.TooltipTree(statusLine)
			default:
				root = githubnotifications.PanelTree(statusLine, errMsg, items)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	refresh := func() {
		if session.Refresh(ctx) {
			// The unread count grew past the previous value; a toast is the
			// one interruption worth causing.
			_, unread := session.Items()
			_, _ = c.Call(ctx, v1.CallNotify, v1.NotifyParams{
				Summary: "GitHub", Body: unreadText(unread), Urgency: v1.UrgencyNormal,
			})
		}
		saveCache()
		publish()
	}

	go func() {
		// First fetch happens immediately; the ticker keeps the list fresh.
		refresh()
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(settings.interval):
				refresh()
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
					refresh()
				case msg.Node == "mark-all":
					session.MarkAll(ctx)
					saveCache()
					publish()
				case len(msg.Node) > 5 && msg.Node[:5] == "open:":
					openURL(ctx, msg.Node[5:])
				case len(msg.Node) > 5 && msg.Node[:5] == "read:":
					session.MarkRead(ctx, msg.Node[5:])
					saveCache()
					publish()
				}
			case *v1.SettingsChanged:
				changed := false
				if raw, ok := msg.Values["refresh_interval_seconds"]; ok {
					if f, ok := raw.(float64); ok {
						sec := int(f)
						if sec < 30 {
							sec = 30
						}
						settings.interval = time.Duration(sec) * time.Second
						changed = true
					}
				}
				if raw, ok := msg.Values["per_page"]; ok {
					if f, ok := raw.(float64); ok {
						session.SetPerPage(int(f))
					}
				}
				if raw, ok := msg.Values["display_mode"]; ok {
					if s, ok := raw.(string); ok && (s == "icon_and_count" || s == "icon_only") {
						settings.displayMode = s
						changed = true
					}
				}
				if raw, ok := msg.Values["hide_when_zero"]; ok {
					if b, ok := raw.(bool); ok {
						settings.hideWhenZero = b
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

func openURL(ctx context.Context, url string) {
	if _, err := exec.LookPath("xdg-open"); err != nil {
		return
	}
	exec.CommandContext(ctx, "xdg-open", url).Start()
}

func statusLine(status githubnotifications.Status, unread int, updated time.Time, stale bool) string {
	base := ""
	switch status {
	case githubnotifications.StatusLoading:
		base = "Loading notifications…"
	case githubnotifications.StatusAuth:
		base = "gh authentication failed"
	case githubnotifications.StatusRate:
		base = "GitHub rate limit reached"
	case githubnotifications.StatusError:
		base = "Notifications unavailable"
	default:
		if unread == 0 {
			base = "No unread notifications"
		} else {
			base = unreadText(unread)
		}
	}
	if !updated.IsZero() {
		base += " · updated " + updated.Format("15:04")
	}
	if stale {
		base += " (cached)"
	}
	return base
}

func unreadText(unread int) string {
	if unread == 1 {
		return "1 unread notification"
	}
	return strconv.Itoa(unread) + " unread notifications"
}

func countText(mode string, hideWhenZero bool, status githubnotifications.Status, unread int) string {
	if mode == "icon_only" {
		return ""
	}
	if status == githubnotifications.StatusLoading && unread == 0 {
		return "…"
	}
	if unread == 0 && hideWhenZero {
		return ""
	}
	return strconv.Itoa(unread)
}
