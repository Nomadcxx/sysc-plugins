package protonvpn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadSplitTunnel reads features.split_tunneling out of the ProtonVPN
// settings.json (default ~/.config/Proton/VPN/settings.json, supplied by the
// caller). A missing features or split_tunneling object is an error: the
// plugin only speaks to a settings file it recognizes.
func ReadSplitTunnel(path string) (bool, []string, error) {
	root, err := decodeSettings(path)
	if err != nil {
		return false, nil, err
	}
	st, err := splitTunnelObject(root, path)
	if err != nil {
		return false, nil, err
	}
	enabled, _ := st["enabled"].(bool)
	var apps []string
	if list, ok := st["apps"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				apps = append(apps, s)
			}
		}
	}
	return enabled, apps, nil
}

// WriteSplitTunnel updates features.split_tunneling.{enabled,apps} in place,
// preserving every other key in the file. A malformed file or a missing
// parent object is an error and the file is left untouched — never clobber.
func WriteSplitTunnel(path string, enabled bool, apps []string) error {
	root, err := decodeSettings(path)
	if err != nil {
		return err
	}
	st, err := splitTunnelObject(root, path)
	if err != nil {
		return err
	}
	st["enabled"] = enabled
	st["apps"] = apps
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	return writeAtomic(path, out.Bytes())
}

// writeAtomic replaces path by renaming a complete temp file in the same
// directory. os.WriteFile truncates the live ProtonVPN settings first, so a
// crash or a concurrent reader can observe a partial document.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".protonvpn-tmp-")
	if err != nil {
		return fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// decodeSettings reads and parses the settings file, failing loudly on an
// unreadable or malformed one.
func decodeSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("protonvpn: %s: %w", path, err)
	}
	return root, nil
}

// splitTunnelObject walks to features.split_tunneling, erroring when a parent
// object is missing rather than inventing structure in someone else's file.
func splitTunnelObject(root map[string]any, path string) (map[string]any, error) {
	features, ok := root["features"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("protonvpn: %s: no features object", path)
	}
	st, ok := features["split_tunneling"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("protonvpn: %s: no split_tunneling object", path)
	}
	return st, nil
}
