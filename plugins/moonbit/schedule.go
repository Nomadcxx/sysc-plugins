package moonbit

import (
	"os/exec"
	"strings"
)

// Schedule is what the TUI's Schedule screen shows: daemon mode and the scan
// and clean timers, which systemd keeps mutually exclusive. Reading it needs
// no root.
type Schedule struct {
	Known         bool
	DaemonEnabled bool
	DaemonActive  bool
	ScanTimer     bool
	CleanTimer    bool
}

// TimersEnabled reports whether either timer is enabled.
func (s Schedule) TimersEnabled() bool { return s.ScanTimer || s.CleanTimer }

// ValidSchedule reports whether target and action are the ones the schedule
// screen offers. The screen's node ids are host input, so this is the gate
// that keeps an unknown unit or verb out of the sudo systemctl run.
func ValidSchedule(target, action string) bool {
	switch target {
	case "daemon", "timers":
	default:
		return false
	}
	switch action {
	case "enable", "disable":
		return true
	}
	return false
}

// ReadSchedule asks systemd for the units' state.
func ReadSchedule() Schedule {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return Schedule{}
	}
	return Schedule{
		Known:         true,
		DaemonEnabled: unitIs("is-enabled", "moonbit-daemon.service", "enabled"),
		DaemonActive:  unitIs("is-active", "moonbit-daemon.service", "active"),
		ScanTimer:     unitIs("is-enabled", "moonbit-scan.timer", "enabled"),
		CleanTimer:    unitIs("is-enabled", "moonbit-clean.timer", "enabled"),
	}
}

func unitIs(verb, unit, want string) bool {
	out, _ := exec.Command("systemctl", verb, unit).Output()
	return strings.TrimSpace(string(out)) == want
}
