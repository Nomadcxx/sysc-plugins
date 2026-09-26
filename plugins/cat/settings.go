package cat

import (
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Settings are the plugin's options. The host sends only the values the
// user has stored, so every field starts from the manifest's default.
type Settings struct {
	// Size is the bar glyph's square in logical pixels. The bar view's own
	// height caps it at publish time.
	Size        int
	ShowPercent bool
	// Tone is the theme role the cat paints in while load is ordinary.
	Tone  v1.Tone
	Bands Thresholds
	// AlertAbove paints the cat and its reading in the error tone at or
	// above this load, in whole percent. Zero turns the alert off.
	AlertAbove int
	// SampleEvery is the CPU sampling period.
	SampleEvery time.Duration
	// NapAfter is how long the cat stays up and idles before it naps. Zero
	// sends it straight to sleep, as the reference cats do.
	NapAfter time.Duration
}

// The manifest's bounds, mirrored so a value the host has already checked
// cannot be misread here either.
const (
	minSize, maxSize = 16, 32
	minSample        = 1
	maxSample        = 10
	maxNap           = 600
)

// DefaultSettings matches manifest.json.
func DefaultSettings() Settings {
	return Settings{
		Size:        28,
		Tone:        v1.ToneAccent,
		Bands:       Thresholds{SleepBelow: 10, TopAt: 80},
		AlertAbove:  90,
		SampleEvery: 2 * time.Second,
		NapAfter:    time.Minute,
	}
}

// ParseSettings reads a plugin-scope settings map over the defaults.
func ParseSettings(values map[string]any) Settings {
	s := DefaultSettings()
	if n, ok := number(values["size"]); ok {
		s.Size = min(max(n, minSize), maxSize)
	}
	if b, ok := values["show_percent"].(bool); ok {
		s.ShowPercent = b
	}
	if t, ok := values["tone"].(string); ok {
		switch t {
		case "accent":
			s.Tone = v1.ToneAccent
		case "normal":
			s.Tone = v1.ToneNormal
		case "subtle":
			s.Tone = v1.ToneSubtle
		}
	}
	if n, ok := number(values["sleep_below"]); ok {
		s.Bands.SleepBelow = n
	}
	if n, ok := number(values["top_speed_at"]); ok {
		s.Bands.TopAt = n
	}
	s.Bands = s.Bands.normalized()
	if n, ok := number(values["alert_above"]); ok {
		s.AlertAbove = min(max(n, 0), 100)
	}
	if n, ok := number(values["sample_seconds"]); ok {
		s.SampleEvery = time.Duration(min(max(n, minSample), maxSample)) * time.Second
	}
	if n, ok := number(values["nap_after"]); ok {
		s.NapAfter = time.Duration(min(max(n, 0), maxNap)) * time.Second
	}
	return s
}

// number accepts the JSON number the host delivers.
func number(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// alert reports whether a reading crosses the user's alert line.
func (s Settings) alert(pct int, known bool) bool {
	return known && s.AlertAbove > 0 && pct >= s.AlertAbove
}

// tone is the role the cat paints in for a reading.
func (s Settings) tone(pct int, known bool) v1.Tone {
	if s.alert(pct, known) {
		return v1.ToneError
	}
	return s.Tone
}
