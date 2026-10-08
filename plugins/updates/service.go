package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Schedule defaults. The start delay keeps the first check off the shell's
// startup path; tests shrink it through Service.StartDelay.
const (
	DefaultStartDelay = 2 * time.Minute
	DefaultInterval   = 3 * time.Hour
	MinIntervalHours  = 1
	MaxIntervalHours  = 24
)

// State is what the plugin knows right now. The unexported field markers
// keep the transient parts out of the persisted copy.
type State struct {
	Updates   []Update  `json:"updates,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`

	CheckErr       string `json:"-"`
	RepoMissing    bool   `json:"-"`
	AURMissing     bool   `json:"-"`
	FlatpakMissing bool   `json:"-"`
	Checking       bool   `json:"-"`
	RebootNeeded   bool   `json:"-"`
	RebootDetail   string `json:"-"`
}

// persisted is the subset of State worth keeping across shell restarts: the
// last good result, so the badge is honest before the first check lands.
type persisted struct {
	Updates   []Update  `json:"updates"`
	CheckedAt time.Time `json:"checked_at"`
}

// Config is the user-facing check configuration.
type Config struct {
	AURHelper      string // "auto", "paru", "yay", or "off"
	IncludeFlatpak bool
}

// Service owns the check state and schedule. Its methods are safe for
// concurrent use: the event loop reads State while a check runs.
type Service struct {
	run      Runner
	lookPath func(string) (string, error)
	now      func() time.Time
	reboot   func() (bool, string)

	// StartDelay is how long after start the first check runs. Zero means
	// DefaultStartDelay.
	StartDelay time.Duration

	mu             sync.Mutex
	cfg            Config
	state          State
	checking       bool
	lastNotifyDay  string
	rebootNotified bool
}

// NewService builds a Service. now and reboot may be nil for the real
// implementations; tests inject their own.
func NewService(cfg Config, run Runner, lookPath func(string) (string, error), now func() time.Time, reboot func() (bool, string)) *Service {
	if now == nil {
		now = time.Now
	}
	if reboot == nil {
		reboot = func() (bool, string) { return false, "" }
	}
	return &Service{run: run, lookPath: lookPath, now: now, reboot: reboot, cfg: cfg}
}

// SetConfig replaces the check configuration; the next check uses it.
func (s *Service) SetConfig(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

// State returns a copy safe to read without holding any lock.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	state.Updates = append([]Update(nil), s.state.Updates...)
	return state
}

// RestoreState loads a persisted result from a previous shell session.
func (s *Service) RestoreState(raw []byte) {
	var p persisted
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Updates = p.Updates
	s.state.CheckedAt = p.CheckedAt
}

// PersistedState encodes the last good result for state.set. It returns nil
// when there is nothing worth persisting.
func (s *Service) PersistedState() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.CheckedAt.IsZero() {
		return nil
	}
	data, err := json.Marshal(persisted{Updates: s.state.Updates, CheckedAt: s.state.CheckedAt})
	if err != nil {
		return nil
	}
	return data
}

// Check runs every enabled backend and returns the resulting state. A
// failure keeps the previous list and records the reason; one check runs at
// a time, so a second caller while one is in flight gets the current state.
func (s *Service) Check(ctx context.Context) State {
	s.mu.Lock()
	if s.checking {
		state := s.state
		s.mu.Unlock()
		return state
	}
	s.checking = true
	s.state.Checking = true
	cfg := s.cfg
	s.mu.Unlock()

	updates, missing, err := s.checkAll(ctx, cfg)
	needed, detail := s.reboot()

	s.mu.Lock()
	s.checking = false
	s.state.Checking = false
	s.state.RepoMissing = missing.repo
	s.state.AURMissing = missing.aur
	s.state.FlatpakMissing = missing.flatpak
	if err != nil {
		s.state.CheckErr = err.Error()
	} else {
		s.state.Updates = updates
		s.state.CheckedAt = s.now()
		s.state.CheckErr = ""
	}
	s.state.RebootNeeded = needed
	s.state.RebootDetail = detail
	state := s.state
	state.Updates = append([]Update(nil), s.state.Updates...)
	s.mu.Unlock()
	return state
}

type missingTools struct {
	repo, aur, flatpak bool
}

func (s *Service) checkAll(ctx context.Context, cfg Config) ([]Update, missingTools, error) {
	var missing missingTools
	var updates []Update

	if _, err := s.lookPath("checkupdates"); err != nil {
		missing.repo = true
	} else {
		found, err := CheckRepo(ctx, s.run)
		if err != nil {
			return nil, missing, err
		}
		updates = append(updates, found...)
	}

	helper := ResolveAURHelper(cfg.AURHelper, s.lookPath)
	switch {
	case helper != "":
		found, err := CheckAUR(ctx, s.run, helper)
		if err != nil {
			return nil, missing, err
		}
		updates = append(updates, found...)
	case cfg.AURHelper != "off":
		missing.aur = true
	}

	if cfg.IncludeFlatpak {
		if _, err := s.lookPath("flatpak"); err != nil {
			missing.flatpak = true
		} else {
			found, err := CheckFlatpak(ctx, s.run)
			if err != nil {
				return nil, missing, err
			}
			updates = append(updates, found...)
		}
	}

	return updates, missing, nil
}

// ResolveAURHelper picks the helper to run for a setting. "off" disables
// AUR checks; an explicitly named helper must be installed; "auto" and the
// empty setting take paru, then yay, then nothing.
func ResolveAURHelper(setting string, lookPath func(string) (string, error)) string {
	switch setting {
	case "off":
		return ""
	case "paru", "yay":
		if _, err := lookPath(setting); err == nil {
			return setting
		}
		return ""
	default:
		for _, helper := range []string{"paru", "yay"} {
			if _, err := lookPath(helper); err == nil {
				return helper
			}
		}
		return ""
	}
}

// Loop checks once after StartDelay, then every interval, until ctx ends.
// onResult runs after each check so the caller can publish. interval is
// asked fresh each cycle so a settings change takes effect next time.
func (s *Service) Loop(ctx context.Context, interval func() time.Duration, onResult func()) {
	delay := s.StartDelay
	if delay <= 0 {
		delay = DefaultStartDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.Check(ctx)
		if onResult != nil {
			onResult()
		}
		next := interval()
		if next <= 0 {
			next = DefaultInterval
		}
		timer.Reset(next)
	}
}

// TakeNotification reports the body of a notification that should go out
// now, if any, and remembers it so updates fire at most once a day and a
// newly-needed reboot fires only once.
func (s *Service) TakeNotification(now time.Time) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.RebootNeeded && !s.rebootNotified {
		s.rebootNotified = true
		return "Restart to finish updating: " + s.state.RebootDetail, true
	}
	if len(s.state.Updates) == 0 {
		return "", false
	}
	day := now.Format("2006-01-02")
	if day == s.lastNotifyDay {
		return "", false
	}
	s.lastNotifyDay = day
	if core := CoreCount(s.state.Updates); core > 0 {
		return fmt.Sprintf("%d updates (%d core)", len(s.state.Updates), core), true
	}
	return fmt.Sprintf("%d updates", len(s.state.Updates)), true
}
