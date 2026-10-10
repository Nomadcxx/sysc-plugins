// Command sysc-plugin-herdr is the Herdr plugin process: it speaks the
// plugin/v1 wire protocol, drives the session service, and dispatches the bar
// and panel interactions onto the herdr actions layer.
//
// This file is wiring only. The service owns discovery, events and
// notifications; the herdr package owns the model, renderers and actions.
package main

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/herdr"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

// lockedWriter serialises whole messages: the encoder writes one message per
// Write, and host calls go out from their own goroutines so a reply the event
// loop is not waiting on can never stall it.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Host-call and action deadlines. Actions are bounded so the protocol loop
// never blocks on a slow socket or terminal.
const (
	callTimeout   = 5 * time.Second
	actionTimeout = 6 * time.Second
	stopTimeout   = herdr.DefaultStopTimeout
)

type view struct {
	kind     v1.ViewKind
	entry    string
	rev      uint64
	instance string
	width    int
	height   int
}

type plugin struct {
	c              *v1.Client
	ctx            context.Context
	views          map[string]*view
	pluginSettings herdr.Settings
	widget         map[string]herdr.WidgetSettings
	service        *herdr.Service
	actions        *herdr.Actions
	peeks          map[string]herdr.Peek
	confirmDelete  string
	newName        string
	actionErr      string

	// results carries a mutation built off the event loop back to it, so every
	// view state change happens on the one goroutine that owns it.
	results chan func()
}

func run(in io.Reader, out io.Writer) error {
	c := v1.NewClient(in, &lockedWriter{w: out})
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.herdr", Name: "Herdr", Version: "0.1.0"})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &plugin{
		c:              c,
		ctx:            ctx,
		views:          map[string]*view{},
		pluginSettings: herdr.DefaultSettings(),
		widget:         map[string]herdr.WidgetSettings{},
		service:        herdr.NewService(herdr.Options{}),
		actions:        herdr.NewActions(""),
		peeks:          map[string]herdr.Peek{},
		results:        make(chan func(), 8),
	}
	go p.service.Start(ctx)
	defer p.service.Close()

	incoming := make(chan v1.Message, 16)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.service.Updates():
			herdr.PrunePeeks(p.service.Model(), p.peeks)
			p.snapshotAll()
		case n := <-p.service.Notifications():
			p.notify(n)
		case fn := <-p.results:
			fn()
			p.snapshotAll()
		case msg := <-incoming:
			if !p.handle(msg) {
				return nil
			}
		}
	}
}

// handle applies one host message. It returns false on shutdown.
func (p *plugin) handle(msg v1.Message) bool {
	switch m := msg.(type) {
	case *v1.HostShutdown:
		return false
	case *v1.ViewOpen:
		p.views[m.ViewID] = &view{
			kind: m.View, entry: m.Entry, instance: m.Instance,
			width: m.Width, height: m.Height,
		}
		p.snapshot(m.ViewID)
	case *v1.ViewClose:
		delete(p.views, m.ViewID)
	case *v1.ViewResync:
		p.snapshot(m.ViewID)
	case *v1.InputEvent:
		p.input(m)
	case *v1.SettingsChanged:
		p.settingsChanged(m)
	}
	return true
}

func (p *plugin) settingsChanged(m *v1.SettingsChanged) {
	switch m.Scope {
	case v1.ScopePlugin:
		p.pluginSettings = herdr.ParseSettings(m.Values)
		p.service.SetSettings(p.pluginSettings)
		p.snapshotAll()
	case v1.ScopeInstance:
		p.widget[m.Instance] = herdr.ParseWidgetSettings(m.Values)
		p.snapshotBars()
	}
}

// nodeAction splits an action identifier into a kind and its arguments without
// a client, so the dispatch grammar is unit-testable on its own.
//
// A pane ID contains colons (w1:p1), so focus and read take the session from
// the first field and the pane from everything after it: "focus:demo:w1:p1"
// is ("focus", "demo", "w1:p1"). Any identifier without a known prefix is
// returned as its own kind with no arguments.
func nodeAction(id string) (kind, arg1, arg2 string) {
	switch {
	case strings.HasPrefix(id, "focus:"):
		return splitPane("focus", id)
	case strings.HasPrefix(id, "read:"):
		return splitPane("read", id)
	case strings.HasPrefix(id, "attach:"):
		return "attach", strings.TrimPrefix(id, "attach:"), ""
	case strings.HasPrefix(id, "stop:"):
		return "stop", strings.TrimPrefix(id, "stop:"), ""
	case strings.HasPrefix(id, "delete:"):
		return "delete", strings.TrimPrefix(id, "delete:"), ""
	default:
		return id, "", ""
	}
}

// splitPane splits "kind:session:pane" where the pane ID may itself hold
// colons: the session is the first field, the pane is everything after it.
func splitPane(kind, id string) (string, string, string) {
	sess, pane, _ := strings.Cut(strings.TrimPrefix(id, kind+":"), ":")
	return kind, sess, pane
}

func (p *plugin) input(m *v1.InputEvent) {
	if m.Node == "newname" {
		switch m.Event {
		case v1.EventChange:
			p.newName = m.Text
			p.snapshotAll()
		case v1.EventSubmit:
			p.newName = m.Text
			p.startNew()
		}
		return
	}
	if m.Event != v1.EventActivate {
		return
	}

	kind, arg1, arg2 := nodeAction(m.Node)
	switch kind {
	case "open":
		p.callPanel(v1.CallPanelOpen, p.panelParams(m, "panel"))
	case "settings":
		p.callPanel(v1.CallPanelOpen, p.panelParams(m, "settings"))
	case "back":
		p.callPanel(v1.CallPanelOpen, p.panelParams(m, "panel"))
	case "close":
		entry := "panel"
		if v, ok := p.views[m.ViewID]; ok && v.entry != "" {
			entry = v.entry
		}
		p.callPanel(v1.CallPanelClose, p.panelParams(m, entry))
	case "refresh":
		p.actionErr = ""
		p.service.Refresh()
		p.snapshotAll()
	case "attach":
		p.attach(arg1)
	case "focus":
		p.focus(arg1, arg2)
	case "read":
		p.read(arg1, arg2)
	case "stop":
		p.stop(arg1)
	case "delete":
		p.confirmDelete = arg1
		p.snapshotAll()
	case "confirmdelete":
		p.confirmDeleteNow()
	case "canceldelete":
		p.confirmDelete = ""
		p.snapshotAll()
	case "newstart":
		p.startNew()
	}
}

func (p *plugin) panelParams(m *v1.InputEvent, entry string) v1.PanelParams {
	params := v1.PanelParams{Entry: entry, Output: m.Output, Generation: m.Generation}
	if v, ok := p.views[m.ViewID]; ok {
		params.Instance = v.instance
	}
	return params
}

// callPanel opens or closes a panel off the loop with a bounded deadline.
func (p *plugin) callPanel(kind v1.CallKind, params v1.PanelParams) {
	go func() {
		ctx, cancel := context.WithTimeout(p.ctx, callTimeout)
		defer cancel()
		_, _ = p.c.Call(ctx, kind, params)
	}()
}

func (p *plugin) attach(name string) {
	info, ok := p.sessionInfo(name)
	if !ok {
		p.actionErr = "herdr: session " + name + " is not running"
		p.snapshotAll()
		return
	}
	p.actionErr = ""
	p.runAction(actionTimeout, func(ctx context.Context) error {
		return p.actions.Attach(ctx, info)
	}, func(err error) {
		if err != nil {
			p.actionErr = err.Error()
		}
	})
}

func (p *plugin) focus(sess, pane string) {
	sock, ok := p.sessionSocket(sess)
	if !ok {
		p.actionErr = "herdr: session " + sess + " is not running"
		p.snapshotAll()
		return
	}
	p.actionErr = ""
	p.runAction(actionTimeout, func(ctx context.Context) error {
		return p.actions.Focus(ctx, sock, pane)
	}, func(err error) {
		if err != nil {
			p.actionErr = err.Error()
		}
	})
}

func (p *plugin) read(sess, pane string) {
	key := herdr.PeekKey(sess, pane)
	if _, open := p.peeks[key]; open {
		delete(p.peeks, key)
		p.snapshotAll()
		return
	}
	sock, ok := p.sessionSocket(sess)
	if !ok {
		p.actionErr = "herdr: session " + sess + " is not running"
		p.snapshotAll()
		return
	}
	p.actionErr = ""
	lines := p.pluginSettings.OutputPreviewLines
	var text string
	p.runAction(actionTimeout, func(ctx context.Context) error {
		var err error
		text, err = p.actions.Read(ctx, sock, pane, lines)
		return err
	}, func(err error) {
		if err != nil {
			p.actionErr = err.Error()
			return
		}
		// Capture the status at store time: a status change during the read
		// must not prune the preview the user just opened.
		status, _ := herdr.PaneStatus(p.service.Model(), sess, pane)
		p.peeks[key] = herdr.Peek{Session: sess, PaneID: pane, Status: status, Text: text, Lines: lines}
	})
}

func (p *plugin) stop(name string) {
	p.actionErr = ""
	p.runAction(stopTimeout, func(ctx context.Context) error {
		return p.actions.Stop(ctx, name)
	}, func(err error) {
		if err != nil {
			p.actionErr = err.Error()
		}
		p.service.Refresh()
	})
}

func (p *plugin) confirmDeleteNow() {
	name := p.confirmDelete
	if name == "" {
		return
	}
	found := false
	for _, s := range p.service.Model().Sessions {
		if s.Name == name {
			found = true
			break
		}
	}
	if !found {
		p.confirmDelete = ""
		p.actionErr = "session no longer exists: " + name
		p.service.Refresh()
		p.snapshotAll()
		return
	}
	p.actionErr = ""
	p.runAction(stopTimeout, func(ctx context.Context) error {
		return p.actions.Delete(ctx, name)
	}, func(err error) {
		if err != nil {
			p.actionErr = err.Error()
		} else {
			p.confirmDelete = ""
		}
		p.service.Refresh()
	})
}

func (p *plugin) startNew() {
	name := p.newName
	p.actionErr = ""
	p.runAction(actionTimeout, func(ctx context.Context) error {
		return p.actions.New(ctx, name)
	}, func(err error) {
		if err != nil {
			p.actionErr = err.Error()
			return
		}
		p.newName = ""
	})
}

// runAction executes fn away from the event loop with a bounded deadline, then
// hands its error back to the loop so after and the republish stay on the one
// goroutine that owns the view state.
func (p *plugin) runAction(timeout time.Duration, fn func(context.Context) error, after func(error)) {
	go func() {
		ctx, cancel := context.WithTimeout(p.ctx, timeout)
		defer cancel()
		err := fn(ctx)
		select {
		case p.results <- func() { after(err) }:
		case <-p.ctx.Done():
		}
	}()
}

// sessionSocket finds the socket of a named session from the current model.
func (p *plugin) sessionSocket(name string) (string, bool) {
	info, ok := p.sessionInfo(name)
	if !ok || info.Socket == "" {
		return "", false
	}
	return info.Socket, true
}

func (p *plugin) sessionInfo(name string) (herdr.SessionInfo, bool) {
	for _, s := range p.service.Model().Sessions {
		if s.Name == name {
			return herdr.SessionInfo{
				Name: s.Name, Default: s.Default, Running: s.Running, Socket: s.Socket,
			}, true
		}
	}
	return herdr.SessionInfo{}, false
}

func (p *plugin) notify(n herdr.Notification) {
	ctx, cancel := context.WithTimeout(p.ctx, callTimeout)
	defer cancel()
	_, _ = p.c.Call(ctx, v1.CallNotify, v1.NotifyParams{
		Summary: n.Title, Body: n.Body, Urgency: v1.UrgencyNormal,
	})
}

func (p *plugin) snapshotAll() {
	for id := range p.views {
		p.snapshot(id)
	}
}

func (p *plugin) snapshotBars() {
	for id, v := range p.views {
		if v.kind == v1.ViewBar {
			p.snapshot(id)
		}
	}
}

func (p *plugin) snapshot(id string) {
	v, ok := p.views[id]
	if !ok {
		return
	}
	m := p.service.Model()
	var root *v1.Node
	switch {
	case v.kind == v1.ViewBar:
		root = herdr.BarTreeAtWidth(m, p.widgetFor(v.instance), v.height, v.width)
	case v.kind == v1.ViewTooltip:
		root = herdr.TooltipTree(m, p.widgetFor(v.instance))
	case v.entry == "settings":
		root = herdr.SettingsPanelTree("Herdr")
	default:
		width, height := v.width, v.height
		if width <= 0 {
			width = herdr.PanelWidth
		}
		if height <= 0 {
			height = herdr.PanelHeight
		}
		root = herdr.PanelTree(herdr.PanelState{
			Model: m, Settings: p.pluginSettings, Peeks: p.peeks,
			ConfirmDelete: p.confirmDelete, NewName: p.newName,
			ActionError: p.actionErr, SettingsEntry: true,
		}, width, height)
	}
	if err := p.c.Snapshot(id, v.rev+1, root); err != nil {
		return
	}
	v.rev++
}

func (p *plugin) widgetFor(instance string) herdr.WidgetSettings {
	if ws, ok := p.widget[instance]; ok {
		return ws
	}
	return herdr.DefaultWidgetSettings()
}
