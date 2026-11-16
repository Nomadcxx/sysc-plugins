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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	// Per-view UI state, owned by the later view tasks.
	section  string
	picker   string
	composer string
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

	views := newViewSet()
	configured := false
	var lastStatus v1.ServiceStatus
	statusRev := uint64(1)

	publish := func(id string, v *viewState) error {
		root := viewTree(v, svc.Snapshot(), configured)
		rev, send := v.next(root)
		if !send {
			return nil
		}
		return client.Snapshot(id, rev, root)
	}
	publishAll := func() error {
		for id, v := range views.m {
			if err := publish(id, v); err != nil {
				return err
			}
		}
		if !configured {
			return nil
		}
		st := serviceStatus(svc.Snapshot())
		if st.State != lastStatus.State || st.Busy != lastStatus.Busy || st.Message != lastStatus.Message {
			statusRev++
			st.Revision = statusRev
			lastStatus = st
			return client.Send(&st)
		}
		return nil
	}

	for {
		select {
		case <-svc.Updates():
			if err := publishAll(); err != nil {
				stop()
				return err
			}
		case msg, ok := <-inbound:
			if !ok {
				// The host closed the pipe: nothing is left to serve.
				stop()
				return nil
			}
			if err := handle(msg, svc, views, &configured, publish, publishAll, logf); err != nil {
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

// handle does only cheap state work: HTTP and host calls run in the service.
func handle(msg v1.Message, svc *hermes.Service, views *viewSet, configured *bool,
	publish func(string, *viewState) error, publishAll func() error, logf func(string, ...any)) error {
	switch m := msg.(type) {
	case *v1.HostShutdown:
		return errShutdown
	case *v1.SettingsChanged:
		if m.Scope != v1.ScopePlugin && m.Scope != "" {
			return nil
		}
		svc.Configure(hermes.SettingsFromValues(m.Values))
		*configured = true
		return publishAll()
	case *v1.ViewOpen:
		return publish(m.ViewID, views.open(m))
	case *v1.ViewResize:
		if v := views.get(m.ViewID); v != nil {
			v.width, v.height = m.Width, m.Height
			return publish(m.ViewID, v)
		}
	case *v1.ViewResync:
		if v := views.get(m.ViewID); v != nil {
			v.resync()
			return publish(m.ViewID, v)
		}
	case *v1.ViewClose:
		views.close(m.ViewID)
	case *v1.ViewError:
		logf("view %s refused revision %d: %s", m.ViewID, m.Revision, m.Reason)
		svc.SetViewError(m.Reason)
	case *v1.InputEvent:
		v := views.get(m.ViewID)
		if v == nil || !v.accepts(m) {
			return nil
		}
		if m.Node == "refresh" && m.Event == v1.EventActivate {
			svc.Refresh()
		}
		if strings.HasPrefix(m.Node, "approval:") && m.Event == v1.EventActivate {
			svc.ApprovalClick(m.Node)
		}
	}
	return nil
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

// viewTree renders one view from the cached service state. Before settings
// arrive every view is the Connecting… placeholder.
func viewTree(v *viewState, view hermes.View, configured bool) *v1.Node {
	if !configured {
		return connectingTree(v.kind, v.entry, v.width, v.height)
	}
	return statusTree(v.kind, v.entry, v.width, v.height, view)
}

// statusTree is the interim view until the UI tasks land: the derived
// status, the connection line and a Refresh button.
func statusTree(kind v1.ViewKind, entry string, width, height int, view hermes.View) *v1.Node {
	status := view.Status
	switch kind {
	case v1.ViewBar:
		if barwidth.Compact(width) {
			return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: status.Icon, Tone: status.Tone, Name: "Hermes: " + status.Text, Tooltip: status.Text},
			}}
		}
		return &v1.Node{Kind: v1.KindRow, Gap: 6, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: status.Icon, Tone: status.Tone, Name: "Hermes"},
			{Kind: v1.KindText, Text: status.Text, Tone: v1.ToneSubtle},
		}}
	case v1.ViewTooltip:
		col := &v1.Node{Kind: v1.KindColumn, Gap: 4, Padding: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: pluginName, Bold: true},
			{Kind: v1.KindText, Text: status.Text, Tone: status.Tone},
		}}
		for _, line := range []string{view.Message, view.Warning} {
			if line != "" {
				col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: line, Tone: v1.ToneSubtle})
			}
		}
		return col
	}
	if width <= 0 || height <= 0 {
		width, height = declaredSize(entry)
	}
	col := &v1.Node{
		Kind: v1.KindColumn, Width: width, Height: height, Padding: 12, Gap: 10,
		Children: []*v1.Node{
			{Kind: v1.KindRow, Height: 32, Gap: 8, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: status.Icon, IconSize: 24, Tone: status.Tone, Name: "Hermes"},
				{Kind: v1.KindText, Text: pluginName, Size: "title", Bold: true},
			}},
			{Kind: v1.KindText, Text: status.Text, Tone: status.Tone},
		},
	}
	lines := []struct {
		text string
		tone v1.Tone
	}{
		{view.Message, v1.ToneSubtle},
		{view.Warning, v1.ToneWarning},
		{view.ActionError, v1.ToneError},
	}
	if view.ViewError != "" {
		lines = append(lines, struct {
			text string
			tone v1.Tone
		}{"View error: " + view.ViewError, v1.ToneError})
	}
	for _, line := range lines {
		if line.text != "" {
			col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: line.text, Tone: line.tone})
		}
	}
	col.Children = append(col.Children, &v1.Node{
		Kind: v1.KindButton, ID: "refresh", Text: "Refresh", Name: "Refresh", Role: "button",
		Disabled: view.Pending, Events: []v1.EventKind{v1.EventActivate},
	})
	return col
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
