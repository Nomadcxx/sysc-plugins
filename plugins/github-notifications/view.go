package githubnotifications

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	ModeInbox    = "inbox"
	ModeWork     = "work"
	ModeActivity = "activity"
)

type PanelState struct {
	ViewID     string
	Mode       string
	WorkKind   WorkKind
	Search     string
	StatusLine string
	Inbox      InboxSnapshot
	Work       WorkSnapshot
	WorkFeeds  map[WorkKind]WorkSnapshot
	Activity   ActivitySnapshot
}

// BarTree renders the unread-only bar count.
func BarTree(countText string, hasUnread bool) *v1.Node {
	icon := "notifications"
	if countText == "" && !hasUnread {
		icon = "notifications-off"
	}
	row := &v1.Node{Kind: v1.KindRow, Gap: 6}
	if countText != "" {
		row.Children = append(row.Children,
			&v1.Node{Kind: v1.KindIcon, Icon: icon},
			&v1.Node{Kind: v1.KindText, Text: countText, Tabular: true},
		)
	} else {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindIcon, Icon: icon})
	}
	return row
}

func TooltipTree(statusLine string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindText, Text: statusLine},
	}}
}

// PanelTree retains the original call shape for small embedders and tests.
func PanelTree(statusLine, errMsg string, items []Item) *v1.Node {
	state := PanelState{Mode: ModeInbox, StatusLine: statusLine,
		Inbox: InboxSnapshot{Items: cloneItems(items), Status: StatusReady}}
	if errMsg != "" {
		state.Inbox.Status = classify(fmt.Errorf("%s", errMsg))
		state.Inbox.Error = errMsg
	}
	return PanelTreeForState(state)
}

func PanelTreeForState(state PanelState) *v1.Node {
	if state.Mode == "" {
		state.Mode = ModeInbox
	}
	if state.WorkKind == "" {
		state.WorkKind = WorkReviews
	}
	children := []*v1.Node{
		{Kind: v1.KindRow, Height: 42, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "GitHub", Size: "title", Bold: true},
			{Kind: v1.KindText, Text: state.StatusLine, Tone: v1.ToneSubtle, PinEnd: true},
			button("refresh", "Refresh", "Refresh GitHub feeds", ""),
		}},
		modeRow(state.Mode, state.Inbox),
	}

	switch state.Mode {
	case ModeWork:
		children = append(children,
			input("search", "Search work items…", state.Search, state.ViewID),
			workCategoryRow(state.WorkKind, state.WorkFeeds),
			workBody(state.Work, state.Search),
		)
	case ModeActivity:
		children = append(children, activityBody(state.Activity))
	default:
		children = append(children,
			&v1.Node{Kind: v1.KindRow, Height: 42, Gap: 8, Children: []*v1.Node{
				input("search", "Search notifications…", state.Search, state.ViewID),
				button("mark-all", "Mark all read", "Mark all GitHub account notifications read", ""),
			}},
			inboxBody(state.Inbox, state.Search),
		)
	}
	return &v1.Node{Kind: v1.KindList, ID: "github-panel", Key: "github-panel", Height: 640, Padding: 14, Gap: 8, Children: children}
}

func modeRow(mode string, inbox InboxSnapshot) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Height: 38, Gap: 6, Children: []*v1.Node{
		tab("mode:inbox", "Inbox · "+inbox.CountLabel(), mode == ModeInbox, "Unread notification threads"),
		tab("mode:work", "Work", mode == ModeWork, "Review requests, your pull requests, and assigned issues"),
		tab("mode:activity", "Activity", mode == ModeActivity, "Your contribution activity for the trailing year"),
	}}
}

func workCategoryRow(kind WorkKind, feeds map[WorkKind]WorkSnapshot) *v1.Node {
	labels := []struct {
		kind  WorkKind
		label string
	}{{WorkReviews, "Reviews"}, {WorkMyPRs, "My PRs"}, {WorkIssues, "Issues"}}
	children := make([]*v1.Node, 0, len(labels))
	for _, category := range labels {
		count := ""
		if snapshot, ok := feeds[category.kind]; ok && snapshot.Status != StatusLoading {
			count = " · " + snapshot.CountLabel()
		}
		children = append(children, tab("work:"+string(category.kind), category.label+count, kind == category.kind, category.label))
	}
	return &v1.Node{Kind: v1.KindRow, Height: 36, Gap: 6, Children: children}
}

func inboxBody(snapshot InboxSnapshot, query string) *v1.Node {
	children := feedMessages(snapshot.Status, snapshot.Error, snapshot.Stale, snapshot.UpdatedAt, snapshot.Loading)
	items := filterNotifications(snapshot.Items, query)
	switch {
	case len(items) == 0 && query != "":
		children = append(children, message("inbox-empty", "No notifications match this search.", false))
	case len(items) == 0 && snapshot.Status == StatusLoading:
		children = append(children, message("inbox-empty", "Loading unread notifications…", false))
	case len(items) == 0 && snapshot.Status != StatusError:
		children = append(children, message("inbox-empty", "No unread notifications", false))
	case len(items) > 0:
		for _, item := range items {
			children = append(children, notificationRow(item))
		}
	}
	if snapshot.NeedsRefresh {
		children = append(children, &v1.Node{Kind: v1.KindRow, Height: 38, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Refresh the inbox before loading older threads", Tone: v1.ToneSubtle},
			button("load-more-inbox", "Refresh + load older", "Refresh the inbox and load the next page", ""),
		}})
	} else if snapshot.HasMore {
		children = append(children, &v1.Node{Kind: v1.KindRow, Height: 38, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Showing " + snapshot.CountLabel() + " unread threads", Tone: v1.ToneSubtle},
			button("load-more-inbox", "Load older", "Load the next inbox page", ""),
		}})
	}
	return &v1.Node{Kind: v1.KindColumn, ID: "inbox-feed", Gap: 6, Children: children}
}

func workBody(snapshot WorkSnapshot, query string) *v1.Node {
	children := feedMessages(snapshot.Status, snapshot.Error, snapshot.Stale, snapshot.UpdatedAt, snapshot.Loading)
	items := filterWork(snapshot.Items, query)
	switch {
	case len(items) == 0 && query != "":
		children = append(children, message("work-empty", "No work items match this search.", false))
	case len(items) == 0 && snapshot.Status == StatusLoading:
		children = append(children, message("work-empty", "Loading GitHub work…", false))
	case len(items) == 0 && snapshot.Status != StatusError:
		children = append(children, message("work-empty", "No open work items in this category.", false))
	case len(items) > 0:
		for _, item := range items {
			children = append(children, workRow(item))
		}
	}
	if snapshot.HasMore {
		children = append(children, &v1.Node{Kind: v1.KindRow, Height: 38, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: fmt.Sprintf("Showing %d of %s", len(snapshot.Items), snapshot.CountLabel()), Tone: v1.ToneSubtle},
			button("load-more-work", "Load more", "Load the next page of work items", ""),
		}})
	}
	if snapshot.LimitReached {
		children = append(children, message("work-search-limit", "GitHub search returned 1,000 items. Narrow your search to see more.", false))
	}
	return &v1.Node{Kind: v1.KindColumn, ID: "work-feed", Gap: 6, Children: children}
}

func activityBody(snapshot ActivitySnapshot) *v1.Node {
	children := feedMessages(snapshot.Status, snapshot.Error, snapshot.Stale, snapshot.UpdatedAt, snapshot.Loading)
	if len(snapshot.Weeks) == 0 {
		if snapshot.Status == StatusLoading {
			children = append(children, message("activity-empty", "Loading contribution activity…", false))
		} else if snapshot.Status != StatusError {
			children = append(children, message("activity-empty", "Contribution activity is not available yet.", false))
		}
		return &v1.Node{Kind: v1.KindColumn, ID: "activity-feed", Gap: 8, Children: children}
	}
	children = append(children,
		&v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("%d contributions · %d active days", snapshot.TotalContributions, snapshot.ActiveDays), Size: "title"},
		activityMonthLabels(snapshot.Weeks),
		activityGrid(snapshot.Weeks),
		&v1.Node{Kind: v1.KindText, Text: "Daily contribution activity · trailing year", Tone: v1.ToneSubtle, Size: "caption"},
	)
	return &v1.Node{Kind: v1.KindColumn, ID: "activity-feed", Gap: 8, Children: children}
}

func activityMonthLabels(weeks []ActivityWeek) *v1.Node {
	months := make([]string, 0, 13)
	last := ""
	for _, week := range weeks {
		for _, day := range week.Days {
			if day.Date == "" {
				continue
			}
			month := day.Date[:7]
			if month != last {
				parsed, _ := time.Parse("2006-01", month)
				months = append(months, parsed.Format("Jan"))
				last = month
			}
			break
		}
	}
	children := make([]*v1.Node, len(months))
	for i, month := range months {
		children[i] = &v1.Node{Kind: v1.KindText, Text: month, Size: "caption", Width: 24}
	}
	return &v1.Node{Kind: v1.KindRow, ID: "activity-months", Gap: 2, Children: children}
}

func activityGrid(weeks []ActivityWeek) *v1.Node {
	columns := make([]*v1.Node, len(weeks))
	for wi, week := range weeks {
		days := make([]*v1.Node, 7)
		for weekday, day := range week.Days {
			fill := contributionFill(day.Level)
			cell := &v1.Node{Kind: v1.KindColumn, Key: fmt.Sprintf("activity:%d:%d", wi, weekday), Width: 5, Height: 7, Fill: fill, Radius: 1}
			if day.Date != "" {
				cell.Tooltip = fmt.Sprintf("%s · %s", day.Date, plural(day.Count, "contribution"))
			}
			days[weekday] = cell
		}
		columns[wi] = &v1.Node{Kind: v1.KindColumn, Width: 5, Gap: 2, Children: days}
	}
	return &v1.Node{Kind: v1.KindRow, ID: "activity-heatmap", Gap: 2, Children: columns}
}

func contributionFill(level ContributionLevel) string {
	switch level {
	case ContributionFirstQuartile:
		return "container"
	case ContributionSecondQuartile:
		return "card"
	case ContributionThirdQuartile:
		return "chip"
	case ContributionFourthQuartile:
		return "accent"
	default:
		return "surface"
	}
}

func notificationRow(item Item) *v1.Node {
	marker := item.Type
	switch item.Type {
	case "PullRequest":
		marker = "PR"
	case "Issue":
		marker = "Issue"
	case "CheckSuite":
		marker = "CI"
	}
	return &v1.Node{Kind: v1.KindColumn, Key: "notification:" + item.ID, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: 40, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindText, Text: marker, Width: 40, Size: "caption", Tone: v1.ToneAccent},
			{Kind: v1.KindButton, ID: "open:" + item.ID, Text: bounded(item.Title, 80), Name: bounded("Open "+item.Title+" in GitHub", v1.MaxIdentBytes), Role: "button", Events: []v1.EventKind{v1.EventActivate}, Width: 180, MaxWidth: 168},
			{Kind: v1.KindText, Text: item.RelativeTime, Width: 32, Tabular: true, Tone: v1.ToneSubtle},
			{Kind: v1.KindButton, ID: "read:" + item.ID, Text: "Read", Name: bounded("Mark "+item.Title+" as read", v1.MaxIdentBytes), Role: "button", Events: []v1.EventKind{v1.EventActivate}, Width: 48},
		}},
		{Kind: v1.KindText, Text: item.Repo + " · " + item.ReasonLabel, Tone: v1.ToneSubtle, Size: "caption"},
	}}
}

func workRow(item WorkItem) *v1.Node {
	marker := "PR"
	if item.Kind == WorkIssues {
		marker = "Issue"
	}
	return &v1.Node{Kind: v1.KindColumn, Key: "work:" + workToken(item.URL), Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: 40, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindText, Text: marker, Width: 40, Size: "caption", Tone: v1.ToneAccent},
			{Kind: v1.KindButton, ID: WorkOpenID(item), Text: bounded(item.Title, 110), Name: bounded("Open "+item.Title+" in GitHub", 120), Role: "button", Events: []v1.EventKind{v1.EventActivate}, Width: 252, MaxWidth: 238},
			{Kind: v1.KindText, Text: item.RelativeTime, Width: 42, Tabular: true, Tone: v1.ToneSubtle},
		}},
		{Kind: v1.KindText, Text: fmt.Sprintf("%s · #%d", item.Repo, item.Number), Tone: v1.ToneSubtle, Size: "caption"},
	}}
}

func WorkOpenID(item WorkItem) string { return "work-open:" + workToken(item.URL) }

func workToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func feedMessages(status Status, err string, stale bool, updated time.Time, loading bool) []*v1.Node {
	var children []*v1.Node
	if err != "" {
		children = append(children, message("feed-error", err, true))
	}
	if loading {
		children = append(children, message("feed-loading", "Refreshing…", false))
	}
	if stale {
		label := "Showing cached data"
		if !updated.IsZero() {
			label += " · last updated " + updated.Format("Jan 2 15:04")
		}
		children = append(children, message("feed-stale", label, false))
	}
	return children
}

func input(id, placeholder, value, viewID string) *v1.Node {
	return &v1.Node{Kind: v1.KindTextInput, ID: id, Key: "github-search:" + viewID, Text: value, Placeholder: placeholder,
		Name: placeholder, Role: "textbox", Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}, Height: 40, Width: 248}
}

func button(id, label, name, fill string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: bounded(name, v1.MaxIdentBytes), Role: "button",
		Events: []v1.EventKind{v1.EventActivate}, Fill: fill}
}

func tab(id, label string, selected bool, name string) *v1.Node {
	fill := ""
	if selected {
		fill = "container"
	}
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: bounded(name, v1.MaxIdentBytes), Role: "tab",
		Fill: fill, Events: []v1.EventKind{v1.EventActivate}}
}

func message(id, text string, isError bool) *v1.Node {
	tone := v1.ToneSubtle
	if isError {
		tone = v1.ToneError
	}
	return &v1.Node{Kind: v1.KindText, ID: id, Text: bounded(text, 250), Tone: tone, Size: "caption"}
}

func filterNotifications(items []Item, query string) []Item {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	out := make([]Item, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Title+" "+item.Repo+" "+item.ReasonLabel+" "+item.Type), query) {
			out = append(out, item)
		}
	}
	return out
}

func filterWork(items []WorkItem, query string) []WorkItem {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	out := make([]WorkItem, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Title+" "+item.Repo), query) {
			out = append(out, item)
		}
	}
	return out
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func bounded(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	if maxBytes < 4 {
		return ""
	}
	for len(value) > maxBytes-3 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value + "…"
}
