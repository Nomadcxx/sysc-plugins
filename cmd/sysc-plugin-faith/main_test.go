package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// fakeHost speaks the host side of the protocol to an in-process plugin.
type fakeHost struct {
	t      *testing.T
	enc    *v1.Encoder
	mu     sync.Mutex
	roots  map[string]*v1.Node
	revs   map[string]uint64
	notify []v1.NotifyParams
	panels []v1.PanelParams
	state  map[string]json.RawMessage
	done   chan struct{}
}

func startPlugin(t *testing.T, state map[string]json.RawMessage, opt options) *fakeHost {
	t.Helper()
	toPlugin, fromHost := io.Pipe()
	toHost, fromPlugin := io.Pipe()
	if state == nil {
		state = map[string]json.RawMessage{}
	}
	h := &fakeHost{t: t, enc: v1.NewEncoder(fromHost), roots: map[string]*v1.Node{}, revs: map[string]uint64{}, state: state, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		_ = run(toPlugin, fromPlugin, opt)
		_ = fromPlugin.Close()
	}()
	t.Cleanup(func() {
		_ = h.send(&v1.HostShutdown{})
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
			t.Error("plugin did not exit after host.shutdown")
		}
		_ = fromHost.Close()
		_ = toHost.Close()
	})
	dec := v1.NewDecoder(toHost, v1.ToHost)
	go func() {
		for {
			msg, err := dec.Decode()
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *v1.HostCall:
				reply := v1.HostReply{ID: m.ID, OK: true}
				h.mu.Lock()
				switch m.Call {
				case v1.CallNotify:
					var p v1.NotifyParams
					_ = json.Unmarshal(m.Params, &p)
					h.notify = append(h.notify, p)
					reply.Result, _ = json.Marshal(v1.NotifyResult{ID: 1})
				case v1.CallPanelOpen:
					var p v1.PanelParams
					_ = json.Unmarshal(m.Params, &p)
					h.panels = append(h.panels, p)
					reply.Result, _ = json.Marshal(v1.PanelResult{ViewID: "panel-x"})
				case v1.CallStateGet:
					var p v1.StateGetParams
					_ = json.Unmarshal(m.Params, &p)
					val, ok := h.state[p.Key]
					reply.Result, _ = json.Marshal(v1.StateGetResult{Found: ok, Value: val})
				case v1.CallStateSet:
					var p v1.StateSetParams
					_ = json.Unmarshal(m.Params, &p)
					h.state[p.Key] = p.Value
				}
				h.mu.Unlock()
				_ = h.send(&reply)
			case *v1.ViewSnapshot:
				h.mu.Lock()
				h.roots[m.ViewID] = m.Root
				h.revs[m.ViewID] = m.Revision
				h.mu.Unlock()
			}
		}
	}()
	if err := h.send(&v1.HostHello{
		Supported:    []v1.Version{{Major: 1, Minor: 4}},
		Plugin:       v1.Identity{ID: "org.sysc.faith", Name: "Faith", Version: "0.1.0"},
		Capabilities: []string{"notifications", "panels", "settings", "state"},
		Limits:       v1.DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

var sendMu sync.Mutex

func (h *fakeHost) send(m v1.Message) error {
	sendMu.Lock()
	defer sendMu.Unlock()
	return h.enc.Encode(m)
}

func (h *fakeHost) open(id string, kind v1.ViewKind) {
	h.t.Helper()
	if err := h.send(&v1.ViewOpen{ViewID: id, View: kind, Entry: map[v1.ViewKind]string{v1.ViewPanel: "panel"}[kind], Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHost) input(view, node string, ev v1.EventKind, button v1.PointerButton) {
	h.t.Helper()
	h.mu.Lock()
	rev := h.revs[view]
	h.mu.Unlock()
	if err := h.send(&v1.InputEvent{ViewID: view, Revision: rev, Node: node, Event: ev, Button: button, Output: "DP-1"}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHost) waitFor(what string, ok func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		good := ok()
		h.mu.Unlock()
		if good {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

func treeText(n *v1.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*v1.Node)
	walk = func(n *v1.Node) {
		if n.Text != "" {
			b.WriteString(n.Text + "|")
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func headerRef(n *v1.Node) string {
	if n == nil || len(n.Children) == 0 || len(n.Children[0].Children) == 0 {
		return ""
	}
	return n.Children[0].Children[0].Text
}

func offline() options {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return options{APIBase: base, CanRead: true}
}

func TestClicksOnTheCross(t *testing.T) {
	h := startPlugin(t, nil, offline())
	h.open("bar-1", v1.ViewBar)
	h.waitFor("the bar", func() bool { return h.roots["bar-1"] != nil })

	// A left click is a primary press, then the release's activate.
	h.input("bar-1", "cross", v1.EventPointer, v1.ButtonPrimary)
	h.input("bar-1", "cross", v1.EventActivate, "")
	h.waitFor("a prayer", func() bool { return len(h.notify) == 1 })
	time.Sleep(200 * time.Millisecond)
	h.mu.Lock()
	n := len(h.notify)
	first := h.notify[0]
	h.mu.Unlock()
	if n != 1 {
		t.Fatalf("one left click sent %d prayers", n)
	}
	if first.Summary == "" || !strings.Contains(first.Body, "\n\n— ") || first.Urgency != v1.UrgencyLow || first.TimeoutMS != 30000 {
		t.Fatalf("prayer = %+v", first)
	}

	h.input("bar-1", "cross", v1.EventActivate, "")
	h.waitFor("a second prayer", func() bool { return len(h.notify) == 2 })
	h.mu.Lock()
	second := h.notify[1]
	h.mu.Unlock()
	if second.Summary == first.Summary {
		t.Fatalf("the same prayer twice in a row: %s", first.Summary)
	}

	h.input("bar-1", "cross", v1.EventPointer, v1.ButtonSecondary)
	h.waitFor("panel.open", func() bool { return len(h.panels) == 1 })
	h.mu.Lock()
	p := h.panels[0]
	prayers := len(h.notify)
	h.mu.Unlock()
	if prayers != 2 {
		t.Fatalf("a right click also sent a prayer (%d prayers)", prayers)
	}
	if p.Entry != "panel" || p.Output != "DP-1" || p.Instance != "bar-1" {
		t.Fatalf("panel.open = %+v", p)
	}
}

func TestPanelNavigationAndOfflineCommentary(t *testing.T) {
	ref, _ := json.Marshal(map[string]int{"book": 42, "chapter": 3, "verse": 16})
	h := startPlugin(t, map[string]json.RawMessage{"ref": ref}, offline())
	h.open("panel-1", v1.ViewPanel)
	h.waitFor("the panel on John 3:16", func() bool { return headerRef(h.roots["panel-1"]) == "John 3:16" })
	h.waitFor("the offline commentary line", func() bool {
		return strings.Contains(treeText(h.roots["panel-1"]), "Commentary unavailable offline.")
	})
	all := treeText(h.roots["panel-1"])
	for _, want := range []string{"For God so loved the world", "See also", "Romans 5:8", "OpenBible.info"} {
		if !strings.Contains(all, want) {
			t.Fatalf("panel lacks %q: %s", want, all)
		}
	}
	h.input("panel-1", "next", v1.EventActivate, "")
	h.waitFor("John 3:17", func() bool { return headerRef(h.roots["panel-1"]) == "John 3:17" })
	h.input("panel-1", "xref:0", v1.EventActivate, "")
	h.waitFor("a cross-reference", func() bool { return headerRef(h.roots["panel-1"]) != "John 3:17" })
	h.waitFor("the saved reference", func() bool {
		var r map[string]int
		return json.Unmarshal(h.state["ref"], &r) == nil && !(r["chapter"] == 3 && r["verse"] == 17)
	})

	h.mu.Lock()
	before := h.revs["panel-1"]
	h.mu.Unlock()
	if err := h.send(&v1.ViewResync{ViewID: "panel-1"}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("a resync snapshot", func() bool { return h.revs["panel-1"] > before })
}

func TestCommentaryOnline(t *testing.T) {
	fixture, err := os.ReadFile("../../plugins/faith/testdata/adam-clarke-GEN-1.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/c/adam-clarke/GEN/1.json" {
			_, _ = w.Write(fixture)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ref, _ := json.Marshal(map[string]int{"book": 0, "chapter": 1, "verse": 1})
	h := startPlugin(t, map[string]json.RawMessage{"ref": ref}, options{APIBase: srv.URL + "/api"})
	h.open("panel-1", v1.ViewPanel)
	h.waitFor("Adam Clarke on Genesis 1:1", func() bool {
		return strings.Contains(treeText(h.roots["panel-1"]), "God in the beginning created")
	})
	if strings.Contains(treeText(h.roots["panel-1"]), "Read the chapter") {
		t.Fatal("the read button showed without xdg-open")
	}
}

func TestRestartRestoresTheBag(t *testing.T) {
	state := map[string]json.RawMessage{}
	h := startPlugin(t, state, offline())
	h.open("bar-1", v1.ViewBar)
	h.waitFor("the bar", func() bool { return h.roots["bar-1"] != nil })
	h.input("bar-1", "cross", v1.EventActivate, "")
	h.waitFor("the saved bag", func() bool { return len(state["bag"]) > 0 })
	h.mu.Lock()
	first := h.notify[0].Summary
	saved := map[string]json.RawMessage{"bag": state["bag"], "ref": state["ref"]}
	h.mu.Unlock()

	h2 := startPlugin(t, saved, offline())
	h2.open("bar-1", v1.ViewBar)
	h2.waitFor("the bar", func() bool { return h2.roots["bar-1"] != nil })
	h2.input("bar-1", "cross", v1.EventActivate, "")
	h2.waitFor("a prayer", func() bool { return len(h2.notify) == 1 })
	h2.mu.Lock()
	next := h2.notify[0].Summary
	var bag struct{ Pos int }
	_ = json.Unmarshal(h2.state["bag"], &bag)
	h2.mu.Unlock()
	if next == first {
		t.Fatalf("the restored bag repeated %q", first)
	}
	h2.waitFor("the bag to advance", func() bool {
		_ = json.Unmarshal(h2.state["bag"], &bag)
		return bag.Pos == 2
	})
}

func TestSettingsChangeTheBar(t *testing.T) {
	h := startPlugin(t, nil, offline())
	h.open("bar-1", v1.ViewBar)
	h.waitFor("the bar", func() bool { return h.roots["bar-1"] != nil })
	if err := h.send(&v1.SettingsChanged{Scope: v1.ScopePlugin, Values: map[string]any{"show_reference": true, "translation": "KJV"}}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the reference on the bar", func() bool {
		r := h.roots["bar-1"]
		return r != nil && len(r.Children) == 2
	})
}
