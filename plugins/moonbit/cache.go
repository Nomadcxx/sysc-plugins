package moonbit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CachePath is where `sudo moonbit` (the TUI, and the panel's runs) keeps the
// last scan: the invoking user's ~/.cache, chowned back to them so a panel
// can read it without a password. sudo resets the environment, so moonbit
// resolves the home from SUDO_USER and ignores XDG_CACHE_HOME.
// SYSC_MOONBIT_CACHE overrides it (tests).
func CachePath() string {
	if p := os.Getenv("SYSC_MOONBIT_CACHE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "moonbit", "scan_results.json")
}

// SummaryPath is the per-category rollup moonbit 1.7.1+ saves beside the
// cache. A deep scan's cache runs past 100 MB; the summary is a few hundred
// bytes.
func SummaryPath() string {
	return filepath.Join(filepath.Dir(CachePath()), "scan_summary.json")
}

// scanFile is the part of the cache and the summary the panel reads; the
// cache carries per-file entries, the summary carries categories.
type scanFile struct {
	ScanResults *struct {
		Files []struct {
			Size         uint64 `json:"size"`
			CategoryName string `json:"category_name"`
		} `json:"files"`
	} `json:"scan_results"`
	Categories []CategoryStat `json:"categories"`
	TotalSize  uint64         `json:"total_size"`
	TotalFiles int            `json:"total_files"`
	ScannedAt  time.Time      `json:"scanned_at"`
}

// lastScan remembers the last reading so a refresh only parses again when
// the file changed.
var lastScan struct {
	sync.Mutex
	path string
	mod  time.Time
	size int64
	info *CacheInfo
}

// LastScan reads the last scan, from the summary when moonbit wrote one and
// from the full cache otherwise. A missing file is not an error: there is
// simply no scan yet.
func LastScan() (*CacheInfo, error) {
	path := SummaryPath()
	st, err := os.Stat(path)
	if err != nil {
		path = CachePath()
		if st, err = os.Stat(path); os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	lastScan.Lock()
	defer lastScan.Unlock()
	if lastScan.path == path && lastScan.mod.Equal(st.ModTime()) && lastScan.size == st.Size() {
		return lastScan.info, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c scanFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	info := &CacheInfo{Files: c.TotalFiles, Bytes: c.TotalSize, Categories: c.Categories}
	if !c.ScannedAt.IsZero() {
		info.ScannedAt = c.ScannedAt.Format(time.RFC3339Nano)
	}
	if c.ScanResults != nil {
		index := map[string]int{}
		for _, f := range c.ScanResults.Files {
			i, ok := index[f.CategoryName]
			if !ok {
				i = len(info.Categories)
				index[f.CategoryName] = i
				info.Categories = append(info.Categories, CategoryStat{Name: f.CategoryName})
			}
			info.Categories[i].Files++
			info.Categories[i].Bytes += f.Size
		}
	}
	lastScan.path, lastScan.mod, lastScan.size, lastScan.info = path, st.ModTime(), st.Size(), info
	return info, nil
}
