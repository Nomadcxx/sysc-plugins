package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/world-clock"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.world-clock", Name: "World Clock", Version: "1.0.0"})); err != nil {
		return err
	}
	clk := worldclock.New()
	type view struct {
		kind v1.ViewKind
		rev  uint64
	}
	views := map[string]view{}
	draft := ""
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
	restore(ctx, c, clk)
	ticks := time.NewTicker(time.Second)
	defer ticks.Stop()

	snapshot := func() {
		readings := clk.Readings(time.Now())
		first := worldclock.Reading{}
		if len(readings) > 0 {
			first = readings[0]
		}
		for id, v := range views {
			v.rev++
			views[id] = v
			switch v.kind {
			case v1.ViewBar:
				_ = c.Snapshot(id, v.rev, worldclock.BarTree(first))
			default:
				_ = c.Snapshot(id, v.rev, worldclock.PanelTree(readings, clk.PendingAdd(), clk.PendingRemove(), draft))
			}
		}
	}
	patchTimes := func() {
		readings := clk.Readings(time.Now())
		repl := worldclock.TimePatch(readings)
		for id, v := range views {
			if v.rev == 0 {
				continue
			}
			next := v.rev + 1
			if err := c.Patch(id, v.rev, next, repl); err != nil {
				continue
			}
			v.rev = next
			views[id] = v
		}
	}

	snapshot()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks.C:
			patchTimes()
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				views[m.ViewID] = view{kind: m.View}
				snapshot()
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					v.rev = 0
					views[m.ViewID] = v
				}
				snapshot()
			case *v1.InputEvent:
				handle(ctx, c, clk, &draft, m)
				snapshot()
			case *v1.SettingsChanged:
				if raw, ok := m.Values["hour24"]; ok {
					switch v := raw.(type) {
					case bool:
						clk.SetHour24(v)
					case float64:
						clk.SetHour24(v != 0)
					}
					snapshot()
				}
			}
		}
	}
}

func handle(ctx context.Context, c *v1.Client, clk *worldclock.Clock, draft *string, m *v1.InputEvent) {
	switch {
	case m.Node == "open":
		_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
	case m.Node == "zone":
		*draft = m.Text
		if m.Event == v1.EventSubmit {
			_ = clk.ProposeAdd(strings.TrimSpace(*draft))
		}
	case m.Node == "confirm-add":
		clk.ConfirmAdd()
		*draft = ""
		save(ctx, c, clk)
	case m.Node == "confirm-remove":
		clk.ConfirmRemove()
		save(ctx, c, clk)
	case m.Node == "cancel":
		clk.CancelPending()
	case strings.HasPrefix(m.Node, "rm:"):
		clk.ProposeRemove(strings.TrimPrefix(m.Node, "rm:"))
	case strings.HasPrefix(m.Node, "drop:") && m.Event == v1.EventDrop:
		insert, _ := strconv.Atoi(strings.TrimPrefix(m.Node, "drop:"))
		zones := clk.Zones()
		from := -1
		for i, z := range zones {
			if z == m.Text {
				from = i
				break
			}
		}
		if from >= 0 {
			_ = clk.Reorder(from, insert)
			save(ctx, c, clk)
		}
	}
}

func save(ctx context.Context, c *v1.Client, clk *worldclock.Clock) {
	raw, _ := json.Marshal(clk.Zones())
	_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "zones", Value: raw})
}

func restore(ctx context.Context, c *v1.Client, clk *worldclock.Clock) {
	reply, err := c.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "zones"})
	if err != nil || !reply.OK {
		return
	}
	var result v1.StateGetResult
	if err := json.Unmarshal(reply.Result, &result); err != nil || !result.Found {
		return
	}
	var zones []string
	if err := json.Unmarshal(result.Value, &zones); err != nil {
		return
	}
	clk.Restore(zones)
}
