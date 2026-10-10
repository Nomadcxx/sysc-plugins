package herdr

import "testing"

func TestDefaultSettings(t *testing.T) {
	got := DefaultSettings()
	want := Settings{NotifyOnBlocked: true, NotifyOnDone: false, ShowStoppedSessions: true, OutputPreviewLines: 20}
	if got != want {
		t.Fatalf("DefaultSettings = %+v, want %+v", got, want)
	}
}

func TestParseSettingsOverlay(t *testing.T) {
	got := ParseSettings(map[string]any{
		"notify_on_blocked":     false,
		"notify_on_done":        "true",
		"show_stopped_sessions": "false",
		"output_preview_lines":  "42",
	})
	want := Settings{NotifyOnBlocked: false, NotifyOnDone: true, ShowStoppedSessions: false, OutputPreviewLines: 42}
	if got != want {
		t.Fatalf("ParseSettings = %+v, want %+v", got, want)
	}
}

func TestParseSettingsClampsLines(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{"0", 1},
		{"999", 200},
		{float64(0), 1},
		{float64(999), 200},
		{int(10), 10},
		{int64(150), 150},
		{300, 200},
	}
	for _, tc := range cases {
		got := ParseSettings(map[string]any{"output_preview_lines": tc.in})
		if got.OutputPreviewLines != tc.want {
			t.Errorf("ParseSettings(lines=%v) = %d, want %d", tc.in, got.OutputPreviewLines, tc.want)
		}
	}
}

func TestParseSettingsIgnoresWrongTypes(t *testing.T) {
	def := DefaultSettings()
	cases := []map[string]any{
		{"notify_on_blocked": "yes"},
		{"notify_on_done": 5},
		{"show_stopped_sessions": nil},
		{"output_preview_lines": true},
		{"output_preview_lines": "abc"},
		{"output_preview_lines": nil},
	}
	for _, values := range cases {
		if got := ParseSettings(values); got != def {
			t.Errorf("ParseSettings(%v) = %+v, want defaults %+v", values, got, def)
		}
	}
	if got := ParseSettings(nil); got != def {
		t.Fatalf("ParseSettings(nil) = %+v, want defaults", got)
	}
}

func TestDefaultWidgetSettings(t *testing.T) {
	got := DefaultWidgetSettings()
	want := WidgetSettings{DisplayMode: "icon_and_count", HideCountWhenZero: true}
	if got != want {
		t.Fatalf("DefaultWidgetSettings = %+v, want %+v", got, want)
	}
}

func TestParseWidgetSettings(t *testing.T) {
	got := ParseWidgetSettings(map[string]any{
		"display_mode":         "icon",
		"hide_count_when_zero": false,
	})
	want := WidgetSettings{DisplayMode: "icon", HideCountWhenZero: false}
	if got != want {
		t.Fatalf("ParseWidgetSettings = %+v, want %+v", got, want)
	}

	got = ParseWidgetSettings(map[string]any{
		"display_mode":         "icon_and_count",
		"hide_count_when_zero": "true",
	})
	want = WidgetSettings{DisplayMode: "icon_and_count", HideCountWhenZero: true}
	if got != want {
		t.Fatalf("ParseWidgetSettings = %+v, want %+v", got, want)
	}
}

func TestParseWidgetSettingsRejectsBadDisplayMode(t *testing.T) {
	for _, mode := range []any{"bogus", "", 7, nil} {
		got := ParseWidgetSettings(map[string]any{"display_mode": mode})
		if got.DisplayMode != "icon_and_count" {
			t.Errorf("display_mode=%v gave %q, want default icon_and_count", mode, got.DisplayMode)
		}
	}
	got := ParseWidgetSettings(map[string]any{"hide_count_when_zero": "yes"})
	if got.HideCountWhenZero != true {
		t.Fatalf("wrong-typed bool should keep default true, got %v", got.HideCountWhenZero)
	}
}
