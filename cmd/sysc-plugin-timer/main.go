// Command sysc-plugin-timer is the Pomodoro timer plugin.
package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/timer"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	return runClock(in, out, time.Now)
}

func runClock(in io.Reader, out io.Writer, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.timer", Name: "Pomodoro Timer", Version: "1.4.0"})); err != nil {
		return err
	}
	tm := timer.NewSession(now)
	type view struct {
		kind     v1.ViewKind
		rev      uint64
		instance string
		width    int
	}
	views := map[string]view{}
	showWhenIdle := true
	playSound := true
	stateChanged := false
	ctx, cancel := context.WithCancel(context.Background())
	saves := hostcall.NewStateSaver(ctx, c)
	defer func() {
		cancel()
		saves.Flush(time.Second)
	}()

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
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	restoreResults := make(chan timer.Snapshot, 1)
	go func() {
		if snap, ok := restore(ctx, c); ok {
			select {
			case restoreResults <- snap:
			case <-ctx.Done():
			}
		}
	}()

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
				root = timer.BarTreeAtWidth(text, now.State, showWhenIdle, v.width)
			case v1.ViewTooltip:
				root = timer.TooltipTree(text, now.State)
			default:
				root = timer.PanelTree(text, now.State, now.Progress, now.Mode, now.Completed, now.Sessions)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	handleInput := func(ctx context.Context, c *v1.Client, tm *timer.Session, m *v1.InputEvent) bool {
		switch m.Node {
		case "open":
			// A click on the fired timer clears it; the user does not want
			// another session just yet.
			if tm.Fired() {
				tm.Reset()
				save(saves, tm)
				return true
			}
			hostcall.Async(ctx, c, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
		case "close":
			hostcall.Async(ctx, c, v1.CallPanelClose, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
		case "start":
			// The panel stays open. It carries the ring, the phase pills and
			// the transport, so closing it on start takes the controls away
			// at the moment they start mattering -- and the shell's own open
			// call is a toggle, which a close issued from here races.
			tm.Start()
			save(saves, tm)
			return true
		case "pause":
			tm.Pause()
			save(saves, tm)
			return true
		case "reset":
			tm.Reset()
			save(saves, tm)
			return true
		case "skip":
			tm.Skip()
			save(saves, tm)
			return true
		case "mode-work":
			tm.SetMode(timer.ModeWork)
			save(saves, tm)
			return true
		case "mode-short":
			tm.SetMode(timer.ModeShort)
			save(saves, tm)
			return true
		case "mode-long":
			tm.SetMode(timer.ModeLong)
			save(saves, tm)
			return true
		}
		return false
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
				// Start stored this phase's deadline. The roll stays in
				// memory until it is saved, and a restart would complete
				// the finished phase again.
				save(saves, tm)
				body := "Break over — back to work"
				sound := timer.SoundBreakDone
				if prev == timer.ModeWork {
					body = "Work complete — time for a break"
					if tm.Mode() == timer.ModeLong {
						body = "Work complete — time for a long break"
					}
					sound = timer.SoundWorkDone
				}
				if playSound {
					timer.Play(sound)
				}
				hostcall.Async(ctx, c, v1.CallNotify, v1.NotifyParams{Summary: "Pomodoro", Body: body, Urgency: v1.UrgencyNormal})
			}
			if tm.Running() || done {
				publish()
			}
		case snap := <-restoreResults:
			if !stateChanged {
				tm.RestoreSnapshot(snap)
				publish()
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				views[m.ViewID] = view{kind: m.View, instance: m.Instance, width: m.Width}
				publish()
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.InputEvent:
				if handleInput(ctx, c, tm, m) {
					stateChanged = true
				}
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
				if raw, ok := m.Values["sound"]; ok {
					if b, ok := raw.(bool); ok {
						playSound = b
					}
				}
				if changed {
					publish()
				}
			}
		}
	}
}

func save(saves *hostcall.StateSaver, tm *timer.Session) {
	raw, err := json.Marshal(tm.Snapshot())
	if err != nil {
		return
	}
	saves.Save("session", raw)
}

func restore(ctx context.Context, c *v1.Client) (timer.Snapshot, bool) {
	reply, err := hostcall.Call(ctx, c, v1.CallStateGet, v1.StateGetParams{Key: "session"})
	if err != nil || !reply.OK {
		return timer.Snapshot{}, false
	}
	var result v1.StateGetResult
	if err := json.Unmarshal(reply.Result, &result); err != nil || !result.Found {
		return timer.Snapshot{}, false
	}
	var snap timer.Snapshot
	if err := json.Unmarshal(result.Value, &snap); err != nil {
		return timer.Snapshot{}, false
	}
	return snap, true
}
