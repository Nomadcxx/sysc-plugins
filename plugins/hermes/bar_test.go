package hermes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/icons"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// barWidths are the bar boxes every state is checked at: the horizontal
// slot, the compact threshold, and a side bar.
var barWidths = []int{shelllint.BarWidth, 120, 32}

func repoAvatar(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("assets", avatarFile))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() || info.Size() == 0 {
		t.Fatalf("avatar asset %s is missing: %v", abs, err)
	}
	return abs
}

// stateFor builds the bar input for the given status, the way the runtime
// publishes it.
func stateFor(s StatusKey) BarState {
	b := BarState{Status: StatusFor(s)}
	switch s {
	case StatusOffline:
		b.BridgeError = "Connection failed: 127.0.0.1:19777 unreachable"
	case StatusIdle:
		b.Model = "example-model"
	case StatusUnknown:
		b.GatewayStatus = "stopped"
	default:
		b.Model = "example-model"
	}
	return b
}

func lintView(t *testing.T, name string, root *v1.Node, view v1.ViewKind, width, height int) {
	t.Helper()
	if err := v1.Validate(root, view); err != nil {
		t.Errorf("%s does not validate: %v", name, err)
	}
	for _, f := range shelllint.Tree(root, view, width, height) {
		t.Errorf("%s does not lay out: %s", name, f)
	}
}

func walk(n *v1.Node, visit func(*v1.Node)) {
	if n == nil {
		return
	}
	visit(n)
	for _, c := range n.Children {
		walk(c, visit)
	}
}

func texts(n *v1.Node) []string {
	var out []string
	walk(n, func(c *v1.Node) {
		if c.Kind == v1.KindText {
			out = append(out, c.Text)
		}
	})
	return out
}

func TestStatusVocabularyMatchesNoctalia(t *testing.T) {
	want := []struct {
		status StatusKey
		label  string
		glyph  string
		tone   v1.Tone
	}{
		{StatusOffline, "Offline", "power_settings_new", v1.ToneError},
		{StatusIdle, "Online", "check", v1.ToneAccent},
		{StatusBusy, "Working", "refresh", v1.ToneAccent},
		{StatusAttention, "Needs You", "notifications-active", v1.ToneWarning},
		{StatusDegraded, "Degraded", "gpp_maybe", v1.ToneWarning},
		{StatusError, "Error", "cancel", v1.ToneError},
		{StatusUnknown, "Unknown", "ai-usage", v1.ToneNormal},
	}
	if len(AllStatusKeys) != len(want) {
		t.Fatalf("AllStatusKeys has %d entries, want the seven noctalia statuses", len(AllStatusKeys))
	}
	forbidden := []string{"warning", "history", "alert", "error"}
	seen := map[string]StatusKey{}
	for i, w := range want {
		s := AllStatusKeys[i]
		if s != w.status {
			t.Errorf("AllStatusKeys[%d] = %q, want %q", i, s, w.status)
		}
		if got := StatusFor(s).Text; got != w.label {
			t.Errorf("%s label = %q, want %q", s, got, w.label)
		}
		if got := StatusFor(s).Tone; got != w.tone {
			t.Errorf("%s tone = %q, want %q", s, got, w.tone)
		}
		if got := StatusFor(s).Icon; got != w.glyph {
			t.Errorf("%s glyph = %q, want %q", s, got, w.glyph)
		}
		glyph := StatusFor(s).Icon
		if !icons.ValidName(glyph) {
			t.Errorf("%s glyph %q is not in the pinned icon catalogue", s, glyph)
		}
		for _, bad := range forbidden {
			if strings.Contains(glyph, bad) {
				t.Errorf("%s glyph %q is a %s icon", s, glyph, bad)
			}
		}
		if prev, dup := seen[glyph]; dup {
			t.Errorf("%s and %s share glyph %q", prev, s, glyph)
		}
		seen[glyph] = s
	}
}

func TestDeriveStatusFollowsNoctalia(t *testing.T) {
	cases := []struct {
		name  string
		state State
		want  StatusKey
	}{
		{"zero state", State{}, StatusOffline},
		{"bridge offline wins", State{Bridge: BridgeInfo{Status: "offline"}, Hermes: HermesInfo{Status: "busy"}}, StatusOffline},
		{"bridge starting", State{Bridge: BridgeInfo{Status: "starting"}, Hermes: HermesInfo{Status: "idle"}}, StatusOffline},
		{"bridge error", State{Bridge: BridgeInfo{Status: "error"}, Hermes: HermesInfo{Status: "idle"}}, StatusError},
		{"hermes idle", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "idle"}}, StatusIdle},
		{"hermes busy", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "busy"}}, StatusBusy},
		{"hermes attention", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "attention"}}, StatusAttention},
		{"hermes degraded", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "degraded"}}, StatusDegraded},
		{"hermes error", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "error"}}, StatusError},
		{"unknown, gateway running", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "unknown"}, Summary: Summary{Gateway: SummaryGateway{Status: "running"}}}, StatusIdle},
		{"empty, gateway running", State{Bridge: BridgeInfo{Status: "online"}, Summary: Summary{Gateway: SummaryGateway{Status: "running"}}}, StatusIdle},
		{"unknown, gateway stopped", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "unknown"}, Summary: Summary{Gateway: SummaryGateway{Status: "stopped"}}}, StatusUnknown},
		{"unrecognised hermes status", State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "thinking"}}, StatusUnknown},
	}
	for _, c := range cases {
		if got := DeriveStatus(c.state).Key; got != c.want {
			t.Errorf("%s: DeriveStatus = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestViewsLayOutInEveryStatus(t *testing.T) {
	avatar := repoAvatar(t)
	long := strings.Repeat("a-very-long-model-name-", 4)
	for _, s := range AllStatusKeys {
		t.Run(string(s), func(t *testing.T) {
			states := map[string]BarState{"plain": stateFor(s)}
			noisy := stateFor(s)
			noisy.Model = long
			noisy.BridgeError = strings.Repeat("the bridge said something long ", 4)
			states["long model and error"] = noisy
			for name, state := range states {
				for _, av := range []string{avatar, ""} {
					for _, hide := range []bool{false, true} {
						for _, width := range barWidths {
							label := name + " bar"
							if av == "" {
								label += " (no avatar)"
							}
							lintView(t, label, BarTreeAtWidth(state, hide, av, width), v1.ViewBar, width, shelllint.BarHeight)
						}
					}
				}
				lintView(t, name+" tooltip", TooltipTree(state), v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight)
			}
		})
	}
}

func TestEveryGlyphIsInTheCatalogue(t *testing.T) {
	avatar := repoAvatar(t)
	for _, s := range AllStatusKeys {
		for _, av := range []string{avatar, ""} {
			for _, width := range barWidths {
				walk(BarTreeAtWidth(stateFor(s), false, av, width), func(n *v1.Node) {
					if n.Icon == "" {
						return
					}
					if !icons.ValidName(n.Icon) {
						t.Errorf("%s bar at %d uses %q, not in the pinned catalogue", s, width, n.Icon)
					}
					if strings.Contains(n.Icon, "warning") || strings.Contains(n.Icon, "history") {
						t.Errorf("%s bar at %d uses forbidden glyph %q", s, width, n.Icon)
					}
				})
			}
		}
	}
}

// TestBarHidesWhenIdleWhenAsked follows plugins/updates
// TestBarHidesAtZeroWhenAsked: the hidden bar is an empty row, not a nil
// tree, so the host keeps a valid view and lays nothing out.
func TestBarHidesWhenIdleWhenAsked(t *testing.T) {
	avatar := repoAvatar(t)
	hidden := BarTree(stateFor(StatusIdle), true, avatar)
	if hidden.Kind != v1.KindRow || len(hidden.Children) != 0 {
		t.Fatalf("bar should be an empty row when idle and hide_when_idle is on, got %+v", hidden)
	}
	shown := BarTree(stateFor(StatusIdle), false, avatar)
	if len(shown.Children) == 0 {
		t.Fatal("bar should still show the pill when hide_when_idle is off")
	}
	for _, s := range AllStatusKeys {
		if s == StatusIdle {
			continue
		}
		if bar := BarTree(stateFor(s), true, avatar); len(bar.Children) == 0 {
			t.Errorf("%s must stay visible with hide_when_idle on", s)
		}
	}
}

func openButton(t *testing.T, bar *v1.Node) *v1.Node {
	t.Helper()
	var found *v1.Node
	walk(bar, func(n *v1.Node) {
		if n.ID == barOpenID {
			found = n
		}
	})
	if found == nil {
		t.Fatalf("bar has no %q button: %+v", barOpenID, bar)
	}
	if found.Kind != v1.KindButton || found.Role != "button" || found.Name == "" {
		t.Errorf("open control is not an accessible button: %+v", found)
	}
	hasActivate, hasPointer := false, false
	for _, e := range found.Events {
		hasActivate = hasActivate || e == v1.EventActivate
		hasPointer = hasPointer || e == v1.EventPointer
	}
	if !hasActivate || !hasPointer {
		t.Errorf("open button must take activate and pointer (right-click menu), got %v", found.Events)
	}
	return found
}

func TestBarShowsAvatarStatusAndLabel(t *testing.T) {
	avatar := repoAvatar(t)
	wantText := map[StatusKey]string{
		StatusOffline:   "Offline",
		StatusIdle:      "",
		StatusBusy:      "Working",
		StatusAttention: "!",
		StatusDegraded:  "Degraded",
		StatusError:     "Error",
		StatusUnknown:   "",
	}
	for _, s := range AllStatusKeys {
		bar := BarTreeAtWidth(stateFor(s), false, avatar, shelllint.BarWidth)
		if bar.Kind != v1.KindRow {
			t.Fatalf("%s bar root is %s, want row", s, bar.Kind)
		}
		btn := openButton(t, bar)
		if len(btn.Children) != 1 || btn.Children[0].Kind != v1.KindImage || btn.Children[0].Path != avatar {
			t.Errorf("%s: open button should hold the avatar image %s, got %+v", s, avatar, btn.Children)
		}
		var glyph *v1.Node
		walk(bar, func(n *v1.Node) {
			if n.Kind == v1.KindIcon {
				glyph = n
			}
		})
		if glyph == nil || glyph.Icon != StatusFor(s).Icon || glyph.Tone != StatusFor(s).Tone {
			t.Errorf("%s: bar status glyph = %+v, want %q in tone %q", s, glyph, StatusFor(s).Icon, StatusFor(s).Tone)
		}
		got := strings.Join(texts(bar), "|")
		if got != wantText[s] {
			t.Errorf("%s: bar text = %q, want %q", s, got, wantText[s])
		}
		if !strings.Contains(btn.Name, StatusFor(s).Text) {
			t.Errorf("%s: open button name %q should carry the status label", s, btn.Name)
		}
	}
}

func TestCompactBarIsTheStatusGlyphOnly(t *testing.T) {
	avatar := repoAvatar(t)
	for _, s := range AllStatusKeys {
		bar := BarTreeAtWidth(stateFor(s), false, avatar, 32)
		if len(bar.Children) != 1 {
			t.Fatalf("%s compact bar has %d children, want the open button alone", s, len(bar.Children))
		}
		btn := openButton(t, bar)
		if btn.Icon != StatusFor(s).Icon || btn.Tone != StatusFor(s).Tone || len(btn.Children) != 0 {
			t.Errorf("%s: compact open button = icon %q tone %q children %d, want %q in %q", s, btn.Icon, btn.Tone, len(btn.Children), StatusFor(s).Icon, StatusFor(s).Tone)
		}
		if len(texts(bar)) != 0 {
			t.Errorf("%s: compact bar carries text %v", s, texts(bar))
		}
	}
}

func TestBarFallsBackToAGlyphWithoutTheAvatar(t *testing.T) {
	btn := openButton(t, BarTreeAtWidth(stateFor(StatusBusy), false, "", shelllint.BarWidth))
	if btn.Icon != fallbackAvatarIcon || len(btn.Children) != 0 {
		t.Errorf("open button without avatar = icon %q children %d, want %q", btn.Icon, len(btn.Children), fallbackAvatarIcon)
	}
	if !icons.ValidName(fallbackAvatarIcon) {
		t.Errorf("fallback avatar icon %q is not in the pinned catalogue", fallbackAvatarIcon)
	}
}

func TestTooltipMatchesNoctalia(t *testing.T) {
	cases := []struct {
		name  string
		state BarState
		want  []string
		tones []v1.Tone
	}{
		{"idle with model", stateFor(StatusIdle), []string{"Hermes: Online · example-model"}, []v1.Tone{v1.ToneNormal}},
		{"unknown without model", stateFor(StatusUnknown), []string{"Hermes: Unknown"}, []v1.Tone{v1.ToneNormal}},
		{"offline with error", stateFor(StatusOffline), []string{"Hermes: Offline", "Connection failed: 127.0.0.1:19…"}, []v1.Tone{v1.ToneNormal, v1.ToneError}},
	}
	for _, c := range cases {
		tip := TooltipTree(c.state)
		if tip.Kind != v1.KindColumn {
			t.Fatalf("%s: tooltip root is %s, want column", c.name, tip.Kind)
		}
		if len(tip.Children) != len(c.want) {
			t.Fatalf("%s: tooltip has %d lines %v, want %v", c.name, len(tip.Children), texts(tip), c.want)
		}
		for i, line := range tip.Children {
			if line.Kind != v1.KindText || line.Text != c.want[i] || line.Tone != c.tones[i] {
				t.Errorf("%s: line %d = %q tone %q, want %q tone %q", c.name, i, line.Text, line.Tone, c.want[i], c.tones[i])
			}
			if len(line.Text) > tooltipLineBytes {
				t.Errorf("%s: line %d is %d bytes, past the %d-byte budget", c.name, i, len(line.Text), tooltipLineBytes)
			}
		}
	}
}

func TestTooltipWarnsOnCleartextRemoteClient(t *testing.T) {
	warning := func(host string) string {
		return "Cleartext HTTP to " + host + ": the token and prompts are not encrypted."
	}
	online := BarState{Status: StatusFor(StatusIdle), Model: "example-model"}
	cases := []struct {
		name    string
		state   BarState
		wantMsg string
	}{
		{"client-only remote", withHost(online, true, "example.com"), warning("example.com")},
		{"client-only LAN", withHost(online, true, "192.168.0.10"), warning("192.168.0.10")},
		{"client-only loopback", withHost(online, true, "127.0.0.1"), ""},
		{"client-only localhost", withHost(online, true, "localhost"), ""},
		{"client-only IPv6 loopback", withHost(online, true, "::1"), ""},
		{"local mode remote host", withHost(online, false, "example.com"), ""},
	}
	for _, c := range cases {
		tip := TooltipTree(c.state)
		got := strings.Join(texts(tip), " ")
		switch {
		case c.wantMsg == "" && strings.Contains(got, "Cleartext HTTP"):
			t.Errorf("%s: tooltip %q should omit the cleartext warning", c.name, got)
		case c.wantMsg != "" && !strings.Contains(got, c.wantMsg):
			t.Errorf("%s: tooltip %q does not show %q", c.name, got, c.wantMsg)
		}
		lintView(t, c.name+" tooltip", tip, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight)
		for i, line := range texts(tip) {
			if len(line) > tooltipLineBytes {
				t.Errorf("%s: line %d is %d bytes, past the %d-byte budget", c.name, i, len(line), tooltipLineBytes)
			}
		}
	}
}

func withHost(state BarState, clientOnly bool, host string) BarState {
	state.ClientOnly = clientOnly
	state.Host = host
	return state
}

func TestAvatarPathResolvesTheShippedAsset(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := AvatarPath(bin); got != "" {
		t.Errorf("AvatarPath with no asset = %q, want empty", got)
	}
	asset := filepath.Join(root, "assets", avatarFile)
	if err := os.WriteFile(asset, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AvatarPath(bin); got != asset {
		t.Errorf("AvatarPath = %q, want %q", got, asset)
	}
	if avatarFile != "hermes-icon.png" {
		t.Errorf("avatarFile = %q, want hermes-icon.png", avatarFile)
	}
	repoAvatar(t)
}

func TestServiceStatusReflectsTheBarStatus(t *testing.T) {
	want := map[StatusKey]v1.ServiceState{
		StatusOffline:   v1.ServiceStateInactive,
		StatusIdle:      v1.ServiceStateActive,
		StatusBusy:      v1.ServiceStateActive,
		StatusAttention: v1.ServiceStateActive,
		StatusDegraded:  v1.ServiceStateActive,
		StatusError:     v1.ServiceStateError,
		StatusUnknown:   v1.ServiceStateUnknown,
	}
	for _, s := range AllStatusKeys {
		st := ServiceStatusFor(stateFor(s), 7)
		if st.ServiceID != serviceID || st.Revision != 7 {
			t.Errorf("%s: service status id %q revision %d", s, st.ServiceID, st.Revision)
		}
		if st.State != want[s] {
			t.Errorf("%s: service state = %q, want %q", s, st.State, want[s])
		}
		if st.Busy {
			t.Errorf("%s: busy is the host's action flag, the view must leave it false", s)
		}
		if !strings.HasPrefix(st.Message, StatusFor(s).Text) {
			t.Errorf("%s: message %q should lead with %q", s, st.Message, StatusFor(s).Text)
		}
		if len(st.Message) > v1.MaxServiceStatusMessageBytes {
			t.Errorf("%s: message is %d bytes", s, len(st.Message))
		}
	}
	offline := ServiceStatusFor(stateFor(StatusOffline), 1)
	if offline.Message != "Offline · Connection failed: 127.0.0.1:19777 unreachable" {
		t.Errorf("offline message = %q", offline.Message)
	}
	idle := ServiceStatusFor(stateFor(StatusIdle), 1)
	if idle.Message != "Online · example-model" {
		t.Errorf("idle message = %q", idle.Message)
	}
	huge := stateFor(StatusOffline)
	huge.BridgeError = strings.Repeat("x", 2*v1.MaxServiceStatusMessageBytes)
	if msg := ServiceStatusFor(huge, 1).Message; len(msg) > v1.MaxServiceStatusMessageBytes {
		t.Errorf("message is %d bytes, past the protocol limit", len(msg))
	}
}

// TestStartGatewayOnlyWhenLocalAndDown mirrors the noctalia context menu:
// Start gateway appears only outside client-only mode, while offline or
// unknown.
func TestStartGatewayOnlyWhenLocalAndDown(t *testing.T) {
	if len(AllStatusKeys) != 7 {
		t.Fatalf("AllStatusKeys has %d entries, want seven", len(AllStatusKeys))
	}
	for _, s := range AllStatusKeys {
		down := s == StatusOffline || s == StatusUnknown
		if got := CanStartGateway(StatusFor(s), false); got != down {
			t.Errorf("CanStartGateway(%s, local) = %v, want %v", s, got, down)
		}
		if CanStartGateway(StatusFor(s), true) {
			t.Errorf("CanStartGateway(%s, client-only) = true; client-only never starts processes", s)
		}
	}
}

// TestViewsConsumeTheUnifiedStatus pins that the UI views take the one
// runtime Status, not a bar-local stand-in.
func TestViewsConsumeTheUnifiedStatus(t *testing.T) {
	st := DeriveStatus(State{Bridge: BridgeInfo{Status: "online"}, Hermes: HermesInfo{Status: "idle"}})
	if st.Key != StatusIdle {
		t.Fatalf("DeriveStatus = %q, want idle", st.Key)
	}
	p := PanelState{Summary: SummaryState{BarState: BarState{Status: st}}}
	if panelHeader(p, st, 400) == nil {
		t.Fatal("panel header does not take the unified status")
	}
	if summaryHeader(st, "", 300) == nil {
		t.Fatal("summary header does not take the unified status")
	}
	if MenuTree(st, false, 200, 240) == nil {
		t.Fatal("menu does not take the unified status")
	}
	if !CanStartGateway(StatusFor(StatusOffline), false) {
		t.Fatal("offline local must offer the gateway")
	}
}
