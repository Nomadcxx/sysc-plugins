package google

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/remind"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// Event is the desired all-day event for one subscription.
type Event struct {
	SubscriptionID, Date, Summary, Description string
	Reminders                                  []string
	Hash                                       string
}

const (
	maxReminders = 5
	// Google caps reminders at four weeks.
	maxReminderMinutes = 40320
	// alarmHour: all-day reminder offsets count back from midnight, so N days
	// before at 09:00 is N×24−9 hours.
	alarmHour = 9
	footer    = "Managed by bitsub — edits here are overwritten."
)

// DesiredEvents is one event per live subscription on its next target, or
// on the cancel-by date when the owner is cancelling.
func DesiredEvents(list []subs.Subscription, rates subs.Rates, today subs.Date) []Event {
	var out []Event
	for _, s := range list {
		if s.Status != subs.Active || s.DeletedAt != nil {
			continue
		}
		it := subs.ItemOf(s, rates, today)
		e := Event{SubscriptionID: s.ID, Date: it.Target.String()}
		switch {
		case s.Intent == subs.Cancel:
			e.Date = it.CancelBy.String()
			e.Summary = fmt.Sprintf("Cancel %s by %s", s.Name, it.CancelBy.Short())
		case it.TargetKind == subs.TargetTrialEnd:
			e.Summary = s.Name + " trial ends · " + it.Display()
		default:
			e.Summary = s.Name + " renews · " + it.Display()
		}
		desc := []string{"Intent: " + string(s.Intent), "Price: " + it.Display(), "Cancel by: " + it.CancelBy.Short()}
		if s.PaymentLabel != "" {
			desc = append(desc, "Payment: "+s.PaymentLabel)
		}
		if s.URL != "" {
			desc = append(desc, "Account: "+s.URL)
		}
		e.Description = strings.Join(desc, "\n") + "\n\n" + footer
		e.Reminders = reminders(remind.Offsets(s, it.TargetKind))
		e.Hash = hashEvent(e)
		out = append(out, e)
	}
	return out
}

func reminders(offsets []int) []string {
	hours := []int{24 - alarmHour}
	for _, d := range offsets {
		h := d*24 - alarmHour
		if h > 0 && h*60 <= maxReminderMinutes && !slices.Contains(hours, h) {
			hours = append(hours, h)
		}
	}
	slices.SortFunc(hours, func(a, b int) int { return b - a })
	if len(hours) > maxReminders {
		hours = hours[:maxReminders]
	}
	out := make([]string, len(hours))
	for i, h := range hours {
		out[i] = fmt.Sprintf("popup:%dh", h)
	}
	return out
}

func hashEvent(e Event) string {
	h := sha256.New()
	for _, f := range append([]string{e.SubscriptionID, e.Date, e.Summary, e.Description}, e.Reminders...) {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type Result struct{ Created, Updated, Deleted int }

const (
	calendarMetaKey = "calendar_id"
	resyncEvery     = 15 * time.Minute
	backoffStart    = time.Minute
	backoffCap      = 30 * time.Minute
)

// Calendar owns one dedicated calendar and never writes to any other.
type Calendar struct {
	r    Runner
	st   *store.Store
	name string
	Now  func() time.Time
	Loc  *time.Location

	mu       sync.Mutex
	syncedAt *time.Time
	lastErr  string
	failures int
	next     time.Time
}

func NewCalendar(r Runner, st *store.Store, name string) *Calendar {
	return &Calendar{r: r, st: st, name: name, Now: time.Now, Loc: time.Local}
}

// Ensure returns the dedicated calendar's id, creating it on first use.
func (c *Calendar) Ensure(ctx context.Context) (string, error) {
	var id string
	err := c.st.Tx(ctx, func(tx *store.Tx) error {
		v, ok, err := tx.GetMeta(calendarMetaKey)
		if ok {
			id = v
		}
		return err
	})
	if err != nil || id != "" {
		return id, err
	}
	out, err := c.r.Run(ctx, "calendar", "create-calendar", c.name)
	if err != nil {
		return "", err
	}
	var resp struct {
		Calendar struct {
			ID string `json:"id"`
		} `json:"calendar"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.Calendar.ID == "" {
		return "", fmt.Errorf("gog calendar create-calendar: unexpected output: %.200s", out)
	}
	id = resp.Calendar.ID
	return id, c.st.Tx(ctx, func(tx *store.Tx) error { return tx.SetMeta(calendarMetaKey, id) })
}

type remoteEvent struct {
	ID       string `json:"id"`
	Extended struct {
		Private map[string]string `json:"private"`
	} `json:"extendedProperties"`
}

// Reconcile makes the calendar match desired. If listing fails it changes
// nothing; partial failures are reported and retried with backoff.
func (c *Calendar) Reconcile(ctx context.Context, desired []Event) (Result, error) {
	res, err := c.reconcile(ctx, desired)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.Now()
	if err != nil {
		c.failures++
		c.lastErr = err.Error()
		wait := backoffStart << min(c.failures-1, 10)
		c.next = now.Add(min(wait, backoffCap))
		return res, err
	}
	c.failures, c.lastErr, c.syncedAt, c.next = 0, "", &now, now.Add(resyncEvery)
	return res, nil
}

func (c *Calendar) reconcile(ctx context.Context, desired []Event) (Result, error) {
	var res Result
	calID, err := c.Ensure(ctx)
	if err != nil {
		return res, err
	}
	today := subs.Today(c.Now(), c.Loc)
	out, err := c.r.Run(ctx, "calendar", "events", calID, "--private-prop-filter", "bitsub=1",
		"--from", today.AddDays(-400).String(), "--to", subs.NthRenewal(today, subs.Year, 40, 1).String(),
		"--all-pages", "--max", "250")
	if err != nil {
		return res, err
	}
	var listed struct {
		Events []remoteEvent `json:"events"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return res, fmt.Errorf("gog calendar events: %w", err)
	}
	existing := map[string]remoteEvent{}
	var strays []remoteEvent
	for _, ev := range listed.Events {
		id := ev.Extended.Private["bitsub_id"]
		if id == "" || ev.Extended.Private["bitsub"] != "1" {
			continue // not ours: never touched
		}
		if _, dup := existing[id]; dup {
			strays = append(strays, ev)
			continue
		}
		existing[id] = ev
	}
	want := map[string]bool{}
	var errs []error
	for _, e := range desired {
		want[e.SubscriptionID] = true
		ev, found := existing[e.SubscriptionID]
		switch {
		case !found:
			out, err := c.r.Run(ctx, append([]string{"calendar", "create", calID}, eventArgs(e)...)...)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			var created struct {
				Event struct {
					ID string `json:"id"`
				} `json:"event"`
			}
			json.Unmarshal(out, &created)
			c.link(ctx, e, created.Event.ID)
			res.Created++
		case ev.Extended.Private["bitsub_hash"] != e.Hash:
			if _, err := c.r.Run(ctx, append([]string{"calendar", "update", calID, ev.ID}, eventArgs(e)...)...); err != nil {
				errs = append(errs, err)
				continue
			}
			c.link(ctx, e, ev.ID)
			res.Updated++
		}
	}
	for id, ev := range existing {
		if !want[id] {
			strays = append(strays, ev)
		}
	}
	for _, ev := range strays {
		if _, err := c.r.Run(ctx, "calendar", "delete", calID, ev.ID, "--force"); err != nil {
			errs = append(errs, err)
			continue
		}
		id := ev.Extended.Private["bitsub_id"]
		if !want[id] {
			c.st.Tx(ctx, func(tx *store.Tx) error { return tx.DeleteCalendarLink(id) })
		}
		res.Deleted++
	}
	return res, errors.Join(errs...)
}

func eventArgs(e Event) []string {
	d, _ := subs.ParseDate(e.Date)
	args := []string{"--summary", e.Summary, "--from", e.Date, "--to", d.AddDays(1).String(), "--all-day",
		"--description", e.Description}
	for _, r := range e.Reminders {
		args = append(args, "--reminder", r)
	}
	return append(args, "--private-prop", "bitsub=1", "--private-prop", "bitsub_id="+e.SubscriptionID,
		"--private-prop", "bitsub_hash="+e.Hash)
}

func (c *Calendar) link(ctx context.Context, e Event, eventID string) {
	c.st.Tx(ctx, func(tx *store.Tx) error {
		return tx.SetCalendarLink(store.CalendarLink{SubscriptionID: e.SubscriptionID, EventID: eventID, Hash: e.Hash, SyncedAt: c.Now()})
	})
}

// Status is the last successful sync time and the last error.
func (c *Calendar) Status() (syncedAt *time.Time, lastErr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.syncedAt, c.lastErr
}

// NextAttempt is when the next periodic reconcile is due (zero before the first).
func (c *Calendar) NextAttempt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.next
}
