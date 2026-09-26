package main

import (
	"path/filepath"
	"testing"
)

// Every manifest the repo ships must pass, so the validator cannot drift
// behind the host's manifest schema unnoticed.
func TestShippedManifestsValidate(t *testing.T) {
	paths, err := filepath.Glob("../../plugins/*/manifest.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no manifests found: %v", err)
	}
	seen := map[string]string{}
	for _, path := range paths {
		if err := validate(path, seen); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestUnknownCapabilityIsRejected(t *testing.T) {
	if allowedCapabilities["teleport"] {
		t.Fatal("unknown capability allowed")
	}
}
