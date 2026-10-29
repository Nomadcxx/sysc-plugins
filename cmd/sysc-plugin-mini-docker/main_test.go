package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	minidocker "github.com/Nomadcxx/sysc-plugins/plugins/mini-docker"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// fakeCLI answers instantly so the harness never touches a real daemon.
type fakeCLI struct{}

type pagedCLI struct {
	fakeCLI
	containers []minidocker.Container
}

func (c pagedCLI) List(context.Context) ([]minidocker.Container, int, error) {
	return c.containers, 0, nil
}

func eventStreamUntilCancelled(ctx context.Context) <-chan struct{} {
	events := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(events)
	}()
	return events
}

func (fakeCLI) List(context.Context) ([]minidocker.Container, int, error) {
	return []minidocker.Container{
		{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up 2 hours"},
		{ID: "b2", Names: "db", Image: "postgres:16", State: "exited", Status: "Exited (0)"},
	}, 0, nil
}

func (fakeCLI) Events(ctx context.Context) (<-chan struct{}, error) {
	return eventStreamUntilCancelled(ctx), nil
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

// flakyCLI fails List once armed, so the bar's availability tone flips and
// the next publish carries a new digest.
type flakyCLI struct {
	fakeCLI
	fail atomic.Bool
}

func (c *flakyCLI) List(context.Context) ([]minidocker.Container, int, error) {
	if c.fail.Load() {
		return nil, 0, errors.New("Docker daemon not running")
	}
	return c.fakeCLI.List(context.Background())
}

// slowCLI makes List take delay, emulating a hung docker daemon.
type slowCLI struct{ delay time.Duration }

func (s slowCLI) List(context.Context) ([]minidocker.Container, int, error) {
	time.Sleep(s.delay)
	return []minidocker.Container{
		{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up"},
	}, 0, nil
}

func (slowCLI) Events(ctx context.Context) (<-chan struct{}, error) {
	return eventStreamUntilCancelled(ctx), nil
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
		Supported:    []v1.Version{{Major: 1, Minor: 4}},
		Plugin:       v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.4.0"},
		Capabilities: []string{"panels", "settings"},
		Limits:       v1.DefaultLimits,
	})

	newSession = func() *minidocker.Session { return minidocker.NewSession(fakeCLI{}) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	// Drain everything the plugin writes; an unread pipe would block publish
	// and mask the races under test. Count snapshots as liveness evidence.
	var snapshots atomic.Int64
	var latestRevision atomic.Uint64
	go func() {
		dec := json.NewDecoder(outR)
		var m map[string]any
		for {
			if err := dec.Decode(&m); err != nil {
				return
			}
			if m["type"] == v1.TypeViewSnapshot {
				snapshots.Add(1)
				if revision, ok := m["revision"].(float64); ok {
					latestRevision.Store(uint64(revision))
				}
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
			}})
		h.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "v1",
			Revision: latestRevision.Load(), Node: "start:a1", Event: v1.EventActivate})
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
// and then opens a second view; the second view's snapshot (the canary) must
// land while the refresh is still in flight.
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
		Supported: []v1.Version{{Major: 1, Minor: 4}},
		Plugin:    v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.4.0"},
		Limits:    v1.DefaultLimits,
	})

	newSession = func() *minidocker.Session { return minidocker.NewSession(slowCLI{delay: delay}) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	// The canary is the snapshot for a second view: opening a view forces a
	// publish, so it can only land once the main loop is free. It is armed
	// only after the first view's snapshot, since the pre-refresh bar also
	// publishes.
	var armed, canary atomic.Bool
	var latestRevision atomic.Uint64
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
			if m.Type != v1.TypeViewSnapshot {
				continue
			}
			latestRevision.Store(m.Revision)
			switch m.ViewID {
			case "v1":
				armed.Store(true)
			case "v2":
				canary.Store(true)
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- run(inR, outW) }()

	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "v1", View: v1.ViewBar, Entry: "bar"})
	// The startup poll runs in the poller goroutine and takes the full delay;
	// wait for its first snapshot before clicking anything.
	if !waitFor(armed.Load, delay+2*time.Second) {
		t.Fatal("no available snapshot; harness did not reach the armed state")
	}

	// Refresh against the hung daemon, then open a second view. With the
	// refresh inline on the main loop the open waits out the full delay.
	h.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "v1", Revision: latestRevision.Load(),
		Node: "refresh", Event: v1.EventActivate})
	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "v2", View: v1.ViewBar, Entry: "bar"})

	if !waitFor(canary.Load, delay-300*time.Millisecond) {
		t.Fatal("view open was blocked behind the in-flight refresh")
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

type shutdownCLI struct {
	fakeCLI
	started  chan struct{}
	canceled chan struct{}
	release  <-chan struct{}
}

func (c shutdownCLI) List(ctx context.Context) ([]minidocker.Container, int, error) {
	close(c.started)
	<-ctx.Done()
	close(c.canceled)
	<-c.release
	return nil, 0, ctx.Err()
}

func TestRunWaitsForRefreshBeforeReturningOnShutdown(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer inW.Close()
	defer outR.Close()
	defer outW.Close()
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	h := host{w: bufio.NewWriter(inW)}
	h.send(&v1.HostHello{
		Type:      v1.TypeHostHello,
		Supported: []v1.Version{{Major: 1, Minor: 4}},
		Plugin:    v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.4.0"},
		Limits:    v1.DefaultLimits,
	})

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	cli := shutdownCLI{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  release,
	}
	newSession = func() *minidocker.Session { return minidocker.NewSession(cli) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	errCh := make(chan error, 1)
	go func() { errCh <- run(inR, outW) }()
	select {
	case <-cli.started:
	case <-time.After(5 * time.Second):
		t.Fatal("initial refresh did not start")
	}

	h.send(&v1.HostShutdown{Type: v1.TypeHostShutdown})
	select {
	case <-cli.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the initial refresh")
	}
	select {
	case err := <-errCh:
		t.Fatalf("run returned before the refresh worker exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	unblock()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the refresh worker exited")
	}
}

func TestWatchDockerEventStreamWaitsForProcessExitAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan struct{}, 1)
	events <- struct{}{}
	enqueued := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		watchDockerEventStream(ctx, events, func(refreshRequest) { enqueued <- struct{}{} })
		close(done)
	}()

	select {
	case <-enqueued:
	case <-time.After(5 * time.Second):
		t.Fatal("event watcher did not consume the stream event")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("event watcher returned before its stream closed")
	case <-time.After(100 * time.Millisecond):
	}

	close(events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event watcher did not return after its stream closed")
	}
}

func TestPanelOpenDoesNotWedgeBehindFullInputQueue(t *testing.T) {
	restoreTimeout := hostcall.SetTimeout(750 * time.Millisecond)
	defer restoreTimeout()

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
		Supported: []v1.Version{{Major: 1, Minor: 4}},
		Plugin:    v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.4.0"},
		Limits:    v1.DefaultLimits,
	})
	newSession = func() *minidocker.Session { return minidocker.NewSession(fakeCLI{}) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	pluginHello := make(chan struct{}, 1)
	snapshots := make(chan string, 4)
	panelCalls := make(chan *v1.HostCall, 1)
	go func() {
		dec := v1.NewDecoder(outR, v1.ToHost)
		for {
			msg, err := dec.Decode()
			if err != nil {
				return
			}
			switch msg := msg.(type) {
			case *v1.PluginHello:
				pluginHello <- struct{}{}
			case *v1.ViewSnapshot:
				if msg.ViewID == "canary" {
					select {
					case snapshots <- msg.ViewID:
					default:
					}
				} else if msg.ViewID == "bar" {
					select {
					case snapshots <- msg.ViewID:
					default:
					}
				}
			case *v1.HostCall:
				if msg.Call == v1.CallPanelOpen {
					panelCalls <- msg
				}
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- run(inR, outW) }()
	select {
	case <-pluginHello:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin handshake timed out")
	}

	var pendingCallID string
	defer func() {
		if pendingCallID != "" {
			h.send(&v1.HostReply{ID: pendingCallID, OK: true, Result: json.RawMessage(`null`)})
		}
		h.send(&v1.HostShutdown{Type: v1.TypeHostShutdown})
		_ = inW.Close()
		_ = outW.Close()
		select {
		case <-errCh:
		case <-time.After(3 * time.Second):
			t.Error("run did not stop after host.shutdown")
		}
	}()

	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "bar", View: v1.ViewBar, Entry: "bar"})
	select {
	case <-snapshots:
	case <-time.After(2 * time.Second):
		t.Fatal("bar snapshot timed out")
	}
	h.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "bar", Revision: 1, Node: "open", Event: v1.EventActivate})
	var panelCall *v1.HostCall
	select {
	case panelCall = <-panelCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("bar click did not call panel.open")
	}
	pendingCallID = panelCall.ID

	// Fill the bounded incoming queue before the delayed reply. The reply then
	// sits behind these messages until the event loop keeps making progress.
	for i := 0; i < 20; i++ {
		h.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Values: map[string]any{}})
	}
	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "canary", View: v1.ViewBar, Entry: "bar"})

	gotCanary := false
	select {
	case <-snapshots:
		gotCanary = true
	case <-time.After(300 * time.Millisecond):
	}
	h.send(&v1.HostReply{ID: panelCall.ID, OK: true, Result: json.RawMessage(`null`)})
	pendingCallID = ""
	if !gotCanary {
		t.Fatal("panel.open reply was delayed behind a full input queue")
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
		Supported: []v1.Version{{Major: 1, Minor: 4}},
		Plugin:    v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.4.0"},
		Limits:    v1.DefaultLimits,
	})

	cli := &flakyCLI{}
	newSession = func() *minidocker.Session { return minidocker.NewSession(cli) }
	defer func() { newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) } }()

	// Snapshots arrive on one pipe for the whole run; counting the revision
	// values is enough to prove the reset, and atomics keep the drain
	// goroutine out of the test's way.
	var firstRevision, secondRevision atomic.Int64
	var latest atomic.Uint64
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
			latest.Store(m.Revision)
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

	h.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "v1", View: v1.ViewPanel, Entry: "panel"})
	if !waitFor(func() bool { return firstRevision.Load() >= 1 }, 5*time.Second) {
		t.Fatal("no first snapshot within 5s")
	}
	// The daemon goes down: an explicit panel refresh changes the state, so the
	// snapshot digest changes and the revision climbs.
	cli.fail.Store(true)
	h.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "v1", Revision: latest.Load(),
		Node: "refresh", Event: v1.EventActivate})
	if !waitFor(func() bool { return secondRevision.Load() >= 1 }, 5*time.Second) {
		t.Fatal("refresh produced no second snapshot")
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

type interactionCLI struct {
	listCalls    atomic.Int64
	imageCalls   atomic.Int64
	networkCalls atomic.Int64
	runs         chan minidocker.RunOpts
}

func (c *interactionCLI) List(context.Context) ([]minidocker.Container, int, error) {
	c.listCalls.Add(1)
	return []minidocker.Container{{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up"}}, 0, nil
}

func (*interactionCLI) Events(ctx context.Context) (<-chan struct{}, error) {
	return eventStreamUntilCancelled(ctx), nil
}

func (*interactionCLI) Start(context.Context, string) error   { return nil }
func (*interactionCLI) Stop(context.Context, string) error    { return nil }
func (*interactionCLI) Restart(context.Context, string) error { return nil }
func (c *interactionCLI) Images(context.Context) ([]minidocker.Image, int, error) {
	c.imageCalls.Add(1)
	return []minidocker.Image{{ID: "image1", Repository: "alpine", Tag: "3"}}, 0, nil
}
func (*interactionCLI) Volumes(context.Context) ([]minidocker.Volume, int, error) {
	return nil, 0, nil
}
func (c *interactionCLI) Networks(context.Context) ([]minidocker.Network, int, error) {
	c.networkCalls.Add(1)
	return []minidocker.Network{
		{Name: "bridge", ID: "network1"},
		{Name: "custom", ID: "network2"},
	}, 0, nil
}
func (*interactionCLI) ImageExposedPorts(context.Context, string) ([]int, error) { return nil, nil }
func (*interactionCLI) Remove(context.Context, string) error                     { return nil }
func (*interactionCLI) Rmi(context.Context, string) error                        { return nil }
func (*interactionCLI) VolRm(context.Context, string) error                      { return nil }
func (*interactionCLI) NetRm(context.Context, string) error                      { return nil }
func (c *interactionCLI) Run(_ context.Context, opts minidocker.RunOpts) error {
	if c.runs != nil {
		c.runs <- opts
	}
	return nil
}

type dockerEventsCLI struct {
	*interactionCLI
	events     <-chan struct{}
	eventCalls atomic.Int64
}

func (c *dockerEventsCLI) Events(ctx context.Context) (<-chan struct{}, error) {
	c.eventCalls.Add(1)
	events := make(chan struct{}, 1)
	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-c.events:
				if !ok {
					return
				}
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()
	return events, nil
}

type closedDockerEventsCLI struct {
	*interactionCLI
	eventCalls atomic.Int64
}

func (c *closedDockerEventsCLI) Events(context.Context) (<-chan struct{}, error) {
	c.eventCalls.Add(1)
	events := make(chan struct{})
	close(events)
	return events, nil
}

type blockedActionCLI struct {
	*blockedRefreshCLI
	entered chan struct{}
	release <-chan struct{}
}

type blockedRefreshCLI struct {
	*interactionCLI
	blockAfter atomic.Int64
	completed  atomic.Int64
	blocked    atomic.Bool
	entered    chan struct{}
	release    <-chan struct{}
	stopped    bool
}

func (c *blockedRefreshCLI) List(context.Context) ([]minidocker.Container, int, error) {
	call := c.listCalls.Add(1)
	if threshold := c.blockAfter.Load(); threshold > 0 && call >= threshold && c.blocked.CompareAndSwap(false, true) {
		c.entered <- struct{}{}
		<-c.release
	}
	c.completed.Add(1)
	state, status := "running", "Up"
	containers := []minidocker.Container{{ID: "a1", Names: "web", Image: "nginx:latest", State: state, Status: status}}
	if c.stopped {
		containers[0].State, containers[0].Status = "exited", "Exited (0)"
		containers = append(containers, minidocker.Container{ID: "b2", Names: "worker", Image: "busybox:latest", State: "exited", Status: "Exited (0)"})
	}
	return containers, 0, nil
}

func (c *blockedActionCLI) Start(context.Context, string) error {
	c.entered <- struct{}{}
	<-c.release
	return nil
}

type wireSnapshot struct {
	Type     string   `json:"type"`
	ViewID   string   `json:"view_id"`
	Revision uint64   `json:"revision"`
	Root     *v1.Node `json:"root"`
}

type pluginHarness struct {
	host      host
	inW       *os.File
	outW      *os.File
	snapshots chan wireSnapshot
	done      chan error
	stopOnce  sync.Once
}

func startPluginHarness(t *testing.T, docker minidocker.Docker) *pluginHarness {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	h := &pluginHarness{
		host: host{w: bufio.NewWriter(inW)}, inW: inW, outW: outW,
		snapshots: make(chan wireSnapshot, 256), done: make(chan error, 1),
	}
	h.host.send(&v1.HostHello{
		Type: v1.TypeHostHello, Supported: []v1.Version{{Major: 1, Minor: 4}},
		Plugin:       v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: "0.4.0"},
		Capabilities: []string{"panels", "settings"}, Limits: v1.DefaultLimits,
	})
	previous := newSession
	newSession = func() *minidocker.Session { return minidocker.NewSession(docker) }
	go func() { h.done <- run(inR, outW) }()
	go func() {
		defer close(h.snapshots)
		dec := json.NewDecoder(outR)
		for {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return
			}
			var envelope struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &envelope) != nil || envelope.Type != v1.TypeViewSnapshot {
				continue
			}
			var snapshot wireSnapshot
			if json.Unmarshal(raw, &snapshot) == nil {
				h.snapshots <- snapshot
			}
		}
	}()
	t.Cleanup(func() {
		h.stop()
		newSession = previous
		_ = inR.Close()
		_ = outR.Close()
	})
	return h
}

func (h *pluginHarness) stop() {
	h.stopOnce.Do(func() {
		h.host.send(&v1.HostShutdown{Type: v1.TypeHostShutdown})
		_ = h.inW.Close()
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
		}
		_ = h.outW.Close()
	})
}

func (h *pluginHarness) nextSnapshot(t *testing.T, viewID string, match func(wireSnapshot) bool) wireSnapshot {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case snapshot, ok := <-h.snapshots:
			if !ok {
				t.Fatal("plugin output closed before expected snapshot")
			}
			if snapshot.ViewID == viewID && match(snapshot) {
				return snapshot
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for view %s snapshot", viewID)
		}
	}
}

func (h *pluginHarness) settledSnapshot(t *testing.T, viewID string, match func(wireSnapshot) bool) wireSnapshot {
	t.Helper()
	latest := h.nextSnapshot(t, viewID, match)
	quiet := time.NewTimer(100 * time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case snapshot, ok := <-h.snapshots:
			if !ok {
				t.Fatal("plugin output closed before the view settled")
			}
			if snapshot.ViewID == viewID && match(snapshot) {
				latest = snapshot
			}
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(100 * time.Millisecond)
		case <-quiet.C:
			return latest
		}
	}
}

func nodeByID(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := nodeByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func treeHasText(root *v1.Node, text string) bool {
	if root == nil {
		return false
	}
	if root.Text == text {
		return true
	}
	for _, child := range root.Children {
		if treeHasText(child, text) {
			return true
		}
	}
	return false
}

func TestRunRoutesPaginationControls(t *testing.T) {
	containers := make([]minidocker.Container, 243)
	for i := range containers {
		containers[i] = minidocker.Container{
			ID: fmt.Sprintf("c%03d", i), Names: fmt.Sprintf("container-%03d", i), State: "exited",
		}
	}
	h := startPluginHarness(t, pagedCLI{containers: containers})
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "panel", View: v1.ViewPanel, Entry: "panel"})
	first := h.settledSnapshot(t, "panel", func(s wireSnapshot) bool {
		return treeHasText(s.Root, "Items 1–240 of 243") && !treeHasText(s.Root, "Refreshing…")
	})
	if button := nodeByID(first.Root, "page:previous"); button == nil || !button.Disabled {
		t.Fatalf("previous control on first page = %+v", button)
	}
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: first.Revision,
		Node: "page:next", Event: v1.EventActivate})
	last := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		return treeHasText(s.Root, "Items 241–243 of 243")
	})
	if row := nodeByID(last.Root, "select:c240"); row == nil {
		t.Fatal("page-next did not make the remaining Docker row reachable")
	}
	if button := nodeByID(last.Root, "page:next"); button == nil || !button.Disabled {
		t.Fatalf("next control on last page = %+v", button)
	}
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: last.Revision,
		Node: "page:previous", Event: v1.EventActivate})
	h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		return treeHasText(s.Root, "Items 1–240 of 243")
	})
}

func TestRunRoutesTextInputAndRejectsStaleRevision(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	cli := &blockedRefreshCLI{
		interactionCLI: &interactionCLI{}, blockAfter: atomic.Int64{}, entered: make(chan struct{}, 1), release: release,
	}
	cli.blockAfter.Store(2)
	h := startPluginHarness(t, cli)
	t.Cleanup(finish)
	h.host.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin,
		Values: map[string]any{"default_network": "custom"}})
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "panel", View: v1.ViewPanel, Entry: "panel"})
	select {
	case <-cli.entered:
	case <-time.After(time.Second):
		t.Fatal("panel-open refresh did not reach Docker")
	}
	panel := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		return nodeByID(s.Root, "select:a1") != nil && treeHasText(s.Root, "Refreshing…")
	})
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: panel.Revision,
		Node: "tab:images", Event: v1.EventActivate})
	imageSnapshot := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		return treeHasText(s.Root, "Docker images")
	})
	finish()
	imageSnapshot = h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return nodeByID(s.Root, "select:alpine:3") != nil })
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: imageSnapshot.Revision,
		Node: "select:alpine:3", Event: v1.EventActivate})
	selected := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return nodeByID(s.Root, "run:alpine:3") != nil })
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: selected.Revision,
		Node: "run:alpine:3", Event: v1.EventActivate})
	form := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return nodeByID(s.Root, "name") != nil })
	if got := nodeByID(form.Root, "form:network"); got == nil || got.Text != "Network: custom" {
		t.Fatalf("default network not applied to run form: %+v", got)
	}
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: form.Revision,
		Node: "name", Event: v1.EventChange, Text: "from-host"})
	updated := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		node := nodeByID(s.Root, "name")
		return node != nil && node.Text == "from-host"
	})
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: updated.Revision - 1,
		Node: "name", Event: v1.EventChange, Text: "stale"})
	h.host.send(&v1.ViewResync{Type: v1.TypeViewResync, ViewID: "panel"})
	resynced := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return s.Revision == 1 })
	if got := nodeByID(resynced.Root, "name"); got == nil || got.Text != "from-host" {
		t.Fatalf("stale revision changed the retained draft: %+v", got)
	}
}

func TestRunPublishesChangedTreesPerViewAndForcesOpenAndResync(t *testing.T) {
	h := startPluginHarness(t, &interactionCLI{})
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "first", View: v1.ViewBar, Entry: "bar"})
	first := h.settledSnapshot(t, "first", func(s wireSnapshot) bool {
		node := nodeByID(s.Root, "open")
		return node != nil && node.Icon == "docker"
	})
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "first", Revision: first.Revision,
		Node: "refresh", Event: v1.EventActivate})
	select {
	case snapshot := <-h.snapshots:
		t.Fatalf("unchanged view was republished: %+v", snapshot)
	case <-time.After(120 * time.Millisecond):
	}
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "second", View: v1.ViewBar, Entry: "bar"})
	second := h.nextSnapshot(t, "second", func(s wireSnapshot) bool { return nodeByID(s.Root, "open") != nil })
	if second.Revision != 1 {
		t.Fatalf("new view revision = %d, want 1", second.Revision)
	}
	h.host.send(&v1.ViewResync{Type: v1.TypeViewResync, ViewID: "first"})
	resynced := h.nextSnapshot(t, "first", func(s wireSnapshot) bool { return s.Revision == 1 })
	if resynced.Revision != 1 || nodeByID(resynced.Root, "open") == nil || first.Revision == 0 {
		t.Fatalf("forced resync snapshot = %+v", resynced)
	}
}

func TestRunResetsPollTimerWhenIntervalChanges(t *testing.T) {
	cli := &interactionCLI{}
	h := startPluginHarness(t, cli)
	if !waitFor(func() bool { return cli.listCalls.Load() >= 1 }, 2*time.Second) {
		t.Fatal("startup poll did not run")
	}
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "panel", View: v1.ViewPanel, Entry: "panel"})
	if !waitFor(func() bool { return cli.listCalls.Load() >= 2 }, time.Second) {
		t.Fatal("panel-open refresh did not run")
	}
	h.host.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin,
		Values: map[string]any{"refresh_interval_seconds": 1.0}})
	baseline := cli.listCalls.Load()
	if !waitFor(func() bool { return cli.listCalls.Load() > baseline }, 1800*time.Millisecond) {
		t.Fatal("poll timer kept the old five-second interval after the setting changed")
	}
}

func TestRunDoesNotPollAtPanelCadenceWithOnlyBarOpen(t *testing.T) {
	cli := &interactionCLI{}
	h := startPluginHarness(t, cli)
	if !waitFor(func() bool { return cli.listCalls.Load() >= 1 }, 2*time.Second) {
		t.Fatal("startup poll did not run")
	}
	h.host.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin,
		Values: map[string]any{"refresh_interval_seconds": 1.0}})
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "bar", View: v1.ViewBar, Entry: "bar"})
	h.nextSnapshot(t, "bar", func(s wireSnapshot) bool { return nodeByID(s.Root, "open") != nil })
	baseline := cli.listCalls.Load()
	time.Sleep(1200 * time.Millisecond)
	if got := cli.listCalls.Load(); got != baseline {
		t.Fatalf("container polls with only the bar open = %d, want no panel-cadence poll", got-baseline)
	}
}

func TestRunUsesIdleCadenceAfterLastPanelCloses(t *testing.T) {
	cli := &interactionCLI{}
	h := startPluginHarness(t, cli)
	if !waitFor(func() bool { return cli.listCalls.Load() >= 1 }, 2*time.Second) {
		t.Fatal("startup poll did not run")
	}
	h.host.send(&v1.SettingsChanged{Type: v1.TypeSettingsChanged, Scope: v1.ScopePlugin,
		Values: map[string]any{"refresh_interval_seconds": 1.0}})
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "panel", View: v1.ViewPanel, Entry: "panel"})
	h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return nodeByID(s.Root, "select:a1") != nil })
	if !waitFor(func() bool { return cli.listCalls.Load() >= 2 }, time.Second) {
		t.Fatal("panel-open refresh did not run")
	}
	baseline := cli.listCalls.Load()
	h.host.send(&v1.ViewClose{Type: v1.TypeViewClose, ViewID: "panel"})
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "bar", View: v1.ViewBar, Entry: "bar"})
	h.nextSnapshot(t, "bar", func(s wireSnapshot) bool { return nodeByID(s.Root, "open") != nil })
	time.Sleep(1200 * time.Millisecond)
	if got := cli.listCalls.Load(); got != baseline {
		t.Fatalf("container polls after the last panel closed = %d, want no panel-cadence poll", got-baseline)
	}
}

func TestRunRefreshesContainerListFromDockerEvents(t *testing.T) {
	events := make(chan struct{}, 64)
	cli := &dockerEventsCLI{interactionCLI: &interactionCLI{}, events: events}
	startPluginHarness(t, cli)
	if !waitFor(func() bool { return cli.interactionCLI.listCalls.Load() >= 1 }, 2*time.Second) {
		t.Fatal("startup poll did not run")
	}
	if !waitFor(func() bool { return cli.eventCalls.Load() == 1 }, time.Second) {
		t.Fatal("Docker event stream did not start")
	}
	baseline := cli.interactionCLI.listCalls.Load()
	for range 50 {
		events <- struct{}{}
	}
	if !waitFor(func() bool { return cli.interactionCLI.listCalls.Load() > baseline }, 2*time.Second) {
		t.Fatal("container event did not refresh the list")
	}
	time.Sleep(300 * time.Millisecond)
	if got := cli.interactionCLI.listCalls.Load() - baseline; got > 2 {
		t.Fatalf("container polls for one event burst = %d, want at most 2", got)
	}
}

func TestRunRetriesClosedDockerEventStreamWithBackoff(t *testing.T) {
	cli := &closedDockerEventsCLI{interactionCLI: &interactionCLI{}}
	startPluginHarness(t, cli)
	if !waitFor(func() bool { return cli.eventCalls.Load() == 1 }, time.Second) {
		t.Fatal("Docker event stream did not start")
	}
	time.Sleep(100 * time.Millisecond)
	if got := cli.eventCalls.Load(); got != 1 {
		t.Fatalf("event stream retries before backoff = %d, want 1", got)
	}
	if !waitFor(func() bool { return cli.eventCalls.Load() >= 2 }, 1500*time.Millisecond) {
		t.Fatal("closed Docker event stream was not retried")
	}
	time.Sleep(100 * time.Millisecond)
	if got := cli.eventCalls.Load(); got != 2 {
		t.Fatalf("event stream retries before the next backoff = %d, want 2", got)
	}
}

func TestRunPublishesInFlightActionBeforeDockerCompletes(t *testing.T) {
	refreshRelease := make(chan struct{})
	var refreshReleaseOnce sync.Once
	finishRefresh := func() { refreshReleaseOnce.Do(func() { close(refreshRelease) }) }
	actionRelease := make(chan struct{})
	var actionReleaseOnce sync.Once
	finishAction := func() { actionReleaseOnce.Do(func() { close(actionRelease) }) }
	base := &blockedRefreshCLI{
		interactionCLI: &interactionCLI{}, blockAfter: atomic.Int64{}, entered: make(chan struct{}, 1),
		release: refreshRelease, stopped: true,
	}
	base.blockAfter.Store(2)
	cli := &blockedActionCLI{
		blockedRefreshCLI: base, entered: make(chan struct{}, 1), release: actionRelease,
	}
	h := startPluginHarness(t, cli)
	t.Cleanup(finishRefresh)
	t.Cleanup(finishAction)
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "panel", View: v1.ViewPanel, Entry: "panel"})
	select {
	case <-base.entered:
	case <-time.After(time.Second):
		t.Fatalf("panel-open refresh did not reach Docker (calls=%d)", base.listCalls.Load())
	}
	state := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		return nodeByID(s.Root, "select:a1") != nil && treeHasText(s.Root, "Refreshing…")
	})
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: state.Revision,
		Node: "select:a1", Event: v1.EventActivate})
	selected := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return nodeByID(s.Root, "start:a1") != nil })
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: selected.Revision,
		Node: "start:a1", Event: v1.EventActivate})
	select {
	case <-cli.entered:
	case <-time.After(time.Second):
		t.Fatal("Docker start did not begin")
	}
	busy := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		node := nodeByID(s.Root, "start:a1")
		return node != nil && node.Disabled
	})
	if tab := nodeByID(busy.Root, "tab:images"); tab == nil || tab.Disabled {
		t.Fatalf("unrelated control disabled during action: %+v", tab)
	}
	finishAction()
	finishRefresh()
}

func TestRunShowsRefreshingWithLastSnapshotDuringRefresh(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	cli := &blockedRefreshCLI{
		interactionCLI: &interactionCLI{}, entered: make(chan struct{}, 1), release: release,
	}
	h := startPluginHarness(t, cli)
	t.Cleanup(finish)
	h.host.send(&v1.ViewOpen{Type: v1.TypeViewOpen, ViewID: "panel", View: v1.ViewPanel, Entry: "panel"})
	h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return nodeByID(s.Root, "select:a1") != nil })
	if !waitFor(func() bool { return cli.completed.Load() >= 2 }, time.Second) {
		t.Fatal("panel-open refresh did not finish")
	}
	h.host.send(&v1.ViewResync{Type: v1.TypeViewResync, ViewID: "panel"})
	state := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool {
		return s.Revision == 1 && nodeByID(s.Root, "select:a1") != nil && !treeHasText(s.Root, "Refreshing…")
	})
	cli.blockAfter.Store(cli.listCalls.Load() + 1)
	h.host.send(&v1.InputEvent{Type: v1.TypeInputEvent, ViewID: "panel", Revision: state.Revision,
		Node: "refresh", Event: v1.EventActivate})
	select {
	case <-cli.entered:
	case <-time.After(time.Second):
		t.Fatal("explicit refresh did not reach Docker")
	}
	refreshing := h.nextSnapshot(t, "panel", func(s wireSnapshot) bool { return treeHasText(s.Root, "Refreshing…") })
	if nodeByID(refreshing.Root, "select:a1") == nil {
		t.Fatal("refresh blanked the last successful container snapshot")
	}
	finish()
}

func TestTooltipIncludesDockerFailureDiagnosis(t *testing.T) {
	state := minidocker.SessionSnapshot{
		ContainerTab: minidocker.TabStatus{ListError: "Docker daemon not running"},
	}
	root := viewTree(v1.ViewTooltip, state, 0)
	if !treeHasText(root, "Docker daemon not running") {
		t.Fatalf("tooltip omitted the Docker diagnosis: %+v", root)
	}
}
