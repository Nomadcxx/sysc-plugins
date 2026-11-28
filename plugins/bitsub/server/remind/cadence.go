// Package remind decides which reminder step is due for each subscription
// and issues it once.
package remind

import (
	"fmt"
	"slices"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

type Step struct {
	SubscriptionID string
	Target         subs.Date
	Key            string
	Urgency        string
	Summary, Body  string
	Actions        []subs.Action
	Email          bool
}

const (
	urgencyLow      = "low"
	urgencyNormal   = "normal"
	urgencyCritical = "critical"

	// criticalWithin: offsets this close to the target are critical.
	criticalWithin = 2
	// cancelLeadDays: cancel nagging starts this many days before cancel-by.
	cancelLeadDays = 3
)

var (
	trialOffsets     = []int{7, 2, 1}
	undecidedOffsets = []int{7, 2}
	keepOffsets      = []int{1}
	cancelOffsets    = []int{cancelLeadDays, 2, 1}
)

// DueSteps returns at most one step: the latest whose day has arrived while
// its target is still ahead, or today's daily step for a cancel intent.
// charged is the service's "probably charged" flag: a renewal passed after
// the owner chose to cancel, so the nag continues past the renewal.
func DueSteps(s subs.Subscription, today subs.Date, rates subs.Rates, charged bool) []Step {
	if s.Status != subs.Active || s.DeletedAt != nil {
		return nil
	}
	item := subs.ItemOf(s, rates, today)
	price := item.Display()
	target, kind := item.Target, item.TargetKind
	days := subs.DaysBetween(today, target)

	if s.Intent == subs.Cancel {
		cancelBy := item.CancelBy
		if !charged && today.Before(cancelBy.AddDays(-cancelLeadDays)) {
			return nil
		}
		st := Step{SubscriptionID: s.ID, Target: target, Key: "cancel:" + today.String(), Urgency: urgencyNormal,
			Summary: fmt.Sprintf("Cancel %s by %s", s.Name, cancelBy.Short()),
			Body:    price + " · Confirm once you've cancelled.",
			Actions: []subs.Action{{Key: "done:" + s.ID, Label: "I've cancelled it"}}}
		if charged || today.After(cancelBy) {
			st.Summary = s.Name + ": cancellation not confirmed"
		}
		if charged || !today.Before(cancelBy) {
			st.Urgency = urgencyCritical
		}
		st.Email = st.Urgency == urgencyCritical
		return []Step{st}
	}

	prefix := "undecided"
	switch {
	case s.Intent == subs.Keep:
		prefix = "keep"
	case kind == subs.TargetTrialEnd:
		prefix = "trial"
	}
	offset, ok := latestArrived(Offsets(s, kind), days)
	if !ok {
		return nil
	}
	st := Step{SubscriptionID: s.ID, Target: target, Key: fmt.Sprintf("%s:%s:%d", prefix, target, offset),
		Summary: summary(s.Name, kind, days), Body: price}
	switch {
	case s.Intent == subs.Keep:
		st.Urgency = urgencyLow
	case offset <= criticalWithin:
		st.Urgency = urgencyCritical
	default:
		st.Urgency = urgencyNormal
	}
	if s.Intent != subs.Keep {
		st.Body += " · Keep or cancel?"
		st.Actions = []subs.Action{{Key: "keep:" + s.ID, Label: "Keep"}, {Key: "cancel:" + s.ID, Label: "Cancel"}}
	}
	st.Email = st.Urgency == urgencyCritical
	return []Step{st}
}

// Offsets are the days before the target on which steps fire. The calendar
// mirror uses the same list for its alarms. ReminderDays replaces them
// except for a cancel intent, whose nag runs from cancel-by − 3.
func Offsets(s subs.Subscription, kind subs.TargetKind) []int {
	switch {
	case s.Intent == subs.Cancel:
		return cancelOffsets
	case len(s.ReminderDays) > 0:
		return s.ReminderDays
	case s.Intent == subs.Keep:
		return keepOffsets
	case kind == subs.TargetTrialEnd:
		return trialOffsets
	}
	return undecidedOffsets
}

// latestArrived is the smallest offset ≥ days: the most recent step whose
// day has come.
func latestArrived(offsets []int, days int) (int, bool) {
	sorted := slices.Sorted(slices.Values(offsets))
	for _, o := range sorted {
		if o >= days {
			return o, true
		}
	}
	return 0, false
}

func summary(name string, kind subs.TargetKind, days int) string {
	verb := name + " renews"
	if kind == subs.TargetTrialEnd {
		verb = name + " trial ends"
	}
	switch days {
	case 0:
		return verb + " today"
	case 1:
		return verb + " tomorrow"
	}
	return fmt.Sprintf("%s in %d days", verb, days)
}
