package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// pluginManifest is the subset of a plugins/<dir>/manifest.json this tool
// needs. It decodes leniently (unknown fields such as widgets, panels and
// settings are ignored) since sysc-shell's plugin/v1 and internal/plugin
// packages own the full, strict manifest schema; this tool only reads the
// fields a catalog row carries forward.
type pluginManifest struct {
	Schema      int    `json:"schema"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Exec        string `json:"exec"`
	Protocol    struct {
		Major int `json:"major"`
		Minor int `json:"minor"`
	} `json:"protocol"`
	Capabilities []string `json:"capabilities"`
	Requires     struct {
		Commands []string `json:"commands"`
	} `json:"requires"`
}

// readManifest reads and lightly sanity-checks pluginDir/manifest.json.
func readManifest(pluginDir string) (pluginManifest, error) {
	path := filepath.Join(pluginDir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return pluginManifest{}, err
	}
	var m pluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return pluginManifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if m.ID == "" || m.Version == "" || m.Exec == "" {
		return pluginManifest{}, fmt.Errorf("%s: missing id, version or exec", path)
	}
	return m, nil
}

// pluginDirs lists the plugin directories under repoRoot/plugins, keyed by
// manifest id, so validate and update can find a plugin by the id a catalog
// row names without assuming the id's last label matches the directory.
func pluginDirsByID(repoRoot string) (map[string]string, error) {
	roots, err := filepath.Glob(filepath.Join(repoRoot, "plugins", "*"))
	if err != nil {
		return nil, err
	}
	byID := make(map[string]string, len(roots))
	for _, dir := range roots {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		m, err := readManifest(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		byID[m.ID] = filepath.Base(dir)
	}
	return byID, nil
}
