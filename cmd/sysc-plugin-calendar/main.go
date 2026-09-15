// Command sysc-plugin-calendar is the Calendar plugin: a bar widget with
// today's date and a month panel ported from the shell's built-in clock panel.
package main

import (
	"context"
	"os"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/wire"
	"github.com/Nomadcxx/sysc-plugins/plugins/calendar"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(v1.Identity{ID: "org.sysc.calendar", Name: "Calendar", Version: "0.1.0"}); err != nil {
		return err
	}
	m := calendar.New(time.Now)
	type view struct {
		kind     v1.ViewKind
		rev      uint64
		instance string
	}
	views := map[string]view{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	publish := func() {
		_, weeks, header := m.View()
		date := m.Date()
		wd := m.WeekdayHeader()
		day := m.DayOfMonth()
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = calendar.BarTree(day)
			case v1.ViewTooltip:
				root = calendar.TooltipTree(date, header)
			default:
				root = calendar.PanelTree(header, wd, weeks)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	// The bar label and tooltip go stale at midnight; re-publish periodically.
	ticks := time.NewTicker(30 * time.Second)
	defer ticks.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks.C:
			publish()
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
				switch msg.Node {
				case "cal-prev":
					m.PrevMonth()
				case "cal-next":
					m.NextMonth()
				case "today":
					m.Today()
				}
				publish()
			case *v1.SettingsChanged:
				if raw, ok := msg.Values["week_start"]; ok {
					if s, ok := raw.(string); ok {
						m.SetWeekStart(s)
						publish()
					}
				}
			}
		}
	}
}
