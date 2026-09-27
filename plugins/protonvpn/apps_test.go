package protonvpn

import (
	"os"
	"path/filepath"
	"testing"
)

// tempBinDir builds a bin dir inside the package checkout: $TMPDIR here is a
// nosuid mount, so a setuid fixture could not carry its bit.
func tempBinDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "bin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func suidCapable(dir string) bool {
	p := filepath.Join(dir, "suid-probe")
	os.WriteFile(p, []byte("x"), 0o644)
	defer os.Remove(p)
	os.Chmod(p, 0o755|os.ModeSetuid)
	fi, _ := os.Stat(p)
	return fi.Mode()&os.ModeSetuid != 0
}

func TestScanApps(t *testing.T) {
	bin := tempBinDir(t)
	if !suidCapable(bin) {
		t.Skip("filesystem cannot carry setuid bits")
	}
	for _, name := range []string{"firefox", "code"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A setuid binary is a wrapper around the real app; it must be dropped.
	setuid := filepath.Join(bin, "setuid-tool")
	if err := os.WriteFile(setuid, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(setuid, 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}

	apps := ScanApps([]string{"testdata/applications"}, []string{bin})
	got := map[string]string{}
	for _, a := range apps {
		got[a.Label] = a.Value
	}
	if got["Firefox"] != filepath.Join(bin, "firefox") {
		t.Fatalf("firefox: %v", got)
	}
	if got["Code"] != filepath.Join(bin, "code") {
		t.Fatalf("code: %v", got)
	}
	for _, banned := range []string{"Flatpak App", "Snap App", "Setuid Tool", "Hidden App"} {
		if _, ok := got[banned]; ok {
			t.Fatalf("%s must be excluded", banned)
		}
	}
	// The env-wrapper entry resolves to the same binary and loses dedupe to
	// the shorter label.
	if _, ok := got["Web Kiosk Launcher"]; ok {
		t.Fatalf("duplicate path must collapse to the shortest label: %v", got)
	}
	if len(apps) != 2 {
		t.Fatalf("want 2 apps, got %d: %v", len(apps), got)
	}
}

func TestScanAppsSortedByLabel(t *testing.T) {
	bin := tempBinDir(t)
	for _, name := range []string{"firefox", "code"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	apps := ScanApps([]string{"testdata/applications"}, []string{bin})
	for i := 1; i < len(apps); i++ {
		if apps[i-1].Label > apps[i].Label {
			t.Fatalf("not sorted: %q before %q", apps[i-1].Label, apps[i].Label)
		}
	}
}
