package main

import (
	"path/filepath"
	"testing"
)

func TestValidateCurrentShellManifestFeatures(t *testing.T) {
	seen := make(map[string]string)
	for _, plugin := range []string{"calendar", "notes"} {
		path := filepath.Join("..", "..", "plugins", plugin, "manifest.json")
		if err := validate(path, seen); err != nil {
			t.Errorf("validate %s: %v", plugin, err)
		}
	}
}
