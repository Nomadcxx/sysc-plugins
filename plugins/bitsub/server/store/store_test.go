package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var ctx = context.Background()

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "bitsub.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func tx(t *testing.T, st *Store, fn func(*Tx) error) {
	t.Helper()
	if err := st.Tx(ctx, fn); err != nil {
		t.Fatal(err)
	}
}

func sample(id, name string) subs.Subscription {
	now := time.Date(2026, 11, 29, 3, 4, 5, 0, time.UTC)
	return subs.Subscription{
		ID: id, Name: name, Price: subs.Money{Minor: 1899, Currency: "AUD"},
		Cycle: subs.Month, Interval: 1, Anchor: subs.MustDate("2026-12-14"),
		Intent: subs.Undecided, Status: subs.Active, CreatedBy: "test", CreatedAt: now, UpdatedAt: now,
	}
}

func TestOpenAppliesMigrationsOnceAndIsIdempotent(t *testing.T) {
	st, path := openTemp(t)
	version := func(st *Store) string {
		var v string
		tx(t, st, func(tx *Tx) error {
			var ok bool
			var err error
			v, ok, err = tx.GetMeta("schema_version")
			if !ok {
				t.Fatal("no schema_version")
			}
			return err
		})
		return v
	}
	if v := version(st); v != "1" {
		t.Fatalf("schema_version %q", v)
	}
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if v := version(st2); v != "1" {
		t.Fatalf("reopen schema_version %q", v)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("db mode %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
}

func TestSubscriptionRoundTrip(t *testing.T) {
	st, _ := openTemp(t)
	trial := subs.MustDate("2026-12-02")
	until := subs.MustDate("2027-01-01")
	deleted := time.Date(2026, 11, 30, 1, 0, 0, 0, time.UTC)
	full := sample("a", "Speechify Premium")
	full.URL = "https://speechify.com/account"
	full.Price = subs.Money{Minor: 13900, Currency: "USD"}
	full.Cycle, full.Interval = subs.Year, 2
	full.TrialEnd, full.AccessUntil, full.DeletedAt = &trial, &until, &deleted
	full.Intent, full.Status, full.NoticeDays = subs.Cancel, subs.Cancelled, 3
	full.Category, full.PaymentLabel, full.Notes = "AI", "Visa ··21", "note"
	full.ReminderDays = []int{14, 3, 1}
	bare := sample("b", "Netflix")

	tx(t, st, func(tx *Tx) error {
		if err := tx.InsertSubscription(full); err != nil {
			return err
		}
		return tx.InsertSubscription(bare)
	})
	tx(t, st, func(tx *Tx) error {
		for _, want := range []subs.Subscription{full, bare} {
			got, err := tx.GetSubscription(want.ID)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
			}
		}
		return nil
	})

	bare.Name, bare.Intent = "Netflix Premium", subs.Keep
	tx(t, st, func(tx *Tx) error { return tx.UpdateSubscription(bare) })
	tx(t, st, func(tx *Tx) error {
		got, err := tx.GetSubscription("b")
		if got.Name != "Netflix Premium" || got.Intent != subs.Keep {
			t.Errorf("update lost: %+v", got)
		}
		if _, err := tx.GetSubscription("missing"); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing: %v", err)
		}
		all, _ := tx.ListSubscriptions(false)
		withDeleted, _ := tx.ListSubscriptions(true)
		if len(all) != 1 || len(withDeleted) != 2 {
			t.Errorf("list %d / %d", len(all), len(withDeleted))
		}
		return err
	})
	if err := st.Tx(ctx, func(tx *Tx) error { return tx.UpdateSubscription(sample("nope", "x")) }); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
}

func TestSoftDeletedHiddenFromFindByNormName(t *testing.T) {
	st, _ := openTemp(t)
	gone := sample("old", "Netflix")
	now := time.Now().UTC()
	gone.DeletedAt = &now
	tx(t, st, func(tx *Tx) error {
		if err := tx.InsertSubscription(gone); err != nil {
			return err
		}
		return tx.InsertSubscription(sample("new", "  NETFLIX  "))
	})
	tx(t, st, func(tx *Tx) error {
		got, err := tx.FindByNormName(NormName("netflix"))
		if len(got) != 1 || got[0].ID != "new" {
			t.Errorf("FindByNormName = %+v", got)
		}
		return err
	})
	if NormName("  Netflix \t Premium ") != "netflix premium" {
		t.Error(NormName("  Netflix \t Premium "))
	}
}

func TestChargeUniquePerDay(t *testing.T) {
	st, _ := openTemp(t)
	c := subs.Charge{ID: "c1", SubscriptionID: "a", Date: subs.MustDate("2026-12-14"),
		Amount: subs.Money{Minor: 2000, Currency: "USD"}, AUDMinor: 3101, FXRate: 0.645,
		FXDate: subs.MustDate("2026-12-13"), Kind: subs.Assumed}
	tx(t, st, func(tx *Tx) error {
		if err := tx.InsertSubscription(sample("a", "Synthetic")); err != nil {
			return err
		}
		return tx.InsertCharge(c)
	})
	c.ID = "c2"
	if err := st.Tx(ctx, func(tx *Tx) error { return tx.InsertCharge(c) }); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate charge: %v", err)
	}
	c.ID, c.Kind = "c3", subs.Recorded
	tx(t, st, func(tx *Tx) error { return tx.InsertCharge(c) })
	tx(t, st, func(tx *Tx) error {
		list, err := tx.ListCharges(subs.MustDate("2026-01-01"))
		if len(list) != 2 || list[0].AUDMinor != 3101 || list[0].FXDate.String() != "2026-12-13" {
			t.Errorf("charges %+v", list)
		}
		has, _ := tx.HasCharge("a", subs.MustDate("2026-12-14"))
		hasNot, _ := tx.HasCharge("a", subs.MustDate("2026-12-15"))
		if !has || hasNot {
			t.Errorf("HasCharge %v %v", has, hasNot)
		}
		later, _ := tx.ListCharges(subs.MustDate("2026-12-15"))
		if len(later) != 0 {
			t.Errorf("since filter %+v", later)
		}
		return err
	})
}

func TestNotificationDedup(t *testing.T) {
	st, _ := openTemp(t)
	n := subs.Notification{SubscriptionID: "a", Summary: "s", Body: "b", Urgency: "critical",
		Actions: []subs.Action{{Key: "keep:a", Label: "Keep"}}, IssuedAt: time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)}
	var first, second int64
	tx(t, st, func(tx *Tx) error {
		seq, inserted, err := tx.InsertNotification("trial:2026-12-09:7:a", n)
		if !inserted || seq == 0 {
			t.Errorf("first insert %d %v", seq, inserted)
		}
		first = seq
		return err
	})
	tx(t, st, func(tx *Tx) error {
		seq, inserted, err := tx.InsertNotification("trial:2026-12-09:7:a", n)
		if inserted || seq != first {
			t.Errorf("dup insert %d %v", seq, inserted)
		}
		second, _, _ = tx.InsertNotification("trial:2026-12-09:2:a", n)
		return err
	})
	tx(t, st, func(tx *Tx) error {
		after, err := tx.NotificationsAfter(first, 50)
		if len(after) != 1 || after[0].Seq != second {
			t.Errorf("after %+v", after)
		}
		all, _ := tx.NotificationsAfter(0, 50)
		if len(all) != 2 || !reflect.DeepEqual(all[0].Actions, n.Actions) || !all[0].IssuedAt.Equal(n.IssuedAt) {
			t.Errorf("all %+v", all)
		}
		last, _ := tx.LastSeq()
		if last != second {
			t.Errorf("LastSeq %d", last)
		}
		return err
	})
}

func TestTxRollsBackOnError(t *testing.T) {
	st, _ := openTemp(t)
	boom := errors.New("boom")
	err := st.Tx(ctx, func(tx *Tx) error {
		if err := tx.InsertSubscription(sample("a", "Netflix")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	tx(t, st, func(tx *Tx) error {
		if _, err := tx.GetSubscription("a"); !errors.Is(err, ErrNotFound) {
			t.Errorf("survived rollback: %v", err)
		}
		return nil
	})
}

func TestAuditAndMetaAndCalendarLinks(t *testing.T) {
	st, _ := openTemp(t)
	tx(t, st, func(tx *Tx) error {
		if err := tx.Audit("laptop-plugin", "set_intent", "a", map[string]string{"intent": "undecided"}, map[string]string{"intent": "keep"}); err != nil {
			return err
		}
		if err := tx.SetMeta("calendar_id", "x"); err != nil {
			return err
		}
		if err := tx.SetMeta("calendar_id", "y"); err != nil {
			return err
		}
		return tx.SetCalendarLink(CalendarLink{SubscriptionID: "a", EventID: "ev1", Hash: "h", SyncedAt: time.Unix(10, 0).UTC()})
	})
	tx(t, st, func(tx *Tx) error {
		rows, err := tx.ListAudit("a")
		if len(rows) != 1 || rows[0].Actor != "laptop-plugin" || rows[0].After != `{"intent":"keep"}` {
			t.Errorf("audit %+v", rows)
		}
		v, ok, _ := tx.GetMeta("calendar_id")
		if !ok || v != "y" {
			t.Errorf("meta %q %v", v, ok)
		}
		if _, ok, _ := tx.GetMeta("absent"); ok {
			t.Error("absent meta found")
		}
		links, _ := tx.CalendarLinks()
		if links["a"].EventID != "ev1" {
			t.Errorf("links %+v", links)
		}
		return err
	})
	tx(t, st, func(tx *Tx) error { return tx.DeleteCalendarLink("a") })
	tx(t, st, func(tx *Tx) error {
		links, err := tx.CalendarLinks()
		if len(links) != 0 {
			t.Errorf("link not deleted %+v", links)
		}
		return err
	})
}

func TestBackupDailyKeepsFourteen(t *testing.T) {
	st, _ := openTemp(t)
	tx(t, st, func(tx *Tx) error { return tx.InsertSubscription(sample("a", "Netflix")) })
	dir := filepath.Join(t.TempDir(), "backups")
	day := subs.MustDate("2026-11-01")
	for i := 0; i < 16; i++ {
		if err := st.BackupDaily(dir, day.AddDays(i), 14); err != nil {
			t.Fatal(err)
		}
	}
	// A second run on the same day is a no-op.
	if err := st.BackupDaily(dir, day.AddDays(15), 14); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 14 || !slices.Contains(names, "bitsub-20261116.db") || slices.Contains(names, "bitsub-20261102.db") {
		t.Fatalf("backups %v", names)
	}
	backup, err := Open(filepath.Join(dir, "bitsub-20261116.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := backup.Tx(ctx, func(tx *Tx) error { _, err := tx.GetSubscription("a"); return err }); err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
}
