package thinkpad

import "time"

// Config is the plugin-wide configuration the host hands down. The host
// validates and persists it, so everything here survives a reboot with no help
// from the plugin.
type Config struct {
	// ActiveProfile names the profile replayed when the plugin starts.
	ActiveProfile string
	// ApplyOnStart turns that replay on. With it off the plugin only observes,
	// which is what you want if something else owns the fan.
	ApplyOnStart bool
	// LogoPath overrides the built-in wordmark in the panel header.
	LogoPath string
	// PollInterval is how often the hardware is re-read.
	PollInterval       time.Duration
	WarnThermal        bool
	ThermalLimitC      int
	NotifyOnWriteError bool
}

// DefaultConfig mirrors the manifest defaults.
func DefaultConfig() Config {
	return Config{
		ActiveProfile:      "",
		ApplyOnStart:       true,
		LogoPath:           "",
		PollInterval:       2500 * time.Millisecond,
		WarnThermal:        true,
		ThermalLimitC:      85,
		NotifyOnWriteError: true,
	}
}

// ParseConfig reads committed settings, clamping anything to the range the
// manifest declares so a hand-edited config file cannot produce a value the
// UI has no room to show.
func ParseConfig(values map[string]any) Config {
	cfg := DefaultConfig()

	if v, ok := values["active_profile"].(string); ok {
		cfg.ActiveProfile = v
	}
	if v, ok := values["apply_on_start"].(bool); ok {
		cfg.ApplyOnStart = v
	}
	if v, ok := values["logo_path"].(string); ok {
		cfg.LogoPath = v
	}
	if v, ok := values["poll_interval_ms"].(float64); ok {
		cfg.PollInterval = time.Duration(clampInt(int(v), 1000, 10000)) * time.Millisecond
	}
	if v, ok := values["warn_thermal"].(bool); ok {
		cfg.WarnThermal = v
	}
	if v, ok := values["thermal_limit_c"].(float64); ok {
		cfg.ThermalLimitC = clampInt(int(v), 60, 110)
	}
	if v, ok := values["notify_on_write_error"].(bool); ok {
		cfg.NotifyOnWriteError = v
	}
	cfg.PollInterval = clampDuration(cfg.PollInterval, time.Second, 10*time.Second)
	return cfg
}

// clampInt bounds a value to the range the manifest declares, so a hand-edited
// config file cannot produce a number the UI has no room to show.
// DurationFromMS converts the manifest's millisecond poll interval.
func DurationFromMS(ms int) time.Duration {
	return clampDuration(time.Duration(ms)*time.Millisecond,
		time.Second, 10*time.Second)
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// clampDuration bounds a duration to a sane polling window.
func clampDuration(d, low, high time.Duration) time.Duration {
	if d < low {
		return low
	}
	if d > high {
		return high
	}
	return d
}
