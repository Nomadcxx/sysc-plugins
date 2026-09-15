package timer

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Timer is one countdown. Remaining is never negative.
type Timer struct {
	mu        sync.Mutex
	duration  time.Duration
	remaining time.Duration
	running   bool
	origin    time.Time
	now       func() time.Time
	fired     bool
}

func New(now func() time.Time) *Timer {
	if now == nil {
		now = time.Now
	}
	return &Timer{duration: 5 * time.Minute, remaining: 5 * time.Minute, now: now}
}

// ParseDuration accepts 90, 90s, 5m, 1h, and m:ss.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("negative duration")
		}
		return time.Duration(n) * time.Second, nil
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) != 2 {
			return 0, fmt.Errorf("duration %q", s)
		}
		m, err1 := strconv.Atoi(parts[0])
		sec, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || m < 0 || sec < 0 || sec > 59 {
			return 0, fmt.Errorf("duration %q", s)
		}
		return time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("duration %q", s)
	}
	return d, nil
}

func (t *Timer) SetDuration(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if d < 0 {
		d = 0
	}
	t.duration = d
	if !t.running {
		t.remaining = d
		t.fired = false
	}
}

func (t *Timer) Start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running || t.remaining <= 0 {
		return
	}
	t.running = true
	t.origin = t.now()
	t.fired = false
}

func (t *Timer) Pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return
	}
	t.remaining = t.leftLocked(t.now())
	t.running = false
}

func (t *Timer) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = false
	t.remaining = t.duration
	t.fired = false
}

// Tick advances a running countdown. done is true once, on the transition
// through zero, so the plugin can send one completion notification.
func (t *Timer) Tick() (remaining time.Duration, done bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	left := t.leftLocked(t.now())
	if t.running && left == 0 && !t.fired {
		t.fired = true
		t.running = false
		t.remaining = 0
		return 0, true
	}
	return left, false
}

func (t *Timer) Remaining() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.leftLocked(t.now())
}

func (t *Timer) Running() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

func (t *Timer) Duration() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.duration
}

// Restore resumes a countdown that was saved as an absolute deadline.
func (t *Timer) Restore(deadline time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	left := deadline.Sub(now)
	if left <= 0 {
		t.remaining = 0
		t.running = false
		t.fired = true
		return
	}
	t.remaining = left
	t.duration = left
	t.running = true
	t.origin = now
	t.fired = false
}

func (t *Timer) Deadline() (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return time.Time{}, false
	}
	return t.origin.Add(t.remaining), true
}

func (t *Timer) leftLocked(now time.Time) time.Duration {
	if !t.running {
		if t.remaining < 0 {
			return 0
		}
		return t.remaining
	}
	left := t.remaining - now.Sub(t.origin)
	if left < 0 {
		return 0
	}
	return left
}

// FormatMMSS renders remaining time. Values below zero render as 00:00.
func FormatMMSS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}
