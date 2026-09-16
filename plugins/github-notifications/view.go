package githubnotifications

import (
	"fmt"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// BarTree renders the bar pill: bell icon plus the unread count.
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

// TooltipTree explains the bar: status, count, and last refresh age.
func TooltipTree(statusLine string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindText, Text: statusLine},
	}}
}

// PanelTree lists notifications with per-row open and mark-read actions.
func PanelTree(statusLine, errMsg string, items []Item) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: statusLine},
			{Kind: v1.KindButton, ID: "refresh", Text: "Refresh", Name: "Refresh notifications", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindButton, ID: "mark-all", Text: "Mark all read", Name: "Mark all notifications read", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
	}}
	if errMsg != "" {
		col.Children = append(col.Children,
			&v1.Node{Kind: v1.KindText, Text: errMsg, Tone: v1.ToneError})
	}
	if len(items) == 0 {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: "No unread notifications"})
		return col
	}
	for _, item := range items {
		col.Children = append(col.Children, notificationRow(item))
	}
	return col
}

func notificationRow(item Item) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindButton, ID: "open:" + item.ID,
				Text: item.Title, Name: "Open " + item.Title, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindText, Text: item.RelativeTime, Tabular: true},
			{Kind: v1.KindButton, ID: "read:" + item.ID,
				Text: "Read", Name: "Mark " + item.Title + " as read", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
		{Kind: v1.KindText, Text: fmt.Sprintf("%s · %s", item.Repo, item.ReasonLabel)},
	}}
}
