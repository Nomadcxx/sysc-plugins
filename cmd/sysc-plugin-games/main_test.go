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
	t       *testing.T
	host    io.WriteCloser
	lines   chan []byte
	done    chan error
	ran     chan []string
	stopped bool
	fetch   func(ctx context.Context, key, slug, dir string) string
}

func start(t *testing.T) *harness {
	t.Helper()
	dbPath, procRoot := makeFixture(t)
	input, host := io.Pipe()
	plugin, output := io.Pipe()
	h := &harness{t: t, host: host, lines: make(chan []byte, 64), done: make(chan error, 1), ran: make(chan []string, 8)}
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

func (h *harness) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	h.send(v1.HostShutdown{Type: "host.shutdown"})
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		h.t.Fatal("plugin did not stop")
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
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "b", View: v1.ViewBar, Entry: "bar", Output: "DP-1"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "b" && find(s.Root, "bar") != nil
	})
	pill := find(snapshotOf(line).Root, "bar")
	if pill.Kind != v1.KindButton || pill.Key != "bar" {
		t.Fatalf("pill = %+v", pill)
	}
	if pill.Text != "Hades · 0m" {
		t.Fatalf("pill text = %q", pill.Text)
	}
}

func TestTooltipViewLaysOut(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "t", View: v1.ViewTooltip, Entry: "bar", Output: "DP-1"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "t"
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
		return s.ViewID == "p" && find(s.Root, "card-2") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-1", Event: v1.EventActivate})
	// Hades is running in the fixture, so its primary action is Stop.
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "stop-1") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:favtoggle-2", Event: v1.EventActivate})
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
		if err := json.Unmarshal(p.Value, &prefs); err != nil || !prefs.Favorites["2"] {
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
		return s.ViewID == "p" && find(s.Root, "card-2") != nil && find(s.Root, "card-1") == nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:launch-2", Event: v1.EventActivate})
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
		return s.ViewID == "p" && find(s.Root, "card-2") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-2", Event: v1.EventActivate})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-2") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-2", Event: v1.EventActivate})
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
		return s.ViewID == "p" && find(s.Root, "card-1") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-1", Event: v1.EventPointer, Button: v1.ButtonSecondary})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "config-1") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:config-1", Event: v1.EventActivate})
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
	h.pump(func(l []byte) bool { return find(snapshotOf(l).Root, "bar") != nil })

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
		return s.ViewID == "f" && find(s.Root, "sw-stop-1") != nil
	})
	snap := snapshotOf(line)
	if find(snap.Root, "sw-open-1") == nil {
		t.Fatal("missing sw-open row")
	}
	if find(snap.Root, "switcher-list") == nil {
		t.Fatal("floating view must render the switcher list")
	}

	h.send(v1.InputEvent{Type: "input.event", ViewID: "f", Revision: snap.Revision, Node: "f:sw-open-1", Event: v1.EventActivate})
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
	line := h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })
	h.send(v1.SettingsChanged{Type: "settings.changed", Values: map[string]any{"steamgriddb_key": "k"}})
	line = h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })

	rev := snapshotOf(line).Revision
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: rev, Node: "panel:card-2", Event: v1.EventActivate})
	if got := <-calls; got != "beta" {
		t.Fatalf("first cover job = %q, want beta", got) // worker now parked inside beta's fetch
	}
	line = h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })
	rev = snapshotOf(line).Revision
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: rev, Node: "panel:card-1", Event: v1.EventActivate})
	line = h.pump(func(l []byte) bool { return snapshotOf(l).ViewID == "p" })
	rev = snapshotOf(line).Revision
	// Re-paint (favtoggle saves prefs) must NOT re-enqueue hades while it is dirty.
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: rev, Node: "panel:favtoggle-1", Event: v1.EventActivate})
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
		return s.ViewID == "p" && find(s.Root, "card-2") != nil
	})
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-2")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-2") != nil
	})
	select {
	case got := <-h.ran:
		t.Fatalf("first click launched: %v", got)
	case <-time.After(300 * time.Millisecond):
	}
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-2")
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
		return s.ViewID == "p" && find(s.Root, "card-2") != nil
	})
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:card-2")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-2") != nil
	})
	h.clickLikeHost("p", snapshotOf(line).Revision, "panel:launch-2")
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		b := find(s.Root, "launch-2")
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
