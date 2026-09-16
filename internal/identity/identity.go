// Package identity builds a plugin's handshake identity from its manifest so
// the version a plugin declares can never drift from the version its
// manifest carries. The host rejects a mismatch, and a hardcoded version
// string drifts the moment a manifest is bumped.
package identity

import (
	"encoding/json"
	"os"
	"path/filepath"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// FromManifest reads the manifest that ships beside the running executable
// -- the executable lives at <plugin dir>/bin/<name>, so the manifest is one
// directory up -- and returns the identity it declares. fallback is returned
// when the manifest cannot be read (a go run, a test binary, a layout the
// helper does not recognise), so callers keep working with a pinned value
// instead of failing to start.
func FromManifest(fallback v1.Identity) v1.Identity {
	exe, err := os.Executable()
	if err != nil {
		return fallback
	}
	return fromManifestAt(filepath.Join(filepath.Dir(filepath.Dir(exe)), "manifest.json"), fallback)
}

func fromManifestAt(path string, fallback v1.Identity) v1.Identity {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	var m struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return fallback
	}
	if m.ID == "" || m.Name == "" || m.Version == "" {
		return fallback
	}
	return v1.Identity{ID: m.ID, Name: m.Name, Version: m.Version}
}
