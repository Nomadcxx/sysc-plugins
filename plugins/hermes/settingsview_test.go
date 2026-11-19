package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/icons"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// settingsGranted are the declared settings panel width and the fitted one.
var settingsGranted = []int{640, 560}

const (
	pastedToken = "s3cr3t-pasted-token"
	fileToken   = "f1le-secret-token"
	localToken  = "l0cal-bridge-token"
)

func lanState() SettingsState {
	return SettingsState{
		Connection: ConnectionFor(true, "192.168.0.10", 19778,
			TokenCandidates{Pasted: pastedToken, File: fileToken},
			"Authentication failed: wrong bridge token", ""),
		Models:          modelOptions(5),
		DefaultProvider: "nous", DefaultModel: "model-02",
		Pinned:     true,
		TestResult: ResultConnected,
		Detected:   SettingsDetected{Provider: "nous", Model: "model-02"},
		SSHTarget:  "rama@hermes-vps",
	}
}

func settingsStates() map[string]SettingsState {
	local := SettingsState{
		Connection: ConnectionFor(false, "127.0.0.1", 19777, TokenCandidates{Local: localToken}, "", ""),
		Models:     modelOptions(3),
		Detected:   SettingsDetected{HermesHome: "~/.hermes", HermesCommand: "hermes", Provider: "openrouter", Model: "model-00"},
	}
	noisy := lanState()
	noisy.Connection = ConnectionFor(true, "a-very-long-hostname.internal.example.org", 65535,
		TokenCandidates{}, strings.Repeat("the bridge said something long ", 6), strings.Repeat("root.children[3]: row refused ", 5))
	noisy.Models = modelOptions(45)
	noisy.TestResult = ResultUnreachable
	noisy.SSHTarget = "not a target"
	loop := lanState()
	loop.Connection = ConnectionFor(true, "127.0.0.1", 19777, TokenCandidates{File: fileToken}, "", "")
	loop.Models = nil
	loop.TestResult = ""
	return map[string]SettingsState{"local": local, "lan": lanState(), "noisy": noisy, "loopback": loop, "empty": {}}
}

func TestSettingsInnerWidthFromThemeMetrics(t *testing.T) {
	if SettingsInnerWidth(640) != 640-2*13-2*9 || SettingsInnerWidth(560) != 560-2*13-2*9 {
		t.Errorf("inner widths = %d, %d", SettingsInnerWidth(640), SettingsInnerWidth(560))
	}
	// The pads are the pinned shell's theme metrics; fail if the pin moves
	// them so the constants are re-read rather than drift.
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/Nomadcxx/sysc-shell").Output()
	if err != nil {
		t.Fatalf("locate pinned sysc-shell: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "internal", "theme", "profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`PanelPadding: (\d+), CardPadding: (\d+)`)
	all := re.FindAllStringSubmatch(string(src), -1)
	if len(all) == 0 {
		t.Fatal("no density metrics found in the pinned theme")
	}
	for _, m := range all {
		if m[1] != fmt.Sprint(hostPanelPadding) || m[2] != fmt.Sprint(hostCardPadding) {
			t.Errorf("pinned theme has PanelPadding %s CardPadding %s, want %d and %d", m[1], m[2], hostPanelPadding, hostCardPadding)
		}
	}
}

func TestSettingsLaysOutAtInnerWidths(t *testing.T) {
	for name, s := range settingsStates() {
		for _, granted := range settingsGranted {
			inner := SettingsInnerWidth(granted)
			tree := SettingsTree(s, inner)
			label := fmt.Sprintf("%s at %d (inner %d)", name, granted, inner)
			if tree.Kind != v1.KindColumn || tree.Width != inner || tree.Height <= 0 {
				t.Fatalf("%s: root %s %dx%d, want a column %d wide with a height", label, tree.Kind, tree.Width, tree.Height, inner)
			}
			lintView(t, label, tree, v1.ViewPanel, inner, tree.Height)
			used := 2 * tree.Padding
			for i, c := range tree.Children {
				if c.Height <= 0 {
					t.Errorf("%s: child %d (%s) has no height", label, i, c.Kind)
				}
				used += c.Height
				if i > 0 {
					used += tree.Gap
				}
			}
			if used != tree.Height {
				t.Errorf("%s: children use %d, root says %d", label, used, tree.Height)
			}
			walk(tree, func(n *v1.Node) {
				if n.Kind == v1.KindText && textWidth(n.Text) > inner {
					t.Errorf("%s: text %q is wider than %d", label, n.Text, inner)
				}
				if n.Icon == "" {
					return
				}
				if !icons.ValidName(n.Icon) {
					t.Errorf("%s: %q is not in the pinned catalogue", label, n.Icon)
				}
				for _, bad := range []string{"warning", "history", "bolt", "link_off"} {
					if n.Icon == bad {
						t.Errorf("%s: forbidden glyph %q", label, bad)
					}
				}
			})
		}
	}
}

func TestSettingsOrderFollowsDesign(t *testing.T) {
	tree := SettingsTree(lanState(), SettingsInnerWidth(640))
	want := []string{"title", "connection", "model-picker", "pin-side-panel", "test-connection", "detected", "cleartext", "copy-ssh"}
	var got []string
	walk(tree, func(n *v1.Node) {
		for _, w := range want {
			if (n.Key == w || n.ID == w) && (len(got) == 0 || got[len(got)-1] != w) {
				got = append(got, w)
			}
		}
	})
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	if title := findKey(tree, "title"); title == nil || !strings.Contains(strings.Join(texts(title), "|"), "Hermes Settings") {
		t.Error("no Hermes Settings title")
	}
}

// settingsFakeBridge answers /health and /state the way the bundled bridge does,
// with knobs for each failure.
type settingsFakeBridge struct {
	mu          sync.Mutex
	token       string
	healthCode  int
	stateCode   int
	paths       []string
	sentTokens  []string
	stateNeedsT bool
}

func (f *settingsFakeBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.sentTokens = append(f.sentTokens, r.Header.Get("X-Bridge-Token"))
	f.mu.Unlock()
	switch r.URL.Path {
	case "/health":
		w.WriteHeader(f.healthCode)
	case "/state":
		if r.Header.Get("X-Bridge-Token") != f.token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(f.stateCode)
		io.WriteString(w, `{"summary":{}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// httpProber is the test's stand-in for the runtime client: GET with the
// token header, status or network error.
type httpProber struct {
	base, token string
}

func (p httpProber) Get(ctx context.Context, path string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if err != nil {
		return 0, err
	}
	if p.token != "" {
		req.Header.Set("X-Bridge-Token", p.token)
	}
	res, err := (&http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	return res.StatusCode, nil
}

func TestTestConnectionReportsEachResultAgainstAFakeBridge(t *testing.T) {
	for _, c := range []struct {
		name          string
		health, state int
		token         string
		want          string
		wantPaths     string
	}{
		{"connected", 200, 200, "right", "Connected", "/health,/state"},
		{"wrong token", 200, 200, "wrong", "Wrong token", "/health,/state"},
		{"health 403", 403, 200, "right", "Wrong token", "/health"},
		{"health fails", 503, 200, "right", "Failed: 503", "/health"},
		{"state fails", 200, 500, "right", "Failed: 500", "/health,/state"},
	} {
		bridge := &settingsFakeBridge{token: "right", healthCode: c.health, stateCode: c.state}
		srv := httptest.NewServer(bridge)
		got := CheckConnection(context.Background(), httpProber{base: srv.URL, token: c.token})
		srv.Close()
		if got != c.want {
			t.Errorf("%s: result %q, want %q", c.name, got, c.want)
		}
		if p := strings.Join(bridge.paths, ","); p != c.wantPaths {
			t.Errorf("%s: bridge saw %s, want %s", c.name, p, c.wantPaths)
		}
		for _, tok := range bridge.sentTokens {
			if tok != c.token {
				t.Errorf("%s: sent token %q, want the current one", c.name, tok)
			}
		}
	}
	srv := httptest.NewServer(&settingsFakeBridge{})
	url := srv.URL
	srv.Close()
	if got := CheckConnection(context.Background(), httpProber{base: url, token: "right"}); got != "Unreachable: wrong host/port or bridge not running" {
		t.Errorf("closed bridge: %q", got)
	}
	for _, r := range []string{ResultConnected, ResultUnreachable, ResultWrongToken, FailedResult(502)} {
		s := lanState()
		s.TestResult = r
		row := findKey(SettingsTree(s, SettingsInnerWidth(640)), "test-connection-row")
		joined := strings.Join(texts(row), "|")
		if !strings.Contains(joined, r) {
			t.Errorf("result %q not shown: %q", r, joined)
		}
		var tone v1.Tone
		walk(row, func(n *v1.Node) {
			if n.Kind == v1.KindText && n.Text == r {
				tone = n.Tone
			}
		})
		if want := v1.ToneError; r == ResultConnected && tone != v1.ToneAccent || r != ResultConnected && tone != want {
			t.Errorf("result %q tone %q", r, tone)
		}
	}
	if b := findID(SettingsTree(lanState(), SettingsInnerWidth(640)), "test-connection"); b == nil || b.Kind != v1.KindButton || b.Text != "Test connection" {
		t.Errorf("test connection button = %+v", b)
	}
	running := lanState()
	running.Testing = true
	if b := findID(SettingsTree(running, SettingsInnerWidth(640)), "test-connection"); b == nil || !b.Disabled ||
		!strings.Contains(strings.Join(texts(SettingsTree(running, SettingsInnerWidth(640))), "|"), "Testing...") {
		t.Error("a running test should disable the button and say Testing...")
	}
}

func TestConnectionLineNamesTheTokenSourceAndNeverTheToken(t *testing.T) {
	for _, c := range []struct {
		clientOnly bool
		tokens     TokenCandidates
		want       ConnectionTokenSource
		mode       string
		addr       string
	}{
		{true, TokenCandidates{Pasted: pastedToken, File: fileToken}, SourcePasted, "Client-only", "192.168.0.10:19778"},
		{true, TokenCandidates{Pasted: "   ", File: fileToken}, SourceFile, "Client-only", "192.168.0.10:19778"},
		{true, TokenCandidates{}, SourceNone, "Client-only", "192.168.0.10:19778"},
		{true, TokenCandidates{Local: localToken}, SourceNone, "Client-only", "192.168.0.10:19778"},
		{false, TokenCandidates{Local: localToken, Pasted: pastedToken}, SourceLocal, "Local", "127.0.0.1:19777"},
		{false, TokenCandidates{Pasted: pastedToken}, SourceNone, "Local", "127.0.0.1:19777"},
	} {
		// Errors that quote every token the runtime holds; each must be
		// redacted, whichever one is the active source.
		leak := "request with " + c.tokens.Pasted + " / " + c.tokens.File + " / " + c.tokens.Local + " failed"
		conn := ConnectionFor(c.clientOnly, "192.168.0.10", 19778, c.tokens, leak, "view refused: "+leak)
		if conn.TokenSource != c.want {
			t.Errorf("%+v: source %q, want %q", c.tokens, conn.TokenSource, c.want)
		}
		s := SettingsState{Connection: conn}
		tree := SettingsTree(s, SettingsInnerWidth(640))
		line := strings.Join(texts(findKey(tree, "connection")), " ")
		for _, want := range []string{c.mode, c.addr, "token: " + string(c.want), "Last error:", "Last view error:"} {
			if !strings.Contains(line, want) {
				t.Errorf("connection line %q is missing %q", line, want)
			}
		}
		raw, _ := json.Marshal(tree)
		for _, tok := range []string{c.tokens.Pasted, c.tokens.File, c.tokens.Local} {
			if strings.TrimSpace(tok) != "" && strings.Contains(string(raw), tok) {
				t.Errorf("%+v: the settings view carries a token", c.tokens)
			}
		}
	}
	for _, src := range []ConnectionTokenSource{SourcePasted, SourceFile, SourceLocal, SourceNone} {
		if !map[ConnectionTokenSource]bool{"pasted": true, "file": true, "local bridge.token": true, "none": true}[src] {
			t.Errorf("token source %q is not one of the design's names", src)
		}
	}
}

func TestCopySSHNeedsAValidTarget(t *testing.T) {
	for target, want := range map[string]bool{
		"rama@hermes-vps": true, "a.b_c-d@10.0.0.1": true, "u@::1": true, "u@host.example.org:22": true,
		"": false, "host": false, "u@[::1]": false, "u@h;rm -rf": false, "u@h h": false, "u@": false, "@h": false,
		"-oProxyCommand@x": false, "u@h\n": false,
	} {
		if got := ValidSSHTarget(target); got != want {
			t.Errorf("ValidSSHTarget(%q) = %v, want %v", target, got, want)
		}
	}
	if cmd, ok := SSHCommand("rama@hermes-vps", 19778); !ok || cmd != "ssh -N -L 19778:127.0.0.1:19778 rama@hermes-vps" {
		t.Errorf("SSHCommand = %q %v", cmd, ok)
	}
	if _, ok := SSHCommand("nope", 19778); ok {
		t.Error("SSHCommand accepted an invalid target")
	}
	for target, enabled := range map[string]bool{"rama@hermes-vps": true, "": false, "not a target": false} {
		s := lanState()
		s.SSHTarget = target
		tree := SettingsTree(s, SettingsInnerWidth(560))
		b := findID(tree, "copy-ssh")
		if b == nil || b.Text != "Copy SSH command" || b.Icon != "content_copy" {
			t.Fatalf("target %q: copy button = %+v", target, b)
		}
		if b.Disabled == enabled {
			t.Errorf("target %q: disabled = %v", target, b.Disabled)
		}
		joined := strings.Join(texts(findKey(tree, "copy-ssh-row")), " ")
		if enabled && !strings.Contains(joined, "ssh -N -L 19778:127.0.0.1:19778 rama@hermes-vps") {
			t.Errorf("valid target: caption %q", joined)
		}
		if !enabled && !strings.Contains(joined, "Set SSH target") {
			t.Errorf("invalid target: caption %q", joined)
		}
	}
	local := settingsStates()["local"]
	local.SSHTarget = "rama@hermes-vps"
	if findID(SettingsTree(local, SettingsInnerWidth(640)), "copy-ssh") != nil {
		t.Error("Copy SSH command is client-only")
	}
}

// fakeHost answers one host call over pipes the way the shell would.
func fakeHost(t *testing.T, reply func(*v1.HostCall) *v1.HostReply) (*v1.Client, <-chan *v1.HostCall, func()) {
	t.Helper()
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	c := v1.NewClient(toPlugin, pluginOut)
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()
	calls := make(chan *v1.HostCall, 4)
	go func() {
		dec := v1.NewDecoder(hostIn, v1.ToHost)
		enc := v1.NewEncoder(hostOut)
		for {
			msg, err := dec.Decode()
			if err != nil {
				return
			}
			if call, ok := msg.(*v1.HostCall); ok {
				calls <- call
				_ = enc.Encode(reply(call))
			}
		}
	}()
	return c, calls, func() { toPlugin.Close(); hostOut.Close(); hostIn.Close(); pluginOut.Close() }
}

func TestCopySSHWritesTheClipboardThroughTheHost(t *testing.T) {
	c, calls, stop := fakeHost(t, func(call *v1.HostCall) *v1.HostReply { return &v1.HostReply{ID: call.ID, OK: true} })
	defer stop()
	if err := CopySSHCommand(context.Background(), c, "rama@hermes-vps", 19778); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-calls:
		var p v1.ClipboardWriteParams
		if call.Call != v1.CallClipboardWrite || json.Unmarshal(call.Params, &p) != nil || p.Text != "ssh -N -L 19778:127.0.0.1:19778 rama@hermes-vps" {
			t.Errorf("host saw %s %s", call.Call, call.Params)
		}
	case <-time.After(time.Second):
		t.Fatal("no clipboard.write")
	}
	if err := CopySSHCommand(context.Background(), c, "not a target", 19778); err == nil {
		t.Error("an invalid target must not reach the clipboard")
	}
	select {
	case call := <-calls:
		t.Errorf("invalid target still called %s", call.Call)
	case <-time.After(50 * time.Millisecond):
	}
	refused, _, stop2 := fakeHost(t, func(call *v1.HostCall) *v1.HostReply {
		return &v1.HostReply{ID: call.ID, OK: false, Error: "no clipboard tool"}
	})
	defer stop2()
	if err := CopySSHCommand(context.Background(), refused, "rama@hermes-vps", 19778); err == nil || !strings.Contains(err.Error(), "no clipboard tool") {
		t.Errorf("refused write err = %v", err)
	}
}

func TestCleartextWarningOnlyForANonLoopbackClientOnlyHost(t *testing.T) {
	for _, c := range []struct {
		clientOnly bool
		host       string
		warn       bool
	}{
		{true, "192.168.0.10", true}, {true, "hermes.example.org", true}, {true, "10.0.0.5", true},
		{true, "127.0.0.1", false}, {true, "127.1.2.3", false}, {true, "localhost", false}, {true, "LOCALHOST", false},
		{true, "::1", false}, {true, "[::1]", false},
		{true, "127.0.0.1:19778", true}, {true, "localhost:19777", true},
		{false, "192.168.0.10", false}, {false, "127.0.0.1", false},
	} {
		got := CleartextWarning(c.clientOnly, c.host)
		want := ""
		if c.warn {
			want = "Cleartext HTTP to " + c.host + ": the token and prompts are not encrypted."
		}
		if got != want {
			t.Errorf("CleartextWarning(%v, %q) = %q, want %q", c.clientOnly, c.host, got, want)
		}
		s := SettingsState{Connection: ConnectionFor(c.clientOnly, c.host, 19777, TokenCandidates{}, "", "")}
		for _, granted := range settingsGranted {
			card := findKey(SettingsTree(s, SettingsInnerWidth(granted)), "cleartext")
			if (card != nil) != c.warn {
				t.Errorf("%v %q at %d: warning shown = %v", c.clientOnly, c.host, granted, card != nil)
				continue
			}
			if card == nil {
				continue
			}
			if joined := strings.Join(texts(card), " "); joined != want {
				t.Errorf("warning reads %q, want %q", joined, want)
			}
			var icon *v1.Node
			walk(card, func(n *v1.Node) {
				if n.Kind == v1.KindIcon {
					icon = n
				}
			})
			if icon == nil || icon.Icon != "gpp_maybe" || icon.Tone != v1.ToneWarning {
				t.Errorf("warning icon = %+v", icon)
			}
		}
	}
}

func TestDetectedCaptionsAndPinToggle(t *testing.T) {
	s := settingsStates()["local"]
	joined := strings.Join(texts(findKey(SettingsTree(s, SettingsInnerWidth(640)), "detected")), "|")
	for _, want := range []string{"Detected: Hermes home ~/.hermes", "Detected: Hermes command hermes", "Detected: default model openrouter / model-00"} {
		if !strings.Contains(joined, want) {
			t.Errorf("detected captions %q are missing %q", joined, want)
		}
	}
	s.Detected = SettingsDetected{}
	if findKey(SettingsTree(s, SettingsInnerWidth(640)), "detected") != nil {
		t.Error("no detected values should mean no captions")
	}
	for _, pinned := range []bool{false, true} {
		s.Pinned = pinned
		b := findID(SettingsTree(s, SettingsInnerWidth(640)), "pin-side-panel")
		wantFill := "soft"
		if pinned {
			wantFill = "accent"
		}
		if b == nil || b.Text != "Pin side panel" || b.Icon != "push_pin" || b.Fill != wantFill {
			t.Errorf("pinned=%v: toggle = %+v", pinned, b)
		}
	}
}

func TestSettingsReusesTheModelPicker(t *testing.T) {
	s := lanState()
	inner := SettingsInnerWidth(640)
	tree := SettingsTree(s, inner)
	picker := findKey(tree, "model-picker")
	if picker == nil {
		t.Fatal("no model picker")
	}
	want := modelRowIDs(ModelSection(s.Models, s.Picker, s.DefaultProvider, s.DefaultModel, inner, 400))
	if got := modelRowIDs(picker); strings.Join(got, ",") != strings.Join(want, ",") || len(got) == 0 {
		t.Errorf("settings model rows %v, want the shared picker's %v", got, want)
	}
	for _, id := range []string{"model-apply", "model-persist"} {
		if findID(picker, id) == nil {
			t.Errorf("model picker has no %s", id)
		}
	}
	s.Models, s.DefaultProvider, s.DefaultModel = nil, "", ""
	if !strings.Contains(strings.Join(texts(SettingsTree(s, inner)), "|"), "No models reported by the bridge") {
		t.Error("empty picker should say no models were reported")
	}
}

// composeSettingsPanel stands in for pluginHost.panelTree at v0.2.5: the
// plugin tree in a CardPadding column, then the manifest's visible
// settings as host cards, all in a PanelPadding scroll. sysc-shell's
// internal ui nodes cannot be built from here, so the host controls are
// drawn with v1 equivalents of the same size: a switch per bool, a slider
// per int, an input per string, file or folder.
func composeSettingsPanel(t *testing.T, s SettingsState, clientOnly bool, granted, height int) *v1.Node {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Settings []struct {
			Key, Type, Label, Description string
			Secret                        bool
			Min, Max                      float64
			Default                       any
			VisibleWhen                   *struct {
				Key    string
				Equals any
			} `json:"visible_when"`
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	inner := SettingsInnerWidth(granted)
	cardW := granted - 2*hostPanelPadding
	rowW := cardW - 2*hostCardPadding
	controlW := rowW * 2 / 5
	if controlW > rowW/2 {
		controlW = rowW / 2
	}
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Width: cardW, Padding: hostCardPadding, Gap: 4,
		Children: []*v1.Node{{Kind: v1.KindText, Text: "Settings", Bold: true, Size: "title", Height: 24}}}
	card.Height = 2*hostCardPadding + 24
	for _, st := range m.Settings {
		if st.VisibleWhen != nil && st.VisibleWhen.Equals != clientOnly {
			continue
		}
		var control *v1.Node
		id := "setting-" + strings.ReplaceAll(st.Key, "_", "-")
		switch st.Type {
		case "bool":
			on := st.Default == true || st.Key == "client_only_mode" && clientOnly
			text := "Off"
			if on {
				text = "On"
			}
			control = &v1.Node{Kind: v1.KindButton, ID: id, Text: text, Name: st.Label, Role: "switch", Width: 56, Height: 28, Padding: 6, Events: []v1.EventKind{v1.EventActivate}}
		case "int":
			control = &v1.Node{Kind: v1.KindSlider, ID: id, Name: st.Label, Role: "slider", Min: st.Min, Max: st.Max, Step: 1, Value: st.Max / 2, Width: controlW, Height: 28, Events: []v1.EventKind{v1.EventChange}}
		default:
			control = &v1.Node{Kind: v1.KindTextInput, ID: id, Key: id, Name: st.Label, Role: "textbox", Masked: st.Secret, Text: fmt.Sprint(st.Default), Width: controlW, Height: 32, Padding: 6, Events: []v1.EventKind{v1.EventChange}}
		}
		labelW := rowW - control.Width - 8
		label := &v1.Node{Kind: v1.KindColumn, Width: labelW, Children: []*v1.Node{{Kind: v1.KindText, Text: clip(st.Label, labelW/8)}}}
		h := 32
		if st.Description != "" {
			label.Children = append(label.Children, &v1.Node{Kind: v1.KindText, Text: clip(st.Description, labelW/8), Size: "caption", Tone: v1.ToneSubtle})
			h = 36
		}
		card.Children = append(card.Children, &v1.Node{Kind: v1.KindRow, Width: rowW, Height: h, Gap: 8, PinEnd: true, Children: []*v1.Node{label, control}})
		card.Height += 4 + h
	}
	head := &v1.Node{Kind: v1.KindColumn, Width: cardW, Padding: hostCardPadding, Children: []*v1.Node{SettingsTree(s, inner)}}
	head.Height = head.Children[0].Height + 2*hostCardPadding
	return &v1.Node{Kind: v1.KindColumn, Width: granted, Height: height, Padding: hostPanelPadding, Gap: 8, Children: []*v1.Node{head, card}}
}

func TestComposedSettingsPanelLaysOutAndCaptures(t *testing.T) {
	scenes := map[string]struct {
		s          SettingsState
		clientOnly bool
	}{
		"client-only": {lanState(), true},
		"local":       {settingsStates()["local"], false},
	}
	for name, sc := range scenes {
		for _, granted := range settingsGranted {
			tree := composeSettingsPanel(t, sc.s, sc.clientOnly, granted, 720)
			label := fmt.Sprintf("composed %s %d", name, granted)
			if err := v1.Validate(tree, v1.ViewPanel); err != nil {
				t.Errorf("%s: %v", label, err)
			}
			for _, f := range shelllint.Tree(tree, v1.ViewPanel, granted, 720) {
				t.Errorf("%s: %s", label, f)
			}
			if os.Getenv("CAPTURE") != "1" {
				continue
			}
			preview := os.Getenv("SYSC_PANEL_PREVIEW")
			if preview == "" {
				preview = "sysc-panel-preview"
			}
			data, _ := json.Marshal(tree)
			out := filepath.Join(os.TempDir(), fmt.Sprintf("hermes-settings-%s-%d.png", name, granted))
			cmd := exec.Command(preview, "-width", fmt.Sprint(granted), "-height", "720", "-o", out)
			cmd.Stdin = strings.NewReader(string(data))
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s: capture failed: %v %s", label, err, b)
				continue
			}
			t.Logf("wrote %s", out)
		}
	}
}

// Local mode always runs its bridge on 127.0.0.1:19777; the host and port
// settings belong to client-only. The header once showed a client-only port
// while the local bridge listened on 19777.
func TestLocalConnectionShowsTheLocalBridge(t *testing.T) {
	c := ConnectionFor(false, "192.168.0.10", 19778, TokenCandidates{Local: "tok"}, "", "")
	if c.Host != LocalHost || c.Port != LocalPort {
		t.Fatalf("local connection = %s:%d, want %s:%d", c.Host, c.Port, LocalHost, LocalPort)
	}
	if c := ConnectionFor(true, "192.168.0.10", 19778, TokenCandidates{File: "tok"}, "", ""); c.Host != "192.168.0.10" || c.Port != 19778 {
		t.Fatalf("client-only connection = %s:%d, want the settings", c.Host, c.Port)
	}
}
