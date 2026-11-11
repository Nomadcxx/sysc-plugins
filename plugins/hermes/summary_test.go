package hermes

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/icons"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// summarySizes are the declared summary panel and the fitted-down size the
// scaffold's connecting tree is also checked at.
var summarySizes = [][2]int{{380, 480}, {320, 400}}

func richSummary(s Status) SummaryState {
	return SummaryState{
		BarState:   stateFor(s),
		Provider:   "openrouter",
		ModelName:  "example-model",
		CronActive: 2, CronTotal: 3,
		CronJobs: []CronJob{
			{Name: "Morning digest", Active: true, State: "scheduled", Schedule: "0 9 * * *", NextRun: "2026-11-12T09:00:00", LastStatus: "ok"},
			{Name: "Inbox sweep", Active: true, State: "scheduled", Schedule: "every 30m", LastStatus: "ok"},
			{Name: "", Active: false, State: "paused"},
			{Name: strings.Repeat("a very long cron job name ", 4), Active: true, NextRun: "2026-11-13T18:30:00", LastStatus: "error"},
			{Name: "Weekly report", Active: false, State: "paused", Schedule: "0 8 * * 1"},
		},
		MCPEnabled: 2, MCPTotal: 4,
		ToolEvents: 12, RunningTools: 1,
		GatewayPlatforms: map[string]string{"telegram": "connected", "discord": "disconnected"},
		ApprovalPending:  true,
		ApprovalMessage:  "Run go test ./... in the repository",
	}
}

func summaryStates(s Status) map[string]SummaryState {
	noisy := richSummary(s)
	noisy.ModelName = strings.Repeat("a-very-long-model-name-", 4)
	noisy.ApprovalPending = false
	noisy.BridgeError = strings.Repeat("the bridge said something long ", 4)
	return map[string]SummaryState{
		"empty":          {BarState: stateFor(s)},
		"rich":           richSummary(s),
		"long and error": noisy,
	}
}

// checkBudget fails when the root's children, gaps and padding do not fit
// the box. A column never refuses, so lint.Tree cannot catch this.
func checkBudget(t *testing.T, name string, root *v1.Node, width, height int) {
	t.Helper()
	if root.Width != width || root.Height != height {
		t.Errorf("%s: root is %dx%d, want the %dx%d view box", name, root.Width, root.Height, width, height)
	}
	used := 2 * root.Padding
	for i, c := range root.Children {
		if c.Kind == v1.KindSeparator {
			used++
		} else if c.Height <= 0 {
			t.Errorf("%s: child %d (%s) has no explicit height to budget", name, i, c.Kind)
		}
		used += c.Height
		if i > 0 {
			used += root.Gap
		}
	}
	if used > height {
		t.Errorf("%s: children need %d of %d pixels", name, used, height)
	}
}

func findID(root *v1.Node, id string) *v1.Node {
	var found *v1.Node
	walk(root, func(n *v1.Node) {
		if found == nil && n.ID == id {
			found = n
		}
	})
	return found
}

func controlIDs(root *v1.Node) []string {
	var ids []string
	walk(root, func(n *v1.Node) {
		if n.Kind == v1.KindButton {
			ids = append(ids, n.ID)
		}
	})
	return ids
}

func checkGlyphs(t *testing.T, name string, root *v1.Node) {
	t.Helper()
	walk(root, func(n *v1.Node) {
		if n.Icon == "" {
			return
		}
		if !icons.ValidName(n.Icon) {
			t.Errorf("%s uses %q, not in the pinned catalogue", name, n.Icon)
		}
		if strings.Contains(n.Icon, "warning") || strings.Contains(n.Icon, "history") || strings.Contains(n.Icon, "alert") {
			t.Errorf("%s uses forbidden glyph %q", name, n.Icon)
		}
	})
}

func manifestActions(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Services []struct {
			ID      string   `json:"id"`
			Actions []string `json:"actions"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, s := range m.Services {
		if s.ID == serviceID {
			for _, a := range s.Actions {
				out[a] = true
			}
		}
	}
	return out
}

func TestSummaryLaysOutInEveryStatus(t *testing.T) {
	avatar := repoAvatar(t)
	if len(Statuses) != 7 {
		t.Fatalf("Statuses has %d entries, want seven", len(Statuses))
	}
	for _, s := range Statuses {
		for name, state := range summaryStates(s) {
			for _, av := range []string{avatar, ""} {
				for _, size := range summarySizes {
					label := string(s) + " " + name
					if av == "" {
						label += " (no avatar)"
					}
					tree := SummaryTree(state, av, size[0], size[1])
					lintView(t, label, tree, v1.ViewPanel, size[0], size[1])
					checkBudget(t, label, tree, size[0], size[1])
					checkGlyphs(t, label, tree)
				}
			}
		}
	}
}

func TestSummaryRowsMatchNoctalia(t *testing.T) {
	cases := []struct {
		name  string
		state SummaryState
		want  []summaryRow
	}{
		{"empty", SummaryState{BarState: stateFor(StatusUnknown)}, []summaryRow{
			{"Model", "Not set"},
			{"Cron", "0 active / 0 total"},
			{"MCP", "No MCP servers"},
			{"Gateway", "stopped"},
			{"Activity", "No background actions"},
		}},
		{"rich", richSummary(StatusBusy), []summaryRow{
			{"Model", "openrouter / example-model"},
			{"Cron", "2 active / 3 total"},
			{"MCP", "2 enabled / 4 total"},
			{"Gateway", "discord: disconnected, telegram: connected"},
			{"Activity", "1 running"},
		}},
		{"model from hermes, no provider; idle tools", SummaryState{
			BarState: BarState{BridgeStatus: "online", HermesStatus: "idle", Model: "hermes-model"}, ToolEvents: 3,
		}, []summaryRow{
			{"Model", "hermes-model"},
			{"Cron", "0 active / 0 total"},
			{"MCP", "No MCP servers"},
			{"Gateway", "Unknown"},
			{"Activity", "3 background actions"},
		}},
		{"provider plus hermes model", SummaryState{BarState: stateFor(StatusIdle), Provider: "anthropic"}, []summaryRow{
			{"Model", "anthropic / example-model"},
			{"Cron", "0 active / 0 total"},
			{"MCP", "No MCP servers"},
			{"Gateway", "Unknown"},
			{"Activity", "No background actions"},
		}},
	}
	for _, c := range cases {
		got := summaryRows(c.state)
		if len(got) != len(c.want) {
			t.Fatalf("%s: rows = %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: row %d = %v, want %v", c.name, i, got[i], c.want[i])
			}
		}
		tree := SummaryTree(c.state, "", 380, 480)
		joined := strings.Join(texts(tree), "|")
		for _, r := range c.want {
			// Values elide at the row edge, as noctalia's ElideRight does.
			prefix := r.Value
			if len(prefix) > 20 {
				prefix = prefix[:20]
			}
			if !strings.Contains(joined, r.Label) || !strings.Contains(joined, prefix) {
				t.Errorf("%s: tree is missing row %v in %q", c.name, r, joined)
			}
		}
	}
}

func TestSummaryHeaderShowsAvatarStatusAndOpen(t *testing.T) {
	avatar := repoAvatar(t)
	for _, s := range Statuses {
		tree := SummaryTree(SummaryState{BarState: stateFor(s)}, avatar, 380, 480)
		var img *v1.Node
		walk(tree, func(n *v1.Node) {
			if n.Kind == v1.KindImage {
				img = n
			}
		})
		if img == nil || img.Path != avatar {
			t.Errorf("%s: summary has no avatar image %s", s, avatar)
		}
		var title, status *v1.Node
		walk(tree, func(n *v1.Node) {
			if n.Kind == v1.KindText && n.Text == "Hermes" && title == nil {
				title = n
			}
			if n.Kind == v1.KindText && n.Text == s.Label() && status == nil {
				status = n
			}
		})
		if title == nil || !title.Bold {
			t.Errorf("%s: summary title should be a bold \"Hermes\"", s)
		}
		if status == nil || status.Tone != s.Tone() {
			t.Errorf("%s: status line = %+v, want %q in tone %q", s, status, s.Label(), s.Tone())
		}
		open := findID(tree, "open")
		if open == nil || open.Text != "Open Hermes" || open.Icon != menuIconOpen || open.Role != "button" {
			t.Errorf("%s: open button = %+v", s, open)
		}
	}
	noAvatar := SummaryTree(SummaryState{BarState: stateFor(StatusBusy)}, "", 380, 480)
	var glyph *v1.Node
	walk(noAvatar, func(n *v1.Node) {
		if n.Kind == v1.KindIcon && glyph == nil {
			glyph = n
		}
	})
	if glyph == nil || glyph.Icon != StatusBusy.Glyph() {
		t.Errorf("busy summary without avatar should fall back to the busy glyph, got %+v", glyph)
	}
}

func TestSummaryActionsFollowNoctalia(t *testing.T) {
	actions := manifestActions(t)
	for _, s := range Statuses {
		for _, running := range []bool{false, true} {
			state := SummaryState{BarState: stateFor(s), SessionRunning: running}
			tree := SummaryTree(state, "", 380, 480)
			ids := controlIDs(tree)
			want := []string{"open", "new-session", "interrupt", "refresh", "settings"}
			if strings.Join(ids, ",") != strings.Join(want, ",") {
				t.Errorf("%s running=%v: controls = %v, want %v", s, running, ids, want)
			}
			for _, id := range ids {
				if !actions[id] {
					t.Errorf("%s: control %q is not a manifest service action", s, id)
				}
			}
			labels := map[string]string{"new-session": "New session", "interrupt": "Interrupt", "refresh": "Refresh", "settings": "Hermes Settings"}
			for id, text := range labels {
				if n := findID(tree, id); n == nil || n.Text != text {
					t.Errorf("%s: %s button text = %+v, want %q", s, id, n, text)
				}
			}
			interrupt := findID(tree, "interrupt")
			wantDisabled := !(running || s == StatusBusy)
			if interrupt != nil && interrupt.Disabled != wantDisabled {
				t.Errorf("%s running=%v: interrupt disabled = %v, want %v", s, running, interrupt.Disabled, wantDisabled)
			}
		}
	}
}

func TestSummaryCronJobs(t *testing.T) {
	tree := SummaryTree(richSummary(StatusIdle), "", 380, 480)
	joined := strings.Join(texts(tree), "|")
	for _, want := range []string{"Cron jobs", "Morning digest", "2026-11-12 09:00:00", "every 30m", "Unnamed job", "paused", "Weekly report", "0 8 * * 1", "ok", "error"} {
		if !strings.Contains(joined, want) {
			t.Errorf("cron list is missing %q in %q", want, joined)
		}
	}
	var list *v1.Node
	walk(tree, func(n *v1.Node) {
		if n.Kind == v1.KindList && list == nil {
			list = n
		}
	})
	if list == nil || len(list.Children) != 5 {
		t.Fatalf("cron jobs should be a scrolling list of five rows, got %+v", list)
	}
	for i, job := range richSummary(StatusIdle).CronJobs {
		row := list.Children[i]
		var icon *v1.Node
		walk(row, func(n *v1.Node) {
			if n.Kind == v1.KindIcon && icon == nil {
				icon = n
			}
		})
		wantIcon, wantTone := cronIconActive, v1.ToneAccent
		if !job.Active {
			wantIcon, wantTone = cronIconInactive, v1.ToneSubtle
		}
		if icon == nil || icon.Icon != wantIcon || icon.Tone != wantTone {
			t.Errorf("job %d icon = %+v, want %q %q", i, icon, wantIcon, wantTone)
		}
	}
	empty := SummaryTree(SummaryState{BarState: stateFor(StatusIdle)}, "", 380, 480)
	if strings.Contains(strings.Join(texts(empty), "|"), "Cron jobs") {
		t.Error("summary without cron jobs should not show the Cron jobs section")
	}
	for _, size := range summarySizes {
		tree := SummaryTree(richSummary(StatusIdle), "", size[0], size[1])
		var l *v1.Node
		walk(tree, func(n *v1.Node) {
			if n.Kind == v1.KindList && l == nil {
				l = n
			}
		})
		if l == nil || l.Height < cronRowHeight {
			t.Errorf("%dx%d: cron list = %+v, want room for at least one %d-pixel row", size[0], size[1], l, cronRowHeight)
		}
	}
}

func TestSummaryBanner(t *testing.T) {
	type want struct {
		text, icon string
		fill       string
	}
	approval := richSummary(StatusAttention)
	bare := approval
	bare.ApprovalMessage = ""
	errState := SummaryState{BarState: stateFor(StatusOffline)}
	both := approval
	both.BridgeError = "Bridge request failed: 500"
	cases := map[string]struct {
		state SummaryState
		want  *want
	}{
		"approval with message": {approval, &want{"Run go test ./... in the repository", bannerIconApproval, "container"}},
		"approval bare":         {bare, &want{"Approval required", bannerIconApproval, "container"}},
		"error":                 {errState, &want{"Connection failed: 127.0.0.1:19777", bannerIconError, "error-container"}},
		"approval wins":         {both, &want{"Run go test ./... in the repository", bannerIconApproval, "container"}},
		"none":                  {SummaryState{BarState: stateFor(StatusIdle)}, nil},
	}
	for name, c := range cases {
		tree := SummaryTree(c.state, "", 380, 480)
		var banner *v1.Node
		walk(tree, func(n *v1.Node) {
			if n.Key == "banner" {
				banner = n
			}
		})
		if c.want == nil {
			if banner != nil {
				t.Errorf("%s: unexpected banner %+v", name, banner)
			}
			continue
		}
		if banner == nil {
			t.Errorf("%s: no banner", name)
			continue
		}
		if banner.Fill != c.want.fill {
			t.Errorf("%s: banner fill = %q, want %q", name, banner.Fill, c.want.fill)
		}
		joined := strings.Join(texts(banner), "|")
		if !strings.Contains(joined, c.want.text) {
			t.Errorf("%s: banner text %q, want %q", name, joined, c.want.text)
		}
		var icon *v1.Node
		walk(banner, func(n *v1.Node) {
			if n.Kind == v1.KindIcon {
				icon = n
			}
		})
		if icon == nil || icon.Icon != c.want.icon {
			t.Errorf("%s: banner icon = %+v, want %q", name, icon, c.want.icon)
		}
	}
}

func TestSummaryUsageRow(t *testing.T) {
	cost := func(v float64) *float64 { return &v }
	base := SummaryState{BarState: stateFor(StatusIdle)}
	cases := []struct {
		name  string
		state SummaryState
		want  *summaryRow
	}{
		{"placeholder zeros", base, nil},
		{"input and output", withUsage(base, 12, 34, 46, nil), &summaryRow{"Usage", "12 in · 34 out"}},
		{"input, output and cost", withUsage(base, 12, 34, 46, cost(0.0123)), &summaryRow{"Usage", "12 in · 34 out · $0.0123"}},
		{"total only", withUsage(base, 0, 0, 100, nil), &summaryRow{"Usage", "100 tokens"}},
		{"total only with cost", withUsage(base, 0, 0, 100, cost(1.25)), &summaryRow{"Usage", "100 tokens · $1.2500"}},
		{"cost only", withUsage(base, 0, 0, 0, cost(0.0001)), &summaryRow{"Usage", "0 in · 0 out · $0.0001"}},
	}
	for _, c := range cases {
		got := summaryRows(c.state)
		activity := -1
		for i, r := range got {
			if r.Label == "Activity" {
				activity = i
			}
		}
		if activity < 0 {
			t.Fatalf("%s: no Activity row in %v", c.name, got)
		}
		var usage *summaryRow
		for i, r := range got {
			if r.Label == "Usage" {
				row := r
				usage = &row
				if i != activity+1 {
					t.Errorf("%s: Usage is row %d, want immediately below Activity at %d", c.name, i, activity+1)
				}
			}
		}
		switch {
		case c.want == nil && usage != nil:
			t.Errorf("%s: unexpected Usage row %v", c.name, *usage)
		case c.want != nil && usage == nil:
			t.Errorf("%s: missing Usage row %v in %v", c.name, *c.want, got)
		case c.want != nil && usage != nil && *usage != *c.want:
			t.Errorf("%s: Usage = %v, want %v", c.name, *usage, *c.want)
		}
		for _, size := range summarySizes {
			tree := SummaryTree(c.state, "", size[0], size[1])
			label := c.name
			lintView(t, label, tree, v1.ViewPanel, size[0], size[1])
			checkBudget(t, label, tree, size[0], size[1])
			checkGlyphs(t, label, tree)
			joined := strings.Join(texts(tree), "|")
			if c.want == nil {
				if strings.Contains(joined, "Usage") {
					t.Errorf("%s: tree should hide the Usage row, got %q", c.name, joined)
				}
				continue
			}
			if !strings.Contains(joined, c.want.Label) || !strings.Contains(joined, c.want.Value) {
				t.Errorf("%s: tree is missing %v in %q", c.name, *c.want, joined)
			}
		}
	}
}

func withUsage(state SummaryState, input, output, total int64, cost *float64) SummaryState {
	state.UsageInput, state.UsageOutput, state.UsageTotal, state.UsageCostUSD = input, output, total, cost
	return state
}

func TestSummaryNeverOffersGatewayControl(t *testing.T) {
	for _, s := range Statuses {
		for _, state := range summaryStates(s) {
			ids := controlIDs(SummaryTree(state, "", 380, 480))
			if len(ids) == 0 {
				t.Errorf("%s: summary has no controls", s)
			}
			for _, id := range ids {
				if strings.Contains(id, "gateway") || strings.Contains(id, "stop") {
					t.Errorf("%s: summary offers %q; gateway control lives in the local menu only", s, id)
				}
			}
		}
	}
}
