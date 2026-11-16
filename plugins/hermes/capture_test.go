package hermes

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	capWidth  = 780
	capHeight = 720
)

// captureFixture is the invented bridge state the capture shows: an online,
// idle Hermes on a local gateway, a short conversation, two background
// actions and one pending approval. Every value is fictional.
func captureFixture(avatar string) PanelState {
	return PanelState{
		Summary: SummaryState{
			BarState:        BarState{Status: StatusFor(StatusIdle), GatewayStatus: "running", Model: "example-model"},
			Provider:        "openrouter",
			ModelName:       "example-model",
			ToolEvents:      2,
			ApprovalPending: true,
			ApprovalMessage: "Run go test ./... in the repository",
		},
		ApprovalTool:     "terminal",
		SessionID:        "20261111_115900_abcd",
		LastTool:         "terminal",
		ShowToolActivity: true,
		Messages: []Message{
			{ID: "m1", Role: "user", Text: "Tidy the release notes and run the test suite."},
			{ID: "m2", Role: "assistant", Text: "The notes are tidied: three duplicate entries merged and the headings made consistent.\nThe tests need a shell command, so approve it below."},
			{ID: "m3", Role: "user", Text: "Go ahead once it is approved."},
		},
		Events: []ToolEvent{
			{ID: "e1", Type: "tool.complete", Name: "read_file", Text: "RELEASE_NOTES.md"},
			{ID: "e2", Type: "tool.complete", Name: "write_file", Text: "RELEASE_NOTES.md"},
		},
		Sessions: SessionsSnapshot{Loaded: true},
		Avatar:   avatar,
		Now:      time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC),
	}
}

// TestCaptureSceneLaysOut runs without CAPTURE so a broken scene fails the
// normal test run, not only a capture.
func TestCaptureSceneLaysOut(t *testing.T) {
	tree := PanelTree(captureFixture(repoAvatar(t)), capWidth, capHeight)
	if err := v1.Validate(tree, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	for _, f := range shelllint.Tree(tree, v1.ViewPanel, capWidth, capHeight) {
		t.Error(f)
	}
}

// TestCapturePanel writes plugins/hermes/screenshot.png when CAPTURE=1: the
// real chat panel over the fixture above.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "hermes", PanelTree(captureFixture(repoAvatar(t)), capWidth, capHeight))
}
