package main

import (
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

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
