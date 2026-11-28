// Package mcptools exposes the bitsub service as MCP tools over streamable HTTP.
package mcptools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/httpapi"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/service"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

const (
	hardRule = "Records only. Never attempt to cancel with the vendor; the owner does that."
	// sessionIdle closes MCP sessions a client abandoned.
	sessionIdle = 30 * time.Minute
)

type itemOut struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceDisplay    string `json:"price_display" jsonschema:"AUD first with the original currency in brackets, as the owner sees it"`
	PriceAUD        string `json:"price_aud" jsonschema:"Price per cycle in AUD; empty when no exchange rate is known yet"`
	PriceOriginal   string `json:"price_original"`
	Amount          string `json:"amount" jsonschema:"Price per cycle in the original currency, as a decimal"`
	Currency        string `json:"currency"`
	PriceApprox     bool   `json:"price_approx"`
	Cycle           string `json:"cycle"`
	Interval        int    `json:"interval"`
	NextDate        string `json:"next_date" jsonschema:"Next trial end or renewal, YYYY-MM-DD"`
	NextKind        string `json:"next_kind" jsonschema:"trial_end or renewal"`
	CancelBy        string `json:"cancel_by" jsonschema:"Last day to cancel with the vendor before next_date"`
	Intent          string `json:"intent"`
	ProbablyCharged bool   `json:"probably_charged" jsonschema:"A renewal passed after the owner chose to cancel; ask whether they did"`
}

func itemFrom(it subs.Item) itemOut {
	out := itemOut{
		ID: it.ID, Name: it.Name, PriceDisplay: it.Display(), PriceOriginal: it.Price.String(),
		Amount: it.Price.Decimal(), Currency: string(it.Price.Currency), PriceApprox: it.PriceApprox,
		Cycle: string(it.Cycle), Interval: it.Interval, NextDate: it.Target.String(), NextKind: string(it.TargetKind),
		CancelBy: it.CancelBy.String(), Intent: string(it.Intent), ProbablyCharged: it.ProbablyCharged,
	}
	if it.PriceAUD != 0 || it.Price.Currency == subs.AUD {
		out.PriceAUD = subs.Money{Minor: it.PriceAUD, Currency: subs.AUD}.String()
	}
	return out
}

type subscriptionOut struct {
	itemOut
	Status       string `json:"status"`
	URL          string `json:"url,omitempty"`
	Category     string `json:"category,omitempty"`
	PaymentLabel string `json:"payment_label,omitempty"`
	Notes        string `json:"notes,omitempty"`
	NoticeDays   int    `json:"notice_days"`
	ReminderDays []int  `json:"reminder_days,omitempty"`
	AnchorDate   string `json:"anchor_date"`
	TrialEnd     string `json:"trial_end,omitempty"`
	AccessUntil  string `json:"access_until,omitempty"`
	CreatedBy    string `json:"created_by"`
	Note         string `json:"note,omitempty"`
}

func optDate(d *subs.Date) string {
	if d == nil {
		return ""
	}
	return d.String()
}

type listOut struct {
	Count         int       `json:"count"`
	Subscriptions []itemOut `json:"subscriptions"`
}

type totalsOut struct {
	PerMonth    string `json:"per_month"`
	PerYear     string `json:"per_year"`
	Next30Days  string `json:"next_30_days"`
	Next30Count int    `json:"next_30_count"`
	Approx      bool   `json:"approx" jsonschema:"Some non-AUD prices lack a current rate (left out) or rates are stale"`
}

type chargeOut struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id"`
	Date           string `json:"date"`
	Amount         string `json:"amount"`
	AUD            string `json:"aud"`
	Kind           string `json:"kind"`
}

type deleteOut struct {
	DeletedID string `json:"deleted_id"`
	Note      string `json:"note"`
}

type listIn struct {
	Status string `json:"status,omitempty" jsonschema:"active (default) or cancelled"`
	Intent string `json:"intent,omitempty" jsonschema:"Only this intent: keep, cancel or undecided"`
	Text   string `json:"text,omitempty" jsonschema:"Case-insensitive match on name or category"`
}

type getIn struct {
	IDOrName string `json:"id_or_name" jsonschema:"Subscription id, or its exact name (case and spacing ignored)"`
}

type addIn struct {
	Name           string `json:"name" jsonschema:"Vendor and plan as the owner calls it, e.g. Netflix or Speechify Premium"`
	Amount         string `json:"amount" jsonschema:"Price per cycle as a decimal string in the original currency, e.g. 18.99"`
	Currency       string `json:"currency" jsonschema:"ISO 4217 code of the price, e.g. AUD, USD, EUR"`
	Cycle          string `json:"cycle" jsonschema:"day, week, month or year"`
	Interval       int    `json:"interval,omitempty" jsonschema:"Cycles between charges, 1 to 36; 3 with month is quarterly. Default 1"`
	AnchorDate     string `json:"anchor_date,omitempty" jsonschema:"First real charge date, YYYY-MM-DD. For trials, omit and set trial_end"`
	TrialEnd       string `json:"trial_end,omitempty" jsonschema:"Last day of a free trial, YYYY-MM-DD; the first charge is assumed that day"`
	Intent         string `json:"intent,omitempty" jsonschema:"keep, cancel or undecided. Only set what the owner said; default undecided"`
	URL            string `json:"url,omitempty" jsonschema:"Vendor account or billing page, http(s)"`
	Category       string `json:"category,omitempty" jsonschema:"Free text, e.g. Streaming or AI and LLMs"`
	PaymentLabel   string `json:"payment_label,omitempty" jsonschema:"How it is paid, e.g. Visa ··21 or PayPal. Never a card number"`
	Notes          string `json:"notes,omitempty"`
	NoticeDays     int    `json:"notice_days,omitempty" jsonschema:"Days before renewal the vendor needs a cancellation, 0 to 90"`
	ReminderDays   []int  `json:"reminder_days,omitempty" jsonschema:"Override reminder lead days, e.g. [14,3,1]; up to 5 values in 1 to 60"`
	AllowDuplicate bool   `json:"allow_duplicate,omitempty" jsonschema:"Add even if an active subscription has the same name. Prefer update_subscription"`
}

type updateIn struct {
	ID            string  `json:"id" jsonschema:"Subscription id from list_subscriptions"`
	Name          *string `json:"name,omitempty"`
	Amount        *string `json:"amount,omitempty" jsonschema:"New price per cycle as a decimal string"`
	Currency      *string `json:"currency,omitempty"`
	Cycle         *string `json:"cycle,omitempty"`
	Interval      *int    `json:"interval,omitempty"`
	AnchorDate    *string `json:"anchor_date,omitempty" jsonschema:"YYYY-MM-DD"`
	TrialEnd      *string `json:"trial_end,omitempty" jsonschema:"YYYY-MM-DD"`
	ClearTrialEnd bool    `json:"clear_trial_end,omitempty" jsonschema:"Remove the trial end (the trial was converted or never existed)"`
	Intent        *string `json:"intent,omitempty" jsonschema:"keep, cancel or undecided; only what the owner said"`
	URL           *string `json:"url,omitempty"`
	Category      *string `json:"category,omitempty"`
	PaymentLabel  *string `json:"payment_label,omitempty" jsonschema:"Never a card number"`
	Notes         *string `json:"notes,omitempty"`
	NoticeDays    *int    `json:"notice_days,omitempty"`
	ReminderDays  *[]int  `json:"reminder_days,omitempty" jsonschema:"Replace the reminder lead days; [] restores the defaults"`
}

type intentIn struct {
	ID     string `json:"id"`
	Intent string `json:"intent" jsonschema:"keep, cancel or undecided"`
}

type confirmIn struct {
	ID          string `json:"id"`
	AccessUntil string `json:"access_until,omitempty" jsonschema:"Last day of paid access after cancelling, YYYY-MM-DD"`
}

type chargeIn struct {
	ID       string `json:"id"`
	Date     string `json:"date" jsonschema:"Day the charge happened, YYYY-MM-DD, not in the future"`
	Amount   string `json:"amount,omitempty" jsonschema:"Charged amount as a decimal string; default the list price"`
	Currency string `json:"currency,omitempty" jsonschema:"Currency of amount; default the subscription's"`
}

type upcomingIn struct {
	Days int `json:"days,omitempty" jsonschema:"Window in days, 1 to 366; default 30"`
}

type idIn struct {
	ID string `json:"id"`
}

type empty struct{}

// actorOf names the calling client by its bearer token, falling back to "mcp".
func actorOf(auth *httpapi.Auth, req *mcp.CallToolRequest) string {
	if auth != nil && req != nil && req.Extra != nil {
		if name := auth.Actor(req.Extra.Header); name != "" {
			return name
		}
	}
	return "mcp"
}

// NewServer registers bitsub's ten tools. auth resolves the actor recorded
// in the audit log; nil records "mcp".
func NewServer(svc *service.Service, version string, auth *httpapi.Auth) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "bitsub", Version: version}, &mcp.ServerOptions{
		Instructions: "bitsub tracks the owner's subscriptions, trials and renewals. Search with list_subscriptions " +
			"before adding. Quote prices as AUD with the original in brackets. " + hardRule,
	})
	subOut := func(ctx context.Context, s subs.Subscription) (subscriptionOut, error) {
		it, err := svc.Item(ctx, s)
		if err != nil {
			return subscriptionOut{}, err
		}
		return subscriptionOut{
			itemOut: itemFrom(it), Status: string(s.Status), URL: s.URL, Category: s.Category, PaymentLabel: s.PaymentLabel,
			Notes: s.Notes, NoticeDays: s.NoticeDays, ReminderDays: s.ReminderDays, AnchorDate: s.Anchor.String(),
			TrialEnd: optDate(s.TrialEnd), AccessUntil: optDate(s.AccessUntil), CreatedBy: s.CreatedBy,
		}, nil
	}
	finish := func(ctx context.Context, s subs.Subscription, err error, note string) (*mcp.CallToolResult, subscriptionOut, error) {
		if err != nil {
			return nil, subscriptionOut{}, toolError(err)
		}
		out, err := subOut(ctx, s)
		out.Note = note
		return nil, out, toolError(err)
	}
	list := func(items []subs.Item, err error) (*mcp.CallToolResult, listOut, error) {
		if err != nil {
			return nil, listOut{}, toolError(err)
		}
		out := listOut{Count: len(items), Subscriptions: []itemOut{}}
		for _, it := range items {
			out.Subscriptions = append(out.Subscriptions, itemFrom(it))
		}
		return nil, out, nil
	}

	mcp.AddTool(srv, &mcp.Tool{Name: "list_subscriptions",
		Description: "List subscriptions, soonest renewal or trial end first, with AUD and original prices. Call this before adding one."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
			return list(svc.List(ctx, service.Filter{Status: subs.Status(in.Status), Intent: subs.Intent(in.Intent), Text: in.Text}))
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "get_subscription",
		Description: "Get one subscription by id or by its exact name."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, subscriptionOut, error) {
			s, err := svc.Get(ctx, in.IDOrName)
			return finish(ctx, s, err, "")
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "add_subscription",
		Description: "Record a new subscription or free trial. Required: name, amount, currency, cycle, and anchor_date (first real " +
			"charge) or trial_end. Leave intent undecided unless the owner said keep or cancel. Refuses a name that already " +
			"exists; update that entry instead."},
		func(ctx context.Context, req *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, subscriptionOut, error) {
			s, err := svc.Add(ctx, actorOf(auth, req), service.AddInput{
				Name: in.Name, URL: in.URL, Amount: in.Amount, Currency: in.Currency, Cycle: subs.Cycle(in.Cycle),
				Interval: in.Interval, AnchorDate: in.AnchorDate, TrialEnd: in.TrialEnd, Intent: subs.Intent(in.Intent),
				NoticeDays: in.NoticeDays, Category: in.Category, PaymentLabel: in.PaymentLabel, Notes: in.Notes,
				ReminderDays: in.ReminderDays, AllowDuplicate: in.AllowDuplicate,
			})
			return finish(ctx, s, err, "")
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "update_subscription",
		Description: "Change fields of a subscription; omitted fields stay as they are. Use it for price changes, new dates " +
			"and corrections. Every change is kept in the audit log."},
		func(ctx context.Context, req *mcp.CallToolRequest, in updateIn) (*mcp.CallToolResult, subscriptionOut, error) {
			s, err := svc.Update(ctx, actorOf(auth, req), in.ID, service.UpdateInput{
				Name: in.Name, URL: in.URL, Amount: in.Amount, Currency: in.Currency, Cycle: (*subs.Cycle)(in.Cycle),
				Interval: in.Interval, AnchorDate: in.AnchorDate, TrialEnd: in.TrialEnd, Intent: (*subs.Intent)(in.Intent),
				NoticeDays: in.NoticeDays, Category: in.Category, PaymentLabel: in.PaymentLabel, Notes: in.Notes,
				ReminderDays: in.ReminderDays, ClearTrialEnd: in.ClearTrialEnd,
			})
			return finish(ctx, s, err, "")
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "set_intent",
		Description: "Record what the owner wants at the next charge: keep, cancel or undecided. With cancel, bitsub reminds " +
			"the owner daily until they confirm. " + hardRule},
		func(ctx context.Context, req *mcp.CallToolRequest, in intentIn) (*mcp.CallToolResult, subscriptionOut, error) {
			s, err := svc.SetIntent(ctx, actorOf(auth, req), in.ID, subs.Intent(in.Intent))
			note := ""
			if in.Intent == string(subs.Cancel) {
				note = "Recorded. The owner must cancel with the vendor themselves, then confirm_cancelled."
			}
			return finish(ctx, s, err, note)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "confirm_cancelled",
		Description: "Mark a subscription cancelled, only after the owner says they have cancelled it. Stops reminders; " +
			"charge history is kept. " + hardRule},
		func(ctx context.Context, req *mcp.CallToolRequest, in confirmIn) (*mcp.CallToolResult, subscriptionOut, error) {
			s, err := svc.ConfirmCancelled(ctx, actorOf(auth, req), in.ID, in.AccessUntil)
			return finish(ctx, s, err, "")
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "record_charge",
		Description: "Record an actual charge seen on a statement or receipt; the amount may differ from the list price."},
		func(ctx context.Context, req *mcp.CallToolRequest, in chargeIn) (*mcp.CallToolResult, chargeOut, error) {
			c, err := svc.RecordCharge(ctx, actorOf(auth, req), in.ID, in.Date, in.Amount, in.Currency)
			if err != nil {
				return nil, chargeOut{}, toolError(err)
			}
			aud := ""
			if c.FXRate != 0 {
				aud = subs.Money{Minor: c.AUDMinor, Currency: subs.AUD}.String()
			}
			return nil, chargeOut{ID: c.ID, SubscriptionID: c.SubscriptionID, Date: c.Date.String(),
				Amount: c.Amount.String(), AUD: aud, Kind: string(c.Kind)}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "upcoming",
		Description: "Renewals and trial ends in the next N days (default 30), soonest first."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in upcomingIn) (*mcp.CallToolResult, listOut, error) {
			return list(svc.Upcoming(ctx, in.Days))
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "totals",
		Description: "Spending in AUD: per month and per year for subscriptions being kept, and everything due in the next 30 days."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, totalsOut, error) {
			t, err := svc.Totals(ctx)
			if err != nil {
				return nil, totalsOut{}, toolError(err)
			}
			aud := func(m int64) string { return subs.FormatAUD(m, subs.Money{Currency: subs.AUD}, t.Approx) }
			return nil, totalsOut{PerMonth: aud(t.MonthlyAUD), PerYear: aud(t.YearlyAUD), Next30Days: aud(t.Next30AUD),
				Next30Count: t.Next30Count, Approx: t.Approx}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "delete_subscription",
		Description: "Soft-delete an entry recorded by mistake. Not for cancellations: use set_intent and confirm_cancelled."},
		func(ctx context.Context, req *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, deleteOut, error) {
			if err := svc.Delete(ctx, actorOf(auth, req), in.ID); err != nil {
				return nil, deleteOut{}, toolError(err)
			}
			return nil, deleteOut{DeletedID: in.ID, Note: "Soft-deleted; it can be restored by hand from the audit log."}, nil
		})
	return srv
}

// toolError makes service errors actionable for the calling model.
func toolError(err error) error {
	if errors.Is(err, service.ErrNotFound) {
		return fmt.Errorf("no such subscription; call list_subscriptions to find its id")
	}
	return err
}

// Handler serves srv over streamable HTTP.
func Handler(srv *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{SessionTimeout: sessionIdle})
}
