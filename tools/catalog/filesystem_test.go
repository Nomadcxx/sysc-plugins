package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/catalog"
)

// confinedArchiveManifest is the same declaration in the form the release
// archive carries it.
func confinedArchiveManifest() pluginManifest {
	m := archiveManifest("org.sysc.timer", "1.0.0")
	m.Protocol.Minor = 20
	m.Filesystem = &catalog.Filesystem{
		Read:      []string{"~/Documents"},
		ReadWrite: []string{"~/Notes"},
		Sockets:   []string{"session-bus", "/run/user/1000/notes.sock"},
		Signals:   "any",
	}
	return m
}

// confinedRepo builds a repo root whose manifest declares a filesystem, a
// release archive that carries the same declaration, and the tagged manifest
// the validator reads.
func confinedRepo(t *testing.T) (root, dist string) {
	t.Helper()
	root = newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	manifest, err := os.ReadFile(filepath.Join("..", "testdata", "confined-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "plugins", "timer", "manifest.json"), string(manifest))
	withTaggedContentServer(t, manifest)
	dist = t.TempDir()
	writeDistArchiveWithManifest(t, dist, "org.sysc.timer", "1.0.0", "amd64", confinedArchiveManifest(), "v1")
	return root, dist
}

func TestUpdateCopiesTheManifestFilesystemIntoTheCatalog(t *testing.T) {
	root, dist := confinedRepo(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, now); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	cat := readCatalogFile(t, filepath.Join(root, "catalog.json"))
	e := entryByID(t, cat, "org.sysc.timer")
	if e.Filesystem == nil {
		t.Fatal("the catalog row dropped the manifest's filesystem declaration")
	}
	want := &catalog.Filesystem{
		Read:      []string{"~/Documents"},
		ReadWrite: []string{"~/Notes"},
		Sockets:   []string{"session-bus", "/run/user/1000/notes.sock"},
		Signals:   "any",
	}
	if !catalog.SameSet(e.Filesystem.Read, want.Read) ||
		!catalog.SameSet(e.Filesystem.ReadWrite, want.ReadWrite) ||
		!catalog.SameSet(e.Filesystem.Sockets, want.Sockets) ||
		e.Filesystem.SignalAny() != want.SignalAny() {
		t.Fatalf("filesystem = %+v, want %+v", *e.Filesystem, *want)
	}
}

func TestValidateCatalogAcceptsACopiedFilesystem(t *testing.T) {
	root, dist := confinedRepo(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, now); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	var out bytes.Buffer
	if err := validateCatalog(root, false, false, &out); err != nil {
		t.Fatalf("validateCatalog: %v (output: %s)", err, out.String())
	}
	if !strings.Contains(out.String(), "ok   org.sysc.timer") {
		t.Fatalf("expected an ok line for org.sysc.timer, got: %s", out.String())
	}
}

func TestValidateCatalogCatchesACatalogThatDropsTheFilesystem(t *testing.T) {
	root, dist := confinedRepo(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, now); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	// Drop the declaration from the row, the way a catalog written before the
	// field existed reads. The host would then show the plugin as unconfined
	// and refuse the install, so the validator must catch it.
	catalogPath := filepath.Join(root, "catalog.json")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc catalogDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Plugins[0].Filesystem = nil
	data, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = validateCatalog(root, false, false, &out)
	if err == nil {
		t.Fatalf("expected a validation failure, output: %s", out.String())
	}
	if !strings.Contains(out.String(), "FAIL org.sysc.timer") || !strings.Contains(out.String(), "filesystem") {
		t.Fatalf("expected a filesystem mismatch failure, got: %s", out.String())
	}
}
