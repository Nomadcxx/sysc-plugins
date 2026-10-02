package moonbit

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// LastScan rolls the session cache up per category. A missing cache is not
// an error: there is simply no scan yet.
func LastScan() (*CacheInfo, error) {
	raw, err := os.ReadFile(CachePath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c struct {
		ScanResults *struct {
			Files []struct {
				Size         uint64 `json:"size"`
				CategoryName string `json:"category_name"`
			} `json:"files"`
		} `json:"scan_results"`
		TotalSize  uint64    `json:"total_size"`
		TotalFiles int       `json:"total_files"`
		ScannedAt  time.Time `json:"scanned_at"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	info := &CacheInfo{Files: c.TotalFiles, Bytes: c.TotalSize}
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
	return info, nil
}
