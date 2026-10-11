package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	"github.com/Nomadcxx/sysc-plugins/plugins/aiusage"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type pluginHarness struct {
	t       *testing.T
	in      *os.File
	host    *os.File
	reader  *os.File
	out     *os.File
	encoder *v1.Encoder
	mu      sync.Mutex
	msgs    chan v1.Message
	done    chan error
	reply   func(*v1.HostCall) *v1.HostReply
	stopped bool
	closed  bool
}

func startPluginHarness(t *testing.T) *pluginHarness {
	return startPluginHarnessWithReply(t, func(call *v1.HostCall) *v1.HostReply {
		result := json.RawMessage(`{}`)
		if call.Call == v1.CallStateGet {
			result = json.RawMessage(`{"found":false}`)
		}
		return &v1.HostReply{ID: call.ID, OK: true, Result: result}
	})
}

func startPluginHarnessWithReply(t *testing.T, reply func(*v1.HostCall) *v1.HostReply) *pluginHarness {
	t.Helper()
	in, host, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader, out, err := os.Pipe()
	if err != nil {
		in.Close()
		host.Close()
		t.Fatal(err)
	}
	h := &pluginHarness{
		t: t, in: in, host: host, reader: reader, out: out,
		encoder: v1.NewEncoder(host), msgs: make(chan v1.Message, 32), done: make(chan error, 1),
		reply: reply,
	}
	go func() {
		err := run(in, out)
		_ = out.Close()
		h.done <- err
	}()
	go func() {
		defer close(h.msgs)
		dec := v1.NewDecoder(reader, v1.ToHost)
		for {
			m, err := dec.Decode()
			if err != nil {
				return
			}
			if call, ok := m.(*v1.HostCall); ok {
				if h.reply != nil {
					if reply := h.reply(call); reply != nil {
						_ = h.write(reply)
					}
				}
			}
			h.msgs <- m
		}
	}()
	if err := h.write(&v1.HostHello{Supported: []v1.Version{{Major: 1, Minor: 7}}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.next(5 * time.Second).(*v1.PluginHello); !ok {
		t.Fatal("missing plugin hello")
	}
	t.Cleanup(h.stop)
	return h
}

func TestPanelOpenDoesNotBlockInputLoopOnHostReply(t *testing.T) {
	originalDefault := hostcall.Default
	hostcall.Default = 400 * time.Millisecond
	defer func() { hostcall.Default = originalDefault }()

	panelCalls := make(chan *v1.HostCall, 1)
	h := startPluginHarnessWithReply(t, func(call *v1.HostCall) *v1.HostReply {
		if call.Call == v1.CallPanelOpen {
			panelCalls <- call
			return nil
		}
		result := json.RawMessage(`{}`)
		if call.Call == v1.CallStateGet {
			result = json.RawMessage(`{"found":false}`)
		}
		return &v1.HostReply{ID: call.ID, OK: true, Result: result}
	})
	h.send(&v1.ViewOpen{ViewID: "bar", View: v1.ViewBar, Entry: "bar", Instance: "placement-1"})
	bar := h.nextSnapshot("bar", time.Second)
	h.send(&v1.InputEvent{ViewID: "bar", Revision: bar.Revision, Node: "open", Event: v1.EventActivate})
	select {
	case <-panelCalls:
	case <-time.After(time.Second):
		t.Fatal("bar click did not call panel.open")
	}

	h.send(&v1.ViewOpen{ViewID: "canary", View: v1.ViewBar, Entry: "bar"})
	deadline := time.NewTimer(50 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case msg := <-h.msgs:
			if snapshot, ok := msg.(*v1.ViewSnapshot); ok && snapshot.ViewID == "canary" {
				return
			}
		case <-deadline.C:
			t.Fatal("opening a view waited for the panel.open reply")
		}
	}
}

func TestAlertLedgerReadDoesNotBlockViewProcessing(t *testing.T) {
	stateGets := make(chan struct{}, 1)
	h := startPluginHarnessWithReply(t, func(call *v1.HostCall) *v1.HostReply {
		if call.Call == v1.CallStateGet {
			stateGets <- struct{}{}
			return nil // Hold the reply while views continue through the owner loop.
		}
		return &v1.HostReply{ID: call.ID, OK: true, Result: json.RawMessage(`{}`)}
	})
	h.send(&v1.ViewOpen{ViewID: "first", View: v1.ViewBar, Entry: "bar"})
	select {
	case <-stateGets:
	case <-time.After(time.Second):
		t.Fatal("first view did not request the alert ledger")
	}
	h.send(&v1.ViewOpen{ViewID: "canary", View: v1.ViewBar, Entry: "bar"})
	h.nextSnapshot("canary", 100*time.Millisecond)
}

func (h *pluginHarness) write(m v1.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.encoder.Encode(m)
}

func (h *pluginHarness) send(m v1.Message) {
	h.t.Helper()
	if err := h.write(m); err != nil {
		h.t.Fatalf("send %T: %v", m, err)
	}
}

func (h *pluginHarness) next(timeout time.Duration) v1.Message {
	h.t.Helper()
	select {
	case m, ok := <-h.msgs:
		if !ok {
			h.t.Fatal("plugin output closed")
		}
		return m
	case <-time.After(timeout):
		h.t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

func (h *pluginHarness) nextSnapshot(id string, timeout time.Duration) *v1.ViewSnapshot {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Until(deadline) > 0 {
		m := h.next(time.Until(deadline))
		if snapshot, ok := m.(*v1.ViewSnapshot); ok && snapshot.ViewID == id {
			return snapshot
		}
	}
	h.t.Fatalf("timed out waiting for view %q", id)
	return nil
}

func (h *pluginHarness) stop() {
	if !h.stopped {
		h.stopped = true
		if err := h.write(&v1.HostShutdown{Type: "host.shutdown"}); err != nil {
			h.t.Errorf("send shutdown: %v", err)
		}
		select {
		case err := <-h.done:
			if err != nil {
				h.t.Errorf("run: %v", err)
			}
		case <-time.After(2 * time.Second):
			h.t.Error("plugin did not stop")
		}
	}
	if !h.closed {
		h.closed = true
		_ = h.host.Close()
		_ = h.reader.Close()
		_ = h.in.Close()
		_ = h.out.Close()
	}
}

func TestPanelActionsRunOncePerClick(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	h := startPluginHarness(t)
	values := map[string]any{}
	for _, key := range []string{"track_claude", "track_codex", "track_commandcode", "alerts_enabled"} {
		values[key] = false
	}
	h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: values})
	var calls []v1.HostCall
	waitView := func(id string) {
		t.Helper()
		for {
			switch m := h.next(5 * time.Second).(type) {
			case *v1.HostCall:
				if m.Call == v1.CallPanelOpen || m.Call == v1.CallPanelClose {
					calls = append(calls, *m)
				}
			case *v1.ViewSnapshot:
				if m.ViewID == id {
					return
				}
			}
		}
	}
	waitPanelCall := func() {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for len(calls) == 0 && time.Until(deadline) > 0 {
			switch m := h.next(time.Until(deadline)).(type) {
			case *v1.HostCall:
				if m.Call == v1.CallPanelOpen || m.Call == v1.CallPanelClose {
					calls = append(calls, *m)
				}
			}
		}
	}
	h.send(&v1.ViewOpen{ViewID: "bar", View: v1.ViewBar, Entry: "bar", Instance: "placement-1"})
	waitView("bar")
	for _, step := range []struct {
		node, source, target string
		kind                 v1.CallKind
	}{
		{"open", "bar", "panel", v1.CallPanelOpen},
		{"settings", "panel", "settings", v1.CallPanelOpen},
		{"back", "settings", "panel", v1.CallPanelOpen},
		{"settings", "panel", "settings", v1.CallPanelOpen},
		{"close", "settings", "settings", v1.CallPanelClose},
	} {
		calls = nil
		for _, event := range []v1.EventKind{v1.EventPointer, v1.EventActivate} {
			h.send(&v1.InputEvent{ViewID: step.source, Node: step.node, Event: event, Button: v1.ButtonPrimary, Output: "DP-1"})
		}
		// A later view is a processing barrier, without a sleep or an idle timeout.
		h.send(&v1.ViewOpen{ViewID: "barrier", View: v1.ViewBar, Entry: "bar"})
		waitView("barrier")
		waitPanelCall()
		h.send(&v1.ViewClose{ViewID: "barrier"})
		if len(calls) != 1 || calls[0].Call != step.kind {
			t.Fatalf("%s click made %d panel calls: %+v", step.node, len(calls), calls)
		}
		var params v1.PanelParams
		if err := json.Unmarshal(calls[0].Params, &params); err != nil || params.Entry != step.target {
			t.Fatalf("%s target = %+v, err=%v", step.node, params, err)
		}
		if step.kind == v1.CallPanelOpen {
			if step.source != "bar" {
				h.send(&v1.ViewClose{ViewID: step.source})
			}
			h.send(&v1.ViewOpen{ViewID: step.target, View: v1.ViewPanel, Entry: step.target, Instance: "placement-1"})
			waitView(step.target)
		}
	}
}

func TestRefreshWorkerKeepsOwnerResponsiveAndDiscardsStaleSettings(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	cachedAt := time.Now().Add(-7 * time.Minute)
	cached := aiusage.Report{CapturedAt: cachedAt, Providers: []aiusage.ProviderReport{{
		ID: "synthetic", Name: "Synthetic", State: aiusage.StateFresh, UpdatedAt: cachedAt,
		Windows: []aiusage.Window{{Key: "primary", Label: "Subscription", HasPercent: true,
			UsedPercent: 42, DisplayValue: "42 / 100 requests"}},
	}}}
	cachePath := filepath.Join(cacheHome, "sysc-shell", "plugins", "aiusage", "report.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	h := startPluginHarness(t)
	var requestCount atomic.Int32
	entered := make(chan int, 4)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var releaseOnce [2]sync.Once
	releaseRequest := func(index int) {
		releaseOnce[index].Do(func() { close(releases[index]) })
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusBadRequest)
			return
		}
		index := int(requestCount.Add(1)) - 1
		entered <- index + 1
		if index >= len(releases) {
			http.Error(w, "unexpected request", http.StatusBadGateway)
			return
		}
		select {
		case <-releases[index]:
			http.Error(w, "held request released", http.StatusBadGateway)
		case <-r.Context().Done():
		}
	}))
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Cleanup(func() {
		releaseRequest(0)
		releaseRequest(1)
		proxy.Close()
	})
	h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: syntheticSettings(true)})
	h.send(&v1.ViewOpen{ViewID: "panel", View: v1.ViewPanel, Entry: "panel", Width: 460, Height: 580})
	select {
	case request := <-entered:
		if request != 1 {
			t.Fatalf("first request = %d, want 1", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Synthetic collector did not reach the held proxy")
	}
	deadline := time.Now().Add(5 * time.Second)
	var initial *v1.ViewSnapshot
	for time.Until(deadline) > 0 {
		snapshot := h.nextSnapshot("panel", time.Until(deadline))
		button := findNode(snapshot.Root, "refresh")
		if hasText(snapshot.Root, "42%") {
			if button == nil || !button.Disabled {
				t.Fatalf("refresh button = %+v, want busy state while cached data remains visible", button)
			}
			initial = snapshot
			break
		}
	}
	if initial == nil {
		t.Fatal("last-good cached report was not visible while the collector was held")
	}

	h.send(&v1.InputEvent{ViewID: "panel", Node: "refresh", Event: v1.EventActivate})
	h.send(&v1.ViewOpen{ViewID: "barrier", View: v1.ViewBar, Entry: "bar"})
	if h.nextSnapshot("barrier", 5*time.Second) == nil {
		t.Fatal("input remained blocked by the held collector")
	}
	select {
	case request := <-entered:
		t.Fatalf("request %d overlapped the held refresh", request)
	case <-time.After(250 * time.Millisecond):
	}

	h.send(&v1.ViewClose{ViewID: "panel"})
	h.send(&v1.ViewOpen{ViewID: "panel", View: v1.ViewPanel, Entry: "panel", Width: 460, Height: 580})
	deadline = time.Now().Add(5 * time.Second)
	reopened := false
	for time.Until(deadline) > 0 {
		m := h.next(time.Until(deadline))
		if snapshot, ok := m.(*v1.ViewSnapshot); ok && snapshot.ViewID == "panel" && snapshot.Revision == 1 {
			reopened = true
			break
		}
	}
	if !reopened {
		t.Fatal("reopened view did not publish while collector was held")
	}
	if requestCount.Load() != 1 {
		t.Fatalf("requests while collector held = %d, want one serial fetch", requestCount.Load())
	}

	h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: syntheticSettings(false)})
	h.send(&v1.ViewResync{ViewID: "panel"})
	deadline = time.Now().Add(5 * time.Second)
	resynced := false
	for time.Until(deadline) > 0 {
		m := h.next(time.Until(deadline))
		if snapshot, ok := m.(*v1.ViewSnapshot); ok && snapshot.ViewID == "panel" && snapshot.Revision == 1 {
			resynced = true
			break
		}
	}
	if !resynced {
		t.Fatal("settings change blocked while collector was held")
	}
	releaseRequest(0)

	deadline = time.Now().Add(5 * time.Second)
	finished := false
	for time.Until(deadline) > 0 {
		m := h.next(time.Until(deadline))
		snapshot, ok := m.(*v1.ViewSnapshot)
		if !ok || snapshot.ViewID != "panel" {
			continue
		}
		if hasText(snapshot.Root, "Bad Gateway") {
			t.Fatal("old-config completion replaced the current report")
		}
		if button := findNode(snapshot.Root, "refresh"); button != nil && !button.Disabled {
			finished = true
			break
		}
	}
	if !finished {
		t.Fatal("latest settings refresh did not complete")
	}

	h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: syntheticSettings(true)})
	select {
	case request := <-entered:
		if request != 2 {
			t.Fatalf("second request = %d, want 2", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("re-enabled collector did not start")
	}
	h.send(&v1.HostShutdown{Type: "host.shutdown"})
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		h.stopped = true
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for the held collector")
	}
}

func syntheticSettings(enabled bool) map[string]any {
	values := map[string]any{}
	for _, id := range []string{
		"claude", "codex", "commandcode", "copilot", "minimax", "ollama",
		"opencode_go", "kimi", "zai", "deepseek", "alibaba",
	} {
		values["track_"+id] = false
	}
	values["track_synthetic"] = enabled
	values["synthetic_api_key"] = "fixture-key"
	values["alerts_enabled"] = false
	return values
}

func findNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

func hasText(root *v1.Node, text string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, text) {
		return true
	}
	for _, child := range root.Children {
		if hasText(child, text) {
			return true
		}
	}
	return false
}

func TestPanelEntryForActionSwitchesBetweenUsageAndSettings(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   string
	}{{"settings", "settings"}, {"back", "panel"}} {
		if got := panelEntryForAction(tc.action); got != tc.want {
			t.Errorf("panel entry for %q = %q, want %q", tc.action, got, tc.want)
		}
	}
}

func TestSwitchPanelOpensTargetWithoutClosingCurrentPanel(t *testing.T) {
	type call struct {
		kind   v1.CallKind
		params v1.PanelParams
	}
	var calls []call
	invoke := func(_ context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
		panel, ok := params.(v1.PanelParams)
		if !ok {
			t.Fatalf("params = %T, want v1.PanelParams", params)
		}
		calls = append(calls, call{kind: kind, params: panel})
		return v1.HostReply{OK: true}, nil
	}

	if err := switchPanel(context.Background(), invoke, "settings", v1.PanelParams{
		Entry: "panel", Instance: "placement-1", Output: "DP-1", Generation: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("host calls = %+v, want a single replacement open", calls)
	}
	if calls[0].kind != v1.CallPanelOpen || calls[0].params.Entry != "settings" {
		t.Fatalf("call = %+v, want open settings entry", calls[0])
	}
	for _, got := range calls {
		if got.params.Output != "DP-1" || got.params.Generation != 7 || got.params.Instance != "placement-1" {
			t.Errorf("call params = %+v, want output/generation/instance preserved", got.params)
		}
	}
}

func TestSwitchPanelReportsRejectedOpen(t *testing.T) {
	err := switchPanel(context.Background(), func(_ context.Context, _ v1.CallKind, _ any) (v1.HostReply, error) {
		return v1.HostReply{Error: "open rejected"}, nil
	}, "settings", v1.PanelParams{Entry: "panel"})
	if err == nil || !strings.Contains(err.Error(), "open rejected") {
		t.Fatalf("switch error = %v, want host open rejection", err)
	}
}

func TestClosePanelUsesSettingsEntry(t *testing.T) {
	var got v1.PanelParams
	err := closePanel(context.Background(), func(_ context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
		if kind != v1.CallPanelClose {
			t.Fatalf("call kind = %q, want panel.close", kind)
		}
		got = params.(v1.PanelParams)
		return v1.HostReply{OK: true}, nil
	}, v1.PanelParams{Entry: "settings", Instance: "placement-1", Output: "DP-1", Generation: 7})
	if err != nil {
		t.Fatal(err)
	}
	if got.Entry != "settings" || got.Instance != "placement-1" || got.Output != "DP-1" || got.Generation != 7 {
		t.Fatalf("close params = %+v, want settings popup identity preserved", got)
	}
}

func TestSettingsChangesKeepPluginAndInstanceScopesSeparate(t *testing.T) {
	state := newSettingsState(7)
	if !state.config.AlertsEnabled {
		t.Fatal("threshold alerts should remain enabled by default")
	}

	changed := state.apply(&v1.SettingsChanged{
		Scope: v1.ScopeInstance, Instance: "one",
		Values: map[string]any{"vendor": "claude", "track_minimax": true},
	}, 7)
	if changed {
		t.Fatal("instance settings reconfigured plugin settings")
	}
	if state.config.Track["minimax"] {
		t.Fatal("instance-only provider toggle changed plugin tracking")
	}
	if state.instance("one").Vendor != "claude" || state.instance("two").Vendor == "claude" {
		t.Fatalf("instance settings leaked across views: one=%q two=%q", state.instance("one").Vendor, state.instance("two").Vendor)
	}
	state.forgetInstance("one")
	if state.instance("one").Vendor == "claude" {
		t.Fatal("closed instance settings were retained for a later placement")
	}

	changed = state.apply(&v1.SettingsChanged{
		Scope: v1.ScopePlugin,
		Values: map[string]any{
			"track_copilot": true, "track_ollama": true,
			"track_minimax": true, "track_synthetic": true,
			"ollama_api_key": "ollama-key", "minimax_api_key": "minimax-key",
			"synthetic_api_key": "synthetic-key", "commandcode_api_key": "commandcode-key",
			"alerts_enabled": false,
		},
	}, 7)
	if !changed {
		t.Fatal("plugin settings were not applied")
	}
	if !state.config.Track["copilot"] || !state.config.Track["ollama"] ||
		!state.config.Track["minimax"] || !state.config.Track["synthetic"] ||
		state.config.Keys["ollama"] != "ollama-key" || state.config.Keys["minimax"] != "minimax-key" ||
		state.config.Keys["synthetic"] != "synthetic-key" ||
		state.config.Keys["commandcode"] != "commandcode-key" || state.config.AlertsEnabled {
		t.Fatalf("plugin settings not resolved: %+v", state.config)
	}
	state.apply(&v1.SettingsChanged{
		Scope: v1.ScopeInstance, Instance: "one",
		Values: map[string]any{"vendor": "codex", "track_minimax": false},
	}, 7)
	if !state.config.Track["minimax"] || state.instance("one").Vendor != "codex" {
		t.Fatal("later instance settings replaced plugin-wide collection settings")
	}
}

func TestRegistryIncludesAllProviderCollectors(t *testing.T) {
	registered := registry()
	for _, id := range []string{"claude", "codex", "commandcode", "copilot", "ollama", "minimax", "opencode-go", "synthetic"} {
		if _, ok := registered[id]; !ok {
			t.Errorf("provider %q is not registered", id)
		}
	}
}

func TestResolveConfigIncludesOpenCodeGoAsOptInProvider(t *testing.T) {
	cfg := resolveConfig(map[string]any{"opencode_go_api_key": "go-key"}, 7)
	if cfg.Track["opencode-go"] {
		t.Fatal("OpenCode Go should remain opt-in by default")
	}
	if cfg.Keys["opencode-go"] != "go-key" {
		t.Fatalf("OpenCode Go key = %q, want configured key", cfg.Keys["opencode-go"])
	}

	cfg = resolveConfig(map[string]any{"track_opencode_go": true}, 7)
	if !cfg.Track["opencode-go"] {
		t.Fatal("enabling OpenCode Go in plugin settings should track it")
	}
}

func TestResolveConfigParsesHistoryRetentionSelect(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{{"500", 500}, {"2000", 2000}, {"10000", 10000}} {
		cfg := resolveConfig(map[string]any{"history_retention": tc.value}, 7)
		if cfg.HistoryRetention != tc.want {
			t.Errorf("retention %q = %d, want %d", tc.value, cfg.HistoryRetention, tc.want)
		}
	}
}
