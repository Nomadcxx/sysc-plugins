// Command sysc-plugin-cat is the Cat plugin: a bar cat whose behaviour
// follows CPU load.
//
// The plugin never animates a pose. It publishes what the cat is doing -- an
// act's pose list and how long one pass takes -- and the shell steps through
// the poses on its own frame clock. Messages go out when a sample lands, when
// the cat changes act, and for the few retargets that ease a change of
// speed. With no view open it holds no timers at all.
package main

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/cat"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

// lockedWriter serialises whole messages: the encoder writes one message
// per Write, and host calls go out from their own goroutines so a reply the
// event loop is not waiting on can never stall it.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// firstSample is how soon after the first view opens the cat takes its
// first real reading, so it does not sit on "Measuring" for a whole period.
const firstSample = 250 * time.Millisecond

type view struct {
	kind     v1.ViewKind
	rev      uint64
	instance string
	height   int
}

type plugin struct {
	c        *v1.Client
	ctx      context.Context
	now      func() time.Time
	cat      *cat.Cat
	settings cat.Settings
	sampler  *cat.Sampler
	history  cat.History
	views    map[string]*view

	sample *time.Timer
	act    *time.Timer
	// graph is reused by every panel snapshot.
	graph []float64
}

func run(in io.Reader, out io.Writer) error {
	c := v1.NewClient(in, &lockedWriter{w: out})
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.cat", Name: "Cat", Version: "1.0.0"})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	settings := cat.DefaultSettings()
	p := &plugin{
		c:        c,
		ctx:      ctx,
		now:      time.Now,
		cat:      cat.New(settings.Bands, settings.NapAfter, uint64(time.Now().UnixNano())),
		settings: settings,
		views:    map[string]*view{},
		sample:   stoppedTimer(),
		act:      stoppedTimer(),
		graph:    make([]float64, 0, cat.HistoryLen),
	}
	sampler, err := cat.OpenSampler(cat.ProcStat)
	if err != nil {
		_ = c.Send(&v1.PluginStatus{State: v1.StatusError, Message: "cannot read CPU load: " + err.Error()})
	} else {
		p.sampler = sampler
		defer sampler.Close()
	}

	incoming := make(chan v1.Message, 16)
	go func() {
		defer cancel()
		for {
			msg, err := c.Recv()
			if err != nil {
				return
			}
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.sample.C:
			p.takeSample()
		case <-p.act.C:
			if p.cat.Tick(p.now()) {
				p.snapshotAll()
			}
			p.schedule()
		case msg := <-incoming:
			if !p.handle(msg) {
				return nil
			}
		}
	}
}

func stoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return t
}

// handle applies one host message. It returns false on shutdown.
func (p *plugin) handle(msg v1.Message) bool {
	switch m := msg.(type) {
	case *v1.HostShutdown:
		return false
	case *v1.ViewOpen:
		wasIdle := len(p.views) == 0
		v := &view{kind: m.View, instance: m.Instance, height: m.Height}
		p.views[m.ViewID] = v
		p.snapshot(m.ViewID, v)
		if wasIdle {
			p.wake()
		}
	case *v1.ViewClose:
		delete(p.views, m.ViewID)
		if len(p.views) == 0 {
			// Nobody can see the cat: hold no timers at all.
			p.sample.Stop()
			p.act.Stop()
		}
	case *v1.ViewResync:
		if v, ok := p.views[m.ViewID]; ok {
			p.snapshot(m.ViewID, v)
		}
	case *v1.InputEvent:
		p.input(m)
	case *v1.SettingsChanged:
		if m.Scope != v1.ScopePlugin {
			return true
		}
		prev := p.settings.SampleEvery
		p.settings = cat.ParseSettings(m.Values)
		p.cat.SetThresholds(p.settings.Bands, p.settings.NapAfter, p.now())
		if p.settings.SampleEvery != prev && len(p.views) > 0 {
			p.sample.Reset(p.settings.SampleEvery)
		}
		p.snapshotAll()
		p.schedule()
	}
	return true
}

// wake starts the timers when the first view opens. An idle act that fell
// due while nobody was watching plays out straight away.
func (p *plugin) wake() {
	if p.sampler != nil {
		if _, known := p.cat.Load(); known {
			p.sample.Reset(p.settings.SampleEvery)
		} else {
			p.sample.Reset(firstSample)
		}
	}
	p.schedule()
}

// schedule arms the behaviour timer for the cat's next due change.
func (p *plugin) schedule() {
	if len(p.views) == 0 {
		p.act.Stop()
		return
	}
	if d := p.cat.Next(p.now()); d > 0 {
		p.act.Reset(d)
		return
	}
	p.act.Stop()
}

func (p *plugin) input(m *v1.InputEvent) {
	var (
		kind   v1.CallKind
		params v1.PanelParams
	)
	switch m.Node {
	case "open":
		kind = v1.CallPanelOpen
		params = v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation}
		if v, ok := p.views[m.ViewID]; ok {
			params.Instance = v.instance
		}
	case "close":
		kind = v1.CallPanelClose
		params = v1.PanelParams{Entry: "panel"}
	default:
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
		defer cancel()
		_, _ = p.c.Call(ctx, kind, params)
	}()
}

// takeSample reads the load, lets the cat react, and republishes every view,
// since the reading, the act or pace, and the graph may all have changed.
func (p *plugin) takeSample() {
	if len(p.views) == 0 || p.sampler == nil {
		return
	}
	p.sample.Reset(p.settings.SampleEvery)
	load, ok, err := p.sampler.Sample()
	if err != nil {
		_ = p.c.Send(&v1.PluginStatus{State: v1.StatusError, Message: "cannot read CPU load: " + err.Error()})
		return
	}
	if !ok {
		return
	}
	p.cat.Observe(load, p.now())
	p.history.Push(load)
	p.snapshotAll()
	p.schedule()
}

func (p *plugin) snapshotAll() {
	for id, v := range p.views {
		p.snapshot(id, v)
	}
}

func (p *plugin) snapshot(id string, v *view) {
	f := cat.FrameOf(p.cat)
	var root *v1.Node
	switch v.kind {
	case v1.ViewBar:
		root = cat.BarTree(f, p.settings, v.height)
	case v1.ViewTooltip:
		root = cat.TooltipTree(f)
	default:
		p.graph = p.history.Values(p.graph[:0])
		root = cat.PanelTree(f, p.settings, p.graph)
	}
	if err := p.c.Snapshot(id, v.rev+1, root); err != nil {
		return
	}
	v.rev++
}
