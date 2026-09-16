package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestFromManifestReadsTheShippedManifest(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]string{"id": "org.sysc.timer", "name": "Timer", "version": "1.1.0"}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// The helper resolves relative to the running executable; emulate the
	// installed layout by pointing a fake binary inside bin/.
	fake := filepath.Join(bin, "sysc-plugin-timer")
	if err := os.WriteFile(fake, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := fromManifestAt(filepath.Join(dir, "manifest.json"), v1.Identity{Version: "fallback"})
	if got.ID != "org.sysc.timer" || got.Version != "1.1.0" {
		t.Fatalf("identity = %+v", got)
	}
}

func TestFromManifestFallsBackWhenUnreadable(t *testing.T) {
	fallback := v1.Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.1.0"}
	got := fromManifestAt(filepath.Join(t.TempDir(), "missing", "manifest.json"), fallback)
	if got != fallback {
		t.Fatalf("identity = %+v, want the fallback", got)
	}
}
