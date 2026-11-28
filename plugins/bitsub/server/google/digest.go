package google

import (
	"fmt"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

const digestHorizonDays = 14

// Digest is the weekly plain-text summary. Empty sections are left out.
func Digest(s subs.Snapshot, today subs.Date) (subject, body string) {
	var decide, cancelling, soon []string
	horizon := today.AddDays(digestHorizonDays)
	for _, it := range s.Items {
		line := fmt.Sprintf("- %s — %s — %s", it.Name, when(it, today), it.Display())
		switch it.Intent {
		case subs.Undecided:
			decide = append(decide, line)
		case subs.Cancel:
			l := fmt.Sprintf("- %s — cancel by %s — %s", it.Name, it.CancelBy.Short(), it.Display())
			if it.ProbablyCharged {
				l += " — probably charged, confirm whether you cancelled"
			}
			cancelling = append(cancelling, l)
		}
		if !it.Target.After(horizon) {
			soon = append(soon, line)
		}
	}
	monthly := subs.FormatAUD(s.Totals.MonthlyAUD, subs.Money{Currency: subs.AUD}, s.Totals.Approx)
	subject = fmt.Sprintf("bitsub: %d to decide · %s/mo", len(decide), monthly)

	var b strings.Builder
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s\n%s\n\n", title, strings.Join(lines, "\n"))
	}
	section("Decide", decide)
	section("Cancelling", cancelling)
	section("Next 14 days", soon)
	next30 := subs.FormatAUD(s.Totals.Next30AUD, subs.Money{Currency: subs.AUD}, s.Totals.Approx)
	section("Totals", []string{
		fmt.Sprintf("- %s a month", monthly),
		fmt.Sprintf("- %s in the next 30 days (%d renewals)", next30, s.Totals.Next30Count),
	})
	section("Problems", problems(s.Channels))
	return subject, strings.TrimRight(b.String(), "\n") + "\n"
}

func when(it subs.Item, today subs.Date) string {
	days := subs.DaysBetween(today, it.Target)
	verb := "renews"
	if it.TargetKind == subs.TargetTrialEnd {
		verb = "trial ends"
	}
	return fmt.Sprintf("%s %s (in %d days)", verb, it.Target.Short(), days)
}

func problems(ch subs.Channels) []string {
	var out []string
	if ch.CalendarError != "" {
		out = append(out, "- Calendar out of date: "+ch.CalendarError)
	}
	if ch.EmailError != "" {
		out = append(out, "- Email failing: "+ch.EmailError)
	}
	if ch.FXError != "" {
		out = append(out, "- Exchange rates failing: "+ch.FXError)
	} else if ch.FXStale {
		out = append(out, "- Exchange rates are stale; totals are approximate")
	}
	return out
}
