package herdr

import "strconv"

// Settings are the plugin-level settings, parsed from host JSON values.
type Settings struct {
	NotifyOnBlocked     bool
	NotifyOnDone        bool
	ShowStoppedSessions bool
	OutputPreviewLines  int
}

// DefaultSettings returns the settings used when nothing is configured.
func DefaultSettings() Settings {
	return Settings{
		NotifyOnBlocked:     true,
		NotifyOnDone:        false,
		ShowStoppedSessions: true,
		OutputPreviewLines:  20,
	}
}

// ParseSettings overlays recognized keys onto the defaults. Wrong-typed keys
// are ignored; output_preview_lines is clamped to 1..200.
func ParseSettings(values map[string]any) Settings {
	s := DefaultSettings()
	if v, ok := asBool(values["notify_on_blocked"]); ok {
		s.NotifyOnBlocked = v
	}
	if v, ok := asBool(values["notify_on_done"]); ok {
		s.NotifyOnDone = v
	}
	if v, ok := asBool(values["show_stopped_sessions"]); ok {
		s.ShowStoppedSessions = v
	}
	if v, ok := asInt(values["output_preview_lines"]); ok {
		s.OutputPreviewLines = clamp(v, 1, 200)
	}
	return s
}

// WidgetSettings configure the bar widget.
type WidgetSettings struct {
	DisplayMode       string
	HideCountWhenZero bool
}

// DefaultWidgetSettings returns the widget defaults.
func DefaultWidgetSettings() WidgetSettings {
	return WidgetSettings{DisplayMode: "icon_and_count", HideCountWhenZero: true}
}

// ParseWidgetSettings overlays recognized keys onto the widget defaults.
// display_mode only accepts "icon" or "icon_and_count".
func ParseWidgetSettings(values map[string]any) WidgetSettings {
	s := DefaultWidgetSettings()
	if mode, ok := values["display_mode"].(string); ok {
		switch mode {
		case "icon", "icon_and_count":
			s.DisplayMode = mode
		}
	}
	if v, ok := asBool(values["hide_count_when_zero"]); ok {
		s.HideCountWhenZero = v
	}
	return s
}

// asBool accepts a JSON bool or the strings "true"/"false".
func asBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch t {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

// asInt accepts JSON numbers (float64) and their string/Go forms.
func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n, true
		}
	}
	return 0, false
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
