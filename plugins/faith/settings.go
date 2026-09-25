package faith

// Settings are the manifest's settings, decoded.
type Settings struct {
	Tradition      string
	VerseMode      string
	RefreshMinutes int
	Translation    string
	ShowCommentary bool
	ShowReference  bool
	PrayerSeconds  int
}

// Verse modes.
const (
	ModePool  = "pool"
	ModeDaily = "daily"
	ModeBible = "bible"
)

// DefaultSettings match manifest.json.
func DefaultSettings() Settings {
	return Settings{
		Tradition:      Ecumenical,
		VerseMode:      ModePool,
		RefreshMinutes: 30,
		Translation:    "BSB",
		ShowCommentary: true,
		PrayerSeconds:  30,
	}
}

// Apply merges committed values from a settings.changed message. The host
// has validated them against the manifest, but numbers arrive as float64 and
// an unknown value keeps the current one.
func (s *Settings) Apply(values map[string]any) {
	str := func(key string, ok func(string) bool, dst *string) {
		if v, isStr := values[key].(string); isStr && ok(v) {
			*dst = v
		}
	}
	num := func(key string, lo, hi int, dst *int) {
		switch v := values[key].(type) {
		case float64:
			*dst = clamp(int(v), lo, hi)
		case int:
			*dst = clamp(v, lo, hi)
		}
	}
	flag := func(key string, dst *bool) {
		if v, ok := values[key].(bool); ok {
			*dst = v
		}
	}
	str("tradition", ValidTradition, &s.Tradition)
	str("verse_mode", func(v string) bool { return v == ModePool || v == ModeDaily || v == ModeBible }, &s.VerseMode)
	str("translation", ValidTranslation, &s.Translation)
	num("refresh_minutes", 0, 1440, &s.RefreshMinutes)
	num("prayer_seconds", 0, 300, &s.PrayerSeconds)
	flag("show_commentary", &s.ShowCommentary)
	flag("show_reference", &s.ShowReference)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
