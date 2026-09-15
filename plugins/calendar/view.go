package calendar

import (
	"fmt"

	"github.com/Nomadcxx/sysc-plugins/internal/wire"
)

const dayCellWidth = 28

func BarTree(day string) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: "schedule"},
		{Kind: v1.KindText, Text: day, Tabular: true},
	}}
}

func TooltipTree(date, month string) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindText, Text: date},
		{Kind: v1.KindText, Text: month},
	}}
}

// PanelTree draws the month: header with navigation, weekday row, and the grid.
// The today cell is the one interactive node; activating it snaps back to the
// current month.
func PanelTree(header string, weekdays []string, weeks [][]Cell) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindButton, ID: "cal-prev", Text: "<", Name: "Previous month", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindText, Text: header},
			{Kind: v1.KindButton, ID: "cal-next", Text: ">", Name: "Next month", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
	}}
	weekdaysRow := &v1.Node{Kind: v1.KindRow, Gap: 2}
	for _, d := range weekdays {
		weekdaysRow.Children = append(weekdaysRow.Children, &v1.Node{
			Kind: v1.KindText, Text: d, Width: dayCellWidth,
		})
	}
	col.Children = append(col.Children, weekdaysRow)
	for _, week := range weeks {
		row := &v1.Node{Kind: v1.KindRow, Gap: 2}
		for _, cell := range week {
			row.Children = append(row.Children, dayNode(cell))
		}
		col.Children = append(col.Children, row)
	}
	return col
}

func dayNode(cell Cell) *v1.Node {
	label := fmt.Sprintf("%d", cell.Day)
	if cell.Today {
		return &v1.Node{Kind: v1.KindButton, ID: "today", Text: label,
			Name: "Today, back to current month", Role: "button", Tabular: true,
			Width: dayCellWidth, Events: []v1.EventKind{v1.EventActivate}}
	}
	return &v1.Node{Kind: v1.KindText, Text: label, Tabular: true, Width: dayCellWidth}
}
