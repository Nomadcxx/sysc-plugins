// Package fx fetches daily ECB reference rates from Frankfurter and caches
// the last good set in the store's meta table.
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

const (
	DefaultURL   = "https://api.frankfurter.dev/v1/latest?base=AUD"
	metaKey      = "fx_rates"
	fetchTimeout = 10 * time.Second
	maxBody      = 64 << 10
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Rates struct {
	st  *store.Store
	do  Doer
	url string
	now func() time.Time
	loc *time.Location

	mu      sync.Mutex
	current subs.Rates
	have    bool
	lastErr string
}

// New loads the cached set from st; url "" means DefaultURL.
func New(st *store.Store, do Doer, url string, now func() time.Time, loc *time.Location) *Rates {
	if url == "" {
		url = DefaultURL
	}
	r := &Rates{st: st, do: do, url: url, now: now, loc: loc}
	st.Tx(context.Background(), func(tx *store.Tx) error {
		v, ok, err := tx.GetMeta(metaKey)
		if err != nil || !ok {
			return err
		}
		var cached subs.Rates
		if json.Unmarshal([]byte(v), &cached) == nil && len(cached.PerAUD) > 0 {
			r.current, r.have = cached, true
		}
		return nil
	})
	return r
}

// Refresh fetches the latest rates, keeping the previous set on any failure.
func (r *Rates) Refresh(ctx context.Context) error {
	got, err := r.fetch(ctx)
	if err == nil {
		var b []byte
		b, err = json.Marshal(got)
		if err == nil {
			err = r.st.Tx(ctx, func(tx *store.Tx) error { return tx.SetMeta(metaKey, string(b)) })
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.lastErr = err.Error()
		return err
	}
	r.current, r.have, r.lastErr = got, true, ""
	return nil
}

func (r *Rates) fetch(ctx context.Context) (subs.Rates, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return subs.Rates{}, err
	}
	resp, err := r.do.Do(req)
	if err != nil {
		return subs.Rates{}, fmt.Errorf("exchange rates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return subs.Rates{}, fmt.Errorf("exchange rates: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return subs.Rates{}, fmt.Errorf("exchange rates: %w", err)
	}
	if len(body) > maxBody {
		return subs.Rates{}, errors.New("exchange rates: response too large")
	}
	var wire struct {
		Base  string                    `json:"base"`
		Date  string                    `json:"date"`
		Rates map[subs.Currency]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return subs.Rates{}, fmt.Errorf("exchange rates: %w", err)
	}
	if wire.Base != string(subs.AUD) {
		return subs.Rates{}, fmt.Errorf("exchange rates: base %q, want AUD", wire.Base)
	}
	date, err := subs.ParseDate(wire.Date)
	if err != nil {
		return subs.Rates{}, fmt.Errorf("exchange rates: %w", err)
	}
	if len(wire.Rates) == 0 {
		return subs.Rates{}, errors.New("exchange rates: no rates")
	}
	for c, v := range wire.Rates {
		if _, err := subs.ParseCurrency(string(c)); err != nil || !(v > 0) {
			return subs.Rates{}, fmt.Errorf("exchange rates: bad rate %s=%v", c, v)
		}
	}
	return subs.Rates{Date: date, PerAUD: wire.Rates}, nil
}

// Current satisfies service.RatesSource.
func (r *Rates) Current() (subs.Rates, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current, r.have
}

// Status reports the rate date, whether it is stale (or missing) and the
// last fetch error.
func (r *Rates) Status() (date subs.Date, stale bool, lastErr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Date, subs.RatesStale(r.current, subs.Today(r.now(), r.loc)), r.lastErr
}
