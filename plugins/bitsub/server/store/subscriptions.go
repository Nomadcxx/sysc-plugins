package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

const subColumns = `id, name, url, amount_minor, currency, cycle, interval, anchor_date, trial_end,
	intent, status, notice_days, access_until, category, payment_label, notes, reminder_days,
	created_by, created_at, updated_at, deleted_at`

func subArgs(s subs.Subscription) []any {
	days, _ := json.Marshal(s.ReminderDays)
	return []any{s.ID, s.Name, s.URL, s.Price.Minor, string(s.Price.Currency), string(s.Cycle), s.Interval,
		s.Anchor.String(), nullDate(s.TrialEnd), string(s.Intent), string(s.Status), s.NoticeDays,
		nullDate(s.AccessUntil), s.Category, s.PaymentLabel, s.Notes, string(days), s.CreatedBy,
		formatTime(s.CreatedAt), formatTime(s.UpdatedAt), nullTime(s.DeletedAt), NormName(s.Name)}
}

func (t *Tx) InsertSubscription(s subs.Subscription) error {
	_, err := t.tx.Exec(`INSERT INTO subscriptions(`+subColumns+`, norm_name)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, subArgs(s)...)
	if isUnique(err) {
		return ErrExists
	}
	return err
}

func (t *Tx) UpdateSubscription(s subs.Subscription) error {
	args := subArgs(s)
	res, err := t.tx.Exec(`UPDATE subscriptions SET name=?, url=?, amount_minor=?, currency=?, cycle=?,
		interval=?, anchor_date=?, trial_end=?, intent=?, status=?, notice_days=?, access_until=?,
		category=?, payment_label=?, notes=?, reminder_days=?, created_by=?, created_at=?, updated_at=?,
		deleted_at=?, norm_name=? WHERE id=?`, append(args[1:], args[0])...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (t *Tx) GetSubscription(id string) (subs.Subscription, error) {
	s, err := scanSub(t.tx.QueryRow(`SELECT `+subColumns+` FROM subscriptions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// FindByNormName returns the non-deleted subscriptions whose normalised
// name equals norm.
func (t *Tx) FindByNormName(norm string) ([]subs.Subscription, error) {
	return t.querySubs(`SELECT `+subColumns+` FROM subscriptions
		WHERE norm_name=? AND deleted_at IS NULL ORDER BY created_at`, norm)
}

func (t *Tx) ListSubscriptions(includeDeleted bool) ([]subs.Subscription, error) {
	q := `SELECT ` + subColumns + ` FROM subscriptions`
	if !includeDeleted {
		q += ` WHERE deleted_at IS NULL`
	}
	return t.querySubs(q + ` ORDER BY name`)
}

func (t *Tx) querySubs(q string, args ...any) ([]subs.Subscription, error) {
	rows, err := t.tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subs.Subscription
	for rows.Next() {
		s, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanSub(r scanner) (subs.Subscription, error) {
	var (
		s                       subs.Subscription
		currency, cycle, anchor string
		intent, status, days    string
		created, updated        string
		trial, until, deleted   sql.NullString
	)
	err := r.Scan(&s.ID, &s.Name, &s.URL, &s.Price.Minor, &currency, &cycle, &s.Interval, &anchor, &trial,
		&intent, &status, &s.NoticeDays, &until, &s.Category, &s.PaymentLabel, &s.Notes, &days,
		&s.CreatedBy, &created, &updated, &deleted)
	if err != nil {
		return s, err
	}
	s.Price.Currency, s.Cycle = subs.Currency(currency), subs.Cycle(cycle)
	s.Intent, s.Status = subs.Intent(intent), subs.Status(status)
	if s.Anchor, err = subs.ParseDate(anchor); err != nil {
		return s, err
	}
	if s.TrialEnd, err = parseNullDate(trial); err != nil {
		return s, err
	}
	if s.AccessUntil, err = parseNullDate(until); err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(days), &s.ReminderDays); err != nil {
		return s, err
	}
	if s.CreatedAt, err = parseTime(created); err != nil {
		return s, err
	}
	if s.UpdatedAt, err = parseTime(updated); err != nil {
		return s, err
	}
	if deleted.Valid {
		d, err := parseTime(deleted.String)
		if err != nil {
			return s, err
		}
		s.DeletedAt = &d
	}
	return s, nil
}

func nullDate(d *subs.Date) any {
	if d == nil {
		return nil
	}
	return d.String()
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseNullDate(ns sql.NullString) (*subs.Date, error) {
	if !ns.Valid {
		return nil, nil
	}
	d, err := subs.ParseDate(ns.String)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
