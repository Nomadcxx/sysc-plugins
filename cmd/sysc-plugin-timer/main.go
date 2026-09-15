// Command sysc-plugin-timer is the Timer reference plugin.
package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	v1 "github.com/Nomadcxx/sysc-plugins/internal/wire"
	"github.com/Nomadcxx/sysc-plugins/plugins/timer"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	hello, err := c.Handshake(v1.Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.0.0"})
	if err != nil {
		return err
	}
	_ = hello
	tm := timer.New(time.Now)
	type view struct {
		kind     v1.ViewKind
		rev      uint64
		instance string
	}
	views := map[string]view{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticks := time.NewTicker(time.Second)
	defer ticks.Stop()

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
	restore(ctx, c, tm)

	publish := func() {
		text := timer.FormatMMSS(tm.Remaining())
		dur := formatDur(tm.Duration())
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = timer.BarTree(text, tm.Running())
			case v1.ViewTooltip:
				root = timer.TooltipTree(text)
			default:
				root = timer.PanelTree(text, dur, tm.Running())
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks.C:
			_, done := tm.Tick()
			if done {
				_, _ = c.Call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Timer", Body: "Done", Urgency: v1.UrgencyNormal})
			}
			if tm.Running() || done {
				publish()
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				views[m.ViewID] = view{kind: m.View, instance: m.Instance}
				publish()
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.InputEvent:
				handleInput(ctx, c, tm, m)
				publish()
			case *v1.SettingsChanged:
				if raw, ok := m.Values["default_duration"]; ok {
					if s, ok := raw.(string); ok {
						if d, err := timer.ParseDuration(s); err == nil && !tm.Running() {
							tm.SetDuration(d)
							publish()
						}
					}
				}
			}
		}
	}
}

func handleInput(ctx context.Context, c *v1.Client, tm *timer.Timer, m *v1.InputEvent) {
	switch m.Node {
	case "start":
		tm.Start()
		save(ctx, c, tm)
	case "pause":
		tm.Pause()
		save(ctx, c, tm)
	case "reset":
		tm.Reset()
		save(ctx, c, tm)
	case "duration":
		if m.Event == v1.EventSubmit || m.Event == v1.EventChange {
			if d, err := timer.ParseDuration(m.Text); err == nil {
				tm.SetDuration(d)
			}
		}
	}
}

func save(ctx context.Context, c *v1.Client, tm *timer.Timer) {
	if deadline, ok := tm.Deadline(); ok {
		raw, _ := json.Marshal(deadline.Unix())
		_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "deadline", Value: raw})
		return
	}
	_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "deadline", Value: json.RawMessage("null")})
}

func restore(ctx context.Context, c *v1.Client, tm *timer.Timer) {
	reply, err := c.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "deadline"})
	if err != nil || !reply.OK {
		return
	}
	var result v1.StateGetResult
	if err := json.Unmarshal(reply.Result, &result); err != nil || !result.Found {
		return
	}
	var unix int64
	if err := json.Unmarshal(result.Value, &unix); err != nil || unix == 0 {
		return
	}
	tm.Restore(time.Unix(unix, 0))
}

func formatDur(d time.Duration) string {
	return timer.FormatMMSS(d)
}
