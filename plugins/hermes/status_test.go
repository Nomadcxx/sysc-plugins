package hermes

import (
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/icons"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func online(mut func(*State)) State {
	s := State{Bridge: BridgeInfo{Status: "online"}}
	if mut != nil {
		mut(&s)
	}
	return s
}

func TestDeriveStatusFollowsTheNineSteps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state State
		want  StatusKey
	}{
		// 1. plugin-side auth or protocol failure.
		{"plugin error beats everything", State{Bridge: BridgeInfo{Status: "error"}, Approval: Approval{Pending: true}, Hermes: HermesInfo{Status: "busy"}}, StatusError},
		// 2. bridge not online.
		{"offline", State{Bridge: BridgeInfo{Status: "offline"}, Hermes: HermesInfo{Status: "idle"}}, StatusOffline},
		{"empty bridge status", State{}, StatusOffline},
		{"offline hides a pending approval", State{Bridge: BridgeInfo{Status: "offline"}, Approval: Approval{Pending: true}}, StatusOffline},
		// 3. attention.
		{"pending approval", online(func(s *State) { s.Approval.Pending = true }), StatusAttention},
		{"approval overrides busy", online(func(s *State) { s.Approval.Pending = true; s.Hermes.Status = "busy"; s.Session.Running = true }), StatusAttention},
		{"attention without approval.pending", online(func(s *State) { s.Hermes.Status = "attention" }), StatusAttention},
		// 4. busy.
		{"hermes busy", online(func(s *State) { s.Hermes.Status = "busy" }), StatusBusy},
		{"session running", online(func(s *State) { s.Hermes.Status = "idle"; s.Session.Running = true }), StatusBusy},
		// 5. hermes error.
		{"hermes error", online(func(s *State) { s.Hermes.Status = "error" }), StatusError},
		// 6. idle.
		{"idle", online(func(s *State) { s.Hermes.Status = "idle" }), StatusIdle},
		// 7. degraded passthrough.
		{"degraded passthrough", online(func(s *State) { s.Hermes.Status = "degraded" }), StatusDegraded},
		// 8. silent Hermes with a running gateway.
		{"unknown plus a running gateway", online(func(s *State) { s.Hermes.Status = "unknown"; s.Summary.Gateway.Status = "running" }), StatusIdle},
		{"silent plus a running gateway", online(func(s *State) { s.Summary.Gateway.Status = "running" }), StatusIdle},
		// 9. otherwise unknown.
		{"unknown", online(func(s *State) { s.Hermes.Status = "unknown" }), StatusUnknown},
		{"unknown with a stopped gateway", online(func(s *State) { s.Hermes.Status = "unknown"; s.Summary.Gateway.Status = "stopped" }), StatusUnknown},
		{"unrecognised hermes status", online(func(s *State) { s.Hermes.Status = "pondering"; s.Summary.Gateway.Status = "running" }), StatusUnknown},
	} {
		if got := DeriveStatus(tc.state); got.Key != tc.want {
			t.Errorf("%s: DeriveStatus = %s, want %s", tc.name, got.Key, tc.want)
		}
	}
}

func TestStatusPresentation(t *testing.T) {
	for _, tc := range []struct {
		key                 StatusKey
		text, barText, icon string
		tone                v1.Tone
	}{
		{StatusOffline, "Offline", "Offline", "power_settings_new", v1.ToneError},
		{StatusIdle, "Online", "", "check", v1.ToneAccent},
		{StatusBusy, "Working", "Working", "refresh", v1.ToneAccent},
		{StatusAttention, "Needs You", "!", "notifications-active", v1.ToneWarning},
		{StatusDegraded, "Degraded", "Degraded", "gpp_maybe", v1.ToneWarning},
		{StatusError, "Error", "Error", "cancel", v1.ToneError},
		{StatusUnknown, "Unknown", "", "ai-usage", v1.ToneNormal},
	} {
		got := StatusFor(tc.key)
		if got.Key != tc.key || got.Text != tc.text || got.BarText != tc.barText || got.Icon != tc.icon || got.Tone != tc.tone {
			t.Errorf("StatusFor(%s) = %+v, want text %q bar %q icon %q tone %q", tc.key, got, tc.text, tc.barText, tc.icon, tc.tone)
		}
	}
}

func TestOnlyOfflineAndErrorUseTheErrorTone(t *testing.T) {
	for _, key := range AllStatusKeys {
		st := StatusFor(key)
		isErr := key == StatusOffline || key == StatusError
		if (st.Tone == v1.ToneError) != isErr {
			t.Errorf("%s tone = %q", key, st.Tone)
		}
		if (key == StatusAttention || key == StatusDegraded) && st.Tone != v1.ToneWarning {
			t.Errorf("%s tone = %q, want warning", key, st.Tone)
		}
	}
	if len(AllStatusKeys) != 7 {
		t.Fatalf("AllStatusKeys has %d entries, want 7", len(AllStatusKeys))
	}
}

func TestEveryStatusIconIsInTheShellCatalogue(t *testing.T) {
	for _, key := range AllStatusKeys {
		if icon := StatusFor(key).Icon; !icons.ValidName(icon) {
			t.Errorf("%s icon %q is not in the pinned shell's catalogue", key, icon)
		}
	}
}
