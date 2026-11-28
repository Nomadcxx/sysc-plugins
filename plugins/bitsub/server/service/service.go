// Package service is the one set of operations behind both of bitsubd's
// front doors (MCP tools and the plugin JSON API), so validation and
// behaviour cannot drift between them.
package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var ErrNotFound = store.ErrNotFound

// RatesSource reports the current FX rates; false until the first fetch succeeds.
type RatesSource interface {
	Current() (subs.Rates, bool)
}

// ChannelSource reports calendar, email and FX status for the snapshot.
type ChannelSource interface {
	Channels() subs.Channels
}

type Service struct {
	st       *store.Store
	now      func() time.Time
	loc      *time.Location
	rates    RatesSource
	ch       ChannelSource
	servedBy string
	changed  chan struct{}
}

func New(st *store.Store, now func() time.Time, loc *time.Location, rates RatesSource, ch ChannelSource, servedBy string) *Service {
	return &Service{st: st, now: now, loc: loc, rates: rates, ch: ch, servedBy: servedBy, changed: make(chan struct{}, 1)}
}

// SetChannels attaches the channel source after construction; bitsubd builds
// the calendar and mailer from the service, so it can't pass them to New.
func (s *Service) SetChannels(ch ChannelSource) { s.ch = ch }

// Changed receives after successful mutations (coalesced, never blocks writers).
func (s *Service) Changed() <-chan struct{} { return s.changed }

func (s *Service) signal() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *Service) Today() subs.Date { return subs.Today(s.now(), s.loc) }

func (s *Service) currentRates() (subs.Rates, bool) {
	if s.rates == nil {
		return subs.Rates{}, false
	}
	return s.rates.Current()
}

type AddInput struct {
	Name, URL, Amount, Currency string
	Cycle                       subs.Cycle
	Interval                    int
	AnchorDate, TrialEnd        string
	Intent                      subs.Intent
	NoticeDays                  int
	Category, PaymentLabel      string
	Notes                       string
	ReminderDays                []int
	AllowDuplicate              bool
}

// UpdateInput changes only the non-nil fields.
type UpdateInput struct {
	Name, URL, Amount, Currency *string
	Cycle                       *subs.Cycle
	Interval                    *int
	AnchorDate, TrialEnd        *string
	Intent                      *subs.Intent
	NoticeDays                  *int
	Category, PaymentLabel      *string
	Notes                       *string
	ReminderDays                *[]int
	AllowDuplicate              bool
	ClearTrialEnd               bool
}

type Filter struct {
	Status         subs.Status // "" means active
	Intent         subs.Intent
	Text           string
	IncludeDeleted bool
}

func (s *Service) Add(ctx context.Context, actor string, in AddInput) (subs.Subscription, error) {
	today := s.Today()
	rates, known := s.currentRates()
	var sub subs.Subscription
	var err error
	if sub.Name, err = validName(in.Name); err != nil {
		return sub, err
	}
	cur, err := validCurrency(in.Currency, rates, known)
	if err != nil {
		return sub, err
	}
	if sub.Price, err = validAmount(in.Amount, cur); err != nil {
		return sub, err
	}
	if err := validCycle(in.Cycle); err != nil {
		return sub, err
	}
	sub.Cycle, sub.Interval = in.Cycle, in.Interval
	if sub.Interval == 0 {
		sub.Interval = 1
	}
	if err := validInterval(sub.Interval); err != nil {
		return sub, err
	}
	if in.TrialEnd != "" {
		d, err := validDate("trial_end", in.TrialEnd, today)
		if err != nil {
			return sub, err
		}
		sub.TrialEnd = &d
	}
	switch {
	case in.AnchorDate != "":
		if sub.Anchor, err = validDate("anchor_date", in.AnchorDate, today); err != nil {
			return sub, err
		}
	case sub.TrialEnd != nil:
		sub.Anchor = *sub.TrialEnd
	default:
		return sub, invalid("anchor_date", "set anchor_date (first real charge) or trial_end")
	}
	sub.Intent = in.Intent
	if sub.Intent == "" {
		sub.Intent = subs.Undecided
	}
	if err := validIntent(sub.Intent); err != nil {
		return sub, err
	}
	if err := validNoticeDays(in.NoticeDays); err != nil {
		return sub, err
	}
	sub.NoticeDays = in.NoticeDays
	if sub.ReminderDays, err = validReminderDays(in.ReminderDays); err != nil {
		return sub, err
	}
	if sub.URL, err = validURL(in.URL); err != nil {
		return sub, err
	}
	if sub.PaymentLabel, err = validPaymentLabel(in.PaymentLabel); err != nil {
		return sub, err
	}
	if sub.Category, err = validCategory(in.Category); err != nil {
		return sub, err
	}
	if sub.Notes, err = validNotes(in.Notes); err != nil {
		return sub, err
	}
	now := s.now().UTC()
	sub.ID, sub.Status, sub.CreatedBy, sub.CreatedAt, sub.UpdatedAt = uuid.NewString(), subs.Active, actor, now, now

	err = s.mutate(ctx, func(tx *store.Tx) error {
		if err := checkDuplicate(tx, sub.Name, sub.ID, in.AllowDuplicate); err != nil {
			return err
		}
		if err := tx.InsertSubscription(sub); err != nil {
			return err
		}
		if err := tx.SetMeta(intentAtKey(sub.ID), today.String()); err != nil {
			return err
		}
		return tx.Audit(actor, "add", sub.ID, nil, sub)
	})
	return sub, err
}

func checkDuplicate(tx *store.Tx, name, selfID string, allow bool) error {
	if allow {
		return nil
	}
	found, err := tx.FindByNormName(store.NormName(name))
	if err != nil {
		return err
	}
	for _, f := range found {
		if f.ID != selfID {
			return &DuplicateError{Existing: f}
		}
	}
	return nil
}

func intentAtKey(id string) string { return "intent_at:" + id }

// mutate runs fn in one transaction and signals Changed on success.
func (s *Service) mutate(ctx context.Context, fn func(*store.Tx) error) error {
	if err := s.st.Tx(ctx, fn); err != nil {
		return err
	}
	s.signal()
	return nil
}

func live(tx *store.Tx, id string) (subs.Subscription, error) {
	sub, err := tx.GetSubscription(id)
	if err == nil && sub.DeletedAt != nil {
		return sub, ErrNotFound
	}
	return sub, err
}

func (s *Service) Update(ctx context.Context, actor, id string, in UpdateInput) (subs.Subscription, error) {
	today := s.Today()
	rates, known := s.currentRates()
	var after subs.Subscription
	err := s.mutate(ctx, func(tx *store.Tx) error {
		before, err := live(tx, id)
		if err != nil {
			return err
		}
		sub := before
		if in.Name != nil {
			if sub.Name, err = validName(*in.Name); err != nil {
				return err
			}
			if err := checkDuplicate(tx, sub.Name, sub.ID, in.AllowDuplicate); err != nil {
				return err
			}
		}
		if in.Currency != nil || in.Amount != nil {
			cur := sub.Price.Currency
			if in.Currency != nil {
				if cur, err = validCurrency(*in.Currency, rates, known); err != nil {
					return err
				}
			}
			amount := formatPlain(sub.Price)
			if in.Amount != nil {
				amount = *in.Amount
			}
			if sub.Price, err = validAmount(amount, cur); err != nil {
				return err
			}
		}
		if in.Cycle != nil {
			if err := validCycle(*in.Cycle); err != nil {
				return err
			}
			sub.Cycle = *in.Cycle
		}
		if in.Interval != nil {
			if err := validInterval(*in.Interval); err != nil {
				return err
			}
			sub.Interval = *in.Interval
		}
		if in.AnchorDate != nil {
			if sub.Anchor, err = validDate("anchor_date", *in.AnchorDate, today); err != nil {
				return err
			}
		}
		if in.ClearTrialEnd {
			sub.TrialEnd = nil
		}
		if in.TrialEnd != nil {
			d, err := validDate("trial_end", *in.TrialEnd, today)
			if err != nil {
				return err
			}
			sub.TrialEnd = &d
		}
		if in.Intent != nil {
			if err := validIntent(*in.Intent); err != nil {
				return err
			}
			if *in.Intent != sub.Intent {
				if err := tx.SetMeta(intentAtKey(sub.ID), today.String()); err != nil {
					return err
				}
			}
			sub.Intent = *in.Intent
		}
		if in.NoticeDays != nil {
			if err := validNoticeDays(*in.NoticeDays); err != nil {
				return err
			}
			sub.NoticeDays = *in.NoticeDays
		}
		if in.ReminderDays != nil {
			if sub.ReminderDays, err = validReminderDays(*in.ReminderDays); err != nil {
				return err
			}
		}
		if in.URL != nil {
			if sub.URL, err = validURL(*in.URL); err != nil {
				return err
			}
		}
		if in.PaymentLabel != nil {
			if sub.PaymentLabel, err = validPaymentLabel(*in.PaymentLabel); err != nil {
				return err
			}
		}
		if in.Category != nil {
			if sub.Category, err = validCategory(*in.Category); err != nil {
				return err
			}
		}
		if in.Notes != nil {
			if sub.Notes, err = validNotes(*in.Notes); err != nil {
				return err
			}
		}
		sub.UpdatedAt = s.now().UTC()
		if err := tx.UpdateSubscription(sub); err != nil {
			return err
		}
		after = sub
		return tx.Audit(actor, "update", sub.ID, before, sub)
	})
	return after, err
}

// formatPlain renders m as a bare decimal ParseAmount accepts.
func formatPlain(m subs.Money) string {
	s := m.String()
	if i := strings.IndexAny(s, "0123456789"); i >= 0 {
		s = s[i:]
	}
	return strings.ReplaceAll(s, ",", "")
}

func (s *Service) SetIntent(ctx context.Context, actor, id string, intent subs.Intent) (subs.Subscription, error) {
	if err := validIntent(intent); err != nil {
		return subs.Subscription{}, err
	}
	var after subs.Subscription
	err := s.mutate(ctx, func(tx *store.Tx) error {
		sub, err := live(tx, id)
		if err != nil {
			return err
		}
		if sub.Status != subs.Active {
			return invalid("id", "subscription is already cancelled")
		}
		before := sub
		sub.Intent, sub.UpdatedAt = intent, s.now().UTC()
		if err := tx.UpdateSubscription(sub); err != nil {
			return err
		}
		if err := tx.SetMeta(intentAtKey(sub.ID), s.Today().String()); err != nil {
			return err
		}
		after = sub
		return tx.Audit(actor, "set_intent", sub.ID, before, sub)
	})
	return after, err
}

// ConfirmCancelled records that the owner has cancelled with the vendor.
func (s *Service) ConfirmCancelled(ctx context.Context, actor, id string, accessUntil string) (subs.Subscription, error) {
	var until *subs.Date
	if accessUntil != "" {
		d, err := validDate("access_until", accessUntil, s.Today())
		if err != nil {
			return subs.Subscription{}, err
		}
		until = &d
	}
	var after subs.Subscription
	err := s.mutate(ctx, func(tx *store.Tx) error {
		sub, err := live(tx, id)
		if err != nil {
			return err
		}
		before := sub
		sub.Status, sub.Intent, sub.AccessUntil, sub.UpdatedAt = subs.Cancelled, subs.Cancel, until, s.now().UTC()
		if err := tx.UpdateSubscription(sub); err != nil {
			return err
		}
		after = sub
		return tx.Audit(actor, "confirm_cancelled", sub.ID, before, sub)
	})
	return after, err
}

// RecordCharge confirms an actual charge; an empty amount means the list
// price and an empty currency the subscription's.
func (s *Service) RecordCharge(ctx context.Context, actor, id, date, amount, currency string) (subs.Charge, error) {
	today := s.Today()
	rates, known := s.currentRates()
	d, err := validDate("date", date, today)
	if err != nil {
		return subs.Charge{}, err
	}
	if d.After(today) {
		return subs.Charge{}, invalid("date", "cannot record a charge in the future")
	}
	var c subs.Charge
	err = s.mutate(ctx, func(tx *store.Tx) error {
		sub, err := live(tx, id)
		if err != nil {
			return err
		}
		price := sub.Price
		if currency != "" {
			cur, err := validCurrency(currency, rates, known)
			if err != nil {
				return err
			}
			if cur != price.Currency && amount == "" {
				return invalid("amount", "required when the currency differs from the list price")
			}
			price.Currency = cur
		}
		if amount != "" {
			if price, err = validAmount(amount, price.Currency); err != nil {
				return err
			}
		}
		c = chargeAt(sub.ID, d, price, rates, known, subs.Recorded)
		if err := tx.InsertCharge(c); errors.Is(err, store.ErrExists) {
			return invalid("date", "a charge is already recorded on %s", d)
		} else if err != nil {
			return err
		}
		return tx.Audit(actor, "record_charge", sub.ID, nil, c)
	})
	return c, err
}

// chargeAt locks the AUD value at the current rate. With no rate the AUD
// figure is left 0 with FXRate 0, and the snapshot fills it later.
func chargeAt(subID string, d subs.Date, m subs.Money, rates subs.Rates, known bool, kind subs.ChargeKind) subs.Charge {
	c := subs.Charge{ID: uuid.NewString(), SubscriptionID: subID, Date: d, Amount: m, Kind: kind}
	switch {
	case m.Currency == subs.AUD:
		c.AUDMinor, c.FXRate, c.FXDate = m.Minor, 1, d
	case known:
		if aud, ok := rates.ToAUD(m); ok {
			c.AUDMinor, c.FXRate, c.FXDate = aud, rates.PerAUD[m.Currency], rates.Date
		}
	}
	return c
}

func (s *Service) Get(ctx context.Context, idOrName string) (subs.Subscription, error) {
	var out subs.Subscription
	err := s.st.Tx(ctx, func(tx *store.Tx) error {
		sub, err := tx.GetSubscription(strings.TrimSpace(idOrName))
		if err == nil {
			out = sub
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		found, err := tx.FindByNormName(store.NormName(idOrName))
		if err != nil {
			return err
		}
		switch len(found) {
		case 0:
			return ErrNotFound
		case 1:
			out = found[0]
			return nil
		}
		return invalid("id_or_name", "%d subscriptions are named %q; use an id", len(found), found[0].Name)
	})
	return out, err
}

func (s *Service) List(ctx context.Context, f Filter) ([]subs.Item, error) {
	var items []subs.Item
	err := s.st.Tx(ctx, func(tx *store.Tx) error {
		var err error
		items, err = s.items(tx, f)
		return err
	})
	return items, err
}

func (s *Service) items(tx *store.Tx, f Filter) ([]subs.Item, error) {
	all, err := tx.ListSubscriptions(f.IncludeDeleted)
	if err != nil {
		return nil, err
	}
	status := f.Status
	if status == "" {
		status = subs.Active
	}
	text := store.NormName(f.Text)
	today := s.Today()
	rates, _ := s.currentRates()
	var out []subs.Item
	for _, sub := range all {
		if sub.Status != status || (f.Intent != "" && sub.Intent != f.Intent) {
			continue
		}
		if text != "" && !strings.Contains(store.NormName(sub.Name), text) && !strings.Contains(store.NormName(sub.Category), text) {
			continue
		}
		it := subs.ItemOf(sub, rates, today)
		if it.ProbablyCharged, err = s.probablyCharged(tx, sub); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c := out[i].Target.Compare(out[j].Target); c != 0 {
			return c < 0
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// probablyCharged: intent cancel and an assumed charge on or after the day
// the intent was last set.
func (s *Service) probablyCharged(tx *store.Tx, sub subs.Subscription) (bool, error) {
	if sub.Intent != subs.Cancel || sub.Status != subs.Active {
		return false, nil
	}
	since := subs.Today(sub.CreatedAt, s.loc)
	if v, ok, err := tx.GetMeta(intentAtKey(sub.ID)); err != nil {
		return false, err
	} else if ok {
		if d, err := subs.ParseDate(v); err == nil {
			since = d
		}
	}
	charges, err := tx.ChargesFor(sub.ID)
	if err != nil {
		return false, err
	}
	for _, c := range charges {
		if c.Kind == subs.Assumed && !c.Date.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

const maxUpcomingDays = 366

// Upcoming lists active items whose next target falls within days (default 30).
func (s *Service) Upcoming(ctx context.Context, days int) ([]subs.Item, error) {
	if days == 0 {
		days = 30
	}
	if days < 1 || days > maxUpcomingDays {
		return nil, invalid("days", "must be 1–%d", maxUpcomingDays)
	}
	items, err := s.List(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	through := s.Today().AddDays(days)
	var out []subs.Item
	for _, it := range items {
		if !it.Target.After(through) {
			out = append(out, it)
		}
	}
	return out, nil
}

func (s *Service) Totals(ctx context.Context) (subs.Totals, error) {
	var t subs.Totals
	err := s.st.Tx(ctx, func(tx *store.Tx) error {
		list, err := tx.ListSubscriptions(false)
		if err != nil {
			return err
		}
		rates, _ := s.currentRates()
		t = subs.ComputeTotals(list, rates, s.Today())
		return nil
	})
	return t, err
}

// Delete is a soft delete for mistakes, never for cancellation.
func (s *Service) Delete(ctx context.Context, actor, id string) error {
	return s.mutate(ctx, func(tx *store.Tx) error {
		sub, err := live(tx, id)
		if err != nil {
			return err
		}
		before := sub
		now := s.now().UTC()
		sub.DeletedAt, sub.UpdatedAt = &now, now
		if err := tx.UpdateSubscription(sub); err != nil {
			return err
		}
		return tx.Audit(actor, "delete", sub.ID, before, sub)
	})
}

func (s *Service) Restore(ctx context.Context, actor, id string) error {
	return s.mutate(ctx, func(tx *store.Tx) error {
		sub, err := tx.GetSubscription(id)
		if err != nil {
			return err
		}
		if sub.DeletedAt == nil {
			return nil
		}
		if err := checkDuplicate(tx, sub.Name, sub.ID, false); err != nil {
			return err
		}
		before := sub
		sub.DeletedAt, sub.UpdatedAt = nil, s.now().UTC()
		if err := tx.UpdateSubscription(sub); err != nil {
			return err
		}
		return tx.Audit(actor, "restore", sub.ID, before, sub)
	})
}
