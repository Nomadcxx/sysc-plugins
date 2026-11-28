package subs

import "time"

// Cycle is the unit a subscription renews on.
type Cycle string

const (
	Day   Cycle = "day"
	Week  Cycle = "week"
	Month Cycle = "month"
	Year  Cycle = "year"
)

func (c Cycle) Valid() bool { return c == Day || c == Week || c == Month || c == Year }

// Intent is what the owner wants to happen at the next charge.
type Intent string

const (
	Keep      Intent = "keep"
	Cancel    Intent = "cancel"
	Undecided Intent = "undecided"
)

func (i Intent) Valid() bool { return i == Keep || i == Cancel || i == Undecided }

// Status is active until the owner confirms the cancellation.
type Status string

const (
	Active    Status = "active"
	Cancelled Status = "cancelled"
)

type Subscription struct {
	ID, Name, URL string
	Price         Money
	Cycle         Cycle
	Interval      int
	// Anchor is the first real (or post-trial) charge date; renewals derive from it.
	Anchor       Date
	TrialEnd     *Date
	Intent       Intent
	Status       Status
	NoticeDays   int
	AccessUntil  *Date
	Category     string
	PaymentLabel string
	Notes        string
	ReminderDays []int
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

// TrialRunning reports whether the trial has not yet ended on today.
func (s Subscription) TrialRunning(today Date) bool {
	return s.TrialEnd != nil && !s.TrialEnd.Before(today)
}

type ChargeKind string

const (
	// Assumed charges are recorded when a renewal date passes.
	Assumed ChargeKind = "assumed"
	// Recorded charges were confirmed by the owner or an MCP client.
	Recorded ChargeKind = "recorded"
)

type Charge struct {
	ID, SubscriptionID string
	Date               Date
	Amount             Money
	// AUDMinor is locked at FXRate (units per AUD, dated FXDate) when the
	// charge is written. FXRate 0 means no rate was known then.
	AUDMinor int64
	FXRate   float64
	FXDate   Date
	Kind     ChargeKind
}
