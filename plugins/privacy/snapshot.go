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
	Categories          []Category `json:"categories"`
	CategoriesShown     int        `json:"categories_shown"`
	CategoriesTotal     int        `json:"categories_total"`
	CategoriesTruncated bool       `json:"categories_truncated"`
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
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Grade string `json:"grade"`
	Scope string `json:"scope,omitempty"`
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
	Findings []struct {
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

func UnavailableSnapshot(reason string, at time.Time) Snapshot {
	if reason == "" {
		reason = "privacybit audit output is unavailable"
	}
	return Snapshot{
		Schema: Schema, Available: false, Reason: text(reason, maxSummaryBytes),
		Hint:        "Install privacybit and make sure it is available on PATH.",
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
			projected.Installed = append(projected.Installed, projectApp(app))
		}
		if category.Worst != nil {
			if !validGrade(category.Worst.Grade) {
				return Snapshot{}, fmt.Errorf("privacybit returned invalid grade %q for worst app %q", category.Worst.Grade, category.Worst.Name)
			}
			worst := projectApp(*category.Worst)
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
		if !validGrade(finding.Grade) {
			return Snapshot{}, fmt.Errorf("privacybit returned invalid grade %q for finding %q", finding.Grade, finding.ID)
		}
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

func projectApp(app App) App {
	return App{ID: text(app.ID, maxLabelBytes), Name: text(app.Name, maxLabelBytes), Grade: app.Grade, Scope: text(app.Scope, maxLabelBytes)}
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
