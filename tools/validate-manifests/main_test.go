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

func TestFilesystemRules(t *testing.T) {
	paths := strings.TrimSuffix(strings.Repeat(`"~/allowed",`, 33), ",")
	paths32 := strings.TrimSuffix(strings.Repeat(`"~/allowed",`, 32), ",")
	cases := []struct {
		name   string
		minor  int
		fields string
		want   string
	}{
		{
			name:  "author_and_filesystem_at_minor20",
			minor: 20,
			fields: `,"author":"Nomadcxx","filesystem":{"read":["~/Documents"],` +
				`"read_write":["/var/tmp/plugin"],"sockets":["session-bus","/run/user/1000/plugin.sock"],"signals":"own"}`,
		},
		{name: "filesystem_allows_32_paths", minor: 20, fields: `,"filesystem":{"read":[` + paths32 + `]}`},
		{name: "filesystem_allows_any_signals", minor: 20, fields: `,"filesystem":{"signals":"any"}`},
		{name: "filesystem_below_minor20", minor: 19, fields: `,"filesystem":{"read":["~/Documents"]}`, want: "filesystem requires protocol minor 20"},
		{name: "filesystem_too_many_paths", minor: 20, fields: `,"filesystem":{"read":[` + paths + `]}`, want: "33 paths, more than the 32 allowed"},
		{name: "filesystem_root_path", minor: 20, fields: `,"filesystem":{"read":["/"]}`, want: "grants everything"},
		{name: "filesystem_parent_path", minor: 20, fields: `,"filesystem":{"read":["~/docs/../secrets"]}`, want: "not a clean path inside its root"},
		{name: "filesystem_unknown_socket", minor: 20, fields: `,"filesystem":{"sockets":["~/session-bus"]}`, want: "neither an endpoint"},
		{name: "filesystem_unknown_signal_mode", minor: 20, fields: `,"filesystem":{"signals":"none"}`, want: `not "own" or "any"`},
		{name: "author_over_limit", minor: 8, fields: `,"author":"` + strings.Repeat("a", 257) + `"`, want: "author is 257 bytes, more than the 256 allowed"},
		{name: "negative_protocol_minor", minor: -1, want: "protocol minor -1 is negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateManifestFixture(t, tc.minor, `[]`, tc.fields, "")
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestConfinedSharedManifestFixtureValidates(t *testing.T) {
	path := filepath.Join("..", "testdata", "confined-manifest.json")
	if err := validate(path, map[string]string{}); err != nil {
		t.Fatalf("validate shared confined manifest fixture: %v", err)
	}
}

func TestNarrowCapabilitiesAndHostcallGrants(t *testing.T) {
	cases := []struct {
		name  string
		minor int
		caps  string
		calls string
		want  string
	}{
		{name: "state_read", minor: 19, caps: `["state-read"]`, calls: "CallStateGet CallStateList"},
		{name: "state_write", minor: 19, caps: `["state-write"]`, calls: "CallStateSet"},
		{name: "state_read_does_not_write", minor: 19, caps: `["state-read"]`, calls: "CallStateSet", want: `capability "state-write" is not granted`},
		{name: "state_write_does_not_read", minor: 19, caps: `["state-write"]`, calls: "CallStateGet", want: `capability "state-read" is not granted`},
		{name: "broad_state_grants_both", minor: 18, caps: `["state"]`, calls: "CallStateGet CallStateList CallStateSet"},
		{name: "wallpaper_read", minor: 19, caps: `["wallpaper-read"]`, calls: "CallWallpaperSnapshot"},
		{name: "wallpaper_write", minor: 19, caps: `["wallpaper-write"]`, calls: "CallWallpaperMaskSet"},
		{name: "wallpaper_read_does_not_write", minor: 19, caps: `["wallpaper-read"]`, calls: "CallWallpaperMaskSet", want: `capability "wallpaper-write" is not granted`},
		{name: "wallpaper_write_does_not_read", minor: 19, caps: `["wallpaper-write"]`, calls: "CallWallpaperSnapshot", want: `capability "wallpaper-read" is not granted`},
		{name: "broad_wallpaper_grants_read_and_write", minor: 18, caps: `["wallpaper"]`, calls: "CallWallpaperSnapshot CallWallpaperMaskSet"},
		{name: "screenshot_capture", minor: 19, caps: `["screenshot-capture"]`, calls: "CallScreenshotStart"},
		{name: "screenshot_read", minor: 19, caps: `["screenshot-read"]`, calls: "CallScreenshotDirectory"},
		{name: "screenshot_capture_does_not_read", minor: 19, caps: `["screenshot-capture"]`, calls: "CallScreenshotDirectory", want: `capability "screenshot-read" is not granted`},
		{name: "screenshot_read_does_not_capture", minor: 19, caps: `["screenshot-read"]`, calls: "CallScreenshotStart", want: `capability "screenshot-capture" is not granted`},
		{name: "broad_screenshot_grants_both", minor: 18, caps: `["screenshot"]`, calls: "CallScreenshotStart CallScreenshotDirectory"},
		{name: "files_browser", minor: 19, caps: `["files:browser"]`, calls: "CallFilesBrowse"},
		{name: "broad_files_grants_browser", minor: 18, caps: `["files"]`, calls: "CallFilesBrowse"},
		{name: "outputs_read", minor: 19, caps: `["outputs-read"]`, calls: "CallOutputContext"},
		{name: "panels_grant_outputs_read", minor: 19, caps: `["panels"]`, calls: "CallOutputContext"},
		{name: "floating_surfaces_grant_outputs_read", minor: 19, caps: `["floating_surfaces"]`, calls: "CallOutputContext"},
		{name: "output_context_needs_capability_at_minor19", minor: 19, caps: `[]`, calls: "CallOutputContext", want: `capability "outputs-read" is not granted`},
		{name: "output_context_is_ungated_below_minor19", minor: 18, caps: `[]`, calls: "CallOutputContext"},
		{name: "narrow_capability_needs_minor19", minor: 18, caps: `["state-read"]`, want: `"state-read" requires protocol minor 19`},
		{name: "state_write_needs_minor19", minor: 18, caps: `["state-write"]`, want: `"state-write" requires protocol minor 19`},
		{name: "wallpaper_read_needs_minor19", minor: 18, caps: `["wallpaper-read"]`, want: `"wallpaper-read" requires protocol minor 19`},
		{name: "wallpaper_write_needs_minor19", minor: 18, caps: `["wallpaper-write"]`, want: `"wallpaper-write" requires protocol minor 19`},
		{name: "screenshot_capture_needs_minor19", minor: 18, caps: `["screenshot-capture"]`, want: `"screenshot-capture" requires protocol minor 19`},
		{name: "screenshot_read_needs_minor19", minor: 18, caps: `["screenshot-read"]`, want: `"screenshot-read" requires protocol minor 19`},
		{name: "outputs_read_needs_minor19", minor: 18, caps: `["outputs-read"]`, want: `"outputs-read" requires protocol minor 19`},
		{name: "files_browser_needs_minor19", minor: 18, caps: `["files:browser"]`, want: `"files:browser" requires protocol minor 19`},
		{name: "unknown_files_scope_rejected", minor: 19, caps: `["files:audio"]`, want: `files:audio`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateManifestFixture(t, tc.minor, tc.caps, "", tc.calls)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func validateManifestFixture(t *testing.T, minor int, caps, fields, calls string) error {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if calls != "" {
		cmdDir := filepath.Join(root, "cmd", "sysc-plugin-x")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package main\n\nvar _ = " + calls + "\n"
		if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mani := `{"schema":1,"id":"org.sysc.x","name":"X","description":"d","version":"1.0.0",` +
		`"protocol":{"major":1,"minor":` + strconv.Itoa(minor) + `},"exec":"bin/x","capabilities":` + caps + fields +
		`,"panels":[{"id":"p","width":100,"height":100,"placement":"attached","shortcuts":[{"key":"g","node":"v"}]}]}`
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(mani), 0o644); err != nil {
		t.Fatal(err)
	}
	return validate(path, map[string]string{})
}
