// Copyright (C) 2026 Nomadcxx
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	panelWidth  = 620
	panelHeight = 720
	staleAfter  = 15 * time.Minute
)

type gradeStyle struct {
	word       string
	icon       string
	fill       string
	stroke     int
	strokeFill string
}

func BarTree(snapshot Snapshot, now time.Time, width int) *v1.Node {
	grade := currentGrade(snapshot, now)
	style := styleForGrade(grade)
	label := style.word
	if barwidth.Compact(width) {
		label = ""
	}
	return &v1.Node{Kind: v1.KindRow, Gap: 6,
		Tooltip: "privacybit · Overall: " + style.word + " · " + scanAge(snapshot.GeneratedAt, now),
		Children: []*v1.Node{{
			Kind: v1.KindButton, ID: "privacy-open", Icon: "shield", Text: label,
			Name: "Open privacybit audit, " + style.word, Role: "button", Fill: style.fill,
			Stroke: style.stroke, StrokeFill: style.strokeFill,
			Events: []v1.EventKind{v1.EventActivate},
		}}}
}

func TooltipTree(snapshot Snapshot, now time.Time) *v1.Node {
	style := styleForGrade(currentGrade(snapshot, now))
	children := []*v1.Node{
		{Kind: v1.KindText, Text: "privacybit", Size: "title", Bold: true},
		{Kind: v1.KindText, Text: "Overall · " + style.word, Size: "label"},
		{Kind: v1.KindText, Text: scanAge(snapshot.GeneratedAt, now), Size: "caption", Tone: v1.ToneSubtle},
	}
	if !snapshot.Available && snapshot.Reason != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: text(snapshot.Reason, maxSummaryBytes), Size: "caption", Tone: v1.ToneSubtle})
	} else {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "Live endpoint attribution is unavailable.", Size: "caption", Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Gap: 5, Children: children}
}

// PanelTree renders the validated, bounded publisher snapshot. scanMessage is
// short status text owned by the command loop and empty when no scan is active.
func PanelTree(snapshot Snapshot, now time.Time, scanning bool, scanMessage string) *v1.Node {
	grade := currentGrade(snapshot, now)
	root := &v1.Node{Kind: v1.KindList, Height: panelHeight, Gap: 12, Padding: 16,
		Events: []v1.EventKind{v1.EventScroll}, Children: []*v1.Node{
			{Kind: v1.KindRow, Gap: 10, Height: 40, Children: []*v1.Node{
				{Kind: v1.KindIcon, Icon: "shield", IconSize: 28},
				{Kind: v1.KindColumn, Gap: 1, Children: []*v1.Node{
					{Kind: v1.KindText, Text: "privacybit", Size: "title", Bold: true},
					{Kind: v1.KindText, Text: "Desktop privacy audit", Size: "caption", Tone: v1.ToneSubtle},
				}},
			}},
			panelSummary(snapshot, now, grade, scanning, scanMessage),
		}}

	if !snapshot.Available {
		root.Children = append(root.Children, unavailableCard(snapshot))
		return root
	}
	if !snapshotCurrent(snapshot, now) {
		root.Children = append(root.Children, staleCard(snapshot, now))
		return root
	}
	root.Children = append(root.Children,
		categorySection(snapshot),
		networkSection(snapshot.Network),
		findingSection(snapshot),
		coverageSection(snapshot),
	)
	return root
}

func panelSummary(snapshot Snapshot, now time.Time, grade string, scanning bool, scanMessage string) *v1.Node {
	style := styleForGrade(grade)
	rows := []*v1.Node{
		{Kind: v1.KindRow, Height: 32, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Overall assessment", Size: "label", Bold: true},
			gradeChip(grade, "privacy-grade"),
		}},
		{Kind: v1.KindText, Text: scanAge(snapshot.GeneratedAt, now), Size: "caption", Tone: v1.ToneSubtle},
	}
	if snapshot.Available && snapshotCurrent(snapshot, now) {
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: findingCount(snapshot), Size: "caption", Tone: v1.ToneSubtle})
	}
	label := "Scan now"
	if scanning {
		label = "Scanning…"
	}
	rows = append(rows, &v1.Node{Kind: v1.KindButton, ID: "privacy-scan", Icon: "restart_alt", Text: label,
		Name: label + " with privacybit", Role: "button", Fill: "card", Disabled: scanning,
		Events: []v1.EventKind{v1.EventActivate},
	})
	if scanMessage != "" {
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: text(scanMessage, maxSummaryBytes), Size: "caption", Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Key: "privacy-summary", Gap: 8, Fill: "container", Shape: "large", Padding: 14, Children: rows,
		Tooltip: "Overall: " + style.word}
}

func unavailableCard(snapshot Snapshot) *v1.Node {
	children := []*v1.Node{
		sectionTitle("Audit unavailable"),
		{Kind: v1.KindText, Text: text(snapshot.Reason, maxSummaryBytes), Tone: v1.ToneSubtle},
	}
	if snapshot.Hint != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: text(snapshot.Hint, maxSummaryBytes), Size: "caption", Tone: v1.ToneSubtle})
	}
	return card("privacy-unavailable", children...)
}

func staleCard(snapshot Snapshot, now time.Time) *v1.Node {
	message := "The last scan timestamp is in the future. Check the system clock and scan again."
	if snapshot.GeneratedAt.IsZero() {
		message = "No completed scan has a usable timestamp. Scan again to see current results."
	} else if now.Sub(snapshot.GeneratedAt) >= staleAfter {
		message = "The last scan is stale. Scan again to see current results."
	}
	return card("privacy-stale",
		sectionTitle("Current results unavailable"),
		&v1.Node{Kind: v1.KindText, Text: message, Tone: v1.ToneSubtle},
		&v1.Node{Kind: v1.KindText, Text: "Previous grade: " + styleForGrade(snapshot.Overall.Grade).word, Size: "caption", Tone: v1.ToneSubtle},
	)
}

func categorySection(snapshot Snapshot) *v1.Node {
	children := []*v1.Node{sectionTitle("App categories")}
	if len(snapshot.Categories) == 0 {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "No category result is available.", Tone: v1.ToneSubtle})
	}
	for categoryIndex, category := range snapshot.Categories {
		rows := []*v1.Node{{Kind: v1.KindRow, Height: 30, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: text(category.Name, maxLabelBytes), Size: "label", Bold: true},
			gradeChip(category.Grade, fmt.Sprintf("grade-category-%d", categoryIndex)),
		}}}
		rows = append(rows, &v1.Node{Kind: v1.KindText, Text: shownCount(category.InstalledShown, category.InstalledTotal, "app"), Size: "caption", Tone: v1.ToneSubtle})
		for appIndex, app := range category.Installed {
			appName := text(app.Name, maxLabelBytes)
			if app.Scope != "" {
				appName += " · " + text(app.Scope, maxLabelBytes)
			}
			rows = append(rows, &v1.Node{Kind: v1.KindRow, Gap: 8, PinEnd: true, Children: []*v1.Node{
				{Kind: v1.KindText, Text: appName, MaxWidth: 400},
				gradeChip(app.Grade, fmt.Sprintf("grade-app-%d-%d", categoryIndex, appIndex)),
			}})
		}
		if category.InstalledTruncated {
			rows = append(rows, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("Showing %d of %d apps.", category.InstalledShown, category.InstalledTotal), Size: "caption", Tone: v1.ToneSubtle})
		}
		children = append(children, card(fmt.Sprintf("category-%d", categoryIndex), rows...))
	}
	if snapshot.CategoriesTruncated {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("Showing %d of %d categories.", snapshot.CategoriesShown, snapshot.CategoriesTotal), Size: "caption", Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Key: "privacy-categories", Gap: 8, Children: children}
}

func networkSection(network Network) *v1.Node {
	vpn := network.VPN
	children := []*v1.Node{sectionTitle("Network protection")}
	state := humanize(vpn.State)
	if state == "" || strings.EqualFold(state, "unknown") {
		state = "Unknown"
	}
	children = append(children,
		valueRow("VPN", state),
		valueRow("Default route", containment(vpn.DefaultRouteViaTunnel)),
		valueRow("DNS", containment(vpn.DNSContained)),
		valueRow("IPv6", containment(vpn.IPv6Contained)),
	)
	if vpn.Client != "" {
		children = append(children, valueRow("Client", text(vpn.Client, maxLabelBytes)))
	}
	if vpn.KillSwitchNote != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: text(vpn.KillSwitchNote, maxSummaryBytes), Size: "caption", Tone: v1.ToneSubtle})
	}
	for _, defect := range vpn.Defects {
		children = append(children, &v1.Node{Kind: v1.KindColumn, Gap: 3, Fill: "soft", Shape: "medium", Padding: 8, Children: []*v1.Node{
			{Kind: v1.KindText, Text: text(defect.Title, maxLabelBytes), Bold: true},
			{Kind: v1.KindText, Text: text(defect.Detail, maxSummaryBytes), Size: "caption", Tone: v1.ToneSubtle},
		}})
	}
	if vpn.DefectsTruncated {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("Showing %d of %d VPN findings.", vpn.DefectsShown, vpn.DefectsTotal), Size: "caption", Tone: v1.ToneSubtle})
	}
	return card("privacy-network", children...)
}

func findingSection(snapshot Snapshot) *v1.Node {
	children := []*v1.Node{sectionTitle("Findings")}
	if len(snapshot.Findings) == 0 {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "No findings were returned by the checks that ran.", Tone: v1.ToneSubtle})
	}
	for findingIndex, finding := range snapshot.Findings {
		rows := []*v1.Node{
			{Kind: v1.KindRow, Height: 30, PinEnd: true, Children: []*v1.Node{
				{Kind: v1.KindText, Text: text(finding.Title, maxLabelBytes), Bold: true},
				gradeChip(finding.Grade, fmt.Sprintf("grade-finding-%d", findingIndex)),
			}},
			{Kind: v1.KindText, Text: text(finding.Category+" · "+finding.Severity, maxLabelBytes), Size: "caption", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: text(finding.Summary, maxSummaryBytes), Tone: v1.ToneSubtle},
		}
		for _, action := range finding.Actions {
			rows = append(rows, &v1.Node{Kind: v1.KindText, Text: "Suggested: " + text(action.Label, maxLabelBytes), Size: "caption", Tone: v1.ToneSubtle})
		}
		if finding.ActionsTruncated {
			rows = append(rows, &v1.Node{Kind: v1.KindText, Text: "Some suggestions are omitted.", Size: "caption", Tone: v1.ToneSubtle})
		}
		children = append(children, card(fmt.Sprintf("finding-%d", findingIndex), rows...))
	}
	if snapshot.FindingsTruncated {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("Showing %d of %d findings.", snapshot.FindingsShown, snapshot.FindingsTotal), Size: "caption", Tone: v1.ToneSubtle})
	}
	return &v1.Node{Kind: v1.KindColumn, Key: "privacy-findings", Gap: 8, Children: children}
}

func coverageSection(snapshot Snapshot) *v1.Node {
	children := []*v1.Node{
		sectionTitle("Coverage limits"),
		{Kind: v1.KindText, Text: "Live endpoint attribution is unavailable.", Tone: v1.ToneSubtle},
	}
	for _, item := range snapshot.Reduced {
		if item.Fact == "network.telemetry" {
			continue
		}
		label := text(item.Fact, maxLabelBytes)
		if item.Effect != "" {
			label += " · " + text(item.Effect, maxSummaryBytes)
		}
		children = append(children, &v1.Node{Kind: v1.KindText, Text: label, Size: "caption", Tone: v1.ToneSubtle})
	}
	if snapshot.ReducedTruncated {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: fmt.Sprintf("Showing %d of %d coverage notes.", snapshot.ReducedShown, snapshot.ReducedTotal), Size: "caption", Tone: v1.ToneSubtle})
	}
	if snapshot.CatalogRev != "" {
		children = append(children, &v1.Node{Kind: v1.KindText, Text: "Catalogue " + text(snapshot.CatalogRev, maxLabelBytes), Size: "caption", Tone: v1.ToneSubtle})
	}
	return card("privacy-coverage", children...)
}

func gradeChip(grade, key string) *v1.Node {
	style := styleForGrade(grade)
	return &v1.Node{Kind: v1.KindRow, Key: key, Gap: 5, Height: 28, Fill: style.fill, Shape: "stadium", Padding: 5, Children: []*v1.Node{
		// Fill and stroke are both theme roles; the grade word and icon keep
		// each state distinguishable without colour.
		{Kind: v1.KindIcon, Icon: style.icon, IconSize: 16},
		{Kind: v1.KindText, Text: style.word, Size: "label", Bold: true},
	}, Stroke: style.stroke, StrokeFill: style.strokeFill}
}

func sectionTitle(label string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: label, Size: "label", Bold: true}
}

func card(key string, children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Key: key, Gap: 7, Fill: "soft", Shape: "card", Padding: 12, Children: children}
}

func valueRow(label, value string) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 10, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Text: label, Size: "caption", Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: value, Size: "label"},
	}}
}

func styleForGrade(grade string) gradeStyle {
	switch grade {
	case "green":
		return gradeStyle{word: "Good", icon: "verified_user", fill: "accent"}
	case "amber":
		return gradeStyle{word: "Review", icon: "flag", fill: "surface", stroke: 1, strokeFill: "outline"}
	case "red":
		return gradeStyle{word: "Risky", icon: "gpp_bad", fill: "error-container"}
	default:
		return gradeStyle{word: "Unrated", icon: "disabled_by_default", fill: "soft"}
	}
}

func currentGrade(snapshot Snapshot, now time.Time) string {
	if !snapshot.Available || !snapshotCurrent(snapshot, now) || !validGrade(snapshot.Overall.Grade) {
		return "unknown"
	}
	return snapshot.Overall.Grade
}

func snapshotCurrent(snapshot Snapshot, now time.Time) bool {
	if snapshot.GeneratedAt.IsZero() {
		return false
	}
	age := now.Sub(snapshot.GeneratedAt)
	return age >= 0 && age < staleAfter
}

func scanAge(at, now time.Time) string {
	if at.IsZero() {
		return "No completed scan"
	}
	age := now.Sub(at)
	if age < 0 {
		return "Scan time is in the future"
	}
	if age < time.Minute {
		return "Scanned just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("Scanned %d min ago", int(age/time.Minute))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("Scanned %d hr ago", int(age/time.Hour))
	}
	return "Scanned " + at.Local().Format("2 Jan 15:04")
}

func findingCount(snapshot Snapshot) string {
	if snapshot.Overall.High > 0 {
		return fmt.Sprintf("%d findings · %d high priority", snapshot.Overall.Findings, snapshot.Overall.High)
	}
	if snapshot.Overall.Findings == 1 {
		return "1 finding"
	}
	return fmt.Sprintf("%d findings", snapshot.Overall.Findings)
}

func shownCount(shown, total int, singular string) string {
	if total < shown {
		total = shown
	}
	if total == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", total, singular)
}

func containment(value *bool) string {
	if value == nil {
		return "Unknown"
	}
	if *value {
		return "Contained"
	}
	return "Outside tunnel"
}

func humanize(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "_", " "), "-", " "))
	if value == "" {
		return "Unknown"
	}
	r, size := utf8.DecodeRuneInString(value)
	return string(unicode.ToUpper(r)) + value[size:]
}
