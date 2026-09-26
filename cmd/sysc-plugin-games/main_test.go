package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestPanelFlowAndPrefsPersist(t *testing.T) {
	h := start(t)
	h.send(v1.ViewOpen{Type: "view.open", ViewID: "p", View: v1.ViewPanel, Entry: "panel"})
	line := h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "card-2") != nil
	})
	h.send(v1.InputEvent{Type: "input.event", ViewID: "p", Revision: snapshotOf(line).Revision, Node: "panel:card-1", Event: v1.EventActivate})
	line = h.pump(func(l []byte) bool {
		s := snapshotOf(l)
		return s.ViewID == "p" && find(s.Root, "launch-1") != nil
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
