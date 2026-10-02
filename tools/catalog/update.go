package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/catalog"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// tagPattern splits a release tag into its plugin directory and version. Tags
// are per plugin (Owner decisions): "<dir>-v<version>", e.g. "timer-v1.4.0".
// Directory names may themselves contain hyphens (github-notifications,
// mini-docker, wallpaper-depth, world-clock, screen-recorder), so the split
// point is the last "-v" immediately followed by a full MAJOR.MINOR.PATCH.
var tagPattern = regexp.MustCompile(`^(.+)-v(\d+\.\d+\.\d+)$`)

// releasesCap is the number of prior releases catalog.json keeps for a
// plugin, per the Controller ruling.
const releasesCap = 5

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	tag := fs.String("tag", "", "release tag, <dir>-v<version>")
	dist := fs.String("dist", "", "directory holding the built release archives")
	nowFlag := fs.String("now", "", "RFC3339 timestamp to use instead of the current time")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" || *dist == "" {
		return fmt.Errorf("update: -tag and -dist are both required")
	}
	now := time.Now().UTC()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return fmt.Errorf("update: -now %q: %w", *nowFlag, err)
		}
		now = t.UTC()
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	return updateCatalog(repoRoot, *tag, *dist, now)
}

// updateCatalog rewrites catalog.json for one tagged release: it resolves
// the release's assets from dist, merges the row into the existing catalog
// (creating one if this is the plugin's first release), and writes the
// result back sorted by id.
func updateCatalog(repoRoot, tag, dist string, now time.Time) error {
	m := tagPattern.FindStringSubmatch(tag)
	if m == nil {
		return fmt.Errorf("update: tag %q is not <dir>-v<version>", tag)
	}
	pluginDir, tagVersion := m[1], m[2]

	identity, err := readManifest(filepath.Join(repoRoot, "plugins", pluginDir))
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	manifest, err := readTaggedManifest(dist, pluginDir, tagVersion, identity.ID)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if manifest.Version != tagVersion {
		return fmt.Errorf("update: tag %q names version %q, but plugins/%s/manifest.json has %q",
			tag, tagVersion, pluginDir, manifest.Version)
	}

	assets, err := scanAssets(dist, manifest.ID, manifest.Version, tag)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	readme, err := readPluginReadme(pluginDir, tag)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	metaPath := filepath.Join(repoRoot, catalogMetaFile)
	metaAll, err := readCatalogMeta(metaPath)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if err := checkMetaCategories(metaAll); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	meta, ok := metaAll[manifest.ID]
	if !ok {
		return fmt.Errorf("update: %s has no entry for %q", catalogMetaFile, manifest.ID)
	}

	catalogPath := filepath.Join(repoRoot, "catalog.json")
	cat, err := decodeCatalogFile(catalogPath)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	newRelease := catalog.Release{
		Version:      manifest.Version,
		Protocol:     v1.Version{Major: manifest.Protocol.Major, Minor: manifest.Protocol.Minor},
		Capabilities: manifest.Capabilities,
		Requires:     catalog.Requires{Commands: manifest.Requires.Commands},
		Assets:       assets,
		ReleaseNotes: fmt.Sprintf("https://github.com/Nomadcxx/sysc-plugins/releases/tag/%s", tag),
	}

	var existing *catalog.Entry
	entries := make([]catalog.Entry, 0, len(cat.Entries))
	for _, e := range cat.Entries {
		if e.ID == manifest.ID {
			row := e
			existing = &row
			continue
		}
		entries = append(entries, e)
	}
	entries = append(entries, mergeRelease(existing, newRelease, now, meta, manifest, readme))

	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return writeCatalog(catalogPath, entries)
}

// mergeRelease produces the catalog row for one plugin after a new release.
// A nil existing row starts a fresh one; otherwise the previous top-level
// release moves to the front of Releases (capped at releasesCap), unless the
// new release is the same version being re-published, in which case it
// replaces the old top-level release rather than duplicating it there.
func mergeRelease(existing *catalog.Entry, newRelease catalog.Release, now time.Time, meta catalogMeta, manifest pluginManifest, readme *catalog.Screenshot) catalog.Entry {
	if existing == nil {
		return catalog.Entry{
			ID:              manifest.ID,
			Name:            manifest.Name,
			Author:          meta.Author,
			Description:     manifest.Description,
			LongDescription: meta.LongDescription,
			Category:        meta.Category,
			License:         meta.License,
			Homepage:        meta.Homepage,
			Screenshot:      meta.Screenshot.toCatalog(),
			Readme:          readme,
			AddedAt:         now,
			UpdatedAt:       now,
			Release:         newRelease,
		}
	}

	e := *existing
	releases := slices.Clone(e.Releases)
	if e.Release.Version != newRelease.Version {
		releases = append([]catalog.Release{e.Release}, releases...)
	}
	filtered := releases[:0]
	for _, r := range releases {
		if r.Version != newRelease.Version {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) > releasesCap {
		filtered = filtered[:releasesCap]
	}

	e.Releases = filtered
	e.Release = newRelease
	e.UpdatedAt = now
	// Listing fields are refreshed from the archive and catalog-meta.json on
	// every update, so they never drift from what actually shipped.
	e.Name = manifest.Name
	e.Description = manifest.Description
	e.Author = meta.Author
	e.Category = meta.Category
	e.License = meta.License
	e.Homepage = meta.Homepage
	e.LongDescription = meta.LongDescription
	e.Readme = readme
	if meta.Screenshot != nil {
		e.Screenshot = meta.Screenshot.toCatalog()
	}
	return e
}

// taggedFileBaseURL is the raw host for content pinned to release tags. Tests
// replace it with an in-memory transport.
var taggedFileBaseURL = "https://raw.githubusercontent.com"

var catalogHTTPClient = &http.Client{Timeout: 45 * time.Second}

// readPluginReadme returns a tag-pinned URL and hash for the plugin README,
// when the tagged tree includes one. The hash and presence both come from
// the tagged URL, so the catalog describes what shipped.
func readPluginReadme(pluginDir, tag string) (*catalog.Screenshot, error) {
	url := fmt.Sprintf("%s/Nomadcxx/sysc-plugins/%s/plugins/%s/README.md", taggedFileBaseURL, tag, pluginDir)
	resp, err := catalogHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("readme: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("readme %s: status %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, catalog.MaxReadmeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("readme %s: %w", url, err)
	}
	if int64(len(data)) > catalog.MaxReadmeBytes {
		return nil, fmt.Errorf("plugins/%s/README.md is larger than %d bytes", pluginDir, catalog.MaxReadmeBytes)
	}
	sum := sha256.Sum256(data)
	return &catalog.Screenshot{URL: url, SHA256: hex.EncodeToString(sum[:])}, nil
}

// readTaggedManifestURL fetches the manifest from the published release tag.
func readTaggedManifestURL(pluginDir, version, id string) (pluginManifest, error) {
	tag := pluginDir + "-v" + version
	url := fmt.Sprintf("%s/Nomadcxx/sysc-plugins/%s/plugins/%s/manifest.json", taggedFileBaseURL, tag, pluginDir)
	resp, err := catalogHTTPClient.Get(url)
	if err != nil {
		return pluginManifest{}, fmt.Errorf("manifest %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pluginManifest{}, fmt.Errorf("manifest %s: status %s", url, resp.Status)
	}
	const maxManifestBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return pluginManifest{}, fmt.Errorf("manifest %s: %w", url, err)
	}
	if int64(len(data)) > maxManifestBytes {
		return pluginManifest{}, fmt.Errorf("manifest %s is larger than %d bytes", url, maxManifestBytes)
	}
	m, err := decodeManifest(data, url)
	if err != nil {
		return pluginManifest{}, err
	}
	if m.ID != id || m.Version != version || m.Exec != path.Join("bin", "sysc-plugin-"+pluginDir) {
		return pluginManifest{}, fmt.Errorf("manifest %s does not match plugin %q at version %q", url, id, version)
	}
	return m, nil
}

// readTaggedManifest reads the manifest embedded in every release archive for
// this plugin. All architectures for one tag must carry identical manifest bytes.
func readTaggedManifest(dist, pluginDir, tagVersion, id string) (pluginManifest, error) {
	pattern := filepath.Join(dist, fmt.Sprintf("%s-%s-linux-*.tar.gz", id, tagVersion))
	archives, err := filepath.Glob(pattern)
	if err != nil {
		return pluginManifest{}, err
	}
	if len(archives) == 0 {
		return pluginManifest{}, fmt.Errorf("no archives matching %s", filepath.Base(pattern))
	}
	sort.Strings(archives)
	wantExec := path.Join("bin", "sysc-plugin-"+pluginDir)
	var tagged pluginManifest
	var taggedBytes []byte
	for _, archive := range archives {
		manifest, data, err := readArchiveManifest(archive)
		if err != nil {
			return pluginManifest{}, fmt.Errorf("%s: %w", archive, err)
		}
		if manifest.ID != id {
			return pluginManifest{}, fmt.Errorf("%s manifest id %q, want %q", archive, manifest.ID, id)
		}
		if manifest.Version != tagVersion {
			return pluginManifest{}, fmt.Errorf("tag %q names version %q, but archive manifest has %q",
				pluginDir+"-v"+tagVersion, tagVersion, manifest.Version)
		}
		if manifest.Exec != wantExec {
			return pluginManifest{}, fmt.Errorf("%s manifest exec %q, want %q", archive, manifest.Exec, wantExec)
		}
		if taggedBytes != nil && !bytes.Equal(data, taggedBytes) {
			return pluginManifest{}, fmt.Errorf("%s archives contain different manifests", pluginDir+"-v"+tagVersion)
		}
		tagged, taggedBytes = manifest, data
	}
	if taggedBytes == nil {
		return pluginManifest{}, fmt.Errorf("no release archive manifest for plugin %q", pluginDir)
	}
	return tagged, nil
}

func readArchiveManifest(archive string) (pluginManifest, []byte, error) {
	f, err := os.Open(archive)
	if err != nil {
		return pluginManifest{}, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return pluginManifest{}, nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return pluginManifest{}, nil, err
		}
		name := path.Clean(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || path.Dir(name) == "." || path.Base(name) != "manifest.json" {
			continue
		}
		const maxManifestBytes = 1 << 20
		if hdr.Size < 0 || hdr.Size > maxManifestBytes {
			return pluginManifest{}, nil, fmt.Errorf("manifest.json has invalid size %d", hdr.Size)
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes+1))
		if err != nil {
			return pluginManifest{}, nil, err
		}
		if len(data) > maxManifestBytes {
			return pluginManifest{}, nil, fmt.Errorf("manifest.json exceeds %d bytes", maxManifestBytes)
		}
		var manifest pluginManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return pluginManifest{}, nil, fmt.Errorf("parse manifest.json: %w", err)
		}
		if manifest.ID == "" || manifest.Version == "" || manifest.Exec == "" || path.Dir(name) != manifest.ID {
			return pluginManifest{}, nil, fmt.Errorf("manifest.json has missing fields or does not match archive root %q", path.Dir(name))
		}
		return manifest, data, nil
	}
	return pluginManifest{}, nil, fmt.Errorf("archive has no top-level manifest.json")
}

// checkMetaCategories fails when catalog-meta.json names a category outside
// the shell's closed set; catalog.Entry.Validate would silently remap it to
// "other" instead.
func checkMetaCategories(metaAll map[string]catalogMeta) error {
	for id, meta := range metaAll {
		if !slices.Contains(catalog.Categories, meta.Category) {
			return fmt.Errorf("%s: %s category %q is not one of %v", catalogMetaFile, id, meta.Category, catalog.Categories)
		}
	}
	return nil
}

// scanAssets finds <id>-<version>-linux-<arch>.tar.gz files in dist and
// returns them keyed "linux-<arch>", with the download URL Global
// Constraints define, the file's size, and its sha256.
func scanAssets(dist, id, version, tag string) (map[string]catalog.Asset, error) {
	pattern := filepath.Join(dist, fmt.Sprintf("%s-%s-linux-*.tar.gz", id, version))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no archives matching %s in %s", filepath.Base(pattern), dist)
	}
	prefix := fmt.Sprintf("%s-%s-linux-", id, version)
	assets := make(map[string]catalog.Asset, len(matches))
	for _, path := range matches {
		base := filepath.Base(path)
		arch := base[len(prefix) : len(base)-len(".tar.gz")]
		size, sum, err := hashFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		assets["linux-"+arch] = catalog.Asset{
			URL:    fmt.Sprintf("https://github.com/Nomadcxx/sysc-plugins/releases/download/%s/%s", tag, base),
			SHA256: sum,
			Size:   size,
		}
	}
	return assets, nil
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// catalogDoc is the on-disk shape of catalog.json.
type catalogDoc struct {
	Schema  int             `json:"schema"`
	Plugins []catalog.Entry `json:"plugins"`
}

// decodeCatalogFile reads and validates the catalog at path. A missing file
// is not an error: it reads as an empty catalog, so `update` can run before
// catalog.json exists.
func decodeCatalogFile(path string) (catalog.Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return catalog.Catalog{}, nil
		}
		return catalog.Catalog{}, err
	}
	cat, err := catalog.Decode(data)
	if err != nil {
		return catalog.Catalog{}, err
	}
	if len(cat.Rejected) > 0 {
		return catalog.Catalog{}, fmt.Errorf("%s already has %d rejected row(s); fix them before updating: %v",
			path, len(cat.Rejected), cat.Rejected[0].Err)
	}
	return cat, nil
}

// writeCatalog writes entries (already sorted by id) to path with two-space
// indentation, then decodes the result back and refuses to write a catalog
// that would reject any row.
func writeCatalog(path string, entries []catalog.Entry) error {
	doc := catalogDoc{Schema: catalog.Schema, Plugins: entries}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	cat, err := catalog.Decode(data)
	if err != nil {
		return fmt.Errorf("regenerated catalog does not decode: %w", err)
	}
	if len(cat.Rejected) > 0 {
		return fmt.Errorf("regenerated catalog rejects %d row(s): %v", len(cat.Rejected), cat.Rejected[0].Err)
	}
	return os.WriteFile(path, data, 0o644)
}
