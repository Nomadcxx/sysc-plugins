package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (t *Tx) GetMeta(key string) (string, bool, error) {
	var v string
	err := t.tx.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (t *Tx) SetMeta(key, value string) error {
	_, err := t.tx.Exec(`INSERT INTO meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// Audit records one mutation; before and after are stored as JSON.
func (t *Tx) Audit(actor, action, subID string, before, after any) error {
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(`INSERT INTO audit(at, actor, action, subscription_id, before, after) VALUES (?,?,?,?,?,?)`,
		formatTime(time.Now()), actor, action, subID, string(b), string(a))
	return err
}

type AuditRow struct {
	At                            time.Time
	Actor, Action, SubscriptionID string
	Before, After                 string
}

// ListAudit returns audit rows for subID (all rows when empty), oldest first.
func (t *Tx) ListAudit(subID string) ([]AuditRow, error) {
	rows, err := t.tx.Query(`SELECT at, actor, action, subscription_id, before, after FROM audit
		WHERE ? = '' OR subscription_id = ? ORDER BY id`, subID, subID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var at string
		if err := rows.Scan(&at, &r.Actor, &r.Action, &r.SubscriptionID, &r.Before, &r.After); err != nil {
			return nil, err
		}
		if r.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type CalendarLink struct {
	SubscriptionID, EventID, Hash string
	SyncedAt                      time.Time
}

func (t *Tx) CalendarLinks() (map[string]CalendarLink, error) {
	rows, err := t.tx.Query(`SELECT subscription_id, event_id, hash, synced_at FROM calendar_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]CalendarLink{}
	for rows.Next() {
		var l CalendarLink
		var at string
		if err := rows.Scan(&l.SubscriptionID, &l.EventID, &l.Hash, &at); err != nil {
			return nil, err
		}
		if l.SyncedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out[l.SubscriptionID] = l
	}
	return out, rows.Err()
}

func (t *Tx) SetCalendarLink(l CalendarLink) error {
	_, err := t.tx.Exec(`INSERT INTO calendar_links(subscription_id, event_id, hash, synced_at) VALUES (?,?,?,?)
		ON CONFLICT(subscription_id) DO UPDATE SET event_id=excluded.event_id, hash=excluded.hash,
		synced_at=excluded.synced_at`, l.SubscriptionID, l.EventID, l.Hash, formatTime(l.SyncedAt))
	return err
}

func (t *Tx) DeleteCalendarLink(subID string) error {
	_, err := t.tx.Exec(`DELETE FROM calendar_links WHERE subscription_id=?`, subID)
	return err
}
