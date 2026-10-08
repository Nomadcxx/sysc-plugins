package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nomadcxx/sysc-shell/plugin/catalog"
)

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	community := fs.Bool("community", false, "require every row to carry a screenshot, as the community catalog does")
	fetch := fs.Bool("fetch", false, "download every asset, screenshot, and README and check size and sha256")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	return validateCatalog(repoRoot, *community, *fetch, os.Stdout)
}

// validateCatalog checks catalog.json the way the release workflow's
// `catalog update` step does, and the way CI does without -fetch: every row
// must decode and validate against the shared schema, have a
// catalog-meta.json entry, and agree with the immutable release-tag manifest
// on name, description, protocol, capabilities and requires. -community
// additionally requires a screenshot. -fetch downloads
// every asset, screenshot, and README named by the catalog and checks size
// and sha256, the release workflow's job before it opens the catalog PR.
func validateCatalog(repoRoot string, community, fetch bool, w io.Writer) error {
	catalogPath := filepath.Join(repoRoot, "catalog.json")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	cat, err := catalog.Decode(data)
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	failures := 0
	for _, rej := range cat.Rejected {
		fmt.Fprintf(w, "FAIL row %d (%s): %v\n", rej.Index, rej.ID, rej.Err)
		failures++
	}

	metaAll, err := readCatalogMeta(filepath.Join(repoRoot, catalogMetaFile))
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	if err := checkMetaCategories(metaAll); err != nil {
		fmt.Fprintf(w, "FAIL %v\n", err)
		failures++
	}
	dirsByID, err := pluginDirsByID(repoRoot)
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	for _, e := range cat.Entries {
		if err := validateEntry(e, metaAll, dirsByID, community); err != nil {
			fmt.Fprintf(w, "FAIL %s: %v\n", e.ID, err)
			failures++
			continue
		}
		fmt.Fprintf(w, "ok   %s\n", e.ID)
	}

	if fetch {
		for _, e := range cat.Entries {
			for _, err := range fetchEntry(e) {
				fmt.Fprintf(w, "FAIL %s: %v\n", e.ID, err)
				failures++
			}
		}
	}

	if failures > 0 {
		return fmt.Errorf("validate: %d failure(s)", failures)
	}
	return nil
}

// validateEntry checks one decoded, already-schema-valid catalog row against
// catalog-meta.json and its published release-tag manifest.
func validateEntry(e catalog.Entry, metaAll map[string]catalogMeta, dirsByID map[string]string, community bool) error {
	if _, ok := metaAll[e.ID]; !ok {
		return fmt.Errorf("no %s entry for %q", catalogMetaFile, e.ID)
	}
	dir, ok := dirsByID[e.ID]
	if !ok {
		return fmt.Errorf("no plugins/<dir> declares id %q", e.ID)
	}
	// The embedded release is the newest by contract; Releases hold the older
	// versions for hosts below it, unique and strictly oldest-last.
	prev := e.Release.Version
	seen := map[string]bool{prev: true}
	for i, r := range e.Releases {
		if seen[r.Version] {
			return fmt.Errorf("releases[%d] repeats version %s", i, r.Version)
		}
		if !catalog.Newer(prev, r.Version) {
			return fmt.Errorf("releases[%d] %s is not older than %s", i, r.Version, prev)
		}
		seen[r.Version], prev = true, r.Version
	}

	repo, err := repoFromAssets(dir, e)
	if err != nil {
		return err
	}
	// The working tree can contain edits made after the release. Always check
	// the immutable tag so coordinated edits to the checkout and catalog cannot
	// make unpublished metadata appear valid. The tag is read from the
	// repository the row's assets come from, not from our own environment.
	tagged, err := readTaggedManifestURL(repo, dir, e.Release.Version, e.ID)
	if err != nil {
		return fmt.Errorf("cannot check tagged release manifest: %w", err)
	}
	if mismatches := manifestMismatches(tagged, e); len(mismatches) > 0 {
		return fmt.Errorf("tagged release manifest: %s", strings.Join(mismatches, "; "))
	}

	if community && e.Screenshot == nil {
		return fmt.Errorf("no screenshot; required for the community catalog")
	}
	return nil
}

// repoFromAssets derives the repository a row's assets come from. GitHub
// asset URLs must be release downloads for <dir>-v<version> in one
// repository; the loopback URLs the schema admits for tests carry no
// repository, so a row without a GitHub asset falls back to defaultRepo.
func repoFromAssets(dir string, e catalog.Entry) (string, error) {
	wantTag := dir + "-v" + e.Release.Version
	repo := ""
	for arch, a := range e.Assets {
		u, err := url.Parse(a.URL)
		if err != nil {
			return "", fmt.Errorf("asset %s: %w", arch, err)
		}
		if u.Host != "github.com" {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" || parts[4] != wantTag {
			return "", fmt.Errorf("asset %s: %s is not a GitHub release download for %s", arch, a.URL, wantTag)
		}
		if r := parts[0] + "/" + parts[1]; repo == "" {
			repo = r
		} else if r != repo {
			return "", fmt.Errorf("asset %s: %s names repository %s, other assets name %s", arch, a.URL, r, repo)
		}
	}
	if repo == "" {
		repo = defaultRepo
	}
	return repo, nil
}

func manifestMismatches(m pluginManifest, e catalog.Entry) []string {
	var mismatches []string
	if m.Name != e.Name {
		mismatches = append(mismatches, fmt.Sprintf("name: manifest %q, catalog %q", m.Name, e.Name))
	}
	if m.Description != e.Description {
		mismatches = append(mismatches, fmt.Sprintf("description: manifest %q, catalog %q", m.Description, e.Description))
	}
	if m.Protocol.Major != e.Protocol.Major || m.Protocol.Minor != e.Protocol.Minor {
		mismatches = append(mismatches, fmt.Sprintf("protocol: manifest %d.%d, catalog %d.%d",
			m.Protocol.Major, m.Protocol.Minor, e.Protocol.Major, e.Protocol.Minor))
	}
	if !catalog.SameSet(m.Capabilities, e.Capabilities) {
		mismatches = append(mismatches, fmt.Sprintf("capabilities: manifest %v, catalog %v", m.Capabilities, e.Capabilities))
	}
	if !catalog.SameSet(m.Requires.Commands, e.Requires.Commands) {
		mismatches = append(mismatches, fmt.Sprintf("requires.commands: manifest %v, catalog %v", m.Requires.Commands, e.Requires.Commands))
	}
	return mismatches
}

// fetchEntry downloads every asset and screenshot a row names, across its
// top-level release and every kept older one, and reports a size or sha256
// mismatch against what the catalog declares.
func fetchEntry(e catalog.Entry) []error {
	var errs []error
	check := func(label, url, wantSHA string, wantSize, maxBytes int64) {
		if err := checkFetchable(url, wantSHA, wantSize, maxBytes); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", label, err))
		}
	}
	releases := append([]catalog.Release{e.Release}, e.Releases...)
	for _, r := range releases {
		for key, a := range r.Assets {
			check(fmt.Sprintf("%s assets[%s]", r.Version, key), a.URL, a.SHA256, a.Size, catalog.MaxAssetBytes)
		}
	}
	if e.Screenshot != nil {
		check("screenshot", e.Screenshot.URL, e.Screenshot.SHA256, 0, catalog.MaxAssetBytes)
	}
	if e.Readme != nil {
		check("readme", e.Readme.URL, e.Readme.SHA256, 0, catalog.MaxReadmeBytes)
	}
	return errs
}

// checkFetchable downloads url, capped at the catalog's own asset ceiling,
// and compares its size (when wantSize is positive) and sha256 against what
// the catalog declares.
func checkFetchable(url, wantSHA string, wantSize, maxBytes int64) error {
	resp, err := catalogHTTPClient.Get(url)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch: status %s", resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if n > maxBytes {
		return fmt.Errorf("larger than %d bytes", maxBytes)
	}
	if wantSize > 0 && n != wantSize {
		return fmt.Errorf("size %d, catalog says %d", n, wantSize)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != wantSHA {
		return fmt.Errorf("sha256 %s, catalog says %s", sum, wantSHA)
	}
	return nil
}
