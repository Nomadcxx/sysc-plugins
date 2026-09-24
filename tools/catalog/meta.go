package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Nomadcxx/sysc-shell/plugin/catalog"
)

// catalogMetaFile is the name of the listing-metadata file at the repo root.
const catalogMetaFile = "catalog-meta.json"

// catalogMeta is one catalog-meta.json row: the listing fields a catalog
// entry needs that do not live in the plugin's own manifest.json (Controller
// ruling in docs/plans/2026-09-25-plugin-release-pipeline.md). The manifest
// decoder is strict and must not grow to carry these.
type catalogMeta struct {
	Category        string          `json:"category"`
	Author          string          `json:"author"`
	License         string          `json:"license,omitempty"`
	Homepage        string          `json:"homepage,omitempty"`
	LongDescription string          `json:"long_description,omitempty"`
	Screenshot      *metaScreenshot `json:"screenshot,omitempty"`
}

type metaScreenshot struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

func (s *metaScreenshot) toCatalog() *catalog.Screenshot {
	if s == nil {
		return nil
	}
	return &catalog.Screenshot{URL: s.URL, SHA256: s.SHA256}
}

func readCatalogMeta(path string) (map[string]catalogMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]catalogMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}
