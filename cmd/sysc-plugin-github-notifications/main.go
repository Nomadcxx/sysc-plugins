// Command sysc-plugin-github-notifications presents GitHub's unread inbox,
// open work, and contribution calendar in one native panel.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	githubnotifications "github.com/Nomadcxx/sysc-plugins/plugins/github-notifications"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	stateCacheKey = "cache"
	minInterval   = 30 * time.Second
	defaultPage   = 100
)

func main() {
	if err := runPlugin(os.Stdin, os.Stdout, githubnotifications.CLI{}, openURL); err != nil {
		os.Exit(1)
	}
}

type panelView struct {
	kind     v1.ViewKind
	rev      uint64
	mode     string
	workKind githubnotifications.WorkKind
	search   string
}

type pluginSettings struct {
	interval     time.Duration
	displayMode  string
	hideWhenZero bool
}

func runPlugin(in io.Reader, out io.Writer, gh githubnotifications.GH, opener func(context.Context, string) error) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.github-notifications", Name: "GitHub Notifications", Version: "0.3.0"})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	coordinator := githubnotifications.NewCoordinator(ctx)
	defer func() {
		cancel()
		coordinator.Close()
	}()

	session := githubnotifications.NewSession(gh, defaultPage)
	views := map[string]panelView{}
	settings := pluginSettings{interval: 120 * time.Second, displayMode: "icon_and_count"}
	refreshing := false
	toastUnread := make(chan bool, 64)

	incoming := make(chan v1.Message, 16)
	go func() {
		for {
			msg, err := c.Recv()
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

	if reply, err := c.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: stateCacheKey}); err == nil && reply.OK {
		var result v1.StateGetResult
		if json.Unmarshal(reply.Result, &result) == nil && result.Found {
			session.RestoreCache(result.Value)
		}
	}

	saveCache := func() {
		callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		_, _ = c.Call(callCtx, v1.CallStateSet, v1.StateSetParams{Key: stateCacheKey, Value: session.Cache()})
	}

	publish := func() {
		inbox := session.Inbox()
		unread := len(inbox.Items)
		statusLine := statusLine(session, refreshing)
		count := countText(settings.displayMode, settings.hideWhenZero, inbox.Status, unread, inbox.HasMore)
		for id, view := range views {
			view.rev++
			views[id] = view
			var root *v1.Node
			switch view.kind {
			case v1.ViewBar:
				root = githubnotifications.BarTree(count, unread > 0 || inbox.HasMore)
			case v1.ViewTooltip:
				root = githubnotifications.TooltipTree(statusLine)
			default:
				feeds := map[githubnotifications.WorkKind]githubnotifications.WorkSnapshot{
					githubnotifications.WorkReviews: session.Work(githubnotifications.WorkReviews),
					githubnotifications.WorkMyPRs:   session.Work(githubnotifications.WorkMyPRs),
					githubnotifications.WorkIssues:  session.Work(githubnotifications.WorkIssues),
				}
				root = githubnotifications.PanelTreeForState(githubnotifications.PanelState{
					ViewID: id, Mode: view.mode, WorkKind: view.workKind, Search: view.search, StatusLine: statusLine,
					Inbox: inbox, Work: feeds[view.workKind], WorkFeeds: feeds, Activity: session.ActivityFeed(),
				})
			}
			_ = c.Snapshot(id, view.rev, root)
		}
	}

	refreshRun := func(runCtx context.Context) {
		grew := session.Refresh(runCtx)
		for _, kind := range []githubnotifications.WorkKind{githubnotifications.WorkReviews, githubnotifications.WorkMyPRs, githubnotifications.WorkIssues} {
			if runCtx.Err() != nil {
				break
			}
			_ = session.RefreshWork(runCtx, kind)
		}
		if runCtx.Err() == nil {
			_ = session.RefreshActivity(runCtx)
		}
		select {
		case toastUnread <- grew:
		case <-runCtx.Done():
		}
	}
	refreshComplete := func() {
		refreshing = coordinator.Refreshing()
		if <-toastUnread {
			inbox := session.Inbox()
			callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			_, _ = c.Call(callCtx, v1.CallNotify, v1.NotifyParams{
				Summary: "GitHub", Body: unreadText(len(inbox.Items), inbox.HasMore), Urgency: v1.UrgencyNormal,
			})
			stop()
		}
		saveCache()
		publish()
	}
	requestRefresh := func() {
		refreshing = true
		coordinator.RequestRefresh(refreshRun, refreshComplete)
		publish()
	}
	actionComplete := func() {
		saveCache()
		publish()
	}

	// Progress makes the optimistic removal visible before the serialized PATCH
	// starts; the worker waits for the event loop to publish that snapshot.
	progress := make(chan chan struct{}, 1)
	notifyProgress := func(runCtx context.Context) {
		ack := make(chan struct{})
		select {
		case progress <- ack:
		case <-runCtx.Done():
			return
		}
		select {
		case <-ack:
		case <-runCtx.Done():
		}
	}
	queueWorkRefresh := func(kind githubnotifications.WorkKind) {
		coordinator.Submit(func(runCtx context.Context) { _ = session.RefreshWork(runCtx, kind) }, actionComplete)
	}

	requestRefresh()
	timer := time.NewTimer(settings.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			requestRefresh()
			timer.Reset(settings.interval)
		case complete := <-coordinator.Completed():
			if complete != nil {
				complete()
			}
		case ack := <-progress:
			publish()
			close(ack)
		case msg := <-incoming:
			switch msg := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				views[msg.ViewID] = panelView{kind: msg.View, mode: githubnotifications.ModeInbox, workKind: githubnotifications.WorkReviews}
				publish()
			case *v1.ViewClose:
				delete(views, msg.ViewID)
			case *v1.ViewResync:
				if view, ok := views[msg.ViewID]; ok {
					view.rev = 0
					views[msg.ViewID] = view
					publish()
				}
			case *v1.InputEvent:
				view, ok := views[msg.ViewID]
				if !ok || msg.Revision != view.rev {
					continue
				}
				if msg.Event == v1.EventChange || msg.Event == v1.EventSubmit {
					if view.kind == v1.ViewPanel && msg.Node == "search" {
						view.search = msg.Text
						views[msg.ViewID] = view
						publish()
					}
					continue
				}
				if msg.Event != v1.EventActivate || view.kind != v1.ViewPanel {
					continue
				}
				switch {
				case msg.Node == "refresh":
					requestRefresh()
				case msg.Node == "mode:inbox":
					view.mode = githubnotifications.ModeInbox
					views[msg.ViewID] = view
					publish()
				case msg.Node == "mode:work":
					view.mode = githubnotifications.ModeWork
					views[msg.ViewID] = view
					publish()
				case msg.Node == "mode:activity":
					view.mode = githubnotifications.ModeActivity
					views[msg.ViewID] = view
					publish()
				case strings.HasPrefix(msg.Node, "work:"):
					kind := githubnotifications.WorkKind(strings.TrimPrefix(msg.Node, "work:"))
					if !validWorkKind(kind) {
						continue
					}
					view.mode, view.workKind = githubnotifications.ModeWork, kind
					views[msg.ViewID] = view
					publish()
					if session.Work(kind).UpdatedAt.IsZero() {
						queueWorkRefresh(kind)
					}
				case msg.Node == "mark-all":
					coordinator.Submit(func(runCtx context.Context) {
						if session.BeginMarkAll() {
							notifyProgress(runCtx)
							session.FinishMarkAll(runCtx)
						}
					}, actionComplete)
				case msg.Node == "load-more-inbox":
					coordinator.Submit(func(runCtx context.Context) { _ = session.LoadMoreInbox(runCtx) }, actionComplete)
				case msg.Node == "load-more-work":
					kind := view.workKind
					coordinator.Submit(func(runCtx context.Context) { _ = session.LoadMoreWork(runCtx, kind) }, actionComplete)
				case strings.HasPrefix(msg.Node, "read:"):
					id := strings.TrimPrefix(msg.Node, "read:")
					coordinator.Submit(func(runCtx context.Context) {
						if session.BeginMarkRead(id) {
							notifyProgress(runCtx)
							session.FinishMarkRead(runCtx, id)
						}
					}, actionComplete)
				case strings.HasPrefix(msg.Node, "open:"):
					id := strings.TrimPrefix(msg.Node, "open:")
					if item, found := notification(session.Inbox(), id); found && githubnotifications.CanonicalGitHubURL(item.URL) {
						coordinator.Submit(func(runCtx context.Context) {
							if opener == nil || opener(runCtx, item.URL) != nil {
								return
							}
							if session.BeginMarkRead(id) {
								notifyProgress(runCtx)
								session.FinishMarkRead(runCtx, id)
							}
						}, actionComplete)
					}
				case strings.HasPrefix(msg.Node, "work-open:"):
					for _, item := range session.Work(view.workKind).Items {
						if githubnotifications.WorkOpenID(item) == msg.Node && githubnotifications.CanonicalGitHubURL(item.URL) {
							coordinator.Submit(func(runCtx context.Context) {
								if opener != nil {
									_ = opener(runCtx, item.URL)
								}
							}, nil)
							break
						}
					}
				}
			case *v1.SettingsChanged:
				refreshIntervalChanged := false
				pageSizeChanged := false
				if raw, ok := msg.Values["refresh_interval_seconds"].(float64); ok {
					seconds := max(int(raw), int(minInterval.Seconds()))
					settings.interval = time.Duration(seconds) * time.Second
					refreshIntervalChanged = true
				}
				if raw, ok := msg.Values["per_page"].(float64); ok {
					session.SetPerPage(int(raw))
					pageSizeChanged = true
				}
				if raw, ok := msg.Values["display_mode"].(string); ok && (raw == "icon_and_count" || raw == "icon_only") {
					settings.displayMode = raw
				}
				if raw, ok := msg.Values["hide_when_zero"].(bool); ok {
					settings.hideWhenZero = raw
				}
				if refreshIntervalChanged {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(settings.interval)
				}
				if pageSizeChanged {
					requestRefresh()
				} else {
					publish()
				}
			}
		}
	}
}

func openURL(ctx context.Context, raw string) error {
	if !githubnotifications.CanonicalGitHubURL(raw) {
		return errors.New("refusing to open a non-canonical GitHub URL")
	}
	path, err := exec.LookPath("xdg-open")
	if err != nil {
		return err
	}
	openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(openCtx, path, raw).Run()
}

func notification(snapshot githubnotifications.InboxSnapshot, id string) (githubnotifications.Item, bool) {
	for _, item := range snapshot.Items {
		if item.ID == id {
			return item, true
		}
	}
	return githubnotifications.Item{}, false
}

func validWorkKind(kind githubnotifications.WorkKind) bool {
	return kind == githubnotifications.WorkReviews || kind == githubnotifications.WorkMyPRs || kind == githubnotifications.WorkIssues
}

func statusLine(session *githubnotifications.Session, refreshing bool) string {
	if refreshing {
		return "Refreshing GitHub…"
	}
	inbox := session.Inbox()
	status := inbox.Status
	updated := inbox.UpdatedAt
	stale := inbox.Stale
	count := inbox.CountLabel()
	base := count + " unread"
	if len(inbox.Items) == 0 {
		base = "No unread notifications"
	}
	switch status {
	case githubnotifications.StatusLoading:
		base = "Loading notifications…"
	case githubnotifications.StatusAuth:
		base = "gh authentication failed"
	case githubnotifications.StatusRate:
		base = "GitHub rate limit reached"
	case githubnotifications.StatusError:
		base = "Notifications unavailable"
	}
	if !updated.IsZero() {
		base += " · updated " + updated.Format("15:04")
	}
	if stale {
		base += " (cached)"
	}
	return base
}

func unreadText(unread int, hasMore bool) string {
	count := strconv.Itoa(unread)
	if hasMore {
		count += "+"
	}
	if unread == 1 && !hasMore {
		return "1 unread notification"
	}
	return count + " unread notifications"
}

func countText(mode string, hideWhenZero bool, status githubnotifications.Status, unread int, hasMore bool) string {
	if mode == "icon_only" {
		return ""
	}
	if status == githubnotifications.StatusLoading && unread == 0 {
		return "…"
	}
	if unread == 0 && hideWhenZero {
		return ""
	}
	text := strconv.Itoa(unread)
	if hasMore {
		text += "+"
	}
	return text
}
