// Command sysc-plugin-hermes is the sysc-shell Hermes Agent plugin. It
// completes the handshake, runs the Hermes service (local or client-only
// bridge) off the event loop, and publishes each open view from the cached
// service state. Until the first settings.changed arrives every view shows
// Connecting…. The real bar, panels and settings views come in later tasks;
// until then each view is a status placeholder.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	"github.com/Nomadcxx/sysc-plugins/plugins/hermes"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	pluginID      = "org.sysc.hermes"
	pluginName    = "Hermes Agent"
	pluginVersion = "0.1.0"
	serviceID     = "hermes"

	connectingText = "Connecting…"
	barIcon        = "ai-usage"

	// inboundBuffer keeps the reader draining the pipe while the loop works,
	// so host replies are never stuck behind a full queue.
	inboundBuffer = 64
	// shutdownWait bounds host.shutdown: the owned bridge gets its 3 s grace.
	shutdownWait = 4 * time.Second
)

// viewState is what the plugin remembers about one host view.
type viewState struct {
	kind       v1.ViewKind
	entry      string
	instance   string
	output     string
	generation uint32
	width      int
	height     int

	// rev is the last published revision; contentRev the newest revision at
	// which the addressable nodes changed (the mini-docker watermark).
	rev        uint64
	contentRev uint64
	signature  string
	digest     [32]byte
	published  bool

	// Per-view UI state.
	section    hermes.Section
	picker     hermes.ModelPicker
	composer   hermes.ComposerState
	sessions   hermes.SessionsSnapshot
	draft      string
	testResult string
	testing    bool
}

// next records root as the view's next revision. It returns false when the
// tree is byte-identical to the last one, so nothing needs sending.
func (v *viewState) next(root *v1.Node) (uint64, bool) {
	encoded, err := json.Marshal(root)
	if err != nil {
		return v.rev, false
	}
	digest := sha256.Sum256(encoded)
	if v.published && digest == v.digest {
		return v.rev, false
	}
	v.rev++
	if sig := actionSignature(root); !v.published || sig != v.signature {
		v.contentRev = v.rev
		v.signature = sig
	}
	v.digest, v.published = digest, true
	return v.rev, true
}

// accepts reports whether an event names content the view still shows.
func (v *viewState) accepts(m *v1.InputEvent) bool {
	return v.published && m.Revision >= v.contentRev && m.Revision <= v.rev
}

// resync restarts the revision count.
func (v *viewState) resync() {
	v.rev, v.contentRev, v.signature, v.published = 0, 0, "", false
}

type viewSet struct{ m map[string]*viewState }

func newViewSet() *viewSet { return &viewSet{m: map[string]*viewState{}} }

func (vs *viewSet) open(m *v1.ViewOpen) *viewState {
	v := &viewState{kind: m.View, entry: m.Entry, instance: m.Instance, output: m.Output,
		generation: m.Generation, width: m.Width, height: m.Height}
	vs.m[m.ViewID] = v
	return v
}

func (vs *viewSet) get(id string) *viewState { return vs.m[id] }
func (vs *viewSet) close(id string)          { delete(vs.m, id) }

// panel is the open panel view with this entry, or nil. The bar uses it to
// see whether the summary is already up, so a second primary click closes
// it the way noctalia's toggle does.
func (vs *viewSet) panel(entry string) *viewState {
	for _, v := range vs.m {
		if v.kind == v1.ViewPanel && v.entry == entry {
			return v
		}
	}
	return nil
}

// actionSignature describes what an input event can address in a view: the
// id, kind, label, disabled state and event declarations of every node that
// carries an id, in tree order. Copied from mini-docker (a6737ab, PR #233).
// A refresh that changes only the status lines keeps the signature, so an
// action aimed at the tree on screen is honoured when it arrives.
func actionSignature(root *v1.Node) string {
	var b strings.Builder
	var walk func(n *v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if n.ID != "" {
			b.WriteString(n.ID)
			b.WriteByte(0)
			b.WriteString(string(n.Kind))
			b.WriteByte(0)
			b.WriteString(n.Text)
			b.WriteByte(0)
			if n.Disabled {
				b.WriteByte('d')
			}
			for _, event := range n.Events {
				b.WriteByte(0)
				b.WriteString(string(event))
			}
			b.WriteByte('\n')
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(root)
	return b.String()
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "launcher-list", "launcher-select":
			os.Exit(runLauncher(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, runArgv))
		}
	}
	if err := runWith(os.Stdin, os.Stdout, os.Stderr, hermes.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-plugin-hermes:", err)
		os.Exit(1)
	}
}

// bundledScript is the bridge shipped beside the binary: the plugin
// directory is the parent of bin/.
func bundledScript(executable string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(executable)), "scripts", "hermes_bridge.py")
}

// runWith serves the plugin protocol; stderr takes the debug log.
func runWith(in io.Reader, out io.Writer, stderr io.Writer, opts hermes.Options) error {
	logf := func(format string, args ...any) { fmt.Fprintf(stderr, "sysc-plugin-hermes: "+format+"\n", args...) }
	client := v1.NewClient(in, out)
	if _, err := client.Handshake(v1.Identity{ID: pluginID, Name: pluginName, Version: pluginVersion}); err != nil {
		return err
	}
	if err := client.Send(&v1.ServiceStatus{
		ServiceID: serviceID, State: v1.ServiceStateUnknown, Message: connectingText, Revision: 1,
	}); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	saver := hostcall.NewStateSaver(ctx, client)
	if opts.DefaultScript == "" {
		if exe, err := os.Executable(); err == nil {
			opts.DefaultScript = bundledScript(exe)
		}
	}
	svc := hermes.NewService(hermes.Deps{
		Notify: func(summary, body string) {
			hostcall.Async(ctx, client, v1.CallNotify, v1.NotifyParams{Summary: summary, Body: body})
		},
		StateGet: func(ctx context.Context, key string) (json.RawMessage, bool, error) {
			reply, err := hostcall.Call(ctx, client, v1.CallStateGet, v1.StateGetParams{Key: key})
			if err != nil {
				return nil, false, err
			}
			if !reply.OK {
				return nil, false, fmt.Errorf("state.get: %s", reply.Error)
			}
			var res v1.StateGetResult
			if len(reply.Result) > 0 {
				if err := json.Unmarshal(reply.Result, &res); err != nil {
					return nil, false, err
				}
			}
			return res.Value, res.Found, nil
		},
		StateSave: saver.Save,
		Logf:      logf,
	}, opts)
	stop := func() {
		svc.Shutdown(shutdownWait)
		saver.Flush(time.Second)
	}

	// The reader only decodes and hands off; host replies are routed inside
	// Recv, so a host call never waits behind the event loop.
	inbound := make(chan v1.Message, inboundBuffer)
	go func() {
		defer close(inbound)
		for {
			msg, err := client.Recv()
			if err != nil {
				return
			}
			select {
			case inbound <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	a := &app{
		ctx: ctx, client: client, svc: svc, views: newViewSet(),
		pinned: hermes.NewPinnedSurface(client), set: hermes.DefaultSettings(),
		save:   saver.Save,
		avatar: hermes.AvatarPath(""), ui: make(chan func(), inboundBuffer),
		logf: logf, statusRev: 1,
	}
	// Restore the saved pin at start, like Noctalia's panelPinned. The shell
	// refuses surface.open until the bar has an output, so a refused restore
	// stays pending and is retried when the bar opens or the backoff fires.
	go func() {
		a.surfaceMu.Lock()
		want, err := a.pinned.Restore(ctx)
		pinned := a.pinned.Pinned()
		a.surfaceMu.Unlock()
		a.post(func() {
			switch {
			case pinned:
				a.pinnedOpen = true
			case want && err != nil:
				a.restorePending = true
				a.restoreTries = 0
				a.restoreErr = err
				a.scheduleRestore()
			case err != nil:
				logf("hermes: restore pinned surface: %v", err)
			}
			if err := a.publishAll(); err != nil {
				logf("hermes: publish after restore: %v", err)
			}
		})
	}()

	// The host sends plugin settings only when it starts the plugin, not when
	// it respawns it, so reuse the copy saved on the last start. The grace
	// keeps host settings first; whichever of the reply and the timer is last
	// runs the apply check.
	go func() {
		values, note := readCachedSettings(ctx, client)
		a.post(func() {
			a.cacheLoaded, a.cacheValues = true, values
			if note != "" {
				a.logf("%s", note)
			}
			if err := a.tryCachedSettings(); err != nil {
				a.logf("hermes: restore saved settings: %v", err)
			}
		})
	}()
	a.graceTimer = time.AfterFunc(settingsGrace, func() {
		a.post(func() {
			a.graceDone = true
			if err := a.tryCachedSettings(); err != nil {
				a.logf("hermes: restore saved settings: %v", err)
			}
		})
	})
	defer func() {
		if a.graceTimer != nil {
			a.graceTimer.Stop()
		}
	}()

	for {
		select {
		case <-svc.Updates():
			if err := a.publishAll(); err != nil {
				stop()
				return err
			}
		case f := <-a.ui:
			f()
		case msg, ok := <-inbound:
			if !ok {
				// The host closed the pipe: nothing is left to serve.
				stop()
				return nil
			}
			if err := a.handle(msg); err != nil {
				stop()
				if err == errShutdown {
					return nil
				}
				return err
			}
		}
	}
}

var errShutdown = fmt.Errorf("host shutdown")

// restoreBackoff paces the retries of a pinned restore the host refused. The
// bar opening with an output retries sooner; this is the fallback bound.
var restoreBackoff = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
}

// settingsCacheKey holds the last host settings in plugin state. The host
// sends plugin settings only when it starts the plugin, not when it respawns
// it, so a respawned process reconnects from this copy.
const settingsCacheKey = "host_settings"

// settingsGrace lets host settings win over the cached copy at start. Tests
// shorten it.
var settingsGrace = 2 * time.Second

// app is the plugin's event-loop state: the service, the open views and the
// per-view UI state. Everything here runs on the loop except the goroutines
// that make one blocking host call and post their result back.
type app struct {
	ctx    context.Context
	client *v1.Client
	svc    *hermes.Service
	views  *viewSet
	pinned *hermes.PinnedSurface

	set        hermes.Settings
	configured bool
	// save writes plugin state; the cached host settings land there too.
	save         func(string, json.RawMessage)
	graceDone    bool
	cacheLoaded  bool
	cacheChecked bool
	cacheValues  map[string]any
	graceTimer   *time.Timer
	avatar       string
	pinnedOpen   bool
	pinBusy      bool

	// A pinned restore the host refused at startup; retried with backoff or
	// as soon as the bar reports an output.
	restorePending bool
	restoreBusy    bool
	restoreTries   int
	restoreErr     error
	restoreTimer   *time.Timer

	ui        chan func()
	logf      func(string, ...any)
	statusRev uint64
	lastStat  v1.ServiceStatus

	// surfaceMu serialises the pinned surface's blocking host calls.
	surfaceMu sync.Mutex
}

func (a *app) post(f func()) {
	select {
	case a.ui <- f:
	case <-a.ctx.Done():
	}
}

// cancelRestore stops a pending pinned restore. An explicit open, pin, unpin
// or close means the user has taken over, so the saved state no longer needs
// restoring behind their back.
func (a *app) cancelRestore() {
	if a.restoreTimer != nil {
		a.restoreTimer.Stop()
		a.restoreTimer = nil
	}
	a.restorePending = false
}

// scheduleRestore arms the next backoff attempt of a refused restore.
func (a *app) scheduleRestore() {
	if !a.restorePending || a.restoreTimer != nil {
		return
	}
	if a.restoreTries >= len(restoreBackoff) {
		a.restorePending = false
		a.logf("hermes: restore pinned surface: giving up: %v", a.restoreErr)
		return
	}
	delay := restoreBackoff[a.restoreTries]
	a.restoreTries++
	a.restoreTimer = time.AfterFunc(delay, func() {
		a.post(a.restoreOnce)
	})
}

// restoreOnce retries a pending pinned restore. The host call blocks on the
// reply, which the event loop delivers, so it runs off the loop.
func (a *app) restoreOnce() {
	if !a.restorePending || a.restoreBusy {
		return
	}
	if a.restoreTimer != nil {
		a.restoreTimer.Stop()
		a.restoreTimer = nil
	}
	a.restoreBusy = true
	go func() {
		a.surfaceMu.Lock()
		want, err := a.pinned.Restore(a.ctx)
		pinned := a.pinned.Pinned()
		a.surfaceMu.Unlock()
		a.post(func() {
			a.restoreBusy = false
			if !a.restorePending {
				return
			}
			switch {
			case pinned:
				a.restorePending = false
				a.pinnedOpen = true
				if err := a.publishAll(); err != nil {
					a.logf("hermes: publish after restore: %v", err)
				}
			case err == nil && !want:
				a.restorePending = false
			case err != nil:
				a.restoreErr = err
				a.scheduleRestore()
			default:
				a.restorePending = false
			}
		})
	}()
}

func (a *app) env() renderEnv {
	return renderEnv{set: a.set, avatar: a.avatar, pinned: a.pinnedOpen, now: time.Now()}
}

func (a *app) publish(id string) error {
	v := a.views.get(id)
	if v == nil {
		return nil
	}
	root := viewTree(v, a.svc.Snapshot(), a.configured, a.env())
	rev, send := v.next(root)
	if !send {
		return nil
	}
	return a.client.Snapshot(id, rev, root)
}

func (a *app) republish(id string) {
	if err := a.publish(id); err != nil {
		a.logf("hermes: publish %s: %v", id, err)
	}
}

func (a *app) publishAll() error {
	for id := range a.views.m {
		if err := a.publish(id); err != nil {
			return err
		}
	}
	if !a.configured {
		return nil
	}
	st := serviceStatus(a.svc.Snapshot())
	if st.State != a.lastStat.State || st.Busy != a.lastStat.Busy || st.Message != a.lastStat.Message {
		a.statusRev++
		st.Revision = a.statusRev
		a.lastStat = st
		return a.client.Send(&st)
	}
	return nil
}

// readCachedSettings reads the settings saved on the last start. The note is
// a short line for the log; it never carries a value.
func readCachedSettings(ctx context.Context, c *v1.Client) (map[string]any, string) {
	reply, err := hostcall.Call(ctx, c, v1.CallStateGet, v1.StateGetParams{Key: settingsCacheKey})
	if err != nil {
		return nil, fmt.Sprintf("hermes: cannot read saved settings: %v", err)
	}
	if !reply.OK {
		return nil, fmt.Sprintf("hermes: cannot read saved settings: %s", reply.Error)
	}
	var res v1.StateGetResult
	if len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, &res); err != nil {
			return nil, fmt.Sprintf("hermes: cannot read saved settings: %v", err)
		}
	}
	if !res.Found || len(res.Value) == 0 {
		return nil, "hermes: no saved settings to restore"
	}
	var values map[string]any
	if err := json.Unmarshal(res.Value, &values); err != nil {
		return nil, "hermes: saved settings are unreadable"
	}
	if values == nil {
		// A JSON null is this plugin's own all-defaults copy from before the
		// empty object; apply the defaults instead of failing.
		values = map[string]any{}
	}
	return values, ""
}

// applySettings configures the service from host values and paints the views.
func (a *app) applySettings(values map[string]any) error {
	a.set = hermes.SettingsFromValues(values)
	a.svc.Configure(a.set)
	a.configured = true
	return a.publishAll()
}

// tryCachedSettings applies the saved settings once the grace has passed and
// the state.get reply is in, unless host settings arrived first (applying
// them sets configured). Identical settings do not reconnect:
// Service.Configure compares the connection key.
func (a *app) tryCachedSettings() error {
	if a.cacheChecked || a.configured || !a.cacheLoaded || !a.graceDone {
		return nil
	}
	a.cacheChecked = true
	if a.cacheValues == nil {
		return nil
	}
	a.logf("hermes: using the settings saved at the last start")
	return a.applySettings(a.cacheValues)
}

// handle does only cheap state work: HTTP and host calls run in the service
// or in one goroutine that posts its result back.
func (a *app) handle(msg v1.Message) error {
	switch m := msg.(type) {
	case *v1.HostShutdown:
		return errShutdown
	case *v1.SettingsChanged:
		if m.Scope != v1.ScopePlugin && m.Scope != "" {
			return nil
		}
		if a.graceTimer != nil {
			a.graceTimer.Stop()
			a.graceTimer = nil
		}
		// Save the whole map, including a pasted bridge token, so a respawn
		// can reconnect; state.json is mode 0600 like config.json. An
		// all-defaults message has nil values; save it as an empty object so
		// the copy stays decodable.
		values := m.Values
		if values == nil {
			values = map[string]any{}
		}
		if raw, err := json.Marshal(values); err != nil {
			a.logf("hermes: cannot cache settings: %v", err)
		} else if a.save != nil {
			a.save(settingsCacheKey, raw)
		}
		return a.applySettings(values)
	case *v1.ServiceAction:
		if m.ServiceID != serviceID {
			return nil
		}
		a.serviceAction(m.ActionID)
		return nil
	case *v1.ViewOpen:
		a.views.open(m)
		if err := a.publish(m.ViewID); err != nil {
			return err
		}
		// The bar gaining an output means the shell will accept surface.open
		// now, so retry a refused pinned restore without waiting for backoff.
		if m.View == v1.ViewBar && m.Output != "" && a.restorePending {
			a.restoreOnce()
		}
		return nil
	case *v1.ViewResize:
		if v := a.views.get(m.ViewID); v != nil {
			v.width, v.height = m.Width, m.Height
			return a.publish(m.ViewID)
		}
	case *v1.ViewResync:
		if v := a.views.get(m.ViewID); v != nil {
			v.resync()
			return a.publish(m.ViewID)
		}
	case *v1.ViewClose:
		if v := a.views.get(m.ViewID); v != nil && v.kind == v1.ViewFloating && a.pinnedOpen {
			// The user closed the floating surface through the shell.
			a.cancelRestore()
			go func() {
				a.surfaceMu.Lock()
				_ = a.pinned.Unpin(a.ctx)
				a.surfaceMu.Unlock()
				a.post(func() { a.pinnedOpen = false })
			}()
		}
		a.views.close(m.ViewID)
	case *v1.ViewError:
		a.logf("view %s refused revision %d: %s", m.ViewID, m.Revision, m.Reason)
		a.svc.SetViewError(m.Reason)
	case *v1.InputEvent:
		a.input(m)
	}
	return nil
}

func (a *app) serviceAction(action string) {
	switch action {
	case "open":
		a.openChat()
	case "new-session":
		a.svc.Submit("/session/create", nil)
		a.openChat()
	case "interrupt":
		a.interrupt()
	case "refresh":
		a.svc.Refresh()
	case "resume-latest":
		if id := a.svc.Snapshot().State.Session.StoredID; id != "" {
			a.svc.Submit("/session/resume", hermes.ResumeRequest{SessionID: id})
			a.openChat()
		}
	case "start-gateway":
		if !a.set.ClientOnly {
			a.svc.Submit("/gateway/start", map[string]any{})
		}
	case "settings":
		a.openPanelFrom(nil, "settings")
	case "launcher-ask":
		a.launcherAsk()
	}
}

// launcherAsk consumes the launcher's hand-off file, asks the one-shot and
// raises the chat surface. A missing or unusable file is logged and dropped;
// the launcher already reported its own failure.
func (a *app) launcherAsk() {
	dir, err := launcherAskDir(os.Getenv)
	if err != nil {
		a.logf("hermes: %v", err)
		return
	}
	text, msg := takeLauncherAsk(dir)
	if msg != "" {
		a.logf("hermes: %s", msg)
	}
	if text == "" {
		return
	}
	a.svc.OneShot(text)
	a.openChat()
}

// openChat raises the pinned surface when one is pinned, else opens the
// chat panel, as the bar, service action and launcher all do.
func (a *app) openChat() {
	a.cancelRestore()
	go func() {
		a.surfaceMu.Lock()
		err := a.pinned.Open(a.ctx)
		a.surfaceMu.Unlock()
		if err != nil {
			a.post(func() { a.logf("hermes: open: %v", err) })
		}
	}()
}

// openChatFrom raises the chat from the summary or menu panel. Only the
// pinned surface needs that panel closed first: it is a separate surface, so
// panel.open does not replace the panel behind it, and the two host calls go
// out on one goroutine in this order. Otherwise the chat is another plugin
// panel and panel.open replaces the menu or summary itself; a panel.close of
// ours would race that open and could close the panel the click just opened.
func (a *app) openChatFrom(v *viewState) {
	a.cancelRestore()
	if v.kind == v1.ViewPanel && a.pinnedOpen {
		params := v1.PanelParams{Entry: v.entry, Output: v.output, Generation: v.generation, Instance: v.instance}
		go func() {
			if err := a.callPanel(v1.CallPanelClose, params); err != nil {
				a.post(func() { a.logf("hermes: panel.close: %v", err) })
			}
			a.surfaceMu.Lock()
			err := a.pinned.Open(a.ctx)
			a.surfaceMu.Unlock()
			if err != nil {
				a.post(func() { a.logf("hermes: open: %v", err) })
			}
		}()
		return
	}
	a.openChat()
}

func (a *app) openPanelFrom(v *viewState, entry string) {
	a.cancelRestore()
	params := v1.PanelParams{Entry: entry}
	if v != nil {
		params.Output, params.Generation, params.Instance = v.output, v.generation, v.instance
	}
	go func() {
		if err := a.callPanel(v1.CallPanelOpen, params); err != nil {
			a.post(func() { a.logf("hermes: panel.open %s: %v", entry, err) })
		}
		a.post(func() { _ = a.publishAll() })
	}()
}

func (a *app) closePanel(v *viewState) {
	a.cancelRestore()
	if v.kind == v1.ViewFloating {
		// Clear the flag now so the host's ViewClose for this surface does
		// not start a second Unpin.
		a.pinnedOpen = false
		go func() {
			a.surfaceMu.Lock()
			err := a.pinned.Unpin(a.ctx)
			a.surfaceMu.Unlock()
			if err != nil {
				a.post(func() { a.logf("hermes: unpin: %v", err) })
			}
		}()
		return
	}
	params := v1.PanelParams{Entry: v.entry, Output: v.output, Generation: v.generation, Instance: v.instance}
	go func() {
		if err := a.callPanel(v1.CallPanelClose, params); err != nil {
			a.post(func() { a.logf("hermes: panel.close: %v", err) })
		}
	}()
}

func (a *app) callPanel(kind v1.CallKind, params v1.PanelParams) error {
	reply, err := a.client.Call(a.ctx, kind, params)
	if err != nil {
		return err
	}
	if !reply.OK {
		if reply.Error == "" {
			return fmt.Errorf("%s refused", kind)
		}
		return fmt.Errorf("%s: %s", kind, reply.Error)
	}
	return nil
}

// togglePin opens or closes the pinned side panel and saves the state.
func (a *app) togglePin() {
	if a.pinBusy {
		return
	}
	a.cancelRestore()
	a.pinBusy = true
	want := !a.pinnedOpen
	if !want {
		// Clear it now so the surface's ViewClose does not start a second
		// Unpin; the flag is restored if the host call fails.
		a.pinnedOpen = false
	}
	go func() {
		a.surfaceMu.Lock()
		var err error
		if want {
			err = a.pinned.Pin(a.ctx)
		} else {
			err = a.pinned.Unpin(a.ctx)
		}
		a.surfaceMu.Unlock()
		a.post(func() {
			a.pinBusy = false
			if err != nil {
				a.logf("hermes: pin: %v", err)
				a.pinnedOpen = !want
			} else {
				a.pinnedOpen = want
			}
			_ = a.publishAll()
		})
	}()
}

func (a *app) interrupt() {
	a.svc.CancelOneShot()
	a.svc.Submit("/interrupt", map[string]any{})
}

func (a *app) send(id, text string) {
	v := a.views.get(id)
	if v == nil {
		return
	}
	if strings.TrimSpace(text) == "" {
		text = v.draft
	}
	calls, err := hermes.SendPlan(a.svc.Snapshot().State.Session.ID, text, v.composer.OneShot)
	if err != nil {
		return
	}
	for _, call := range calls {
		if req, ok := call.Body.(hermes.PromptRequest); ok && call.Path == "/oneshot" {
			// hermes -z can run for minutes: keep it off the serial action
			// queue, so an interrupt or an approval click is not stuck
			// behind it. Interrupt cancels it like the launcher's Ask.
			a.svc.OneShot(req.Text)
			continue
		}
		a.svc.Submit(call.Path, call.Body)
	}
	v.composer = hermes.AfterSend(v.composer)
	v.draft = ""
	a.republish(id)
}

func (a *app) loadSessions(id string) {
	go func() {
		list, err := a.svc.SessionList(a.ctx)
		a.post(func() {
			v := a.views.get(id)
			if v == nil {
				return
			}
			snap := hermes.SessionsSnapshot{Loaded: true}
			switch {
			case errors.Is(err, hermes.ErrNotFound):
				snap.Unsupported = true
			case err != nil:
				snap.Err = err.Error()
			default:
				snap.Sessions = list
			}
			v.sessions = snap
			a.republish(id)
		})
	}()
}

func (a *app) testConnection(id string) {
	v := a.views.get(id)
	if v == nil {
		return
	}
	v.testing = true
	a.republish(id)
	go func() {
		result := a.svc.TestConnection(a.ctx)
		a.post(func() {
			v := a.views.get(id)
			if v == nil {
				return
			}
			v.testing, v.testResult = false, result
			a.republish(id)
		})
	}()
}

func (a *app) input(m *v1.InputEvent) {
	v := a.views.get(m.ViewID)
	if v == nil || !v.accepts(m) {
		return
	}
	switch m.Event {
	case v1.EventChange:
		switch m.Node {
		case "composer":
			v.draft = m.Text
		case "model-filter":
			touchPicker(v, a.svc.Snapshot())
			v.picker.Filter = m.Text
			a.republish(m.ViewID)
		}
		return
	case v1.EventSubmit:
		if m.Node == "composer" {
			a.send(m.ViewID, m.Text)
		}
		return
	case v1.EventPointer:
		// A primary click arrives twice: EventPointer on press, then
		// EventActivate on release, so only the secondary button is acted on
		// here. Primary and middle presses are left to the release.
		if v.kind == v1.ViewBar && m.Node == "open" && m.Button == v1.ButtonSecondary {
			a.openPanelFrom(v, "menu")
		}
		return
	case v1.EventActivate:
	default:
		return
	}

	snap := a.svc.Snapshot()
	state := snap.State
	// Every menu item closes the menu, as noctalia's does — except the three
	// that open another plugin panel. The host replaces the open panel on
	// panel.open, so a close of ours would only race that open and could
	// close whatever panel arrived just after it. openChatFrom still closes
	// the menu first when the pinned surface, which panel.open does not
	// replace, is what the choice raises.
	if v.kind == v1.ViewPanel && v.entry == "menu" {
		switch m.Node {
		case "open", "new-session", "settings":
		default:
			a.closePanel(v)
		}
	}
	switch {
	case m.Node == "open":
		switch {
		case v.kind == v1.ViewBar:
			if summary := a.views.panel("summary"); summary != nil {
				a.closePanel(summary)
			} else {
				a.openPanelFrom(v, "summary")
			}
		case v.kind == v1.ViewPanel && (v.entry == "summary" || v.entry == "menu"):
			// Open Hermes goes through the same preferred surface as the
			// service action, so a pinned panel is raised instead.
			a.openChatFrom(v)
		default:
			a.openPanelFrom(v, "panel")
		}
	case m.Node == "refresh":
		a.svc.Refresh()
	case m.Node == "pin" || m.Node == "pin-side-panel":
		a.togglePin()
	case m.Node == "close":
		a.closePanel(v)
	case m.Node == "new-session":
		a.svc.Submit("/session/create", nil)
		if v.kind == v1.ViewPanel && (v.entry == "summary" || v.entry == "menu") {
			a.openChatFrom(v)
		} else {
			a.openPanelFrom(v, "panel")
		}
	case m.Node == "interrupt":
		a.interrupt()
	case m.Node == "start-gateway":
		if !a.set.ClientOnly {
			a.svc.Submit("/gateway/start", map[string]any{})
		}
	case m.Node == "stop-gateway":
		if !a.set.ClientOnly {
			a.svc.Submit("/gateway/stop", map[string]any{})
		}
	case m.Node == "settings":
		// The settings panel replaces the menu or summary in place: one
		// panel.open, no close that could land after it.
		a.openPanelFrom(v, "settings")
	case strings.HasPrefix(m.Node, "section:"):
		section, ok := hermes.SectionForNode(m.Node)
		if !ok {
			return
		}
		v.section = hermes.ToggleSection(v.section, section)
		if v.section == hermes.SectionSessions && !v.sessions.Loaded {
			a.loadSessions(m.ViewID)
		}
		a.republish(m.ViewID)
	case m.Node == "sessions-refresh":
		a.loadSessions(m.ViewID)
	case strings.HasPrefix(m.Node, "session:"):
		if req, ok := hermes.ResumeFor(m.Node, v.sessions); ok {
			a.svc.Submit("/session/resume", req)
			a.openPanelFrom(v, "panel")
		}
	case strings.HasPrefix(m.Node, "provider:"):
		if provider, ok := hermes.ProviderForNode(m.Node, state.Summary.Models); ok {
			touchPicker(v, snap)
			v.picker.Provider = provider
			a.republish(m.ViewID)
		}
	case strings.HasPrefix(m.Node, "model:"):
		if option, ok := hermes.ModelForNode(m.Node, state.Summary.Models, a.set.DefaultProvider, a.set.DefaultModel); ok {
			touchPicker(v, snap)
			v.picker.Provider, v.picker.Model = option.Provider, option.Model
			a.republish(m.ViewID)
		}
	case m.Node == "model-persist":
		touchPicker(v, snap)
		v.picker.Persist = !v.picker.Persist
		a.republish(m.ViewID)
	case m.Node == "model-apply":
		touchPicker(v, snap)
		liveProvider, _ := liveModel(snap)
		if req, err := hermes.ApplyModel(v.picker, liveProvider); err != nil {
			// A name the bridge would reject: say so where a failed bridge
			// action is said (R8).
			a.svc.ReportActionError(err.Error())
		} else {
			a.svc.Submit("/model", req)
		}
		a.republish(m.ViewID)
	case m.Node == "one-shot":
		v.composer.OneShot = !v.composer.OneShot
		a.republish(m.ViewID)
	case m.Node == "send":
		a.send(m.ViewID, "")
	case m.Node == "composer-interrupt":
		a.interrupt()
	case m.Node == "composer-new-session":
		a.svc.Submit("/session/create", nil)
	case m.Node == "composer-reset":
		for _, call := range hermes.ResetPlan() {
			a.svc.Submit(call.Path, call.Body)
		}
	case m.Node == "test-connection":
		a.testConnection(m.ViewID)
	case m.Node == "copy-ssh":
		go func() {
			if err := hermes.CopySSHCommand(a.ctx, a.client, a.set.SSHTarget, a.set.Port); err != nil {
				a.post(func() { a.logf("hermes: copy ssh: %v", err) })
			}
		}()
	case strings.HasPrefix(m.Node, "approval:"):
		a.svc.ApprovalClick(m.Node)
	}
}

// serviceStatus maps the derived status onto the host's service states.
func serviceStatus(view hermes.View) v1.ServiceStatus {
	st := v1.ServiceStatus{ServiceID: serviceID, Message: view.Status.Text,
		Busy: view.Pending || view.Status.Key == hermes.StatusBusy}
	switch view.Status.Key {
	case hermes.StatusOffline:
		st.State = v1.ServiceStateUnavailable
	case hermes.StatusError:
		st.State = v1.ServiceStateError
	case hermes.StatusUnknown:
		st.State = v1.ServiceStateUnknown
	default:
		st.State = v1.ServiceStateActive
	}
	return st
}

// renderEnv is the plugin-wide input every view builder needs.
type renderEnv struct {
	set    hermes.Settings
	avatar string
	pinned bool
	now    time.Time
}

// viewTree renders one view from the cached service state. Before settings
// arrive every view is the Connecting… placeholder.
func viewTree(v *viewState, view hermes.View, configured bool, env renderEnv) *v1.Node {
	if !configured {
		return connectingTree(v.kind, v.entry, v.width, v.height)
	}
	width, height := v.width, v.height
	if width <= 0 || height <= 0 {
		width, height = declaredSize(v.entry)
	}
	switch {
	case v.kind == v1.ViewBar:
		return hermes.BarTreeAtWidth(barStateFor(view, env), env.set.HideWhenIdle, env.avatar, width)
	case v.kind == v1.ViewTooltip:
		return hermes.TooltipTree(barStateFor(view, env))
	case v.kind == v1.ViewFloating || v.entry == "pinned":
		return hermes.PinnedTree(panelStateFor(v, view, env), width, height)
	case v.kind == v1.ViewPanel && v.entry == "summary":
		return hermes.SummaryTree(summaryStateFor(view, env), env.avatar, width, height)
	case v.kind == v1.ViewPanel && v.entry == "menu":
		return hermes.MenuTree(view.Status, env.set.ClientOnly, width, height)
	case v.kind == v1.ViewPanel && v.entry == "settings":
		return hermes.SettingsTree(settingsStateFor(v, view, env), hermes.SettingsInnerWidth(width))
	case v.kind == v1.ViewPanel:
		return hermes.PanelTree(panelStateFor(v, view, env), width, height)
	}
	return connectingTree(v.kind, v.entry, width, height)
}

// liveModel is the bridge's current provider and model, falling back to the
// Hermes section when the summary does not carry them.
// touchPicker marks the picker edited. An untouched picker shows the live
// model without holding it, so it is seeded first: a toggle, a filter or
// Apply must not drop the model the user sees.
func touchPicker(v *viewState, view hermes.View) {
	if !v.picker.Dirty {
		provider, model := liveModel(view)
		v.picker = hermes.SeedPicker(v.picker, provider, model)
	}
	v.picker.Dirty = true
}

func liveModel(view hermes.View) (provider, model string) {
	m := view.State.Summary.Model
	if m.Name != "" || m.Provider != "" {
		return m.Provider, m.Name
	}
	return view.State.Hermes.Provider, view.State.Hermes.Model
}

func barStateFor(view hermes.View, env renderEnv) hermes.BarState {
	_, model := liveModel(view)
	return hermes.BarState{
		Status:        view.Status,
		BridgeError:   view.State.Bridge.Error,
		GatewayStatus: view.State.Summary.Gateway.Status,
		Model:         model,
		ClientOnly:    env.set.ClientOnly,
		Host:          env.set.Host,
	}
}

func summaryStateFor(view hermes.View, env renderEnv) hermes.SummaryState {
	state := view.State
	provider, model := liveModel(view)
	return hermes.SummaryState{
		BarState:         barStateFor(view, env),
		Provider:         provider,
		ModelName:        model,
		CronActive:       state.Summary.Cron.Active,
		CronTotal:        state.Summary.Cron.Total,
		CronJobs:         state.Summary.Cron.Jobs,
		MCPEnabled:       state.Summary.MCP.Enabled,
		MCPTotal:         state.Summary.MCP.Total,
		ToolEvents:       state.Summary.Activity.ToolEvents,
		RunningTools:     state.Summary.Activity.RunningTools,
		GatewayPlatforms: state.Summary.Gateway.Platforms,
		ApprovalPending:  state.Approval.Pending,
		ApprovalMessage:  state.Approval.Display().Message,
		ActionError:      strings.TrimSpace(view.ActionError),
		SessionRunning:   state.Session.Running,
		UsageInput:       state.Usage.Input,
		UsageOutput:      state.Usage.Output,
		UsageTotal:       state.Usage.Total,
		UsageCostUSD:     state.Usage.CostUSD,
	}
}

func panelStateFor(v *viewState, view hermes.View, env renderEnv) hermes.PanelState {
	state := view.State
	provider, model := liveModel(view)
	events := make([]hermes.ToolEvent, 0, len(state.Events))
	for _, e := range state.Events {
		events = append(events, hermes.ToolEvent{ID: e.ID, Type: e.Type, Name: e.Name, Text: e.Text})
	}
	return hermes.PanelState{
		Summary:          summaryStateFor(view, env),
		Approvals:        hermes.ApprovalButtons(state),
		Approval:         state.Approval,
		ApprovalTool:     state.Approval.ToolName,
		SessionID:        state.Session.ID,
		Messages:         state.Messages,
		Events:           events,
		LastTool:         state.Summary.Activity.LastTool,
		ClientOnly:       env.set.ClientOnly,
		Pinned:           env.pinned || v.entry == "pinned",
		ShowToolActivity: env.set.ShowToolActivity,
		DefaultProvider:  env.set.DefaultProvider,
		DefaultModel:     env.set.DefaultModel,
		Sessions:         v.sessions,
		Models:           state.Summary.Models,
		Picker:           hermes.SeedPicker(v.picker, provider, model),
		Composer:         v.composer,
		Section:          v.section,
		Avatar:           env.avatar,
		Now:              env.now,
		ActionError:      strings.TrimSpace(view.ActionError),
	}
}

func settingsStateFor(v *viewState, view hermes.View, env renderEnv) hermes.SettingsState {
	state := view.State
	provider, model := liveModel(view)
	connection := hermes.ConnectionFor(env.set.ClientOnly, env.set.Host, env.set.Port,
		hermes.TokenCandidates{Pasted: env.set.TokenManual, File: env.set.TokenFile},
		view.Message, view.ViewError)
	switch view.Token {
	case hermes.TokenPasted:
		connection.TokenSource = hermes.SourcePasted
	case hermes.TokenFile:
		connection.TokenSource = hermes.SourceFile
	case hermes.TokenLocal:
		connection.TokenSource = hermes.SourceLocal
	case hermes.TokenNone:
		connection.TokenSource = hermes.SourceNone
	}
	return hermes.SettingsState{
		Connection:      connection,
		Models:          state.Summary.Models,
		Picker:          hermes.SeedPicker(v.picker, provider, model),
		DefaultProvider: env.set.DefaultProvider,
		DefaultModel:    env.set.DefaultModel,
		Pinned:          env.pinned,
		TestResult:      v.testResult,
		Testing:         v.testing,
		Detected: hermes.SettingsDetected{
			HermesHome:    view.Detected.HermesHome,
			HermesCommand: view.Detected.HermesCommand,
			Provider:      view.Detected.DefaultProvider,
			Model:         view.Detected.DefaultModel,
		},
		SSHTarget: env.set.SSHTarget,
	}
}

// connectingTree is the placeholder each view shows until the bridge client
// exists. Sizes of zero fall back to the manifest's declared size.
func connectingTree(kind v1.ViewKind, entry string, width, height int) *v1.Node {
	switch kind {
	case v1.ViewBar:
		if barwidth.Compact(width) {
			return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: barIcon, Name: "Hermes: " + connectingText, Tooltip: connectingText},
			}}
		}
		return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: barIcon, Name: "Hermes"},
			{Kind: v1.KindText, Text: connectingText, Tone: v1.ToneSubtle},
		}}
	case v1.ViewTooltip:
		return &v1.Node{Kind: v1.KindColumn, Gap: 4, Padding: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: pluginName, Bold: true},
			{Kind: v1.KindText, Text: connectingText, Tone: v1.ToneSubtle},
		}}
	}
	if width <= 0 || height <= 0 {
		width, height = declaredSize(entry)
	}
	return &v1.Node{
		Kind: v1.KindColumn, Width: width, Height: height, Padding: 12, Gap: 10,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Height: 32, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: barIcon, IconSize: 24, Name: "Hermes"},
				{Kind: v1.KindText, Text: pluginName, Size: "title", Bold: true},
			}},
			{Kind: v1.KindText, Text: connectingText, Tone: v1.ToneSubtle},
		},
	}
}

// declaredSize mirrors the manifest's panel sizes; the pinned surface is
// 640×720.
func declaredSize(entry string) (int, int) {
	switch entry {
	case "summary":
		return 380, 480
	case "menu":
		return 260, 300
	case "settings", "pinned":
		return 640, 720
	default:
		return 780, 720
	}
}
