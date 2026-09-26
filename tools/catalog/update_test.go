package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/catalog"
)

// newFixtureRepo builds a minimal repo root with one plugin manifest and a
// catalog-meta.json row for it, ready for updateCatalog.
func newFixtureRepo(t *testing.T, dir, id, name, version string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugins", dir, "manifest.json"), fmt.Sprintf(`{
		"schema": 1, "id": %q, "name": %q, "description": "A test plugin.",
		"version": %q, "exec": "bin/sysc-plugin-%s",
		"protocol": {"major": 1, "minor": 2},
		"capabilities": ["panels"], "requires": {"commands": []}
	}`, id, name, version, dir))
	writeFile(t, filepath.Join(root, catalogMetaFile), fmt.Sprintf(`{
		%q: {"category": "productivity", "author": "Nomadcxx", "license": "MIT",
		     "homepage": "https://github.com/Nomadcxx/sysc-plugins"}
	}`, id))
	return root
}

// writeDistArchive drops a fake release archive into dist, with content
// controlling its sha256 so tests can tell releases apart.
func writeDistArchive(t *testing.T, dist, id, version, arch, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dist, fmt.Sprintf("%s-%s-linux-%s.tar.gz", id, version, arch)), content)
}

func readCatalogFile(t *testing.T, path string) catalog.Catalog {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Decode(data)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(cat.Rejected) > 0 {
		t.Fatalf("%s has rejected rows: %v", path, cat.Rejected)
	}
	return cat
}

func entryByID(t *testing.T, cat catalog.Catalog, id string) catalog.Entry {
	t.Helper()
	for _, e := range cat.Entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no entry %q in catalog", id)
	return catalog.Entry{}
}

func TestUpdateCreatesFirstRow(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, "timer-v1.0.0", dist, now); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	cat := readCatalogFile(t, filepath.Join(root, "catalog.json"))
	e := entryByID(t, cat, "org.sysc.timer")
	if e.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", e.Version)
	}
	if !e.AddedAt.Equal(now) || !e.UpdatedAt.Equal(now) {
		t.Fatalf("added_at/updated_at = %v/%v, want both %v", e.AddedAt, e.UpdatedAt, now)
	}
	if len(e.Releases) != 0 {
		t.Fatalf("first release must not carry any Releases, got %d", len(e.Releases))
	}
	if _, ok := e.Assets["linux-amd64"]; !ok {
		t.Fatalf("expected a linux-amd64 asset, got %v", e.Assets)
	}
}

func TestUpdatePinsReadmeWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readme   bool
		wantRead bool
	}{
		{name: "without README"},
		{name: "with README", readme: true, wantRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
			const body = "# Timer\n\nA simple timer.\n"
			if tc.readme {
				writeFile(t, filepath.Join(root, "plugins", "timer", "README.md"), body)
			}
			dist := t.TempDir()
			writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
			if err := updateCatalog(root, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
				t.Fatalf("updateCatalog: %v", err)
			}

			data, err := os.ReadFile(filepath.Join(root, "catalog.json"))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Plugins []struct {
					Readme *struct {
						URL    string `json:"url"`
						SHA256 string `json:"sha256"`
					} `json:"readme"`
				} `json:"plugins"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Plugins) != 1 || (doc.Plugins[0].Readme != nil) != tc.wantRead {
				t.Fatalf("readme = %+v, want present %t", doc.Plugins, tc.wantRead)
			}
			if !tc.wantRead {
				return
			}
			sum := sha256.Sum256([]byte(body))
			if doc.Plugins[0].Readme.URL != "https://raw.githubusercontent.com/Nomadcxx/sysc-plugins/timer-v1.0.0/plugins/timer/README.md" {
				t.Errorf("readme URL = %q", doc.Plugins[0].Readme.URL)
			}
			if doc.Plugins[0].Readme.SHA256 != hex.EncodeToString(sum[:]) {
				t.Errorf("readme sha256 = %q, want %x", doc.Plugins[0].Readme.SHA256, sum)
			}
		})
	}
}

func TestUpdateMovesOldReleaseToFrontOfReleases(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	added := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, "timer-v1.0.0", dist, added); err != nil {
		t.Fatal(err)
	}

	bumpManifestVersion(t, root, "timer", "1.1.0")
	writeDistArchive(t, dist, "org.sysc.timer", "1.1.0", "amd64", "v2")
	updated := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, "timer-v1.1.0", dist, updated); err != nil {
		t.Fatal(err)
	}

	cat := readCatalogFile(t, filepath.Join(root, "catalog.json"))
	e := entryByID(t, cat, "org.sysc.timer")
	if e.Version != "1.1.0" {
		t.Fatalf("top-level version = %q, want 1.1.0", e.Version)
	}
	if len(e.Releases) != 1 || e.Releases[0].Version != "1.0.0" {
		t.Fatalf("releases = %+v, want exactly [1.0.0]", e.Releases)
	}
	if !e.AddedAt.Equal(added) {
		t.Fatalf("added_at = %v, want unchanged %v", e.AddedAt, added)
	}
	if !e.UpdatedAt.Equal(updated) {
		t.Fatalf("updated_at = %v, want %v", e.UpdatedAt, updated)
	}
}

func TestUpdateCapsReleasesAtFive(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	versions := []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0", "1.6.0"}
	for i, v := range versions {
		if i > 0 {
			bumpManifestVersion(t, root, "timer", v)
		}
		writeDistArchive(t, dist, "org.sysc.timer", v, "amd64", "content-"+v)
		now := time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC)
		if err := updateCatalog(root, "timer-v"+v, dist, now); err != nil {
			t.Fatalf("update %s: %v", v, err)
		}
	}

	cat := readCatalogFile(t, filepath.Join(root, "catalog.json"))
	e := entryByID(t, cat, "org.sysc.timer")
	if e.Version != "1.6.0" {
		t.Fatalf("top-level version = %q, want 1.6.0", e.Version)
	}
	if len(e.Releases) != releasesCap {
		t.Fatalf("len(releases) = %d, want %d", len(e.Releases), releasesCap)
	}
	want := []string{"1.5.0", "1.4.0", "1.3.0", "1.2.0", "1.1.0"}
	for i, r := range e.Releases {
		if r.Version != want[i] {
			t.Fatalf("releases[%d] = %q, want %q (full: %v)", i, r.Version, want[i], e.Releases)
		}
	}
}

func TestUpdateSameVersionReplacesRatherThanDuplicates(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "first-build")
	if err := updateCatalog(root, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	firstSHA := readCatalogFile(t, filepath.Join(root, "catalog.json"))

	dist2 := t.TempDir()
	writeDistArchive(t, dist2, "org.sysc.timer", "1.0.0", "amd64", "rebuilt-same-version")
	if err := updateCatalog(root, "timer-v1.0.0", dist2, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	cat := readCatalogFile(t, filepath.Join(root, "catalog.json"))
	e := entryByID(t, cat, "org.sysc.timer")
	if len(e.Releases) != 0 {
		t.Fatalf("re-releasing the same version must not duplicate it into releases, got %+v", e.Releases)
	}
	if e.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", e.Version)
	}
	oldAsset := entryByID(t, firstSHA, "org.sysc.timer").Assets["linux-amd64"]
	if e.Assets["linux-amd64"].SHA256 == oldAsset.SHA256 {
		t.Fatal("expected the rebuilt asset's sha256 to replace the old one")
	}
}

func TestUpdateRefusesTagVersionMismatch(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "9.9.9", "amd64", "v1")
	err := updateCatalog(root, "timer-v9.9.9", dist, time.Now().UTC())
	if err == nil {
		t.Fatal("expected an error when the tag version disagrees with the manifest")
	}
}

func TestUpdateRefusesUnparsableTag(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	err := updateCatalog(root, "not-a-tag", t.TempDir(), time.Now().UTC())
	if err == nil {
		t.Fatal("expected an error for a tag that is not <dir>-v<version>")
	}
}

func TestUpdateRefusesMissingCatalogMetaEntry(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	// Remove the catalog-meta.json entry.
	writeFile(t, filepath.Join(root, catalogMetaFile), "{}")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, "timer-v1.0.0", dist, time.Now().UTC()); err == nil {
		t.Fatal("expected an error when catalog-meta.json has no entry for the plugin")
	}
}

func TestUpdateSortsCatalogByID(t *testing.T) {
	root := t.TempDir()
	for _, p := range []struct{ dir, id, name string }{
		{"world-clock", "org.sysc.world-clock", "World Clock"},
		{"aiusage", "org.sysc.aiusage", "AI Usage"},
	} {
		writeFile(t, filepath.Join(root, "plugins", p.dir, "manifest.json"), fmt.Sprintf(`{
			"schema": 1, "id": %q, "name": %q, "description": "d",
			"version": "1.0.0", "exec": "bin/sysc-plugin-%s",
			"protocol": {"major": 1, "minor": 0},
			"capabilities": [], "requires": {"commands": []}
		}`, p.id, p.name, p.dir))
	}
	writeFile(t, filepath.Join(root, catalogMetaFile), `{
		"org.sysc.world-clock": {"category": "utilities", "author": "Nomadcxx"},
		"org.sysc.aiusage": {"category": "monitoring", "author": "Nomadcxx"}
	}`)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.world-clock", "1.0.0", "amd64", "a")
	if err := updateCatalog(root, "world-clock-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	writeDistArchive(t, dist, "org.sysc.aiusage", "1.0.0", "amd64", "b")
	if err := updateCatalog(root, "aiusage-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Plugins) != 2 || doc.Plugins[0].ID != "org.sysc.aiusage" || doc.Plugins[1].ID != "org.sysc.world-clock" {
		t.Fatalf("catalog.json plugins are not sorted by id: %+v", doc.Plugins)
	}
}

func bumpManifestVersion(t *testing.T, root, dir, version string) {
	t.Helper()
	path := filepath.Join(root, "plugins", dir, "manifest.json")
	m, err := readManifest(filepath.Join(root, "plugins", dir))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, fmt.Sprintf(`{
		"schema": 1, "id": %q, "name": %q, "description": "A test plugin.",
		"version": %q, "exec": %q,
		"protocol": {"major": 1, "minor": 2},
		"capabilities": ["panels"], "requires": {"commands": []}
	}`, m.ID, m.Name, version, m.Exec))
}
