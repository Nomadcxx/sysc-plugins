package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/screen-recorder"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout, recorder.Options{}); err != nil {
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer, opt recorder.Options) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(v1.Identity{ID: "org.sysc.screen-recorder", Name: "Screen Recorder", Version: "1.0.0"}); err != nil {
		return err
	}

	cfg, _ := recorder.ParseConfig(nil)
	rec := recorder.New(cfg, opt)
	defer rec.Close()

	type view struct {
		kind   v1.ViewKind
		rev    uint64
		output string
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

	restore(ctx, c, rec)
	last := rec.Snapshot()
	notified := ""

	publish := func() {
		last = rec.Snapshot()
		now := time.Now()
		if opt.Now != nil {
			now = opt.Now()
		}
		for id, v := range views {
			v.rev++
			views[id] = v
			var root *v1.Node
			switch v.kind {
			case v1.ViewTooltip:
				root = recorder.TooltipTree(last)
			case v1.ViewPanel:
				root = recorder.PanelTree(last, cfg, now)
			default:
				root = recorder.BarTree(last, cfg)
			}
			_ = c.Snapshot(id, v.rev, root)
		}
		if last.Mode == recorder.Failed && last.Err != notified {
			notified = last.Err
			_, _ = c.Call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Screen Recorder", Body: last.Err + "\n" + last.Logs, Urgency: v1.UrgencyNormal})
		}
		if last.Artifact != "" && last.Artifact != notified && last.Mode == recorder.Idle {
			notified = last.Artifact
			_, _ = c.Call(ctx, v1.CallNotify, v1.NotifyParams{Summary: "Recording saved", Body: last.Artifact, Urgency: v1.UrgencyNormal})
		}
		saveOwnership(ctx, c, rec)
	}

	ticks := time.NewTicker(time.Second)
	defer ticks.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks.C:
			if last.Mode != recorder.Idle {
				publish()
			}
		case snap := <-rec.Updates():
			if snap == last {
				continue
			}
			publish()
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				rec.Close()
				return nil
			case *v1.ViewOpen:
				views[m.ViewID] = view{kind: m.View, output: m.Output}
				publish()
			case *v1.ViewClose:
				delete(views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					v.rev = 0
					views[m.ViewID] = v
				}
				publish()
			case *v1.InputEvent:
				output := m.Output
				if output == "" {
					reply, err := c.Call(ctx, v1.CallOutputContext, v1.OutputContextParams{Generation: m.Generation})
					if err == nil && reply.OK {
						var got v1.OutputContextResult
						_ = json.Unmarshal(reply.Result, &got)
						output = got.Output
					}
				}
				open, record, stop, replay, save := recorder.HandleInput(m, last.Mode)
				if open {
					_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: output, Instance: m.ViewID})
				}
				if record || stop {
					rec.ToggleRecord(output)
				}
				if replay {
					rec.ToggleReplay(output)
				}
				if save {
					rec.SaveReplay()
				}
			case *v1.SettingsChanged:
				next, err := recorder.ParseConfig(m.Values)
				if err == nil {
					cfg = next
					rec.Reconfigure(cfg)
					publish()
				}
			}
		}
	}
}

func restore(ctx context.Context, c *v1.Client, rec *recorder.Recorder) {
	reply, err := c.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "ownership"})
	if err != nil || !reply.OK {
		return
	}
	var got v1.StateGetResult
	if err := json.Unmarshal(reply.Result, &got); err != nil || !got.Found {
		return
	}
	var own recorder.Ownership
	if err := json.Unmarshal(got.Value, &own); err != nil || own.PID == 0 {
		return
	}
	rec.Recover(own)
}

func saveOwnership(ctx context.Context, c *v1.Client, rec *recorder.Recorder) {
	own := rec.Ownership()
	raw, _ := json.Marshal(own)
	_, _ = c.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "ownership", Value: raw})
}
