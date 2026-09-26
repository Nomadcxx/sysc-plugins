package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	githubnotifications "github.com/Nomadcxx/sysc-plugins/plugins/github-notifications"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type protocolGH struct {
	markReadCalls atomic.Int32
	markAllCalls  atomic.Int32
	markRead      chan struct{}
	markAll       chan struct{}
	pageTwo       chan struct{}
}

func (g *protocolGH) Notifications(context.Context, int, int) ([]githubnotifications.RawItem, error) {
	return []githubnotifications.RawItem{notification42()}, nil
}

func (g *protocolGH) Search(_ context.Context, kind githubnotifications.WorkKind, page, _ int) (githubnotifications.RawWorkPage, error) {
	if page == 2 {
		select {
		case g.pageTwo <- struct{}{}:
		default:
		}
	}
	items := make([]githubnotifications.RawWorkItem, 0, 100)
	start := 1
	end := 100
	if page == 2 {
		start, end = 101, 101
	}
	for number := start; number <= end; number++ {
		item := githubnotifications.RawWorkItem{
			Title:         fmt.Sprintf("Work item %d", number),
			Number:        number,
			RepositoryURL: "https://api.github.com/repos/acme/api",
			UpdatedAt:     "2026-09-27T10:00:00Z",
		}
		if kind != githubnotifications.WorkIssues {
			item.PullRequest = &struct{}{}
			item.HTMLURL = fmt.Sprintf("https://github.com/acme/api/pull/%d", number)
		} else {
			item.HTMLURL = fmt.Sprintf("https://github.com/acme/api/issues/%d", number)
		}
		items = append(items, item)
	}
	return githubnotifications.RawWorkPage{TotalCount: 101, Items: items}, nil
}

func (g *protocolGH) Activity(context.Context, time.Time, time.Time) (githubnotifications.RawActivity, error) {
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	week := githubnotifications.RawActivityWeek{}
	for day := 0; day < 7; day++ {
		count, level := 0, "NONE"
		if day == 1 {
			count, level = 2, "FIRST_QUARTILE"
		}
		week.Days = append(week.Days, githubnotifications.RawContributionDay{
			Date: start.AddDate(0, 0, day).Format("2006-01-02"), Weekday: day,
			ContributionCount: count, ContributionLevel: level,
		})
	}
	return githubnotifications.RawActivity{TotalContributions: 2, Weeks: []githubnotifications.RawActivityWeek{week}}, nil
}

func (g *protocolGH) MarkRead(context.Context, string) error {
	g.markReadCalls.Add(1)
	select {
	case g.markRead <- struct{}{}:
	default:
	}
	return nil
}

func (g *protocolGH) MarkAll(context.Context) error {
	g.markAllCalls.Add(1)
	select {
	case g.markAll <- struct{}{}:
	default:
	}
	return nil
}

type protocolHarness struct {
	t       *testing.T
	host    io.WriteCloser
	lines   chan []byte
	done    chan error
	writeMu sync.Mutex
	snapMu  sync.Mutex
	pending map[string][]v1.ViewSnapshot
}

func startProtocol(t *testing.T, gh *protocolGH, opener func(context.Context, string) error) *protocolHarness {
	t.Helper()
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &protocolHarness{t: t, host: host, lines: make(chan []byte, 256), done: make(chan error, 1), pending: map[string][]v1.ViewSnapshot{}}
	go func() {
		scanner := bufio.NewScanner(plugin)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			h.lines <- line
			if typeOf(line) == v1.TypeHostCall {
				var call v1.HostCall
				if json.Unmarshal(line, &call) == nil {
					var result json.RawMessage = json.RawMessage(`{}`)
					if call.Call == v1.CallStateGet {
						result, _ = json.Marshal(v1.StateGetResult{Found: false})
					}
					h.send(v1.HostReply{Type: v1.TypeHostReply, ID: call.ID, OK: true, Result: result})
				}
			}
		}
		close(h.lines)
	}()
	go func() {
		err := runPlugin(input, output, gh, opener)
		_ = output.Close()
		h.done <- err
	}()
	h.send(v1.HostHello{Type: v1.TypeHostHello, Supported: []v1.Version{{Major: 1, Minor: 9}}, Capabilities: []string{"notifications", "panels", "settings", "state"}})
	if got := h.nextType(v1.TypePluginHello); got == nil {
		t.Fatal("plugin handshake did not complete")
	}
	t.Cleanup(h.stop)
	return h
}

func (h *protocolHarness) send(message any) {
	h.t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		h.t.Fatal(err)
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.host.Write(append(data, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *protocolHarness) nextType(want string) []byte {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-h.lines:
			if typeOf(line) == want {
				return line
			}
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s", want)
			return nil
		}
	}
}

func (h *protocolHarness) snapshot(viewID string, after uint64, accept func(*v1.Node) bool) v1.ViewSnapshot {
	h.t.Helper()
	h.snapMu.Lock()
	queued := h.pending[viewID]
	for i, snapshot := range queued {
		if snapshot.Revision > after && accept(snapshot.Root) {
			h.pending[viewID] = append(queued[:i], queued[i+1:]...)
			h.snapMu.Unlock()
			return snapshot
		}
	}
	h.snapMu.Unlock()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-h.lines:
			if typeOf(line) != v1.TypeViewSnapshot {
				continue
			}
			var snapshot v1.ViewSnapshot
			if json.Unmarshal(line, &snapshot) != nil {
				continue
			}
			if snapshot.ViewID == viewID && snapshot.Revision > after && accept(snapshot.Root) {
				return snapshot
			}
			if snapshot.ViewID != viewID {
				h.snapMu.Lock()
				h.pending[snapshot.ViewID] = append(h.pending[snapshot.ViewID], snapshot)
				h.snapMu.Unlock()
			}
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s snapshot after revision %d", viewID, after)
			return v1.ViewSnapshot{}
		}
	}
}

func (h *protocolHarness) openPanel(id string) v1.ViewSnapshot {
	h.t.Helper()
	h.send(v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: id, View: v1.ViewPanel, Entry: "panel", Width: 420, Height: 640})
	return h.snapshot(id, 0, func(*v1.Node) bool { return true })
}

func (h *protocolHarness) stop() {
	h.t.Helper()
	h.send(v1.HostShutdown{Type: v1.TypeHostShutdown})
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		h.t.Fatal("plugin did not stop")
	}
	_ = h.host.Close()
}

func TestRoutesViewsSearchPagingAndOpenRead(t *testing.T) {
	gh := &protocolGH{markRead: make(chan struct{}, 2), markAll: make(chan struct{}, 2), pageTwo: make(chan struct{}, 8)}
	var openCalls atomic.Int32
	opened := make(chan string, 8)
	open := func(_ context.Context, raw string) error {
		opened <- raw
		if openCalls.Add(1) == 1 {
			return errors.New("desktop opener unavailable")
		}
		return nil
	}
	h := startProtocol(t, gh, open)
	p1 := h.openPanel("panel-1")
	if findNode(p1.Root, "open:42") == nil {
		p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") != nil })
	}
	p2 := h.openPanel("panel-2")
	if findNode(p2.Root, "open:42") == nil {
		p2 = h.snapshot("panel-2", p2.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") != nil })
	}
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") != nil })
	if name := findNode(p1.Root, "mark-all").Name; name != "Mark all GitHub account notifications read" {
		t.Fatalf("mark-all scope = %q", name)
	}

	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "open:42", Event: v1.EventActivate})
	if got := receiveString(t, opened); got != "https://github.com/acme/api/pull/42" {
		t.Fatalf("opened URL = %q", got)
	}
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") != nil })
	if got := gh.markReadCalls.Load(); got != 0 {
		t.Fatalf("failed opener marked notification read %d times", got)
	}

	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "mark-all", Event: v1.EventActivate})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") == nil })
	select {
	case <-gh.markAll:
	case <-time.After(3 * time.Second):
		t.Fatal("mark-all was not routed")
	}
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") == nil })

	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "refresh", Event: v1.EventActivate})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") != nil })
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "mode:work", Event: v1.EventActivate})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "work-feed") != nil })
	p2 = h.snapshot("panel-2", p2.Revision, func(root *v1.Node) bool { return findNode(root, "inbox-feed") != nil })
	if findNode(p2.Root, "work-feed") != nil {
		t.Fatal("changing one view changed the other view's mode")
	}
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "work:issues", Event: v1.EventActivate})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool {
		return findNode(root, githubnotifications.WorkOpenID(normalizedWorkIssueForTest(1))) != nil
	})
	if !containsText(p1.Root, "Issue") {
		t.Fatal("assigned issue category did not render issue rows")
	}
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "work:reviews", Event: v1.EventActivate})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "load-more-work") != nil })

	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "search", Event: v1.EventChange, Text: "no such work"})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "work-empty") != nil })
	if findNode(p1.Root, githubnotifications.WorkOpenID(normalizedWorkPRForTest(1))) != nil {
		t.Fatal("local work search did not filter rows")
	}
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "search", Event: v1.EventChange, Text: ""})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "load-more-work") != nil })
	if findNode(p1.Root, "work:reviews") == nil || !findNode(p1.Root, "work:reviews").Selected && findNode(p1.Root, "work:reviews").Fill != "container" {
		t.Fatal("reviews category control missing")
	}
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "load-more-work", Event: v1.EventActivate})
	select {
	case <-gh.pageTwo:
	case <-time.After(3 * time.Second):
		t.Fatal("work load-more did not request the next search page")
	}
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool {
		return findNode(root, githubnotifications.WorkOpenID(normalizedWorkPRForTest(101))) != nil
	})

	workOpen := findIDWithPrefix(p1.Root, "work-open:")
	if workOpen == "" {
		t.Fatal("work row open control missing")
	}
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: workOpen, Event: v1.EventActivate})
	if got := receiveString(t, opened); got != "https://github.com/acme/api/pull/1" {
		t.Fatalf("work opened URL = %q", got)
	}
	if got := gh.markReadCalls.Load(); got != 0 {
		t.Fatalf("opening work marked a notification read: %d", got)
	}
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "mode:activity", Event: v1.EventActivate})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "activity-heatmap") != nil })
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "mode:inbox", Event: v1.EventActivate})
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") != nil })
	h.send(v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel-1", Revision: p1.Revision, Node: "open:42", Event: v1.EventActivate})
	if got := receiveString(t, opened); got != "https://github.com/acme/api/pull/42" {
		t.Fatalf("successfully opened notification URL = %q", got)
	}
	p1 = h.snapshot("panel-1", p1.Revision, func(root *v1.Node) bool { return findNode(root, "open:42") == nil })
	select {
	case <-gh.markRead:
	case <-time.After(3 * time.Second):
		t.Fatal("successful notification open did not mark its thread read")
	}
	if got := gh.markReadCalls.Load(); got != 1 {
		t.Fatalf("mark-read calls = %d, want 1", got)
	}
}

func notification42() githubnotifications.RawItem {
	var item githubnotifications.RawItem
	item.ID = "42"
	item.Unread = true
	item.Reason = "review_requested"
	item.UpdatedAt = "2026-09-27T10:00:00Z"
	item.Repository.FullName = "acme/api"
	item.Repository.HTMLURL = "https://github.com/acme/api"
	item.Subject.Title = "Repair retries"
	item.Subject.Type = "PullRequest"
	item.Subject.URL = "https://api.github.com/repos/acme/api/pulls/42"
	return item
}

func normalizedWorkPRForTest(number int) githubnotifications.WorkItem {
	item, _ := githubnotifications.NormalizeWorkItem(githubnotifications.RawWorkItem{
		Title: "Work item 1", Number: number, RepositoryURL: "https://api.github.com/repos/acme/api",
		HTMLURL: fmt.Sprintf("https://github.com/acme/api/pull/%d", number), UpdatedAt: "2026-09-27T10:00:00Z", PullRequest: &struct{}{},
	}, githubnotifications.WorkReviews, time.Now())
	return item
}

func normalizedWorkIssueForTest(number int) githubnotifications.WorkItem {
	item, _ := githubnotifications.NormalizeWorkItem(githubnotifications.RawWorkItem{
		Title: "Work item 1", Number: number, RepositoryURL: "https://api.github.com/repos/acme/api",
		HTMLURL: fmt.Sprintf("https://github.com/acme/api/issues/%d", number), UpdatedAt: "2026-09-27T10:00:00Z",
	}, githubnotifications.WorkIssues, time.Now())
	return item
}

func receiveString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for opener")
		return ""
	}
}

func findNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

func containsText(root *v1.Node, text string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, text) {
		return true
	}
	for _, child := range root.Children {
		if containsText(child, text) {
			return true
		}
	}
	return false
}

func findIDWithPrefix(root *v1.Node, prefix string) string {
	if root == nil {
		return ""
	}
	if strings.HasPrefix(root.ID, prefix) {
		return root.ID
	}
	for _, child := range root.Children {
		if found := findIDWithPrefix(child, prefix); found != "" {
			return found
		}
	}
	return ""
}

func typeOf(line []byte) string {
	var header struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &header)
	return header.Type
}
