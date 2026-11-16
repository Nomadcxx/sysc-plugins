package hermes

import (
	"fmt"
	"sort"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	summaryPad   = 12
	summaryGap   = 10
	headerHeight = 32
	lineHeight   = 16
	rowGap       = 4
	labelWidth   = 72
	bannerHeight = 28
	bannerPad    = 6
	buttonHeight = 32

	// cronRowHeight fits a job's name over its schedule.
	cronRowHeight = 2 * lineHeight
	cronIconSize  = 16
	cronStatusW   = 80

	cronIconActive     = "check"
	cronIconInactive   = "pause"
	bannerIconApproval = "notifications-active"
	bannerIconError    = "cancel"

	summaryAvatarSize = 28
)

// SummaryState is the slice of the bridge snapshot the summary popup reads,
// on top of the bar's: summary.model, summary.cron, summary.mcp,
// summary.activity, usage, summary.gateway.platforms, approval and
// session.running. It stands in until the runtime snapshot lands.
type SummaryState struct {
	BarState
	Provider         string
	ModelName        string
	CronActive       int
	CronTotal        int
	CronJobs         []CronJob
	MCPEnabled       int
	MCPTotal         int
	ToolEvents       int
	RunningTools     int
	GatewayPlatforms map[string]string
	ApprovalPending  bool
	ApprovalMessage  string
	SessionRunning   bool
	// UsageInput, UsageOutput, UsageTotal and UsageCostUSD match the
	// runtime Usage shape (input, output, total, cost_usd) without
	// declaring a second type Usage.
	UsageInput   int64
	UsageOutput  int64
	UsageTotal   int64
	UsageCostUSD *float64
}

type summaryRow struct{ Label, Value string }

// summaryRows are noctalia's summary lines, with its wording. Usage is
// added immediately below Activity when the bridge reports it.
func summaryRows(s SummaryState) []summaryRow {
	rows := []summaryRow{
		{"Model", modelLabel(s)},
		{"Cron", fmt.Sprintf("%d active / %d total", s.CronActive, s.CronTotal)},
		{"MCP", mcpLabel(s)},
		{"Gateway", gatewayLabel(s)},
		{"Activity", activityLabel(s)},
	}
	if usageReported(s) {
		rows = append(rows, summaryRow{"Usage", usageLabel(s)})
	}
	return rows
}

func usageReported(s SummaryState) bool {
	return s.UsageInput != 0 || s.UsageOutput != 0 || s.UsageTotal != 0 || s.UsageCostUSD != nil
}

func usageLabel(s SummaryState) string {
	var value string
	if s.UsageInput == 0 && s.UsageOutput == 0 && s.UsageTotal != 0 {
		value = fmt.Sprintf("%d tokens", s.UsageTotal)
	} else {
		value = fmt.Sprintf("%d in · %d out", s.UsageInput, s.UsageOutput)
	}
	if s.UsageCostUSD != nil {
		value += fmt.Sprintf(" · $%.4f", *s.UsageCostUSD)
	}
	return value
}

func modelLabel(s SummaryState) string {
	name := s.ModelName
	if name == "" {
		name = s.Model
	}
	switch {
	case s.Provider != "" && name != "":
		return s.Provider + " / " + name
	case name != "":
		return name
	case s.Provider != "":
		return s.Provider
	}
	return "Not set"
}

func mcpLabel(s SummaryState) string {
	if s.MCPTotal == 0 {
		return "No MCP servers"
	}
	return fmt.Sprintf("%d enabled / %d total", s.MCPEnabled, s.MCPTotal)
}

func gatewayLabel(s SummaryState) string {
	if len(s.GatewayPlatforms) == 0 {
		if s.GatewayStatus != "" {
			return s.GatewayStatus
		}
		return "Unknown"
	}
	names := make([]string, 0, len(s.GatewayPlatforms))
	for name := range s.GatewayPlatforms {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + ": " + s.GatewayPlatforms[name]
	}
	return strings.Join(parts, ", ")
}

func activityLabel(s SummaryState) string {
	switch {
	case s.ToolEvents == 0:
		return "No background actions"
	case s.RunningTools > 0:
		return fmt.Sprintf("%d running", s.RunningTools)
	}
	return fmt.Sprintf("%d background actions", s.ToolEvents)
}

// SummaryTree renders the left-click summary: the header with the avatar,
// status and Open Hermes; the summary lines (Usage below Activity when
// reported); the cron jobs; the approval or error banner; and New session,
// Interrupt, Refresh and Hermes Settings. It fills the width by height box
// the host opened.
func SummaryTree(s SummaryState, avatar string, width, height int) *v1.Node {
	status := s.Status
	inner := width - 2*summaryPad

	children := []*v1.Node{
		summaryHeader(status, avatar, inner),
		{Kind: v1.KindSeparator},
		summaryGrid(s, inner),
	}
	banner := summaryBanner(s, inner)
	actions := summaryActions(s, status, inner)

	if len(s.CronJobs) > 0 {
		used := 2*summaryPad + headerHeight + 1 + gridHeight(len(summaryRows(s))) + lineHeight + 2*buttonHeight
		// Gaps between header, separator, grid, title, list, actions and
		// settings, plus one more for the banner.
		gaps := 6
		if banner != nil {
			used += bannerHeight
			gaps++
		}
		used += gaps * summaryGap
		listHeight := height - used
		if listHeight < cronRowHeight {
			listHeight = cronRowHeight
		}
		children = append(children,
			&v1.Node{Kind: v1.KindText, Text: "Cron jobs", Bold: true, Size: "label", Height: lineHeight},
			cronList(s.CronJobs, inner, listHeight),
		)
	}
	if banner != nil {
		children = append(children, banner)
	}
	children = append(children, actions, &v1.Node{
		Kind: v1.KindButton, ID: "settings", Key: "settings", Icon: menuIconSettings,
		Text: "Hermes Settings", Name: "Hermes Settings", Role: "button",
		Width: inner, Height: buttonHeight, Padding: 8,
		Events: []v1.EventKind{v1.EventActivate},
	})
	return &v1.Node{
		Kind: v1.KindColumn, Width: width, Height: height,
		Padding: summaryPad, Gap: summaryGap, Children: children,
	}
}

func summaryHeader(status Status, avatar string, inner int) *v1.Node {
	var mark *v1.Node
	if avatar != "" {
		mark = &v1.Node{Kind: v1.KindImage, Path: avatar, Name: "Hermes", Role: "img", ImageSize: summaryAvatarSize}
	} else {
		glyph := fallbackAvatarIcon
		if status.Key == StatusBusy {
			glyph = StatusFor(StatusBusy).Icon
		}
		mark = &v1.Node{Kind: v1.KindIcon, Icon: glyph, IconSize: 24, Tone: status.Tone, Name: "Hermes"}
	}
	return &v1.Node{
		Kind: v1.KindRow, Width: inner, Height: headerHeight, PinEnd: true,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: 10, Children: []*v1.Node{
				mark,
				{Kind: v1.KindColumn, Children: []*v1.Node{
					{Kind: v1.KindText, Text: "Hermes", Bold: true, Size: "title"},
					{Kind: v1.KindText, Text: status.Text, Size: "caption", Tone: status.Tone},
				}},
			}},
			{
				Kind: v1.KindButton, ID: "open", Key: "open", Icon: menuIconOpen,
				Text: "Open Hermes", Name: "Open Hermes", Role: "button",
				Height: buttonHeight, Padding: 8, Events: []v1.EventKind{v1.EventActivate},
			},
		},
	}
}

func gridHeight(rows int) int {
	if rows <= 0 {
		return 0
	}
	return rows*lineHeight + (rows-1)*rowGap
}

func summaryGrid(s SummaryState, inner int) *v1.Node {
	rows := summaryRows(s)
	valueBytes := (inner - labelWidth - 8) / 8
	grid := &v1.Node{Kind: v1.KindColumn, Width: inner, Height: gridHeight(len(rows)), Gap: rowGap}
	for _, r := range rows {
		grid.Children = append(grid.Children, &v1.Node{
			Kind: v1.KindRow, Width: inner, Height: lineHeight, Gap: 8, PinEnd: true,
			Children: []*v1.Node{
				{Kind: v1.KindText, Text: r.Label, Size: "caption", Tone: v1.ToneSubtle, Width: labelWidth},
				{Kind: v1.KindText, Text: clip(r.Value, valueBytes), Size: "caption"},
			},
		})
	}
	return grid
}

func cronList(jobs []CronJob, inner, height int) *v1.Node {
	textW := inner - cronIconSize - cronStatusW - 2*8
	list := &v1.Node{Kind: v1.KindList, Key: "cron-jobs", Width: inner, Height: height, Gap: 0}
	for i, job := range jobs {
		icon, tone := cronIconActive, v1.ToneAccent
		if !job.Active {
			icon, tone = cronIconInactive, v1.ToneSubtle
		}
		name := job.Name
		if name == "" {
			name = "Unnamed job"
		}
		when := job.NextRun
		if when == "" {
			when = job.Schedule
		}
		if when == "" {
			when = job.State
		}
		when = strings.Replace(when, "T", " ", 1)
		last := job.LastStatus
		if last == "" {
			last = job.State
		}
		list.Children = append(list.Children, &v1.Node{
			Kind: v1.KindRow, Key: fmt.Sprintf("cron-%d", i), Width: inner, Height: cronRowHeight, Gap: 8,
			Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: icon, IconSize: cronIconSize, Tone: tone, Name: name},
				{Kind: v1.KindColumn, Width: textW, Children: []*v1.Node{
					{Kind: v1.KindText, Text: clip(name, textW/8), Size: "caption"},
					{Kind: v1.KindText, Text: clip(when, textW/8), Size: "caption", Tone: v1.ToneSubtle},
				}},
				{Kind: v1.KindText, Text: clip(last, cronStatusW/8), Size: "caption", Tone: tone, Width: cronStatusW},
			},
		})
	}
	return list
}

// summaryBanner is noctalia's alert strip: a pending approval wins over a
// bridge error, and neither shows nothing.
func summaryBanner(s SummaryState, inner int) *v1.Node {
	var icon, text, fill string
	tone := v1.ToneNormal
	switch {
	case s.ApprovalPending:
		icon, fill, tone = bannerIconApproval, "container", v1.ToneWarning
		text = strings.TrimSpace(s.ApprovalMessage)
		if text == "" {
			text = "Approval required"
		}
	case strings.TrimSpace(s.BridgeError) != "":
		icon, fill, tone = bannerIconError, "error-container", v1.ToneError
		text = strings.TrimSpace(s.BridgeError)
	default:
		return nil
	}
	textBytes := (inner - 2*bannerPad - cronIconSize - 8) / 8
	return &v1.Node{
		Kind: v1.KindRow, Key: "banner", Fill: fill, Radius: 8,
		Width: inner, Height: bannerHeight, Padding: bannerPad, Gap: 8,
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon, IconSize: cronIconSize, Tone: tone},
			{Kind: v1.KindText, Text: clip(text, textBytes), Size: "caption"},
		},
	}
}

// summaryActions is New session, Interrupt and Refresh. Interrupt is live
// only while a session runs or Hermes is busy, as in noctalia.
func summaryActions(s SummaryState, status Status, inner int) *v1.Node {
	w := (inner - 2*8) / 3
	button := func(id, text, icon string) *v1.Node {
		return &v1.Node{
			Kind: v1.KindButton, ID: id, Key: id, Icon: icon, Text: text, Name: text,
			Role: "button", Width: w, Height: buttonHeight, Padding: 8,
			Events: []v1.EventKind{v1.EventActivate},
		}
	}
	interrupt := button("interrupt", "Interrupt", menuIconInterrupt)
	interrupt.Disabled = !(s.SessionRunning || status.Key == StatusBusy)
	return &v1.Node{
		Kind: v1.KindRow, Width: inner, Height: buttonHeight, Gap: 8,
		Children: []*v1.Node{
			button("new-session", "New session", menuIconNewSession),
			interrupt,
			button("refresh", "Refresh", menuIconRefresh),
		},
	}
}
