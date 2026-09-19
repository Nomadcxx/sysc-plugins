// Command sysc-plugin-timer is the Pomodoro timer plugin.
package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/timer"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.3.0"})); err != nil {
		return err
	}
	tm := timer.NewSession(time.Now)
	type view struct {
		kind     v1.ViewKind
		rev      uint64
		instance string
	}
	views := map[string]view{}
	showWhenIdle := true
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
		// One read for the whole frame: the bar and the panel have to agree
		// about the phase even when a tick rolls it over mid-publish.
		now := tm.View()
		text := timer.FormatClock(now.Remaining)
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = timer.BarTree(text, now.State, showWhenIdle)
			case v1.ViewTooltip:
				root = timer.TooltipTree(text, now.State)
			default:
				root = timer.PanelTree(text, now.State, now.Progress, now.Mode, now.Completed, now.Sessions)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	handleInput := func(ctx context.Context, c *v1.Client, tm *timer.Session, m *v1.InputEvent) {
		switch m.Node {
		case "open":
			// A click on the fired timer clears it; the user does not want
			// another session just yet.
			if tm.Fired() {
				tm.Reset()
				save(ctx, c, tm)
				return
			}
			_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
		case "close":
			_, _ = c.Call(ctx, v1.CallPanelClose, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
		case "start":
			paused := tm.State() == timer.StatePaused
			tm.Start()
			save(ctx, c, tm)
			if !paused {
				// Noctalia closes the panel on start; the bar carries the count.
				_, _ = c.Call(ctx, v1.CallPanelClose, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
			}
		case "pause":
			tm.Pause()
			save(ctx, c, tm)
		case "reset":
			tm.Reset()
			save(ctx, c, tm)
		case "mode-work":
			tm.SetMode(timer.ModeWork)
			save(ctx, c, tm)
		case "mode-short":
			tm.SetMode(timer.ModeShort)
			save(ctx, c, tm)
		case "mode-long":
			tm.SetMode(timer.ModeLong)
			save(ctx, c, tm)
		}
	}

	minutes := func(raw any) time.Duration {
		if f, ok := raw.(float64); ok {
			return time.Duration(int(f)) * time.Minute
		}
		return 0
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks.C:
			prev := tm.Mode()
			_, done := tm.Tick()
			if done {
				body := "Break over — back to work"
				if prev == timer.ModeWork {
					body = "Work complete — time for a break"
					if tm.Mode() == timer.ModeLong {
						body = "Work complete — time for a long break"
					}
				}
				_, _ = c.Call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Pomodoro", Body: body, Urgency: v1.UrgencyNormal})
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
				changed := false
				if raw, ok := m.Values["work_duration"]; ok {
					if d := minutes(raw); d > 0 {
						tm.SetDurations(d, 0, 0)
						changed = true
					}
				}
				if raw, ok := m.Values["short_break_duration"]; ok {
					if d := minutes(raw); d > 0 {
						tm.SetDurations(0, d, 0)
						changed = true
					}
				}
				if raw, ok := m.Values["long_break_duration"]; ok {
					if d := minutes(raw); d > 0 {
						tm.SetDurations(0, 0, d)
						changed = true
					}
				}
				if raw, ok := m.Values["sessions_before_long_break"]; ok {
					if f, ok := raw.(float64); ok {
						tm.SetSessions(int(f))
						changed = true
					}
				}
				if raw, ok := m.Values["auto_start_work"]; ok {
					if b, ok := raw.(bool); ok {
						tm.SetAutoWork(b)
						changed = true
					}
				}
				if raw, ok := m.Values["auto_start_breaks"]; ok {
					if b, ok := raw.(bool); ok {
						tm.SetAutoBreak(b)
						changed = true
					}
				}
				if raw, ok := m.Values["show_when_idle"]; ok {
					if b, ok := raw.(bool); ok {
						showWhenIdle = b
						changed = true
					}
				}
				if changed {
					publish()
				}
			}
		}
	}
}

func save(ctx context.Context, c *v1.Client, tm *timer.Session) {
	raw, err := json.Marshal(tm.Snapshot())
	if err != nil {
		return
	}
	_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "session", Value: raw})
}

func restore(ctx context.Context, c *v1.Client, tm *timer.Session) {
	reply, err := c.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "session"})
	if err != nil || !reply.OK {
		return
	}
	var result v1.StateGetResult
	if err := json.Unmarshal(reply.Result, &result); err != nil || !result.Found {
		return
	}
	var snap timer.Snapshot
	if err := json.Unmarshal(result.Value, &snap); err != nil {
		return
	}
	tm.RestoreSnapshot(snap)
}
