package remind

import (
	"slices"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var rates = subs.Rates{Date: subs.MustDate("2026-11-28"), PerAUD: map[subs.Currency]float64{"USD": 0.645}}

func ratesOn(day string) subs.Rates {
	r := rates
	r.Date = subs.MustDate(day)
	return r
}

func trial(end string, intent subs.Intent) subs.Subscription {
	d := subs.MustDate(end)
	return subs.Subscription{ID: "t1", Name: "Grok", Price: subs.Money{Minor: 3000, Currency: "AUD"}, Cycle: subs.Month,
		Interval: 1, Anchor: d, TrialEnd: &d, Intent: intent, Status: subs.Active}
}

func one(t *testing.T, s subs.Subscription, today string, charged bool) *Step {
	t.Helper()
	steps := DueSteps(s, subs.MustDate(today), ratesOn(today), charged)
	if len(steps) > 1 {
		t.Fatalf("%s: %d steps", today, len(steps))
	}
	if len(steps) == 0 {
		return nil
	}
	return &steps[0]
}

func TestTrialSteps(t *testing.T) {
	s := trial("2026-12-09", subs.Undecided)
	cases := []struct {
		today, key, urgency, summary string
	}{
		{"2026-12-01", "", "", ""},
		{"2026-12-02", "trial:2026-12-09:7", "normal", "Grok trial ends in 7 days"},
		{"2026-12-06", "trial:2026-12-09:7", "normal", "Grok trial ends in 3 days"},
		{"2026-12-07", "trial:2026-12-09:2", "critical", "Grok trial ends in 2 days"},
		{"2026-12-08", "trial:2026-12-09:1", "critical", "Grok trial ends tomorrow"},
		{"2026-12-09", "trial:2026-12-09:1", "critical", "Grok trial ends today"},
		{"2026-12-10", "", "", ""},
	}
	for _, c := range cases {
		st := one(t, s, c.today, false)
		if c.key == "" {
			if st != nil {
				t.Errorf("%s: unexpected %+v", c.today, st)
			}
			continue
		}
		if st == nil || st.Key != c.key || st.Urgency != c.urgency || st.Summary != c.summary || st.Email != (c.urgency == "critical") {
			t.Errorf("%s: got %+v", c.today, st)
			continue
		}
		if len(st.Actions) != 2 || st.Actions[0] != (subs.Action{Key: "keep:t1", Label: "Keep"}) || st.Actions[1] != (subs.Action{Key: "cancel:t1", Label: "Cancel"}) {
			t.Errorf("%s: actions %+v", c.today, st.Actions)
		}
	}
}

func TestUndecidedRenewalSteps(t *testing.T) {
	s := subs.Subscription{ID: "u", Name: "Synthetic", Price: subs.Money{Minor: 2000, Currency: "USD"}, Cycle: subs.Month,
		Interval: 1, Anchor: subs.MustDate("2026-01-20"), Intent: subs.Undecided, Status: subs.Active}
	if st := one(t, s, "2026-12-12", false); st != nil {
		t.Fatalf("8 days out: %+v", st)
	}
	if st := one(t, s, "2026-12-13", false); st == nil || st.Key != "undecided:2026-12-20:7" || st.Urgency != "normal" ||
		st.Summary != "Synthetic renews in 7 days" {
		t.Fatalf("%+v", st)
	}
	if st := one(t, s, "2026-12-18", false); st == nil || st.Key != "undecided:2026-12-20:2" || st.Urgency != "critical" {
		t.Fatalf("%+v", st)
	}
	if st := one(t, s, "2026-12-19", false); st == nil || st.Key != "undecided:2026-12-20:2" || st.Summary != "Synthetic renews tomorrow" {
		t.Fatalf("%+v", st)
	}
}

func TestCancelNagsDailyUntilConfirmed(t *testing.T) {
	s := subs.Subscription{ID: "c", Name: "Stan", Price: subs.Money{Minor: 1700, Currency: "AUD"}, Cycle: subs.Month,
		Interval: 1, Anchor: subs.MustDate("2026-11-10"), Intent: subs.Cancel, Status: subs.Active}
	if st := one(t, s, "2026-12-06", false); st != nil {
		t.Fatalf("12-06: %+v", st)
	}
	st := one(t, s, "2026-12-07", false)
	if st == nil || st.Key != "cancel:2026-12-07" || st.Urgency != "normal" || st.Summary != "Cancel Stan by Thu 10 Dec" ||
		st.Body != "A$17.00/mo · Confirm once you've cancelled." || st.Email {
		t.Fatalf("12-07: %+v", st)
	}
	if len(st.Actions) != 1 || st.Actions[0] != (subs.Action{Key: "done:c", Label: "I've cancelled it"}) {
		t.Fatalf("actions %+v", st.Actions)
	}
	if st := one(t, s, "2026-12-10", false); st == nil || st.Key != "cancel:2026-12-10" || st.Urgency != "critical" || !st.Email {
		t.Fatalf("12-10: %+v", st)
	}
	// After the renewal passed unconfirmed the service flags it probably charged.
	if st := one(t, s, "2026-12-12", true); st == nil || st.Key != "cancel:2026-12-12" || st.Urgency != "critical" ||
		st.Summary != "Stan: cancellation not confirmed" {
		t.Fatalf("12-12: %+v", st)
	}
	// Notice days: past the cancel-by date but before the renewal, still nagging.
	s.NoticeDays = 3
	if st := one(t, s, "2026-12-08", false); st == nil || st.Summary != "Stan: cancellation not confirmed" || st.Urgency != "critical" {
		t.Fatalf("notice: %+v", st)
	}
	s.Status = subs.Cancelled
	if st := one(t, s, "2026-12-12", true); st != nil {
		t.Fatalf("confirmed still nags: %+v", st)
	}
}

func TestKeepSingleLowHeadsUp(t *testing.T) {
	s := subs.Subscription{ID: "k", Name: "Netflix", Price: subs.Money{Minor: 1899, Currency: "AUD"}, Cycle: subs.Month,
		Interval: 1, Anchor: subs.MustDate("2026-01-14"), Intent: subs.Keep, Status: subs.Active}
	if st := one(t, s, "2026-12-12", false); st != nil {
		t.Fatalf("%+v", st)
	}
	st := one(t, s, "2026-12-13", false)
	if st == nil || st.Key != "keep:2026-12-14:1" || st.Urgency != "low" || st.Email || len(st.Actions) != 0 ||
		st.Summary != "Netflix renews tomorrow" || st.Body != "A$18.99/mo" {
		t.Fatalf("%+v", st)
	}
	// A kept trial gets the same single heads-up, worded as a trial.
	k := trial("2026-12-09", subs.Keep)
	if st := one(t, k, "2026-12-02", false); st != nil {
		t.Fatalf("kept trial at 7 days: %+v", st)
	}
	if st := one(t, k, "2026-12-08", false); st == nil || st.Key != "keep:2026-12-09:1" || st.Summary != "Grok trial ends tomorrow" {
		t.Fatalf("kept trial: %+v", st)
	}
}

func TestReminderDaysOverride(t *testing.T) {
	s := subs.Subscription{ID: "u", Name: "Synthetic", Price: subs.Money{Minor: 2000, Currency: "USD"}, Cycle: subs.Month,
		Interval: 1, Anchor: subs.MustDate("2026-01-20"), Intent: subs.Undecided, Status: subs.Active, ReminderDays: []int{14, 3}}
	if st := one(t, s, "2026-12-06", false); st == nil || st.Key != "undecided:2026-12-20:14" || st.Urgency != "normal" {
		t.Fatalf("%+v", st)
	}
	if st := one(t, s, "2026-12-13", false); st == nil || st.Key != "undecided:2026-12-20:14" {
		t.Fatalf("7 days no longer a step: %+v", st)
	}
	if st := one(t, s, "2026-12-17", false); st == nil || st.Key != "undecided:2026-12-20:3" || st.Urgency != "normal" {
		t.Fatalf("%+v", st)
	}
}

func TestStepCopy(t *testing.T) {
	s := trial("2026-12-02", subs.Undecided)
	s.Name, s.Price, s.Cycle = "Speechify Premium", subs.Money{Minor: 13900, Currency: "USD"}, subs.Year
	st := one(t, s, "2026-11-29", false)
	if st == nil || st.Summary != "Speechify Premium trial ends in 3 days" || st.Body != "A$215.50/yr (US$139.00) · Keep or cancel?" {
		t.Fatalf("%+v", st)
	}
}

func TestStepCopyWithoutRates(t *testing.T) {
	s := trial("2026-12-02", subs.Undecided)
	s.Price = subs.Money{Minor: 3000, Currency: "USD"}
	steps := DueSteps(s, subs.MustDate("2026-11-29"), subs.Rates{}, false)
	if len(steps) != 1 || steps[0].Body != "US$30.00/mo · Keep or cancel?" {
		t.Fatalf("%+v", steps)
	}
}

func TestOffsets(t *testing.T) {
	s := trial("2026-12-09", subs.Undecided)
	cases := []struct {
		intent subs.Intent
		kind   subs.TargetKind
		days   []int
		want   []int
	}{
		{subs.Undecided, subs.TargetTrialEnd, nil, []int{7, 2, 1}},
		{subs.Undecided, subs.TargetRenewal, nil, []int{7, 2}},
		{subs.Keep, subs.TargetTrialEnd, nil, []int{1}},
		{subs.Cancel, subs.TargetRenewal, nil, []int{3, 2, 1}},
		{subs.Undecided, subs.TargetRenewal, []int{14, 3}, []int{14, 3}},
		{subs.Cancel, subs.TargetRenewal, []int{14, 3}, []int{3, 2, 1}},
	}
	for _, c := range cases {
		s.Intent, s.ReminderDays = c.intent, c.days
		if got := Offsets(s, c.kind); !slices.Equal(got, c.want) {
			t.Errorf("%s/%s/%v: %v want %v", c.intent, c.kind, c.days, got, c.want)
		}
	}
}
