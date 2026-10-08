package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// commitPattern is a full commit hash. A branch or tag name would move, and a
// pinned thumbnail must never change under its sha256.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func runThumbnails(args []string) error {
	fs := flag.NewFlagSet("thumbnails", flag.ContinueOnError)
	ref := fs.String("ref", "", "full 40-character commit hash that holds the thumbnails")
	repoFlag := fs.String("repo", "", "repository as owner/name (default $GITHUB_REPOSITORY, then "+defaultRepo+")")
	only := fs.String("plugin", "", "limit to one plugin directory; it must have a thumbnail at the commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := resolveRepo(*repoFlag)
	if err != nil {
		return fmt.Errorf("thumbnails: %w", err)
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	return pinThumbnails(repoRoot, repo, *ref, *only, os.Stderr)
}

// pinThumbnails points each catalog row's screenshot at plugins/<dir>/
// thumbnail.webp as it is at the commit ref. It is how rows released before
// thumbnails existed get one without a new version. Only screenshot changes.
// A row whose plugin has no thumbnail at ref is skipped with a note, unless
// only names it.
func pinThumbnails(repoRoot, repo, ref, only string, log io.Writer) error {
	if !commitPattern.MatchString(ref) {
		return fmt.Errorf("thumbnails: -ref must be a full 40-character commit hash, got %q", ref)
	}
	catalogPath := filepath.Join(repoRoot, "catalog.json")
	cat, err := decodeCatalogFile(catalogPath)
	if err != nil {
		return fmt.Errorf("thumbnails: %w", err)
	}
	dirs, err := pluginDirsByID(repoRoot)
	if err != nil {
		return fmt.Errorf("thumbnails: %w", err)
	}

	for i := range cat.Entries {
		e := &cat.Entries[i]
		dir, ok := dirs[e.ID]
		if !ok {
			return fmt.Errorf("thumbnails: no plugins/<dir> declares id %q", e.ID)
		}
		if only != "" && dir != only {
			continue
		}
		shot, found, err := fetchThumbnail(repo, ref, dir)
		if err != nil {
			return fmt.Errorf("thumbnails: %w", err)
		}
		if !found {
			if only != "" {
				return fmt.Errorf("thumbnails: plugins/%s/thumbnail.webp does not exist at %s", dir, ref)
			}
			fmt.Fprintf(log, "skip %s: no thumbnail at %s\n", e.ID, ref)
			continue
		}
		e.Screenshot = shot
	}
	return writeCatalog(catalogPath, cat.Entries)
}
