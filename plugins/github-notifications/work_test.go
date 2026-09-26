package githubnotifications

import (
	"testing"
	"time"
)

func TestNormalizeWorkItemRequiresCanonicalTypeAndRepositoryURLs(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	raw := RawWorkItem{
		Title:         "Fix retries",
		Number:        248,
		RepositoryURL: "https://api.github.com/repos/acme/api",
		HTMLURL:       "https://github.com/acme/api/pull/248",
		UpdatedAt:     "2026-09-27T10:00:00Z",
		PullRequest:   &struct{}{},
	}
	item, ok := NormalizeWorkItem(raw, WorkReviews, now)
	if !ok {
		t.Fatal("valid review PR dropped")
	}
	if item.Kind != WorkReviews || item.Repo != "acme/api" || item.Number != 248 || item.URL != raw.HTMLURL || item.RelativeTime != "2h" {
		t.Fatalf("normalized item = %+v", item)
	}
	if _, ok := NormalizeWorkItem(raw, WorkIssues, now); ok {
		t.Fatal("pull request accepted as an issue")
	}

	bad := raw
	bad.HTMLURL = "https://evil.example/acme/api/pull/248"
	if _, ok := NormalizeWorkItem(bad, WorkReviews, now); ok {
		t.Fatal("unsafe item URL accepted")
	}
	bad = raw
	bad.RepositoryURL = "https://api.github.com/repos/other/repo"
	if _, ok := NormalizeWorkItem(bad, WorkReviews, now); ok {
		t.Fatal("mismatched repository URL accepted")
	}
	bad = raw
	bad.HTMLURL = "https://github.com/acme/api/pull/249"
	if _, ok := NormalizeWorkItem(bad, WorkReviews, now); ok {
		t.Fatal("mismatched issue number accepted")
	}
}

func TestNormalizeWorkPagePreservesGitHubTotal(t *testing.T) {
	raw := RawWorkPage{TotalCount: 211, Items: []RawWorkItem{{
		Title: "Open issue", Number: 19,
		RepositoryURL: "https://api.github.com/repos/acme/cli",
		HTMLURL:       "https://github.com/acme/cli/issues/19",
		UpdatedAt:     "2026-09-27T10:00:00Z",
	}}}
	page, err := NormalizeWorkPage(raw, WorkIssues, 2, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 211 || page.Page != 2 || page.PerPage != 100 || len(page.Items) != 1 {
		t.Fatalf("normalized page = %+v", page)
	}
}
