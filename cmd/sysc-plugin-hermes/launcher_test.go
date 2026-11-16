package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// launcherFake records the argv the launcher subcommands would run. Tests
// never start a shell or a plugin protocol session.
type launcherFake struct {
	mu   sync.Mutex
	argv [][]string
	err  error
}

func (f *launcherFake) run(argv []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.argv = append(f.argv, append([]string(nil), argv...))
	return f.err
}

func (f *launcherFake) calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.argv...)
}

func launcherRun(args []string, getenv func(string) string, run func([]string) error) (int, string, string) {
	var out, errOut bytes.Buffer
	code := runLauncher(args, &out, &errOut, getenv, run)
	return code, out.String(), errOut.String()
}

func noEnv(string) string { return "" }

// rowParts mirrors sysc-launch's command provider parser: one row per line,
// `label` and an optional `\tcomment`; the whole line is the selection.
func rowParts(t *testing.T, row string) (string, string) {
	t.Helper()
	if row == "" {
		t.Fatal("empty launcher row")
	}
	if len(row) > 4<<10 {
		t.Fatalf("launcher row is %d bytes, over the 4 KiB provider limit", len(row))
	}
	if strings.ContainsAny(row, "\r\n") {
		t.Fatalf("launcher row %q contains a line break", row)
	}
	name, comment, _ := strings.Cut(row, "\t")
	if name == "" {
		t.Fatalf("launcher row %q has an empty label", row)
	}
	if strings.Contains(comment, "\t") {
		t.Fatalf("launcher row %q has more than one tab", row)
	}
	return name, comment
}

func launcherRowsOf(t *testing.T, out string) []string {
	t.Helper()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("launcher-list output does not end with a newline: %q", out)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("launcher-list printed %d rows, want 4: %q", len(lines), out)
	}
	return lines
}

func TestLauncherListPrintsTheFourNoctaliaRows(t *testing.T) {
	fake := &launcherFake{}
	code, out, errOut := launcherRun([]string{"launcher-list", "summarise the diff"}, noEnv, fake.run)
	if code != 0 {
		t.Fatalf("launcher-list exit = %d, stderr = %q", code, errOut)
	}
	if len(fake.calls()) != 0 {
		t.Fatalf("launcher-list ran %v, want no commands", fake.calls())
	}
	lines := launcherRowsOf(t, out)
	want := []struct{ name, comment string }{
		{"Ask Hermes", "summarise the diff"},
		{"Open Hermes panel", "Show the chat panel"},
		{"Start Hermes session", "Create a new session"},
		{"Resume latest Hermes session", "Reopen the most recent session"},
	}
	for i, w := range want {
		name, comment := rowParts(t, lines[i])
		if name != w.name || comment != w.comment {
			t.Errorf("row %d = %q / %q, want %q / %q", i, name, comment, w.name, w.comment)
		}
	}
}

func TestLauncherListWithoutAQueryAsksForOne(t *testing.T) {
	fake := &launcherFake{}
	code, out, errOut := launcherRun([]string{"launcher-list", ""}, noEnv, fake.run)
	if code != 0 {
		t.Fatalf("launcher-list exit = %d, stderr = %q", code, errOut)
	}
	lines := launcherRowsOf(t, out)
	name, comment := rowParts(t, lines[0])
	if name != "Type a prompt for Hermes" || comment != "Type after /hermes, then choose this row" {
		t.Fatalf("empty-query first row = %q / %q", name, comment)
	}
	for _, line := range lines[1:] {
		rowParts(t, line)
	}
}

func TestLauncherListKeepsAQueryOnOneLine(t *testing.T) {
	fake := &launcherFake{}
	query := "first line\nsecond\tline\r" + strings.Repeat("x", 400)
	code, out, errOut := launcherRun([]string{"launcher-list", query}, noEnv, fake.run)
	if code != 0 {
		t.Fatalf("launcher-list exit = %d, stderr = %q", code, errOut)
	}
	if len(fake.calls()) != 0 {
		t.Fatalf("launcher-list ran %v", fake.calls())
	}
	lines := launcherRowsOf(t, out)
	name, comment := rowParts(t, lines[0])
	if name != "Ask Hermes" {
		t.Fatalf("first row name = %q", name)
	}
	if strings.ContainsAny(comment, "\r\n\t") {
		t.Fatalf("query comment = %q, want one line without tabs", comment)
	}
	if len(comment) > 200 {
		t.Fatalf("query comment is %d bytes, want at most 200", len(comment))
	}
	if !strings.HasPrefix(comment, "first line second line") {
		t.Fatalf("query comment = %q, want the sanitized query", comment)
	}
}

func TestLauncherSelectRunsThePluginActionForEachRow(t *testing.T) {
	for _, tc := range []struct{ row, action string }{
		{"Open Hermes panel\tShow the chat panel", "open"},
		{"Start Hermes session\tCreate a new session", "new-session"},
		{"Resume latest Hermes session\tReopen the most recent session", "resume-latest"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			dir := t.TempDir()
			fake := &launcherFake{}
			getenv := func(key string) string {
				if key == "XDG_RUNTIME_DIR" {
					return dir
				}
				return ""
			}
			code, out, errOut := launcherRun([]string{"launcher-select", tc.row, ""}, getenv, fake.run)
			if code != 0 {
				t.Fatalf("launcher-select exit = %d, stderr = %q", code, errOut)
			}
			if out != "" {
				t.Fatalf("launcher-select stdout = %q, want none", out)
			}
			calls := fake.calls()
			if len(calls) != 1 {
				t.Fatalf("launcher-select ran %v, want one plugin.action", calls)
			}
			checkPluginAction(t, calls[0], tc.action)
			if _, err := os.Stat(filepath.Join(dir, "sysc-hermes", "ask")); !os.IsNotExist(err) {
				t.Fatalf("launcher-select wrote a hand-off file for %s: %v", tc.action, err)
			}
		})
	}
}

// checkPluginAction pins the exact argv and payload the design ledger gives.
func checkPluginAction(t *testing.T, argv []string, action string) {
	t.Helper()
	if len(argv) != 4 || argv[0] != "sysc-shell" || argv[1] != "ipc" || argv[2] != "plugin.action" {
		t.Fatalf("argv = %q, want sysc-shell ipc plugin.action <json>", argv)
	}
	var payload struct {
		PluginID  string `json:"plugin_id"`
		ServiceID string `json:"service_id"`
		ActionID  string `json:"action_id"`
	}
	if err := json.Unmarshal([]byte(argv[3]), &payload); err != nil {
		t.Fatalf("plugin.action payload %q: %v", argv[3], err)
	}
	if payload.PluginID != "org.sysc.hermes" || payload.ServiceID != "hermes" || payload.ActionID != action {
		t.Fatalf("plugin.action payload = %+v, want org.sysc.hermes/hermes/%s", payload, action)
	}
}

func TestLauncherSelectAskWritesA0600HandoffAndCallsLauncherAsk(t *testing.T) {
	dir := t.TempDir()
	fake := &launcherFake{}
	getenv := func(key string) string {
		if key == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}
	code, out, errOut := launcherRun([]string{"launcher-select", "Ask Hermes\tsummarise the diff", "summarise the diff"}, getenv, fake.run)
	if code != 0 {
		t.Fatalf("launcher-select exit = %d, stderr = %q", code, errOut)
	}
	if out != "" {
		t.Fatalf("launcher-select stdout = %q, want none", out)
	}
	calls := fake.calls()
	if len(calls) != 1 {
		t.Fatalf("launcher-select ran %v, want one plugin.action", calls)
	}
	checkPluginAction(t, calls[0], "launcher-ask")

	askDir := filepath.Join(dir, "sysc-hermes")
	info, err := os.Stat(askDir)
	if err != nil {
		t.Fatalf("hand-off directory: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("hand-off directory mode = %o, want 700", perm)
	}
	path := filepath.Join(askDir, "ask")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hand-off file: %v", err)
	}
	if string(data) != "summarise the diff" {
		t.Errorf("hand-off = %q, want the typed query", data)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("hand-off mode = %o, want 600", perm)
	}
}

func TestLauncherSelectAskReplacesThePendingHandoff(t *testing.T) {
	dir := t.TempDir()
	getenv := func(key string) string {
		if key == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}
	if code, _, errOut := launcherRun([]string{"launcher-select", "Ask Hermes\tfirst", "first"}, getenv, (&launcherFake{}).run); code != 0 {
		t.Fatalf("first ask exit = %d, stderr = %q", code, errOut)
	}
	if code, _, errOut := launcherRun([]string{"launcher-select", "Ask Hermes\tsecond", "second"}, getenv, (&launcherFake{}).run); code != 0 {
		t.Fatalf("second ask exit = %d, stderr = %q", code, errOut)
	}
	askDir := filepath.Join(dir, "sysc-hermes")
	entries, err := os.ReadDir(askDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ask" {
		t.Fatalf("hand-off directory = %v, want only the replaced ask file", entries)
	}
	data, err := os.ReadFile(filepath.Join(askDir, "ask"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Fatalf("hand-off = %q, want the last prompt only", data)
	}
}

func TestLauncherSelectAskRefusesAPromptOverOneMegabyte(t *testing.T) {
	dir := t.TempDir()
	fake := &launcherFake{}
	getenv := func(key string) string {
		if key == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}
	query := strings.Repeat("x", (1<<20)+1)
	code, _, errOut := launcherRun([]string{"launcher-select", "Ask Hermes\tlong", query}, getenv, fake.run)
	if code == 0 {
		t.Fatal("launcher-select accepted a prompt over 1 MB")
	}
	if !strings.Contains(errOut, "1 MB") {
		t.Errorf("stderr = %q, want the 1 MB limit in the message", errOut)
	}
	if len(fake.calls()) != 0 {
		t.Errorf("launcher-select ran %v, want no plugin.action", fake.calls())
	}
	if _, err := os.Stat(filepath.Join(dir, "sysc-hermes", "ask")); !os.IsNotExist(err) {
		t.Errorf("oversized prompt left a hand-off file: %v", err)
	}
}

func TestLauncherSelectAskNeedsTheRuntimeDir(t *testing.T) {
	fake := &launcherFake{}
	code, _, errOut := launcherRun([]string{"launcher-select", "Ask Hermes\tprompt", "prompt"}, noEnv, fake.run)
	if code == 0 {
		t.Fatal("launcher-select accepted an ask without XDG_RUNTIME_DIR")
	}
	if !strings.Contains(errOut, "XDG_RUNTIME_DIR") {
		t.Errorf("stderr = %q, want the missing runtime dir named", errOut)
	}
	if len(fake.calls()) != 0 {
		t.Errorf("launcher-select ran %v, want no plugin.action", fake.calls())
	}
}

func TestLauncherSelectOnThePromptRowDoesNothing(t *testing.T) {
	dir := t.TempDir()
	fake := &launcherFake{}
	getenv := func(key string) string {
		if key == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}
	code, _, errOut := launcherRun([]string{"launcher-select", "Type a prompt for Hermes\tType after /hermes, then choose this row", ""}, getenv, fake.run)
	if code != 0 {
		t.Fatalf("prompt row exit = %d, stderr = %q", code, errOut)
	}
	if len(fake.calls()) != 0 {
		t.Errorf("prompt row ran %v, want nothing", fake.calls())
	}
}

func TestLauncherSelectRejectsAnUnknownRow(t *testing.T) {
	fake := &launcherFake{}
	code, _, errOut := launcherRun([]string{"launcher-select", "Something else\t", "x"}, noEnv, fake.run)
	if code == 0 {
		t.Fatal("launcher-select accepted an unknown row")
	}
	if !strings.Contains(errOut, "unknown") {
		t.Errorf("stderr = %q, want an unknown-row message", errOut)
	}
	if len(fake.calls()) != 0 {
		t.Errorf("unknown row ran %v", fake.calls())
	}
}

func TestTakeLauncherAskReadsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ask")
	if err := os.WriteFile(path, []byte("summarise the diff"), 0o600); err != nil {
		t.Fatal(err)
	}
	text, msg := takeLauncherAsk(dir)
	if text != "summarise the diff" || msg != "" {
		t.Fatalf("takeLauncherAsk = %q / %q", text, msg)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hand-off file survived the read: %v", err)
	}
	if text, msg := takeLauncherAsk(dir); text != "" || msg == "" {
		t.Fatalf("second takeLauncherAsk = %q / %q, want an ignored-missing message", text, msg)
	}
}

func TestTakeLauncherAskIgnoresAnOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ask")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	text, msg := takeLauncherAsk(dir)
	if text != "" || !strings.Contains(msg, "1 MB") {
		t.Fatalf("takeLauncherAsk = %q / %q, want the 1 MB message", text, msg)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("oversized hand-off file survived: %v", err)
	}
}

func TestTakeLauncherAskIgnoresAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ask")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	text, msg := takeLauncherAsk(dir)
	if text != "" || msg == "" {
		t.Fatalf("takeLauncherAsk = %q / %q, want an ignored-empty message", text, msg)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty hand-off file survived: %v", err)
	}
}

// launcherRemote answers the bridge routes the launcher path uses and
// records each one-shot prompt.
func launcherRemote(t *testing.T, oneshots chan<- string) (host string, port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Bridge-Token") != "fake-token" && r.URL.Path != "/detect" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oneshot":
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			oneshots <- body.Text
			_, _ = io.WriteString(w, `{"result":"done"}`)
		case "/health":
			_, _ = io.WriteString(w, `{"bridge":{"status":"online"}}`)
		default:
			_, _ = io.WriteString(w, `{"bridge":{"status":"online"},"hermes":{"status":"idle"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	h, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port
}

func seedLauncherAsk(t *testing.T, dir, text string) string {
	t.Helper()
	askDir := filepath.Join(dir, "sysc-hermes")
	if err := os.MkdirAll(askDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(askDir, "ask")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLauncherAskActionRunsTheOneShotAndOpensThePanel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	path := seedLauncherAsk(t, dir, "summarise the diff")

	oneshots := make(chan string, 4)
	host, port := launcherRemote(t, oneshots)
	h, _ := startPlugin(t)
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateActive
	})
	if err := h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "launcher-ask"}); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-oneshots:
		if text != "summarise the diff" {
			t.Fatalf("one-shot prompt = %q, want the hand-off text", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("launcher-ask never reached /oneshot")
	}
	call := h.waitCall(v1.CallPanelOpen)
	var params v1.PanelParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatalf("panel.open params: %v", err)
	}
	if params.Entry != "panel" {
		t.Fatalf("launcher-ask asked for panel %q, want the chat panel", params.Entry)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("launcher-ask left the hand-off file behind: %v", err)
	}
}

func TestLauncherAskActionIgnoresAMissingHandoff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	oneshots := make(chan string, 4)
	host, port := launcherRemote(t, oneshots)
	h, _ := startPlugin(t)
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: clientOnlyValues(host, port)}); err != nil {
		t.Fatal(err)
	}
	h.wait(func(m v1.Message) bool {
		s, ok := m.(*v1.ServiceStatus)
		return ok && s.State == v1.ServiceStateActive
	})
	if err := h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "launcher-ask"}); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-oneshots:
		t.Fatalf("missing hand-off ran a one-shot with %q", text)
	case <-time.After(300 * time.Millisecond):
	}
	if !strings.Contains(h.stderr.String(), "launcher") {
		t.Fatalf("stderr = %q, want a message about the missing launcher hand-off", h.stderr.String())
	}
	// The event loop still serves: an ordinary open still reaches the host.
	if err := h.send(&v1.ServiceAction{ServiceID: serviceID, ActionID: "open"}); err != nil {
		t.Fatal(err)
	}
	h.waitCall(v1.CallPanelOpen)
}

// readmeProviderSnippet returns the first fenced JSON block under the
// README's Launcher section.
func readmeProviderSnippet(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins", "hermes", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	_, after, ok := strings.Cut(text, "\n## Launcher\n")
	if !ok {
		t.Fatal("README has no ## Launcher section")
	}
	_, block, ok := strings.Cut(after, "```json\n")
	if !ok {
		t.Fatal("README's Launcher section has no ```json snippet")
	}
	snippet, _, ok := strings.Cut(block, "\n```")
	if !ok {
		t.Fatal("README's Launcher snippet is not closed")
	}
	return snippet
}

func TestReadmeShipsTheHermesLauncherProvider(t *testing.T) {
	snippet := readmeProviderSnippet(t)
	var cfg struct {
		Launcher struct {
			Providers []struct {
				Name     string   `json:"name"`
				Prefix   string   `json:"prefix"`
				Glyph    string   `json:"glyph"`
				Command  []string `json:"command"`
				OnSelect []string `json:"on_select"`
			} `json:"providers"`
		} `json:"launcher"`
	}
	if err := json.Unmarshal([]byte(snippet), &cfg); err != nil {
		t.Fatalf("README launcher snippet is not valid JSON: %v\n%s", err, snippet)
	}
	if len(cfg.Launcher.Providers) != 1 {
		t.Fatalf("README snippet declares %d providers, want one", len(cfg.Launcher.Providers))
	}
	provider := cfg.Launcher.Providers[0]
	if provider.Prefix != "/hermes" {
		t.Errorf("provider prefix = %q, want /hermes", provider.Prefix)
	}
	if provider.Name == "" || provider.Glyph != "ai-usage" {
		t.Errorf("provider name/glyph = %q/%q", provider.Name, provider.Glyph)
	}
	wantCommand := []string{"sysc-plugin-hermes", "launcher-list", "{query}"}
	if strings.Join(provider.Command, "\x00") != strings.Join(wantCommand, "\x00") {
		t.Errorf("command = %q, want %q", provider.Command, wantCommand)
	}
	wantSelect := []string{"sysc-plugin-hermes", "launcher-select", "{selection}", "{query}"}
	if strings.Join(provider.OnSelect, "\x00") != strings.Join(wantSelect, "\x00") {
		t.Errorf("on_select = %q, want %q", provider.OnSelect, wantSelect)
	}
	if strings.Contains(provider.Command[0], "{query}") || strings.Contains(provider.OnSelect[0], "{selection}") {
		t.Error("the executable must not be a template")
	}
}
