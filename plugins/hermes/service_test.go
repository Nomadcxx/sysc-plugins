package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// --- fakes -----------------------------------------------------------------

type request struct {
	Method, Path, Token string
	Body                string
	At                  time.Time
}

// fakeBridge is an httptest stand-in for the Hermes bridge. Tokens here are
// fakes; no live token is ever used.
type fakeBridge struct {
	t    *testing.T
	srv  *httptest.Server
	host string
	port int

	online atomic.Bool

	mu        sync.Mutex
	token     string
	reqs      []request
	running   bool
	hermes    string
	gateway   string
	approval  *Approval
	stateFail int // fail this many /state calls with 500
	detect    Detect
	handlers  map[string]http.HandlerFunc
}

func newFakeBridge(t *testing.T, token string) *fakeBridge {
	t.Helper()
	fb := &fakeBridge{t: t, token: token, hermes: "idle", gateway: "running", handlers: map[string]http.HandlerFunc{},
		detect: Detect{Gateway: DetectGateway{Status: "running"}}}
	fb.srv = httptest.NewServer(http.HandlerFunc(fb.serve))
	t.Cleanup(fb.srv.Close)
	host, port, _ := net.SplitHostPort(fb.srv.Listener.Addr().String())
	fb.host = host
	fb.port, _ = strconv.Atoi(port)
	return fb
}

func (fb *fakeBridge) set(f func(fb *fakeBridge)) {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	f(fb)
}

func (fb *fakeBridge) handle(path string, h http.HandlerFunc) {
	fb.set(func(fb *fakeBridge) { fb.handlers[path] = h })
}

func (fb *fakeBridge) stateJSON() map[string]any {
	st := map[string]any{
		"bridge":  map[string]any{"status": "online"},
		"hermes":  map[string]any{"status": fb.hermes, "model": "m1"},
		"session": map[string]any{"id": "s1", "running": fb.running},
		"summary": map[string]any{"gateway": map[string]any{"status": fb.gateway}},
	}
	if fb.approval != nil {
		st["approval"] = fb.approval
	}
	return st
}

func (fb *fakeBridge) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	fb.mu.Lock()
	fb.reqs = append(fb.reqs, request{r.Method, r.URL.Path, r.Header.Get("X-Bridge-Token"), string(body), time.Now()})
	h := fb.handlers[r.URL.Path]
	fb.mu.Unlock()
	if h != nil {
		h(w, r)
		return
	}
	write := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.URL.Path == "/health" {
		if !fb.online.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		write(map[string]any{"bridge": map[string]any{"status": "online"}})
		return
	}
	if r.URL.Path == "/detect" {
		fb.mu.Lock()
		d := fb.detect
		fb.mu.Unlock()
		write(d)
		return
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if r.Header.Get("X-Bridge-Token") != fb.token {
		w.WriteHeader(http.StatusForbidden)
		write(map[string]any{"error": "forbidden"})
		return
	}
	switch r.URL.Path {
	case "/state":
		if fb.stateFail > 0 {
			fb.stateFail--
			w.WriteHeader(http.StatusInternalServerError)
			write(map[string]any{"error": "boom"})
			return
		}
		write(fb.stateJSON())
	case "/gateway/start":
		fb.gateway = "running"
		write(map[string]any{"result": map[string]any{"ok": true}})
	default:
		write(map[string]any{"result": map[string]any{"ok": true}, "state": fb.stateJSON()})
	}
}

func (fb *fakeBridge) requests() []request {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]request(nil), fb.reqs...)
}

func (fb *fakeBridge) count(path string) int {
	n := 0
	for _, r := range fb.requests() {
		if r.Path == path {
			n++
		}
	}
	return n
}

type fakeProc struct {
	pid  int
	done chan struct{}
	once sync.Once
	code int

	mu         sync.Mutex
	signals    []os.Signal
	termExitIn time.Duration // exit this long after SIGTERM; <0 ignores TERM
	exitedAt   time.Time
}

func (p *fakeProc) Pid() int              { return p.pid }
func (p *fakeProc) Done() <-chan struct{} { return p.done }
func (p *fakeProc) ExitCode() int         { <-p.done; return p.code }

func (p *fakeProc) exit(code int) {
	p.once.Do(func() {
		p.mu.Lock()
		p.code = code
		p.exitedAt = time.Now()
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *fakeProc) Signal(sig os.Signal) error {
	select {
	case <-p.done:
		return os.ErrProcessDone
	default:
	}
	p.mu.Lock()
	p.signals = append(p.signals, sig)
	delay := p.termExitIn
	p.mu.Unlock()
	switch {
	case sig == syscall.SIGKILL:
		p.exit(137)
	case delay >= 0:
		go func() { time.Sleep(delay); p.exit(143) }()
	}
	return nil
}

func (p *fakeProc) gotSignals() []os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]os.Signal(nil), p.signals...)
}

type fakeStarter struct {
	mu      sync.Mutex
	specs   []SpawnSpec
	procs   []*fakeProc
	onStart func(n int, spec SpawnSpec, p *fakeProc) error
}

func (s *fakeStarter) Start(spec SpawnSpec) (Process, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	n := len(s.specs)
	p := &fakeProc{pid: 1000 + n, done: make(chan struct{})}
	s.procs = append(s.procs, p)
	hook := s.onStart
	s.mu.Unlock()
	if hook != nil {
		if err := hook(n, spec, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (s *fakeStarter) started() ([]SpawnSpec, []*fakeProc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SpawnSpec(nil), s.specs...), append([]*fakeProc(nil), s.procs...)
}

// hostState stands in for the plugin state store and the notifier.
type hostState struct {
	mu       sync.Mutex
	values   map[string]json.RawMessage
	notified []string
}

func (h *hostState) get(_ context.Context, key string) (json.RawMessage, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.values[key]
	return v, ok, nil
}

func (h *hostState) save(key string, value json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.values == nil {
		h.values = map[string]json.RawMessage{}
	}
	h.values[key] = value
}

func (h *hostState) notify(summary, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notified = append(h.notified, summary+"|"+body)
}

func (h *hostState) value(key string) (json.RawMessage, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.values[key]
	return v, ok
}

func (h *hostState) notifications() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.notified...)
}

// --- harness ---------------------------------------------------------------

type harness struct {
	t       *testing.T
	svc     *Service
	local   *fakeBridge
	starter *fakeStarter
	host    *hostState
	cache   string
	dir     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	h := &harness{t: t, local: newFakeBridge(t, "local-tok"), starter: &fakeStarter{},
		host: &hostState{}, cache: cache, dir: t.TempDir()}
	h.svc = NewService(Deps{
		Starter:   h.starter,
		Notify:    h.host.notify,
		StateGet:  h.host.get,
		StateSave: h.host.save,
		Logf:      t.Logf,
	}, Options{
		LocalHost:     h.local.host,
		LocalPort:     h.local.port,
		RunningPoll:   30 * time.Millisecond,
		Backoff:       []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond},
		ProbeInterval: 10 * time.Millisecond,
		ProbeTimeout:  80 * time.Millisecond,
		StopGrace:     200 * time.Millisecond,
		WatchPoll:     20 * time.Millisecond,
	})
	t.Cleanup(func() { h.svc.Shutdown(2 * time.Second) })
	return h
}

func (h *harness) stateFile() string { return filepath.Join(h.dir, "state", "state.json") }

func (h *harness) localSettings() Settings {
	s := DefaultSettings()
	s.StateFile = h.stateFile()
	s.HermesHome = "/h"
	s.HermesCommand = "hermes"
	s.PythonCommand = "python3"
	s.BridgeScript = "/opt/hermes/scripts/hermes_bridge.py"
	s.PollInterval = 30 * time.Millisecond
	s.AutoStartGateway = false
	return s
}

func (h *harness) clientSettings(fb *fakeBridge, pasted string) Settings {
	s := h.localSettings()
	s.ClientOnly = true
	s.Host = fb.host
	s.Port = fb.port
	s.TokenManual = pasted
	s.TokenFile = filepath.Join(h.dir, "no-such.token")
	return s
}

func (h *harness) writeToken(path, value string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) besideState() string { return filepath.Join(h.dir, "state", "bridge.token") }

func (h *harness) waitFor(what string, ok func(View) bool) View {
	h.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		v := h.svc.Snapshot()
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s; last view: mode=%s status=%s message=%q warning=%q owned=%v adopted=%v",
				what, v.Mode, v.Status.Key, v.Message, v.Warning, v.Owned, v.Adopted)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- settings --------------------------------------------------------------

func TestSettingsFromValues(t *testing.T) {
	s := SettingsFromValues(map[string]any{
		"client_only_mode":     true,
		"bridge_host":          "192.168.0.10",
		"bridge_port":          float64(19778),
		"bridge_token_manual":  "pasted",
		"status_poll_interval": float64(5),
		"auto_start_bridge":    false,
		"notify_on_complete":   true,
		"python_command":       "",
	})
	if !s.ClientOnly || s.Host != "192.168.0.10" || s.Port != 19778 || s.TokenManual != "pasted" {
		t.Fatalf("connection fields: %+v", s.ConnectionKey())
	}
	if s.PollInterval != 5*time.Second || s.AutoStartBridge || !s.NotifyOnComplete {
		t.Fatalf("poll=%s autostart=%v complete=%v", s.PollInterval, s.AutoStartBridge, s.NotifyOnComplete)
	}
	// Blank strings and missing keys fall back to the manifest defaults.
	d := DefaultSettings()
	if s.PythonCommand != "python3" || s.StateFile != d.StateFile || s.HermesHome != "~/.hermes" ||
		s.TokenFile != DefaultTokenFile || !s.AutoStartGateway || !s.NotifyOnApproval {
		t.Fatalf("defaults not applied: python=%q state=%q home=%q", s.PythonCommand, s.StateFile, s.HermesHome)
	}
	if d.PollInterval != 30*time.Second || d.ClientOnly || d.Host != LocalHost || d.Port != LocalPort {
		t.Fatalf("manifest defaults: %+v", d.ConnectionKey())
	}
	// A poll interval below one second is clamped.
	if got := SettingsFromValues(map[string]any{"status_poll_interval": float64(0)}).PollInterval; got != time.Second {
		t.Fatalf("poll clamp = %s", got)
	}
}

// --- local mode ------------------------------------------------------------

func TestSettingsNeverLeaksThePastedToken(t *testing.T) {
	const pasted = "fake-settings-token-9f3a"
	s := DefaultSettings()
	s.TokenManual = pasted
	for _, text := range []string{
		fmt.Sprint(s),
		fmt.Sprintf("%v %+v %#v %s %q", s, s, s, s, s),
		fmt.Sprintf("%v %+v %#v", &s, &s, &s),
	} {
		if strings.Contains(text, pasted) {
			t.Fatalf("formatted Settings %q contains the pasted token", text)
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), pasted) {
		t.Fatalf("json.Marshal(Settings) includes the pasted token: %s", data)
	}
}

func TestLocalSpawnsOnceWithTheExactArgvWhenNoBridge(t *testing.T) {
	h := newHarness(t)
	h.writeToken(h.besideState(), "local-tok")
	h.starter.onStart = func(n int, spec SpawnSpec, p *fakeProc) error {
		h.local.online.Store(true)
		return nil
	}
	h.svc.Configure(h.localSettings())

	h.waitFor("owned bridge online", func(v View) bool { return v.Owned && v.Status.Key == StatusIdle })
	time.Sleep(150 * time.Millisecond)
	specs, _ := h.starter.started()
	if len(specs) != 1 {
		t.Fatalf("spawned %d times, want once", len(specs))
	}
	want := []string{"python3", "/opt/hermes/scripts/hermes_bridge.py",
		"--host", "127.0.0.1", "--port", "19777",
		"--state-file", h.stateFile(), "--hermes-home", "/h", "--hermes-command", "hermes"}
	if got := specs[0].Argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
	if specs[0].LogPath != filepath.Join(h.cache, "sysc-hermes", "bridge.log") {
		t.Fatalf("log path = %q", specs[0].LogPath)
	}
	if v := h.svc.Snapshot(); v.Adopted || v.Mode != ModeLocal {
		t.Fatalf("owned bridge reported adopted=%v mode=%s", v.Adopted, v.Mode)
	}
}

func TestLocalAdoptsAHealthyBridgeAndNeverSpawns(t *testing.T) {
	h := newHarness(t)
	h.local.online.Store(true)
	h.writeToken(h.besideState(), "local-tok")
	h.svc.Configure(h.localSettings())

	h.waitFor("adopted", func(v View) bool { return v.Adopted && v.Status.Key == StatusIdle })
	time.Sleep(150 * time.Millisecond)
	if specs, _ := h.starter.started(); len(specs) != 0 {
		t.Fatalf("spawned %d times next to a healthy bridge", len(specs))
	}
	// Local mode health-checks every tick.
	if n := h.local.count("/health"); n < 3 {
		t.Fatalf("/health probed %d times over several ticks", n)
	}
}

func TestLocalGivesUpAfterThreeFailedStarts(t *testing.T) {
	h := newHarness(t)
	h.svc.Configure(h.localSettings())

	h.waitFor("failed to start", func(v View) bool { return v.Message == MsgFailedToStart })
	time.Sleep(200 * time.Millisecond)
	specs, procs := h.starter.started()
	if len(specs) != 3 {
		t.Fatalf("spawn attempts = %d, want 3", len(specs))
	}
	for i, p := range procs {
		if len(p.gotSignals()) == 0 {
			t.Fatalf("attempt %d's child was left running", i+1)
		}
	}
	if v := h.svc.Snapshot(); v.Status.Key != StatusOffline || v.Owned {
		t.Fatalf("status=%s owned=%v after giving up", v.Status.Key, v.Owned)
	}

	// Re-enabling auto start allows three more attempts.
	s := h.localSettings()
	s.AutoStartBridge = false
	h.svc.Configure(s)
	time.Sleep(60 * time.Millisecond)
	h.svc.Configure(h.localSettings())
	waitUntil(t, "three more attempts", func() bool { specs, _ := h.starter.started(); return len(specs) == 6 })
}

func TestLocalShowsTheBridgeExitCode(t *testing.T) {
	h := newHarness(t)
	h.writeToken(h.besideState(), "local-tok")
	h.starter.onStart = func(n int, spec SpawnSpec, p *fakeProc) error {
		if n > 1 {
			return fmt.Errorf("no more")
		}
		h.local.online.Store(true)
		return nil
	}
	h.svc.Configure(h.localSettings())
	h.waitFor("owned", func(v View) bool { return v.Owned && v.Status.Key == StatusIdle })

	_, procs := h.starter.started()
	h.local.online.Store(false)
	procs[0].exit(2)
	h.waitFor("exit code", func(v View) bool { return v.Message == "Bridge exited: 2" })
	if v := h.svc.Snapshot(); v.Owned || v.Status.Key != StatusOffline {
		t.Fatalf("owned=%v status=%s after the child exited", v.Owned, v.Status.Key)
	}
}

func TestStopOwnedSignalsOnlyTheOwnedChild(t *testing.T) {
	t.Run("owned", func(t *testing.T) {
		h := newHarness(t)
		h.writeToken(h.besideState(), "local-tok")
		h.starter.onStart = func(n int, _ SpawnSpec, _ *fakeProc) error { h.local.online.Store(true); return nil }
		h.svc.Configure(h.localSettings())
		h.waitFor("owned", func(v View) bool { return v.Owned })

		h.svc.StopOwned()
		_, procs := h.starter.started()
		if got := procs[0].gotSignals(); len(got) == 0 || got[0] != syscall.SIGTERM {
			t.Fatalf("owned child got %v, want SIGTERM first", got)
		}
		select {
		case <-procs[0].Done():
		default:
			t.Fatal("StopOwned returned before the child exited")
		}
	})
	t.Run("ignores-term", func(t *testing.T) {
		h := newHarness(t)
		h.writeToken(h.besideState(), "local-tok")
		h.starter.onStart = func(n int, _ SpawnSpec, p *fakeProc) error {
			p.termExitIn = -1
			h.local.online.Store(true)
			return nil
		}
		h.svc.Configure(h.localSettings())
		h.waitFor("owned", func(v View) bool { return v.Owned })
		start := time.Now()
		h.svc.StopOwned()
		_, procs := h.starter.started()
		if got := procs[0].gotSignals(); !reflect.DeepEqual(got, []os.Signal{syscall.SIGTERM, syscall.SIGKILL}) {
			t.Fatalf("signals = %v, want TERM then KILL", got)
		}
		if time.Since(start) < 200*time.Millisecond {
			t.Fatal("SIGKILL sent before the grace period")
		}
	})
	t.Run("adopted", func(t *testing.T) {
		h := newHarness(t)
		h.local.online.Store(true)
		h.writeToken(h.besideState(), "local-tok")
		h.svc.Configure(h.localSettings())
		h.waitFor("adopted", func(v View) bool { return v.Adopted })
		h.svc.StopOwned()
		if specs, procs := h.starter.started(); len(specs) != 0 || len(procs) != 0 {
			t.Fatal("an adopted bridge was treated as owned")
		}
		if !h.local.online.Load() || !h.svc.Snapshot().Adopted {
			t.Fatal("adopted bridge disturbed")
		}
	})
}

func TestSwitchToClientOnlyStopsTheOwnedChildFirst(t *testing.T) {
	h := newHarness(t)
	h.writeToken(h.besideState(), "local-tok")
	h.starter.onStart = func(n int, _ SpawnSpec, p *fakeProc) error {
		p.termExitIn = 100 * time.Millisecond
		h.local.online.Store(true)
		return nil
	}
	s := h.localSettings()
	s.AutoStartGateway = true
	h.svc.Configure(s)
	h.waitFor("owned", func(v View) bool { return v.Owned && v.Status.Key == StatusIdle })

	remote := newFakeBridge(t, "remote-tok")
	h.svc.Configure(h.clientSettings(remote, "remote-tok"))
	h.waitFor("client-only online", func(v View) bool { return v.Mode == ModeClientOnly && v.Status.Key == StatusIdle })

	_, procs := h.starter.started()
	procs[0].mu.Lock()
	exited := procs[0].exitedAt
	procs[0].mu.Unlock()
	if exited.IsZero() {
		t.Fatal("owned child still running after the switch")
	}
	first := remote.requests()[0]
	if first.At.Before(exited) {
		t.Fatalf("first client-only request %s came before the child exited", first.At.Sub(exited))
	}
	for _, fb := range []*fakeBridge{h.local, remote} {
		if n := fb.count("/gateway/stop"); n != 0 {
			t.Fatalf("/gateway/stop sent %d times", n)
		}
	}
	if v := h.svc.Snapshot(); v.Owned || v.Warning != "" {
		t.Fatalf("owned=%v warning=%q after switching", v.Owned, v.Warning)
	}
}

func TestLocalStateFileWatch(t *testing.T) {
	h := newHarness(t)
	h.local.online.Store(true)
	h.writeToken(h.besideState(), "local-tok")
	h.svc.Configure(h.localSettings())
	h.waitFor("adopted", func(v View) bool { return v.Adopted && v.Status.Key == StatusIdle })

	replaceFile(t, h.stateFile(), `{"bridge":{"status":"online"},"hermes":{"status":"busy"},"session":{"running":true}}`)
	h.waitFor("busy from the state file", func(v View) bool { return v.Status.Key == StatusBusy })

	replaceFile(t, h.stateFile(), `{"bridge":`)
	h.waitFor("invalid state file", func(v View) bool { return v.Message == "Invalid state file" })

	replaceFile(t, h.stateFile(), `{"bridge":{"status":"online"},"hermes":{"status":"idle"}}`)
	h.waitFor("recovered", func(v View) bool { return v.Message == "" && v.Status.Key == StatusIdle })

	// The bridge rotates its token: the watch reloads it.
	h.local.set(func(fb *fakeBridge) { fb.token = "local-tok-2" })
	h.writeToken(h.besideState(), "local-tok-2")
	waitUntil(t, "new token accepted", func() bool {
		for _, r := range h.local.requests() {
			if r.Path == "/state" && r.Token == "local-tok-2" {
				return true
			}
		}
		return false
	})
	h.svc.Submit("/interrupt", map[string]any{})
	waitUntil(t, "action with the new token", func() bool {
		for _, r := range h.local.requests() {
			if r.Path == "/interrupt" && r.Token == "local-tok-2" {
				return true
			}
		}
		return false
	})
}

func TestLocalTokenCandidates(t *testing.T) {
	t.Run("noctalia-dir", func(t *testing.T) {
		h := newHarness(t)
		h.local.online.Store(true)
		noctalia := filepath.Join(h.cache, "noctalia-hermes", "bridge.token")
		h.writeToken(noctalia, "local-tok")
		h.svc.Configure(h.localSettings())
		v := h.waitFor("noctalia token", func(v View) bool { return v.TokenPath != "" })
		if v.TokenPath != noctalia || v.Token != TokenLocal {
			t.Fatalf("token = %s %q, want local %q", v.Token, v.TokenPath, noctalia)
		}
	})
	t.Run("403-moves-on", func(t *testing.T) {
		h := newHarness(t)
		h.local.online.Store(true)
		h.writeToken(h.besideState(), "stale")
		h.writeToken(filepath.Join(h.cache, "sysc-hermes", "bridge.token"), "also-stale")
		noctalia := filepath.Join(h.cache, "noctalia-hermes", "bridge.token")
		h.writeToken(noctalia, "local-tok")
		h.svc.Configure(h.localSettings())
		v := h.waitFor("third candidate", func(v View) bool { return v.TokenPath != "" })
		if v.TokenPath != noctalia {
			t.Fatalf("kept %q, want %q", v.TokenPath, noctalia)
		}
		if v.Status.Key != StatusIdle {
			t.Fatalf("status = %s", v.Status.Key)
		}
	})
	t.Run("none-accepted", func(t *testing.T) {
		h := newHarness(t)
		h.local.online.Store(true)
		h.writeToken(h.besideState(), "stale")
		h.svc.Configure(h.localSettings())
		h.waitFor("wrong token", func(v View) bool { return v.Message == MsgWrongToken && v.Status.Key == StatusError })
	})
}

func TestAutoConfigure(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		h := newHarness(t)
		h.writeToken(h.besideState(), "local-tok")
		h.local.set(func(fb *fakeBridge) {
			fb.detect = Detect{HermesHome: "/detected/home", HermesCommand: "hermes-x",
				Model: DetectModel{Name: "m-det", Provider: "p-det"}, Gateway: DetectGateway{Status: "running"}}
		})
		h.starter.onStart = func(n int, _ SpawnSpec, _ *fakeProc) error { h.local.online.Store(true); return nil }
		h.svc.Configure(h.localSettings())

		waitUntil(t, "configured flag", func() bool { _, ok := h.host.value("configured"); return ok })
		raw, _ := h.host.value("detected")
		var det map[string]string
		if err := json.Unmarshal(raw, &det); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"hermes_home": "/detected/home", "hermes_command": "hermes-x",
			"default_model": "m-det", "default_provider": "p-det"}
		if !reflect.DeepEqual(det, want) {
			t.Fatalf("detected = %v, want %v", det, want)
		}
		// The owned bridge restarts once with the detected Hermes home.
		waitUntil(t, "restart", func() bool { specs, _ := h.starter.started(); return len(specs) == 2 })
		h.waitFor("online again", func(v View) bool { return v.Owned && v.Status.Key == StatusIdle })
		time.Sleep(150 * time.Millisecond)
		specs, procs := h.starter.started()
		if len(specs) != 2 || specs[1].HermesHome != "/detected/home" {
			t.Fatalf("specs = %d, second home %q", len(specs), specs[len(specs)-1].HermesHome)
		}
		if len(procs[0].gotSignals()) == 0 {
			t.Fatal("first child not stopped before the restart")
		}
		if v := h.svc.Snapshot(); v.Detected.HermesHome != "/detected/home" || v.Detected.DefaultModel != "m-det" {
			t.Fatalf("view detected = %+v", v.Detected)
		}
		// Once per install: a reset with the flag set does not detect again.
		before := h.local.count("/detect")
		s := h.localSettings()
		s.HermesCommand = "hermes2"
		h.svc.Configure(s)
		time.Sleep(200 * time.Millisecond)
		if n := h.local.count("/detect"); n != before || before != 1 {
			t.Fatalf("/detect called %d then %d times, want once", before, n)
		}
	})
	t.Run("client-only", func(t *testing.T) {
		h := newHarness(t)
		remote := newFakeBridge(t, "remote-tok")
		remote.set(func(fb *fakeBridge) {
			fb.detect = Detect{HermesHome: "/remote/home", HermesCommand: "/remote/hermes",
				Model: DetectModel{Name: "m-r", Provider: "p-r"}}
		})
		h.svc.Configure(h.clientSettings(remote, "remote-tok"))
		waitUntil(t, "configured flag", func() bool { _, ok := h.host.value("configured"); return ok })
		raw, _ := h.host.value("detected")
		var det map[string]string
		_ = json.Unmarshal(raw, &det)
		if !reflect.DeepEqual(det, map[string]string{"default_model": "m-r", "default_provider": "p-r"}) {
			t.Fatalf("client-only recorded %v, want only model/provider", det)
		}
	})
}

func TestGatewayAutoStart(t *testing.T) {
	for _, status := range []string{"stopped", "offline", "unknown", ""} {
		t.Run("local-"+strconv.Quote(status), func(t *testing.T) {
			h := newHarness(t)
			h.local.online.Store(true)
			h.writeToken(h.besideState(), "local-tok")
			h.local.set(func(fb *fakeBridge) { fb.gateway = status; fb.detect.Gateway.Status = status })
			s := h.localSettings()
			s.AutoStartGateway = true
			h.svc.Configure(s)
			waitUntil(t, "gateway start", func() bool { return h.local.count("/gateway/start") == 1 })
			// Refetched after the start.
			h.waitFor("running gateway", func(v View) bool { return v.State.Summary.Gateway.Status == "running" })
			h.local.set(func(fb *fakeBridge) { fb.gateway = status })
			time.Sleep(200 * time.Millisecond)
			if n := h.local.count("/gateway/start"); n != 1 {
				t.Fatalf("/gateway/start sent %d times, want once per connection", n)
			}
		})
	}
	t.Run("local-running", func(t *testing.T) {
		h := newHarness(t)
		h.local.online.Store(true)
		h.writeToken(h.besideState(), "local-tok")
		s := h.localSettings()
		s.AutoStartGateway = true
		h.svc.Configure(s)
		h.waitFor("adopted", func(v View) bool { return v.Status.Key == StatusIdle })
		time.Sleep(150 * time.Millisecond)
		if n := h.local.count("/gateway/start"); n != 0 {
			t.Fatalf("started a running gateway %d times", n)
		}
	})
	t.Run("client-only", func(t *testing.T) {
		h := newHarness(t)
		remote := newFakeBridge(t, "remote-tok")
		remote.set(func(fb *fakeBridge) { fb.gateway = "stopped"; fb.detect.Gateway.Status = "stopped" })
		s := h.clientSettings(remote, "remote-tok")
		s.AutoStartGateway = true
		h.svc.Configure(s)
		h.waitFor("client-only state", func(v View) bool { return v.Status.Key == StatusIdle })
		h.svc.Submit("/gateway/start", map[string]any{})
		h.svc.Submit("/gateway/stop", map[string]any{})
		time.Sleep(200 * time.Millisecond)
		if n := remote.count("/gateway/start") + remote.count("/gateway/stop"); n != 0 {
			t.Fatalf("client-only sent %d gateway calls", n)
		}
	})
}

// --- client-only mode ------------------------------------------------------

func TestClientOnlyNeverStartsAndPollsStateOnly(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	s := h.clientSettings(remote, "remote-tok")
	s.PollInterval = time.Hour
	h.svc.Configure(s)
	h.waitFor("idle", func(v View) bool { return v.Status.Key == StatusIdle && v.Mode == ModeClientOnly })
	time.Sleep(150 * time.Millisecond)
	if n := remote.count("/state"); n != 1 {
		t.Fatalf("idle polled /state %d times inside an hour-long interval", n)
	}

	remote.set(func(fb *fakeBridge) { fb.running = true })
	h.svc.Refresh()
	h.waitFor("busy", func(v View) bool { return v.Status.Key == StatusBusy })
	before := remote.count("/state")
	time.Sleep(300 * time.Millisecond)
	if n := remote.count("/state") - before; n < 5 {
		t.Fatalf("running session polled %d times in 300 ms at a 30 ms running poll", n)
	}
	for _, r := range remote.requests() {
		if r.Path != "/state" && r.Path != "/detect" {
			t.Fatalf("client-only sent %s %s", r.Method, r.Path)
		}
	}
	if specs, _ := h.starter.started(); len(specs) != 0 || h.local.count("/health") != 0 {
		t.Fatal("client-only touched the local bridge or the starter")
	}
}

func TestClientOnlyBacksOffAndRecovers(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	remote.set(func(fb *fakeBridge) { fb.stateFail = 3 })
	s := h.clientSettings(remote, "remote-tok")
	s.PollInterval = time.Hour
	h.svc.Configure(s)
	h.waitFor("recovered", func(v View) bool { return v.Status.Key == StatusIdle && v.Message == "" })

	var times []time.Time
	for _, r := range remote.requests() {
		if r.Path == "/state" {
			times = append(times, r.At)
		}
	}
	if len(times) != 4 {
		t.Fatalf("/state calls = %d, want 3 failures and 1 success", len(times))
	}
	for i, want := range []time.Duration{20, 40, 80} {
		if gap := times[i+1].Sub(times[i]); gap < want*time.Millisecond {
			t.Fatalf("backoff %d = %s, want >= %dms", i, gap, want)
		}
	}
	if d := DefaultOptions().Backoff; !reflect.DeepEqual(d, []time.Duration{time.Second, 2 * time.Second,
		4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second}) {
		t.Fatalf("default backoff = %v", d)
	}
	if DefaultOptions().RunningPoll != 1500*time.Millisecond {
		t.Fatal("default running poll is not 1.5 s")
	}
}

func TestClientOnly403ShowsTheTokenMessage(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	h.svc.Configure(h.clientSettings(remote, ""))
	h.waitFor("no token", func(v View) bool { return v.Message == MsgNoToken && v.Status.Key == StatusError })

	h.svc.Configure(h.clientSettings(remote, "wrong"))
	h.waitFor("wrong token", func(v View) bool { return v.Message == MsgWrongToken && v.Status.Key == StatusError })

	h.svc.Configure(h.clientSettings(remote, "remote-tok"))
	h.waitFor("accepted", func(v View) bool { return v.Message == "" && v.Status.Key == StatusIdle && v.Token == TokenPasted })
}

func TestConnectionErrorsReachTheBarAndSummary(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	const wrongToken = "d8-secret-tok-9c1f"

	textOf := func(n *v1.Node) string {
		var b strings.Builder
		walk(n, func(n *v1.Node) {
			if n.Kind == v1.KindText {
				b.WriteString(n.Text)
				b.WriteByte('\n')
			}
		})
		return b.String()
	}
	render := func(v View) (tooltip, status, summary string) {
		state := BarState{Status: v.Status, BridgeError: v.State.Bridge.Error, Model: "t11-stub"}
		return textOf(TooltipTree(state)),
			ServiceStatusFor(state, 1).Message,
			textOf(SummaryTree(SummaryState{BarState: state}, "", 360, 420))
	}
	assertShows := func(where, text, want string) {
		t.Helper()
		if !strings.Contains(text, want) {
			t.Errorf("%s = %q, want it to contain %q", where, text, want)
		}
	}

	h.svc.Configure(h.clientSettings(remote, ""))
	v := h.waitFor("no token", func(v View) bool {
		return v.Message == MsgNoToken && v.Status.Key == StatusError
	})
	tooltip, status, summary := render(v)
	assertShows("tooltip without a token", tooltip, "No bridge token")
	assertShows("service status without a token", status, MsgNoToken)
	assertShows("summary without a token", summary, "No bridge token")

	h.svc.Configure(h.clientSettings(remote, wrongToken))
	v = h.waitFor("wrong token", func(v View) bool {
		return v.Message == MsgWrongToken && v.Status.Key == StatusError
	})
	tooltip, status, summary = render(v)
	assertShows("tooltip with a wrong token", tooltip, MsgWrongToken)
	assertShows("service status with a wrong token", status, MsgWrongToken)
	assertShows("summary with a wrong token", summary, MsgWrongToken)
	for where, text := range map[string]string{"tooltip": tooltip, "service status": status, "summary": summary} {
		if strings.Contains(text, wrongToken) {
			t.Fatalf("%s leaked the token value: %q", where, text)
		}
	}

	h.svc.Configure(h.clientSettings(remote, "remote-tok"))
	v = h.waitFor("accepted", func(v View) bool {
		return v.Message == "" && v.Status.Key == StatusIdle
	})
	tooltip, status, summary = render(v)
	for where, text := range map[string]string{"tooltip": tooltip, "service status": status, "summary": summary} {
		if strings.Contains(text, MsgWrongToken) || strings.Contains(text, "No bridge token") {
			t.Fatalf("%s kept the old connection error: %q", where, text)
		}
	}
}

func TestClientOnlyWarnings(t *testing.T) {
	t.Run("collision", func(t *testing.T) {
		h := newHarness(t)
		h.local.online.Store(true)
		h.writeToken(h.besideState(), "local-tok")
		h.svc.Configure(h.localSettings())
		h.waitFor("adopted", func(v View) bool { return v.Adopted })

		h.svc.Configure(h.clientSettings(h.local, "local-tok"))
		v := h.waitFor("collision", func(v View) bool { return v.Mode == ModeClientOnly && v.Warning != "" })
		if v.Warning != MsgCollision {
			t.Fatalf("warning = %q", v.Warning)
		}
		before := h.local.count("/state")
		h.svc.Refresh()
		waitUntil(t, "still polling", func() bool { return h.local.count("/state") > before })
	})
	t.Run("cleartext", func(t *testing.T) {
		h := newHarness(t)
		s := h.clientSettings(h.local, "x")
		s.Host = "192.0.2.10"
		h.svc.Configure(s)
		v := h.waitFor("cleartext", func(v View) bool { return v.Warning != "" })
		if want := "Cleartext HTTP to 192.0.2.10: the token and prompts are not encrypted."; v.Warning != want {
			t.Fatalf("warning = %q, want %q", v.Warning, want)
		}
	})
}

// --- actions ---------------------------------------------------------------

func TestPromptsReachTheBridgeInOrder(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	var order []string
	var mu sync.Mutex
	release := make(chan struct{})
	remote.handle("/prompt", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Text string }
		h.t.Helper()
		reqs := remote.requests()
		_ = json.Unmarshal([]byte(reqs[len(reqs)-1].Body), &body)
		mu.Lock()
		order = append(order, body.Text)
		first := len(order) == 1
		mu.Unlock()
		if first {
			<-release
		}
		w.Write([]byte(`{"result":{"ok":true}}`))
	})
	h.svc.Configure(h.clientSettings(remote, "remote-tok"))
	h.waitFor("idle", func(v View) bool { return v.Status.Key == StatusIdle })

	h.svc.Submit("/prompt", map[string]any{"text": "one"})
	h.svc.Submit("/prompt", map[string]any{"text": "two"})
	h.waitFor("pending flag", func(v View) bool { return v.Pending })
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(order) != 1 {
		t.Fatalf("second prompt raced the first: %v", order)
	}
	mu.Unlock()
	close(release)
	waitUntil(t, "both prompts", func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 2 })
	if !reflect.DeepEqual(order, []string{"one", "two"}) {
		t.Fatalf("order = %v", order)
	}
	h.waitFor("pending cleared", func(v View) bool { return !v.Pending })
}

func TestOneShotDoesNotBlockInterrupt(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	cancelled := make(chan struct{})
	remote.handle("/oneshot", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(cancelled)
	})
	h.svc.Configure(h.clientSettings(remote, "remote-tok"))
	h.waitFor("idle", func(v View) bool { return v.Status.Key == StatusIdle })

	h.svc.OneShot("long question")
	h.waitFor("one-shot running", func(v View) bool { return v.OneShotRunning })
	waitUntil(t, "one-shot reached the bridge", func() bool { return remote.count("/oneshot") == 1 })
	start := time.Now()
	h.svc.Submit("/interrupt", map[string]any{})
	waitUntil(t, "interrupt", func() bool { return remote.count("/interrupt") == 1 })
	if d := time.Since(start); d > time.Second {
		t.Fatalf("interrupt took %s behind the one-shot", d)
	}
	h.svc.CancelOneShot()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not end the one-shot request")
	}
	h.waitFor("one-shot cleared", func(v View) bool { return !v.OneShotRunning })
}

func TestActionErrorsAreMapped(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	remote.handle("/approval", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"approval_mismatch"}`))
	})
	remote.handle("/model", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"x","message":"Traceback remote-tok"}`))
	})
	h.svc.Configure(h.clientSettings(remote, "remote-tok"))
	h.waitFor("idle", func(v View) bool { return v.Status.Key == StatusIdle })
	h.svc.Submit("/approval", map[string]any{"choice": "once"})
	h.waitFor("approval changed", func(v View) bool { return v.ActionError == MsgApprovalChanged })
	h.svc.Submit("/model", map[string]any{"model": "m"})
	v := h.waitFor("bridge error", func(v View) bool { return v.ActionError == MsgBridgeError })
	if v.Status.Key != StatusIdle || v.Message != "" {
		t.Fatalf("an action error changed the connection status to %s", v.Status.Key)
	}
}

func TestNotifications(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	s := h.clientSettings(remote, "remote-tok")
	s.NotifyOnComplete = true
	remote.set(func(fb *fakeBridge) { fb.running = true })
	h.svc.Configure(s)
	h.waitFor("busy", func(v View) bool { return v.Status.Key == StatusBusy })

	remote.set(func(fb *fakeBridge) {
		fb.approval = &Approval{Pending: true, ToolName: "terminal", Message: "rm -rf build?"}
	})
	h.waitFor("attention", func(v View) bool { return v.Status.Key == StatusAttention })
	time.Sleep(100 * time.Millisecond) // several polls of the same approval
	remote.set(func(fb *fakeBridge) { fb.approval = nil; fb.running = false })
	h.waitFor("idle", func(v View) bool { return v.Status.Key == StatusIdle })
	time.Sleep(100 * time.Millisecond)

	got := h.host.notifications()
	want := []string{"Hermes needs approval|terminal: rm -rf build?", "Hermes finished|"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifications = %q, want %q", got, want)
	}
}

// --- shutdown --------------------------------------------------------------

func TestShutdownCancelsInFlightAndStopsTheOwnedChild(t *testing.T) {
	h := newHarness(t)
	h.writeToken(h.besideState(), "local-tok")
	h.starter.onStart = func(n int, _ SpawnSpec, _ *fakeProc) error { h.local.online.Store(true); return nil }
	inflight := make(chan struct{})
	cancelled := make(chan struct{})
	h.local.handle("/prompt", func(w http.ResponseWriter, r *http.Request) {
		close(inflight)
		<-r.Context().Done()
		close(cancelled)
	})
	h.svc.Configure(h.localSettings())
	h.waitFor("owned", func(v View) bool { return v.Owned && v.Status.Key == StatusIdle })
	h.svc.Submit("/prompt", map[string]any{"text": "hi"})
	<-inflight

	start := time.Now()
	h.svc.Shutdown(2 * time.Second)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("shutdown took %s", d)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("in-flight request not cancelled")
	}
	_, procs := h.starter.started()
	if got := procs[0].gotSignals(); len(got) == 0 || got[0] != syscall.SIGTERM {
		t.Fatalf("owned child got %v on shutdown", got)
	}
}

func TestLocalReloadsTheTokenAfterA403(t *testing.T) {
	h := newHarness(t)
	h.local.online.Store(true)
	// The Noctalia directory is not watched, so only the 403 path can
	// pick up its new token.
	noctalia := filepath.Join(h.cache, "noctalia-hermes", "bridge.token")
	h.writeToken(noctalia, "local-tok")
	s := h.localSettings()
	s.PollInterval = time.Hour
	h.svc.Configure(s)
	h.waitFor("adopted", func(v View) bool { return v.TokenPath == noctalia && v.Status.Key == StatusIdle })

	h.local.set(func(fb *fakeBridge) { fb.token = "local-tok-2" })
	h.writeToken(noctalia, "local-tok-2")
	h.svc.Submit("/interrupt", map[string]any{})
	h.waitFor("wrong token on the action", func(v View) bool { return v.ActionError == MsgWrongToken })
	waitUntil(t, "token reloaded", func() bool {
		for _, r := range h.local.requests() {
			if r.Path == "/state" && r.Token == "local-tok-2" {
				return true
			}
		}
		return false
	})
}

// TestOneShotFailureShowsTheActionError: a composer one-shot used to fail
// through the action queue, which showed the failure in the panel. Run off
// the queue, a failed one-shot must still say so.
func TestOneShotFailureShowsTheActionError(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	remote.handle("/oneshot", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"oneshot_failed"}`))
	})
	h.svc.Configure(h.clientSettings(remote, "remote-tok"))
	h.waitFor("idle", func(v View) bool { return v.Status.Key == StatusIdle })

	h.svc.OneShot("question")
	v := h.waitFor("one-shot failure", func(v View) bool { return !v.OneShotRunning && v.ActionError != "" })
	if v.ActionError != MsgOneShotFailed {
		t.Fatalf("action error = %q, want %q", v.ActionError, MsgOneShotFailed)
	}
}

// TestOneShotReplyShowsInTheTranscript: like noctalia, which publishes the
// state /oneshot returns (service.luau:236-242), the reply the bridge
// appended shows at once, without waiting for the next poll (R11).
func TestOneShotReplyShowsInTheTranscript(t *testing.T) {
	h := newHarness(t)
	remote := newFakeBridge(t, "remote-tok")
	remote.handle("/oneshot", func(w http.ResponseWriter, r *http.Request) {
		remote.mu.Lock()
		st := remote.stateJSON()
		remote.mu.Unlock()
		st["messages"] = []map[string]any{
			{"id": "m1", "role": "user", "text": "question"},
			{"id": "m2", "role": "assistant", "text": "the answer"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "completed"}, "state": st})
	})
	s := h.clientSettings(remote, "remote-tok")
	s.PollInterval = time.Hour
	h.svc.Configure(s)
	h.waitFor("idle", func(v View) bool { return v.Status.Key == StatusIdle })

	h.svc.OneShot("question")
	h.waitFor("the reply in the transcript", func(v View) bool {
		for _, m := range v.State.Messages {
			if m.Role == "assistant" && m.Text == "the answer" {
				return !v.OneShotRunning
			}
		}
		return false
	})
}
