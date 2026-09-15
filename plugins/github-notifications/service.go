package githubnotifications

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Status is the service's coarse state, shown in the tooltip and panel.
type Status string

const (
	StatusReady   Status = "ready"
	StatusLoading Status = "loading"
	StatusError   Status = "error"
	StatusAuth    Status = "auth_error"
	StatusRate    Status = "rate_limited"
)

// cache is the persisted snapshot restored across restarts.
type cache struct {
	Items     []Item    `json:"items"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Session holds the notification state. Mark-read is optimistic with rollback
// on failure, matching the original plugin.
type Session struct {
	mu         sync.Mutex
	gh         GH
	items      []Item
	status     Status
	errMsg     string
	updatedAt  time.Time
	stale      bool
	perPage    int
	lastUnread int
}

func NewSession(gh GH, perPage int) *Session {
	if perPage < 1 {
		perPage = 1
	}
	if perPage > 50 {
		perPage = 50
	}
	return &Session{gh: gh, perPage: perPage, status: StatusLoading}
}

func (s *Session) SetPerPage(n int) {
	if n < 1 {
		n = 1
	}
	if n > 50 {
		n = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.perPage = n
}

// Items returns a copy of the current list and the unread count.
func (s *Session) Items() ([]Item, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out, len(out)
}

// Unread is the count of items currently held; the gh notifications endpoint
// only returns unread threads, so count equals length.
func (s *Session) Unread() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *Session) Status() (Status, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.errMsg
}

// UpdatedAt reports the last successful refresh and whether the current list
// came from a restored cache.
func (s *Session) UpdatedAt() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updatedAt, s.stale
}

// Refresh fetches unread notifications. crossedToUnread reports that the
// unread count grew past the previous value, so the shell may toast once.
func (s *Session) Refresh(ctx context.Context) (crossedToUnread bool) {
	s.mu.Lock()
	s.status = StatusLoading
	s.mu.Unlock()

	raw, err := s.gh.Refresh(ctx, s.perPage)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status = classify(err)
		s.errMsg = err.Error()
		return false
	}
	items := NormalizeList(raw)
	grew := len(items) > s.lastUnread && s.lastUnread > 0
	s.lastUnread = len(items)
	s.items = items
	s.status = StatusReady
	s.errMsg = ""
	s.updatedAt = time.Now()
	s.stale = false
	return grew
}

// MarkRead removes a thread optimistically and restores it if gh fails.
func (s *Session) MarkRead(ctx context.Context, id string) (restored bool) {
	s.mu.Lock()
	var target Item
	found := false
	kept := s.items[:0:0]
	for _, it := range s.items {
		if it.ID == id {
			target = it
			found = true
			continue
		}
		kept = append(kept, it)
	}
	if !found {
		s.mu.Unlock()
		return false
	}
	s.items = kept
	s.mu.Unlock()

	if err := s.gh.MarkRead(ctx, id); err != nil {
		s.mu.Lock()
		s.items = append([]Item{target}, s.items...)
		s.status = classify(err)
		s.errMsg = err.Error()
		s.mu.Unlock()
		return true
	}
	s.mu.Lock()
	s.lastUnread = len(s.items)
	s.mu.Unlock()
	return false
}

// MarkAll clears the list optimistically and restores it if gh fails.
func (s *Session) MarkAll(ctx context.Context) (restored bool) {
	s.mu.Lock()
	if len(s.items) == 0 {
		s.mu.Unlock()
		return false
	}
	previous := make([]Item, len(s.items))
	copy(previous, s.items)
	s.items = nil
	s.mu.Unlock()

	if err := s.gh.MarkAll(ctx); err != nil {
		s.mu.Lock()
		s.items = previous
		s.status = classify(err)
		s.errMsg = err.Error()
		s.mu.Unlock()
		return true
	}
	s.mu.Lock()
	s.lastUnread = 0
	s.updatedAt = time.Now()
	s.mu.Unlock()
	return false
}

// Cache serializes the current list for the state store.
func (s *Session) Cache() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(cache{Items: s.items, UpdatedAt: s.updatedAt})
	return raw
}

// RestoreCache reloads a cached list after a restart; the list is marked
// stale until the first successful refresh.
func (s *Session) RestoreCache(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var c cache
	if err := json.Unmarshal(raw, &c); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = c.Items
	s.updatedAt = c.UpdatedAt
	s.stale = true
	s.status = StatusReady
	s.lastUnread = len(c.Items)
}

func classify(err error) Status {
	if err == nil {
		return StatusReady
	}
	msg := err.Error()
	if containsAny(msg, "rate limit", "rate-limit", "secondary rate") {
		return StatusRate
	}
	if containsAny(msg, "403", "permission", "scope", "sso", "auth", "token", "login", "401") {
		return StatusAuth
	}
	return StatusError
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
