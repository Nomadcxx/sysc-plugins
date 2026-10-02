package moonbit

import "encoding/json"

// Event is one NDJSON line from the moonbit daemon panel socket. The union is
// flat: every moonbit event kind decodes into it, and the fold switches on T.
// Categories is raw because moonbit reuses the key for a count (done) and a
// rollup list (clean_begin, status cache).
type Event struct {
	T          string          `json:"t"`
	Name       string          `json:"name,omitempty"`
	I          int             `json:"i,omitempty"`
	Total      int             `json:"total,omitempty"`
	Files      int             `json:"files,omitempty"`
	Bytes      uint64          `json:"bytes,omitempty"`
	Dir        string          `json:"dir,omitempty"`
	Duration   int64           `json:"duration_ms,omitempty"`
	Msg        string          `json:"msg,omitempty"`
	DryRun     bool            `json:"dry_run,omitempty"`
	Done       int             `json:"done,omitempty"`
	Deleted    int             `json:"deleted,omitempty"`
	Freed      uint64          `json:"freed,omitempty"`
	File       string          `json:"file,omitempty"`
	Errors     []string        `json:"errors,omitempty"`
	Categories json.RawMessage `json:"categories,omitempty"`
	Cache      *CacheInfo      `json:"cache,omitempty"`
	Daemon     bool            `json:"daemon,omitempty"`
	LastScan   string          `json:"last_scan,omitempty"`
	LastClean  string          `json:"last_clean,omitempty"`
	// ScannedAt stamps a scan's done event (moonbit 1.6+); a clean sends it
	// back so the daemon refuses a cache a later scan replaced.
	ScannedAt string `json:"scanned_at,omitempty"`
	// Truncate marks a category_done whose files are truncated in place
	// rather than deleted (moonbit 1.6+).
	Truncate bool `json:"truncate,omitempty"`
}

// CategoryStat is moonbit's per-category rollup.
type CategoryStat struct {
	Name     string `json:"name"`
	Files    int    `json:"files"`
	Bytes    uint64 `json:"bytes"`
	Truncate bool   `json:"truncate,omitempty"`
}

// CacheInfo is the status event's view of the daemon's session cache.
type CacheInfo struct {
	Files      int            `json:"files"`
	Bytes      uint64         `json:"bytes"`
	ScannedAt  string         `json:"scanned_at"`
	Categories []CategoryStat `json:"categories,omitempty"`
}

func categoryRollup(raw json.RawMessage) []CategoryStat {
	var cats []CategoryStat
	_ = json.Unmarshal(raw, &cats)
	return cats
}
