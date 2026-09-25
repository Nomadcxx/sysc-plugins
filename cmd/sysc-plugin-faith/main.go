package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"sync"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/faith"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// options are the process's seams for tests.
type options struct {
	// APIBase overrides the commentary API; SYSC_FAITH_API_BASE sets it.
	APIBase string
	CanRead bool
	OpenURL func(string)
	Now     func() time.Time
	Tick    time.Duration
}

func main() {
	_, err := exec.LookPath("xdg-open")
	opt := options{
		APIBase: os.Getenv("SYSC_FAITH_API_BASE"),
		CanRead: err == nil,
		OpenURL: func(url string) {
			cmd := exec.Command("xdg-open", url)
			if cmd.Start() == nil {
				go func() { _ = cmd.Wait() }()
			}
		},
	}
	if err := run(os.Stdin, os.Stdout, opt); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-plugin-faith:", err)
		os.Exit(1)
	}
}

// lockedWriter makes each message write atomic. v1.Encoder has no lock of
// its own but performs exactly one Write per message, so guarding Write is
// enough for snapshots and host calls to share stdout from any goroutine.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

type commentaryResult struct {
	gen   uint64
	text  string
	found bool
	err   error
}

type view struct {
	kind v1.ViewKind
	rev  uint64
}

func run(in io.Reader, out io.Writer, opt options) error {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Tick == 0 {
		opt.Tick = 30 * time.Second
	}
	if opt.OpenURL == nil {
		opt.OpenURL = func(string) {}
	}
	c := v1.NewClient(in, &lockedWriter{w: out})
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.faith", Name: "Faith", Version: "0.1.0"})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	incoming := make(chan v1.Message, 64)
	go func() {
		defer close(incoming)
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

	sess, err := faith.NewSession(faith.DefaultSettings(), rand.New(rand.NewPCG(uint64(opt.Now().UnixNano()), 0x5eed)), opt.Now(), opt.CanRead)
	if err != nil {
		return err
	}
	sess.Restore(restore(ctx, c))

	commentary := faith.NewCommentary(opt.APIBase)
	results := make(chan commentaryResult, 8)
	saves := newSaver(ctx, c)

	views := map[string]*view{}
	publish := func() {
		for id, v := range views {
			v.rev++
			switch v.kind {
			case v1.ViewBar:
				_ = c.Snapshot(id, v.rev, sess.BarTree())
			case v1.ViewTooltip:
				_ = c.Snapshot(id, v.rev, sess.TooltipTree())
			default:
				_ = c.Snapshot(id, v.rev, sess.PanelTree())
			}
		}
	}
	perform := func(effects []faith.Effect) {
		for _, e := range effects {
			switch e := e.(type) {
			case faith.NotifyEffect:
				go func() {
					cctx, done := context.WithTimeout(ctx, 3*time.Second)
					defer done()
					_, _ = c.Call(cctx, v1.CallNotify, e.Params)
				}()
			case faith.OpenPanelEffect:
				go func() {
					cctx, done := context.WithTimeout(ctx, 3*time.Second)
					defer done()
					_, _ = c.Call(cctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: e.Output, Instance: e.Instance})
				}()
			case faith.OpenURLEffect:
				opt.OpenURL(e.URL)
			case faith.FetchCommentaryEffect:
				go func() {
					cctx, done := context.WithTimeout(ctx, 15*time.Second)
					defer done()
					text, found, err := commentary.Entry(cctx, e.Ref)
					select {
					case results <- commentaryResult{e.Gen, text, found, err}:
					case <-ctx.Done():
					}
				}()
			case faith.SaveStateEffect:
				saves.put(sess.Persisted())
			}
		}
	}

	ticker := time.NewTicker(opt.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case r := <-results:
			if sess.CommentaryResult(r.gen, r.text, r.found, r.err) {
				publish()
			}
		case <-ticker.C:
			if changed, effects := sess.Tick(opt.Now()); changed {
				perform(effects)
				publish()
			}
		case msg, ok := <-incoming:
			if !ok {
				return nil
			}
			switch m := msg.(type) {
			case *v1.HostShutdown:
				// A prayer or a step just before shutdown must still reach
				// host state; the deferred cancel would abort its write.
				saves.flush(time.Second)
				return nil
			case *v1.ViewOpen:
				views[m.ViewID] = &view{kind: m.View}
				if m.View == v1.ViewPanel || m.View == v1.ViewFloating {
					perform(sess.PanelOpened())
				}
				publish()
			case *v1.ViewClose:
				if v, ok := views[m.ViewID]; ok && (v.kind == v1.ViewPanel || v.kind == v1.ViewFloating) {
					sess.PanelClosed()
				}
				delete(views, m.ViewID)
			case *v1.ViewResync:
				publish()
			case *v1.InputEvent:
				perform(sess.Input(m, opt.Now()))
				publish()
			case *v1.SettingsChanged:
				perform(sess.ApplySettings(m.Values, opt.Now()))
				publish()
			}
		}
	}
}

// State keys.
const (
	keyRef = "ref"
	keyBag = "bag"
)

func restore(ctx context.Context, c *v1.Client) faith.Persisted {
	var p faith.Persisted
	get := func(key string, dst any) bool {
		cctx, done := context.WithTimeout(ctx, 3*time.Second)
		defer done()
		reply, err := c.Call(cctx, v1.CallStateGet, v1.StateGetParams{Key: key})
		if err != nil || !reply.OK {
			return false
		}
		var res v1.StateGetResult
		if json.Unmarshal(reply.Result, &res) != nil || !res.Found {
			return false
		}
		// A stored null decodes cleanly into a zero value; it is no state.
		if bytes.Equal(bytes.TrimSpace(res.Value), []byte("null")) {
			return false
		}
		return json.Unmarshal(res.Value, dst) == nil
	}
	var ref faith.Ref
	if get(keyRef, &ref) {
		p.Ref = &ref
	}
	var bag faith.Bag
	if get(keyBag, &bag) {
		p.Bag = &bag
	}
	return p
}

// saver writes the latest state on its own goroutine; a burst of saves
// collapses into the last one. write serialises a write with flush, so a
// flush never races the goroutine for the same pending state.
type saver struct {
	c       *v1.Client
	ctx     context.Context
	mu      sync.Mutex
	pending *faith.Persisted
	signal  chan struct{}
	write   sync.Mutex
}

func newSaver(ctx context.Context, c *v1.Client) *saver {
	s := &saver{c: c, ctx: ctx, signal: make(chan struct{}, 1)}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.signal:
			}
			s.drain(ctx, 3*time.Second)
		}
	}()
	return s
}

func (s *saver) put(p faith.Persisted) {
	s.mu.Lock()
	s.pending = &p
	s.mu.Unlock()
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

// flush writes any pending state now, waiting at most timeout per key.
func (s *saver) flush(timeout time.Duration) {
	s.drain(context.WithoutCancel(s.ctx), timeout)
}

func (s *saver) drain(ctx context.Context, timeout time.Duration) {
	s.write.Lock()
	defer s.write.Unlock()
	s.mu.Lock()
	p := s.pending
	s.pending = nil
	s.mu.Unlock()
	if p == nil {
		return
	}
	for _, kv := range []struct {
		key string
		val any
	}{{keyRef, p.Ref}, {keyBag, p.Bag}} {
		raw, err := json.Marshal(kv.val)
		if err != nil {
			continue
		}
		cctx, done := context.WithTimeout(ctx, timeout)
		_, _ = s.c.Call(cctx, v1.CallStateSet, v1.StateSetParams{Key: kv.key, Value: raw})
		done()
	}
}
