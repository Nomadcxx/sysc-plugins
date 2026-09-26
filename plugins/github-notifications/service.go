package githubnotifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Status is the coarse state of one feed.
type Status string

const (
	StatusReady   Status = "ready"
	StatusLoading Status = "loading"
	StatusError   Status = "error"
	StatusAuth    Status = "auth_error"
	StatusRate    Status = "rate_limited"
)

// InboxSnapshot is the current unread page set. HasMore makes CountLabel an
// honest lower bound until GitHub returns a short page.
type InboxSnapshot struct {
	Items        []Item    `json:"items"`
	Page         int       `json:"page"`
	PerPage      int       `json:"per_page"`
	HasMore      bool      `json:"has_more"`
	NeedsRefresh bool      `json:"needs_refresh,omitempty"`
	Loading      bool      `json:"loading"`
	Stale        bool      `json:"stale"`
	Status       Status    `json:"status"`
	Error        string    `json:"error,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

func (s InboxSnapshot) CountLabel() string {
	count := len(s.Items)
	if s.HasMore {
		return fmt.Sprintf("%d+", count)
	}
	return fmt.Sprint(count)
}

// WorkSnapshot is one independently refreshed and paged Search API feed.
type WorkSnapshot struct {
	Items        []WorkItem `json:"items"`
	TotalCount   int        `json:"total_count"`
	Page         int        `json:"page"`
	PerPage      int        `json:"per_page"`
	HasMore      bool       `json:"has_more"`
	LimitReached bool       `json:"limit_reached,omitempty"`
	Loading      bool       `json:"loading"`
	Stale        bool       `json:"stale"`
	Status       Status     `json:"status"`
	Error        string     `json:"error,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at,omitempty"`
}

func (s WorkSnapshot) CountLabel() string {
	if s.LimitReached {
		return fmt.Sprintf("%d+", MaxWorkResults)
	}
	return fmt.Sprint(s.TotalCount)
}

// ActivitySnapshot stores normalized contribution data and its own freshness.
type ActivitySnapshot struct {
	Activity
	Loading   bool      `json:"loading"`
	Stale     bool      `json:"stale"`
	Status    Status    `json:"status"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type cache struct {
	// These fields preserve the version 0.2.0 cache format.
	Items     []Item                    `json:"items"`
	UpdatedAt time.Time                 `json:"updated_at"`
	Inbox     *InboxSnapshot            `json:"inbox,omitempty"`
	Work      map[WorkKind]WorkSnapshot `json:"work,omitempty"`
	Activity  *ActivitySnapshot         `json:"activity,omitempty"`
}

type pendingRead struct {
	item         Item
	index        int
	page         int
	hasMore      bool
	needsRefresh bool
	stale        bool
	lastUnread   int
}

// Session owns normalized feed state. Coordinator serializes live operations;
// the mutex also keeps snapshots and optimistic actions safe for readers.
type Session struct {
	mu sync.Mutex
	gh GH

	inbox    InboxSnapshot
	work     map[WorkKind]WorkSnapshot
	activity ActivitySnapshot

	perPage        int
	lastUnread     int
	inboxRevision  uint64
	pendingReads   map[string]pendingRead
	markAllPending bool
	pendingAll     *InboxSnapshot
}

func NewSession(gh GH, perPage int) *Session {
	return &Session{
		gh: gh, perPage: clamp(perPage, 1, 100),
		inbox: InboxSnapshot{Status: StatusLoading},
		work: map[WorkKind]WorkSnapshot{
			WorkReviews: {Status: StatusLoading},
			WorkMyPRs:   {Status: StatusLoading},
			WorkIssues:  {Status: StatusLoading},
		},
		activity:     ActivitySnapshot{Status: StatusLoading},
		pendingReads: make(map[string]pendingRead),
	}
}

func clamp(n, low, high int) int {
	if n < low {
		return low
	}
	if n > high {
		return high
	}
	return n
}

func (s *Session) SetPerPage(n int) {
	s.mu.Lock()
	s.perPage = clamp(n, 1, 100)
	s.inbox.PerPage = s.perPage
	s.mu.Unlock()
}

func (s *Session) Items() ([]Item, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneItems(s.inbox.Items), len(s.inbox.Items)
}

func (s *Session) Unread() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inbox.Items)
}

func (s *Session) Status() (Status, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inbox.Status, s.inbox.Error
}

func (s *Session) UpdatedAt() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inbox.UpdatedAt, s.inbox.Stale
}

func (s *Session) Inbox() InboxSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.inbox
	out.Items = cloneItems(s.inbox.Items)
	return out
}

func (s *Session) Work(kind WorkKind) WorkSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.work[kind]
	out.Items = append([]WorkItem(nil), out.Items...)
	return out
}

func (s *Session) ActivityFeed() ActivitySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.activity
	out.Weeks = append([]ActivityWeek(nil), out.Weeks...)
	return out
}

// Refresh replaces the inbox from page one and reports whether the unread
// count increased after a previous successful load.
func (s *Session) Refresh(ctx context.Context) (crossedToUnread bool) {
	s.mu.Lock()
	pageSize := s.perPage
	revision := s.inboxRevision
	s.inbox.Loading = true
	s.inbox.Status = StatusLoading
	s.inbox.Error = ""
	s.mu.Unlock()

	if s.gh == nil {
		s.inboxFailure(errors.New("GitHub client is unavailable"))
		return false
	}
	items, err := s.gh.Notifications(ctx, 1, pageSize)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbox.Loading = false
	if err != nil {
		s.inbox.Status = classify(err)
		s.inbox.Error = err.Error()
		s.inbox.Stale = len(s.inbox.Items) > 0
		return false
	}
	// A read action that began while the request was in flight owns the newer
	// inbox state. Discard this response so it cannot put read threads back.
	if revision != s.inboxRevision || s.markAllPending {
		return false
	}
	normalized := NormalizeList(items)
	normalized = s.withoutPendingReads(normalized)
	grew := len(normalized) > s.lastUnread && s.lastUnread > 0
	s.inbox.Items = normalized
	s.inbox.Page = 1
	s.inbox.PerPage = pageSize
	s.inbox.HasMore = len(items) == pageSize
	s.inbox.NeedsRefresh = false
	s.inbox.Status = StatusReady
	s.inbox.Error = ""
	s.inbox.UpdatedAt = time.Now()
	s.inbox.Stale = false
	s.lastUnread = len(normalized)
	return grew
}

func (s *Session) LoadMoreInbox(ctx context.Context) error {
	s.mu.Lock()
	if s.inbox.Loading {
		s.mu.Unlock()
		return nil
	}
	if !s.inbox.HasMore && !s.inbox.NeedsRefresh {
		s.mu.Unlock()
		return nil
	}
	pageSize := s.perPage
	needsRefresh := s.inbox.NeedsRefresh
	page := s.inbox.Page + 1
	revision := s.inboxRevision
	s.inbox.Loading = true
	s.inbox.Status = StatusLoading
	s.inbox.Error = ""
	s.mu.Unlock()

	if needsRefresh {
		items, err := s.gh.Notifications(ctx, 1, pageSize)
		if err != nil {
			return s.inboxFailure(err)
		}
		s.mu.Lock()
		if revision != s.inboxRevision || s.markAllPending {
			s.inbox.Loading = false
			s.mu.Unlock()
			return nil
		}
		s.inbox.Items = s.withoutPendingReads(NormalizeList(items))
		s.inbox.Page = 1
		s.inbox.HasMore = len(items) == pageSize
		s.inbox.NeedsRefresh = false
		s.inbox.UpdatedAt = time.Now()
		s.inbox.Stale = false
		s.lastUnread = len(s.inbox.Items)
		if !s.inbox.HasMore {
			s.inbox.Loading = false
			s.inbox.Status = StatusReady
			s.mu.Unlock()
			return nil
		}
		page = 2
		s.mu.Unlock()
	}

	items, err := s.gh.Notifications(ctx, page, pageSize)
	if err != nil {
		return s.inboxFailure(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision != s.inboxRevision || s.markAllPending {
		s.inbox.Loading = false
		return nil
	}
	s.inbox.Items = appendUniqueItems(s.inbox.Items, s.withoutPendingReads(NormalizeList(items)))
	s.inbox.Page = page
	s.inbox.PerPage = pageSize
	s.inbox.HasMore = len(items) == pageSize
	s.inbox.Loading = false
	s.inbox.Status = StatusReady
	s.inbox.Error = ""
	s.inbox.UpdatedAt = time.Now()
	s.inbox.Stale = false
	s.inbox.NeedsRefresh = false
	s.lastUnread = len(s.inbox.Items)
	return nil
}

func (s *Session) inboxFailure(err error) error {
	s.mu.Lock()
	s.inbox.Loading = false
	s.inbox.Status = classify(err)
	s.inbox.Error = err.Error()
	s.inbox.Stale = len(s.inbox.Items) > 0
	s.mu.Unlock()
	return err
}

func (s *Session) withoutPendingReads(items []Item) []Item {
	if len(s.pendingReads) == 0 {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if _, pending := s.pendingReads[item.ID]; !pending {
			out = append(out, item)
		}
	}
	return out
}

func (s *Session) RefreshWork(ctx context.Context, kind WorkKind) error {
	if _, ok := workQueries[kind]; !ok {
		return fmt.Errorf("unknown work kind %q", kind)
	}
	s.mu.Lock()
	current := s.work[kind]
	current.Loading = true
	current.Status = StatusLoading
	current.Error = ""
	s.work[kind] = current
	s.mu.Unlock()

	if s.gh == nil {
		return s.workFailure(kind, errors.New("GitHub client is unavailable"))
	}
	raw, err := s.gh.Search(ctx, kind, 1, MaxWorkPageSize)
	if err != nil {
		return s.workFailure(kind, err)
	}
	page, err := NormalizeWorkPage(raw, kind, 1, MaxWorkPageSize, time.Now())
	if err != nil {
		return s.workFailure(kind, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.work[kind] = WorkSnapshot{
		Items: page.Items, TotalCount: page.TotalCount, Page: page.Page, PerPage: page.PerPage,
		HasMore: page.HasMore, LimitReached: page.TotalCount > MaxWorkResults,
		Status: StatusReady, UpdatedAt: time.Now(),
	}
	return nil
}

func (s *Session) LoadMoreWork(ctx context.Context, kind WorkKind) error {
	s.mu.Lock()
	current, ok := s.work[kind]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown work kind %q", kind)
	}
	if current.Loading || !current.HasMore {
		s.mu.Unlock()
		return nil
	}
	page := current.Page + 1
	current.Loading = true
	current.Status = StatusLoading
	current.Error = ""
	s.work[kind] = current
	s.mu.Unlock()

	raw, err := s.gh.Search(ctx, kind, page, MaxWorkPageSize)
	if err != nil {
		return s.workFailure(kind, err)
	}
	next, err := NormalizeWorkPage(raw, kind, page, MaxWorkPageSize, time.Now())
	if err != nil {
		return s.workFailure(kind, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current = s.work[kind]
	current.Items = appendUniqueWorkItems(current.Items, next.Items)
	current.TotalCount = next.TotalCount
	current.Page = page
	current.PerPage = next.PerPage
	current.HasMore = next.HasMore
	current.LimitReached = next.TotalCount > MaxWorkResults
	current.Loading = false
	current.Stale = false
	current.Status = StatusReady
	current.Error = ""
	current.UpdatedAt = time.Now()
	s.work[kind] = current
	return nil
}

func (s *Session) workFailure(kind WorkKind, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.work[kind]
	current.Loading = false
	current.Status = classify(err)
	current.Error = err.Error()
	current.Stale = len(current.Items) > 0
	s.work[kind] = current
	return err
}

func (s *Session) RefreshActivity(ctx context.Context) error {
	s.mu.Lock()
	s.activity.Loading = true
	s.activity.Status = StatusLoading
	s.activity.Error = ""
	s.mu.Unlock()
	if s.gh == nil {
		return s.activityFailure(errors.New("GitHub client is unavailable"))
	}
	to := time.Now().UTC()
	from := to.AddDate(-1, 0, 0)
	raw, err := s.gh.Activity(ctx, from, to)
	if err != nil {
		return s.activityFailure(err)
	}
	activity, err := NormalizeActivity(raw)
	if err != nil {
		return s.activityFailure(err)
	}
	s.mu.Lock()
	s.activity = ActivitySnapshot{Activity: activity, Status: StatusReady, UpdatedAt: time.Now()}
	s.mu.Unlock()
	return nil
}

func (s *Session) activityFailure(err error) error {
	s.mu.Lock()
	s.activity.Loading = false
	s.activity.Status = classify(err)
	s.activity.Error = err.Error()
	s.activity.Stale = len(s.activity.Weeks) > 0
	s.mu.Unlock()
	return err
}

// MarkRead removes a thread optimistically and rolls it back if GitHub rejects
// the action. The mutation revision protects the result from older refreshes.
func (s *Session) MarkRead(ctx context.Context, id string) (restored bool) {
	if !s.BeginMarkRead(id) {
		return false
	}
	return s.FinishMarkRead(ctx, id)
}

// BeginMarkRead applies the optimistic local change before a serialized GitHub
// operation is queued, retaining the page state needed for rollback.
func (s *Session) BeginMarkRead(id string) bool {
	if !ValidThreadID(id) || s.gh == nil {
		return false
	}
	s.mu.Lock()
	index := -1
	for i := range s.inbox.Items {
		if s.inbox.Items[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		return false
	}
	target := s.inbox.Items[index]
	s.inbox.Items = append(s.inbox.Items[:index], s.inbox.Items[index+1:]...)
	s.pendingReads[id] = pendingRead{item: target, index: index, page: s.inbox.Page, hasMore: s.inbox.HasMore,
		needsRefresh: s.inbox.NeedsRefresh, stale: s.inbox.Stale, lastUnread: s.lastUnread}
	s.inboxRevision++
	s.inbox.NeedsRefresh = true
	s.inbox.Loading = false
	s.inbox.Status = StatusReady
	s.inbox.Error = ""
	s.mu.Unlock()
	return true
}

func (s *Session) FinishMarkRead(ctx context.Context, id string) (restored bool) {
	if err := s.gh.MarkRead(ctx, id); err != nil {
		s.mu.Lock()
		if before, ok := s.pendingReads[id]; ok {
			s.inbox.Items = insertItem(s.inbox.Items, before.index, before.item)
			s.inbox.Page = before.page
			s.inbox.HasMore = before.hasMore
			s.inbox.NeedsRefresh = before.needsRefresh
			s.inbox.Stale = before.stale
			s.lastUnread = before.lastUnread
			delete(s.pendingReads, id)
		}
		s.inboxRevision++
		s.inbox.Status = classify(err)
		s.inbox.Error = err.Error()
		s.mu.Unlock()
		return true
	}
	s.mu.Lock()
	delete(s.pendingReads, id)
	s.inboxRevision++
	s.lastUnread = len(s.inbox.Items)
	s.inbox.Status = StatusReady
	s.inbox.Error = ""
	s.mu.Unlock()
	return false
}

func (s *Session) MarkAll(ctx context.Context) (restored bool) {
	if !s.BeginMarkAll() {
		return false
	}
	return s.FinishMarkAll(ctx)
}

func (s *Session) BeginMarkAll() bool {
	if s.gh == nil {
		return false
	}
	s.mu.Lock()
	if len(s.inbox.Items) == 0 {
		s.mu.Unlock()
		return false
	}
	previous := cloneItems(s.inbox.Items)
	before := s.inbox
	before.Items = previous
	s.pendingAll = &before
	s.inbox.Items = nil
	s.inbox.HasMore = false
	s.inbox.NeedsRefresh = true
	s.markAllPending = true
	s.inboxRevision++
	s.inbox.Loading = false
	s.inbox.Status = StatusReady
	s.inbox.Error = ""
	s.mu.Unlock()
	return true
}

func (s *Session) FinishMarkAll(ctx context.Context) (restored bool) {
	if err := s.gh.MarkAll(ctx); err != nil {
		s.mu.Lock()
		if s.markAllPending && s.pendingAll != nil {
			s.inbox = *s.pendingAll
			s.inbox.Items = cloneItems(s.pendingAll.Items)
			s.pendingAll = nil
			s.markAllPending = false
		}
		s.inboxRevision++
		s.inbox.Status = classify(err)
		s.inbox.Error = err.Error()
		s.mu.Unlock()
		return true
	}
	s.mu.Lock()
	s.markAllPending = false
	s.pendingAll = nil
	s.inboxRevision++
	s.lastUnread = 0
	s.inbox.UpdatedAt = time.Now()
	s.inbox.Status = StatusReady
	s.inbox.Error = ""
	s.mu.Unlock()
	return false
}

func (s *Session) Cache() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	inbox := s.inbox
	inbox.Items = cloneItems(inbox.Items)
	work := make(map[WorkKind]WorkSnapshot, len(s.work))
	for kind, snapshot := range s.work {
		snapshot.Items = append([]WorkItem(nil), snapshot.Items...)
		work[kind] = snapshot
	}
	activity := s.activity
	activity.Weeks = append([]ActivityWeek(nil), activity.Weeks...)
	raw, _ := json.Marshal(cache{Items: cloneItems(s.inbox.Items), UpdatedAt: s.inbox.UpdatedAt, Inbox: &inbox, Work: work, Activity: &activity})
	return raw
}

func (s *Session) RestoreCache(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var restored cache
	if err := json.Unmarshal(raw, &restored); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	legacyInbox := restored.Inbox == nil
	if !legacyInbox {
		s.inbox = *restored.Inbox
		s.inbox.Items = cloneItems(s.inbox.Items)
	} else {
		s.inbox = InboxSnapshot{Items: cloneItems(restored.Items), PerPage: s.perPage, UpdatedAt: restored.UpdatedAt}
		// Version 0.2 cached only a configured first page, capped at 50 rows.
		// A full legacy page cannot prove the unread total was exact.
		if len(s.inbox.Items) >= 50 {
			s.inbox.Page = 1
			s.inbox.PerPage = 50
			s.inbox.HasMore = true
			s.inbox.NeedsRefresh = true
		}
	}
	s.inbox.Stale = true
	s.inbox.Loading = false
	s.inbox.Status = StatusReady
	s.inbox.Error = ""
	s.lastUnread = len(s.inbox.Items)
	if restored.Work != nil {
		for kind, snapshot := range restored.Work {
			if _, valid := workQueries[kind]; !valid {
				continue
			}
			snapshot.Items = append([]WorkItem(nil), snapshot.Items...)
			snapshot.Stale = true
			snapshot.Loading = false
			snapshot.Status = StatusReady
			snapshot.Error = ""
			s.work[kind] = snapshot
		}
	}
	if restored.Activity != nil {
		s.activity = *restored.Activity
		s.activity.Weeks = append([]ActivityWeek(nil), restored.Activity.Weeks...)
		s.activity.Stale = true
		s.activity.Loading = false
		s.activity.Status = StatusReady
		s.activity.Error = ""
	}
}

func cloneItems(items []Item) []Item { return append([]Item(nil), items...) }

func appendUniqueItems(dst, source []Item) []Item {
	seen := make(map[string]bool, len(dst)+len(source))
	for _, item := range dst {
		seen[item.ID] = true
	}
	for _, item := range source {
		if item.ID != "" && !seen[item.ID] {
			dst = append(dst, item)
			seen[item.ID] = true
		}
	}
	return dst
}

func appendUniqueWorkItems(dst, source []WorkItem) []WorkItem {
	seen := make(map[string]bool, len(dst)+len(source))
	for _, item := range dst {
		seen[item.URL] = true
	}
	for _, item := range source {
		if item.URL != "" && !seen[item.URL] {
			dst = append(dst, item)
			seen[item.URL] = true
		}
	}
	return dst
}

func insertItem(items []Item, at int, item Item) []Item {
	if at < 0 || at > len(items) {
		at = len(items)
	}
	items = append(items, Item{})
	copy(items[at+1:], items[at:])
	items[at] = item
	return items
}

func classify(err error) Status {
	if err == nil {
		return StatusReady
	}
	switch Classify(err.Error()) {
	case ErrRateLimited:
		return StatusRate
	case ErrAuth:
		return StatusAuth
	default:
		return StatusError
	}
}
