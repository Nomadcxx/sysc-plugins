package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/fx"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/google"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/httpapi"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/mcptools"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/remind"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/service"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

const (
	tickEvery       = time.Minute
	fxEvery         = 24 * time.Hour
	digestRetry     = time.Hour
	backupsKept     = 14
	defaultDebounce = 2 * time.Second
	shutdownGrace   = 5 * time.Second
	notConfigured   = "Google not configured"
)

// deps are the seams tests replace; zero values mean production defaults.
type deps struct {
	now      func() time.Time
	runner   google.Runner
	fxDoer   fx.Doer
	debounce time.Duration
}

type daemon struct {
	cfg       Config
	now       func() time.Time
	loc       *time.Location
	dataDir   string
	digestDay time.Weekday
	debounce  time.Duration

	st      *store.Store
	svc     *service.Service
	rates   *fx.Rates
	engine  *remind.Engine
	cal     *google.Calendar // nil without a gog account
	mail    *google.Mail     // nil without a gog account
	handler http.Handler

	// Owned by the serve loop.
	dirty      bool
	lastFX     time.Time
	lastBackup subs.Date

	mu            sync.Mutex
	digestErr     string
	lastDigestTry time.Time
}

func newDaemon(cfg Config, d deps) (*daemon, error) {
	if d.now == nil {
		d.now = time.Now
	}
	if d.fxDoer == nil {
		d.fxDoer = &http.Client{Timeout: 15 * time.Second}
	}
	if d.debounce == 0 {
		d.debounce = defaultDebounce
	}
	day, err := parseWeekday(cfg.DigestWeekday)
	if err != nil {
		return nil, err
	}
	dm := &daemon{cfg: cfg, now: d.now, loc: time.Local, dataDir: expandHome(cfg.DataDir), digestDay: day, debounce: d.debounce}
	if dm.st, err = store.Open(filepath.Join(dm.dataDir, "bitsub.db")); err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	dm.rates = fx.New(dm.st, d.fxDoer, cfg.FXURL, d.now, dm.loc)
	dm.svc = service.New(dm.st, d.now, dm.loc, dm.rates, nil, host)
	var mailer remind.Mailer
	if cfg.GogAccount != "" {
		runner := d.runner
		if runner == nil {
			runner = google.ExecRunner{Bin: cfg.GogBin, Account: cfg.GogAccount}
		}
		dm.cal = google.NewCalendar(runner, dm.st, cfg.CalendarName)
		dm.cal.Now, dm.cal.Loc = d.now, dm.loc
		to := cfg.EmailTo
		if to == "" {
			to = cfg.GogAccount
		}
		dm.mail = &google.Mail{R: runner, To: to}
		mailer = dm.mail
	}
	dm.engine = remind.NewEngine(dm.svc, dm.st, dm.rates, mailer, d.now, dm.loc, cfg.ReminderHour)
	dm.svc.SetChannels(dm)
	auth := httpapi.NewAuth(cfg.Tokens)
	dm.handler = httpapi.NewHandler(dm.svc, auth, mcptools.Handler(mcptools.NewServer(dm.svc, version(), auth)))
	return dm, nil
}

func listen(addrs []string) ([]net.Listener, error) {
	var lns []net.Listener
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			for _, l := range lns {
				l.Close()
			}
			return nil, fmt.Errorf("listen %s: %w", a, err)
		}
		lns = append(lns, ln)
	}
	return lns, nil
}

// serve runs the HTTP servers and the scheduling loop until ctx ends, then
// shuts down and closes the store.
func (d *daemon) serve(ctx context.Context, lns []net.Listener) error {
	var servers []*http.Server
	for _, ln := range lns {
		srv := &http.Server{Handler: d.handler, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("bitsubd: serve %s: %v", ln.Addr(), err)
			}
		}()
		log.Printf("bitsubd: listening on %s", ln.Addr())
	}

	d.refreshFX(ctx)
	d.tick(ctx)
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	var debounced <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
			for _, srv := range servers {
				if srv.Shutdown(shutdown) != nil {
					srv.Close()
				}
			}
			cancel()
			return d.st.Close()
		case <-ticker.C:
			d.tick(ctx)
		case <-d.svc.Changed():
			d.dirty = true
			if debounced == nil {
				debounced = time.After(d.debounce)
			}
		case <-debounced:
			debounced = nil
			d.reconcile(ctx)
		}
	}
}

func (d *daemon) tick(ctx context.Context) {
	now := d.now()
	if err := d.engine.Tick(ctx); err != nil {
		log.Printf("bitsubd: reminders: %v", err)
	}
	if d.cal != nil && (d.dirty || !now.Before(d.cal.NextAttempt())) {
		d.reconcile(ctx)
	}
	if now.Sub(d.lastFX) >= fxEvery {
		d.refreshFX(ctx)
	}
	if today := subs.Today(now, d.loc); today != d.lastBackup {
		if err := d.st.BackupDaily(filepath.Join(d.dataDir, "backups"), today, backupsKept); err != nil {
			log.Printf("bitsubd: backup: %v", err)
		} else {
			d.lastBackup = today
		}
	}
	d.maybeDigest(ctx)
}

func (d *daemon) refreshFX(ctx context.Context) {
	d.lastFX = d.now()
	if err := d.rates.Refresh(ctx); err != nil {
		log.Printf("bitsubd: %v", err)
	}
}

func (d *daemon) reconcile(ctx context.Context) {
	d.dirty = false
	if d.cal == nil {
		return
	}
	list, err := d.svc.Active(ctx)
	if err != nil {
		log.Printf("bitsubd: calendar: %v", err)
		return
	}
	rates, _ := d.rates.Current()
	desired := google.DesiredEvents(list, rates, subs.Today(d.now(), d.loc))
	if res, err := d.cal.Reconcile(ctx, desired); err != nil {
		log.Printf("bitsubd: calendar: %v", err)
	} else if res != (google.Result{}) {
		log.Printf("bitsubd: calendar: %d created, %d updated, %d deleted", res.Created, res.Updated, res.Deleted)
	}
}

func digestKey(day subs.Date) string { return "digest_sent:" + day.String() }

func (d *daemon) digestSent(day subs.Date) bool {
	sent := false
	d.st.Tx(context.Background(), func(tx *store.Tx) error {
		_, ok, err := tx.GetMeta(digestKey(day))
		sent = ok
		return err
	})
	return sent
}

// maybeDigest sends the weekly digest once on digest_weekday from
// reminder_hour, retrying hourly on failure.
func (d *daemon) maybeDigest(ctx context.Context) {
	if d.mail == nil {
		return
	}
	now := d.now()
	local := now.In(d.loc)
	today := subs.Today(now, d.loc)
	d.mu.Lock()
	recent := now.Sub(d.lastDigestTry) < digestRetry
	d.mu.Unlock()
	if local.Weekday() != d.digestDay || local.Hour() < d.cfg.ReminderHour || recent || d.digestSent(today) {
		return
	}
	snap, err := d.svc.Snapshot(ctx, 0)
	if err == nil {
		subject, body := google.Digest(snap, today)
		err = d.mail.Send(ctx, subject, body)
	}
	d.mu.Lock()
	d.lastDigestTry = now
	d.digestErr = ""
	if err != nil {
		d.digestErr = "weekly digest: " + err.Error()
	}
	d.mu.Unlock()
	if err != nil {
		log.Printf("bitsubd: %v", err)
		return
	}
	d.st.Tx(ctx, func(tx *store.Tx) error { return tx.SetMeta(digestKey(today), now.UTC().Format(time.RFC3339)) })
}

// Channels implements service.ChannelSource.
func (d *daemon) Channels() subs.Channels {
	var ch subs.Channels
	ch.FXDate, ch.FXStale, ch.FXError = d.rates.Status()
	if d.cal == nil {
		ch.CalendarError, ch.EmailError = notConfigured, notConfigured
	} else {
		ch.CalendarSyncedAt, ch.CalendarError = d.cal.Status()
		ch.EmailError = d.engine.LastEmailError()
		if ch.EmailError == "" {
			d.mu.Lock()
			ch.EmailError = d.digestErr
			d.mu.Unlock()
		}
	}
	ch.NextDigest = d.nextDigest()
	return ch
}

func (d *daemon) nextDigest() time.Time {
	today := subs.Today(d.now(), d.loc)
	for i := 0; i <= 7; i++ {
		day := today.AddDays(i)
		if day.Weekday() == d.digestDay && (i > 0 || !d.digestSent(day)) {
			return day.At(d.cfg.ReminderHour, d.loc)
		}
	}
	return time.Time{}
}
