package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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

	manifest, err := readManifest(filepath.Join(repoRoot, "plugins", pluginDir))
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
	readme, err := readPluginReadme(repoRoot, pluginDir, tag)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	metaPath := filepath.Join(repoRoot, catalogMetaFile)
	metaAll, err := readCatalogMeta(metaPath)
	if err != nil {
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

// readPluginReadme returns a tag-pinned URL and hash for the plugin README,
// when the tagged tree includes one.
func readPluginReadme(repoRoot, pluginDir, tag string) (*catalog.Screenshot, error) {
	path := filepath.Join(repoRoot, "plugins", pluginDir, "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if int64(len(data)) > catalog.MaxReadmeBytes {
		return nil, fmt.Errorf("plugins/%s/README.md is larger than %d bytes", pluginDir, catalog.MaxReadmeBytes)
	}
	sum := sha256.Sum256(data)
	return &catalog.Screenshot{
		URL:    fmt.Sprintf("https://raw.githubusercontent.com/Nomadcxx/sysc-plugins/%s/plugins/%s/README.md", tag, pluginDir),
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
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
