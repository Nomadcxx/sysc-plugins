package githubnotifications

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestTreesValidate(t *testing.T) {
	items := NormalizeList([]RawItem{rawItem("1", "Fix the thing", "PullRequest", "review_requested"), rawItem("2", "CI failed", "CheckSuite", "ci_activity")})

	bar := BarTree(true)
	if err := v1.Validate(bar, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree(false), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(TooltipTree("3 unread · updated 10:00"), v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree("3 unread notifications", "", items), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree("No unread notifications", "gh exploded", nil), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
}

func TestBarTreeIsIconOnlyAndOpensOnBothMouseButtons(t *testing.T) {
	if button := findNode(BarTree(false), "open"); button == nil || button.Icon != "github" {
		t.Fatalf("read bar button = %+v, want the plain GitHub mark", button)
	}
	bar := BarTree(true)
	if err := v1.Validate(bar, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	button := findNode(bar, "open")
	if button == nil {
		t.Fatal("bar has no addressed open button")
	}
	if button.Kind != v1.KindButton || button.Icon != "github-unread" || button.Text != "" || len(button.Children) != 0 {
		t.Fatalf("bar button = %+v, want only the GitHub mark with its unread dot", button)
	}
	if button.Name == "" || button.Role != "button" {
		t.Fatalf("bar button is not accessible: %+v", button)
	}
	want := []v1.EventKind{v1.EventActivate, v1.EventPointer}
	if !reflect.DeepEqual(button.Events, want) {
		t.Fatalf("bar events = %v, want %v", button.Events, want)
	}
	if containsText(bar, "50") {
		t.Fatal("bar displays an unread count")
	}
}

func TestPanelModesExposeInboxWorkAndActivity(t *testing.T) {
	inbox := InboxSnapshot{Status: StatusReady, Items: []Item{mustItem(t, rawItem("1", "Repair retry handling", "PullRequest", "review_requested"))}, HasMore: true}
	work := WorkSnapshot{Status: StatusReady, TotalCount: 21, Items: []WorkItem{normalizedWorkPR(9)}}
	feeds := map[WorkKind]WorkSnapshot{
		WorkReviews: work, WorkMyPRs: {Status: StatusReady, TotalCount: 3}, WorkIssues: {Status: StatusReady, TotalCount: 1},
	}
	activity := testActivityWeeks(53)
	cases := []struct {
		mode string
		want []string
	}{
		{ModeInbox, []string{"mode:inbox", "search", "mark-all", "open:1", "read:1", "Load older"}},
		{ModeWork, []string{"mode:work", "work:reviews", "work:my_prs", "work:issues", WorkOpenID(work.Items[0])}},
		{ModeActivity, []string{"mode:activity", "activity-heatmap", "activity-months"}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			state := PanelState{Mode: tc.mode, WorkKind: WorkReviews, Inbox: inbox, Work: work, WorkFeeds: feeds, Activity: activity}
			root := PanelTreeForState(state)
			if err := v1.Validate(root, v1.ViewPanel); err != nil {
				t.Fatal(err)
			}
			for _, id := range tc.want {
				if node := findNode(root, id); node == nil && !containsText(root, id) {
					t.Errorf("panel lacks %q", id)
				}
			}
		})
	}
}

func TestPanelSearchAndActivityGrid(t *testing.T) {
	items := []Item{
		mustItem(t, rawItem("1", "Repair retry handling", "PullRequest", "review_requested")),
		mustItem(t, rawItem("2", "Update documentation", "Issue", "mention")),
	}
	inbox := PanelTreeForState(PanelState{Mode: ModeInbox, Search: "repair", Inbox: InboxSnapshot{Status: StatusReady, Items: items}})
	if !containsText(inbox, "Repair retry handling") || containsText(inbox, "Update documentation") {
		t.Fatal("inbox local search did not filter rows")
	}
	work := PanelTreeForState(PanelState{Mode: ModeWork, Search: "retry", WorkKind: WorkReviews,
		Work: WorkSnapshot{Status: StatusReady, Items: []WorkItem{normalizedWorkPR(1), {Kind: WorkReviews, Title: "Update docs", Repo: "acme/docs", Number: 2}}}})
	if !containsText(work, "PR") || containsText(work, "Update docs") {
		t.Fatal("work local search did not filter rows")
	}

	activity := testActivityWeeks(53)
	root := PanelTreeForState(PanelState{Mode: ModeActivity, Activity: activity})
	heatmap := findNode(root, "activity-heatmap")
	if heatmap == nil || len(heatmap.Children) != 53 {
		t.Fatalf("activity week columns = %d", len(heatmap.Children))
	}
	levels := map[string]bool{}
	tooltipDays := 0
	for _, week := range heatmap.Children {
		if len(week.Children) != 7 {
			t.Fatalf("week has %d day cells", len(week.Children))
		}
		for _, day := range week.Children {
			levels[day.Fill] = true
			if day.Tooltip != "" {
				tooltipDays++
			}
		}
	}
	for _, fill := range []string{"surface", "container", "card", "chip", "accent"} {
		if !levels[fill] {
			t.Errorf("activity grid has no %q level", fill)
		}
	}
	if tooltipDays != 53*7 {
		t.Fatalf("tooltip days = %d, want %d", tooltipDays, 53*7)
	}
}

func TestViewsFitAtManifestSizesWithFullPages(t *testing.T) {
	inboxItems := make([]Item, 100)
	workItems := make([]WorkItem, 100)
	for i := range inboxItems {
		id := fmt.Sprint(i + 1)
		inboxItems[i] = mustItem(t, rawItem(id, "Repair retries in worker", "PullRequest", "review_requested"))
		workItems[i] = normalizedWorkPR(i + 1)
	}
	activity := testActivityWeeks(53)
	checks := []struct {
		name   string
		root   *v1.Node
		kind   v1.ViewKind
		width  int
		height int
	}{
		{"bar", BarTree(false), v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight},
		{"bar-unread", BarTree(true), v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight},
		{"tooltip", TooltipTree("100 unread · updated 10:00"), v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight},
		{"inbox", PanelTreeForState(PanelState{Mode: ModeInbox, Inbox: InboxSnapshot{Status: StatusReady, Items: inboxItems, HasMore: true}}), v1.ViewPanel, 420, 640},
		{"work", PanelTreeForState(PanelState{Mode: ModeWork, WorkKind: WorkReviews, Work: WorkSnapshot{Status: StatusReady, Items: workItems, TotalCount: 200, HasMore: true}}), v1.ViewPanel, 420, 640},
		{"activity", PanelTreeForState(PanelState{Mode: ModeActivity, Activity: activity}), v1.ViewPanel, 420, 640},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if nodes := nodeCount(check.root); nodes > v1.MaxNodes {
				t.Fatalf("tree has %d nodes, maximum is %d", nodes, v1.MaxNodes)
			}
			if findings := shelllint.Tree(check.root, check.kind, check.width, check.height); len(findings) != 0 {
				t.Fatalf("layout findings: %+v", findings)
			}
		})
	}
}

func TestPanelHeaderFitsWithLiveStatus(t *testing.T) {
	root := PanelTreeForState(PanelState{
		Mode:       ModeInbox,
		StatusLine: "123 unread · updated 14:32 (cached)",
		Inbox:      InboxSnapshot{Status: StatusReady},
	})
	if refresh := findNode(root, "refresh"); refresh == nil || refresh.Text != "Refresh" {
		t.Fatal("panel header has no Refresh action")
	}
	if findings := shelllint.Tree(root, v1.ViewPanel, 420, 640); len(findings) != 0 {
		t.Fatalf("layout findings: %+v", findings)
	}
}

func testActivityWeeks(n int) ActivitySnapshot {
	levels := []ContributionLevel{ContributionNone, ContributionFirstQuartile, ContributionSecondQuartile, ContributionThirdQuartile, ContributionFourthQuartile}
	start := time.Date(2025, 9, 14, 0, 0, 0, 0, time.UTC)
	activity := ActivitySnapshot{Status: StatusReady, Activity: Activity{Weeks: make([]ActivityWeek, n)}}
	for wi := 0; wi < n; wi++ {
		for day := 0; day < 7; day++ {
			index := wi*7 + day
			count := 0
			if index%5 != 0 {
				count = index%7 + 1
				activity.TotalContributions += count
				activity.ActiveDays++
			}
			activity.Weeks[wi].Days[day] = ContributionDay{Date: start.AddDate(0, 0, index).Format("2006-01-02"), Count: count, Level: levels[index%len(levels)]}
		}
	}
	return activity
}

func normalizedWorkPR(number int) WorkItem {
	item, _ := NormalizeWorkItem(rawWorkPR(number), WorkReviews, time.Now())
	return item
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

func containsText(root *v1.Node, value string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, value) {
		return true
	}
	for _, child := range root.Children {
		if containsText(child, value) {
			return true
		}
	}
	return false
}

func nodeCount(root *v1.Node) int {
	if root == nil {
		return 0
	}
	n := 1
	for _, child := range root.Children {
		n += nodeCount(child)
	}
	return n
}

// Marking one thread or the whole inbox read leaves the page cursor stale.
// The footer must still fit, offer older pages only when some exist, and
// never survive mark-all into an empty inbox.
func TestInboxFooterAfterMarkingRead(t *testing.T) {
	items := []Item{mustItem(t, rawItem("1", "Repair retry handling", "PullRequest", "review_requested"))}
	cases := []struct {
		name     string
		inbox    InboxSnapshot
		wantMore bool
	}{
		{"after mark all", InboxSnapshot{Status: StatusReady, NeedsRefresh: true}, false},
		{"after one read, last page", InboxSnapshot{Status: StatusReady, Items: items, NeedsRefresh: true}, false},
		{"after one read, older pages", InboxSnapshot{Status: StatusReady, Items: items, NeedsRefresh: true, HasMore: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := PanelTreeForState(PanelState{Mode: ModeInbox, StatusLine: "123 unread · updated 14:32", Inbox: tc.inbox})
			more := findNode(root, "load-more-inbox")
			if (more != nil) != tc.wantMore {
				t.Fatalf("load-more present = %v, want %v", more != nil, tc.wantMore)
			}
			if more != nil && more.Text != "Load older" {
				t.Fatalf("load-more label = %q, want the short %q", more.Text, "Load older")
			}
			if findings := shelllint.Tree(root, v1.ViewPanel, 420, 640); len(findings) != 0 {
				t.Fatalf("layout findings: %+v", findings)
			}
		})
	}
}
