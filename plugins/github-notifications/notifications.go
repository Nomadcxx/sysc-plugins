// Package githubnotifications ports the behavior of the Noctalia community
// plugin "github-notifications": unread GitHub notifications in the bar, a
// panel list with mark-read actions, driven by the gh CLI.
package githubnotifications

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RawItem mirrors one entry of GitHub's /notifications response.
type RawItem struct {
	ID         string `json:"id"`
	Unread     bool   `json:"unread"`
	Reason     string `json:"reason"`
	UpdatedAt  string `json:"updated_at"`
	Repository struct {
		FullName string `json:"full_name"`
		Name     string `json:"name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	Subject struct {
		Title string `json:"title"`
		Type  string `json:"type"`
		URL   string `json:"url"`
	} `json:"subject"`
}

// Item is the normalized shape the views render.
type Item struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Reason       string `json:"reason"`
	ReasonLabel  string `json:"reason_label"`
	Repo         string `json:"repo"`
	URL          string `json:"url"`
	UpdatedAt    string `json:"updated_at"`
	RelativeTime string `json:"relative_time"`
	IsFailure    bool   `json:"is_failure"`
}

var reasonLabels = map[string]string{
	"mention":                  "Mentioned",
	"team_mention":             "Team mention",
	"review_requested":         "Review requested",
	"assign":                   "Assigned",
	"assigned":                 "Assigned",
	"author":                   "Author",
	"comment":                  "Comment",
	"ci_activity":              "CI activity",
	"security_alert":           "Security alert",
	"subscribed":               "Subscribed",
	"state_change":             "State changed",
	"approval_requested":       "Approval requested",
	"member_feature_requested": "Feature requested",
	"invitation":               "Invitation",
	"manual":                   "Manual",
}

// FormatReason maps a notification reason to a label, capitalizing unknown
// snake_case reasons the way the original does.
func FormatReason(reason string) string {
	if reason == "" {
		return "Notification"
	}
	if label, ok := reasonLabels[reason]; ok {
		return label
	}
	words := strings.Split(reason, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

var (
	pullURL   = regexp.MustCompile(`^https://api\.github\.com/repos/([\w.\-]+)/([\w.\-]+)/pulls/(\d+)$`)
	issueURL  = regexp.MustCompile(`^https://api\.github\.com/repos/([\w.\-]+)/([\w.\-]+)/issues/(\d+)$`)
	discURL   = regexp.MustCompile(`^https://api\.github\.com/repos/([\w.\-]+)/([\w.\-]+)/discussions/(\d+)$`)
	commitURL = regexp.MustCompile(`^https://api\.github\.com/repos/([\w.\-]+)/([\w.\-]+)/commits/([0-9a-fA-F]+)$`)
	relURL    = regexp.MustCompile(`^https://api\.github\.com/repos/([\w.\-]+)/([\w.\-]+)/releases/(\d+)$`)
	repoURL   = regexp.MustCompile(`^https://github\.com/([\w.\-]+)/([\w.\-]+)$`)
)

// ResolveHTMLURL converts the API URL of a subject into its github.com page,
// falling back to the repo page, then to the notifications inbox.
func ResolveHTMLURL(raw RawItem) string {
	if u := raw.Subject.URL; u != "" {
		for _, re := range []*regexp.Regexp{pullURL, issueURL, discURL} {
			if m := re.FindStringSubmatch(u); m != nil {
				kind := map[*regexp.Regexp]string{pullURL: "pull", issueURL: "issues", discURL: "discussions"}[re]
				return fmt.Sprintf("https://github.com/%s/%s/%s/%s", m[1], m[2], kind, m[3])
			}
		}
		if m := commitURL.FindStringSubmatch(u); m != nil {
			return fmt.Sprintf("https://github.com/%s/%s/commit/%s", m[1], m[2], m[3])
		}
		if m := relURL.FindStringSubmatch(u); m != nil {
			return fmt.Sprintf("https://github.com/%s/%s/releases", m[1], m[2])
		}
	}
	if m := repoURL.FindStringSubmatch(raw.Repository.HTMLURL); m != nil {
		if raw.Subject.Type == "CheckSuite" {
			return fmt.Sprintf("https://github.com/%s/%s/actions", m[1], m[2])
		}
		return fmt.Sprintf("https://github.com/%s/%s", m[1], m[2])
	}
	return "https://github.com/notifications"
}

// NormalizeItem validates and normalizes one raw notification. Items whose id
// is not a bare number are dropped, matching the original's thread-id rule.
func NormalizeItem(raw RawItem) (Item, bool) {
	if !ValidThreadID(raw.ID) {
		return Item{}, false
	}
	item := Item{
		ID:          raw.ID,
		Title:       raw.Subject.Title,
		Type:        raw.Subject.Type,
		Reason:      raw.Reason,
		ReasonLabel: FormatReason(raw.Reason),
		Repo:        raw.Repository.FullName,
		URL:         ResolveHTMLURL(raw),
		UpdatedAt:   raw.UpdatedAt,
	}
	if item.Title == "" {
		item.Title = "Untitled notification"
	}
	if item.Repo == "" {
		item.Repo = raw.Repository.Name
	}
	if item.Repo == "" {
		item.Repo = "GitHub"
	}
	if item.Type == "" {
		item.Type = "Unknown"
	}
	if item.Reason == "" {
		item.Reason = "subscribed"
	}
	item.RelativeTime = FormatRelative(raw.UpdatedAt, time.Now())
	lower := strings.ToLower(item.Title)
	item.IsFailure = raw.Subject.Type == "RepositoryVulnerabilityAlert" || raw.Reason == "security_alert" ||
		strings.Contains(lower, "fail") || strings.Contains(lower, "error")
	return item, true
}

// NormalizeList drops malformed entries.
func NormalizeList(raw []RawItem) []Item {
	out := make([]Item, 0, len(raw))
	for _, r := range raw {
		if item, ok := NormalizeItem(r); ok {
			out = append(out, item)
		}
	}
	return out
}

// ValidThreadID accepts positive numeric thread ids only; they are used to
// build a REST path.
func ValidThreadID(id string) bool {
	if id == "" {
		return false
	}
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n > 0
}

// FormatRelative renders an RFC3339 timestamp as a coarse relative age.
func FormatRelative(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 35*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	default:
		return t.Format("Jan 2")
	}
}
