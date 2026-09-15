package githubnotifications

import (
	"encoding/json"
	"testing"
	"time"
)

func rawItem(id, title, subjectType, reason string) RawItem {
	r := RawItem{ID: id, Unread: true, Reason: reason, UpdatedAt: "2026-09-15T10:00:00Z"}
	r.Subject.Title = title
	r.Subject.Type = subjectType
	r.Subject.URL = "https://api.github.com/repos/octocat/hello/issues/7"
	r.Repository.FullName = "octocat/hello"
	r.Repository.HTMLURL = "https://github.com/octocat/hello"
	return r
}

func TestNormalizeListDropsInvalidIDs(t *testing.T) {
	good := rawItem("42", "Fix bug", "Issue", "assign")
	bad := rawItem("not-a-number", "Sneaky", "Issue", "assign")
	zero := rawItem("0", "Zero", "Issue", "assign")
	items := NormalizeList([]RawItem{good, bad, zero})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].ID != "42" || items[0].Title != "Fix bug" {
		t.Fatalf("item = %+v", items[0])
	}
	if items[0].ReasonLabel != "Assigned" {
		t.Fatalf("reason label = %q", items[0].ReasonLabel)
	}
	if items[0].URL != "https://github.com/octocat/hello/issues/7" {
		t.Fatalf("url = %q", items[0].URL)
	}
	if items[0].Repo != "octocat/hello" {
		t.Fatalf("repo = %q", items[0].Repo)
	}
}

func TestNormalizeSecurityAlertFlagsFailure(t *testing.T) {
	item, ok := NormalizeItem(rawItem("7", "CVE-2026-1234", "RepositoryVulnerabilityAlert", "security_alert"))
	if !ok {
		t.Fatal("security alert dropped")
	}
	if !item.IsFailure {
		t.Fatalf("security alert not flagged: %+v", item)
	}
}

func TestResolveHTMLURLVariants(t *testing.T) {
	cases := []struct {
		apiURL, subjectType, want string
	}{
		{"https://api.github.com/repos/o/r/pulls/9", "PullRequest", "https://github.com/o/r/pull/9"},
		{"https://api.github.com/repos/o/r/commits/abc123", "Commit", "https://github.com/o/r/commit/abc123"},
		{"https://api.github.com/repos/o/r/releases/3", "Release", "https://github.com/o/r/releases"},
		{"", "CheckSuite", "https://github.com/octocat/hello/actions"},
	}
	for _, c := range cases {
		r := rawItem("1", "t", c.subjectType, "subscribed")
		r.Subject.URL = c.apiURL
		if got := ResolveHTMLURL(r); got != c.want {
			t.Errorf("ResolveHTMLURL(%q, %s) = %q, want %q", c.apiURL, c.subjectType, got, c.want)
		}
	}
}

func TestFormatReasonCapitalizesUnknown(t *testing.T) {
	if got := FormatReason("ci_activity"); got != "CI activity" {
		t.Fatalf("known reason = %q", got)
	}
	if got := FormatReason("weird_new_reason"); got != "Weird New Reason" {
		t.Fatalf("unknown reason = %q", got)
	}
	if got := FormatReason(""); got != "Notification" {
		t.Fatalf("empty reason = %q", got)
	}
}

func TestFormatRelative(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ts   time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "now"},
		{now.Add(-5 * time.Minute), "5m"},
		{now.Add(-3 * time.Hour), "3h"},
		{now.Add(-2 * 24 * time.Hour), "2d"},
		{now.Add(-10 * 24 * time.Hour), "1w"},
		{now.Add(-21 * 24 * time.Hour), "3w"},
		{now.Add(-90 * 24 * time.Hour), "Jun 17"},
	}
	for _, c := range cases {
		if got := FormatRelative(c.ts.Format(time.RFC3339), now); got != c.want {
			t.Errorf("FormatRelative(%s) = %q, want %q", c.ts, got, c.want)
		}
	}
	if got := FormatRelative("garbage", now); got != "" {
		t.Fatalf("garbage = %q", got)
	}
}

func TestClassify(t *testing.T) {
	if Classify("API rate limit exceeded") != ErrRateLimited {
		t.Fatal("rate limit misclassified")
	}
	if Classify("HTTP 401: Bad credentials") != ErrAuth {
		t.Fatal("auth misclassified")
	}
	if Classify("connection refused") != ErrNetwork {
		t.Fatal("network misclassified")
	}
}

func TestValidThreadID(t *testing.T) {
	for _, ok := range []string{"1", "42", "999999"} {
		if !ValidThreadID(ok) {
			t.Errorf("ValidThreadID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "0", "-1", "1e3", "abc", "1; rm -rf /"} {
		if ValidThreadID(bad) {
			t.Errorf("ValidThreadID(%q) = true", bad)
		}
	}
}

func TestCacheRoundTrip(t *testing.T) {
	raw, _ := json.Marshal([]Item{mustItem(t, rawItem("5", "hello", "Issue", "assign"))})
	var items []Item
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
}

func mustItem(t *testing.T, r RawItem) Item {
	t.Helper()
	item, ok := NormalizeItem(r)
	if !ok {
		t.Fatal("normalize failed")
	}
	return item
}
