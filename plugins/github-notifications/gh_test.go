package githubnotifications

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildNotificationArgsUsesGitHubPageLimit(t *testing.T) {
	if got, want := BuildNotificationArgs(100), []string{"api", "notifications?all=false&per_page=100"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildNotificationArgs(100) = %#v, want %#v", got, want)
	}
	if got, want := BuildNotificationArgs(500), []string{"api", "notifications?all=false&per_page=100"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildNotificationArgs(500) = %#v, want %#v", got, want)
	}
}

func TestBuildNotificationPageArgsUsesNumericPage(t *testing.T) {
	if got, want := BuildNotificationPageArgs(3, 100), []string{"api", "notifications?all=false&per_page=100&page=3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildNotificationPageArgs() = %#v, want %#v", got, want)
	}
	if got := BuildNotificationPageArgs(-1, 0); !reflect.DeepEqual(got, BuildNotificationArgs(1)) {
		t.Fatalf("invalid page was not clamped: %#v", got)
	}
}

func TestBuildWorkArgsUsesConstantQueriesAndBoundedPages(t *testing.T) {
	cases := []struct {
		kind WorkKind
		q    string
	}{
		{WorkReviews, "is:open is:pr review-requested:@me"},
		{WorkMyPRs, "is:open is:pr author:@me"},
		{WorkIssues, "is:open is:issue assignee:@me"},
	}
	for _, tc := range cases {
		args, err := BuildWorkArgs(tc.kind, 2, 100)
		if err != nil {
			t.Fatalf("BuildWorkArgs(%q): %v", tc.kind, err)
		}
		query := url.Values{
			"order": {"desc"}, "page": {"2"}, "per_page": {"100"}, "q": {tc.q}, "sort": {"updated"},
		}.Encode()
		want := []string{"api", "search/issues?" + query}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("BuildWorkArgs(%q) = %#v, want %#v", tc.kind, args, want)
		}
	}
	if _, err := BuildWorkArgs(WorkKind("raw query injection"), 1, 100); err == nil {
		t.Fatal("unknown work kind accepted")
	}
	if _, err := BuildWorkArgs(WorkReviews, 0, 100); err == nil {
		t.Fatal("zero page accepted")
	}
}

func TestBuildActivityArgsUsesAuthenticatedGraphQLVariables(t *testing.T) {
	from := time.Date(2025, 9, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	args, err := BuildActivityArgs(from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "graphql", "-f", "query=" + activityGraphQLQuery,
		"-F", "from=2025-09-15T00:00:00Z", "-F", "to=2026-09-15T00:00:00Z"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("BuildActivityArgs() = %#v, want %#v", args, want)
	}
	for _, field := range []string{"viewer", "contributionsCollection", "contributionCalendar", "contributionLevel", "contributionCount"} {
		if !strings.Contains(activityGraphQLQuery, field) {
			t.Errorf("GraphQL query lacks %q", field)
		}
	}
	if _, err := BuildActivityArgs(to, from); err == nil {
		t.Fatal("reversed activity range accepted")
	}
}

func TestParseActivityResponseRejectsGraphQLErrors(t *testing.T) {
	if _, err := ParseActivityResponse([]byte(`{"errors":[{"message":"Resource not accessible"}]}`)); err == nil {
		t.Fatal("GraphQL error response accepted")
	}
	if _, err := ParseActivityResponse([]byte(`{}`)); err == nil {
		t.Fatal("empty GraphQL response accepted")
	}
}
