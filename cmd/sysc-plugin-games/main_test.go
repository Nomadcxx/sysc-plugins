package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
	_ "modernc.org/sqlite"
)

// fixture: real Lutris schema, Hades "running" via a fake /proc entry.
func makeFixture(t *testing.T) (dbPath, procRoot string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "pga.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE games (id INTEGER PRIMARY KEY, name TEXT UNIQUE,
		sortname TEXT, slug TEXT, installer_slug TEXT, parent_slug TEXT, platform TEXT,
		runner TEXT, executable TEXT, directory TEXT, updated DATETIME, lastplayed DATETIME,
		installed BOOLEAN, installed_at DATETIME, year TEXT, configpath TEXT,
		has_custom_banner BOOLEAN DEFAULT 0, has_custom_icon BOOLEAN DEFAULT 0,
		has_custom_coverart_big BOOLEAN DEFAULT 0, playtime REAL DEFAULT 0,
		service TEXT, service_id TEXT, discord_id TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO games (name,slug,runner,platform,playtime,lastplayed,installed,directory,year)
		VALUES ('Hades','hades','wine','Windows',5.5,1759295749,1,'/Games/Hades','2020'),
		('Beta','beta','linux','Linux',0,0,1,'/Games/Beta','')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE games SET configpath='hades' WHERE name='Hades'`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(dbPath), "games"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dbPath), "games", "hades.yml"), []byte("game: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	procRoot = t.TempDir()
	pid := filepath.Join(procRoot, "4242")
	if err := os.MkdirAll(pid, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pid, "cmdline"), []byte("wine\x00/Games/Hades/Hades\x00"), 0o444); err != nil {
		t.Fatal(err)
	}
	return dbPath, procRoot
}

type harness struct {
	t        *testing.T
	host     io.WriteCloser
	lines    chan []byte
	done     chan error
	ran      chan []string
	procRoot string
	stopped  bool
	fetch    func(ctx context.Context, key, slug, dir string) string
}

func start(t *testing.T) *harness {
	return startWith(t, nil)
}

// startWith builds the harness and lets a test adjust the environment (extra
// libraries, injected launchers) before the plugin starts.
func startWith(t *testing.T, mutate func(*environment)) *harness {
	t.Helper()
	dbPath, procRoot := makeFixture(t)
	home := t.TempDir()
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &harness{t: t, host: host, lines: make(chan []byte, 64), done: make(chan error, 1), ran: make(chan []string, 8), procRoot: procRoot}
	ran := h.ran
	env := environment{
		now: time.Now, // fake /proc dir is born "now", so elapsed = 0m
		run: func(_ context.Context, name string, args ...string) error {
			ran <- append([]string{name}, args...)
			return nil
		},
		fetchGrid: func(ctx context.Context, key, slug, dir string) string {
			if h.fetch == nil {
				return ""
			}
			return h.fetch(ctx, key, slug, dir)
		},
		callTimeout: 2 * time.Second,
		dbPath:      dbPath,
		procRoot:    procRoot,
		home:        home,
		// The harness tests the Lutris fixture; every other backend points at
		// paths that do not exist, so a real Steam/Faugus/Heroic library on
		// the build machine cannot leak into the fixture.
		steamRoots:  []string{filepath.Join(home, "steam")},
		faugusData:  []string{filepath.Join(home, "faugus")},
		faugusState: []string{filepath.Join(home, "faugus")},
		heroicDirs:  []string{filepath.Join(home, "heroic")},
	}
	if mutate != nil {
		mutate(&env)
	}
	go func() {
		err := runPlugin(input, output, env)
		_ = output.Close()
		h.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(plugin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			h.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(h.lines)
	}()
	h.send(map[string]any{"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 7}}, "capabilities": []string{"panels", "settings", "state"}})
	if got := h.next(); messageType(got) != "plugin.hello" {
		t.Fatalf("first message = %s", got)
	}
	t.Cleanup(h.stop)
	return h
}

func (h *harness) send(message any) {
	h.t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.host.Write(append(data, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) next() []byte {
	h.t.Helper()
	select {
	case line, ok := <-h.lines:
		if !ok {
			h.t.Fatal("plugin output closed")
		}
		return line
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for plugin output")
		return nil
	}
}

// pump answers every host.call with "state absent, ok" and returns the first
// message satisfying want.
func (h *harness) pump(want func(line []byte) bool) []byte {
	h.t.Helper()
	for {
		line := h.next()
		switch messageType(line) {
		case v1.TypeViewSnapshot, v1.TypeViewPatch:
			if want(line) {
				return line
			}
		case v1.TypeHostCall:
			var call v1.HostCall
			if json.Unmarshal(line, &call) == nil {
				raw, _ := json.Marshal(v1.StateGetResult{Found: false})
				h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: raw})
			}
		}
	}
}

func (h *harness) snapshotWithin(timeout time.Duration, want func(v1.ViewSnapshot) bool) []byte {
	h.t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line := <-h.lines:
			switch messageType(line) {
			case v1.TypeViewSnapshot:
				if snap := snapshotOf(line); want(snap) {
					return line
				}
			case v1.TypeHostCall:
				var call v1.HostCall
				if json.Unmarshal(line, &call) == nil {
					h.replyTo(call.ID)
				}
			}
		case <-timer.C:
			return nil
		}
	}
}

func (h *harness) settleHostCalls(timeout time.Duration) {
	h.t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line := <-h.lines:
			if messageType(line) == v1.TypeHostCall {
				var call v1.HostCall
				if json.Unmarshal(line, &call) == nil {
					h.replyTo(call.ID)
				}
			}
		case <-timer.C:
			return
		}
	}
}

func (h *harness) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case err := <-h.done:
			if err != nil {
				h.t.Fatal(err)
			}
			return
		case line, ok := <-h.lines:
			if !ok {
				err := <-h.done
				if err != nil {
					h.t.Fatal(err)
				}
				return
			}
			if messageType(line) == v1.TypeHostCall {
				var call v1.HostCall
				if json.Unmarshal(line, &call) == nil {
					h.replyTo(call.ID)
				}
			}
		case <-timer.C:
			h.t.Fatal("plugin did not stop")
		}
	}
}

func messageType(line []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &m)
	return m.Type
}

func snapshotOf(line []byte) v1.ViewSnapshot {
	var s v1.ViewSnapshot
	if err := json.Unmarshal(line, &s); err != nil {
		panic(err)
	}
	return s
}

func find(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if n := find(c, id); n != nil {
			return n
		}
	}
	return nil
}

func TestBarShowsRunningGame(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Width: 240})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		pill := find(s.Root, "bar")
		return s.ViewID == "b" && pill != nil && pill.Text == "Hades · 0m"
	})
	pill := find(snapshotOf(line).Root, "bar")
	if pill.Kind != v1.KindButton || pill.Key != "bar" {
		t.Fatalf("pill = %+v", pill)
	}
	if pill.Text != "Hades · 0m" {
		t.Fatalf("pill text = %q", pill.Text)
	}
}

func TestBarWidthsRemainPerView(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "side", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Width: 32})
	sideLine := h.pump(func(line []byte) bool {
		s := snapshotOf(line)
		return s.ViewID == "side" && find(s.Root, "bar") != nil
	})
	if got := find(snapshotOf(sideLine).Root, "bar").Text; got != "" {
		t.Fatalf("side bar text = %q", got)
	}
	h.nextCall(v1.CallStateSet)

	h.send(v1.ViewOpen{Type: "view.open", ViewID: "wide", View: v1.ViewBar, Entry: "bar", Output: "DP-1", Width: 240})
	wideLine := h.pump(func(line []byte) bool {
		s := snapshotOf(line)
		pill := find(s.Root, "bar")
		return s.ViewID == "wide" && pill != nil && pill.Text == "Hades · 0m"
	})
	if got := find(snapshotOf(wideLine).Root, "bar").Text; got != "Hades · 0m" {
		t.Fatalf("horizontal bar text = %q", got)
	}
}

func TestTooltipViewLaysOut(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "t", View: v1.ViewTooltip, Entry: "bar", Output: "DP-1"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "t" && bytes.Contains(l, []byte("Hades"))
	})
	snap := snapshotOf(line)
	if err := v1.Validate(snap.Root, v1.ViewTooltip); err != nil {
		t.Fatalf("tooltip validate: %v", err)
	}
	for _, f := range shelllint.Tree(snap.Root, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
		t.Errorf("tooltip lint: %v", f)
	}
	if !bytes.Contains(line, []byte("Hades")) {
		t.Fatalf("tooltip must list the running game: %s", line)
	}
}

func TestPanelFlowAndPrefsPersist(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-lutris:2") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-lutris:1", Event: v1.EventActivate})
	// Hades is running in the fixture, so its primary action is Stop.
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "stop-lutris:1") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:favtoggle-lutris:2", Event: v1.EventActivate})
	// favtoggle must round-trip through state.set with the favorites map.
	for {
		l := h.next()
		if messageType(l) != v1.TypeHostCall {
			continue
		}
		var call v1.HostCall
		if json.Unmarshal(l, &call) != nil || call.Call != v1.CallStateSet {
			h.replyTo(call.ID)
			continue
		}
		var p struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		_ = json.Unmarshal(call.Params, &p)
		if p.Key != "prefs" {
			h.replyTo(call.ID)
			continue
		}
		var prefs struct {
			Favorites map[string]bool `json:"favorites"`
		}
		h.replyTo(call.ID)
		if err := json.Unmarshal(p.Value, &prefs); err != nil || !prefs.Favorites["lutris:2"] {
			t.Fatalf("persisted prefs = %s", p.Value)
		}
		return
	}
}

func (h *harness) replyTo(id string) {
	raw, _ := json.Marshal(v1.StateGetResult{Found: false})
	h.send(v1.HostReply{Type: "host.reply", ID: id, OK: true, Result: raw})
}

func TestSearchAndLaunch(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "game-list") != nil
	})
	rev := snapshotOf(line).Revision
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: rev, Node: "panel:search", Event: v1.EventChange, Text: "beta"})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-lutris:2") != nil && find(s.Root, "card-lutris:1") == nil
	})
	h.settleHostCalls(100 * time.Millisecond)
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:launch-lutris:2", Event: v1.EventActivate})
	select {
	case got := <-h.ran:
		if len(got) < 2 || got[0] != "xdg-open" || got[1] != "lutris:rungameid/2" {
			t.Fatalf("launch exec = %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no launch exec")
	}
}

func TestReclickSelectedCardLaunches(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-lutris:2") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-lutris:2", Event: v1.EventActivate})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-lutris:2") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-lutris:2", Event: v1.EventActivate})
	select {
	case got := <-h.ran:
		if len(got) < 2 || got[1] != "lutris:rungameid/2" {
			t.Fatalf("launch exec = %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second click on selected card must launch")
	}
}

func TestConfigOpensGameYml(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-lutris:1") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-lutris:1", Event: v1.EventPointer, Button: v1.ButtonSecondary})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "config-lutris:1") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:config-lutris:1", Event: v1.EventActivate})
	select {
	case got := <-h.ran:
		if len(got) < 2 || got[0] != "xdg-open" || filepath.Base(got[1]) != "hades.yml" {
			t.Fatalf("configure exec = %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no configure exec")
	}
}

// nextCall drains messages (auto-replying host.calls) until a host.call of
// the wanted kind, returning its params.
func (h *harness) nextCall(kind v1.CallKind) json.RawMessage {
	h.t.Helper()
	for {
		line := h.next()
		if messageType(line) != v1.TypeHostCall {
			continue
		}
		var call v1.HostCall
		if json.Unmarshal(line, &call) != nil {
			continue
		}
		if call.Call != kind {
			h.replyTo(call.ID)
			continue
		}
		raw, _ := json.Marshal(v1.SurfaceResult{ViewID: "f"})
		h.send(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true, Result: raw})
		return call.Params
	}
}

func TestBarRightClickOpensSwitcher(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar", Output: "DP-1"})
	h.pump(func(l []byte) bool {
		pill := find(snapshotOf(l).Root, "bar")
		return pill != nil && pill.Text == "Hades · 0m"
	})

	h.send(v1.InputEvent{Type: "input.event", ViewID: "b", Node: "bar", Event: v1.EventPointer, Button: v1.ButtonSecondary})
	params := h.nextCall(v1.CallSurfaceOpen)
	var p struct {
		Key    string `json:"key"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if json.Unmarshal(params, &p) != nil || p.Key != "switcher" || p.Width != 320 || p.Height != 400 {
		t.Fatalf("surface.open params = %s", params)
	}

	h.send(v1.ViewOpen{Type: "view.open", ViewID: "f", View: v1.ViewFloating, Entry: "switcher"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "f" && find(s.Root, "sw-stop-lutris:1") != nil
	})
	snap := snapshotOf(line)
	if find(snap.Root, "sw-open-lutris:1") == nil {
		t.Fatal("missing sw-open row")
	}
	if find(snap.Root, "switcher-list") == nil {
		t.Fatal("floating view must render the switcher list")
	}

	h.send(v1.InputEvent{Type: "input.event", ViewID: "f", Revision: snap.Revision, Node: "f:sw-open-lutris:1", Event: v1.EventActivate})
	h.nextCall(v1.CallPanelOpen)
}

func TestCoverQueueIsAsyncAndCoalesced(t *testing.T) {
	h := start(t)
	dir := t.TempDir()
	calls := make(chan string, 8)
	release := make(chan struct{}, 2)
	h.fetch = func(_ context.Context, _, slug, _ string) string {
		calls <- slug
		<-release
		path := filepath.Join(dir, slug+".png")
		_ = os.WriteFile(path, []byte("fake"), 0o644)
		return path
	}
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })
	h.send(v1.SettingsChanged{Type: "settings.changed", Values: map[string]any{"steamgriddb_key": "k"}})
	line := h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })

	rev := snapshotOf(line).Revision
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: rev, Node: "panel:card-lutris:2", Event: v1.EventActivate})
	if got := <-calls; got != "beta" {
		t.Fatalf("first cover job = %q, want beta", got) // worker now parked inside beta's fetch
	}
	line = h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })
	rev = snapshotOf(line).Revision
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: rev, Node: "panel:card-lutris:1", Event: v1.EventActivate})
	line = h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })
	rev = snapshotOf(line).Revision
	// Re-paint (favtoggle saves prefs) must NOT re-enqueue hades while it is dirty.
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: rev, Node: "panel:favtoggle-lutris:1", Event: v1.EventActivate})
	h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })
	select {
	case extra := <-calls:
		t.Fatalf("coalescing failed: extra fetch for %q", extra)
	case <-time.After(200 * time.Millisecond):
	}

	// Single worker: hades only starts once beta's fetch is released.
	release <- struct{}{}
	release <- struct{}{}
	if got := <-calls; got != "hades" {
		t.Fatalf("second cover job = %q, want hades", got)
	}
	// Both results land asynchronously; panel snapshots must pick them up.
	deadline := time.After(5 * time.Second)
	var seenBeta, seenHades bool
	for !(seenBeta && seenHades) {
		select {
		case l := <-h.lines:
			switch messageType(l) {
			case v1.TypeHostCall:
				var call v1.HostCall
				if json.Unmarshal(l, &call) == nil {
					h.replyTo(call.ID)
				}
			case v1.TypeViewSnapshot:
				if bytes.Contains(l, []byte("beta.png")) {
					seenBeta = true
				}
				if bytes.Contains(l, []byte("hades.png")) {
					seenHades = true
				}
			}
		case <-deadline:
			t.Fatalf("covers never appeared: beta=%v hades=%v", seenBeta, seenHades)
		}
	}
}

func TestViewOpenAndInputStayResponsiveDuringHeldScan(t *testing.T) {
	var stopWriter chan struct{}
	var writerDone chan struct{}
	t.Cleanup(func() {
		if stopWriter != nil {
			close(stopWriter)
			<-writerDone
		}
	})
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "cached", View: v1.ViewPanel, Entry: "panel"})
	h.pump(func(line []byte) bool {
		snap := snapshotOf(line)
		return snap.ViewID == "cached" && find(snap.Root, "card-lutris:1") != nil
	})
	h.nextCall(v1.CallStateSet)

	procDir := filepath.Join(h.procRoot, "4242")
	cmdline := filepath.Join(procDir, "cmdline")
	if err := os.Remove(cmdline); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(cmdline, 0o600); err != nil {
		t.Fatal(err)
	}
	readerReady := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	stopWriter = make(chan struct{})
	writerDone = make(chan struct{})
	go func() {
		defer close(writerDone)
		var readyOnce sync.Once
		for {
			select {
			case <-stopWriter:
				return
			default:
			}
			f, err := os.OpenFile(cmdline, os.O_WRONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				select {
				case <-stopWriter:
					return
				case <-time.After(time.Millisecond):
					continue
				}
			}
			readyOnce.Do(func() { close(readerReady) })
			select {
			case <-release:
			case <-stopWriter:
				_ = f.Close()
				return
			}
			_, _ = f.Write([]byte("wine\x00/Games/Hades/Hades\x00"))
			_ = f.Close()
		}
	}()

	h.send(v1.ViewOpen{Type: "view.open", ViewID: "during-scan", View: v1.ViewPanel, Entry: "panel"})
	select {
	case <-readerReady:
	case <-time.After(time.Second):
		t.Fatal("process scan never reached held cmdline")
	}
	line := h.snapshotWithin(50*time.Millisecond, func(snap v1.ViewSnapshot) bool {
		return snap.ViewID == "during-scan"
	})
	if line == nil {
		t.Fatal("ViewOpen waited for the blocked process scan")
	}
	if find(snapshotOf(line).Root, "card-lutris:1") == nil {
		t.Fatal("ViewOpen did not use the cached game list")
	}
	if find(snapshotOf(line).Root, "library-refreshing") == nil {
		t.Fatal("ViewOpen did not publish refresh progress")
	}

	h.send(v1.InputEvent{Type: "input.event", ViewID: "during-scan", Node: "during-scan:search", Event: v1.EventChange, Text: "beta"})
	h.send(v1.ViewResync{Type: "view.resync", ViewID: "during-scan"})
	line = h.snapshotWithin(50*time.Millisecond, func(snap v1.ViewSnapshot) bool {
		search := find(snap.Root, "search")
		return snap.ViewID == "during-scan" && search != nil && search.Text == "beta"
	})
	if line == nil {
		t.Fatal("input was held behind the blocked process scan")
	}
	if find(snapshotOf(line).Root, "card-lutris:2") == nil || find(snapshotOf(line).Root, "card-lutris:1") != nil {
		t.Fatalf("search snapshot did not apply beta filter: %s", line)
	}

	h.send(v1.ViewClose{Type: "view.close", ViewID: "during-scan"})
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "after-close", View: v1.ViewBar, Entry: "bar", Width: 240})
	if line := h.snapshotWithin(50*time.Millisecond, func(snap v1.ViewSnapshot) bool {
		return snap.ViewID == "after-close"
	}); line == nil {
		t.Fatal("ViewClose and the next ViewOpen waited for the blocked process scan")
	}
}

// clickLikeHost sends what the shell sends for one left click on a node that
// declares both activate and pointer: a primary pointer event on press, then
// activate on release (sysc-shell handlePluginBar).
func (h *harness) clickLikeHost(viewID string, rev uint64, node string) {
	h.t.Helper()
	h.send(v1.InputEvent{Type: "input.event", ViewID: viewID, Revision: rev, Node: node, Event: v1.EventPointer, Button: v1.ButtonPrimary})
	h.send(v1.InputEvent{Type: "input.event", ViewID: viewID, Revision: rev, Node: node, Event: v1.EventActivate})
}

// One left click on the pill must ask for the panel once. Asking twice
// toggled it open and then shut, so it took several clicks to stay open.
func TestBarClickOpensPanelOnce(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar", Output: "DP-1"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "b" && find(s.Root, "bar") != nil
	})
	h.clickLikeHost("b", snapshotOf(line).Revision, "bar:bar")
	opens := 0
	deadline := time.After(700 * time.Millisecond)
	for {
		select {
		case l := <-h.lines:
			if messageType(l) != v1.TypeHostCall {
				continue
			}
			var call v1.HostCall
			if json.Unmarshal(l, &call) == nil {
				if call.Call == v1.CallPanelOpen {
					opens++
				}
				h.replyTo(call.ID)
			}
		case <-deadline:
			if opens != 1 {
				t.Fatalf("one click sent %d panel.open calls, want 1", opens)
			}
			return
		}
	}
}

// One left click on a card selects it; only a second click launches.
func TestCardClickSelectsWithoutLaunching(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-lutris:2") != nil
	})
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-lutris:2")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-lutris:2") != nil
	})
	select {
	case got := <-h.ran:
		t.Fatalf("first click launched: %v", got)
	case <-time.After(300 * time.Millisecond):
	}
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-lutris:2")
	select {
	case got := <-h.ran:
		if len(got) < 2 || got[1] != "lutris:rungameid/2" {
			t.Fatalf("launch exec = %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second click on the selected card must launch")
	}
}

// Launching repaints the panel at once with the spinner and a disabled
// "Launching…" button, so the wait reads as work in progress.
func TestLaunchShowsTheSpinnerUntilTheGameIsSeen(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-lutris:2") != nil
	})
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-lutris:2")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-lutris:2") != nil
	})
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:launch-lutris:2")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		b := find(s.Root, "launch-lutris:2")
		return s.ViewID == "p" && b != nil && b.Disabled && b.Text == "Launching…"
	})
	var spinners int
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if n.Kind == v1.KindSpinner {
			spinners++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(snapshotOf(line).Root)
	if spinners != 2 {
		t.Fatalf("launching panel has %d spinners, want the card's and the detail's", spinners)
	}
}

// The gear in the deck header opens the host-rendered settings panel, and the
// settings panel's back button returns to the deck — both as one panel.open
// with the swapped entry, never a close+open pair.
func TestSettingsGearOpensHostSettingsPanel(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "settings") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:settings", Event: v1.EventActivate})
	if entry := h.panelEntry(h.nextCall(v1.CallPanelOpen)); entry != "settings" {
		t.Fatalf("gear opened %q, want the settings entry", entry)
	}

	h.send(v1.ViewOpen{Type: "view.open", ViewID: "s", View: v1.ViewPanel, Entry: "settings"})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "s" && find(s.Root, "back") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "s", Revision: snapshotOf(line).Revision, Node: "s:back", Event: v1.EventActivate})
	if entry := h.panelEntry(h.nextCall(v1.CallPanelOpen)); entry != "panel" {
		t.Fatalf("back opened %q, want the deck entry", entry)
	}
}

func (h *harness) panelEntry(params json.RawMessage) string {
	h.t.Helper()
	var p struct {
		Entry string `json:"entry"`
	}
	_ = json.Unmarshal(params, &p)
	return p.Entry
}

// A Steam game imported into Lutris (service/service_id) and added to Faugus
// (runner Steam, path = appid) must render as ONE card, and switching the
// default launcher must switch which library serves and launches it.
func TestDuplicatesMergeAndFollowTheDefaultLauncher(t *testing.T) {
	faugusData := t.TempDir()
	if err := os.WriteFile(filepath.Join(faugusData, "games.json"),
		[]byte(`[{"gameid": "2", "title": "Portal 2", "path": "620", "runner": "Steam"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := startWith(t, func(env *environment) {
		db, err := sql.Open("sqlite", env.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`INSERT INTO games (name, slug, runner, installed, service, service_id, directory)
			VALUES ('Portal 2','portal-2','steam',1,'steam','620','/Games/Portal2')`); err != nil {
			t.Fatal(err)
		}
		env.faugusData = []string{faugusData}
		env.lookPath = func(name string) (string, error) {
			if name == "faugus-launcher" {
				return "/usr/bin/faugus-launcher", nil
			}
			return "", os.ErrNotExist
		}
	})
	h.send(v1.SettingsChanged{Type: "settings.changed", Values: map[string]any{"default_launcher": "lutris"}})
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})

	// Lutris is the default: its copy wins, and Faugus' copy is folded in.
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-lutris:3") != nil
	})
	if find(snapshotOf(line).Root, "card-faugus:2") != nil {
		t.Fatal("the Faugus copy must not be a second card")
	}
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-lutris:3")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-lutris:3") != nil
	})
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:launch-lutris:3")
	select {
	case got := <-h.ran:
		if len(got) < 2 || got[0] != "xdg-open" || got[1] != "lutris:rungameid/3" {
			t.Fatalf("default-lutris launch = %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no launch exec")
	}

	// Switch the default: the merged card is now served by Faugus and its
	// launch goes through faugus-launcher with the Faugus gameid.
	h.send(v1.SettingsChanged{Type: "settings.changed", Values: map[string]any{"default_launcher": "faugus"}})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-faugus:2") != nil
	})
	if find(snapshotOf(line).Root, "card-lutris:3") != nil {
		t.Fatal("the Lutris copy must not reappear as a second card")
	}
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-faugus:2")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-faugus:2") != nil && bytes.Contains(l, []byte("Also in Lutris"))
	})
	// A Steam import's stored path is an appid, so the installed check must
	// not disable Launch on the Faugus-served card.
	if b := find(snapshotOf(line).Root, "launch-faugus:2"); b.Disabled {
		t.Fatalf("the merged Faugus card's launch button is disabled: %+v", b)
	}
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:launch-faugus:2")
	select {
	case got := <-h.ran:
		want := "faugus-launcher --game 2"
		if strings.Join(got, " ") != want {
			t.Fatalf("default-faugus launch = %v, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no launch exec")
	}
}
