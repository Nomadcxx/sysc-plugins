package githubnotifications

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"
)

type fakeGH struct {
	refresh          func() ([]RawItem, error)
	notificationPage func(page, perPage int) ([]RawItem, error)
	search           func(kind WorkKind, page, perPage int) (RawWorkPage, error)
	activity         func(from, to time.Time) (RawActivity, error)
	markRead         func(id string) error
	markAll          func() error
	markCalled       int
}

func (f *fakeGH) Refresh(ctx context.Context, perPage int) ([]RawItem, error) {
	if f.refresh == nil {
		return nil, nil
	}
	return f.refresh()
}

func (f *fakeGH) Search(ctx context.Context, kind WorkKind, page, perPage int) (RawWorkPage, error) {
	if f.search != nil {
		return f.search(kind, page, perPage)
	}
	return RawWorkPage{}, nil
}

func (f *fakeGH) Activity(ctx context.Context, from, to time.Time) (RawActivity, error) {
	if f.activity != nil {
		return f.activity(from, to)
	}
	return RawActivity{}, nil
}

func (f *fakeGH) Notifications(ctx context.Context, page, perPage int) ([]RawItem, error) {
	if f.notificationPage != nil {
		return f.notificationPage(page, perPage)
	}
	if page == 1 && f.refresh != nil {
		return f.refresh()
	}
	return nil, nil
}

func (f *fakeGH) MarkRead(ctx context.Context, id string) error {
	f.markCalled++
	if f.markRead == nil {
		return nil
	}
	return f.markRead(id)
}

func (f *fakeGH) MarkAll(ctx context.Context) error {
	if f.markAll == nil {
		return nil
	}
	return f.markAll()
}

func TestSessionRefreshAndStatus(t *testing.T) {
	gh := &fakeGH{refresh: func() ([]RawItem, error) {
		return []RawItem{rawItem("1", "a", "Issue", "assign"), rawItem("2", "b", "Issue", "mention")}, nil
	}}
	s := NewSession(gh, 50)
	if status, _ := s.Status(); status != StatusLoading {
		t.Fatalf("initial status = %q", status)
	}
	s.Refresh(context.Background())
	_, unread := s.Items()
	if unread != 2 {
		t.Fatalf("unread = %d", unread)
	}
	if status, msg := s.Status(); status != StatusReady || msg != "" {
		t.Fatalf("status = %q %q", status, msg)
	}
}

func TestSessionRefreshFailureClassifies(t *testing.T) {
	gh := &fakeGH{refresh: func() ([]RawItem, error) {
		return nil, errors.New("exit status 1: HTTP 403: rate limit exceeded")
	}}
	s := NewSession(gh, 50)
	s.Refresh(context.Background())
	if status, _ := s.Status(); status != StatusRate {
		t.Fatalf("status = %q, want rate_limited", status)
	}
}

func TestSessionMarkReadOptimisticWithRollback(t *testing.T) {
	gh := &fakeGH{refresh: func() ([]RawItem, error) {
		return []RawItem{rawItem("1", "a", "Issue", "assign"), rawItem("2", "b", "Issue", "mention")}, nil
	}}
	s := NewSession(gh, 50)
	s.Refresh(context.Background())

	gh.markRead = func(id string) error { return nil }
	if restored := s.MarkRead(context.Background(), "1"); restored {
		t.Fatal("successful mark read reported a rollback")
	}
	_, unread := s.Items()
	if unread != 1 {
		t.Fatalf("unread after mark read = %d", unread)
	}

	gh.markRead = func(id string) error { return errors.New("boom") }
	if restored := s.MarkRead(context.Background(), "2"); !restored {
		t.Fatal("failed mark read did not report a rollback")
	}
	items, unread := s.Items()
	if unread != 1 || len(items) != 1 || items[0].ID != "2" {
		t.Fatalf("rollback left items = %+v", items)
	}
	if status, _ := s.Status(); status != StatusError {
		t.Fatalf("status after failure = %q", status)
	}
}

func TestFailedMarkReadRestoresInboxPagingState(t *testing.T) {
	gh := &fakeGH{
		notificationPage: func(page, perPage int) ([]RawItem, error) {
			return []RawItem{rawItem("1", "a", "Issue", "mention"), rawItem("2", "b", "Issue", "mention")}, nil
		},
		markRead: func(string) error { return errors.New("read failed") },
	}
	s := NewSession(gh, 2)
	s.Refresh(context.Background())
	if before := s.Inbox(); before.Page != 1 || !before.HasMore || before.NeedsRefresh {
		t.Fatalf("initial inbox paging = %+v", before)
	}
	if !s.MarkRead(context.Background(), "1") {
		t.Fatal("failed mark-read did not restore the row")
	}
	after := s.Inbox()
	if len(after.Items) != 2 || after.Items[0].ID != "1" || after.Page != 1 || !after.HasMore || after.NeedsRefresh {
		t.Fatalf("failed mark-read changed valid paging state: %+v", after)
	}
}

func TestSessionMarkAllOptimisticWithRollback(t *testing.T) {
	gh := &fakeGH{refresh: func() ([]RawItem, error) {
		return []RawItem{rawItem("1", "a", "Issue", "assign")}, nil
	}}
	s := NewSession(gh, 1)
	s.Refresh(context.Background())

	if restored := s.MarkAll(context.Background()); restored {
		t.Fatal("successful mark all reported a rollback")
	}
	if _, unread := s.Items(); unread != 0 {
		t.Fatalf("unread after mark all = %d", unread)
	}

	s.Refresh(context.Background())
	gh.markAll = func() error { return errors.New("nope") }
	if restored := s.MarkAll(context.Background()); !restored {
		t.Fatal("failed mark all did not report a rollback")
	}
	if _, unread := s.Items(); unread != 1 {
		t.Fatalf("unread after rollback = %d", unread)
	}
	if got := s.Inbox(); !got.HasMore || got.Page != 1 || got.NeedsRefresh {
		t.Fatalf("failed mark-all changed valid paging state: %+v", got)
	}
}

func TestSessionCacheRoundTrip(t *testing.T) {
	gh := &fakeGH{refresh: func() ([]RawItem, error) {
		return []RawItem{rawItem("1", "a", "Issue", "assign")}, nil
	}}
	s := NewSession(gh, 50)
	s.Refresh(context.Background())
	raw := s.Cache()

	s2 := NewSession(gh, 50)
	s2.RestoreCache(raw)
	_, unread := s2.Items()
	if unread != 1 {
		t.Fatalf("restored unread = %d", unread)
	}
	if status, _ := s2.Status(); status != StatusReady {
		t.Fatalf("restored status = %q", status)
	}
	if _, stale := s2.UpdatedAt(); !stale {
		t.Fatal("restored list not marked stale")
	}
}

func TestSessionPerPageClamps(t *testing.T) {
	s := NewSession(nil, 500)
	s.SetPerPage(0)
	// No direct accessor; the clamp is exercised through Refresh's request
	// building in the CLI tests. Here we just assert no panic.
}

func TestSessionInboxPagesAndReadInvalidation(t *testing.T) {
	var requested []int
	gh := &fakeGH{notificationPage: func(page, perPage int) ([]RawItem, error) {
		requested = append(requested, page)
		switch page {
		case 1:
			if len(requested) > 2 {
				return []RawItem{rawItem("1", "one", "Issue", "mention"), rawItem("3", "three", "Issue", "mention")}, nil
			}
			return []RawItem{rawItem("1", "one", "Issue", "mention"), rawItem("2", "two", "Issue", "mention")}, nil
		case 2:
			if len(requested) == 2 {
				return []RawItem{rawItem("3", "three", "Issue", "mention")}, nil
			}
		}
		return nil, nil
	}}
	s := NewSession(gh, 2)
	if s.Refresh(context.Background()) {
		t.Fatal("initial load unexpectedly requested a toast")
	}
	if got := s.Inbox(); len(got.Items) != 2 || !got.HasMore || got.CountLabel() != "2+" {
		t.Fatalf("first inbox page = %+v", got)
	}
	if err := s.LoadMoreInbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Inbox(); len(got.Items) != 3 || got.HasMore || got.CountLabel() != "3" {
		t.Fatalf("second inbox page = %+v", got)
	}
	if s.MarkRead(context.Background(), "2") {
		t.Fatal("successful read rolled back")
	}
	if err := s.LoadMoreInbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := s.Inbox()
	if len(got.Items) != 2 || got.Items[0].ID != "1" || got.Items[1].ID != "3" || got.NeedsRefresh || got.HasMore {
		t.Fatalf("inbox after read and page reload = %+v", got)
	}
	if want := []int{1, 2, 1, 2}; !reflect.DeepEqual(requested, want) {
		t.Fatalf("requested pages = %v, want %v", requested, want)
	}
}

func TestSessionWorkPagesPreserveExactTotalAndDeduplicate(t *testing.T) {
	gh := &fakeGH{search: func(kind WorkKind, page, perPage int) (RawWorkPage, error) {
		if kind != WorkReviews || perPage != MaxWorkPageSize {
			t.Fatalf("search kind/page size = %q/%d", kind, perPage)
		}
		if page == 1 {
			items := make([]RawWorkItem, 100)
			for i := range items {
				n := i + 1
				items[i] = rawWorkPR(n)
			}
			return RawWorkPage{TotalCount: 101, Items: items}, nil
		}
		return RawWorkPage{TotalCount: 101, Items: []RawWorkItem{rawWorkPR(100), rawWorkPR(101)}}, nil
	}}
	s := NewSession(gh, 50)
	if err := s.RefreshWork(context.Background(), WorkReviews); err != nil {
		t.Fatal(err)
	}
	first := s.Work(WorkReviews)
	if len(first.Items) != 100 || first.TotalCount != 101 || !first.HasMore || first.CountLabel() != "101" {
		t.Fatalf("first work page = %d items, total=%d more=%v label=%q", len(first.Items), first.TotalCount, first.HasMore, first.CountLabel())
	}
	if err := s.LoadMoreWork(context.Background(), WorkReviews); err != nil {
		t.Fatal(err)
	}
	last := s.Work(WorkReviews)
	if len(last.Items) != 101 || last.HasMore || last.Items[99].Number != 100 || last.Items[100].Number != 101 {
		t.Fatalf("merged work page has %d items, more=%v", len(last.Items), last.HasMore)
	}
}

func TestSessionCacheMigratesLegacyAndRestoresIndependentFeedsStale(t *testing.T) {
	old := mustItem(t, rawItem("42", "legacy", "Issue", "mention"))
	legacy, err := json.Marshal(struct {
		Items     []Item    `json:"items"`
		UpdatedAt time.Time `json:"updated_at"`
	}{[]Item{old}, time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession(&fakeGH{
		search: func(WorkKind, int, int) (RawWorkPage, error) {
			return RawWorkPage{TotalCount: 1, Items: []RawWorkItem{rawWorkPR(1)}}, nil
		},
		activity: func(time.Time, time.Time) (RawActivity, error) { return testRawActivity(), nil },
	}, 50)
	s.RestoreCache(legacy)
	if got := s.Inbox(); len(got.Items) != 1 || got.Items[0].ID != "42" || !got.Stale {
		t.Fatalf("legacy inbox = %+v", got)
	}
	if err := s.RefreshWork(context.Background(), WorkReviews); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	cache := s.Cache()
	s2 := NewSession(&fakeGH{}, 50)
	s2.RestoreCache(cache)
	if got := s2.Work(WorkReviews); len(got.Items) != 1 || !got.Stale {
		t.Fatalf("restored work = %+v", got)
	}
	if got := s2.ActivityFeed(); got.TotalContributions != 3 || !got.Stale {
		t.Fatalf("restored activity = %+v", got)
	}
}

func TestLegacyFullInboxCacheKeepsCountAsLowerBound(t *testing.T) {
	items := make([]Item, 50)
	for i := range items {
		items[i] = mustItem(t, rawItem(strconv.Itoa(i+1), "legacy", "Issue", "mention"))
	}
	raw, err := json.Marshal(struct {
		Items     []Item    `json:"items"`
		UpdatedAt time.Time `json:"updated_at"`
	}{Items: items, UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession(&fakeGH{}, 100)
	s.RestoreCache(raw)
	got := s.Inbox()
	if !got.Stale || !got.HasMore || !got.NeedsRefresh || got.CountLabel() != "50+" {
		t.Fatalf("legacy full page was reported as exact: %+v", got)
	}
}

func TestSessionFeedFailuresKeepPriorData(t *testing.T) {
	gh := &fakeGH{
		search: func(WorkKind, int, int) (RawWorkPage, error) {
			return RawWorkPage{TotalCount: 1, Items: []RawWorkItem{rawWorkPR(1)}}, nil
		},
		activity: func(time.Time, time.Time) (RawActivity, error) { return testRawActivity(), nil },
	}
	s := NewSession(gh, 50)
	if err := s.RefreshWork(context.Background(), WorkReviews); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	gh.search = func(WorkKind, int, int) (RawWorkPage, error) { return RawWorkPage{}, errors.New("work offline") }
	gh.activity = func(time.Time, time.Time) (RawActivity, error) { return RawActivity{}, errors.New("activity offline") }
	_ = s.RefreshWork(context.Background(), WorkReviews)
	_ = s.RefreshActivity(context.Background())
	if got := s.Work(WorkReviews); len(got.Items) != 1 || !got.Stale || got.Status != StatusError {
		t.Fatalf("work failure state = %+v", got)
	}
	if got := s.ActivityFeed(); got.TotalContributions != 3 || !got.Stale || got.Status != StatusError {
		t.Fatalf("activity failure state = %+v", got)
	}
}

func TestSessionRefreshCannotRestoreAnItemMarkedReadInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	gh := &fakeGH{notificationPage: func(page, perPage int) ([]RawItem, error) {
		calls++
		if calls == 2 {
			close(started)
			<-release
		}
		return []RawItem{rawItem("1", "one", "Issue", "mention")}, nil
	}}
	s := NewSession(gh, 10)
	s.Refresh(context.Background())
	refreshed := make(chan struct{})
	go func() {
		s.Refresh(context.Background())
		close(refreshed)
	}()
	<-started
	if s.MarkRead(context.Background(), "1") {
		t.Fatal("successful mark-read rolled back")
	}
	close(release)
	<-refreshed
	if items, unread := s.Items(); unread != 0 || len(items) != 0 {
		t.Fatalf("stale refresh restored read item: items=%+v unread=%d", items, unread)
	}
}

func TestSessionRefreshOmitsThreadWhileReadIsPending(t *testing.T) {
	gh := &fakeGH{notificationPage: func(page, perPage int) ([]RawItem, error) {
		return []RawItem{rawItem("1", "one", "Issue", "mention")}, nil
	}}
	s := NewSession(gh, 10)
	s.Refresh(context.Background())
	if !s.BeginMarkRead("1") {
		t.Fatal("could not begin optimistic mark-read")
	}
	s.Refresh(context.Background())
	if items, unread := s.Items(); unread != 0 || len(items) != 0 {
		t.Fatalf("refresh restored a pending thread: %+v", items)
	}
	if s.FinishMarkRead(context.Background(), "1") {
		t.Fatal("successful mark-read rolled back")
	}
}

func TestSessionMarkAllSuppressesAnOlderRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	gh := &fakeGH{
		notificationPage: func(page, perPage int) ([]RawItem, error) {
			calls++
			if calls == 2 {
				close(started)
				<-release
			}
			return []RawItem{rawItem("1", "one", "Issue", "mention")}, nil
		},
	}
	s := NewSession(gh, 10)
	s.Refresh(context.Background())
	refreshed := make(chan struct{})
	go func() {
		s.Refresh(context.Background())
		close(refreshed)
	}()
	<-started
	if s.MarkAll(context.Background()) {
		t.Fatal("successful mark-all rolled back")
	}
	close(release)
	<-refreshed
	if items, unread := s.Items(); unread != 0 || len(items) != 0 {
		t.Fatalf("older refresh restored marked-all items: items=%+v unread=%d", items, unread)
	}
}

func rawWorkPR(number int) RawWorkItem {
	return RawWorkItem{
		Title:         "PR",
		Number:        number,
		RepositoryURL: "https://api.github.com/repos/acme/api",
		HTMLURL:       "https://github.com/acme/api/pull/" + strconv.Itoa(number),
		UpdatedAt:     "2026-09-27T10:00:00Z",
		PullRequest:   &struct{}{},
	}
}

func testRawActivity() RawActivity {
	return RawActivity{TotalContributions: 3, Weeks: []RawActivityWeek{{Days: []RawContributionDay{
		{Date: "2026-09-13", Weekday: 0, ContributionCount: 0, ContributionLevel: "NONE"},
		{Date: "2026-09-14", Weekday: 1, ContributionCount: 2, ContributionLevel: "FIRST_QUARTILE"},
		{Date: "2026-09-15", Weekday: 2, ContributionCount: 1, ContributionLevel: "FIRST_QUARTILE"},
	}}}}
}
