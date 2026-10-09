package githubnotifications

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/github-notifications/screenshot.png when
// CAPTURE=1: an inbox of eight invented notifications.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	item := func(id, title, kind, reason, repo, updated string, failure bool) Item {
		return Item{
			ID: id, Title: title, Type: kind, Reason: reason, ReasonLabel: FormatReason(reason),
			Repo: repo, URL: "https://github.com/" + repo, UpdatedAt: updated,
			RelativeTime: FormatRelative(updated, now), IsFailure: failure,
		}
	}
	items := []Item{
		item("101", "Retry failed uploads with backoff", "PullRequest", "review_requested", "acme/widgets", "2026-10-09T11:48:00Z", false),
		item("102", "Panel overflows on narrow displays", "Issue", "mention", "acme/dashboard", "2026-10-09T10:20:00Z", false),
		item("103", "CI failed on main", "CheckSuite", "ci_activity", "acme/widgets", "2026-10-09T09:05:00Z", true),
		item("104", "Add pagination to the activity feed", "PullRequest", "author", "acme/dashboard", "2026-10-08T17:30:00Z", false),
		item("105", "Release 2.4.0 is available", "Release", "subscribed", "acme/toolkit", "2026-10-08T08:00:00Z", false),
		item("106", "Bump the image decoder to 1.9", "PullRequest", "assign", "acme/toolkit", "2026-10-07T15:10:00Z", false),
		item("107", "Settings page loses scroll position", "Issue", "comment", "acme/dashboard", "2026-10-07T09:45:00Z", false),
		item("108", "Document the export format", "PullRequest", "mention", "acme/widgets", "2026-10-06T13:20:00Z", false),
	}
	capture.Panel(t, "github-notifications", PanelTreeForState(PanelState{
		Mode: ModeInbox, StatusLine: "8 unread",
		Inbox: InboxSnapshot{Status: StatusReady, Items: items},
	}))
}
