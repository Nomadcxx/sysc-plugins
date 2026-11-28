package store

import "github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"

// InsertCharge returns ErrExists when the subscription already has a charge
// of that kind on that date.
func (t *Tx) InsertCharge(c subs.Charge) error {
	_, err := t.tx.Exec(`INSERT INTO charges(id, subscription_id, date, amount_minor, currency, aud_minor,
		fx_rate, fx_date, kind) VALUES (?,?,?,?,?,?,?,?,?)`,
		c.ID, c.SubscriptionID, c.Date.String(), c.Amount.Minor, string(c.Amount.Currency), c.AUDMinor,
		c.FXRate, fxDate(c.FXDate), string(c.Kind))
	if isUnique(err) {
		return ErrExists
	}
	return err
}

func fxDate(d subs.Date) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}

// ListCharges returns charges dated on or after since, oldest first.
func (t *Tx) ListCharges(since subs.Date) ([]subs.Charge, error) {
	return t.queryCharges(`WHERE date >= ? ORDER BY date, id`, since.String())
}

// ChargesFor returns every charge of one subscription, oldest first.
func (t *Tx) ChargesFor(subID string) ([]subs.Charge, error) {
	return t.queryCharges(`WHERE subscription_id = ? ORDER BY date, id`, subID)
}

func (t *Tx) queryCharges(where string, args ...any) ([]subs.Charge, error) {
	rows, err := t.tx.Query(`SELECT id, subscription_id, date, amount_minor, currency, aud_minor, fx_rate,
		fx_date, kind FROM charges `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subs.Charge
	for rows.Next() {
		var c subs.Charge
		var date, currency, fx, kind string
		if err := rows.Scan(&c.ID, &c.SubscriptionID, &date, &c.Amount.Minor, &currency, &c.AUDMinor,
			&c.FXRate, &fx, &kind); err != nil {
			return nil, err
		}
		if c.Date, err = subs.ParseDate(date); err != nil {
			return nil, err
		}
		if fx != "" {
			if c.FXDate, err = subs.ParseDate(fx); err != nil {
				return nil, err
			}
		}
		c.Amount.Currency, c.Kind = subs.Currency(currency), subs.ChargeKind(kind)
		out = append(out, c)
	}
	return out, rows.Err()
}

// HasCharge reports whether any charge exists for subID on d.
func (t *Tx) HasCharge(subID string, d subs.Date) (bool, error) {
	var n int
	err := t.tx.QueryRow(`SELECT count(*) FROM charges WHERE subscription_id=? AND date=?`, subID, d.String()).Scan(&n)
	return n > 0, err
}
