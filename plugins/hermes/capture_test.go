// Package hermes is the sysc-shell Hermes Agent plugin. During the scaffold
// it holds only the catalog capture; the bridge client, state and views land
// in later tasks.
package hermes

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	capWidth  = 780
	capHeight = 720
	capInset  = 16
	capGap    = 12
	capPad    = 12
	capInner  = capWidth - 2*capInset
)

// fixtureMessage is one transcript line in the capture scene.
type fixtureMessage struct {
	role, text string
}

// fixtureApproval is a tool call waiting for the user.
type fixtureApproval struct {
	tool, command, reason string
}

// fixtureState is the invented bridge state the capture shows: online, idle,
// two messages and one pending approval.
type fixtureState struct {
	label, model, session string
	online, busy          bool
	messages              []fixtureMessage
	approvals             []fixtureApproval
}

func captureFixture() fixtureState {
	return fixtureState{
		label:   "Hermes",
		model:   "example-model",
		session: "Tidy the release notes",
		online:  true,
		messages: []fixtureMessage{
			{role: "You", text: "Tidy the release notes and run the test suite."},
			{role: "Hermes", text: "The notes are tidied. The tests need a shell command; approve it below."},
		},
		approvals: []fixtureApproval{
			{tool: "terminal", command: "go test ./...", reason: "Run the test suite"},
		},
	}
}

// scenePanel draws the fixture as the chat panel will look. It is a
// stand-in until the real panel lands, when this scene is recaptured.
func scenePanel(s fixtureState) *v1.Node {
	status, tone := "Offline", v1.ToneError
	if s.online {
		status, tone = "Online · idle", v1.ToneAccent
		if s.busy {
			status = "Online · working"
		}
	}
	children := []*v1.Node{
		{Kind: v1.KindRow, Width: capInner, Height: 36, Gap: 10, Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "ai-usage", IconSize: 28, Name: "Hermes"},
			{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
				{Kind: v1.KindText, Text: "Hermes Agent", Size: "title", Bold: true},
				{Kind: v1.KindText, Text: s.label + " · " + s.model, Size: "caption", Tone: v1.ToneSubtle},
			}},
			{Kind: v1.KindText, Text: status, Size: "label", Bold: true, Tone: tone, PinEnd: true},
		}},
		{Kind: v1.KindText, Text: s.session, Size: "label", Tone: v1.ToneSubtle, MaxWidth: capInner},
	}
	for _, m := range s.messages {
		fill := "card"
		if m.role == "You" {
			fill = "soft"
		}
		children = append(children, &v1.Node{
			Kind: v1.KindColumn, Fill: fill, Shape: "card", Padding: capPad, Gap: 4, Width: capInner,
			Children: []*v1.Node{
				{Kind: v1.KindText, Text: m.role, Size: "caption", Bold: true, Tone: v1.ToneSubtle},
				{Kind: v1.KindText, Text: m.text, MaxWidth: capInner - 2*capPad},
			},
		})
	}
	for _, a := range s.approvals {
		children = append(children, &v1.Node{
			Kind: v1.KindColumn, Fill: "error-container", Shape: "card", Padding: capPad, Gap: 8, Width: capInner,
			Children: []*v1.Node{
				{Kind: v1.KindText, Text: "Approval needed · " + a.tool, Size: "label", Bold: true},
				{Kind: v1.KindText, Text: a.command, Tabular: true, MaxWidth: capInner - 2*capPad},
				{Kind: v1.KindText, Text: a.reason, Size: "caption", MaxWidth: capInner - 2*capPad},
				{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
					{Kind: v1.KindButton, ID: "approve-once", Text: "Approve once", Name: "Approve this command once", Role: "button", Fill: "accent", Shape: "stadium", Height: 32, Padding: 8, Events: []v1.EventKind{v1.EventActivate}},
					{Kind: v1.KindButton, ID: "approve-session", Text: "Approve for session", Name: "Approve for this session", Role: "button", Fill: "soft", Shape: "stadium", Height: 32, Padding: 8, Events: []v1.EventKind{v1.EventActivate}},
					{Kind: v1.KindButton, ID: "deny", Text: "Deny", Name: "Deny this command", Role: "button", Fill: "surface", Shape: "stadium", Height: 32, Padding: 8, Events: []v1.EventKind{v1.EventActivate}},
				}},
			},
		})
	}
	children = append(children,
		&v1.Node{Kind: v1.KindRow, Gap: 8, Children: []*v1.Node{
			{Kind: v1.KindTextInput, ID: "composer", Key: "composer", Name: "Message Hermes", Role: "textbox", Placeholder: "Message Hermes…", Width: capInner - 108, Height: 40, Padding: 10, SubmitOnEnter: true, Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}},
			{Kind: v1.KindButton, ID: "send", Icon: "send", Text: "Send", Name: "Send message", Role: "button", Fill: "accent", Shape: "stadium", Width: 100, Height: 40, Padding: 8, Events: []v1.EventKind{v1.EventActivate}},
		}},
	)
	return &v1.Node{Kind: v1.KindColumn, Width: capWidth, Height: capHeight, Padding: capInset, Gap: capGap, Children: children}
}

// TestCaptureSceneLaysOut runs without CAPTURE so a broken scene fails the
// normal test run, not only a capture.
func TestCaptureSceneLaysOut(t *testing.T) {
	tree := scenePanel(captureFixture())
	if err := v1.Validate(tree, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	for _, f := range shelllint.Tree(tree, v1.ViewPanel, capWidth, capHeight) {
		t.Error(f)
	}
}

// TestCapturePanel writes plugins/hermes/screenshot.png when CAPTURE=1: an
// online, idle bridge with two messages and one pending approval. Every value
// is invented.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "hermes", scenePanel(captureFixture()))
}
