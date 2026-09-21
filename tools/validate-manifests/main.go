// Command validate-manifests checks every plugins/*/manifest.json for the
// structural rules the sysc-shell host enforces at discovery time, so CI
// catches breakage before a plugin is installed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type manifest struct {
	Schema   int    `json:"schema"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Exec     string `json:"exec"`
	Protocol struct {
		Major int `json:"major"`
		Minor int `json:"minor"`
	} `json:"protocol"`
	Capabilities []string `json:"capabilities"`
	Requires     struct {
		Commands []string `json:"commands"`
	} `json:"requires"`
	Services []struct {
		ID string `json:"id"`
	} `json:"services"`
	Widgets []struct {
		ID       string    `json:"id"`
		Settings []setting `json:"settings"`
	} `json:"widgets"`
	Panels []struct {
		ID              string `json:"id"`
		Width           int    `json:"width"`
		Height          int    `json:"height"`
		Placement       string `json:"placement"`
		IncludeSettings bool   `json:"include_settings"`
	} `json:"panels"`
	Settings []setting `json:"settings"`
}

// setting is one manifest settings row, at plugin scope or inside a widget.
type setting struct {
	Key         string         `json:"key"`
	Type        string         `json:"type"`
	Label       string         `json:"label"`
	Default     any            `json:"default"`
	Min         any            `json:"min"`
	Max         any            `json:"max"`
	Options     []any          `json:"options"`
	VisibleWhen map[string]any `json:"visible_when"`
}

var allowedCapabilities = map[string]bool{
	"notifications": true,
	"panels":        true,
	"settings":      true,
	"state":         true,
}

var allowedSettingTypes = map[string]bool{
	"bool": true, "int": true, "float": true, "string": true,
	"select": true, "color": true, "file": true, "folder": true,
}

func main() {
	roots, err := filepath.Glob("plugins/*")
	if err != nil {
		fatal(err)
	}
	if len(roots) == 0 {
		fatal(fmt.Errorf("no plugin directories found under plugins/"))
	}

	seenIDs := map[string]string{}
	failures := 0
	for _, dir := range roots {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		path := filepath.Join(dir, "manifest.json")
		if err := validate(path, seenIDs); err != nil {
			fmt.Printf("FAIL %s: %v\n", path, err)
			failures++
		} else {
			fmt.Printf("ok   %s\n", path)
		}
	}
	if failures > 0 {
		os.Exit(1)
	}
}

func validate(path string, seenIDs map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	switch {
	case m.Schema != 1:
		return fmt.Errorf("schema must be 1, got %d", m.Schema)
	case m.Name == "":
		return fmt.Errorf("name is required")
	case m.Version == "":
		return fmt.Errorf("version is required")
	case !v1.ValidPluginID(m.ID):
		return fmt.Errorf("id %q is not a valid reverse-domain plugin id", m.ID)
	case m.Protocol.Major != 1:
		return fmt.Errorf("protocol.major must be 1, got %d", m.Protocol.Major)
	case m.Exec == "":
		return fmt.Errorf("exec is required")
	}

	if prev, dup := seenIDs[m.ID]; dup {
		return fmt.Errorf("id %q already used by %s", m.ID, prev)
	}
	seenIDs[m.ID] = path

	if filepath.IsAbs(m.Exec) || strings.HasPrefix(m.Exec, "..") {
		return fmt.Errorf("exec %q must be a relative path inside the plugin dir", m.Exec)
	}

	for _, c := range m.Capabilities {
		if !allowedCapabilities[c] {
			return fmt.Errorf("unknown capability %q", c)
		}
	}
	if _, err := validateSettings(m.Settings); err != nil {
		return err
	}
	for i, w := range m.Widgets {
		if w.ID == "" {
			return fmt.Errorf("widgets[%d] has empty id", i)
		}
		// Instance-scope settings are declared and validated by position:
		// the same rules the plugin-level list obeys.
		if _, err := validateSettings(w.Settings); err != nil {
			return fmt.Errorf("widgets[%d]: %w", i, err)
		}
	}
	for i, p := range m.Panels {
		if p.ID == "" {
			return fmt.Errorf("panels[%d] has empty id", i)
		}
		if p.Width < 64 || p.Width > 4096 || p.Height < 64 || p.Height > 4096 {
			return fmt.Errorf("panels[%d] size %dx%d outside 64..4096", i, p.Width, p.Height)
		}
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "validate-manifests:", err)
	os.Exit(1)
}

// validateSettings checks one settings list: keys unique and typed, and any
// visible_when shaped exactly as the host expects — an object carrying
// "key" and "equals", where the key names a setting declared in this list.
func validateSettings(settings []setting) (map[string]bool, error) {
	keys := map[string]bool{}
	for _, s := range settings {
		if s.Key == "" {
			return nil, fmt.Errorf("setting with empty key")
		}
		if keys[s.Key] {
			return nil, fmt.Errorf("setting %q declared twice", s.Key)
		}
		if !allowedSettingTypes[s.Type] {
			return nil, fmt.Errorf("setting %q has unknown type %q", s.Key, s.Type)
		}
		keys[s.Key] = true
	}
	for _, s := range settings {
		if s.VisibleWhen == nil {
			continue
		}
		key, hasKey := s.VisibleWhen["key"]
		_, hasEquals := s.VisibleWhen["equals"]
		if !hasKey || !hasEquals || len(s.VisibleWhen) != 2 {
			return nil, fmt.Errorf("setting %q: visible_when must be an object with exactly \"key\" and \"equals\"", s.Key)
		}
		keyStr, isString := key.(string)
		if !isString || keyStr == "" {
			return nil, fmt.Errorf("setting %q: visible_when.key must name a setting", s.Key)
		}
		if !keys[keyStr] {
			return nil, fmt.Errorf("setting %q: visible_when key %q is not a declared setting", s.Key, keyStr)
		}
	}
	return keys, nil
}
