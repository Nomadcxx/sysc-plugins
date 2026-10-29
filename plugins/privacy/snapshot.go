// Copyright (C) 2026 Nomadcxx
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Schema               = 1
	MaxAuditOutputBytes  = 4 << 20
	maxCategories        = 16
	maxInstalledPerCat   = 4
	maxFindings          = 5
	maxActionsPerFinding = 2
	maxDefects           = 4
	maxReduced           = 12
	maxSandboxApps       = 4
	maxSandboxEvidence   = 8
	maxUnmatchedApps     = 12
	maxOriginsPerApp     = 8
	maxOriginSourceBytes = 80
	maxLabelBytes        = 160
	maxSummaryBytes      = 320
)

type Snapshot struct {
	Schema              int        `json:"schema"`
	Available           bool       `json:"available"`
	Reason              string     `json:"reason,omitempty"`
	Hint                string     `json:"hint,omitempty"`
	GeneratedAt         time.Time  `json:"generated_at"`
	CatalogRev          string     `json:"catalog_rev,omitempty"`
	Overall             Overall    `json:"overall"`
	Network             Network    `json:"network"`
	Sandbox             *Sandbox   `json:"sandbox,omitempty"`
	Categories          []Category `json:"categories"`
	CategoriesShown     int        `json:"categories_shown"`
	CategoriesTotal     int        `json:"categories_total"`
	CategoriesTruncated bool       `json:"categories_truncated"`
	Unmatched           []App      `json:"unmatched,omitempty"`
	UnmatchedShown      int        `json:"unmatched_shown,omitempty"`
	UnmatchedTotal      int        `json:"unmatched_total,omitempty"`
	UnmatchedTruncated  bool       `json:"unmatched_truncated,omitempty"`
	Findings            []Finding  `json:"findings"`
	FindingsShown       int        `json:"findings_shown"`
	FindingsTotal       int        `json:"findings_total"`
	FindingsTruncated   bool       `json:"findings_truncated"`
	Reduced             []Reduced  `json:"reduced"`
	ReducedShown        int        `json:"reduced_shown"`
	ReducedTotal        int        `json:"reduced_total"`
	ReducedTruncated    bool       `json:"reduced_truncated"`
}

type Overall struct {
	Grade    string `json:"grade"`
	Score    *int   `json:"score,omitempty"`
	Findings int    `json:"findings"`
	High     int    `json:"high"`
}

type Network struct {
	VPN                         VPN  `json:"vpn"`
	TelemetryConnectionsUnknown bool `json:"telemetry_connections_unknown"`
}

type Sandbox struct {
	Applicable      bool         `json:"applicable"`
	Grade           string       `json:"grade"`
	Apps            []SandboxApp `json:"apps"`
	AppsShown       int          `json:"apps_shown"`
	AppsTotal       int          `json:"apps_total"`
	AppsTruncated   bool         `json:"apps_truncated"`
	AppsChecked     int          `json:"apps_checked"`
	AppsIncomplete  int          `json:"apps_incomplete"`
	PartialCoverage bool         `json:"partial_coverage"`
	Reason          string       `json:"reason,omitempty"`
}

type SandboxApp struct {
	ID               string   `json:"id"`
	Scope            string   `json:"scope"`
	Grade            string   `json:"grade"`
	Grants           []string `json:"grants,omitempty"`
	GrantsShown      int      `json:"grants_shown,omitempty"`
	GrantsTotal      int      `json:"grants_total,omitempty"`
	GrantsTruncated  bool     `json:"grants_truncated,omitempty"`
	Unknown          []string `json:"unknown,omitempty"`
	UnknownShown     int      `json:"unknown_shown,omitempty"`
	UnknownTotal     int      `json:"unknown_total,omitempty"`
	UnknownTruncated bool     `json:"unknown_truncated,omitempty"`
}

type VPN struct {
	State                 string   `json:"state"`
	Client                string   `json:"client,omitempty"`
	DefaultRouteViaTunnel *bool    `json:"default_route_via_tunnel,omitempty"`
	DNSContained          *bool    `json:"dns_contained,omitempty"`
	IPv6Contained         *bool    `json:"ipv6_contained,omitempty"`
	KillSwitchNote        string   `json:"kill_switch_note,omitempty"`
	Defects               []Defect `json:"defects"`
	DefectsShown          int      `json:"defects_shown"`
	DefectsTotal          int      `json:"defects_total"`
	DefectsTruncated      bool     `json:"defects_truncated"`
}

type Defect struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type Category struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Grade              string `json:"grade"`
	Installed          []App  `json:"installed"`
	InstalledShown     int    `json:"installed_shown"`
	InstalledTotal     int    `json:"installed_total"`
	InstalledTruncated bool   `json:"installed_truncated"`
	Worst              *App   `json:"worst,omitempty"`
}

type App struct {
	ID               string      `json:"id,omitempty"`
	Name             string      `json:"name"`
	Grade            string      `json:"grade"`
	Scope            string      `json:"scope,omitempty"`
	Origins          []AppOrigin `json:"origins,omitempty"`
	OriginsShown     int         `json:"origins_shown,omitempty"`
	OriginsTotal     int         `json:"origins_total,omitempty"`
	OriginsTruncated bool        `json:"origins_truncated,omitempty"`
}

type AppOrigin struct {
	Source   string `json:"source"`
	Official bool   `json:"official"`
}

type Finding struct {
	ID               string   `json:"id"`
	Severity         string   `json:"severity"`
	Grade            string   `json:"grade"`
	Category         string   `json:"category"`
	Title            string   `json:"title"`
	Summary          string   `json:"summary"`
	Actions          []Action `json:"actions"`
	ActionsShown     int      `json:"actions_shown"`
	ActionsTotal     int      `json:"actions_total"`
	ActionsTruncated bool     `json:"actions_truncated"`
}

type Action struct {
	Tier   int    `json:"tier"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Target string `json:"target,omitempty"`
}

type Reduced struct {
	Fact   string `json:"fact"`
	Effect string `json:"effect"`
}

type auditEnvelope struct {
	Type     string          `json:"t"`
	Snapshot json.RawMessage `json:"snapshot"`
	Error    string          `json:"error"`
}

type auditSnapshot struct {
	Schema      int       `json:"schema"`
	Available   bool      `json:"available"`
	GeneratedAt time.Time `json:"generated_at"`
	CatalogRev  string    `json:"catalog_rev"`
	Overall     struct {
		Grade    string `json:"grade"`
		Score    *int   `json:"score"`
		Findings int    `json:"findings"`
		High     int    `json:"high"`
	} `json:"overall"`
	Network struct {
		VPN struct {
			State                 string   `json:"state"`
			Client                string   `json:"client"`
			DefaultRouteViaTunnel *bool    `json:"default_route_via_tunnel"`
			DNSContained          *bool    `json:"dns_contained"`
			IPv6Contained         *bool    `json:"ipv6_contained"`
			KillSwitchNote        string   `json:"kill_switch_note"`
			Defects               []Defect `json:"defects"`
		} `json:"vpn"`
		TelemetryConnectionsUnknown bool `json:"telemetry_connections_unknown"`
	} `json:"network"`
	Sandbox    *auditSandbox `json:"sandbox"`
	Categories []struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		Grade              string `json:"grade"`
		Installed          []App  `json:"installed"`
		InstalledShown     int    `json:"installed_shown"`
		InstalledTotal     int    `json:"installed_total"`
		InstalledTruncated bool   `json:"installed_truncated"`
		Worst              *App   `json:"worst"`
	} `json:"categories"`
	Unmatched          []App `json:"unmatched"`
	UnmatchedTotal     int   `json:"unmatched_total"`
	UnmatchedTruncated bool  `json:"unmatched_truncated"`
	Findings           []struct {
		ID       string   `json:"id"`
		Severity string   `json:"severity"`
		Grade    string   `json:"grade"`
		Category string   `json:"category"`
		Title    string   `json:"title"`
		Detail   string   `json:"detail"`
		Actions  []Action `json:"actions"`
	} `json:"findings"`
	FindingsTotal     int       `json:"findings_total"`
	FindingsTruncated bool      `json:"findings_truncated"`
	Reduced           []Reduced `json:"reduced"`
	EntryPoint        string    `json:"entry_point"`
}

type auditSandbox struct {
	Applicable      bool              `json:"applicable"`
	Grade           string            `json:"grade"`
	Apps            []auditSandboxApp `json:"apps"`
	AppsShown       int               `json:"apps_shown"`
	AppsTotal       int               `json:"apps_total"`
	AppsTruncated   bool              `json:"apps_truncated"`
	AppsChecked     int               `json:"apps_checked"`
	AppsIncomplete  int               `json:"apps_incomplete"`
	PartialCoverage bool              `json:"partial_coverage"`
	Reason          string            `json:"reason"`
}

type auditSandboxApp struct {
	ID               string   `json:"id"`
	Scope            string   `json:"scope"`
	Grade            string   `json:"grade"`
	Grants           []string `json:"grants"`
	GrantsTotal      int      `json:"grants_total"`
	GrantsTruncated  bool     `json:"grants_truncated"`
	Unknown          []string `json:"unknown"`
	UnknownTotal     int      `json:"unknown_total"`
	UnknownTruncated bool     `json:"unknown_truncated"`
}

func UnavailableSnapshot(reason string, at time.Time) Snapshot {
	if reason == "" {
		reason = "privacybit audit output is unavailable"
	}
	return Snapshot{
		Schema: Schema, Available: false, Reason: text(reason, maxSummaryBytes),
		Hint:        "Install privacybit and make sure it is on PATH or in ~/.local/bin.",
		GeneratedAt: at.UTC(), Overall: Overall{Grade: "unknown"},
		Network:    Network{VPN: VPN{State: "unknown", Defects: []Defect{}}, TelemetryConnectionsUnknown: true},
		Categories: []Category{}, Findings: []Finding{}, Reduced: []Reduced{},
	}
}

func ParseAuditOutput(data []byte) (Snapshot, error) {
	if len(data) == 0 || len(data) > MaxAuditOutputBytes {
		return Snapshot{}, errors.New("privacybit output is empty or exceeds 4 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var event auditEnvelope
	if err := decoder.Decode(&event); err != nil {
		return Snapshot{}, fmt.Errorf("decode privacybit event: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Snapshot{}, errors.New("privacybit output has more than one event")
		}
		return Snapshot{}, fmt.Errorf("read privacybit output trailer: %w", err)
	}
	if event.Type != "done" {
		if event.Type == "error" && event.Error != "" {
			return Snapshot{}, errors.New(text(event.Error, maxSummaryBytes))
		}
		return Snapshot{}, fmt.Errorf("privacybit returned event %q instead of done", event.Type)
	}
	if len(event.Snapshot) == 0 {
		return Snapshot{}, errors.New("privacybit done event has no snapshot")
	}
	var input auditSnapshot
	if err := json.Unmarshal(event.Snapshot, &input); err != nil {
		return Snapshot{}, fmt.Errorf("decode privacybit snapshot: %w", err)
	}
	if input.Schema != Schema {
		return Snapshot{}, fmt.Errorf("unsupported privacybit snapshot schema %d", input.Schema)
	}
	if !input.Available || input.GeneratedAt.IsZero() {
		return Snapshot{}, errors.New("privacybit snapshot is unavailable or has no timestamp")
	}
	if !validGrade(input.Overall.Grade) {
		return Snapshot{}, fmt.Errorf("privacybit returned invalid overall grade %q", input.Overall.Grade)
	}

	out := Snapshot{
		Schema: Schema, Available: true, GeneratedAt: input.GeneratedAt.UTC(),
		CatalogRev: text(input.CatalogRev, maxLabelBytes),
		Overall: Overall{Grade: input.Overall.Grade, Score: input.Overall.Score,
			Findings: input.Overall.Findings, High: input.Overall.High},
		Network: Network{
			VPN: VPN{State: text(input.Network.VPN.State, maxLabelBytes),
				Client:                text(input.Network.VPN.Client, maxLabelBytes),
				DefaultRouteViaTunnel: input.Network.VPN.DefaultRouteViaTunnel,
				DNSContained:          input.Network.VPN.DNSContained, IPv6Contained: input.Network.VPN.IPv6Contained,
				KillSwitchNote: text(input.Network.VPN.KillSwitchNote, maxSummaryBytes)},
			TelemetryConnectionsUnknown: true,
		},
	}
	if input.Sandbox != nil {
		sandbox, err := projectSandbox(*input.Sandbox)
		if err != nil {
			return Snapshot{}, err
		}
		if sandbox.Applicable && gradeRank(out.Overall.Grade) < gradeRank(sandbox.Grade) {
			return Snapshot{}, errors.New("privacybit returned an overall grade safer than its Flatpak sandbox grade")
		}
		out.Sandbox = sandbox
	}
	if out.Network.VPN.State == "" {
		out.Network.VPN.State = "unknown"
	}
	out.Network.VPN.Defects, out.Network.VPN.DefectsTotal, out.Network.VPN.DefectsTruncated = capDefects(input.Network.VPN.Defects)
	out.Network.VPN.DefectsShown = len(out.Network.VPN.Defects)

	out.CategoriesTotal = len(input.Categories)
	for _, category := range input.Categories {
		if !validGrade(category.Grade) {
			return Snapshot{}, fmt.Errorf("privacybit returned invalid grade %q for category %q", category.Grade, category.ID)
		}
		if gradeRank(out.Overall.Grade) < gradeRank(category.Grade) {
			return Snapshot{}, fmt.Errorf("privacybit returned an overall grade safer than category %q", category.ID)
		}
		installedTotal := category.InstalledTotal
		if installedTotal < len(category.Installed) {
			installedTotal = len(category.Installed)
		}
		projected := Category{
			ID: text(category.ID, maxLabelBytes), Name: text(category.Name, maxLabelBytes),
			Grade: category.Grade, InstalledTotal: installedTotal,
			InstalledTruncated: category.InstalledTruncated,
		}
		for _, app := range category.Installed {
			if !validGrade(app.Grade) {
				return Snapshot{}, fmt.Errorf("privacybit returned invalid grade %q for app %q", app.Grade, app.Name)
			}
			if gradeRank(category.Grade) < gradeRank(app.Grade) {
				return Snapshot{}, fmt.Errorf("privacybit returned a category grade safer than app %q in category %q", app.Name, category.ID)
			}
			projectedApp, err := projectApp(app)
			if err != nil {
				return Snapshot{}, fmt.Errorf("privacybit returned invalid origin context for app %q: %w", app.Name, err)
			}
			projected.Installed = append(projected.Installed, projectedApp)
		}
		if category.Worst != nil {
			if !validGrade(category.Worst.Grade) {
				return Snapshot{}, fmt.Errorf("privacybit returned invalid grade %q for worst app %q", category.Worst.Grade, category.Worst.Name)
			}
			if gradeRank(category.Grade) < gradeRank(category.Worst.Grade) {
				return Snapshot{}, fmt.Errorf("privacybit returned a category grade safer than worst app %q in category %q", category.Worst.Name, category.ID)
			}
			worst, err := projectApp(*category.Worst)
			if err != nil {
				return Snapshot{}, fmt.Errorf("privacybit returned invalid origin context for worst app %q: %w", category.Worst.Name, err)
			}
			projected.Worst = &worst
		}
		projected.InstalledTotal = max(projected.InstalledTotal, len(projected.Installed))
		if len(projected.Installed) > maxInstalledPerCat {
			projected.Installed = projected.Installed[:maxInstalledPerCat]
			projected.InstalledTruncated = true
		}
		projected.InstalledShown = len(projected.Installed)
		projected.InstalledTruncated = projected.InstalledTruncated || projected.InstalledShown < projected.InstalledTotal
		out.Categories = append(out.Categories, projected)
	}
	if len(out.Categories) > maxCategories {
		out.Categories = out.Categories[:maxCategories]
		out.CategoriesTruncated = true
	}
	out.CategoriesShown = len(out.Categories)
	out.CategoriesTruncated = out.CategoriesTruncated || out.CategoriesShown < out.CategoriesTotal
	if input.UnmatchedTotal < 0 {
		return Snapshot{}, errors.New("privacybit returned a negative unmatched app count")
	}
	out.UnmatchedTotal = max(input.UnmatchedTotal, len(input.Unmatched))
	out.UnmatchedTruncated = input.UnmatchedTruncated
	for _, app := range input.Unmatched {
		if app.Grade != "unknown" {
			return Snapshot{}, fmt.Errorf("privacybit returned non-unknown grade %q for unmatched app %q", app.Grade, app.Name)
		}
		projectedApp, err := projectApp(app)
		if err != nil {
			return Snapshot{}, fmt.Errorf("privacybit returned invalid origin context for unmatched app %q: %w", app.Name, err)
		}
		out.Unmatched = append(out.Unmatched, projectedApp)
	}
	if len(out.Unmatched) > maxUnmatchedApps {
		out.Unmatched = out.Unmatched[:maxUnmatchedApps]
		out.UnmatchedTruncated = true
	}
	out.UnmatchedShown = len(out.Unmatched)
	out.UnmatchedTruncated = out.UnmatchedTruncated || out.UnmatchedShown < out.UnmatchedTotal

	out.FindingsTotal = input.FindingsTotal
	if out.FindingsTotal < len(input.Findings) {
		out.FindingsTotal = len(input.Findings)
	}
	out.FindingsTruncated = input.FindingsTruncated
	findings := append([]struct {
		ID       string   `json:"id"`
		Severity string   `json:"severity"`
		Grade    string   `json:"grade"`
		Category string   `json:"category"`
		Title    string   `json:"title"`
		Detail   string   `json:"detail"`
		Actions  []Action `json:"actions"`
	}(nil), input.Findings...)
	for i := range findings {
		// The audit leaves factual Flatpak scope findings ungraded; expose that as unknown.
		if findings[i].Grade == "" && findings[i].Severity == "low" && strings.HasPrefix(findings[i].ID, "flatpak.scope.") {
			findings[i].Grade = "unknown"
		}
		if !validGrade(findings[i].Grade) {
			return Snapshot{}, fmt.Errorf("privacybit returned invalid grade %q for finding %q", findings[i].Grade, findings[i].ID)
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) != severityRank(findings[j].Severity) {
			return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
		}
		if gradeRank(findings[i].Grade) != gradeRank(findings[j].Grade) {
			return gradeRank(findings[i].Grade) > gradeRank(findings[j].Grade)
		}
		return findings[i].ID < findings[j].ID
	})
	for _, finding := range findings {
		projected := Finding{
			ID: text(finding.ID, maxLabelBytes), Severity: text(finding.Severity, maxLabelBytes),
			Grade: finding.Grade, Category: text(finding.Category, maxLabelBytes),
			Title: text(finding.Title, maxLabelBytes), Summary: text(finding.Detail, maxSummaryBytes),
			ActionsTotal: len(finding.Actions),
		}
		for _, action := range finding.Actions {
			if action.Tier != 0 && action.Tier != 3 {
				continue
			}
			projected.Actions = append(projected.Actions, Action{
				Tier: action.Tier, Kind: text(action.Kind, maxLabelBytes),
				Label: text(action.Label, maxLabelBytes), Target: text(action.Target, maxLabelBytes),
			})
		}
		projected.ActionsTotal = len(projected.Actions)
		if len(projected.Actions) > maxActionsPerFinding {
			projected.Actions = projected.Actions[:maxActionsPerFinding]
			projected.ActionsTruncated = true
		}
		projected.ActionsShown = len(projected.Actions)
		projected.ActionsTruncated = projected.ActionsTruncated || projected.ActionsShown < projected.ActionsTotal
		out.Findings = append(out.Findings, projected)
	}
	if len(out.Findings) > maxFindings {
		out.Findings = out.Findings[:maxFindings]
		out.FindingsTruncated = true
	}
	out.FindingsShown = len(out.Findings)
	out.FindingsTruncated = out.FindingsTruncated || out.FindingsShown < out.FindingsTotal

	for _, item := range input.Reduced {
		out.Reduced = append(out.Reduced, Reduced{
			Fact: text(item.Fact, maxLabelBytes), Effect: text(item.Effect, maxSummaryBytes),
		})
	}
	ensureTelemetryUnknown(&out.Reduced)
	out.ReducedTotal = len(out.Reduced)
	prioritizeTelemetry(&out.Reduced)
	if len(out.Reduced) > maxReduced {
		out.Reduced = out.Reduced[:maxReduced]
		out.ReducedTruncated = true
	}
	out.ReducedShown = len(out.Reduced)
	out.ReducedTruncated = out.ReducedTruncated || out.ReducedShown < out.ReducedTotal

	return out, nil
}

func projectApp(app App) (App, error) {
	if app.OriginsShown < 0 || app.OriginsTotal < 0 {
		return App{}, errors.New("negative origin count")
	}
	out := App{ID: text(app.ID, maxLabelBytes), Name: text(app.Name, maxLabelBytes), Grade: app.Grade, Scope: text(app.Scope, maxLabelBytes)}
	seen := make(map[AppOrigin]struct{}, len(app.Origins))
	for _, origin := range app.Origins {
		origin.Source = originSource(origin.Source)
		if origin.Source == "" {
			continue
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		out.Origins = append(out.Origins, origin)
	}
	sort.Slice(out.Origins, func(i, j int) bool {
		if out.Origins[i].Source != out.Origins[j].Source {
			return out.Origins[i].Source < out.Origins[j].Source
		}
		return !out.Origins[i].Official && out.Origins[j].Official
	})
	if app.OriginsShown > 0 && app.OriginsShown < len(out.Origins) {
		return App{}, errors.New("origin shown count is below its list length")
	}
	out.OriginsTotal = max(app.OriginsTotal, len(out.Origins))
	if len(out.Origins) > maxOriginsPerApp {
		out.Origins = out.Origins[:maxOriginsPerApp]
		out.OriginsTruncated = true
	}
	out.OriginsShown = len(out.Origins)
	out.OriginsTruncated = out.OriginsTruncated || app.OriginsTruncated || out.OriginsShown < out.OriginsTotal
	return out, nil
}

func originSource(value string) string {
	value = strings.ToValidUTF8(value, "�")
	var clean strings.Builder
	for _, r := range value {
		if unicode.IsPrint(r) {
			clean.WriteRune(r)
		} else {
			clean.WriteByte(' ')
		}
	}
	return text(strings.Join(strings.Fields(clean.String()), " "), maxOriginSourceBytes)
}

func projectSandbox(input auditSandbox) (*Sandbox, error) {
	if !validGrade(input.Grade) {
		return nil, fmt.Errorf("privacybit returned invalid Flatpak sandbox grade %q", input.Grade)
	}
	if input.AppsShown < 0 || input.AppsTotal < 0 || input.AppsChecked < 0 || input.AppsIncomplete < 0 {
		return nil, errors.New("privacybit returned negative Flatpak sandbox counts")
	}
	appsTotal := max(input.AppsTotal, len(input.Apps))
	if input.AppsChecked > appsTotal || input.AppsIncomplete > appsTotal {
		return nil, errors.New("privacybit returned inconsistent Flatpak sandbox counts")
	}
	if (input.Applicable && appsTotal > 0 && len(input.Apps) == 0) || (!input.Applicable && appsTotal > 0) {
		return nil, errors.New("privacybit returned inconsistent Flatpak sandbox applicability")
	}
	out := &Sandbox{
		Applicable: input.Applicable, Grade: input.Grade,
		AppsTotal: appsTotal, AppsChecked: input.AppsChecked,
		AppsIncomplete: input.AppsIncomplete,
		Reason:         text(input.Reason, maxSummaryBytes),
	}
	out.PartialCoverage = input.PartialCoverage || input.AppsIncomplete > 0 || (input.Applicable && input.Grade == "unknown")
	assessedApps, incompleteApps := 0, 0
	apps := append([]auditSandboxApp(nil), input.Apps...)
	sort.Slice(apps, func(i, j int) bool {
		if gradeRank(apps[i].Grade) != gradeRank(apps[j].Grade) {
			return gradeRank(apps[i].Grade) > gradeRank(apps[j].Grade)
		}
		if apps[i].ID != apps[j].ID {
			return apps[i].ID < apps[j].ID
		}
		return apps[i].Scope < apps[j].Scope
	})
	for i, app := range apps {
		id, scope := text(app.ID, maxLabelBytes), text(app.Scope, maxLabelBytes)
		if id == "" || scope == "" || !validGrade(app.Grade) {
			return nil, fmt.Errorf("privacybit returned an invalid Flatpak sandbox app %q", id)
		}
		grants, grantsTotal, grantsTruncated, err := projectSandboxLabels(app.Grants, app.GrantsTotal, app.GrantsTruncated)
		if err != nil {
			return nil, fmt.Errorf("privacybit returned invalid grants for Flatpak app %q: %w", id, err)
		}
		unknown, unknownTotal, unknownTruncated, err := projectSandboxLabels(app.Unknown, app.UnknownTotal, app.UnknownTruncated)
		if err != nil {
			return nil, fmt.Errorf("privacybit returned invalid unknown permissions for Flatpak app %q: %w", id, err)
		}
		if unknownTotal > 0 && app.Grade != "unknown" {
			return nil, fmt.Errorf("privacybit returned a known Flatpak grade with unclassified evidence for %q", id)
		}
		if gradeRank(out.Grade) < gradeRank(app.Grade) {
			return nil, fmt.Errorf("privacybit returned a Flatpak grade safer than app %q", id)
		}
		if app.Grade == "unknown" || unknownTotal > 0 {
			incompleteApps++
			out.PartialCoverage = true
		}
		if app.Grade != "unknown" {
			assessedApps++
		}
		if i < maxSandboxApps {
			out.Apps = append(out.Apps, SandboxApp{
				ID: id, Scope: scope, Grade: app.Grade,
				Grants: grants, GrantsShown: len(grants), GrantsTotal: grantsTotal, GrantsTruncated: grantsTruncated,
				Unknown: unknown, UnknownShown: len(unknown), UnknownTotal: unknownTotal, UnknownTruncated: unknownTruncated,
			})
		}
	}
	if input.AppsChecked < assessedApps || input.AppsIncomplete < incompleteApps {
		return nil, errors.New("privacybit returned Flatpak sandbox counts below the visible evidence")
	}
	if input.Applicable && appsTotal == 0 && input.Grade != "unknown" {
		return nil, errors.New("privacybit graded Flatpak sandbox without installed app evidence")
	}
	if out.PartialCoverage && input.Grade != "unknown" {
		return nil, errors.New("privacybit returned a known Flatpak sandbox grade with incomplete coverage")
	}
	if !input.Applicable && (input.Grade != "unknown" || out.PartialCoverage) {
		return nil, errors.New("privacybit returned a grade for an inapplicable Flatpak sandbox")
	}
	out.AppsShown = len(out.Apps)
	out.AppsTruncated = input.AppsTruncated || out.AppsShown < out.AppsTotal
	if out.Apps == nil {
		out.Apps = []SandboxApp{}
	}
	return out, nil
}

func projectSandboxLabels(values []string, total int, truncated bool) ([]string, int, bool, error) {
	if total < 0 {
		return nil, 0, false, errors.New("negative item count")
	}
	total = max(total, len(values))
	shown := make([]string, 0, min(len(values), maxSandboxEvidence))
	for _, value := range values[:min(len(values), maxSandboxEvidence)] {
		shown = append(shown, text(value, maxLabelBytes))
	}
	return shown, total, truncated || len(shown) < total, nil
}

func capDefects(defects []Defect) ([]Defect, int, bool) {
	total := len(defects)
	out := make([]Defect, 0, min(total, maxDefects))
	for _, defect := range defects[:min(total, maxDefects)] {
		out = append(out, Defect{ID: text(defect.ID, maxLabelBytes), Title: text(defect.Title, maxLabelBytes), Detail: text(defect.Detail, maxSummaryBytes)})
	}
	return out, total, total > len(out)
}

func ensureTelemetryUnknown(reduced *[]Reduced) {
	for _, item := range *reduced {
		if item.Fact == "network.telemetry" {
			return
		}
	}
	*reduced = append(*reduced, Reduced{
		Fact: "network.telemetry", Effect: "live telemetry endpoint attribution is unavailable",
	})
}

func prioritizeTelemetry(reduced *[]Reduced) {
	items := *reduced
	for i, item := range items {
		if item.Fact == "network.telemetry" && i != 0 {
			copy(items[1:i+1], items[0:i])
			items[0] = item
			break
		}
	}
}

func validGrade(value string) bool {
	switch value {
	case "green", "amber", "red", "unknown":
		return true
	default:
		return false
	}
}

func severityRank(value string) int {
	switch value {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func gradeRank(value string) int {
	switch value {
	case "green":
		return 0
	case "amber":
		return 1
	case "red":
		return 2
	case "unknown":
		return 3
	default:
		return -1
	}
}

func text(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	var clean strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			if r == '\n' || r == '\r' || r == '\t' {
				clean.WriteByte(' ')
			}
			continue
		}
		clean.WriteRune(r)
	}
	value = clean.String()
	if len(value) <= limit {
		return value
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}
