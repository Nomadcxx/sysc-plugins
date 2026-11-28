package remind

import (
	"context"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/service"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

type Mailer interface {
	Send(ctx context.Context, subject, body string) error
}

type Engine struct {
	svc   *service.Service
	st    *store.Store
	rates service.RatesSource
	mail  Mailer
	now   func() time.Time
	loc   *time.Location
	hour  int

	mu      sync.Mutex
	mailErr string
}

func NewEngine(svc *service.Service, st *store.Store, rates service.RatesSource, mail Mailer, now func() time.Time, loc *time.Location, hour int) *Engine {
	return &Engine{svc: svc, st: st, rates: rates, mail: mail, now: now, loc: loc, hour: hour}
}

// Tick does nothing before hour:00 local. Otherwise it rolls charges over,
// queues each due step once and emails critical steps the first time.
func (e *Engine) Tick(ctx context.Context) error {
	now := e.now()
	if now.In(e.loc).Hour() < e.hour {
		return nil
	}
	if _, err := e.svc.Rollover(ctx); err != nil {
		return err
	}
	items, err := e.svc.List(ctx, service.Filter{})
	if err != nil {
		return err
	}
	charged := map[string]bool{}
	for _, it := range items {
		charged[it.ID] = it.ProbablyCharged
	}
	var rates subs.Rates
	if e.rates != nil {
		rates, _ = e.rates.Current()
	}
	today := subs.Today(now, e.loc)
	var emails []Step
	err = e.st.Tx(ctx, func(tx *store.Tx) error {
		list, err := tx.ListSubscriptions(false)
		if err != nil {
			return err
		}
		for _, s := range list {
			for _, step := range DueSteps(s, today, rates, charged[s.ID]) {
				_, inserted, err := tx.InsertNotification(step.Key+":"+s.ID, subs.Notification{
					SubscriptionID: s.ID, Summary: step.Summary, Body: step.Body, Urgency: step.Urgency,
					Actions: step.Actions, IssuedAt: now.UTC(),
				})
				if err != nil {
					return err
				}
				if inserted && step.Email {
					emails = append(emails, step)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if e.mail == nil {
		return nil
	}
	for _, st := range emails {
		err := e.mail.Send(ctx, st.Summary, st.Body+"\n\nReply in the bitsub panel, or tell an MCP client which you chose.\n")
		e.mu.Lock()
		if err != nil {
			e.mailErr = err.Error()
		} else {
			e.mailErr = ""
		}
		e.mu.Unlock()
	}
	return nil
}

// LastEmailError is the most recent mail failure, cleared on success.
func (e *Engine) LastEmailError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mailErr
}
