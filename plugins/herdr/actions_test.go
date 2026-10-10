package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"testing"
)

// startFakeAPI stands up a one-shot unix-socket server, records the single
// request Call sends, and replies with resultJSON. Call dials "unix", so a real
// listener is required (net.Pipe will not work).
func startFakeAPI(t *testing.T, resultJSON string) (string, chan capturedReq) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "herdr.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	reqs := make(chan capturedReq, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     string         `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			return
		}
		reqs <- capturedReq{Method: req.Method, Params: req.Params}
		_ = writeLine(conn, []byte(`{"id":"c1","result":`+resultJSON+`}`))
	}()
	return sock, reqs
}

type capturedReq struct {
	Method string
	Params map[string]any
}

func TestFocusParams(t *testing.T) {
	sock, reqs := startFakeAPI(t, `{}`)
	a := NewActions("herdr")
	if err := a.Focus(context.Background(), sock, "w1:p1"); err != nil {
		t.Fatalf("Focus: %v", err)
	}
	req := <-reqs
	if req.Method != "agent.focus" {
		t.Errorf("method = %q, want agent.focus", req.Method)
	}
	if !reflect.DeepEqual(req.Params, map[string]any{"target": "w1:p1"}) {
		t.Errorf("params = %#v, want target=w1:p1 only", req.Params)
	}
}

func TestReadParams(t *testing.T) {
	sock, reqs := startFakeAPI(t, `{"text":"hello","truncated":true}`)
	a := NewActions("herdr")
	text, err := a.Read(context.Background(), sock, "w1:p1", 20)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if text != "hello" {
		t.Errorf("text = %q, want hello", text)
	}
	req := <-reqs
	if req.Method != "agent.read" {
		t.Errorf("method = %q, want agent.read", req.Method)
	}
	want := map[string]any{
		"target":     "w1:p1",
		"source":     "recent",
		"lines":      float64(20),
		"strip_ansi": true,
	}
	if !reflect.DeepEqual(req.Params, want) {
		t.Errorf("params = %#v, want %#v", req.Params, want)
	}
}

func TestReadClampsLines(t *testing.T) {
	a := NewActions("herdr")
	for _, tc := range []struct{ in, want int }{{0, 1}, {-5, 1}, {50, 50}, {999, 200}} {
		sock, reqs := startFakeAPI(t, `{"text":"x"}`)
		if _, err := a.Read(context.Background(), sock, "p", tc.in); err != nil {
			t.Fatalf("Read(%d): %v", tc.in, err)
		}
		req := <-reqs
		if req.Params["lines"] != float64(tc.want) {
			t.Errorf("lines %d clamped to %v, want %d", tc.in, req.Params["lines"], tc.want)
		}
	}
}

func TestStopCommand(t *testing.T) {
	var gotBin string
	var gotArgs []string
	a := NewActions("herdr")
	a.Run = func(_ context.Context, bin string, args ...string) ([]byte, error) {
		gotBin, gotArgs = bin, args
		return []byte(`{}`), nil
	}
	if err := a.Stop(context.Background(), "demo"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if gotBin != "herdr" {
		t.Errorf("bin = %q, want herdr", gotBin)
	}
	want := []string{"session", "stop", "demo", "--json"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestDeleteCommand(t *testing.T) {
	var gotArgs []string
	a := NewActions("herdr")
	a.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(`{}`), nil
	}
	if err := a.Delete(context.Background(), "demo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	want := []string{"session", "delete", "demo", "--json"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestDeleteRefusesDefault(t *testing.T) {
	called := false
	a := NewActions("herdr")
	a.Run = func(context.Context, string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	if err := a.Delete(context.Background(), "default"); err == nil {
		t.Fatal("Delete(default) = nil, want error")
	}
	if called {
		t.Fatal("Delete(default) called Run; want refusal before any command")
	}
}

func TestNewRejectsInvalidName(t *testing.T) {
	called := false
	a := NewActions("herdr")
	a.Spawn = func(string, ...string) error {
		called = true
		return nil
	}
	if err := a.New(context.Background(), "bad name"); err == nil {
		t.Fatal("New(invalid) = nil, want error")
	}
	if called {
		t.Fatal("New(invalid) spawned a terminal; want no side effects")
	}
}

// stubTerminal forces selection to kitty so Spawn argv is deterministic.
func stubTerminal(a *Actions) {
	a.LookPath = func(name string) (string, error) {
		if name == "kitty" {
			return "/usr/bin/kitty", nil
		}
		return "", errors.New("not found")
	}
}

func TestNewSpawnsTerminal(t *testing.T) {
	t.Setenv("TERMINAL", "")
	a := NewActions("herdr")
	stubTerminal(a)
	var gotCmd string
	var gotArgs []string
	a.Spawn = func(cmd string, args ...string) error {
		gotCmd, gotArgs = cmd, args
		return nil
	}
	if err := a.New(context.Background(), "demo"); err != nil {
		t.Fatalf("New: %v", err)
	}
	if gotCmd != "/usr/bin/kitty" {
		t.Errorf("cmd = %q, want the selected terminal", gotCmd)
	}
	want := []string{"--", "herdr", "--session", "demo"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestAttachUsesSessionFlagWhenNamed(t *testing.T) {
	t.Setenv("TERMINAL", "")
	cases := []struct {
		name string
		info SessionInfo
		want []string
	}{
		{"default", SessionInfo{Name: "default", Default: true}, []string{"--", "herdr"}},
		{"named", SessionInfo{Name: "demo"}, []string{"--", "herdr", "--session", "demo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewActions("herdr")
			stubTerminal(a)
			var gotArgs []string
			a.Spawn = func(_ string, args ...string) error {
				gotArgs = args
				return nil
			}
			if err := a.Attach(context.Background(), tc.info); err != nil {
				t.Fatalf("Attach: %v", err)
			}
			if !reflect.DeepEqual(gotArgs, tc.want) {
				t.Errorf("args = %v, want %v", gotArgs, tc.want)
			}
		})
	}
}

func TestAttachRejectsInvalidName(t *testing.T) {
	t.Setenv("TERMINAL", "")
	a := NewActions("herdr")
	stubTerminal(a)
	a.Spawn = func(string, ...string) error { return nil }
	if err := a.Attach(context.Background(), SessionInfo{Name: "bad name"}); err == nil {
		t.Fatal("Attach(invalid) = nil, want error")
	}
}

func TestTerminalSelectionPreferEnv(t *testing.T) {
	found := func(name string) (string, error) { return "/found/" + name, nil }
	got, err := pickTerminal(found, "foot")
	if err != nil {
		t.Fatalf("pickTerminal: %v", err)
	}
	if got != "/found/foot" {
		t.Errorf("got %q, want /found/foot", got)
	}

	// An unusable $TERMINAL falls through to the fallback list.
	noEnv := func(name string) (string, error) {
		if name == "wezterm" {
			return "", errors.New("nope")
		}
		return "/found/" + name, nil
	}
	got, err = pickTerminal(noEnv, "wezterm")
	if err != nil {
		t.Fatalf("pickTerminal: %v", err)
	}
	if got != "/found/alacritty" {
		t.Errorf("got %q, want fallback /found/alacritty", got)
	}
}

func TestTerminalSelectionFallbackOrder(t *testing.T) {
	only := func(names ...string) func(string) (string, error) {
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		return func(name string) (string, error) {
			if set[name] {
				return "/found/" + name, nil
			}
			return "", errors.New("not found")
		}
	}
	cases := []struct {
		found []string
		want  string
	}{
		{[]string{"kitty", "foot"}, "/found/foot"},
		{[]string{"xfce4-terminal", "kitty"}, "/found/kitty"},
		{[]string{"xfce4-terminal"}, "/found/xfce4-terminal"},
	}
	for _, tc := range cases {
		got, err := pickTerminal(only(tc.found...), "")
		if err != nil {
			t.Fatalf("pickTerminal(%v): %v", tc.found, err)
		}
		if got != tc.want {
			t.Errorf("pickTerminal(%v) = %q, want %q", tc.found, got, tc.want)
		}
	}
}

func TestTerminalSelectionNoneError(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("not found") }
	if _, err := pickTerminal(none, ""); err == nil || err.Error() != "no terminal found" {
		t.Fatalf("err = %v, want \"no terminal found\"", err)
	}
	if _, err := pickTerminal(none, "wezterm"); err == nil || err.Error() != "no terminal found" {
		t.Fatalf("err with unusable $TERMINAL = %v, want \"no terminal found\"", err)
	}
}

func TestTerminalArgsTable(t *testing.T) {
	herdr := []string{"herdr", "--session", "demo"}
	cases := []struct {
		term string
		want []string
	}{
		{"alacritty", []string{"alacritty", "-e", "herdr", "--session", "demo"}},
		{"foot", []string{"foot", "--", "herdr", "--session", "demo"}},
		{"kitty", []string{"kitty", "--", "herdr", "--session", "demo"}},
		{"ghostty", []string{"ghostty", "-e", "herdr", "--session", "demo"}},
		{"xfce4-terminal", []string{"xfce4-terminal", "-x", "herdr", "--session", "demo"}},
		{"wezterm", []string{"wezterm", "-e", "herdr", "--session", "demo"}},
		{"/usr/bin/kitty", []string{"/usr/bin/kitty", "--", "herdr", "--session", "demo"}},
	}
	for _, tc := range cases {
		if got := terminalArgs(tc.term, herdr); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("terminalArgs(%q) = %v, want %v", tc.term, got, tc.want)
		}
	}
}

func TestTerminalsOverrideFallback(t *testing.T) {
	t.Setenv("TERMINAL", "")
	a := NewActions("herdr")
	a.Terminals = []string{"wezterm"}
	var got []string
	a.LookPath = func(name string) (string, error) {
		got = append(got, name)
		if name == "wezterm" {
			return "/found/wezterm", nil
		}
		return "", errors.New("not found")
	}
	term, err := a.selectTerminal()
	if err != nil {
		t.Fatalf("selectTerminal: %v", err)
	}
	if term != "/found/wezterm" {
		t.Errorf("term = %q, want /found/wezterm", term)
	}
	if !reflect.DeepEqual(got, []string{"wezterm"}) {
		t.Errorf("looked up %v; override list should replace the default fallbacks", got)
	}
}
