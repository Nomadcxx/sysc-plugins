package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// writeDistArchive drops a small release archive into dist, with content
// controlling its sha256 so tests can tell releases apart.
func writeDistArchive(t *testing.T, dist, id, version, arch, content string) {
	t.Helper()
	writeDistArchiveWithManifest(t, dist, id, version, arch, archiveManifest(id, version), content)
}

func archiveManifest(id, version string) pluginManifest {
	dir := strings.TrimPrefix(id, "org.sysc.")
	name := dir
	if id == "org.sysc.timer" {
		name = "Pomodoro Timer"
	}
	m := pluginManifest{
		Schema: 1, ID: id, Name: name, Description: "A test plugin.",
		Version: version, Exec: "bin/sysc-plugin-" + dir,
		Capabilities: []string{"panels"},
	}
	m.Protocol.Major, m.Protocol.Minor = 1, 2
	m.Requires.Commands = []string{}
	return m
}

func writeDistArchiveWithManifest(t *testing.T, dist, id, archiveVersion, arch string, manifest pluginManifest, content string) {
	t.Helper()
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dist, fmt.Sprintf("%s-%s-linux-%s.tar.gz", id, archiveVersion, arch))
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{name: id + "/manifest.json", data: manifestData},
		{name: id + "/payload", data: []byte(content)},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(entry.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
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
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, now); err != nil {
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

// withReadmeServer points readmeBaseURL at a server that serves body for the
// tag-pinned README path, and returns the base URL.
func withReadmeServer(t *testing.T, status int, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	old := taggedFileBaseURL
	taggedFileBaseURL = srv.URL
	t.Cleanup(func() { taggedFileBaseURL = old })
	return srv.URL
}

func withTaggedContentServer(t *testing.T, manifest []byte) {
	t.Helper()
	oldURL, oldTransport := taggedFileBaseURL, catalogHTTPClient.Transport
	taggedFileBaseURL = "https://catalog-test.invalid"
	catalogHTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "catalog-test.invalid" {
			return http.DefaultTransport.RoundTrip(r)
		}
		status, body := http.StatusNotFound, []byte(nil)
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			status, body = http.StatusOK, manifest
		}
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status),
			Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r,
		}, nil
	})
	t.Cleanup(func() {
		taggedFileBaseURL, catalogHTTPClient.Transport = oldURL, oldTransport
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdatePinsReadmeWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		wantRead bool
	}{
		{name: "tag without README", status: http.StatusNotFound},
		{name: "README missing from working tree but present in tag", status: http.StatusOK, wantRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
			const body = "# Timer\n\nA simple timer.\n"
			base := withReadmeServer(t, tc.status, []byte(body))
			dist := t.TempDir()
			writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
			if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
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
			if doc.Plugins[0].Readme.URL != base+"/Nomadcxx/sysc-plugins/timer-v1.0.0/plugins/timer/README.md" {
				t.Errorf("readme URL = %q", doc.Plugins[0].Readme.URL)
			}
			if doc.Plugins[0].Readme.SHA256 != hex.EncodeToString(sum[:]) {
				t.Errorf("readme sha256 = %q, want %x", doc.Plugins[0].Readme.SHA256, sum)
			}
		})
	}
}

// The catalog job checks out main, so a README edited after the tag must not
// leak into the pinned hash: the hash has to come from the tag URL's bytes.
func TestUpdatePinsReadmeFromTagNotWorkingTree(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	const tagged = "# Timer\n\nTagged bytes.\n"
	base := withReadmeServer(t, http.StatusOK, []byte(tagged))
	writeFile(t, filepath.Join(root, "plugins", "timer", "README.md"), "# Timer\n\nWorking tree bytes, edited after the tag.\n")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	cat := readCatalogFile(t, filepath.Join(root, "catalog.json"))
	e := entryByID(t, cat, "org.sysc.timer")
	if e.Readme == nil {
		t.Fatal("expected a pinned readme")
	}
	sum := sha256.Sum256([]byte(tagged))
	if e.Readme.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("readme sha256 = %s, want tag bytes %x", e.Readme.SHA256, sum)
	}
	if e.Readme.URL != base+"/Nomadcxx/sysc-plugins/timer-v1.0.0/plugins/timer/README.md" {
		t.Fatalf("readme URL = %q", e.Readme.URL)
	}
}

func TestUpdateUsesTaggedArchiveManifest(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Working Tree Name", "1.0.0")
	working := archiveManifest("org.sysc.timer", "1.0.0")
	working.Name = "Edited on main"
	working.Description = "Main description"
	working.Protocol.Minor = 99
	working.Capabilities = []string{"main-only"}
	working.Requires.Commands = []string{"main-only"}
	workingData, err := json.Marshal(working)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "plugins", "timer", "manifest.json"), string(workingData))

	tagged := archiveManifest("org.sysc.timer", "1.0.0")
	tagged.Name = "Tagged Name"
	tagged.Description = "Description from the release tag"
	tagged.Capabilities = []string{"panels", "state"}
	tagged.Requires.Commands = []string{"moonbit"}
	dist := t.TempDir()
	writeDistArchiveWithManifest(t, dist, tagged.ID, tagged.Version, "amd64", tagged, "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), tagged.ID)
	if e.Name != tagged.Name || e.Description != tagged.Description {
		t.Fatalf("listing fields = %q / %q, want tagged %q / %q", e.Name, e.Description, tagged.Name, tagged.Description)
	}
	if e.Release.Protocol.Major != tagged.Protocol.Major || e.Release.Protocol.Minor != tagged.Protocol.Minor ||
		strings.Join(e.Release.Capabilities, ",") != strings.Join(tagged.Capabilities, ",") ||
		strings.Join(e.Release.Requires.Commands, ",") != strings.Join(tagged.Requires.Commands, ",") {
		t.Fatalf("release manifest fields = %+v, want tagged manifest %+v", e.Release, tagged)
	}
}

func TestUpdateRejectsArchiveWithWrongExec(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "valid archive")

	wrongExec := archiveManifest("org.sysc.timer", "1.0.0")
	wrongExec.Exec = "bin/sysc-plugin-other"
	writeDistArchiveWithManifest(t, dist, wrongExec.ID, wrongExec.Version, "arm64", wrongExec, "wrong exec")

	err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "manifest exec") {
		t.Fatalf("expected wrong-Exec archive to fail, got %v", err)
	}
}

func TestUpdateThenValidateUsesTaggedManifest(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Working Tree Name", "1.0.0")
	tagged := archiveManifest("org.sysc.timer", "1.0.0")
	tagged.Name = "Tagged Name"
	tagged.Description = "Description from the release tag"
	tagged.Protocol.Minor = 7
	tagged.Capabilities = []string{"panels", "state"}
	tagged.Requires.Commands = []string{"moonbit"}
	taggedBytes, err := json.Marshal(tagged)
	if err != nil {
		t.Fatal(err)
	}
	withTaggedContentServer(t, taggedBytes)

	dist := t.TempDir()
	writeDistArchiveWithManifest(t, dist, tagged.ID, tagged.Version, "amd64", tagged, "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}

	var out bytes.Buffer
	if err := validateCatalog(root, false, false, &out); err != nil {
		t.Fatalf("validateCatalog after update: %v (output: %s)", err, out.String())
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), tagged.ID)
	if e.Name != tagged.Name || e.Description != tagged.Description || e.Protocol.Minor != tagged.Protocol.Minor {
		t.Fatalf("catalog row = %+v, want tagged manifest fields %+v", e, tagged)
	}
}

func TestCatalogHTTPRequestsHaveTimeout(t *testing.T) {
	oldTimeout := catalogHTTPClient.Timeout
	catalogHTTPClient.Timeout = 20 * time.Millisecond
	t.Cleanup(func() { catalogHTTPClient.Timeout = oldTimeout })

	const body = "tagged README bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	oldBase := taggedFileBaseURL
	taggedFileBaseURL = srv.URL
	t.Cleanup(func() { taggedFileBaseURL = oldBase })

	if _, err := readPluginReadme(defaultRepo, "timer", "timer-v1.0.0"); err == nil {
		t.Fatal("README fetch should time out")
	}
	sum := sha256.Sum256([]byte(body))
	if err := checkFetchable(srv.URL, hex.EncodeToString(sum[:]), int64(len(body)), int64(len(body))); err == nil {
		t.Fatal("catalog validation fetch should time out")
	}
}

func TestUpdateRefusesInvalidMetaCategory(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	writeFile(t, filepath.Join(root, catalogMetaFile), `{
		"org.sysc.timer": {"category": "games", "author": "Nomadcxx"}
	}`)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "games") {
		t.Fatalf("expected a category error naming games, got %v", err)
	}
}

func TestUpdateMovesOldReleaseToFrontOfReleases(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	added := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, added); err != nil {
		t.Fatal(err)
	}

	bumpManifestVersion(t, root, "timer", "1.1.0")
	writeDistArchive(t, dist, "org.sysc.timer", "1.1.0", "amd64", "v2")
	updated := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.1.0", dist, updated); err != nil {
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
		if err := updateCatalog(root, defaultRepo, "timer-v"+v, dist, now); err != nil {
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
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	firstSHA := readCatalogFile(t, filepath.Join(root, "catalog.json"))

	dist2 := t.TempDir()
	writeDistArchive(t, dist2, "org.sysc.timer", "1.0.0", "amd64", "rebuilt-same-version")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist2, time.Now().UTC()); err != nil {
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

// The catalog job checks out main, so plugins/<dir>/manifest.json in the
// working tree can carry a version that has moved past the tag. The archives
// were built from the tag and the release publishes the tag, so a moving main
// must not refuse a release that is already correct on its own terms. The
// archive is the version gate; the working tree only supplies the id.
func TestUpdateIgnoresWorkingTreeVersionForTag(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.4.1")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.4.0", "amd64", "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.4.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: main is at 1.4.1 and the tag is 1.4.0: %v", err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Release.Version != "1.4.0" {
		t.Fatalf("release version = %q, want the tagged 1.4.0", e.Release.Version)
	}
}

// The archive is the only version gate now that the working tree is not
// consulted, so the refusal has to come from the archive's own manifest.
func TestUpdateRefusesArchiveVersionDisagreeingWithTag(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchiveWithManifest(t, dist, "org.sysc.timer", "9.9.9", "amd64", archiveManifest("org.sysc.timer", "1.0.0"), "v1")
	err := updateCatalog(root, defaultRepo, "timer-v9.9.9", dist, time.Now().UTC())
	if err == nil {
		t.Fatal("expected an error when the archive manifest version disagrees with the tag")
	}
	if !strings.Contains(err.Error(), "archive manifest has") {
		t.Fatalf("expected the archive to be the version gate, got %v", err)
	}
}

func TestUpdateRefusesUnparsableTag(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	err := updateCatalog(root, defaultRepo, "not-a-tag", t.TempDir(), time.Now().UTC())
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
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err == nil {
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
	if err := updateCatalog(root, defaultRepo, "world-clock-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	writeDistArchive(t, dist, "org.sysc.aiusage", "1.0.0", "amd64", "b")
	if err := updateCatalog(root, defaultRepo, "aiusage-v1.0.0", dist, time.Now().UTC()); err != nil {
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

// A re-run of an already-published tag must leave the file byte-identical:
// the workflow commits only when catalog.json changed, so an unchanged row
// has to keep its original updated_at even though the run passes a new now.
func TestUpdateIdenticalRerunIsNoOp(t *testing.T) {
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "same")

	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "catalog.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	second := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, second); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("identical re-run changed catalog.json\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if e := entryByID(t, readCatalogFile(t, path), "org.sysc.timer"); !e.UpdatedAt.Equal(first) {
		t.Fatalf("updated_at = %v, want the original %v", e.UpdatedAt, first)
	}
}

// A real change under the same tag (the archive was rebuilt) must still move
// updated_at, or the shortcut would hide the new asset.
func TestUpdateRerunWithNewAssetBumpsUpdatedAt(t *testing.T) {
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "first-build")

	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "catalog.json")
	oldSHA := entryByID(t, readCatalogFile(t, path), "org.sysc.timer").Assets["linux-amd64"].SHA256

	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "rebuilt-same-version")
	second := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, second); err != nil {
		t.Fatal(err)
	}

	e := entryByID(t, readCatalogFile(t, path), "org.sysc.timer")
	if e.Assets["linux-amd64"].SHA256 == oldSHA {
		t.Fatal("expected the rebuilt archive to replace the asset sha256")
	}
	if !e.UpdatedAt.Equal(second) {
		t.Fatalf("updated_at = %v, want %v after a rebuilt asset", e.UpdatedAt, second)
	}
}

// Editing catalog-meta.json is a real change too, even when the archive is
// byte-identical.
func TestUpdateMetaChangeBumpsUpdatedAt(t *testing.T) {
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "same")

	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, first); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(root, catalogMetaFile), `{
		"org.sysc.timer": {"category": "productivity", "author": "Nomadcxx", "license": "MIT",
		     "homepage": "https://github.com/Nomadcxx/sysc-plugins",
		     "long_description": "Now with a richer description."}
	}`)
	second := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, second); err != nil {
		t.Fatal(err)
	}

	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.LongDescription != "Now with a richer description." {
		t.Fatalf("long_description = %q, want the meta change", e.LongDescription)
	}
	if !e.UpdatedAt.Equal(second) {
		t.Fatalf("updated_at = %v, want %v after a meta change", e.UpdatedAt, second)
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

func releaseVersions(e catalog.Entry) []string {
	versions := make([]string, 0, len(e.Releases))
	for _, r := range e.Releases {
		versions = append(versions, r.Version)
	}
	return versions
}

func TestUpdateOlderTagDoesNotReplaceTopLevel(t *testing.T) {
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	for i, v := range []string{"1.0.0", "1.1.0"} {
		writeDistArchive(t, dist, "org.sysc.timer", v, "amd64", "c-"+v)
		if err := updateCatalog(root, defaultRepo, "timer-v"+v, dist, time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	// A maintenance release on the older line must not demote the newest.
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.1", "amd64", "c-1.0.1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.1", dist, time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Version != "1.1.0" {
		t.Fatalf("top-level release is %s, want 1.1.0 (releases=%v)", e.Version, e.Releases)
	}
	if got := releaseVersions(e); !slices.Equal(got, []string{"1.0.1", "1.0.0"}) {
		t.Fatalf("releases = %v, want [1.0.1 1.0.0]", got)
	}
}

func TestUpdateOlderTagKeepsNewestListingFields(t *testing.T) {
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	for i, v := range []string{"1.0.0", "1.1.0"} {
		writeDistArchive(t, dist, "org.sysc.timer", v, "amd64", "c-"+v)
		if err := updateCatalog(root, defaultRepo, "timer-v"+v, dist, time.Date(2026, 2, i+1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	old := archiveManifest("org.sysc.timer", "1.0.1")
	old.Name = "Legacy Timer"
	old.Description = "The old line."
	writeDistArchiveWithManifest(t, dist, "org.sysc.timer", "1.0.1", "amd64", old, "c-1.0.1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.1", dist, time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Version != "1.1.0" || e.Name != "Pomodoro Timer" || e.Description != "A test plugin." {
		t.Fatalf("older tag changed the listing: version=%s name=%q description=%q", e.Version, e.Name, e.Description)
	}
}

func TestUpdateRerunOfOlderTagReplacesInPlace(t *testing.T) {
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	for i, v := range []string{"1.0.0", "1.1.0"} {
		writeDistArchive(t, dist, "org.sysc.timer", v, "amd64", "c-"+v)
		if err := updateCatalog(root, defaultRepo, "timer-v"+v, dist, time.Date(2026, 3, i+1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	rerun := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "c-1.0.0-rebuilt")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, rerun); err != nil {
		t.Fatal(err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Version != "1.1.0" || len(e.Releases) != 1 || e.Releases[0].Version != "1.0.0" {
		t.Fatalf("rerun of the older tag changed the row: top=%s releases=%v", e.Version, e.Releases)
	}
	if !e.UpdatedAt.Equal(rerun) {
		t.Fatalf("updated_at = %s, want the rerun time %s", e.UpdatedAt, rerun)
	}
}

func TestUpdateReleasesSortedNewestFirst(t *testing.T) {
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	for i, v := range []string{"1.0.0", "1.2.0", "1.1.0"} {
		writeDistArchive(t, dist, "org.sysc.timer", v, "amd64", "c-"+v)
		if err := updateCatalog(root, defaultRepo, "timer-v"+v, dist, time.Date(2026, 4, i+1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Version != "1.2.0" {
		t.Fatalf("top-level release is %s, want 1.2.0", e.Version)
	}
	if got := releaseVersions(e); !slices.Equal(got, []string{"1.1.0", "1.0.0"}) {
		t.Fatalf("releases = %v, want [1.1.0 1.0.0]", got)
	}
}

func TestUpdateUsesRepoForURLs(t *testing.T) {
	var gotReadmePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReadmePath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# Timer\n"))
	}))
	t.Cleanup(srv.Close)
	old := taggedFileBaseURL
	taggedFileBaseURL = srv.URL
	t.Cleanup(func() { taggedFileBaseURL = old })

	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, "acme/plugins", "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	a, ok := e.Assets["linux-amd64"]
	if !ok {
		t.Fatalf("missing linux-amd64 asset: %v", e.Assets)
	}
	const prefix = "https://github.com/acme/plugins/releases/download/timer-v1.0.0/"
	if !strings.HasPrefix(a.URL, prefix) {
		t.Fatalf("asset url = %q, want prefix %q", a.URL, prefix)
	}
	if e.ReleaseNotes != "https://github.com/acme/plugins/releases/tag/timer-v1.0.0" {
		t.Fatalf("release_notes = %q", e.ReleaseNotes)
	}
	if want := "/acme/plugins/timer-v1.0.0/plugins/timer/README.md"; gotReadmePath != want {
		t.Fatalf("readme path = %q, want %q", gotReadmePath, want)
	}
}

func TestUpdateDefaultRepoUnchanged(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	repo, err := resolveRepo("")
	if err != nil || repo != defaultRepo {
		t.Fatalf("resolveRepo() = %q, %v; want %q", repo, err, defaultRepo)
	}
	withReadmeServer(t, http.StatusNotFound, nil)
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, repo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	const want = "https://github.com/Nomadcxx/sysc-plugins/releases/download/timer-v1.0.0/"
	if !strings.HasPrefix(e.Assets["linux-amd64"].URL, want) {
		t.Fatalf("asset url = %q, want prefix %q", e.Assets["linux-amd64"].URL, want)
	}
	if e.ReleaseNotes != "https://github.com/Nomadcxx/sysc-plugins/releases/tag/timer-v1.0.0" {
		t.Fatalf("release_notes = %q", e.ReleaseNotes)
	}
}

func TestRunUpdateRejectsBadRepo(t *testing.T) {
	err := runUpdate([]string{"-repo", "../x", "-tag", "timer-v1.0.0", "-dist", t.TempDir()})
	if err == nil {
		t.Fatal("expected runUpdate to reject a bad -repo")
	}
}
