package service

import (
	"context"
	"errors"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// Rollover records an assumed charge for every charge date that has passed
// since the last charge (the trial end counts as the first), then clears
// trial ends in the past. Rerunning it changes nothing.
func (s *Service) Rollover(ctx context.Context) (int, error) {
	today := s.Today()
	yesterday := today.AddDays(-1)
	rates, known := s.currentRates()
	inserted, changed := 0, false
	err := s.st.Tx(ctx, func(tx *store.Tx) error {
		list, err := tx.ListSubscriptions(false)
		if err != nil {
			return err
		}
		for _, sub := range list {
			if sub.Status != subs.Active {
				continue
			}
			history, err := tx.ChargesFor(sub.ID)
			if err != nil {
				return err
			}
			for _, d := range dueCharges(sub, history, yesterday) {
				err := tx.InsertCharge(chargeAt(sub.ID, d, sub.Price, rates, known, subs.Assumed))
				if errors.Is(err, store.ErrExists) {
					continue
				}
				if err != nil {
					return err
				}
				inserted++
			}
			if sub.TrialEnd != nil && sub.TrialEnd.Before(today) {
				before := sub
				sub.TrialEnd = nil
				if err := tx.UpdateSubscription(sub); err != nil {
					return err
				}
				if err := tx.Audit("bitsubd", "trial_ended", sub.ID, before, sub); err != nil {
					return err
				}
				changed = true
			}
		}
		return nil
	})
	if err == nil && (inserted > 0 || changed) {
		s.signal()
	}
	return inserted, err
}

// dueCharges lists charge dates in (last charge, through] that have no
// charge yet. Before any charge, the window opens at the trial end or anchor.
func dueCharges(sub subs.Subscription, history []subs.Charge, through subs.Date) []subs.Date {
	first := sub.Anchor
	if sub.TrialEnd != nil && sub.TrialEnd.Before(first) {
		first = *sub.TrialEnd
	}
	after := first.AddDays(-1)
	for _, c := range history {
		if c.Date.After(after) {
			after = c.Date
		}
	}
	var out []subs.Date
	if sub.TrialEnd != nil && sub.TrialEnd.After(after) && !sub.TrialEnd.After(through) {
		out = append(out, *sub.TrialEnd)
	}
	for _, d := range subs.RenewalsBetween(sub.Anchor, sub.Cycle, sub.Interval, after, through) {
		if sub.TrialEnd != nil && d.Before(*sub.TrialEnd) || (len(out) > 0 && d == out[0]) {
			continue
		}
		out = append(out, d)
	}
	return out
}
