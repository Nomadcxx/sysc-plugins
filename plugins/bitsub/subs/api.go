package subs

import (
	"strconv"
	"strings"
	"time"
)

// Wire types for GET /api/v1/snapshot, shared by bitsubd and the plugin.

type Item struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Price           Money      `json:"price"`
	PriceAUD        int64      `json:"price_aud"`
	PriceApprox     bool       `json:"price_approx"`
	Cycle           Cycle      `json:"cycle"`
	Interval        int        `json:"interval"`
	Target          Date       `json:"target"`
	TargetKind      TargetKind `json:"target_kind"`
	Intent          Intent     `json:"intent"`
	CancelBy        Date       `json:"cancel_by"`
	ProbablyCharged bool       `json:"probably_charged"`
}

// ItemOf builds the list row for s; the service fills ProbablyCharged.
func ItemOf(s Subscription, r Rates, today Date) Item {
	aud, _, approx := convert(s.Price, r, today)
	target, kind := s.Target(today)
	return Item{
		ID: s.ID, Name: s.Name, Price: s.Price, PriceAUD: aud, PriceApprox: approx,
		Cycle: s.Cycle, Interval: s.Interval, Target: target, TargetKind: kind,
		Intent: s.Intent, CancelBy: target.AddDays(-s.NoticeDays),
	}
}

// Display is the item's price as shown everywhere, e.g. "A$31.00 (US$20.00)/mo".
func (it Item) Display() string {
	return FormatAUDKnown(it.PriceAUD, it.Price, it.PriceApprox) + PriceSuffix(it.Cycle, it.Interval)
}

// FormatAUDKnown is FormatAUD, except that a non-AUD price with no AUD
// figure shows only the original instead of a misleading A$0.00.
func FormatAUDKnown(aud int64, orig Money, approx bool) string {
	if aud == 0 && orig.Currency != AUD && orig.Minor != 0 {
		return orig.String()
	}
	return FormatAUD(aud, orig, approx)
}

type Action struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// ParseActionKey splits "keep:<id>", "cancel:<id>" or "done:<id>".
func ParseActionKey(k string) (verb, id string, ok bool) {
	verb, id, found := strings.Cut(k, ":")
	if !found || id == "" || (verb != "keep" && verb != "cancel" && verb != "done") {
		return "", "", false
	}
	return verb, id, true
}

type Notification struct {
	Seq            int64     `json:"seq"`
	SubscriptionID string    `json:"subscription_id"`
	Summary        string    `json:"summary"`
	Body           string    `json:"body"`
	Urgency        string    `json:"urgency"` // low|normal|critical
	Actions        []Action  `json:"actions,omitempty"`
	IssuedAt       time.Time `json:"issued_at"`
}

type Channels struct {
	CalendarSyncedAt *time.Time `json:"calendar_synced_at,omitempty"`
	CalendarError    string     `json:"calendar_error,omitempty"`
	EmailError       string     `json:"email_error,omitempty"`
	FXError          string     `json:"fx_error,omitempty"`
	NextDigest       time.Time  `json:"next_digest"`
	FXDate           Date       `json:"fx_date"`
	FXStale          bool       `json:"fx_stale"`
}

type Snapshot struct {
	Items         []Item         `json:"items"`
	Totals        Totals         `json:"totals"`
	Series        []MonthTotal   `json:"series"`
	Channels      Channels       `json:"channels"`
	Notifications []Notification `json:"notifications"`
	LastSeq       int64          `json:"last_seq"`
	ServedBy      string         `json:"served_by"`
	GeneratedAt   time.Time      `json:"generated_at"`
}

// PriceSuffix is "/mo", "/yr", "/wk" or "/day", with the interval for
// multi-unit cycles, e.g. "/3mo".
func PriceSuffix(c Cycle, interval int) string {
	unit := map[Cycle]string{Day: "day", Week: "wk", Month: "mo", Year: "yr"}[c]
	if interval > 1 {
		return "/" + strconv.Itoa(interval) + unit
	}
	return "/" + unit
}
