package store

import (
	"encoding/json"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// InsertNotification queues n once per dedupKey. A repeat returns the
// existing seq with inserted=false.
func (t *Tx) InsertNotification(dedupKey string, n subs.Notification) (seq int64, inserted bool, err error) {
	actions, err := json.Marshal(n.Actions)
	if err != nil {
		return 0, false, err
	}
	res, err := t.tx.Exec(`INSERT INTO notifications(dedup_key, subscription_id, summary, body, urgency, actions, issued_at)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(dedup_key) DO NOTHING`,
		dedupKey, n.SubscriptionID, n.Summary, n.Body, n.Urgency, string(actions), formatTime(n.IssuedAt))
	if err != nil {
		return 0, false, err
	}
	if rows, _ := res.RowsAffected(); rows == 1 {
		seq, err = res.LastInsertId()
		return seq, true, err
	}
	err = t.tx.QueryRow(`SELECT seq FROM notifications WHERE dedup_key=?`, dedupKey).Scan(&seq)
	return seq, false, err
}

// NotificationsAfter returns up to limit notifications with seq > after, in seq order.
func (t *Tx) NotificationsAfter(after int64, limit int) ([]subs.Notification, error) {
	rows, err := t.tx.Query(`SELECT seq, subscription_id, summary, body, urgency, actions, issued_at
		FROM notifications WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subs.Notification
	for rows.Next() {
		var n subs.Notification
		var actions, issued string
		if err := rows.Scan(&n.Seq, &n.SubscriptionID, &n.Summary, &n.Body, &n.Urgency, &actions, &issued); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(actions), &n.Actions); err != nil {
			return nil, err
		}
		if n.IssuedAt, err = parseTime(issued); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (t *Tx) LastSeq() (int64, error) {
	var seq int64
	err := t.tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM notifications`).Scan(&seq)
	return seq, err
}
