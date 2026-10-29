// Copyright (C) 2026 Nomadcxx
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/privacy/screenshot.png when CAPTURE=1.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	snapshot := testSnapshot("unknown", now)
	app := &snapshot.Categories[0].Installed[0]
	app.Origins = []AppOrigin{{Source: "flathub", Official: true}, {Source: "community"}}
	app.OriginsShown, app.OriginsTotal = len(app.Origins), len(app.Origins)
	snapshot.Unmatched = []App{{
		Name: "Unknown browser", Grade: "unknown", Origins: []AppOrigin{{Source: "community"}},
		OriginsShown: 1, OriginsTotal: 1,
	}}
	snapshot.UnmatchedShown, snapshot.UnmatchedTotal = 1, 1
	snapshot.Sandbox = &Sandbox{
		Applicable: true, Grade: "unknown", AppsTotal: 1, AppsChecked: 1,
		AppsIncomplete: 1, PartialCoverage: true,
		Apps: []SandboxApp{
			{ID: "com.example.Editor", Scope: "system", Grade: "unknown", Grants: []string{"all devices", "shared network"}, GrantsShown: 2, GrantsTotal: 2,
				Unknown: []string{"unclassified filesystem permission"}, UnknownShown: 1, UnknownTotal: 1},
		},
		AppsShown: 1,
	}
	capture.Panel(t, "privacy", PanelTree(snapshot, now, false, ""))
}
