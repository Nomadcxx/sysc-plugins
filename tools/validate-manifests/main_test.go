package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// The validator must reject what the shell rejects at discovery or dispatch:
// panel shortcuts below protocol minor 8, a placement it does not know or
// center placement below minor 12, and gated hostcalls whose capability the
// manifest does not grant.
func TestHostRulesMirrored(t *testing.T) {
	cases := []struct {
		name      string
		calls     string // hostcall token to plant in the fake plugin source
		minor     int
		caps      string // JSON capability list
		wantErr   string
		placement string // defaults to "attached"
	}{
		{"shortcuts_need_minor8", "", 7, `["panels"]`, "shortcuts require protocol minor 8", ""},
		{"hostcall_needs_capability", "CallSurfaceOpen", 8, `["panels"]`, `capability "floating_surfaces" is not granted`, ""},
		{"granted_and_new_enough", "CallSurfaceOpen", 8, `["panels", "floating_surfaces"]`, "", ""},
		{"screenshot_needs_capability", "CallScreenshotStart", 10, `["panels"]`, `capability "screenshot" is not granted`, ""},
		{"screenshot_directory_needs_capability", "CallScreenshotDirectory", 10, `["panels"]`, `capability "screenshot" is not granted`, ""},
		{"screenshot_granted", "CallScreenshotStart", 10, `["panels", "screenshot"]`, "", ""},
		{"files_needs_capability", "CallFilesBrowse", 16, `["panels"]`, `capability "files" is not granted`, ""},
		{"files_granted", "CallFilesBrowse", 16, `["panels", "files"]`, "", ""},
		{"center_needs_minor12", "", 11, `["panels"]`, "center placement requires protocol minor 12", "center"},
		{"center_at_minor12", "", 12, `["panels"]`, "", "center"},
		{"unknown_placement", "", 12, `["panels"]`, `placement "floating" is not one the shell supports`, "floating"},
	}
	for _, tc := range cases {
		if tc.placement == "" {
			tc.placement = "attached"
		}
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "plugins", "x")
			cmdDir := filepath.Join(root, "cmd", "sysc-plugin-x")
			if err := os.MkdirAll(cmdDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			src := "package main\n\nvar _ = " + tc.calls + "\n"
			if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			mani := `{"schema":1,"id":"org.sysc.x","name":"X","description":"d","version":"1.0.0",` +
				`"protocol":{"major":1,"minor":` + strconv.Itoa(tc.minor) + `},` +
				`"exec":"bin/x","capabilities":` + tc.caps + `,"panels":[{"id":"p","width":100,"height":100,"placement":"` + tc.placement + `",` +
				`"shortcuts":[{"key":"g","node":"v"}]}]}`
			path := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(path, []byte(mani), 0o644); err != nil {
				t.Fatal(err)
			}
			err := validate(path, map[string]string{})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("got %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}
