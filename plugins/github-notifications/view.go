package githubnotifications

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
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

// BarTree is a quiet, icon-only launcher for the GitHub panel. Unread
// notifications swap in the catalogue's dotted mark instead of a count.
func BarTree(hasUnread bool) *v1.Node {
	icon := "github"
	if hasUnread {
		icon = "github-unread"
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{{
		Kind: v1.KindButton, ID: "open", Name: "Open GitHub Notifications", Role: "button",
		Icon: icon, Tooltip: "GitHub Notifications", Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
	}}}
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
	refresh := button("refresh", "Refresh", "Refresh GitHub feeds", "")
	refresh.Icon = "refresh"
	children := []*v1.Node{
		{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
			{Kind: v1.KindRow, Height: 36, Gap: 8, PinEnd: true, Children: []*v1.Node{
				{Kind: v1.KindText, Text: "GitHub", Size: "title", Bold: true},
				refresh,
			}},
			{Kind: v1.KindText, Text: state.StatusLine, Tone: v1.ToneSubtle, Size: "caption"},
		}},
		modeRow(state.Mode, state.Inbox),
	}

	switch state.Mode {
	case ModeWork:
		children = append(children,
			workSearch(state.Search, state.ViewID),
			workCategoryRow(state.WorkKind, state.WorkFeeds),
			feedList(workListHeight, workBody(state.Work, state.Search)),
		)
	case ModeActivity:
		children = append(children, activityBody(state.Activity))
	default:
		children = append(children,
			&v1.Node{Kind: v1.KindRow, Height: 40, Gap: 8, PinEnd: true, Children: []*v1.Node{
				input("search", "Search notifications…", state.Search, state.ViewID),
				button("mark-all", "Mark all read", "Mark all GitHub account notifications read", ""),
			}},
			feedList(inboxListHeight, inboxBody(state.Inbox, state.Search)),
		)
	}
	return &v1.Node{Kind: v1.KindColumn, ID: "github-panel", Key: "github-panel", Padding: 14, Gap: 8, Children: children}
}

// The feed lists take what the 640px panel leaves under the fixed header,
// tabs, and search controls, so the header never scrolls away and the list's
// scrollbar never paints over it.
const (
	inboxListHeight = 430
	workListHeight  = 380
)

func feedList(height int, body *v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindList, ID: body.ID, Key: body.ID, Height: height, Gap: 6, Children: body.Children}
}

func modeRow(mode string, inbox InboxSnapshot) *v1.Node {
	return &v1.Node{Kind: v1.KindSegmented, Height: 40, Gap: 2, Children: []*v1.Node{
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
	return &v1.Node{Kind: v1.KindSegmented, Height: 32, Gap: 2, Children: children}
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
		children = append(children, emptyState("inbox-empty", "You're all caught up"))
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
		&v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			statTile(humanize(snapshot.TotalContributions), "contributions"),
			statTile(humanize(snapshot.ActiveDays), "active days"),
		}},
		&v1.Node{Kind: v1.KindColumn, Fill: "outline", Radius: 10, Padding: 10, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Trailing year", Size: "label", Bold: true},
			activityMonthLabels(snapshot.Weeks),
			activityGrid(snapshot.Weeks),
			activityLegend(),
		}},
	)
	return &v1.Node{Kind: v1.KindColumn, ID: "activity-feed", Gap: 10, Children: children}
}

// activityMonthLabels sets each month's name over the week column where it
// starts, so the labels line up with the grid instead of packing left.
func activityMonthLabels(weeks []ActivityWeek) *v1.Node {
	type start struct {
		week  int
		label string
	}
	var starts []start
	last := ""
	for wi, week := range weeks {
		for _, day := range week.Days {
			if day.Date == "" {
				continue
			}
			if month := day.Date[:7]; month != last {
				parsed, _ := time.Parse("2006-01", month)
				starts = append(starts, start{wi, parsed.Format("Jan")})
				last = month
			}
			break
		}
	}
	children := make([]*v1.Node, 0, len(starts))
	for i, st := range starts {
		end, trailing := len(weeks), activityGap // the grid has no gap after its last week
		if i+1 < len(starts) {
			end, trailing = starts[i+1].week, 0
		}
		text := st.label
		if end-st.week < 4 {
			text = "" // a sliver of a month has no room for its name
		}
		// Text measures its own width, so a sized column holds each slot.
		children = append(children, &v1.Node{Kind: v1.KindColumn, Width: (end-st.week)*activityPitch - trailing,
			Children: []*v1.Node{{Kind: v1.KindText, Text: text, Size: "caption", Tone: v1.ToneSubtle}}})
	}
	return &v1.Node{Kind: v1.KindRow, ID: "activity-months", Children: children}
}

// One week column is a cell plus its gap; 53 of them fill the card.
const (
	activityCell  = 6
	activityGap   = 1
	activityPitch = activityCell + activityGap
)

func activityLegend() *v1.Node {
	row := &v1.Node{Kind: v1.KindRow, Gap: 3, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Less", Size: "caption", Tone: v1.ToneSubtle},
	}}
	for _, level := range []ContributionLevel{ContributionNone, ContributionFirstQuartile, ContributionSecondQuartile, ContributionThirdQuartile, ContributionFourthQuartile} {
		row.Children = append(row.Children, &v1.Node{Kind: v1.KindColumn, Width: 8, Height: 8, Fill: contributionFill(level), Radius: 2})
	}
	row.Children = append(row.Children, &v1.Node{Kind: v1.KindText, Text: "More", Size: "caption", Tone: v1.ToneSubtle})
	return row
}

func statTile(value, label string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "outline", Radius: 10, Padding: 10, Gap: 2, Width: 186, Children: []*v1.Node{
		{Kind: v1.KindText, Text: value, Size: "headline", Bold: true, Tabular: true},
		{Kind: v1.KindText, Text: label, Size: "caption", Tone: v1.ToneSubtle},
	}}
}

// humanize groups the thousands of a non-negative count: 1184 reads as 1,184.
func humanize(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func activityGrid(weeks []ActivityWeek) *v1.Node {
	columns := make([]*v1.Node, len(weeks))
	for wi, week := range weeks {
		days := make([]*v1.Node, 7)
		for weekday, day := range week.Days {
			fill := contributionFill(day.Level)
			cell := &v1.Node{Kind: v1.KindColumn, Key: fmt.Sprintf("activity:%d:%d", wi, weekday), Width: activityCell, Height: activityCell, Fill: fill, Radius: 1}
			if day.Date != "" {
				cell.Tooltip = fmt.Sprintf("%s · %s", day.Date, plural(day.Count, "contribution"))
			}
			days[weekday] = cell
		}
		columns[wi] = &v1.Node{Kind: v1.KindColumn, Width: activityCell, Gap: activityGap, Children: days}
	}
	return &v1.Node{Kind: v1.KindRow, ID: "activity-heatmap", Gap: activityGap, Children: columns}
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
	label, tone := item.Type, v1.ToneSubtle
	switch item.Type {
	case "PullRequest":
		label = "PR"
	case "CheckSuite":
		label = "CI"
	}
	if item.IsFailure {
		tone = v1.ToneError
	}
	return &v1.Node{Kind: v1.KindRow, Key: "notification:" + item.ID, Gap: rowGap, PinEnd: true, Children: []*v1.Node{
		feedCard("open:"+item.ID, "Open "+item.Title+" in GitHub", rowWidth-rowGap-readSize, item.Title, item.RelativeTime,
			label, tone, item.Repo+" · "+item.ReasonLabel),
		{Kind: v1.KindButton, ID: "read:" + item.ID, Icon: "check", Name: bounded("Mark "+item.Title+" as read", v1.MaxIdentBytes),
			Tooltip: "Mark read", Role: "button", Fill: "soft", Shape: "circle", Width: readSize, Height: readSize,
			Events: []v1.EventKind{v1.EventActivate}},
	}}
}

// Feed rows stop short of the list's right edge so its scrollbar never
// paints over a row's trailing action.
const (
	rowWidth = 380
	rowGap   = 8
	readSize = 32
)

// feedCard is one outlined, activatable feed row: the title over a line
// naming the item's type and where it came from.
func feedCard(id, name string, width int, title, when, label string, tone v1.Tone, meta string) *v1.Node {
	inner := width - 20
	return &v1.Node{Kind: v1.KindButton, ID: id, Name: bounded(name, v1.MaxIdentBytes), Role: "button",
		Fill: "outline", Radius: 10, Padding: 10, Width: width, Height: 54,
		Events: []v1.EventKind{v1.EventActivate}, Children: []*v1.Node{{
			Kind: v1.KindColumn, Gap: 3, Children: []*v1.Node{
				// Title and age form a label/value pair, so the host pins the age right.
				{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
					{Kind: v1.KindText, Text: bounded(title, 110), MaxWidth: inner - 44},
					{Kind: v1.KindText, Text: when, Size: "caption", Tone: v1.ToneSubtle, Tabular: true},
				}},
				// One text node: a full page of rows must stay under v1.MaxNodes.
				{Kind: v1.KindText, Text: bounded(label+" · "+meta, 160), Size: "caption", Tone: tone, MaxWidth: inner},
			},
		}},
	}
}

func workRow(item WorkItem) *v1.Node {
	label := "PR"
	if item.Kind == WorkIssues {
		label = "Issue"
	}
	return feedCard(WorkOpenID(item), "Open "+item.Title+" in GitHub", rowWidth, item.Title, item.RelativeTime,
		label, v1.ToneSubtle, fmt.Sprintf("%s · #%d", item.Repo, item.Number))
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

func workSearch(value, viewID string) *v1.Node {
	search := input("search", "Search work items…", value, viewID)
	search.Width = rowWidth
	return search
}

func input(id, placeholder, value, viewID string) *v1.Node {
	return &v1.Node{Kind: v1.KindTextInput, ID: id, Key: "github-search:" + viewID, Text: value, Placeholder: placeholder,
		Name: placeholder, Role: "textbox", Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}, Height: 40, Width: 248}
}

// button is the panel's action pill; an empty fill takes the soft default.
func button(id, label, name, fill string) *v1.Node {
	if fill == "" {
		fill = "soft"
	}
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: bounded(name, v1.MaxIdentBytes), Role: "button",
		Events: []v1.EventKind{v1.EventActivate}, Fill: fill, Shape: "stadium", Padding: 10, Height: 32}
}

func tab(id, label string, selected bool, name string) *v1.Node {
	return &v1.Node{Kind: v1.KindButton, ID: id, Text: label, Name: bounded(name, v1.MaxIdentBytes), Role: "tab",
		Selected: selected, Padding: 3, Events: []v1.EventKind{v1.EventActivate}}
}

// emptyState is a quiet, centred GitHub mark over one line of text.
func emptyState(id, text string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 10, Padding: 48, Width: rowWidth, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: "github", IconSize: 40, Tone: v1.ToneSubtle, CenterX: true},
		{Kind: v1.KindText, ID: id, Text: text, Tone: v1.ToneSubtle, CenterX: true},
	}}
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
