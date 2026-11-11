// Command sysc-plugin-hermes is the sysc-shell Hermes Agent plugin. This is
// the scaffold: it completes the handshake and shows every view it is opened
// as in a Connecting… state. The bridge client, both modes and the real views
// come in later tasks.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	pluginID      = "org.sysc.hermes"
	pluginName    = "Hermes Agent"
	pluginVersion = "0.1.0"
	serviceID     = "hermes"

	connectingText = "Connecting…"
	barIcon        = "ai-usage"
)

// openView is what the plugin remembers about one host view.
type openView struct {
	kind          v1.ViewKind
	entry         string
	width, height int
	revision      uint64
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-plugin-hermes:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	client := v1.NewClient(in, out)
	if _, err := client.Handshake(v1.Identity{ID: pluginID, Name: pluginName, Version: pluginVersion}); err != nil {
		return err
	}
	if err := client.Send(&v1.ServiceStatus{
		ServiceID: serviceID, State: v1.ServiceStateUnknown, Message: connectingText, Revision: 1,
	}); err != nil {
		return err
	}
	views := make(map[string]*openView)
	publish := func(id string, view *openView) error {
		view.revision++
		return client.Snapshot(id, view.revision, connectingTree(view.kind, view.entry, view.width, view.height))
	}
	for {
		msg, err := client.Recv()
		if err != nil {
			// The host closed the pipe: nothing is left to serve.
			return nil
		}
		switch m := msg.(type) {
		case *v1.HostShutdown:
			return nil
		case *v1.ViewOpen:
			view := &openView{kind: m.View, entry: m.Entry, width: m.Width, height: m.Height}
			views[m.ViewID] = view
			if err := publish(m.ViewID, view); err != nil {
				return err
			}
		case *v1.ViewResize:
			if view, ok := views[m.ViewID]; ok {
				view.width, view.height = m.Width, m.Height
				if err := publish(m.ViewID, view); err != nil {
					return err
				}
			}
		case *v1.ViewResync:
			if view, ok := views[m.ViewID]; ok {
				if err := publish(m.ViewID, view); err != nil {
					return err
				}
			}
		case *v1.ViewClose:
			delete(views, m.ViewID)
		}
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
