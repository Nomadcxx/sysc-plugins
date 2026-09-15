package githubnotifications

import (
	"context"
	"errors"
	"testing"
)

type fakeGH struct {
	refresh    func() ([]RawItem, error)
	markRead   func(id string) error
	markAll    func() error
	markCalled int
}

func (f *fakeGH) Refresh(ctx context.Context, perPage int) ([]RawItem, error) {
	if f.refresh == nil {
		return nil, nil
	}
	return f.refresh()
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

func TestSessionMarkAllOptimisticWithRollback(t *testing.T) {
	gh := &fakeGH{refresh: func() ([]RawItem, error) {
		return []RawItem{rawItem("1", "a", "Issue", "assign")}, nil
	}}
	s := NewSession(gh, 50)
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
