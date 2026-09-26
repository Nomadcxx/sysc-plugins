package githubnotifications

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MaxWorkPageSize = 100
	MaxWorkResults  = 1000
)

type WorkKind string

const (
	WorkReviews WorkKind = "reviews"
	WorkMyPRs   WorkKind = "my_prs"
	WorkIssues  WorkKind = "issues"
)

type RawWorkPage struct {
	TotalCount int           `json:"total_count"`
	Items      []RawWorkItem `json:"items"`
}

type RawWorkItem struct {
	Title         string    `json:"title"`
	Number        int       `json:"number"`
	RepositoryURL string    `json:"repository_url"`
	HTMLURL       string    `json:"html_url"`
	UpdatedAt     string    `json:"updated_at"`
	PullRequest   *struct{} `json:"pull_request"`
}

type WorkItem struct {
	Kind         WorkKind `json:"kind"`
	Title        string   `json:"title"`
	Repo         string   `json:"repo"`
	Number       int      `json:"number"`
	URL          string   `json:"url"`
	UpdatedAt    string   `json:"updated_at"`
	RelativeTime string   `json:"relative_time"`
}

type WorkPage struct {
	Items      []WorkItem `json:"items"`
	TotalCount int        `json:"total_count"`
	Page       int        `json:"page"`
	PerPage    int        `json:"per_page"`
	HasMore    bool       `json:"has_more"`
}

var repoAPIURL = regexp.MustCompile(`^https://api\.github\.com/repos/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)

func BuildWorkArgs(kind WorkKind, page, perPage int) ([]string, error) {
	query, ok := workQueries[kind]
	if !ok {
		return nil, fmt.Errorf("unknown work kind %q", kind)
	}
	if page < 1 || perPage < 1 {
		return nil, fmt.Errorf("work page and page size must be positive")
	}
	perPage = min(perPage, MaxWorkPageSize)
	if page > (MaxWorkResults+perPage-1)/perPage {
		return nil, fmt.Errorf("work page exceeds GitHub's %d-result search limit", MaxWorkResults)
	}
	params := url.Values{}
	params.Set("order", "desc")
	params.Set("page", strconv.Itoa(page))
	params.Set("per_page", strconv.Itoa(perPage))
	params.Set("q", query)
	params.Set("sort", "updated")
	return []string{"api", "search/issues?" + params.Encode()}, nil
}

var workQueries = map[WorkKind]string{
	WorkReviews: "is:open is:pr review-requested:@me",
	WorkMyPRs:   "is:open is:pr author:@me",
	WorkIssues:  "is:open is:issue assignee:@me",
}

func NormalizeWorkItem(raw RawWorkItem, kind WorkKind, now time.Time) (WorkItem, bool) {
	if _, ok := workQueries[kind]; !ok || raw.Number < 1 || strings.TrimSpace(raw.Title) == "" {
		return WorkItem{}, false
	}
	if isPR := raw.PullRequest != nil; isPR != (kind != WorkIssues) {
		return WorkItem{}, false
	}

	repository := repoAPIURL.FindStringSubmatch(raw.RepositoryURL)
	if repository == nil {
		return WorkItem{}, false
	}
	repo := repository[1] + "/" + repository[2]
	u, err := url.Parse(raw.HTMLURL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return WorkItem{}, false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0]+"/"+parts[1] != repo || parts[3] != strconv.Itoa(raw.Number) {
		return WorkItem{}, false
	}
	wantType := "pull"
	if kind == WorkIssues {
		wantType = "issues"
	}
	if parts[2] != wantType {
		return WorkItem{}, false
	}
	updated, err := time.Parse(time.RFC3339, raw.UpdatedAt)
	if err != nil {
		return WorkItem{}, false
	}
	return WorkItem{
		Kind: kind, Title: raw.Title, Repo: repo, Number: raw.Number,
		URL: raw.HTMLURL, UpdatedAt: updated.Format(time.RFC3339),
		RelativeTime: FormatRelative(raw.UpdatedAt, now),
	}, true
}

func NormalizeWorkPage(raw RawWorkPage, kind WorkKind, page, perPage int, now time.Time) (WorkPage, error) {
	if _, ok := workQueries[kind]; !ok {
		return WorkPage{}, fmt.Errorf("unknown work kind %q", kind)
	}
	if page < 1 || perPage < 1 || perPage > MaxWorkPageSize {
		return WorkPage{}, fmt.Errorf("invalid work page %d with page size %d", page, perPage)
	}
	if raw.TotalCount < 0 || len(raw.Items) > perPage {
		return WorkPage{}, fmt.Errorf("invalid work search result bounds")
	}
	items := make([]WorkItem, 0, len(raw.Items))
	for _, candidate := range raw.Items {
		if item, ok := NormalizeWorkItem(candidate, kind, now); ok {
			items = append(items, item)
		}
	}
	limit := min(raw.TotalCount, MaxWorkResults)
	return WorkPage{
		Items: items, TotalCount: raw.TotalCount, Page: page, PerPage: perPage,
		HasMore: page*perPage < limit,
	}, nil
}
