package hermes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Modes.
const (
	ModeLocal      = "local"
	ModeClientOnly = "client-only"
)

// Connection and action messages, from the design.
const (
	MsgFailedToStart   = "Bridge failed to start"
	MsgOffline         = "Bridge offline"
	MsgNoToken         = "No bridge token: paste it in Settings or set a token file"
	MsgWrongToken      = "Wrong token"
	MsgCollision       = "A local bridge still listens on 127.0.0.1:19777; stop it or forward the tunnel to another port"
	MsgApprovalChanged = "The approval changed; check the card again"
	MsgInvalidModel    = "Invalid model or provider name"
	MsgOneShotFailed   = "One-shot failed"
	MsgGatewayFailed   = "Failed to start gateway"
	MsgBridgeError     = "Bridge error"
	// MsgGatewayRemote is the plugin's own: client-only never drives a gateway.
	MsgGatewayRemote = "The gateway is not managed in client-only mode"
	MsgQueueFull     = "Too many actions queued"
)

// MsgExited is the connection line for a non-zero bridge exit.
func MsgExited(code int) string { return "Bridge exited: " + strconv.Itoa(code) }

// MsgCleartext is the warning for a non-loopback client-only host.
func MsgCleartext(host string) string {
	return "Cleartext HTTP to " + host + ": the token and prompts are not encrypted."
}

// Settings are the plugin's settings, parsed from settings.changed. The
// pasted token is never formatted: Format prints only mode and address.
type Settings struct {
	ClientOnly       bool
	Host             string
	Port             int
	TokenManual      string `json:"-"`
	TokenFile        string
	SSHTarget        string
	StateFile        string
	HermesHome       string
	HermesCommand    string
	PythonCommand    string
	BridgeScript     string
	AutoStartBridge  bool
	AutoStartGateway bool
	PollInterval     time.Duration
	HideWhenIdle     bool
	ShowToolActivity bool
	DefaultProvider  string
	DefaultModel     string
	ConnectionLabel  string
	NotifyOnApproval bool
	NotifyOnComplete bool
}

// Format keeps the pasted token out of every formatting verb.
func (s Settings) Format(f fmt.State, _ rune) {
	mode := ModeLocal
	if s.ClientOnly {
		mode = ModeClientOnly
	}
	fmt.Fprintf(f, "hermes.Settings{%s %s:%d}", mode, s.Host, s.Port)
}

// DefaultSettings are the manifest defaults.
func DefaultSettings() Settings {
	return Settings{
		Host:             LocalHost,
		Port:             LocalPort,
		TokenFile:        DefaultTokenFile,
		StateFile:        DefaultStateFile,
		HermesHome:       "~/.hermes",
		HermesCommand:    "hermes",
		PythonCommand:    "python3",
		AutoStartBridge:  true,
		AutoStartGateway: true,
		PollInterval:     30 * time.Second,
		ConnectionLabel:  "Hermes",
		NotifyOnApproval: true,
	}
}

// SettingsFromValues reads a settings.changed values map. Missing keys,
// wrong types and blank strings fall back to the manifest defaults.
func SettingsFromValues(values map[string]any) Settings {
	s := DefaultSettings()
	str := func(key string, dst *string) {
		if v, ok := values[key].(string); ok && strings.TrimSpace(v) != "" {
			*dst = strings.TrimSpace(v)
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := values[key].(bool); ok {
			*dst = v
		}
	}
	number := func(key string) (float64, bool) {
		switch v := values[key].(type) {
		case float64:
			return v, !math.IsNaN(v) && !math.IsInf(v, 0)
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			f, err := v.Float64()
			return f, err == nil
		}
		return 0, false
	}
	boolean("client_only_mode", &s.ClientOnly)
	str("bridge_host", &s.Host)
	if p, ok := number("bridge_port"); ok && p >= 1 && p <= 65535 {
		s.Port = int(p)
	}
	// The pasted token is taken as typed; Resolve trims and validates it.
	if v, ok := values["bridge_token_manual"].(string); ok {
		s.TokenManual = v
	}
	str("bridge_token_file", &s.TokenFile)
	str("ssh_target", &s.SSHTarget)
	str("state_file", &s.StateFile)
	str("hermes_home", &s.HermesHome)
	str("hermes_command", &s.HermesCommand)
	str("python_command", &s.PythonCommand)
	str("bridge_script", &s.BridgeScript)
	boolean("auto_start_bridge", &s.AutoStartBridge)
	boolean("auto_start_gateway", &s.AutoStartGateway)
	if p, ok := number("status_poll_interval"); ok {
		secs := math.Max(1, math.Min(p, 3600))
		s.PollInterval = time.Duration(secs * float64(time.Second))
	}
	boolean("hide_when_idle", &s.HideWhenIdle)
	boolean("show_tool_activity", &s.ShowToolActivity)
	str("default_provider", &s.DefaultProvider)
	str("default_model", &s.DefaultModel)
	str("connection_label", &s.ConnectionLabel)
	boolean("notify_on_approval", &s.NotifyOnApproval)
	boolean("notify_on_complete", &s.NotifyOnComplete)
	return s
}

// ConnectionKey is what a connection depends on: a change resets it. The
// pasted token is held only as a hash.
type ConnectionKey struct {
	ClientOnly      bool
	Host            string
	Port            int
	TokenManualHash string
	TokenFile       string
	StateFile       string
	HermesHome      string
	HermesCommand   string
	PythonCommand   string
	BridgeScript    string
	AutoStartBridge bool
}

func (s Settings) ConnectionKey() ConnectionKey {
	k := ConnectionKey{
		ClientOnly: s.ClientOnly, TokenFile: s.TokenFile, StateFile: s.StateFile,
		HermesHome: s.HermesHome, HermesCommand: s.HermesCommand,
		PythonCommand: s.PythonCommand, BridgeScript: s.BridgeScript, AutoStartBridge: s.AutoStartBridge,
	}
	if s.ClientOnly {
		k.Host, k.Port = s.Host, s.Port
		if s.TokenManual != "" {
			sum := sha256.Sum256([]byte(s.TokenManual))
			k.TokenManualHash = hex.EncodeToString(sum[:8])
		}
	}
	return k
}

// spawnKey is what an owned bridge was started with; a change restarts it.
func (s Settings) spawnKey() [5]string {
	return [5]string{s.PythonCommand, s.BridgeScript, s.StateFile, s.HermesHome, s.HermesCommand}
}

// Options are timings and addresses; tests shrink them.
type Options struct {
	LocalHost      string
	LocalPort      int
	RunningPoll    time.Duration
	Backoff        []time.Duration
	ProbeInterval  time.Duration
	ProbeTimeout   time.Duration
	StopGrace      time.Duration
	WatchPoll      time.Duration
	ForcePollWatch bool
	SpawnAttempts  int
	// DefaultScript is the bundled bridge, used when bridge_script is blank.
	DefaultScript string
	QueueSize     int
}

// DefaultOptions are the design's numbers.
func DefaultOptions() Options {
	return Options{
		LocalHost:     LocalHost,
		LocalPort:     LocalPort,
		RunningPoll:   1500 * time.Millisecond,
		Backoff:       []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second},
		ProbeInterval: 500 * time.Millisecond,
		ProbeTimeout:  10 * time.Second,
		StopGrace:     DefaultStopGrace,
		WatchPoll:     DefaultWatchPoll,
		SpawnAttempts: 3,
		QueueSize:     32,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.LocalHost == "" {
		o.LocalHost = d.LocalHost
	}
	if o.LocalPort == 0 {
		o.LocalPort = d.LocalPort
	}
	if o.RunningPoll <= 0 {
		o.RunningPoll = d.RunningPoll
	}
	if len(o.Backoff) == 0 {
		o.Backoff = d.Backoff
	}
	if o.ProbeInterval <= 0 {
		o.ProbeInterval = d.ProbeInterval
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = d.ProbeTimeout
	}
	if o.StopGrace <= 0 {
		o.StopGrace = d.StopGrace
	}
	if o.WatchPoll <= 0 {
		o.WatchPoll = d.WatchPoll
	}
	if o.SpawnAttempts <= 0 {
		o.SpawnAttempts = d.SpawnAttempts
	}
	if o.QueueSize <= 0 {
		o.QueueSize = d.QueueSize
	}
	return o
}

// Deps are the service's ties to the host. All are called off the plugin's
// event loop; Notify and StateSave must not block.
type Deps struct {
	Starter   Starter
	Notify    func(summary, body string)
	StateGet  func(ctx context.Context, key string) (json.RawMessage, bool, error)
	StateSave func(key string, value json.RawMessage)
	Logf      func(format string, args ...any)
}

// Detected is what auto-configure recorded, shown as Detected: captions.
type Detected struct {
	HermesHome      string `json:"hermes_home,omitempty"`
	HermesCommand   string `json:"hermes_command,omitempty"`
	DefaultModel    string `json:"default_model,omitempty"`
	DefaultProvider string `json:"default_provider,omitempty"`
}

// View is the published, render-ready service state. It never holds the
// token value.
type View struct {
	Revision uint64
	Mode     string
	State    State
	Status   Status
	// Message is the connection line; Warning is cleartext or collision;
	// ActionError is the last failed action.
	Message     string
	Warning     string
	ActionError string

	Pending        bool
	OneShotRunning bool
	OneShotResult  json.RawMessage
	OneShotError   string

	Owned   bool
	Adopted bool

	Token     TokenKind
	TokenPath string
	Detected  Detected
	ViewError string
}

type job struct {
	path    string
	body    any
	refetch bool
	// approval is set for an approval:<sig>:<action> click, which checks the
	// card and the live state before it posts.
	approval *approvalClick
}

type owned struct {
	proc     Process
	spec     SpawnSpec
	stopping bool
}

// Service runs the bridge connection, the action queue and the one-shot off
// the plugin's event loop and publishes a View on every change.
type Service struct {
	deps Deps
	opts Options

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	updates     chan struct{}
	cfgSig      chan struct{}
	poke        chan struct{}
	tokenReload chan struct{}
	jobs        chan job

	startOnce    sync.Once
	shutdownOnce sync.Once

	mu       sync.Mutex
	view     View
	settings Settings
	bridge   *Bridge
	owned    *owned
	pending  int
	// adoptedLocal survives a switch to client-only, for the collision guard.
	adoptedLocal bool

	configLoaded bool
	configured   bool
	homeOverride string
	restarted    bool

	approvalSig string
	prevRunning bool
	haveState   bool

	oneID     uint64
	oneCancel context.CancelFunc
}

// NewService builds a service; it connects on the first Configure.
func NewService(deps Deps, opts Options) *Service {
	opts = opts.withDefaults()
	if deps.Starter == nil {
		deps.Starter = ExecStarter{}
	}
	if deps.Logf == nil {
		deps.Logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		deps: deps, opts: opts, ctx: ctx, cancel: cancel,
		updates:     make(chan struct{}, 1),
		cfgSig:      make(chan struct{}, 1),
		poke:        make(chan struct{}, 1),
		tokenReload: make(chan struct{}, 1),
		jobs:        make(chan job, opts.QueueSize),
	}
	s.view.State.Normalize()
	s.view.State.Bridge.Status = "offline"
	s.view.Status = DeriveStatus(s.view.State)
	s.wg.Add(1)
	go s.worker()
	return s
}

// Updates signals that Snapshot changed. It coalesces.
func (s *Service) Updates() <-chan struct{} { return s.updates }

// Snapshot is the current view.
func (s *Service) Snapshot() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view
}

// Configure applies settings. It never blocks; the first call starts the
// connection.
func (s *Service) Configure(set Settings) {
	if s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	s.settings = set
	s.mu.Unlock()
	started := false
	s.startOnce.Do(func() {
		started = true
		s.wg.Add(1)
		go s.run(set)
	})
	if !started {
		signal(s.cfgSig)
	}
}

// Refresh asks for an immediate poll.
func (s *Service) Refresh() { signal(s.poke) }

// SetViewError records a view.error for the settings connection line.
func (s *Service) SetViewError(msg string) {
	s.publish(func(v *View) { v.ViewError = msg })
}

// Submit queues a POST action. Two submits run in order, one at a time.
func (s *Service) Submit(path string, body any) { s.enqueue(job{path: path, body: body}) }

func (s *Service) enqueue(j job) {
	if s.ctx.Err() != nil {
		return
	}
	if j.path == "/gateway/start" || j.path == "/gateway/stop" {
		if s.current().ClientOnly {
			s.publish(func(v *View) { v.ActionError = MsgGatewayRemote })
			return
		}
	}
	s.mu.Lock()
	select {
	case s.jobs <- j:
		s.pending++
		s.mu.Unlock()
		s.publish(func(v *View) { v.Pending = true })
	default:
		s.mu.Unlock()
		s.publish(func(v *View) { v.ActionError = MsgQueueFull })
	}
}

// OneShot asks a one-shot question on its own goroutine, never the queue.
func (s *Service) OneShot(text string) {
	if s.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	if s.oneCancel != nil {
		s.oneCancel()
	}
	s.oneID++
	id := s.oneID
	s.oneCancel = cancel
	b := s.bridge
	s.mu.Unlock()
	s.publish(func(v *View) { v.OneShotRunning, v.OneShotResult, v.OneShotError = true, nil, "" })
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		var snap Snapshot
		err := errors.New("no bridge")
		if b != nil {
			snap, err = b.OneShot(ctx, text)
		}
		s.mu.Lock()
		mine := s.oneID == id
		if mine {
			s.oneCancel = nil
		}
		s.mu.Unlock()
		if !mine {
			return
		}
		s.publish(func(v *View) {
			v.OneShotRunning = false
			switch {
			case err == nil:
				v.OneShotResult = snap.Result
			case ctx.Err() == nil:
				v.OneShotError = MsgOneShotFailed
			}
		})
	}()
}

// CancelOneShot cancels the client side of a running one-shot. The bridge
// keeps running the hermes process to its own timeout.
func (s *Service) CancelOneShot() {
	s.mu.Lock()
	cancel := s.oneCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// StopOwned stops the bridge this plugin spawned: SIGTERM, the grace, then
// SIGKILL. An adopted bridge is never signalled.
func (s *Service) StopOwned() {
	s.mu.Lock()
	o := s.owned
	if o != nil {
		o.stopping = true
	}
	s.mu.Unlock()
	if o == nil {
		return
	}
	StopProcess(o.proc, s.opts.StopGrace)
	s.mu.Lock()
	if s.owned == o {
		s.owned = nil
	}
	s.mu.Unlock()
	s.publish(func(v *View) { v.Owned = false })
}

// Shutdown cancels every request, stops the owned bridge and waits up to
// timeout for the goroutines.
func (s *Service) Shutdown(timeout time.Duration) {
	s.shutdownOnce.Do(func() {
		s.cancel()
		done := make(chan struct{})
		go func() { s.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(timeout):
			s.deps.Logf("hermes: shutdown timed out after %s", timeout)
		}
		// The runner stops the owned child; this covers a service that never
		// started its runner.
		s.StopOwned()
	})
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Service) current() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *Service) publish(f func(v *View)) {
	s.mu.Lock()
	f(&s.view)
	s.view.Revision++
	s.mu.Unlock()
	signal(s.updates)
}

// --- runner ----------------------------------------------------------------

func (s *Service) run(cur Settings) {
	defer s.wg.Done()
	s.reset(cur)
	for {
		connCtx, cancel := context.WithCancel(s.ctx)
		done := make(chan struct{})
		go func(set Settings) {
			defer close(done)
			if set.ClientOnly {
				s.connClient(connCtx, set)
			} else {
				s.connLocal(connCtx, set)
			}
		}(cur)

		var next Settings
	wait:
		for {
			select {
			case <-s.ctx.Done():
				cancel()
				<-done
				s.StopOwned()
				return
			case <-s.cfgSig:
				next = s.current()
				if next.ConnectionKey() == cur.ConnectionKey() {
					cur = next
					s.publish(func(*View) {})
					continue
				}
				break wait
			}
		}
		cancel()
		<-done
		if !cur.ClientOnly && (next.ClientOnly || next.spawnKey() != cur.spawnKey()) {
			// Wait for the exit so a tunnel can take the port.
			s.StopOwned()
			if next.spawnKey() != cur.spawnKey() {
				s.mu.Lock()
				s.homeOverride = ""
				s.mu.Unlock()
			}
		}
		cur = next
		s.reset(cur)
	}
}

// reset drops the connection: bridge, token, state and messages.
func (s *Service) reset(set Settings) {
	s.mu.Lock()
	s.bridge = nil
	s.haveState, s.prevRunning, s.approvalSig = false, false, ""
	if !set.ClientOnly {
		s.adoptedLocal = false
	}
	owned := s.owned != nil
	s.mu.Unlock()
	mode := ModeLocal
	if set.ClientOnly {
		mode = ModeClientOnly
	}
	s.publish(func(v *View) {
		v.Mode = mode
		v.State = State{}
		v.State.Normalize()
		v.State.Bridge.Status = "offline"
		v.Status = DeriveStatus(v.State)
		v.Message, v.Warning, v.ActionError = "", "", ""
		v.Token, v.TokenPath = TokenNone, ""
		v.Owned, v.Adopted = owned, false
	})
}

func (s *Service) setBridge(b *Bridge, tok Token) {
	s.mu.Lock()
	s.bridge = b
	s.mu.Unlock()
	s.publish(func(v *View) { v.Token, v.TokenPath = tok.Kind, tok.Path })
}

func (s *Service) currentBridge() *Bridge {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bridge
}

// publishState publishes a bridge state and sends notifications.
func (s *Service) publishState(st State) {
	st.Normalize()
	if st.Bridge.Status == "" {
		st.Bridge.Status = "online"
	}
	set := s.current()
	var notes [][2]string
	s.mu.Lock()
	if st.Approval.Pending {
		sig := approvalSignature(st.Approval)
		if sig != s.approvalSig {
			s.approvalSig = sig
			if set.NotifyOnApproval {
				notes = append(notes, [2]string{"Hermes needs approval", approvalExcerpt(st.Approval)})
			}
		}
	} else {
		s.approvalSig = ""
	}
	if s.haveState && s.prevRunning && !st.Session.Running && set.NotifyOnComplete {
		notes = append(notes, [2]string{"Hermes finished", ""})
	}
	s.prevRunning, s.haveState = st.Session.Running, true
	s.mu.Unlock()
	s.publish(func(v *View) {
		v.State = st
		v.Status = DeriveStatus(st)
		v.Message = ""
	})
	if s.deps.Notify != nil {
		for _, n := range notes {
			s.deps.Notify(n[0], n[1])
		}
	}
}

// publishConn shows a connection failure over the last good state.
func (s *Service) publishConn(bridgeStatus, msg string) {
	s.publish(func(v *View) {
		v.State.Bridge = BridgeInfo{Status: bridgeStatus}
		v.Status = DeriveStatus(v.State)
		v.Message = msg
	})
}

func approvalSignature(a Approval) string {
	sum := sha256.Sum256([]byte(a.ToolName + "\x00" + a.Message + "\x00" + string(a.Request)))
	return hex.EncodeToString(sum[:])
}

func approvalExcerpt(a Approval) string {
	msg := sanitizeText(a.Message)
	if r := []rune(msg); len(r) > 120 {
		msg = string(r[:120]) + "…"
	}
	if a.ToolName == "" {
		return msg
	}
	return sanitizeText(a.ToolName) + ": " + msg
}

func gatewayIdle(status string) bool {
	switch norm(status) {
	case "", "offline", "stopped", "unknown":
		return true
	}
	return false
}

// Wake reasons returned by sleep.
const (
	wokeCancelled = -1
	wokeTimer     = 0 // the timer or a Refresh poke
)

// sleep waits d or until a wake channel fires; it returns wokeCancelled when
// ctx ends, wokeTimer, or 1+i for wake[i]. A nil wake channel never fires.
func (s *Service) sleep(ctx context.Context, d time.Duration, wake ...<-chan struct{}) int {
	t := time.NewTimer(d)
	defer t.Stop()
	ch := make([]<-chan struct{}, 3)
	copy(ch, wake)
	select {
	case <-ctx.Done():
		return wokeCancelled
	case <-t.C:
	case <-s.poke:
	case <-ch[0]:
		return 1
	case <-ch[1]:
		return 2
	case <-ch[2]:
		return 3
	}
	return wokeTimer
}

// --- local mode ------------------------------------------------------------

func (s *Service) connLocal(ctx context.Context, set Settings) {
	health, err := NewBridge(s.opts.LocalHost, s.opts.LocalPort, "")
	if err != nil {
		s.publishConn("error", MsgBridgeError)
		<-ctx.Done()
		return
	}
	stateFile := ExpandPath(set.StateFile)
	tokenFile := filepath.Join(filepath.Dir(stateFile), "bridge.token")
	w := WatchFiles(ctx, []string{stateFile, tokenFile}, s.opts.WatchPoll, s.opts.ForcePollWatch)

	attempts := 0
	haveToken := false
	gatewayTried := false

	for ctx.Err() == nil {
		s.reapOwned()
		if _, err := health.Health(ctx); err == nil {
			s.mu.Lock()
			isOwned := s.owned != nil
			s.adoptedLocal = !isOwned
			s.mu.Unlock()
			s.publish(func(v *View) { v.Owned, v.Adopted = isOwned, !isOwned })
			if !haveToken {
				haveToken = s.loadLocalToken(ctx, set)
			}
			if haveToken {
				s.autoConfigure(ctx, set, false, &gatewayTried)
				if !gatewayTried && set.AutoStartGateway && gatewayIdle(s.Snapshot().State.Summary.Gateway.Status) {
					gatewayTried = true
					s.enqueue(job{path: "/gateway/start", body: map[string]any{}, refetch: true})
				}
			}
		} else if ctx.Err() == nil {
			haveToken = false
			s.mu.Lock()
			o := s.owned
			s.mu.Unlock()
			msg := s.Snapshot().Message
			if !strings.HasPrefix(msg, "Bridge exited: ") && msg != MsgFailedToStart {
				msg = MsgOffline
			}
			s.publishConn("offline", msg)
			s.publish(func(v *View) { v.Owned, v.Adopted = o != nil, false })
			if o == nil && set.AutoStartBridge && attempts < s.opts.SpawnAttempts {
				attempts++
				if s.spawn(ctx, set) {
					continue
				}
				if attempts == s.opts.SpawnAttempts && ctx.Err() == nil {
					s.publishConn("offline", MsgFailedToStart)
				}
			}
		}

		// Wake when the owned child exits, re-read each pass: a reaped
		// child's closed Done channel must not spin the loop.
		var exited <-chan struct{}
		s.mu.Lock()
		if o := s.owned; o != nil {
			exited = o.proc.Done()
		}
		s.mu.Unlock()
		woke := s.sleep(ctx, s.current().PollInterval, w.Changed(), s.tokenReload, exited)
		if woke == wokeCancelled {
			return
		}
		if woke == 2 {
			// A 403 on an action: the bridge may have a new token.
			haveToken = false
		}
		for _, p := range w.Drain() {
			if p == tokenFile {
				haveToken = false
				continue
			}
			st, err := ReadStateFile(p)
			switch {
			case errors.Is(err, os.ErrNotExist):
			case err != nil:
				s.publish(func(v *View) { v.Message = ErrInvalidStateFile.Error() })
			default:
				s.publishState(st)
			}
		}
	}
}

// reapOwned notices an owned child that exited on its own.
func (s *Service) reapOwned() {
	s.mu.Lock()
	o := s.owned
	if o == nil {
		s.mu.Unlock()
		return
	}
	select {
	case <-o.proc.Done():
	default:
		s.mu.Unlock()
		return
	}
	s.owned = nil
	stopping := o.stopping
	s.mu.Unlock()
	code := o.proc.ExitCode()
	s.deps.Logf("hermes: bridge pid %d exited with %d", o.proc.Pid(), code)
	if code != 0 && !stopping {
		s.publishConn("offline", MsgExited(code))
	}
	s.publish(func(v *View) { v.Owned = false })
}

// loadLocalToken tries the local candidates; /state accepts or refuses each.
func (s *Service) loadLocalToken(ctx context.Context, set Settings) bool {
	src := TokenSource{StateFile: set.StateFile}
	var first State
	var got bool
	var refused bool
	tok, err := src.Resolve(func(token string) bool {
		b, err := NewBridge(s.opts.LocalHost, s.opts.LocalPort, token)
		if err != nil {
			return false
		}
		st, err := b.State(ctx)
		if errors.Is(err, ErrAuth) {
			refused = true
			return false
		}
		if err == nil {
			first, got = st, true
		}
		return err == nil
	})
	if tok.Kind == TokenNone || !got {
		if ctx.Err() != nil {
			return false
		}
		if err != nil {
			s.deps.Logf("hermes: local token: %v", err)
		}
		msg := MsgNoToken
		if refused {
			msg = MsgWrongToken
		}
		s.publishConn("error", msg)
		return false
	}
	b, err := NewBridge(s.opts.LocalHost, s.opts.LocalPort, tok.Value())
	if err != nil {
		return false
	}
	s.setBridge(b, tok)
	s.publishState(first)
	return true
}

// spawn starts the bridge and probes /health until it answers or the probe
// window ends. A child that never answered is stopped.
func (s *Service) spawn(ctx context.Context, set Settings) bool {
	s.mu.Lock()
	home := s.homeOverride
	s.mu.Unlock()
	if home == "" {
		home = ExpandPath(set.HermesHome)
	}
	script := set.BridgeScript
	if script == "" {
		script = s.opts.DefaultScript
	}
	spec := SpawnSpec{
		Python:        set.PythonCommand,
		Script:        ExpandPath(script),
		StateFile:     ExpandPath(set.StateFile),
		HermesHome:    home,
		HermesCommand: set.HermesCommand,
		LogPath:       filepath.Join(CacheHome(), "sysc-hermes", "bridge.log"),
	}
	p, err := s.deps.Starter.Start(spec)
	if err != nil {
		s.deps.Logf("hermes: bridge start failed: %v", err)
		return false
	}
	o := &owned{proc: p, spec: spec}
	s.mu.Lock()
	s.owned = o
	s.mu.Unlock()
	s.deps.Logf("hermes: started bridge pid %d", p.Pid())

	probe, _ := NewBridge(s.opts.LocalHost, s.opts.LocalPort, "")
	deadline := time.Now().Add(s.opts.ProbeTimeout)
	for {
		if _, err := probe.Health(ctx); err == nil {
			return true
		}
		select {
		case <-p.Done():
			return false
		default:
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
		case <-p.Done():
		case <-time.After(s.opts.ProbeInterval):
		}
	}
	s.StopOwned()
	return false
}

// --- client-only mode ------------------------------------------------------

func (s *Service) connClient(ctx context.Context, set Settings) {
	src := TokenSource{ClientOnly: true, Pasted: set.TokenManual, File: set.TokenFile}
	tok, err := src.Resolve(nil)
	if err != nil {
		s.deps.Logf("hermes: client-only token: %v", err)
	}
	b, err := NewBridge(set.Host, set.Port, tok.Value())
	if err != nil {
		s.publishConn("error", "Invalid bridge host or port")
		<-ctx.Done()
		return
	}
	s.setBridge(b, tok)

	s.mu.Lock()
	collision := s.adoptedLocal && sameLocal(set.Host, set.Port, s.opts.LocalHost, s.opts.LocalPort)
	s.mu.Unlock()
	warning := ""
	switch {
	case collision:
		warning = MsgCollision
	case !IsLoopback(set.Host):
		warning = MsgCleartext(set.Host)
	}
	s.publish(func(v *View) { v.Warning = warning })

	fails := 0
	gatewayTried := true // client-only never starts a gateway
	for ctx.Err() == nil {
		st, err := b.State(ctx)
		if ctx.Err() != nil {
			return
		}
		next := s.current().PollInterval
		switch {
		case err == nil:
			fails = 0
			s.publishState(st)
			s.autoConfigure(ctx, set, true, &gatewayTried)
			if st.Session.Running {
				next = s.opts.RunningPoll
			}
		case errors.Is(err, ErrAuth):
			msg := MsgWrongToken
			if tok.Kind == TokenNone {
				msg = MsgNoToken
			}
			s.publishConn("error", msg)
			// The token file may have been fixed meanwhile.
			if t2, err := src.Resolve(nil); err == nil && t2.Value() != tok.Value() {
				if b2, err := NewBridge(set.Host, set.Port, t2.Value()); err == nil {
					tok, b = t2, b2
					s.setBridge(b, tok)
				}
			}
			next = s.backoff(fails)
			fails++
		default:
			s.deps.Logf("hermes: /state: %v", err)
			s.publishConn("offline", MsgOffline)
			next = s.backoff(fails)
			fails++
		}
		if s.sleep(ctx, next) == wokeCancelled {
			return
		}
	}
}

func (s *Service) backoff(fails int) time.Duration {
	if fails >= len(s.opts.Backoff) {
		fails = len(s.opts.Backoff) - 1
	}
	return s.opts.Backoff[fails]
}

func sameLocal(host string, port int, localHost string, localPort int) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		h = "127.0.0.1"
	}
	lh := localHost
	if lh == "localhost" {
		lh = "127.0.0.1"
	}
	return h == lh && port == localPort
}

// --- auto-configure --------------------------------------------------------

// autoConfigure runs /detect once per install. Client-only records only the
// default model and provider: remote paths are wrong for this machine.
func (s *Service) autoConfigure(ctx context.Context, set Settings, clientOnly bool, gatewayTried *bool) {
	s.mu.Lock()
	loaded, configured := s.configLoaded, s.configured
	s.mu.Unlock()
	if !loaded && s.deps.StateGet != nil {
		raw, ok, err := s.deps.StateGet(ctx, "configured")
		if err != nil {
			return // retry on the next success
		}
		var flag bool
		configured = ok && json.Unmarshal(raw, &flag) == nil && flag
		var det Detected
		if raw, ok, err := s.deps.StateGet(ctx, "detected"); err == nil && ok && json.Unmarshal(raw, &det) == nil {
			s.publish(func(v *View) { v.Detected = det })
		}
		s.mu.Lock()
		s.configLoaded, s.configured = true, configured
		s.mu.Unlock()
	}
	if configured {
		return
	}
	b := s.currentBridge()
	if b == nil {
		return
	}
	d, err := b.Detect(ctx)
	if err != nil {
		s.deps.Logf("hermes: /detect: %v", err)
		return
	}
	det := Detected{DefaultModel: d.Model.Name, DefaultProvider: d.Model.Provider}
	if !clientOnly {
		det.HermesHome, det.HermesCommand = d.HermesHome, d.HermesCommand
	}
	if s.deps.StateSave != nil {
		raw, _ := json.Marshal(det)
		s.deps.StateSave("detected", raw)
		s.deps.StateSave("configured", json.RawMessage("true"))
	}
	s.mu.Lock()
	s.configured = true
	o := s.owned
	restart := false
	if !clientOnly && o != nil && !s.restarted && d.HermesHome != "" && ExpandPath(d.HermesHome) != o.spec.HermesHome {
		s.restarted = true
		s.homeOverride = ExpandPath(d.HermesHome)
		restart = true
	}
	s.mu.Unlock()
	s.publish(func(v *View) { v.Detected = det })

	if restart {
		s.deps.Logf("hermes: restarting the bridge with the detected Hermes home")
		s.StopOwned()
		s.spawn(ctx, set)
	}
	if !clientOnly && !*gatewayTried && set.AutoStartGateway && gatewayIdle(d.Gateway.Status) {
		*gatewayTried = true
		s.enqueue(job{path: "/gateway/start", body: map[string]any{}, refetch: true})
	}
}

// SessionList fetches the bridge's recent sessions for the chat panel. The
// caller owns the goroutine; the event loop stays free.
func (s *Service) SessionList(ctx context.Context) ([]Session, error) {
	b := s.currentBridge()
	if b == nil {
		return nil, ErrUnreachable
	}
	list, err := b.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(list))
	for _, item := range list {
		out = append(out, Session{ID: item.ID, Title: item.Title, Preview: item.Preview,
			StartedAt: secondsOf(item.StartedAt)})
	}
	return out, nil
}

// TestConnection is the settings panel's Test connection: /health, then the
// token-bearing /state, reported with Noctalia's result strings.
func (s *Service) TestConnection(ctx context.Context) string {
	b := s.currentBridge()
	if b == nil {
		return ResultUnreachable
	}
	if _, err := b.Health(ctx); err != nil {
		return connectionResult(err)
	}
	if _, err := b.State(ctx); err != nil {
		return connectionResult(err)
	}
	return ResultConnected
}

func connectionResult(err error) string {
	var bad *ErrBridge
	switch {
	case errors.As(err, &bad):
		return FailedResult(bad.Status)
	case errors.Is(err, ErrAuth):
		return ResultWrongToken
	case errors.Is(err, ErrUnreachable):
		return ResultUnreachable
	default:
		return ResultUnreachable
	}
}

// secondsOf decodes started_at, which the bridge may send as a number or a
// numeric string.
func secondsOf(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if n, err := strconv.ParseFloat(text, 64); err == nil {
			return n
		}
	}
	return 0
}

// --- action worker ---------------------------------------------------------

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.jobs:
			s.runJob(j)
			s.mu.Lock()
			s.pending--
			pending := s.pending > 0
			s.mu.Unlock()
			s.publish(func(v *View) { v.Pending = pending })
		}
	}
}

func (s *Service) runJob(j job) {
	if j.approval != nil {
		s.runApproval(*j.approval)
		return
	}
	b := s.currentBridge()
	if b == nil {
		s.publish(func(v *View) { v.ActionError = MsgOffline })
		return
	}
	snap, err := b.Post(s.ctx, j.path, j.body)
	if err == nil && !snap.HasState && j.refetch {
		var st State
		if st, err = b.State(s.ctx); err == nil {
			snap.State, snap.HasState = st, true
		}
	}
	if err != nil {
		if s.ctx.Err() != nil {
			return
		}
		msg := actionMessage(j.path, err)
		if errors.Is(err, ErrAuth) {
			signal(s.tokenReload)
		}
		s.deps.Logf("hermes: %s: %v", j.path, err)
		s.publish(func(v *View) { v.ActionError = msg })
		return
	}
	s.publish(func(v *View) { v.ActionError = "" })
	if snap.HasState {
		s.publishState(snap.State)
	}
}

func actionMessage(path string, err error) string {
	var bad *ErrBadRequest
	switch {
	case errors.Is(err, ErrApprovalMismatch):
		return MsgApprovalChanged
	case errors.As(err, &bad) && bad.Code == "invalid_model":
		return MsgInvalidModel
	case path == "/gateway/start":
		return MsgGatewayFailed
	case errors.Is(err, ErrAuth):
		return MsgWrongToken
	case errors.Is(err, ErrUnreachable):
		return MsgOffline
	default:
		return MsgBridgeError
	}
}

var _ io.Closer = (*bridgeLog)(nil)
