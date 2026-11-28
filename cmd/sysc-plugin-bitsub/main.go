package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	bitsub "github.com/Nomadcxx/sysc-plugins/plugins/bitsub"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The plugin's identity, matching manifest.json.
const (
	pluginID      = "org.sysc.bitsub"
	pluginName    = "bitsub"
	pluginVersion = "0.1.0"
)

const (
	// callTimeout bounds every host call made from the loop.
	callTimeout = 5 * time.Second
	// stateSnapshot holds the last snapshot so an offline start still draws
	// the list; stateLastSeq is the last notification seq shown.
	stateSnapshot = "snapshot"
	stateLastSeq  = "last_seq"
	// resaveEvery refreshes the saved snapshot's age even when it is unchanged.
	resaveEvery         = 15 * time.Minute
	defaultClientConfig = "~/.config/bitsub/client.json"
	// actionsMinor is the first protocol minor that routes notification
	// button clicks back to the plugin.
	actionsMinor          = 29
	criticalTimeoutMS     = 0
	normalTimeoutMS       = 10000
	defaultPollEvery      = 30 * time.Second
	defaultRetryEvery     = 2 * time.Minute
	defaultFirstPollDelay = 250 * time.Millisecond
)

func main() {
	env := environment{now: time.Now, pollEvery: defaultPollEvery, retryEvery: defaultRetryEvery, firstPoll: defaultFirstPollDelay}
	if err := runPlugin(os.Stdin, os.Stdout, env); err != nil {
		os.Exit(1)
	}
}

type environment struct {
	now                              func() time.Time
	pollEvery, retryEvery, firstPoll time.Duration
}

type settings struct {
	clientConfig string
}

func defaultSettings() settings { return settings{clientConfig: defaultClientConfig} }

func (s *settings) apply(values map[string]any) {
	if v, ok := values["client_config"].(string); ok && strings.TrimSpace(v) != "" {
		s.clientConfig = strings.TrimSpace(v)
	}
}

type view struct {
	kind  v1.ViewKind
	rev   uint64
	width int
	last  []byte // the tree last sent, so an unchanged repaint is skipped
}

type jobKind int

const (
	jobPoll jobKind = iota
	jobIntent
	jobConfirm
)

// job is one request to bitsubd, run off the loop.
type job struct {
	kind   jobKind
	api    *bitsub.Client
	gen    uint64
	since  int64
	id     string
	intent subs.Intent
}

type result struct {
	job  job
	snap subs.Snapshot
	err  error
}

type session struct {
	env      environment
	client   *v1.Client
	settings settings
	minor    int
	views    map[string]view
	state    bitsub.State
	api      *bitsub.Client
	// gen invalidates requests made with a previous client config.
	gen uint64
	// loaded is true once state.get answered; saving before then could
	// overwrite stored state with an empty value.
	loaded bool
	// haveSeq is false on the very first run: the first poll then records
	// the server's last seq without replaying its history.
	haveSeq   bool
	lastSeq   int64
	savedSnap []byte
	savedAt   time.Time
	jobs      chan job
	polling   bool
	queued    bool
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	return runPluginWith(in, out, env, defaultSettings())
}

func runPluginWith(in io.Reader, out io.Writer, env environment, initial settings) error {
	c := v1.NewClient(in, out)
	hello, err := c.Handshake(identity.FromManifest(v1.Identity{ID: pluginID, Name: pluginName, Version: pluginVersion}))
	if err != nil {
		return err
	}
	protocol, _ := v1.NegotiateProtocol(hello.Supported)
	s := &session{env: env, client: c, settings: initial, minor: protocol.Minor, views: map[string]view{}, jobs: make(chan job, 16)}
	s.state.HostMinor = protocol.Minor
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
	results := make(chan result, 16)
	go worker(ctx, s.jobs, results)

	s.restore(ctx)
	s.loadConfig()

	timer := time.NewTimer(env.firstPoll)
	defer timer.Stop()
	arm := func(d time.Duration) {
		timer.Stop()
		timer.Reset(d)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			s.requestPoll()
		case r := <-results:
			if next := s.handleResult(ctx, r); next > 0 {
				arm(next)
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View, width: m.Width}
				s.snapshot(m.ViewID)
				if m.View == v1.ViewPanel {
					s.requestPoll()
				}
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := s.views[m.ViewID]; ok {
					v.rev, v.last = 0, nil
					s.views[m.ViewID] = v
					s.snapshot(m.ViewID)
				}
			case *v1.InputEvent:
				// A click painted on a superseded tree must not act.
				if v, ok := s.views[m.ViewID]; ok && m.Revision == v.rev {
					s.handle(ctx, m)
				}
			case *v1.SettingsChanged:
				if m.Scope != v1.ScopePlugin {
					continue
				}
				before := s.settings.clientConfig
				s.settings.apply(m.Values)
				if s.settings.clientConfig != before {
					s.loadConfig()
					s.requestPoll()
				}
				s.snapshotAll()
			}
		}
	}
}

// worker runs bitsubd requests one at a time so a slow or unreachable
// server never stalls the loop.
func worker(ctx context.Context, jobs <-chan job, results chan<- result) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-jobs:
			r := result{job: j}
			switch j.kind {
			case jobPoll:
				r.snap, r.err = j.api.Snapshot(ctx, j.since)
			case jobIntent:
				r.err = j.api.SetIntent(ctx, j.id, j.intent)
			case jobConfirm:
				r.err = j.api.ConfirmCancelled(ctx, j.id)
			}
			select {
			case results <- r:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (s *session) loadConfig() {
	s.gen++
	s.state.ClientConfig = s.settings.clientConfig
	cfg, err := bitsub.LoadClientConfig(s.settings.clientConfig)
	if err != nil {
		s.api, s.state.ConfigErr = nil, err.Error()
		return
	}
	s.api, s.state.ConfigErr = bitsub.NewClient(cfg), ""
}

func (s *session) enqueue(j job) {
	j.api, j.gen = s.api, s.gen
	select {
	case s.jobs <- j:
	default: // a full queue already holds work that will repoll
	}
}

func (s *session) requestPoll() {
	if s.api == nil {
		s.snapshotAll()
		return
	}
	if s.polling {
		s.queued = true
		return
	}
	s.polling = true
	s.enqueue(job{kind: jobPoll, since: s.lastSeq})
}

// handleResult applies one finished request and returns when to poll next
// (zero leaves the timer alone).
func (s *session) handleResult(ctx context.Context, r result) time.Duration {
	if r.job.kind != jobPoll {
		if errors.Is(r.err, bitsub.ErrUnauthorized) {
			s.state.AuthFailed = true
		}
		s.requestPoll() // fetch the truth after any change, failed or not
		return 0
	}
	s.polling = false
	defer func() {
		if s.queued {
			s.queued = false
			s.requestPoll()
		}
	}()
	if r.job.gen != s.gen {
		return 0
	}
	if r.err != nil {
		s.state.Online = false
		s.state.AuthFailed = errors.Is(r.err, bitsub.ErrUnauthorized)
		s.snapshotAll()
		return s.env.retryEvery
	}
	snap := r.snap
	s.state.Online, s.state.AuthFailed, s.state.LastOK = true, false, s.env.now()
	s.relay(ctx, snap)
	snap.Notifications = nil
	s.state.Snap = &snap
	if s.state.Confirming != "" && !hasItem(snap.Items, s.state.Confirming) {
		s.state.Confirming = ""
	}
	s.saveSnapshot(ctx, snap)
	s.snapshotAll()
	return s.env.pollEvery
}

func hasItem(items []subs.Item, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// relay shows notifications newer than the last one shown. The first run
// only records where the server is, so history is never replayed.
func (s *session) relay(ctx context.Context, snap subs.Snapshot) {
	if s.haveSeq && snap.LastSeq >= s.lastSeq {
		for _, n := range bitsub.Fresh(snap.Notifications, s.lastSeq, s.env.now()) {
			s.notify(ctx, n)
		}
	}
	if !s.haveSeq || snap.LastSeq != s.lastSeq {
		s.haveSeq, s.lastSeq = true, snap.LastSeq
		s.saveState(ctx, stateLastSeq, []byte(strconv.FormatInt(snap.LastSeq, 10)))
	}
}

func (s *session) notify(ctx context.Context, n subs.Notification) {
	p := v1.NotifyParams{Summary: n.Summary, Body: n.Body, Urgency: v1.UrgencyNormal, TimeoutMS: normalTimeoutMS}
	switch n.Urgency {
	case "critical":
		p.Urgency, p.TimeoutMS = v1.UrgencyCritical, criticalTimeoutMS
	case "low":
		p.Urgency = v1.UrgencyLow
	}
	if s.minor >= actionsMinor {
		for _, a := range n.Actions {
			p.Actions = append(p.Actions, v1.NotifyAction{Key: a.Key, Label: a.Label})
		}
	}
	_, _ = s.call(ctx, v1.CallNotify, p)
}

func (s *session) call(ctx context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return s.client.Call(ctx, kind, params)
}

// restore loads the last snapshot and seq so an offline start still draws
// the list and a restart never repeats a notification.
func (s *session) restore(ctx context.Context) {
	get := func(key string) (json.RawMessage, bool) {
		reply, err := s.call(ctx, v1.CallStateGet, v1.StateGetParams{Key: key})
		if err != nil || !reply.OK {
			return nil, false
		}
		s.loaded = true
		var res v1.StateGetResult
		if json.Unmarshal(reply.Result, &res) != nil || !res.Found {
			return nil, false
		}
		return res.Value, true
	}
	if raw, ok := get(stateLastSeq); ok {
		if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			s.haveSeq, s.lastSeq = true, n
		}
	}
	if raw, ok := get(stateSnapshot); ok {
		var snap subs.Snapshot
		if json.Unmarshal(raw, &snap) == nil {
			s.state.Snap, s.state.LastOK = &snap, snap.GeneratedAt
			s.savedSnap = canonical(snap)
		}
	}
}

// canonical is the snapshot without its timestamp, for change detection.
func canonical(snap subs.Snapshot) []byte {
	snap.GeneratedAt, snap.Notifications = time.Time{}, nil
	raw, _ := json.Marshal(snap)
	return raw
}

func (s *session) saveSnapshot(ctx context.Context, snap subs.Snapshot) {
	now := s.env.now()
	c := canonical(snap)
	if bytes.Equal(c, s.savedSnap) && now.Sub(s.savedAt) < resaveEvery {
		return
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return
	}
	if s.saveState(ctx, stateSnapshot, raw) {
		s.savedSnap, s.savedAt = c, now
	}
}

func (s *session) saveState(ctx context.Context, key string, value []byte) bool {
	if !s.loaded {
		return false
	}
	reply, err := s.call(ctx, v1.CallStateSet, v1.StateSetParams{Key: key, Value: value})
	return err == nil && reply.OK
}

func (s *session) tree(v view) *v1.Node {
	s.state.Now = s.env.now()
	switch v.kind {
	case v1.ViewBar:
		return bitsub.Bar(&s.state, v.width)
	case v1.ViewTooltip:
		return bitsub.Tooltip(&s.state)
	default:
		return bitsub.Panel(&s.state)
	}
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	tree := s.tree(v)
	raw, _ := json.Marshal(tree)
	if v.rev > 0 && bytes.Equal(raw, v.last) {
		return
	}
	v.rev++
	v.last = raw
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, tree)
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}

func (s *session) item(id string) *subs.Item {
	if s.state.Snap == nil {
		return nil
	}
	for i := range s.state.Snap.Items {
		if s.state.Snap.Items[i].ID == id {
			return &s.state.Snap.Items[i]
		}
	}
	return nil
}

// handle applies a click. Keep and Cancel toggle the intent, a second
// Cancel asks "Have you cancelled it?", and nothing reaches the server
// while it is unreachable.
func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	if m.Event != v1.EventActivate {
		return
	}
	if m.Node == "bar" {
		_, _ = s.call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
		return
	}
	verb, id, ok := strings.Cut(m.Node, ":")
	if !ok {
		return
	}
	if verb == "confirm-no" {
		s.state.Confirming = ""
		s.snapshotAll()
		return
	}
	it := s.item(id)
	if it == nil || !s.state.Online || s.api == nil {
		return
	}
	switch verb {
	case "keep", "cancel":
		target := subs.Intent(verb)
		switch {
		case verb == "cancel" && it.Intent == subs.Cancel:
			s.state.Confirming = id
			s.snapshotAll()
			return
		case it.Intent == target:
			target = subs.Undecided
		}
		it.Intent, s.state.Confirming = target, ""
		s.enqueue(job{kind: jobIntent, id: id, intent: target})
	case "confirm-yes":
		s.state.Confirming = ""
		items := s.state.Snap.Items[:0]
		for _, x := range s.state.Snap.Items {
			if x.ID != id {
				items = append(items, x)
			}
		}
		s.state.Snap.Items = items
		s.enqueue(job{kind: jobConfirm, id: id})
	default:
		return
	}
	s.snapshotAll()
}
