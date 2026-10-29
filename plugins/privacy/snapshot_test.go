// Copyright (C) 2026 Nomadcxx
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseAuditOutputBoundsListsAndPreservesUnknownTelemetry(t *testing.T) {
	var installed strings.Builder
	for i := 0; i < maxInstalledPerCat+2; i++ {
		if i > 0 {
			installed.WriteByte(',')
		}
		fmt.Fprintf(&installed, `{"id":"app-%d","name":"Application %d","grade":"red"}`, i, i)
	}
	var findings strings.Builder
	for i := 0; i < maxFindings+2; i++ {
		if i > 0 {
			findings.WriteByte(',')
		}
		severity := "low"
		if i == maxFindings+1 {
			severity = "high"
		}
		fmt.Fprintf(&findings, `{"id":"finding-%d","severity":%q,"grade":"red","title":"Finding %d","detail":"Details","actions":[{"tier":0,"kind":"inform","label":"Read"},{"tier":3,"kind":"launch","label":"Steps"},{"tier":3,"kind":"launch","label":"More steps"}]}`, i, severity, i)
	}
	var reduced strings.Builder
	for i := 0; i < maxReduced+3; i++ {
		if i > 0 {
			reduced.WriteByte(',')
		}
		fmt.Fprintf(&reduced, `{"fact":"source-%d","effect":"limited"}`, i)
	}
	input := fmt.Sprintf(`{"t":"done","snapshot":{
		"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","catalog_rev":"r1",
		"overall":{"grade":"unknown","findings":20,"high":3},
		"network":{"vpn":{"state":"unknown"},"telemetry_connections_unknown":false},
		"categories":[{"id":"browser","name":"Browser","grade":"red","installed":[%s],"installed_total":12,"worst":{"id":"chrome","name":"Chrome","grade":"red"}}],
		"findings":[%s],"findings_total":9,"reduced":[%s],"entry_point":"privacybit"
	}}`, installed.String(), findings.String(), reduced.String())

	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Overall.Grade != "unknown" || !snapshot.Network.TelemetryConnectionsUnknown {
		t.Fatalf("risk or telemetry state was lost: %+v", snapshot)
	}
	category := snapshot.Categories[0]
	if category.InstalledShown != maxInstalledPerCat || category.InstalledTotal != 12 || !category.InstalledTruncated {
		t.Fatalf("installed projection = %+v", category)
	}
	if snapshot.FindingsShown != maxFindings || snapshot.FindingsTotal != 9 || !snapshot.FindingsTruncated {
		t.Fatalf("finding projection shown=%d total=%d truncated=%v", snapshot.FindingsShown, snapshot.FindingsTotal, snapshot.FindingsTruncated)
	}
	if snapshot.Findings[0].Severity != "high" {
		t.Fatalf("highest severity finding was not prioritized: %+v", snapshot.Findings[0])
	}
	if snapshot.Findings[0].ActionsShown != maxActionsPerFinding || !snapshot.Findings[0].ActionsTruncated {
		t.Fatalf("action projection = %+v", snapshot.Findings[0])
	}
	if snapshot.ReducedShown != maxReduced || snapshot.ReducedTotal != maxReduced+4 || !snapshot.ReducedTruncated {
		t.Fatalf("reduced projection shown=%d total=%d truncated=%v", snapshot.ReducedShown, snapshot.ReducedTotal, snapshot.ReducedTruncated)
	}
	if snapshot.Reduced[0].Fact != "network.telemetry" {
		t.Fatalf("telemetry caveat was dropped from reduced coverage: %+v", snapshot.Reduced[:2])
	}
	if snapshot.GeneratedAt.Location() != time.UTC {
		t.Fatalf("generated_at location = %v, want UTC", snapshot.GeneratedAt.Location())
	}
	if snapshot.Sandbox != nil {
		t.Fatalf("legacy snapshot without sandbox field got %+v", snapshot.Sandbox)
	}
}

func TestParseAuditOutputProjectsBoundedOrigins(t *testing.T) {
	const maxOrigins = 8
	const maxOriginBytes = 80
	type inputOrigin struct {
		Source   string `json:"source"`
		Official bool   `json:"official"`
	}
	origins := []inputOrigin{
		{Source: "alpha", Official: true},
		{Source: "alpha", Official: true},
		{Source: "alpha", Official: false},
		{Source: "repo\nname"},
	}
	for i := range 8 {
		origins = append(origins, inputOrigin{Source: fmt.Sprintf("repo-%02d", i), Official: true})
	}
	origins = append(origins, inputOrigin{Source: "0" + strings.Repeat("界", 40)})
	encodedOrigins, err := json.Marshal(origins)
	if err != nil {
		t.Fatal(err)
	}
	input := fmt.Sprintf(`{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"green"},"categories":[{"id":"browser","name":"Browser","grade":"green","installed":[{"id":"brave-origin","name":"Brave Origin","grade":"green","origins":%s,"origins_shown":12,"origins_total":12}]}]}}`, encodedOrigins)
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Categories []struct {
			Installed []struct {
				Origins   []inputOrigin `json:"origins"`
				Shown     int           `json:"origins_shown"`
				Total     int           `json:"origins_total"`
				Truncated bool          `json:"origins_truncated"`
			} `json:"installed"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	app := got.Categories[0].Installed[0]
	if len(app.Origins) != maxOrigins || app.Shown != maxOrigins || app.Total != 12 || !app.Truncated {
		t.Fatalf("origin projection = %+v, want %d shown of 12 and truncated", app, maxOrigins)
	}
	if !strings.HasPrefix(app.Origins[0].Source, "0") || !utf8.ValidString(app.Origins[0].Source) || len(app.Origins[0].Source) > maxOriginBytes {
		t.Fatalf("long origin was not safely bounded: %q (%d bytes)", app.Origins[0].Source, len(app.Origins[0].Source))
	}
	foundSanitized := false
	alphaStatuses := map[bool]bool{}
	for _, origin := range app.Origins {
		if origin.Source == "repo name" {
			foundSanitized = true
		}
		if origin.Source == "alpha" {
			alphaStatuses[origin.Official] = true
		}
		if !utf8.ValidString(origin.Source) || len(origin.Source) > maxOriginBytes || strings.ContainsAny(origin.Source, "\n\r\x1b") {
			t.Errorf("unsafe origin source %q (%d bytes)", origin.Source, len(origin.Source))
		}
	}
	if !foundSanitized {
		t.Fatalf("newline in origin source was not sanitized: %+v", app.Origins)
	}
	if !alphaStatuses[true] || !alphaStatuses[false] {
		t.Fatalf("origin status variants collapsed: %+v", app.Origins)
	}
}

func TestParseAuditOutputAcceptsLegacyAppWithoutOriginContext(t *testing.T) {
	input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"green"},"categories":[{"id":"browser","name":"Browser","grade":"green","installed":[{"id":"firefox","name":"Firefox","grade":"green"}]}]}}`
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"origins"`) {
		t.Fatalf("legacy app without provenance gained an origin: %s", encoded)
	}
}

func TestParseAuditOutputBoundsFlatpakSandboxEvidence(t *testing.T) {
	labels := make([]string, maxSandboxEvidence+2)
	for i := range labels {
		labels[i] = fmt.Sprintf("permission-%d", i)
	}
	encodedLabels, err := json.Marshal(labels)
	if err != nil {
		t.Fatal(err)
	}
	grants := append([]string{"host filesystem"}, labels...)
	encodedGrants, err := json.Marshal(grants)
	if err != nil {
		t.Fatal(err)
	}
	apps := make([]string, maxSandboxApps+2)
	for i := range apps {
		apps[i] = fmt.Sprintf(`{"id":"com.example.App%d","scope":"system","grade":"unknown","grants":%s,"unknown":%s}`, i, encodedGrants, encodedLabels)
	}
	input := fmt.Sprintf(`{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"unknown"},"sandbox":{"applicable":true,"grade":"unknown","apps":[%s],"apps_shown":%d,"apps_total":%d,"apps_checked":%d,"apps_incomplete":%d,"partial_coverage":true}}}`,
		strings.Join(apps, ","), len(apps), len(apps), len(apps), len(apps))
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sandbox == nil || snapshot.Sandbox.AppsShown != maxSandboxApps || snapshot.Sandbox.AppsTotal != len(apps) || !snapshot.Sandbox.AppsTruncated {
		t.Fatalf("sandbox app projection = %+v", snapshot.Sandbox)
	}
	app := snapshot.Sandbox.Apps[0]
	if len(app.Grants) != maxSandboxEvidence || !app.GrantsTruncated || len(app.Unknown) != maxSandboxEvidence || !app.UnknownTruncated {
		t.Fatalf("sandbox evidence was not bounded: %+v", app)
	}
}

func TestParseAuditOutputKeepsWorstFlatpakCopyVisible(t *testing.T) {
	input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"red"},"sandbox":{"applicable":true,"grade":"red","apps":[{"id":"com.example.A","scope":"user","grade":"green"},{"id":"com.example.B","scope":"user","grade":"green"},{"id":"com.example.C","scope":"user","grade":"green"},{"id":"com.example.D","scope":"user","grade":"green"},{"id":"com.example.Z","scope":"system","grade":"red","grants":["all devices"]}],"apps_shown":5,"apps_total":5,"apps_checked":5}}}`
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sandbox.AppsShown != maxSandboxApps || !snapshot.Sandbox.AppsTruncated {
		t.Fatalf("sandbox app projection = %+v, want a capped list", snapshot.Sandbox)
	}
	if got := snapshot.Sandbox.Apps[0]; got.ID != "com.example.Z" || got.Grade != "red" {
		t.Fatalf("first visible sandbox copy = %+v, want highest-risk copy first", got)
	}
}

func TestParseAuditOutputRejectsInvalidAndFutureSnapshots(t *testing.T) {
	cases := map[string]string{
		"empty":                                      "",
		"malformed":                                  "{",
		"error event":                                `{"t":"error","error":"audit failed"}`,
		"future schema":                              `{"t":"done","snapshot":{"schema":2,"available":true,"generated_at":"2026-10-20T12:00:00Z"}}`,
		"invalid grade":                              `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"perfect"}}}`,
		"invalid sandbox grade":                      `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"},"sandbox":{"applicable":true,"grade":"perfect"}}}`,
		"invalid sandbox app grade":                  `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"},"sandbox":{"applicable":true,"grade":"unknown","apps":[{"id":"com.example.App","scope":"user","grade":"perfect"}]}}}`,
		"negative app origin count":                  `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"green"},"categories":[{"id":"browser","name":"Browser","grade":"green","installed":[{"id":"firefox","name":"Firefox","grade":"green","origins_total":-1}]}]}}`,
		"negative sandbox count":                     `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"},"sandbox":{"applicable":true,"grade":"unknown","apps_total":-1}}}`,
		"green sandbox with incomplete coverage":     `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"green"},"sandbox":{"applicable":true,"grade":"green","apps":[{"id":"com.example.App","scope":"user","grade":"green"}],"apps_shown":1,"apps_total":2,"apps_truncated":true,"apps_checked":1,"apps_incomplete":1}}}`,
		"red sandbox with incomplete coverage":       `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"red"},"sandbox":{"applicable":true,"grade":"red","apps":[{"id":"com.example.App","scope":"user","grade":"red","grants":["host filesystem"]}],"apps_shown":1,"apps_total":2,"apps_truncated":true,"apps_checked":1,"apps_incomplete":1,"partial_coverage":true}}}`,
		"known app grade with unclassified evidence": `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"},"sandbox":{"applicable":true,"grade":"red","apps":[{"id":"com.example.App","scope":"user","grade":"red","grants":["host filesystem"],"unknown":["unclassified permission"]}],"apps_shown":1,"apps_total":1,"apps_checked":1,"apps_incomplete":1,"partial_coverage":true}}}`,
		"overall hides sandbox grade":                `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"green"},"sandbox":{"applicable":true,"grade":"red","apps":[{"id":"com.example.App","scope":"user","grade":"red","grants":["host filesystem"]}],"apps_shown":1,"apps_total":1,"apps_checked":1}}}`,
		"invalid hidden sandbox app":                 `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"green"},"sandbox":{"applicable":true,"grade":"green","apps":[{"id":"com.example.A","scope":"user","grade":"green"},{"id":"com.example.B","scope":"user","grade":"green"},{"id":"com.example.C","scope":"user","grade":"green"},{"id":"com.example.D","scope":"user","grade":"green"},{"id":"com.example.Z","scope":"user","grade":"perfect"}],"apps_shown":5,"apps_total":5,"apps_checked":4}}}`,
		"trailing event":                             `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"}}}{"t":"done"}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAuditOutput([]byte(input)); err == nil {
				t.Fatal("invalid privacybit output was accepted")
			}
		})
	}
	if _, err := ParseAuditOutput(make([]byte, MaxAuditOutputBytes+1)); err == nil {
		t.Fatal("oversized privacybit output was accepted")
	}
}

func TestParseAuditOutputMapsUngradedFlatpakScopeFindingToUnknown(t *testing.T) {
	input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"},"findings":[{"id":"flatpak.scope.freetube","severity":"low","title":"Multiple scopes","detail":"Installed in user and system scopes"}]}}`
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Findings) != 1 || snapshot.Findings[0].Grade != "unknown" {
		t.Fatalf("scope finding = %+v, want one finding with unknown grade", snapshot.Findings)
	}
}

func TestParseAuditOutputRejectsOtherUngradedFindings(t *testing.T) {
	for _, finding := range []string{
		`{"id":"flatpak.scope.freetube","severity":"medium","title":"Multiple scopes"}`,
		`{"id":"other.finding","severity":"low","title":"Ungraded"}`,
	} {
		input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"},"findings":[` + finding + `]}}`
		if _, err := ParseAuditOutput([]byte(input)); err == nil {
			t.Fatalf("accepted unsupported ungraded finding %s", finding)
		}
	}
}

func TestUnavailableSnapshotIsExplicit(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	snapshot := UnavailableSnapshot("privacybit was not found", at)
	if snapshot.Available || snapshot.Reason == "" || snapshot.Hint == "" || snapshot.Overall.Grade != "unknown" || !snapshot.Network.TelemetryConnectionsUnknown {
		t.Fatalf("unavailable snapshot is not explicit: %+v", snapshot)
	}
	if snapshot.Network.VPN.Defects == nil {
		t.Fatal("unavailable snapshot must encode VPN defects as an empty list")
	}
}
