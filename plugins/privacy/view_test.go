// Copyright (C) 2026 Nomadcxx
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestPrivacyBarOpensPanelAndShowsCurrentGrade(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	root := BarTree(testSnapshot("amber", now), now)
	if err := v1.Validate(root, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	button := findNode(root, "privacy-open")
	if button == nil || button.Icon != "gpp_maybe" || button.Tone != v1.ToneWarning || !strings.HasSuffix(button.Name, "Review") {
		t.Fatalf("bar button = %+v", button)
	}
	if len(button.Events) != 1 || button.Events[0] != v1.EventActivate {
		t.Fatalf("bar events = %v, want activate", button.Events)
	}
	if findings := lint.Tree(root, v1.ViewBar, lint.BarWidth, lint.BarHeight); len(findings) != 0 {
		t.Fatalf("bar does not fit: %+v", findings)
	}
}

func TestPrivacyPanelUsesSingularForOneHighPriorityFinding(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	root := PanelTree(testSnapshot("red", now), now, false, "")

	if !treeHasText(findNodeByKey(root, "privacy-summary"), "1 finding · 1 high priority") {
		t.Fatal("summary did not use the singular label for one high-priority finding")
	}
}

func TestPrivacyPanelUsesDistinctGradeFillsAndExplainsCoverage(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		grade  string
		word   string
		fill   string
		stroke int
		icon   string
	}{
		{"green", "Good", "accent", 0, "verified_user"},
		{"amber", "Review", "surface", 1, "gpp_maybe"},
		{"red", "Risky", "error-container", 0, "remove_moderator"},
		{"unknown", "Unrated", "soft", 0, "shield"},
	} {
		t.Run(tc.grade, func(t *testing.T) {
			root := PanelTree(testSnapshot(tc.grade, now), now, false, "")
			if err := v1.Validate(root, v1.ViewPanel); err != nil {
				t.Fatal(err)
			}
			chip := findNodeByKey(root, "privacy-grade")
			if chip == nil || chip.Fill != tc.fill || chip.Stroke != tc.stroke || !treeHasText(chip, tc.word) {
				t.Fatalf("grade chip = %+v, want %q with fill %q and stroke %d", chip, tc.word, tc.fill, tc.stroke)
			}
			if len(chip.Children) == 0 || chip.Children[0].Kind != v1.KindIcon || chip.Children[0].Icon != tc.icon {
				t.Errorf("grade icon = %+v, want %q", chip.Children, tc.icon)
			}
			for _, want := range []string{"privacybit", "VPN", "Browser", "Risky", "Live endpoint attribution is unavailable"} {
				if !treeHasText(root, want) {
					t.Errorf("panel is missing %q", want)
				}
			}
			button := findNode(root, "privacy-scan")
			if button == nil {
				t.Fatal("panel has no Scan now action")
			}
			if button.Icon != "restart_alt" {
				t.Errorf("scan icon = %q, want bundled restart_alt", button.Icon)
			}
			if findings := lint.Tree(root, v1.ViewPanel, panelWidth, panelHeight); len(findings) != 0 {
				t.Fatalf("panel does not fit: %+v", findings)
			}
		})
	}

	root := PanelTree(testSnapshot("green", now), now, false, "")
	if root.Kind != v1.KindList || root.Height != panelHeight {
		t.Fatalf("panel root = kind %q height %d, want scroll list at %d", root.Kind, root.Height, panelHeight)
	}
	if len(root.Events) != 1 || root.Events[0] != v1.EventScroll {
		t.Fatalf("panel scroll events = %v, want scroll", root.Events)
	}
}

func TestPrivacyPanelShowsSourcesBehindReducedCoverage(t *testing.T) {
	input := auditOutputWithReduced(t, []any{map[string]any{
		"fact": "licence", "effect": "not supported by the listed sources",
		"sources": []string{"desktop", "dpkg"},
	}})
	snapshot, err := ParseAuditOutput(input)
	if err != nil {
		t.Fatal(err)
	}
	root := PanelTree(snapshot, time.Date(2026, 10, 30, 0, 1, 0, 0, time.UTC), false, "")
	if !treeHasText(root, "licence (desktop, dpkg) · not supported by the listed sources") {
		t.Fatal("coverage card did not identify sources missing the licence fact")
	}
	if findings := lint.Tree(root, v1.ViewPanel, panelWidth, panelHeight); len(findings) != 0 {
		t.Fatalf("coverage panel does not fit: %+v", findings)
	}
}

func TestPrivacyPanelKeepsUnavailableStaleAndPartialStatesUnrated(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		snapshot Snapshot
		wantText string
	}{
		{name: "unavailable", snapshot: UnavailableSnapshot("privacybit is missing", now), wantText: "privacybit is missing"},
		{name: "stale", snapshot: testSnapshot("green", now.Add(-16*time.Minute)), wantText: "stale"},
		{name: "future", snapshot: testSnapshot("red", now.Add(time.Minute)), wantText: "future"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := PanelTree(tc.snapshot, now, false, "")
			chip := findNodeByKey(root, "privacy-grade")
			if chip == nil || chip.Fill != "soft" || !treeHasText(chip, "Unrated") {
				t.Fatalf("not-current result was presented as rated: %+v", chip)
			}
			if !treeHasText(root, tc.wantText) {
				t.Fatalf("panel omitted %q", tc.wantText)
			}
			if tc.name != "unavailable" && treeHasText(root, "Browser") {
				t.Fatal("stale/future category detail was shown as current")
			}
		})
	}

	partial := testSnapshot("green", now)
	partial.CategoriesTruncated = true
	partial.CategoriesTotal = partial.CategoriesShown + 2
	partial.FindingsTruncated = true
	partial.FindingsTotal = partial.FindingsShown + 3
	partial.ReducedTruncated = true
	partial.ReducedTotal = partial.ReducedShown + 1
	root := PanelTree(partial, now, false, "")
	for _, want := range []string{"Showing", "categories", "findings", "coverage notes"} {
		if !treeHasText(root, want) {
			t.Errorf("partial snapshot omitted %q", want)
		}
	}
}

func TestPrivacyPanelStaleRefreshDoesNotAskForAnotherScan(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	snapshot := testSnapshot("red", now.Add(-16*time.Minute))
	root := PanelTree(snapshot, now, true, "Checking installed apps")

	if !treeHasText(root, "A scan is in progress. The panel will update when it finishes.") {
		t.Fatal("stale panel did not explain that a refresh is already running")
	}
	if treeHasText(root, "Scan again to see current results.") {
		t.Fatal("stale panel asked for another scan while one is running")
	}
	button := findNode(root, "privacy-scan")
	if button == nil || !button.Disabled || button.Text != "Scanning…" {
		t.Fatalf("stale refresh button = %+v, want disabled Scanning…", button)
	}
	if !treeHasText(root, "Previous grade: Risky") {
		t.Fatal("stale panel omitted the previous grade while refreshing")
	}
}

func TestPrivacyPanelScanButtonIsDisabledWhileScanning(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	root := PanelTree(testSnapshot("green", now), now, true, "Checking installed apps")
	button := findNode(root, "privacy-scan")
	if button == nil || !button.Disabled || button.Text != "Scanning…" {
		t.Fatalf("scan button = %+v, want disabled Scanning…", button)
	}
	if !treeHasText(root, "Checking installed apps") {
		t.Fatal("scan error/status was not shown")
	}
}

func TestPrivacyPanelShowsPendingStateForFirstScan(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	root := PanelTree(Snapshot{Schema: Schema}, now, true, "Checking installed apps")
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if findings := lint.Tree(root, v1.ViewPanel, panelWidth, panelHeight); len(findings) != 0 {
		t.Fatalf("pending panel does not fit: %+v", findings)
	}
	if !treeHasText(root, "First audit in progress") {
		t.Fatal("panel omitted the first-scan pending state")
	}
	if treeHasText(root, "Audit unavailable") {
		t.Fatal("panel presented its zero-value initial snapshot as an audit failure")
	}
	button := findNode(root, "privacy-scan")
	if button == nil || !button.Disabled || button.Text != "Scanning…" {
		t.Fatalf("first-scan button = %+v, want disabled Scanning…", button)
	}
}

func TestPrivacyPanelFilledSurfacesUseTheirPairedForeground(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	snapshot := testSnapshot("unknown", now)
	snapshot.Sandbox = &Sandbox{
		Applicable: true, Grade: "unknown", AppsTotal: 1, AppsChecked: 1,
		AppsIncomplete: 1, PartialCoverage: true,
		Apps: []SandboxApp{{
			ID: "com.example.Editor", Scope: "system", Grade: "unknown",
			Grants: []string{"all devices"}, Unknown: []string{"unclassified filesystem permission"},
		}},
		AppsShown: 1,
	}
	root := PanelTree(snapshot, now, false, "")

	var check func(*v1.Node, bool)
	check = func(node *v1.Node, filled bool) {
		if node == nil {
			return
		}
		filled = filled || node.Fill == "container" || node.Fill == "soft"
		if filled && node.Kind == v1.KindText && node.Tone == v1.ToneSubtle {
			t.Errorf("subtle text %q overrides the foreground paired with its filled surface", node.Text)
		}
		for _, child := range node.Children {
			check(child, filled)
		}
	}
	check(root, false)
}

func TestPrivacyPanelKeepsWorstAppAndSuggestionCountsWhenListsAreTruncated(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	snapshot := testSnapshot("red", now)
	snapshot.Categories[0].Installed = snapshot.Categories[0].Installed[:1]
	snapshot.Categories[0].InstalledShown = 1
	snapshot.Categories[0].InstalledTotal = 8
	snapshot.Categories[0].InstalledTruncated = true
	snapshot.Categories[0].Worst = &App{ID: "firefox-nightly", Name: "Firefox Nightly", Grade: "red", Scope: "system"}
	snapshot.Findings[0].ActionsShown = 1
	snapshot.Findings[0].ActionsTotal = 4
	snapshot.Findings[0].ActionsTruncated = true

	root := PanelTree(snapshot, now, false, "")
	for _, want := range []string{"Highest risk app", "Firefox Nightly", "Showing 1 of 4 suggestions."} {
		if !treeHasText(root, want) {
			t.Errorf("panel omitted %q", want)
		}
	}
}

func TestPrivacyPanelShowsSeparateFlatpakSandboxGradesAndCoverage(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"unknown"},"sandbox":{"applicable":true,"grade":"unknown","apps":[{"id":"com.example.App","scope":"system","grade":"red","grants":["all devices"],"unknown":[]}],"apps_shown":1,"apps_total":2,"apps_truncated":true,"apps_checked":1,"apps_incomplete":1,"partial_coverage":true,"reason":"one permission query failed"}}}`
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	root := PanelTree(snapshot, now, false, "")
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Flatpak sandbox", "Risky", "com.example.App · system", "all devices", "Partial coverage", "1 of 2", "one permission query failed"} {
		if !treeHasText(root, want) {
			t.Errorf("panel omitted %q", want)
		}
	}
	if findNodeByKey(root, "privacy-sandbox") == nil {
		t.Fatal("panel has no dedicated sandbox section")
	}
	if grade := findNodeByKey(root, "grade-sandbox"); grade == nil || !treeHasText(grade, "Unrated") {
		t.Fatalf("partial sandbox grade = %+v, want Unrated", grade)
	}
}

func TestPrivacyPanelShowsOriginAsContext(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"green"},"categories":[{"id":"browser","name":"Browser","grade":"green","installed":[{"id":"brave-origin","name":"Brave Origin","grade":"green","origins":[{"source":"flathub","official":true},{"source":"aur","official":false}],"origins_shown":2,"origins_total":2}]}]}}`
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	root := PanelTree(snapshot, now, false, "")
	for _, want := range []string{"Origin context", "flathub (recognized official label; configured source not verified)", "aur (not marked official)"} {
		if !treeHasText(root, want) {
			t.Errorf("panel omitted %q", want)
		}
	}
	if got := snapshot.Categories[0].Installed[0].Grade; got != "green" {
		t.Fatalf("origin context changed app grade to %q", got)
	}
}

func TestPrivacyPanelShowsOriginForWorstAppOutsideTheVisibleList(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	snapshot := testSnapshot("green", now)
	snapshot.Categories[0].Installed = []App{{ID: "shown", Name: "Shown app", Grade: "green"}}
	snapshot.Categories[0].InstalledShown = 1
	snapshot.Categories[0].InstalledTotal = 5
	snapshot.Categories[0].InstalledTruncated = true
	snapshot.Categories[0].Worst = &App{
		ID: "hidden", Name: "Highest risk app", Grade: "red",
		Origins: []AppOrigin{{Source: "community", Official: false}}, OriginsShown: 1, OriginsTotal: 1,
	}
	root := PanelTree(snapshot, now, false, "")
	if !treeHasText(root, "Origin context: community (not marked official)") {
		t.Fatal("panel omitted origin context for its separately displayed highest-risk app")
	}
}

func TestPrivacyPanelShowsUnmatchedOriginContext(t *testing.T) {
	input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"green"},"unmatched":[{"name":"Mystery browser","grade":"unknown","origins":[{"source":"community","official":false}],"origins_shown":1,"origins_total":1}],"unmatched_total":1}}`
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	root := PanelTree(snapshot, time.Date(2026, 10, 23, 10, 0, 1, 0, time.UTC), false, "")
	for _, want := range []string{"Unmatched apps", "Mystery browser", "Unrated", "Origin context: community (not marked official)"} {
		if !treeHasText(root, want) {
			t.Errorf("privacy panel omitted %q", want)
		}
	}
}

func TestPrivacyPanelExplainsWhenNoFlatpakAppsAreInstalled(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	input := `{"t":"done","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-23T10:00:00Z","overall":{"grade":"green"},"sandbox":{"applicable":false,"grade":"unknown","apps":[],"apps_shown":0,"apps_total":0,"reason":"No Flatpak applications are installed."}}}`
	snapshot, err := ParseAuditOutput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	root := PanelTree(snapshot, now, false, "")
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Flatpak sandbox", "Unrated", "No Flatpak applications are installed."} {
		if !treeHasText(root, want) {
			t.Errorf("panel omitted %q", want)
		}
	}
}

func testSnapshot(grade string, generated time.Time) Snapshot {
	trueValue := true
	falseValue := false
	worst := App{ID: "chrome", Name: "Chrome", Grade: "red", Scope: "user"}
	return Snapshot{
		Schema: Schema, Available: true, GeneratedAt: generated,
		Overall: Overall{Grade: grade, Findings: 1, High: 1},
		Network: Network{VPN: VPN{State: "connected", Client: "Mullvad",
			DefaultRouteViaTunnel: &trueValue, DNSContained: &falseValue, IPv6Contained: &trueValue,
			Defects:      []Defect{{ID: "vpn.dns", Title: "DNS outside tunnel", Detail: "The resolver is outside the tunnel."}},
			DefectsShown: 1, DefectsTotal: 1}, TelemetryConnectionsUnknown: true},
		Categories: []Category{{ID: "browser", Name: "Browser", Grade: "red",
			Installed: []App{worst}, InstalledShown: 1, InstalledTotal: 1, Worst: &worst}},
		CategoriesShown: 1, CategoriesTotal: 1,
		Findings: []Finding{{ID: "vpn.dns", Severity: "high", Grade: "red", Category: "network",
			Title: "DNS queries are outside the tunnel", Summary: "Resolver 192.0.2.1 is outside Mullvad.",
			Actions: []Action{{Tier: 3, Kind: "launch", Label: "Review VPN settings"}}, ActionsShown: 1, ActionsTotal: 1}},
		FindingsShown: 1, FindingsTotal: 1,
		Reduced:      []Reduced{{Fact: "network.telemetry", Effect: "Live endpoint attribution is unavailable."}},
		ReducedShown: 1, ReducedTotal: 1,
	}
}

func findNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

func findNodeByKey(root *v1.Node, key string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Key == key {
		return root
	}
	for _, child := range root.Children {
		if found := findNodeByKey(child, key); found != nil {
			return found
		}
	}
	return nil
}

func treeHasText(root *v1.Node, want string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, want) {
		return true
	}
	for _, child := range root.Children {
		if treeHasText(child, want) {
			return true
		}
	}
	return false
}

// The bar pill is the widget's chrome. The shield carries the grade by shape
// and tone alone, and the word survives in the accessible name and tooltip.
func TestPrivacyBarIsABareShieldPerGrade(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	icons := map[string]bool{}
	for _, tc := range []struct {
		grade string
		word  string
		icon  string
		tone  v1.Tone
	}{
		{"green", "Good", "verified_user", v1.ToneAccent},
		{"amber", "Review", "gpp_maybe", v1.ToneWarning},
		{"red", "Risky", "remove_moderator", v1.ToneError},
		{"unknown", "Unrated", "shield", v1.ToneSubtle},
	} {
		t.Run(tc.grade, func(t *testing.T) {
			root := BarTree(testSnapshot(tc.grade, now), now)
			if err := v1.Validate(root, v1.ViewBar); err != nil {
				t.Fatal(err)
			}
			button := findNode(root, "privacy-open")
			if button == nil || button.Icon != tc.icon || button.Tone != tc.tone {
				t.Fatalf("bar button = %+v, want %q in tone %q", button, tc.icon, tc.tone)
			}
			if button.Text != "" || button.Fill != "" || button.Stroke != 0 || button.StrokeFill != "" {
				t.Errorf("bar button carries chrome or a label: text %q fill %q stroke %d/%q", button.Text, button.Fill, button.Stroke, button.StrokeFill)
			}
			if !strings.HasSuffix(button.Name, tc.word) || !strings.Contains(root.Tooltip, "Overall: "+tc.word) {
				t.Errorf("grade word missing: name %q tooltip %q", button.Name, root.Tooltip)
			}
			if findings := lint.Tree(root, v1.ViewBar, lint.BarWidth, lint.BarHeight); len(findings) != 0 {
				t.Fatalf("bar does not fit: %+v", findings)
			}
		})
		icons[tc.icon] = true
	}
	if len(icons) != 4 {
		t.Fatalf("grades share a bar glyph: %v", icons)
	}
	if icons["gpp_bad"] {
		t.Fatal("the bar borrows gpp_bad, the shell's monitoring warning")
	}
}
