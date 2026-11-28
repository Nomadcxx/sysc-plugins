package google

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

func TestMailSendArgvAndStdin(t *testing.T) {
	r := &fakeRunner{replies: map[string]string{"gmail send": `{"messageId":"m1"}`}}
	m := &Mail{R: r, To: "me@example.com"}
	if err := m.Send(ctx, "Grok trial ends tomorrow", "A$30.00/mo · Keep or cancel?\n"); err != nil {
		t.Fatal(err)
	}
	call := r.callsOf("gmail send")[0]
	if !slices.Equal(argValues(call, "--to"), []string{"me@example.com"}) ||
		!slices.Equal(argValues(call, "--subject"), []string{"Grok trial ends tomorrow"}) ||
		!slices.Equal(argValues(call, "--body-file"), []string{"-"}) {
		t.Fatalf("argv %v", call)
	}
	if r.stdin[0] != "A$30.00/mo · Keep or cancel?\n" {
		t.Fatalf("stdin %q", r.stdin[0])
	}
	r.errs = map[string]error{"gmail send": errors.New("exit 4: auth required")}
	if err := m.Send(ctx, "s", "b"); err == nil || !strings.Contains(err.Error(), "auth required") {
		t.Fatalf("error %v", err)
	}
}

func TestDigestSections(t *testing.T) {
	today := subs.MustDate("2026-11-30")
	snap := subs.Snapshot{
		Items: []subs.Item{
			{ID: "a", Name: "Synthetic", Price: subs.Money{Minor: 2000, Currency: "USD"}, PriceAUD: 3101, Cycle: subs.Month, Interval: 1,
				Target: subs.MustDate("2026-12-05"), TargetKind: subs.TargetRenewal, Intent: subs.Undecided, CancelBy: subs.MustDate("2026-12-05")},
			{ID: "b", Name: "Stan", Price: subs.Money{Minor: 1700, Currency: "AUD"}, PriceAUD: 1700, Cycle: subs.Month, Interval: 1,
				Target: subs.MustDate("2026-12-10"), TargetKind: subs.TargetRenewal, Intent: subs.Cancel, CancelBy: subs.MustDate("2026-12-08"), ProbablyCharged: true},
			{ID: "c", Name: "Hetzner", Price: subs.Money{Minor: 549, Currency: "EUR"}, PriceAUD: 931, Cycle: subs.Month, Interval: 1,
				Target: subs.MustDate("2027-01-20"), TargetKind: subs.TargetRenewal, Intent: subs.Keep},
		},
		Totals:   subs.Totals{MonthlyAUD: 34280, YearlyAUD: 411360, Next30AUD: 39611, Next30Count: 7},
		Channels: subs.Channels{CalendarError: "insufficient scope", NextDigest: time.Date(2026, 12, 7, 9, 0, 0, 0, time.UTC)},
	}
	subject, body := Digest(snap, today)
	if subject != "bitsub: 1 to decide · A$342.80/mo" {
		t.Fatalf("subject %q", subject)
	}
	for _, want := range []string{"Decide", "Synthetic", "A$31.01/mo (US$20.00)", "Cancelling", "Stan", "cancel by Tue 8 Dec",
		"probably charged", "Next 14 days", "Totals", "A$342.80 a month", "A$396.11 in the next 30 days (7 renewals)",
		"Problems", "Calendar out of date: insufficient scope"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Hetzner") {
		t.Errorf("kept item outside 14 days listed:\n%s", body)
	}

	_, quiet := Digest(subs.Snapshot{Totals: subs.Totals{}}, today)
	for _, absent := range []string{"Decide", "Cancelling", "Next 14 days", "Problems"} {
		if strings.Contains(quiet, absent) {
			t.Errorf("empty section %q present:\n%s", absent, quiet)
		}
	}
	if !strings.Contains(quiet, "Totals") {
		t.Errorf("totals missing:\n%s", quiet)
	}
}

func TestDigestNoRatesSaysPending(t *testing.T) {
	subject, body := Digest(subs.Snapshot{Totals: subs.Totals{Approx: true}}, subs.MustDate("2026-11-30"))
	if strings.Contains(subject+body, "A$0") {
		t.Fatalf("%s\n%s", subject, body)
	}
}
