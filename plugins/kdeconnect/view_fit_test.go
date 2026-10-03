package kdeconnect

import (
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// TestPanelStatesFitThePanel lays every panel state out with the host's own
// rules at the manifest's width. The action and recent-image rows are three
// cells that fill a card exactly, so a cell or gap that drifts by a pixel
// makes the host refuse the row and show a failure card instead of the panel.
func TestPanelStatesFitThePanel(t *testing.T) {
	t.Parallel()
	noCard := testSettings()
	noCard.ShowDeviceCard = false

	unpaired := pairedSnap()
	unpaired.Devices[0].Paired = false
	incoming := pairedSnap()
	incoming.Devices[0].PairRequestedByPeer = true
	incoming.Devices[0].VerificationKey = "1a2b3c4d"
	peerRequest := pairedSnap()
	peerRequest.Devices[1].PairRequestedByPeer = true
	orphaned := pairedSnap()
	orphaned.SelectedID = "gone"

	for _, tc := range []struct {
		name     string
		snap     Snapshot
		settings Settings
		composer Composer
		switcher bool
	}{
		{"paired", pairedSnap(), testSettings(), ComposerNone, false},
		{"switcher open", pairedSnap(), testSettings(), ComposerNone, true},
		{"switcher with pairing request", peerRequest, testSettings(), ComposerNone, true},
		{"share composer", pairedSnap(), testSettings(), ComposerShare, false},
		{"sms composer", pairedSnap(), testSettings(), ComposerSMS, false},
		{"no device card", pairedSnap(), noCard, ComposerNone, false},
		{"recent images", recentSnap(), testSettings(), ComposerNone, true},
		{"sftp error", sftpErrorSnap(), testSettings(), ComposerNone, false},
		{"unpaired", unpaired, testSettings(), ComposerNone, false},
		{"incoming pairing", incoming, testSettings(), ComposerNone, false},
		{"orphaned selection", orphaned, testSettings(), ComposerNone, false},
		{"no devices", Snapshot{Available: true}, testSettings(), ComposerNone, false},
		{"daemon unavailable", Snapshot{}, testSettings(), ComposerNone, false},
	} {
		tree := PanelTreeForState(tc.snap, tc.settings, tc.composer, Drafts{}, tc.switcher)
		for _, f := range shelllint.Tree(tree, v1.ViewPanel, PanelWidth, PanelHeight) {
			t.Errorf("%s: %s", tc.name, f)
		}
	}
}
