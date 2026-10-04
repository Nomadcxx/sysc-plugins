package main

import (
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/timer"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// timerHost is the shell side of one plugin process: it answers state and
// notify calls and keeps every session snapshot the plugin writes.
type timerHost struct {
	t        *testing.T
	enc      *v1.Encoder
	mu       sync.Mutex
	saves    chan timer.Snapshot
	restored chan struct{}
	done     chan error
}

func startTimerPlugin(t *testing.T, now func() time.Time) *timerHost {
	t.Helper()
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	h := &timerHost{
		t:        t,
		enc:      v1.NewEncoder(hostOut),
		saves:    make(chan timer.Snapshot, 8),
		restored: make(chan struct{}, 1),
		done:     make(chan error, 1),
	}
	go func() {
		h.done <- runClock(toPlugin, pluginOut, now)
		_ = pluginOut.Close()
	}()
	go h.read(hostIn)
	t.Cleanup(func() {
		_ = h.send(&v1.HostShutdown{})
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
			t.Error("plugin did not exit after host.shutdown")
		}
		_ = hostOut.Close()
		_ = hostIn.Close()
		_ = toPlugin.Close()
	})
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: v1.ProtocolMajor, Minor: v1.ProtocolMinor}},
		Plugin:       v1.Identity{ID: "org.sysc.timer", Name: "Pomodoro Timer", Version: "1.4.0"},
		Capabilities: []string{"notifications", "panels", "settings", "state"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.restored:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin did not restore session state")
	}
	return h
}

func (h *timerHost) send(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enc.Encode(m)
}

func (h *timerHost) read(r io.Reader) {
	dec := v1.NewDecoder(r, v1.ToHost)
	for {
		msg, err := dec.Decode()
		if err != nil {
			return
		}
		call, ok := msg.(*v1.HostCall)
		if !ok {
			continue
		}
		var result json.RawMessage
		switch call.Call {
		case v1.CallStateGet:
			result, _ = json.Marshal(v1.StateGetResult{Found: false})
			select {
			case h.restored <- struct{}{}:
			default:
			}
		case v1.CallStateSet:
			var params v1.StateSetParams
			var snap timer.Snapshot
			if json.Unmarshal(call.Params, &params) == nil && params.Key == "session" &&
				json.Unmarshal(params.Value, &snap) == nil {
				h.saves <- snap
			}
		}
		_ = h.send(&v1.HostReply{ID: call.ID, OK: true, Result: result})
	}
}

func (h *timerHost) nextSave(what string) timer.Snapshot {
	h.t.Helper()
	select {
	case snap := <-h.saves:
		return snap
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timed out waiting for %s", what)
	}
	return timer.Snapshot{}
}

// A finished phase has to be written when the tick rolls it. The snapshot
// from start still names that phase's deadline, and restoring it arms the
// countdown at zero so the next tick completes it again.
func TestTickSavesTheRolledPhase(t *testing.T) {
	cases := []struct {
		name    string
		auto    bool
		running bool
	}{
		{name: "idle break", auto: false, running: false},
		{name: "auto-started break", auto: true, running: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			now := time.Unix(1_700_000_000, 0)
			clock := func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return now
			}
			setClock := func(at time.Time) {
				mu.Lock()
				now = at
				mu.Unlock()
			}

			h := startTimerPlugin(t, clock)
			if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{
				"work_duration":        1.0,
				"short_break_duration": 5.0,
				"long_break_duration":  15.0,
				"sound":                false,
				"auto_start_breaks":    tc.auto,
			}}); err != nil {
				t.Fatal(err)
			}
			if err := h.send(&v1.InputEvent{ViewID: "panel", Node: "start", Event: v1.EventActivate}); err != nil {
				t.Fatal(err)
			}
			started := h.nextSave("the snapshot written at start")
			if started.Mode != timer.ModeWork || started.Completed != 0 || started.Deadline == 0 {
				t.Fatalf("start snapshot = %+v, want a running work phase", started)
			}

			// The phase's own deadline has passed. The next tick rolls it.
			setClock(time.Unix(started.Deadline, 0))
			rolled := h.nextSave("the snapshot written when the phase rolls")
			if rolled.Mode != timer.ModeShort || rolled.Completed != 1 {
				t.Fatalf("rolled snapshot = %+v, want a finished pomodoro on a short break", rolled)
			}
			if tc.running {
				if rolled.Deadline <= started.Deadline {
					t.Fatalf("rolled deadline = %d, want the break that just started", rolled.Deadline)
				}
			} else if rolled.Deadline != 0 {
				t.Fatalf("rolled deadline = %d, want none; the break has not started", rolled.Deadline)
			}

			// A restart loads that snapshot with the clock still past the
			// finished phase. It must not complete again.
			restored := timer.NewSession(func() time.Time { return time.Unix(started.Deadline, 0) })
			restored.SetDurations(time.Minute, 5*time.Minute, 15*time.Minute)
			restored.RestoreSnapshot(rolled)
			if _, done := restored.Tick(); done {
				t.Fatal("restart completed the finished phase again")
			}
			if restored.Mode() != timer.ModeShort || restored.Completed() != 1 {
				t.Fatalf("after restart mode=%s completed=%d, want short and 1", restored.Mode(), restored.Completed())
			}
			if restored.Running() != tc.running {
				t.Fatalf("running = %v, want %v", restored.Running(), tc.running)
			}
			if restored.Remaining() != 5*time.Minute {
				t.Fatalf("remaining = %v, want the short break", restored.Remaining())
			}
		})
	}
}
