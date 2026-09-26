package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildValidatingRepo produces a repo root with catalog.json already holding
// one valid row for org.sysc.timer, its manifest and catalog-meta.json entry
// in agreement, ready for validateCatalog.
func buildValidatingRepo(t *testing.T) string {
	t.Helper()
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("seed updateCatalog: %v", err)
	}
	return root
}

func TestValidateCatalogHappyPath(t *testing.T) {
	root := buildValidatingRepo(t)
	var out bytes.Buffer
	if err := validateCatalog(root, false, false, &out); err != nil {
		t.Fatalf("validateCatalog: %v (output: %s)", err, out.String())
	}
	if !strings.Contains(out.String(), "ok   org.sysc.timer") {
		t.Fatalf("expected an ok line for org.sysc.timer, got: %s", out.String())
	}
}

func TestValidateCatalogCatchesManifestDisagreement(t *testing.T) {
	root := buildValidatingRepo(t)
	// Change the manifest's name after the catalog row was built, so they
	// disagree the way a hand-edited manifest could.
	writeFile(t, filepath.Join(root, "plugins", "timer", "manifest.json"), `{
		"schema": 1, "id": "org.sysc.timer", "name": "Renamed Timer",
		"description": "A test plugin.", "version": "1.0.0",
		"exec": "bin/sysc-plugin-timer", "protocol": {"major": 1, "minor": 2},
		"capabilities": ["panels"], "requires": {"commands": []}
	}`)
	var out bytes.Buffer
	err := validateCatalog(root, false, false, &out)
	if err == nil {
		t.Fatalf("expected a validation failure, output: %s", out.String())
	}
	if !strings.Contains(out.String(), "FAIL org.sysc.timer") || !strings.Contains(out.String(), "name:") {
		t.Fatalf("expected a name mismatch failure, got: %s", out.String())
	}
}

func TestValidateCatalogCatchesMissingMetaEntry(t *testing.T) {
	root := buildValidatingRepo(t)
	writeFile(t, filepath.Join(root, catalogMetaFile), "{}")
	var out bytes.Buffer
	err := validateCatalog(root, false, false, &out)
	if err == nil {
		t.Fatalf("expected a validation failure, output: %s", out.String())
	}
	if !strings.Contains(out.String(), catalogMetaFile) {
		t.Fatalf("expected the failure to name %s, got: %s", catalogMetaFile, out.String())
	}
}

func TestValidateCommunityRequiresScreenshot(t *testing.T) {
	root := buildValidatingRepo(t)
	var out bytes.Buffer
	if err := validateCatalog(root, true, false, &out); err == nil {
		t.Fatalf("expected -community to require a screenshot, output: %s", out.String())
	}
	if !strings.Contains(out.String(), "screenshot") {
		t.Fatalf("expected a screenshot failure message, got: %s", out.String())
	}
}

func TestValidateCommunityPassesWithScreenshot(t *testing.T) {
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	writeFile(t, filepath.Join(root, catalogMetaFile), `{
		"org.sysc.timer": {"category": "productivity", "author": "Nomadcxx",
			"screenshot": {"url": "https://example.com/timer.png",
			               "sha256": "000000000000000000000000000000000000000000000000000000000000abcd"}}
	}`)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := validateCatalog(root, true, false, &out); err != nil {
		t.Fatalf("validateCatalog -community: %v (output: %s)", err, out.String())
	}
}

func TestValidateFetchCatchesSHAMismatch(t *testing.T) {
	assetBody := []byte("release-bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(assetBody)
	}))
	defer srv.Close()

	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	// Seed a normal release so its manifest/catalog-meta agree, then hand
	// -edit the catalog row's asset URL and sha to point at the httptest
	// server with a WRONG sha256, so -fetch must catch the mismatch.
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	wrongSHA := strings.Repeat("0", 64)
	rewriteCatalogAssetURL(t, root, srv.URL+"/asset.tar.gz", wrongSHA)

	var out bytes.Buffer
	err := validateCatalog(root, false, true, &out)
	if err == nil {
		t.Fatalf("expected -fetch to catch the sha256 mismatch, output: %s", out.String())
	}
	if !strings.Contains(out.String(), "sha256") {
		t.Fatalf("expected a sha256 mismatch message, got: %s", out.String())
	}
}

func TestValidateFetchPassesOnMatch(t *testing.T) {
	assetBody := []byte("release-bytes")
	sum := sha256.Sum256(assetBody)
	correctSHA := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(assetBody)
	}))
	defer srv.Close()

	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rewriteCatalogAssetURL(t, root, srv.URL+"/asset.tar.gz", correctSHA)

	var out bytes.Buffer
	if err := validateCatalog(root, false, true, &out); err != nil {
		t.Fatalf("validateCatalog -fetch: %v (output: %s)", err, out.String())
	}
}

func TestValidateFetchChecksReadmeSHAAndSize(t *testing.T) {
	readme := []byte("# Plugin README\n")
	sum := sha256.Sum256(readme)
	assetBody := []byte("release-bytes")
	assetSum := sha256.Sum256(assetBody)
	currentReadme := readme

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset.tar.gz" {
			_, _ = w.Write(assetBody)
			return
		}
		_, _ = w.Write(currentReadme)
	}))
	defer srv.Close()

	root := buildValidatingRepo(t)
	rewriteCatalogAssetURL(t, root, srv.URL+"/asset.tar.gz", hex.EncodeToString(assetSum[:]))
	writeCatalogReadme(t, root, srv.URL+"/README.md", hex.EncodeToString(sum[:]))

	var out bytes.Buffer
	if err := validateCatalog(root, false, true, &out); err != nil {
		t.Fatalf("validateCatalog -fetch: %v (output: %s)", err, out.String())
	}

	writeCatalogReadme(t, root, srv.URL+"/README.md", strings.Repeat("0", 64))
	out.Reset()
	if err := validateCatalog(root, false, true, &out); err == nil || !strings.Contains(out.String(), "readme") || !strings.Contains(out.String(), "sha256") {
		t.Fatalf("expected README sha256 failure, err=%v output=%s", err, out.String())
	}

	const maxReadmeBytes = 256 << 10
	currentReadme = bytes.Repeat([]byte("x"), maxReadmeBytes+1)
	largeSum := sha256.Sum256(currentReadme)
	writeCatalogReadme(t, root, srv.URL+"/README.md", hex.EncodeToString(largeSum[:]))
	out.Reset()
	if err := validateCatalog(root, false, true, &out); err == nil || !strings.Contains(out.String(), "readme") || !strings.Contains(out.String(), "larger than") {
		t.Fatalf("expected README size failure, err=%v output=%s", err, out.String())
	}
}

func writeCatalogReadme(t *testing.T, root, url, sum string) {
	t.Helper()
	path := filepath.Join(root, "catalog.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	plugins := doc["plugins"].([]any)
	plugins[0].(map[string]any)["readme"] = map[string]string{"url": url, "sha256": sum}
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// rewriteCatalogAssetURL patches catalog.json's single linux-amd64 asset to
// point at a test server, with a specific declared sha256, without going
// through the schema's https-only rule (the loopback http exception admits
// httptest URLs already).
func rewriteCatalogAssetURL(t *testing.T, root, url, sha256Hex string) {
	t.Helper()
	path := filepath.Join(root, "catalog.json")
	doc := fmt.Sprintf(`{
  "schema": 1,
  "plugins": [
    {
      "id": "org.sysc.timer",
      "name": "Pomodoro Timer",
      "author": "Nomadcxx",
      "description": "A test plugin.",
      "category": "productivity",
      "version": "1.0.0",
      "protocol": {"major": 1, "minor": 2},
      "capabilities": ["panels"],
      "requires": {"commands": []},
      "assets": {"linux-amd64": {"url": %q, "sha256": %q, "size": 13}}
    }
  ]
}
`, url, sha256Hex)
	writeFile(t, path, doc)
}
