package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestPanelActionsRunOncePerClick(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
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
	done := make(chan error, 1)
	go func() { done <- run(in, out) }()
	t.Cleanup(func() {
		host.Close()
		reader.Close()
		in.Close()
		out.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("plugin did not stop")
		}
	})
	enc, dec := v1.NewEncoder(host), v1.NewDecoder(reader, v1.ToHost)
	send := func(m v1.Message) {
		t.Helper()
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
	}
	next := func() v1.Message {
		t.Helper()
		if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		m, err := dec.Decode()
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	send(&v1.HostHello{Supported: []v1.Version{{Major: 1, Minor: 7}}})
	if _, ok := next().(*v1.PluginHello); !ok {
		t.Fatal("missing plugin hello")
	}
	values := map[string]any{}
	for _, key := range []string{"track_claude", "track_codex", "track_commandcode", "alerts_enabled"} {
		values[key] = false
	}
	send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: values})
	var calls []v1.HostCall
	waitView := func(id string) {
		t.Helper()
		for {
			switch m := next().(type) {
			case *v1.HostCall:
				if m.Call == v1.CallPanelOpen || m.Call == v1.CallPanelClose {
					calls = append(calls, *m)
				}
				send(&v1.HostReply{ID: m.ID, OK: true, Result: json.RawMessage(`{}`)})
			case *v1.ViewSnapshot:
				if m.ViewID == id {
					return
				}
			}
		}
	}
	send(&v1.ViewOpen{ViewID: "bar", View: v1.ViewBar, Entry: "bar", Instance: "placement-1"})
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
			send(&v1.InputEvent{ViewID: step.source, Node: step.node, Event: event, Button: v1.ButtonPrimary, Output: "DP-1"})
		}
		// A later view is a processing barrier, without a sleep or an idle timeout.
		send(&v1.ViewOpen{ViewID: "barrier", View: v1.ViewBar, Entry: "bar"})
		waitView("barrier")
		send(&v1.ViewClose{ViewID: "barrier"})
		if len(calls) != 1 || calls[0].Call != step.kind {
			t.Fatalf("%s click made %d panel calls: %+v", step.node, len(calls), calls)
		}
		var params v1.PanelParams
		if err := json.Unmarshal(calls[0].Params, &params); err != nil || params.Entry != step.target {
			t.Fatalf("%s target = %+v, err=%v", step.node, params, err)
		}
		if step.kind == v1.CallPanelOpen {
			if step.source != "bar" {
				send(&v1.ViewClose{ViewID: step.source})
			}
			send(&v1.ViewOpen{ViewID: step.target, View: v1.ViewPanel, Entry: step.target, Instance: "placement-1"})
			waitView(step.target)
		}
	}
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
