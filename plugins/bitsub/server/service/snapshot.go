package service

import (
	"context"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// snapshotNotifications caps how many queued notifications one poll returns.
const snapshotNotifications = 50

// Snapshot is everything the plugin renders, plus notifications after since.
func (s *Service) Snapshot(ctx context.Context, since int64) (subs.Snapshot, error) {
	today := s.Today()
	rates, _ := s.currentRates()
	snap := subs.Snapshot{ServedBy: s.servedBy, GeneratedAt: s.now().UTC()}
	if s.ch != nil {
		snap.Channels = s.ch.Channels()
	}
	err := s.st.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if snap.Items, err = s.items(tx, Filter{}); err != nil {
			return err
		}
		list, err := tx.ListSubscriptions(false)
		if err != nil {
			return err
		}
		snap.Totals = subs.ComputeTotals(list, rates, today)

		first := subs.Date{Year: today.Year, Month: today.Month, Day: 1}
		charges, err := tx.ListCharges(subs.NthRenewal(first, subs.Month, 1, -11))
		if err != nil {
			return err
		}
		for i, c := range charges {
			if c.FXRate == 0 {
				charges[i].AUDMinor, _ = rates.ToAUD(c.Amount)
			}
		}
		snap.Series = subs.MonthlySeries(charges, today)

		batch, err := tx.NotificationsAfter(since, snapshotNotifications)
		if err != nil {
			return err
		}
		active := map[string]bool{}
		for _, it := range snap.Items {
			active[it.ID] = true
		}
		snap.Notifications = []subs.Notification{}
		for _, n := range batch {
			if active[n.SubscriptionID] {
				snap.Notifications = append(snap.Notifications, n)
			}
		}
		// A full batch means more are waiting: point LastSeq at the batch end
		// so the next poll continues from there.
		if len(batch) == snapshotNotifications {
			snap.LastSeq = batch[len(batch)-1].Seq
			return nil
		}
		snap.LastSeq, err = tx.LastSeq()
		return err
	})
	if snap.Items == nil {
		snap.Items = []subs.Item{}
	}
	return snap, err
}
