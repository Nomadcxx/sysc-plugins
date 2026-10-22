package privacy

import (
	"fmt"
	"strings"
	"testing"
	"time"
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
}

func TestParseAuditOutputRejectsInvalidAndFutureSnapshots(t *testing.T) {
	cases := map[string]string{
		"empty":          "",
		"malformed":      "{",
		"error event":    `{"t":"error","error":"audit failed"}`,
		"future schema":  `{"t":"done","snapshot":{"schema":2,"available":true,"generated_at":"2026-10-20T12:00:00Z"}}`,
		"invalid grade":  `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"perfect"}}}`,
		"trailing event": `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"}}}{"t":"done"}`,
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
