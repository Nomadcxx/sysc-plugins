package worldclock

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Reading is one zone at one instant, formatted for display.
type Reading struct {
	Zone     string
	Label    string
	Clock    string
	Offset   string // "UTC+9", "UTC-3:30"
	Relative string // "+9h", "−3h30", "Same time"
	DayShift int    // the zone's date minus local's date: -1, 0, +1
	Daytime  bool   // local hour in the zone is in [6, 18)
	OnBar    bool
}

var locations sync.Map // zone id -> *time.Location

// loadLocation caches LoadLocation, which reads the zone file on every call;
// readings are recomputed every minute for every zone.
func loadLocation(id string) (*time.Location, error) {
	if loc, ok := locations.Load(id); ok {
		return loc.(*time.Location), nil
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, err
	}
	locations.Store(id, loc)
	return loc, nil
}

func Read(z Zone, now time.Time, local *time.Location, hour24 bool) (Reading, error) {
	if z.ID == "" || z.ID == "Local" {
		return Reading{}, fmt.Errorf("%w: %q", ErrInvalidZone, z.ID)
	}
	loc, err := loadLocation(z.ID)
	if err != nil {
		return Reading{}, fmt.Errorf("%w: %q", ErrInvalidZone, z.ID)
	}
	there, here := now.In(loc), now.In(local)
	_, zoneOff := there.Zone()
	_, localOff := here.Zone()
	return Reading{
		Zone:     z.ID,
		Label:    DisplayLabel(z),
		Clock:    formatClock(there, hour24),
		Offset:   formatOffset(zoneOff),
		Relative: formatRelative(zoneOff - localOff),
		DayShift: dayShift(there, here),
		Daytime:  there.Hour() >= 6 && there.Hour() < 18,
		OnBar:    z.OnBar,
	}, nil
}

// DisplayLabel is the custom label, or the zone's short name when none is set.
func DisplayLabel(z Zone) string {
	if z.Label != "" {
		return z.Label
	}
	return ShortLabel(z.ID)
}

// ShortLabel renders a zone's last path segment with underscores as spaces:
// "America/New_York" becomes "New York".
func ShortLabel(zone string) string {
	if i := strings.LastIndex(zone, "/"); i >= 0 && i+1 < len(zone) {
		zone = zone[i+1:]
	}
	return strings.ReplaceAll(zone, "_", " ")
}

// DayMarker is the compact day-shift suffix cards and the bar show.
func DayMarker(shift int) string {
	switch {
	case shift > 0:
		return "+1"
	case shift < 0:
		return "−1"
	}
	return ""
}

func formatClock(t time.Time, hour24 bool) string {
	if hour24 {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

func formatOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	h, m := seconds/3600, seconds%3600/60
	if m == 0 {
		return fmt.Sprintf("UTC%s%d", sign, h)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, h, m)
}

func formatRelative(seconds int) string {
	if seconds == 0 {
		return "Same time"
	}
	sign := "+"
	if seconds < 0 {
		sign, seconds = "−", -seconds
	}
	h, m := seconds/3600, seconds%3600/60
	switch {
	case m == 0:
		return fmt.Sprintf("%s%dh", sign, h)
	case h == 0:
		return fmt.Sprintf("%s%dm", sign, m)
	}
	return fmt.Sprintf("%s%dh%02d", sign, h, m)
}

func dayShift(there, here time.Time) int {
	a := time.Date(there.Year(), there.Month(), there.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(here.Year(), here.Month(), here.Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case a.After(b):
		return 1
	case a.Before(b):
		return -1
	}
	return 0
}
