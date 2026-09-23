package wallpaperdepth

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestManifestContract(t *testing.T) {
	data, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version      string                     `json:"version"`
		Protocol     struct{ Major, Minor int } `json:"protocol"`
		Capabilities []string                   `json:"capabilities"`
		Panels       []struct {
			ID     string `json:"id"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"panels"`
		Settings []struct {
			Key     string          `json:"key"`
			Default json.RawMessage `json:"default"`
			Min     *int            `json:"min"`
			Max     *int            `json:"max"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", manifest.Version)
	}
	if manifest.Protocol.Major != 1 || manifest.Protocol.Minor != 7 {
		t.Errorf("protocol = %d.%d, want 1.7", manifest.Protocol.Major, manifest.Protocol.Minor)
	}
	if !slices.Contains(manifest.Capabilities, "wallpaper") {
		t.Error("wallpaper capability is missing")
	}
	if slices.Contains(manifest.Capabilities, "notifications") {
		t.Error("notifications capability is unused")
	}
	settings := make(map[string]struct {
		defaultValue json.RawMessage
		min          *int
		max          *int
	}, len(manifest.Settings))
	for _, setting := range manifest.Settings {
		settings[setting.Key] = struct {
			defaultValue json.RawMessage
			min          *int
			max          *int
		}{setting.Default, setting.Min, setting.Max}
	}
	if _, ok := settings["wallpaper_path"]; ok {
		t.Error("manual wallpaper_path setting is present")
	}
	for _, tc := range []struct {
		key  string
		want any
		min  int
		max  int
	}{
		{key: "auto_generate", want: true},
		{key: "threshold", want: 30, min: 0, max: 100},
		{key: "feather", want: 8, min: 0, max: 50},
	} {
		setting, ok := settings[tc.key]
		if !ok {
			t.Errorf("setting %q is missing", tc.key)
			continue
		}
		wantDefault, err := json.Marshal(tc.want)
		if err != nil {
			t.Fatal(err)
		}
		if string(setting.defaultValue) != string(wantDefault) {
			t.Errorf("setting %q default = %s, want %s", tc.key, setting.defaultValue, wantDefault)
		}
		if tc.key == "auto_generate" {
			if setting.min != nil || setting.max != nil {
				t.Errorf("auto_generate has numeric bounds: %v..%v", setting.min, setting.max)
			}
			continue
		}
		if setting.min == nil || setting.max == nil || *setting.min != tc.min || *setting.max != tc.max {
			t.Errorf("setting %q bounds = %v..%v, want %d..%d", tc.key, setting.min, setting.max, tc.min, tc.max)
		}
	}
	if len(manifest.Panels) != 1 || manifest.Panels[0].ID != "panel" || manifest.Panels[0].Width != 500 || manifest.Panels[0].Height != 560 {
		t.Errorf("panel declaration = %+v, want panel 500x560", manifest.Panels)
	}
}
