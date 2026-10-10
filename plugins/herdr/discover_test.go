package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSessionList(t *testing.T) {
	data, err := os.ReadFile("testdata/session-list.json")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := parseSessionList(data)
	if err != nil {
		t.Fatalf("parseSessionList: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(sessions))
	}
	if sessions[0].Name != "default" || !sessions[0].Default || sessions[0].Running {
		t.Errorf("default entry = %+v", sessions[0])
	}
	if sessions[1].Name != "demo" || sessions[1].Default || !sessions[1].Running {
		t.Errorf("demo entry = %+v", sessions[1])
	}
	if sessions[1].Socket != "/run/user/1000/herdr/demo.sock" {
		t.Errorf("demo socket = %q", sessions[1].Socket)
	}
	if sessions[2].Name != "old" || sessions[2].Running {
		t.Errorf("old entry = %+v", sessions[2])
	}
}

func TestListSessionsMissingBin(t *testing.T) {
	_, err := ListSessions(context.Background(), "herdr-does-not-exist-xyz")
	if !errors.Is(err, ErrHerdrMissing) {
		t.Fatalf("err = %v, want ErrHerdrMissing", err)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("err = %v, want it to wrap exec.ErrNotFound", err)
	}
}

func TestValidSessionName(t *testing.T) {
	valid := []string{"a", "demo.1", "x_y-z"}
	invalid := []string{"", ".", "..", strings.Repeat("a", 65), "a/b", "a b", "a:b"}
	for _, name := range valid {
		if !ValidSessionName(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	for _, name := range invalid {
		if ValidSessionName(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}

func TestLoadStopped(t *testing.T) {
	ws, err := LoadStopped("testdata")
	if err != nil {
		t.Fatalf("LoadStopped: %v", err)
	}
	if len(ws) != 2 {
		t.Fatalf("got %d workspaces: %+v", len(ws), ws)
	}
	if ws[0].Name != "api" || ws[1].Name != "site" {
		t.Errorf("names = %q, %q; want api, site", ws[0].Name, ws[1].Name)
	}
}

func TestLoadStoppedHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	dir := t.TempDir()
	payload := map[string]any{
		"version":    3,
		"workspaces": []map[string]any{{"workspace_id": "w1", "identity_cwd": home}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := LoadStopped(dir)
	if err != nil {
		t.Fatalf("LoadStopped: %v", err)
	}
	if len(ws) != 1 || ws[0].Name != "~" {
		t.Fatalf("got %+v, want one workspace named ~", ws)
	}
}

func TestLoadStoppedMissing(t *testing.T) {
	ws, err := LoadStopped(t.TempDir())
	if err == nil {
		t.Fatal("want error for missing session.json")
	}
	if ws == nil || len(ws) != 0 {
		t.Fatalf("got %#v, want empty non-nil slice", ws)
	}
}

func TestLoadStoppedCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := LoadStopped(dir)
	if err == nil {
		t.Fatal("want error for corrupt session.json")
	}
	if ws == nil || len(ws) != 0 {
		t.Fatalf("got %#v, want empty non-nil slice", ws)
	}
}
