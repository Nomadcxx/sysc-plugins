package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	minidocker "github.com/Nomadcxx/sysc-plugins/plugins/mini-docker"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// fakeCLI answers instantly so the harness never touches a real daemon.
type fakeCLI struct{}

func (fakeCLI) List(context.Context) ([]minidocker.Container, int, error) {
	return []minidocker.Container{
		{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up 2 hours"},
		{ID: "b2", Names: "db", Image: "postgres:16", State: "exited", Status: "Exited (0)"},
	}, 0, nil
}

func (fakeCLI) Start(context.Context, string) error   { return nil }
func (fakeCLI) Stop(context.Context, string) error    { return nil }
func (fakeCLI) Restart(context.Context, string) error { return nil }
func (fakeCLI) Images(context.Context) ([]minidocker.Image, int, error) {
	return nil, 0, nil
}
func (fakeCLI) Volumes(context.Context) ([]minidocker.Volume, int, error) {
	return nil, 0, nil
}
func (fakeCLI) Networks(context.Context) ([]minidocker.Network, int, error) {
	return nil, 0, nil
}
func (fakeCLI) ImageExposedPorts(context.Context, string) ([]int, error) { return nil, nil }
func (fakeCLI) Remove(context.Context, string) error                     { return nil }
func (fakeCLI) Rmi(context.Context, string) error                        { return nil }
func (fakeCLI) VolRm(context.Context, string) error                      { return nil }
func (fakeCLI) NetRm(context.Context, string) error                      { return nil }
func (fakeCLI) Run(context.Context, minidocker.RunOpts) error            { return nil }

// slowCLI makes List take delay, emulating a hung docker daemon.
type slowCLI struct{ delay time.Duration }

func (s slowCLI) List(context.Context) ([]minidocker.Container, int, error) {
	time.Sleep(s.delay)
	return []minidocker.Container{
		{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up"},
	}, 0, nil
}

func (slowCLI) Start(context.Context, string) error   { return nil }
func (slowCLI) Stop(context.Context, string) error    { return nil }
func (slowCLI) Restart(context.Context, string) error { return nil }
func (slowCLI) Images(context.Context) ([]minidocker.Image, int, error) {
	return nil, 0, nil
}
func (slowCLI) Volumes(context.Context) ([]minidocker.Volume, int, error) {
	return nil, 0, nil
}
func (slowCLI) Networks(context.Context) ([]minidocker.Network, int, error) {
	return nil, 0, nil
}
func (slowCLI) ImageExposedPorts(context.Context, string) ([]int, error) { return nil, nil }
func (slowCLI) Remove(context.Context, string) error                     { return nil }
func (slowCLI) Rmi(context.Context, string) error                        { return nil }
func (slowCLI) VolRm(context.Context, string) error                      { return nil }
func (slowCLI) NetRm(context.Context, string) error                      { return nil }
func (slowCLI) Run(context.Context, minidocker.RunOpts) error            { return nil }

// host writes one framed host message as a JSON line.
type host struct{ w *bufio.Writer }

// waitFor polls cond every 20ms until it holds or the timeout passes.
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func (h host) send(m any) {
	data, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	if _, err := h.w.Write(append(data, '\n')); err != nil {
		panic(err)
	}
	h.w.Flush()
}

// TestRunConcurrentTraffic drives run() the way a real session looks: the
// poller ticks, action goroutines publish, and the main loop opens views and
// commits settings — all at once. Run with -race; any unsynchronized access
// to the views map or the settings struct fails the test.
func TestRunConcurrentTraffic(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer outR.Close()

	h := host{w: bufio.NewWriter(inW)}
	h.send(&v1.HostHello{
		Type:         v1.TypeHostHello,
		Supported:    []v1.Version{{Major: 1, Minor: 1}},
		Plugin:       v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.2.0"},
		Capabilities: []string{"panels", "settings"},
		Limits:       v1.DefaultLimits,
	})

	newSession = func() *minidocker.Session { return minidocker.NewSession(fakeCLI{}) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	// Drain everything the plugin writes; an unread pipe would block publish
	// and mask the races under test. Count snapshots as liveness evidence.
	var snapshots atomic.Int64
	go func() {
		dec := json.NewDecoder(outR)
		var m map[string]any
		for {
			if err := dec.Decode(&m); err != nil {
				return
			}
			if m["type"] == v1.TypeViewSnapshot {
				snapshots.Add(1)
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- run(inR, outW) }()

	// Open the bar view and wait for the first snapshot: the receive loop is
	// live and the plugin responds.
	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "v1", View: v1.ViewBar, Entry: "bar"})
	deadline := time.Now().Add(5 * time.Second)
	for snapshots.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshots.Load() == 0 {
		t.Fatal("no view snapshot within 5s")
	}

	// The storm: settings commits and action activations race the poller.
	for i := 0; i < 300; i++ {
		h.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin,
			Values: map[string]any{
				"refresh_interval_seconds": 1.0,
				"status_mode":              []string{"always", "running_only", "hidden"}[i%3],
			}})
		h.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "v1",
			Node: "start:a1", Event: v1.EventActivate})
		if i%10 == 0 {
			h.send(&v1.ViewClose{Type: v1.TypeViewClose, ViewID: "v1"})
			h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "v1", View: v1.ViewBar, Entry: "bar"})
		}
	}

	// Give action goroutines a beat to land their publishes, then shut down.
	time.Sleep(300 * time.Millisecond)
	h.send(&v1.HostShutdown{Type: v1.TypeHostShutdown})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit after host.shutdown")
	}
	if snapshots.Load() < 10 {
		t.Fatalf("snapshots = %d, want a steady stream under load", snapshots.Load())
	}
}

// TestRefreshClickDoesNotBlockMainLoop clicks refresh against a hung docker
// and then commits a settings change; the settings publish (the canary: the
// bar label drops the count) must land while the refresh is still in flight.
func TestRefreshClickDoesNotBlockMainLoop(t *testing.T) {
	const delay = 1500 * time.Millisecond
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer outR.Close()

	h := host{w: bufio.NewWriter(inW)}
	h.send(&v1.HostHello{
		Type:      v1.TypeHostHello,
		Supported: []v1.Version{{Major: 1, Minor: 1}},
		Plugin:    v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.2.0"},
		Limits:    v1.DefaultLimits,
	})

	newSession = func() *minidocker.Session { return minidocker.NewSession(slowCLI{delay: delay}) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	// The canary is a snapshot whose bar button text has lost the count —
	// only the committed "hidden" settings produce that. It is armed only
	// after the unambiguous "docker 1" (available + counted) snapshot, since
	// the pre-refresh unavailable bar also reads "docker".
	var armed, canary atomic.Bool
	go func() {
		dec := json.NewDecoder(outR)
		var m struct {
			Type string `json:"type"`
			Root *struct {
				Children []struct {
					Text string `json:"text"`
				} `json:"children"`
			} `json:"root"`
		}
		for {
			if err := dec.Decode(&m); err != nil {
				return
			}
			if m.Type != v1.TypeViewSnapshot || m.Root == nil || len(m.Root.Children) == 0 {
				continue
			}
			switch text := m.Root.Children[0].Text; {
			case text == "docker 1":
				armed.Store(true)
			case armed.Load() && text == "docker":
				canary.Store(true)
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- run(inR, outW) }()

	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "v1", View: v1.ViewBar, Entry: "bar"})
	// The startup poll runs in the poller goroutine and takes the full delay;
	// wait for its "docker 1" snapshot before clicking anything.
	if !waitFor(armed.Load, delay+2*time.Second) {
		t.Fatal("no available snapshot; harness did not reach the armed state")
	}

	// Refresh against the hung daemon, then the settings commit. With the
	// refresh inline on the main loop the commit waits out the full delay.
	h.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "v1", Node: "refresh", Event: v1.EventActivate})
	h.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin,
		Values: map[string]any{"status_mode": "hidden"}})

	if !waitFor(canary.Load, delay-300*time.Millisecond) {
		t.Fatal("settings commit was blocked behind the in-flight refresh")
	}

	h.send(&v1.HostShutdown{Type: v1.TypeHostShutdown})
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit after host.shutdown")
	}
}

// The handshake fallback only fires when the manifest is not beside the
// binary (go run, tests); this pins it to the manifest so a version bump
// cannot silently drift the two.
func TestHandshakeFallbackMatchesManifest(t *testing.T) {
	raw, err := os.ReadFile("../../plugins/mini-docker/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if fallbackVersion != m.Version {
		t.Fatalf("handshake fallback %q != manifest version %q", fallbackVersion, m.Version)
	}
}

// TestViewResyncResetsRevisionAndRepublishes pins the host's resync contract:
// when the host drops or rejects a view it asks for a fresh snapshot, and the
// plugin must answer with one whose revision restarts — the host takes the next
// snapshot as a new base. Revisions otherwise only climb, so a second snapshot
// carrying revision 1 is proof the counter was reset for this view.
func TestViewResyncResetsRevisionAndRepublishes(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer outR.Close()

	h := host{w: bufio.NewWriter(inW)}
	h.send(&v1.HostHello{
		Type:      v1.TypeHostHello,
		Supported: []v1.Version{{Major: 1, Minor: 1}},
		Plugin:    v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.3.0"},
		Limits:    v1.DefaultLimits,
	})

	newSession = func() *minidocker.Session { return minidocker.NewSession(fakeCLI{}) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	// Snapshots arrive on one pipe for the whole run; counting the revision
	// values is enough to prove the reset, and atomics keep the drain
	// goroutine out of the test's way.
	var firstRevision, secondRevision atomic.Int64
	go func() {
		dec := json.NewDecoder(outR)
		var m struct {
			Type     string `json:"type"`
			ViewID   string `json:"view_id"`
			Revision uint64 `json:"revision"`
		}
		for {
			if err := dec.Decode(&m); err != nil {
				return
			}
			if m.Type != v1.TypeViewSnapshot || m.ViewID != "v1" {
				continue
			}
			switch m.Revision {
			case 1:
				firstRevision.Add(1)
			case 2:
				secondRevision.Add(1)
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- run(inR, outW) }()

	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "v1", View: v1.ViewBar, Entry: "bar"})
	if !waitFor(func() bool { return firstRevision.Load() >= 1 }, 5*time.Second) {
		t.Fatal("no first snapshot within 5s")
	}
	// A settings commit forces a second publish; the bar revision climbs.
	h.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin,
		Values: map[string]any{"status_mode": "running_only"}})
	if !waitFor(func() bool { return secondRevision.Load() >= 1 }, 5*time.Second) {
		t.Fatal("settings commit produced no second snapshot")
	}

	// The host rejected or dropped the view and asks for a fresh base.
	h.send(&v1.ViewResync{Type: v1.TypeViewResync, ViewID: "v1"})
	if !waitFor(func() bool { return firstRevision.Load() >= 2 }, 5*time.Second) {
		t.Fatal("view.resync ignored: no snapshot restarted at revision 1")
	}

	h.send(&v1.HostShutdown{Type: v1.TypeHostShutdown})
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit after host.shutdown")
	}
}
