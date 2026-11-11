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

// stateFor builds the bar input that derives to the given status, the way
// the bridge would report it.
func stateFor(s Status) BarState {
	switch s {
	case StatusOffline:
		return BarState{BridgeStatus: "offline", BridgeError: "Connection failed: 127.0.0.1:19777 unreachable"}
	case StatusIdle:
		return BarState{BridgeStatus: "online", HermesStatus: "idle", Model: "example-model"}
	case StatusUnknown:
		return BarState{BridgeStatus: "online", HermesStatus: "unknown", GatewayStatus: "stopped"}
	default:
		return BarState{BridgeStatus: "online", HermesStatus: string(s), Model: "example-model"}
	}
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
		status Status
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
	if len(Statuses) != len(want) {
		t.Fatalf("Statuses has %d entries, want the seven noctalia statuses", len(Statuses))
	}
	forbidden := []string{"warning", "history", "alert", "error"}
	seen := map[string]Status{}
	for i, w := range want {
		s := Statuses[i]
		if s != w.status {
			t.Errorf("Statuses[%d] = %q, want %q", i, s, w.status)
		}
		if got := s.Label(); got != w.label {
			t.Errorf("%s label = %q, want %q", s, got, w.label)
		}
		if got := s.Tone(); got != w.tone {
			t.Errorf("%s tone = %q, want %q", s, got, w.tone)
		}
		if got := s.Glyph(); got != w.glyph {
			t.Errorf("%s glyph = %q, want %q", s, got, w.glyph)
		}
		glyph := s.Glyph()
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
		state BarState
		want  Status
	}{
		{"zero state", BarState{}, StatusOffline},
		{"bridge offline wins", BarState{BridgeStatus: "offline", HermesStatus: "busy"}, StatusOffline},
		{"bridge starting", BarState{BridgeStatus: "starting", HermesStatus: "idle"}, StatusOffline},
		{"bridge error", BarState{BridgeStatus: "error", HermesStatus: "idle"}, StatusError},
		{"hermes idle", BarState{BridgeStatus: "online", HermesStatus: "idle"}, StatusIdle},
		{"hermes busy", BarState{BridgeStatus: "online", HermesStatus: "busy"}, StatusBusy},
		{"hermes attention", BarState{BridgeStatus: "online", HermesStatus: "attention"}, StatusAttention},
		{"hermes degraded", BarState{BridgeStatus: "online", HermesStatus: "degraded"}, StatusDegraded},
		{"hermes error", BarState{BridgeStatus: "online", HermesStatus: "error"}, StatusError},
		{"unknown, gateway running", BarState{BridgeStatus: "online", HermesStatus: "unknown", GatewayStatus: "running"}, StatusIdle},
		{"empty, gateway running", BarState{BridgeStatus: "online", GatewayStatus: "running"}, StatusIdle},
		{"unknown, gateway stopped", BarState{BridgeStatus: "online", HermesStatus: "unknown", GatewayStatus: "stopped"}, StatusUnknown},
		{"unrecognised hermes status", BarState{BridgeStatus: "online", HermesStatus: "thinking"}, StatusUnknown},
	}
	for _, c := range cases {
		if got := DeriveStatus(c.state); got != c.want {
			t.Errorf("%s: DeriveStatus = %q, want %q", c.name, got, c.want)
		}
	}
	for _, s := range Statuses {
		if got := DeriveStatus(stateFor(s)); got != s {
			t.Errorf("stateFor(%s) derives %q", s, got)
		}
	}
}

func TestViewsLayOutInEveryStatus(t *testing.T) {
	avatar := repoAvatar(t)
	long := strings.Repeat("a-very-long-model-name-", 4)
	for _, s := range Statuses {
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
	for _, s := range Statuses {
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
	for _, s := range Statuses {
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
	wantText := map[Status]string{
		StatusOffline:   "Offline",
		StatusIdle:      "",
		StatusBusy:      "Working",
		StatusAttention: "!",
		StatusDegraded:  "Degraded",
		StatusError:     "Error",
		StatusUnknown:   "",
	}
	for _, s := range Statuses {
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
		if glyph == nil || glyph.Icon != s.Glyph() || glyph.Tone != s.Tone() {
			t.Errorf("%s: bar status glyph = %+v, want %q in tone %q", s, glyph, s.Glyph(), s.Tone())
		}
		got := strings.Join(texts(bar), "|")
		if got != wantText[s] {
			t.Errorf("%s: bar text = %q, want %q", s, got, wantText[s])
		}
		if !strings.Contains(btn.Name, s.Label()) {
			t.Errorf("%s: open button name %q should carry the status label", s, btn.Name)
		}
	}
}

func TestCompactBarIsTheStatusGlyphOnly(t *testing.T) {
	avatar := repoAvatar(t)
	for _, s := range Statuses {
		bar := BarTreeAtWidth(stateFor(s), false, avatar, 32)
		if len(bar.Children) != 1 {
			t.Fatalf("%s compact bar has %d children, want the open button alone", s, len(bar.Children))
		}
		btn := openButton(t, bar)
		if btn.Icon != s.Glyph() || btn.Tone != s.Tone() || len(btn.Children) != 0 {
			t.Errorf("%s: compact open button = icon %q tone %q children %d, want %q in %q", s, btn.Icon, btn.Tone, len(btn.Children), s.Glyph(), s.Tone())
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
	online := BarState{BridgeStatus: "online", HermesStatus: "idle", Model: "example-model"}
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
	want := map[Status]v1.ServiceState{
		StatusOffline:   v1.ServiceStateInactive,
		StatusIdle:      v1.ServiceStateActive,
		StatusBusy:      v1.ServiceStateActive,
		StatusAttention: v1.ServiceStateActive,
		StatusDegraded:  v1.ServiceStateActive,
		StatusError:     v1.ServiceStateError,
		StatusUnknown:   v1.ServiceStateUnknown,
	}
	for _, s := range Statuses {
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
		if !strings.HasPrefix(st.Message, s.Label()) {
			t.Errorf("%s: message %q should lead with %q", s, st.Message, s.Label())
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
	if len(Statuses) != 7 {
		t.Fatalf("Statuses has %d entries, want seven", len(Statuses))
	}
	for _, s := range Statuses {
		down := s == StatusOffline || s == StatusUnknown
		if got := CanStartGateway(s, false); got != down {
			t.Errorf("CanStartGateway(%s, local) = %v, want %v", s, got, down)
		}
		if CanStartGateway(s, true) {
			t.Errorf("CanStartGateway(%s, client-only) = true; client-only never starts processes", s)
		}
	}
}
