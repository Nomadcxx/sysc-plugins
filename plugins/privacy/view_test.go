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
	root := BarTree(testSnapshot("amber", now), now, lint.BarWidth)
	if err := v1.Validate(root, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	button := findNode(root, "privacy-open")
	if button == nil || button.Icon != "shield" || button.Text != "Review" || button.Name == "" {
		t.Fatalf("bar button = %+v", button)
	}
	if len(button.Events) != 1 || button.Events[0] != v1.EventActivate {
		t.Fatalf("bar events = %v, want activate", button.Events)
	}
	if findings := lint.Tree(root, v1.ViewBar, lint.BarWidth, lint.BarHeight); len(findings) != 0 {
		t.Fatalf("bar does not fit: %+v", findings)
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
		{"amber", "Review", "surface", 1, "flag"},
		{"red", "Risky", "error-container", 0, "gpp_bad"},
		{"unknown", "Unrated", "soft", 0, "disabled_by_default"},
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
