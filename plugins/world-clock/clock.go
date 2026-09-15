package worldclock

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Clock holds an ordered list of IANA zones.
type Clock struct {
	mu            sync.Mutex
	zones         []string
	hour24        bool
	pendingAdd    string
	pendingRemove string
}

func New() *Clock {
	return &Clock{zones: []string{"UTC"}, hour24: true}
}

func (c *Clock) Zones() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.zones))
	copy(out, c.zones)
	return out
}

func (c *Clock) SetHour24(on bool) {
	c.mu.Lock()
	c.hour24 = on
	c.mu.Unlock()
}

func (c *Clock) Hour24() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hour24
}

func (c *Clock) PendingAdd() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingAdd
}

func (c *Clock) PendingRemove() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingRemove
}

func ValidateZone(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty zone")
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("unknown zone %q", name)
	}
	return nil
}

func (c *Clock) ProposeAdd(name string) error {
	if err := ValidateZone(name); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, z := range c.zones {
		if z == name {
			return fmt.Errorf("duplicate zone %q", name)
		}
	}
	c.pendingAdd = name
	c.pendingRemove = ""
	return nil
}

func (c *Clock) ConfirmAdd() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingAdd == "" {
		return
	}
	c.zones = append(c.zones, c.pendingAdd)
	c.pendingAdd = ""
}

func (c *Clock) ProposeRemove(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingRemove = name
	c.pendingAdd = ""
}

func (c *Clock) ConfirmRemove() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingRemove == "" {
		return
	}
	next := c.zones[:0]
	for _, z := range c.zones {
		if z != c.pendingRemove {
			next = append(next, z)
		}
	}
	c.zones = next
	c.pendingRemove = ""
}

func (c *Clock) CancelPending() {
	c.mu.Lock()
	c.pendingAdd, c.pendingRemove = "", ""
	c.mu.Unlock()
}

func (c *Clock) Restore(zones []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.zones = c.zones[:0]
	seen := map[string]bool{}
	for _, z := range zones {
		if ValidateZone(z) != nil || seen[z] {
			continue
		}
		seen[z] = true
		c.zones = append(c.zones, z)
	}
	if len(c.zones) == 0 {
		c.zones = []string{"UTC"}
	}
}

// Reorder moves the item at from in front of insertBefore.
func (c *Clock) Reorder(from, insertBefore int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.zones)
	if from < 0 || from >= n || insertBefore < 0 || insertBefore > n {
		return fmt.Errorf("index")
	}
	if from == insertBefore || from+1 == insertBefore {
		return nil
	}
	item := c.zones[from]
	without := append([]string{}, c.zones[:from]...)
	without = append(without, c.zones[from+1:]...)
	if insertBefore > from {
		insertBefore--
	}
	next := append([]string{}, without[:insertBefore]...)
	next = append(next, item)
	next = append(next, without[insertBefore:]...)
	c.zones = next
	return nil
}

type Reading struct {
	Zone   string
	Clock  string
	Offset string
}

func (c *Clock) Readings(now time.Time) []Reading {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Reading, 0, len(c.zones))
	for _, z := range c.zones {
		loc, err := time.LoadLocation(z)
		if err != nil {
			continue
		}
		out = append(out, Reading{Zone: z, Clock: formatClock(now.In(loc), c.hour24), Offset: formatOffset(now.In(loc))})
	}
	return out
}

func formatClock(t time.Time, hour24 bool) string {
	if hour24 {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

func formatOffset(t time.Time) string {
	_, off := t.Zone()
	h := off / 3600
	m := (off % 3600) / 60
	if m < 0 {
		m = -m
	}
	if h >= 0 {
		if m == 0 {
			return fmt.Sprintf("UTC+%d", h)
		}
		return fmt.Sprintf("UTC+%d:%02d", h, m)
	}
	if m == 0 {
		return fmt.Sprintf("UTC%d", h)
	}
	return fmt.Sprintf("UTC%d:%02d", h, m)
}
